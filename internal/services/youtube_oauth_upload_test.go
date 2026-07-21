package services

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestYouTubeUpload_ChunkBoundary computes the chunk span correctly:
// a file of 200 bytes with a 64-byte chunkSize yields 4 PUTs in
// strict Content-Range order (64+64+64+8). Asserts each chunk's
// Content-Range header reflects the running byte offset so the
// resumable-upload contract is preserved.
func TestYouTubeUpload_ChunkBoundary(t *testing.T) {
	var gotRanges []string
	mux := http.NewServeMux()
	mux.HandleFunc("/upload", func(w http.ResponseWriter, r *http.Request) {
		gotRanges = append(gotRanges, r.Header.Get("Content-Range"))
		// Always 308 — server expects more bytes, no final 200.
		w.WriteHeader(http.StatusPermanentRedirect)
	})
	mux.HandleFunc("/source", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("Content-Length", "200")
		_, _ = w.Write(bytes.Repeat([]byte{0xAB}, 200))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc := youtubeTestSvcUpload(srv)

	_, err := svc.uploadVideoChunks(context.Background(), srv.URL+"/upload", srv.URL+"/source", 200)
	if err == nil {
		t.Fatalf("expected end-of-stream error, got nil")
	}
	if !strings.Contains(err.Error(), "no video ID") {
		t.Errorf("expected end-of-stream error, got: %v", err)
	}
	want := []string{
		"bytes 0-63/200",
		"bytes 64-127/200",
		"bytes 128-191/200",
		"bytes 192-199/200",
	}
	if len(gotRanges) != len(want) {
		t.Fatalf("chunk count: want %d PUTs, got %d (%v)", len(want), len(gotRanges), gotRanges)
	}
	for i, w := range want {
		if gotRanges[i] != w {
			t.Errorf("chunk %d Content-Range: want %q, got %q", i, w, gotRanges[i])
		}
	}
}

// TestYouTubeUpload_PutChunk_5xx_Retryable_RetryAfter verifies the
// 5xx-with-Retry-After path: putChunk returns retryable=true and
// retryAfter=parsed_value so the uploadVideoChunks loop sleeps for
// the server's hint rather than the calculated fallback.
func TestYouTubeUpload_PutChunk_5xx_Retryable_RetryAfter(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/upload", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "7")
		http.Error(w, "transient", http.StatusServiceUnavailable)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc := youtubeTestSvcUpload(srv)

	_, retryAfter, retryable, err := svc.putChunk(context.Background(), srv.URL+"/upload", []byte{0xAB}, "bytes 0-0/1", 1)
	if err == nil {
		t.Fatal("expected error on 503, got nil")
	}
	if !retryable {
		t.Errorf("503 should be retryable")
	}
	if retryAfter != 7*time.Second {
		t.Errorf("retryAfter: want 7s (parsed from Retry-After), got %s", retryAfter)
	}
}

// TestYouTubeUpload_PutChunk_429_RetryAfter verifies that 429 is
// treated as retryable with the parsed Retry-After honored as the
// retry hint. RFC 6585 / Google API conventions.
func TestYouTubeUpload_PutChunk_429_RetryAfter(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/upload", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "3")
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc := youtubeTestSvcUpload(srv)

	_, retryAfter, retryable, err := svc.putChunk(context.Background(), srv.URL+"/upload", []byte{0xAB}, "bytes 0-0/1", 1)
	if err == nil {
		t.Fatal("expected error on 429, got nil")
	}
	if !retryable {
		t.Errorf("429 should be retryable")
	}
	if retryAfter != 3*time.Second {
		t.Errorf("retryAfter: want 3s, got %s", retryAfter)
	}
}

// TestYouTubeUpload_PutChunk_4xx_NotRetried verifies the design
// decision that 4xx (other than 429) is permanent: bubble up
// immediately so the upload-job worker MarkDeadLetter on attempt 1
// instead of wasting the chunk budget on a row YouTube will reject
// forever. Google's docs are clear: bad metadata, body validation
// errors, etc. will not fix themselves on retry.
func TestYouTubeUpload_PutChunk_4xx_NotRetried(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/upload", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad metadata", http.StatusBadRequest)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc := youtubeTestSvcUpload(srv)

	_, _, retryable, err := svc.putChunk(context.Background(), srv.URL+"/upload", []byte{0xAB}, "bytes 0-0/1", 1)
	if err == nil {
		t.Fatal("expected error on 400, got nil")
	}
	if retryable {
		t.Errorf("400 must NOT be retryable (bubble up to MarkDeadLetter)")
	}
}

// TestYouTubeUpload_PutChunk_NetworkError_Retryable verifies that a
// transport-level error (DNS, TCP reset) is treated as retryable so
// the uploadVideoChunks loop can resume from queryUploadStatus.
func TestYouTubeUpload_PutChunk_NetworkError_Retryable(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/upload", func(w http.ResponseWriter, r *http.Request) {
		// Hijack + close to force a transport-level RST.
		conn, _, _ := w.(http.Hijacker).Hijack()
		_ = conn.Close()
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc := youtubeTestSvcUpload(srv)

	_, _, retryable, err := svc.putChunk(context.Background(), srv.URL+"/upload", []byte{0xAB}, "bytes 0-0/1", 1)
	if err == nil {
		t.Fatal("expected error on connection reset, got nil")
	}
	if !retryable {
		t.Errorf("transport error should be retryable, got retryable=false")
	}
}

// TestYouTubeUpload_MaxRetriesExceeded verifies the chunk-retry
// budget: with MaxRetries=3, uploadVideoChunks issues 4 chunk PUTs
// (initial + 3 retries) before bubbling up the error. Without this
// cap, an outage would loop forever.
//
// Discrimination: the mux path /upload handles both chunk PUTs
// (Content-Range "bytes X-Y/TOTAL") AND the recovery phase's
// queryUploadStatusWithRetry PUTs (Content-Range "bytes */TOTAL").
// We inspect the header so we only count chunk PUTs — keeping the
// assertion crisp even though the recovery phase issues its own PUTs
// to the same path. The query path returns 308 with no Range header
// so the resume point is byte 0 (recovery re-GETs the full source,
// simulating "no bytes streamed yet"); chunk PUTs always return 503
// to drive the retry path.
func TestYouTubeUpload_MaxRetriesExceeded(t *testing.T) {
	var chunkPutCount int
	mux := http.NewServeMux()
	mux.HandleFunc("/upload", func(w http.ResponseWriter, r *http.Request) {
		cr := r.Header.Get("Content-Range")
		if strings.HasPrefix(cr, "bytes *") {
			// queryUploadStatusWithRetry: succeed with 308 + no
			// Range header so the resume point is byte 0. This
			// isolates the chunk-retry-counting assertion from
			// any errors in the recovery helper itself.
			w.WriteHeader(http.StatusPermanentRedirect)
			return
		}
		chunkPutCount++
		http.Error(w, "still down", http.StatusServiceUnavailable)
	})
	mux.HandleFunc("/source", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("Content-Length", "10")
		_, _ = w.Write(bytes.Repeat([]byte{0}, 10))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc := youtubeTestSvcUpload(srv)

	_, err := svc.uploadVideoChunks(context.Background(), srv.URL+"/upload", srv.URL+"/source", 10)
	if err == nil {
		t.Fatal("expected MaxRetries error, got nil")
	}
	if chunkPutCount != 4 {
		t.Errorf("chunk PUT count: want 4 (initial + 3 retries), got %d", chunkPutCount)
	}
}

// TestYouTubeUpload_SleepCtxInterruptible verifies the canonical
// shutdown-safe sleep shape: a 1-hour pending sleep returns within
// milliseconds when the ctx is cancelled mid-flight. time.Sleep would
// block past cancellation and break the worker's drain-then-stop
// contract — this is precisely why defaultYouTubeSleep uses
// time.NewTimer + select on ctx.Done().
func TestYouTubeUpload_SleepCtxInterruptible(t *testing.T) {
	svc := youtubeTestSvcUpload(httptest.NewServer(http.NewServeMux()))
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	err := svc.uploadDeps.sleep(ctx, time.Hour)
	if err == nil {
		t.Fatal("expected ctx error after cancel, got nil")
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Errorf("sleep did not interrupt cleanly (elapsed: %s)", elapsed)
	}
}

// TestParseRetryAfterHeader covers the RFC 7231 §7.1.3 parsing
// matrix: delta-seconds (the common case), HTTP-date (deprecated
// but seen in the wild), empty, garbage, and the past-date clamp.
func TestParseRetryAfterHeader(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		if got := parseRetryAfterHeader(""); got != 0 {
			t.Errorf("got %s, want 0", got)
		}
	})
	t.Run("zero-seconds", func(t *testing.T) {
		if got := parseRetryAfterHeader("0"); got != 0 {
			t.Errorf("got %s, want 0", got)
		}
	})
	t.Run("delta-seconds", func(t *testing.T) {
		if got := parseRetryAfterHeader("120"); got != 120*time.Second {
			t.Errorf("got %s, want 120s", got)
		}
	})
	t.Run("negative-clamped", func(t *testing.T) {
		if got := parseRetryAfterHeader("-5"); got != 0 {
			t.Errorf("got %s, want 0 (clamped from -5)", got)
		}
	})
	t.Run("unparseable", func(t *testing.T) {
		if got := parseRetryAfterHeader("not-a-number"); got != 0 {
			t.Errorf("got %s, want 0 (unparseable)", got)
		}
	})
	t.Run("http-date-future", func(t *testing.T) {
		// 60 s in the future → expected ~60 s band (with clock drift).
		hd := time.Now().Add(60 * time.Second).UTC().Format(http.TimeFormat)
		got := parseRetryAfterHeader(hd)
		if got < 50*time.Second || got > 61*time.Second {
			t.Errorf("got %s, want ~60s window", got)
		}
	})
	t.Run("http-date-past-clamped", func(t *testing.T) {
		// Past date → clamped to 0 (never wait a negative amount).
		hd := time.Now().Add(-60 * time.Second).UTC().Format(http.TimeFormat)
		if got := parseRetryAfterHeader(hd); got != 0 {
			t.Errorf("got %s, want 0 (clamped from past)", got)
		}
	})
}

// TestComputeYouTubeBackoff_Bounds verifies the full-jitter curve
// stays within [base, cap] for every attempt number — important
// because computeYouTubeBackoff is what uploadVideoChunks falls
// back to when the server didn't send a Retry-After.
func TestComputeYouTubeBackoff_Bounds(t *testing.T) {
	base := 100 * time.Millisecond
	capd := 1 * time.Second
	fn := computeYouTubeBackoff(base, capd)
	for attempt := 1; attempt <= 12; attempt++ {
		got := fn(attempt)
		if got < base {
			t.Errorf("attempt %d: %s below base %s", attempt, got, base)
		}
		if got > capd {
			t.Errorf("attempt %d: %s above cap %s", attempt, got, capd)
		}
	}
}
