package services

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// TestPublishThumbnail_HappyPath verifies that PublishThumbnail uploads
// the thumbnail, then updates video metadata + privacy, returning the
// public YouTube watch URL.
func TestPublishThumbnail_HappyPath(t *testing.T) {
	var thumbnailUploaded atomic.Bool
	var videoUpdated atomic.Bool

	mux := http.NewServeMux()
	mux.HandleFunc("/upload/youtube/v3/thumbnails/set", func(w http.ResponseWriter, r *http.Request) {
		thumbnailUploaded.Store(true)
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/youtube/v3/videos", func(w http.ResponseWriter, r *http.Request) {
		videoUpdated.Store(true)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestYouTubeService(srv)
	image := []byte("fake-thumbnail-bytes")

	url, err := svc.PublishThumbnail(
		context.Background(), "valid-token", "video123",
		image, "image/jpeg", "public", nil,
		models.YouTubePublishOptions{Title: "Test Title", Description: "Test Desc"},
	)
	if err != nil {
		t.Fatalf("PublishThumbnail returned unexpected error: %v", err)
	}
	if !thumbnailUploaded.Load() {
		t.Error("expected SetThumbnail to be called")
	}
	if !videoUpdated.Load() {
		t.Error("expected videos.update to be called")
	}
	if url != "https://www.youtube.com/watch?v=video123" {
		t.Errorf("unexpected URL: %s", url)
	}
}

// TestPublishThumbnail_EmptyData verifies that empty thumbnail data is
// rejected before any network call.
func TestPublishThumbnail_EmptyData(t *testing.T) {
	srv := httptest.NewServer(http.NewServeMux())
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	_, err := svc.PublishThumbnail(
		context.Background(), "token", "vid",
		[]byte{}, "image/jpeg", "public", nil,
		models.YouTubePublishOptions{Title: "t"},
	)
	if err == nil {
		t.Fatal("expected error for empty data")
	}
	if !strings.Contains(err.Error(), "empty thumbnail data") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestPublishThumbnail_Oversized verifies that thumbnails exceeding 2 MB
// are rejected before any network call.
func TestPublishThumbnail_Oversized(t *testing.T) {
	srv := httptest.NewServer(http.NewServeMux())
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	oversized := make([]byte, 2*1024*1024+1)
	_, err := svc.PublishThumbnail(
		context.Background(), "token", "vid",
		oversized, "image/jpeg", "public", nil,
		models.YouTubePublishOptions{Title: "t"},
	)
	if err == nil {
		t.Fatal("expected error for oversized thumbnail")
	}
	if !strings.Contains(err.Error(), "2 MB") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestPublishThumbnail_UnsupportedMIME verifies that non-JPEG/PNG MIME
// types are rejected before any network call.
func TestPublishThumbnail_UnsupportedMIME(t *testing.T) {
	srv := httptest.NewServer(http.NewServeMux())
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	_, err := svc.PublishThumbnail(
		context.Background(), "token", "vid",
		[]byte("data"), "image/webp", "public", nil,
		models.YouTubePublishOptions{Title: "t"},
	)
	if err == nil {
		t.Fatal("expected error for unsupported MIME")
	}
	if !strings.Contains(err.Error(), "unsupported content type") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestPublishThumbnail_SetThumbnailFails_SetsThumbnailError verifies that
// when SetThumbnail returns a non-retryable error (403 Forbidden), the
// publish fails and the videos.update is never called.
func TestPublishThumbnail_SetThumbnailFails_SetsThumbnailError(t *testing.T) {
	var videoUpdated atomic.Bool

	mux := http.NewServeMux()
	mux.HandleFunc("/upload/youtube/v3/thumbnails/set", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"error":"forbidden"}`))
	})
	mux.HandleFunc("/youtube/v3/videos", func(w http.ResponseWriter, r *http.Request) {
		videoUpdated.Store(true)
		w.WriteHeader(http.StatusOK)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestYouTubeService(srv)
	_, err := svc.PublishThumbnail(
		context.Background(), "token", "vid",
		[]byte("data"), "image/jpeg", "public", nil,
		models.YouTubePublishOptions{Title: "t"},
	)
	if err == nil {
		t.Fatal("expected error when SetThumbnail fails")
	}
	if !strings.Contains(err.Error(), "set thumbnail failed") {
		t.Errorf("unexpected error: %v", err)
	}
	if videoUpdated.Load() {
		t.Error("videos.update should not be called after SetThumbnail failure")
	}
}

// TestPublishThumbnail_SetThumbnailFails_RetryExhausted verifies that
// retryable errors (5xx) are retried 3 times and then fail.
func TestPublishThumbnail_SetThumbnailFails_RetryExhausted(t *testing.T) {
	var setCalls atomic.Int32

	mux := http.NewServeMux()
	mux.HandleFunc("/upload/youtube/v3/thumbnails/set", func(w http.ResponseWriter, r *http.Request) {
		setCalls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"internal"}`))
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestYouTubeService(srv)
	_, err := svc.PublishThumbnail(
		context.Background(), "token", "vid",
		[]byte("data"), "image/jpeg", "public", nil,
		models.YouTubePublishOptions{Title: "t"},
	)
	if err == nil {
		t.Fatal("expected error after retry exhaustion")
	}
	if setCalls.Load() != 3 {
		t.Errorf("expected 3 SetThumbnail calls (retry), got %d", setCalls.Load())
	}
}

// TestPublishThumbnail_UpdateVideoFails verifies that when SetThumbnail
// succeeds but UpdateVideoPrivacy fails, the error propagates.
func TestPublishThumbnail_UpdateVideoFails(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/upload/youtube/v3/thumbnails/set", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/youtube/v3/videos", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"error":"forbidden"}`))
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestYouTubeService(srv)
	_, err := svc.PublishThumbnail(
		context.Background(), "token", "vid",
		[]byte("data"), "image/jpeg", "public", nil,
		models.YouTubePublishOptions{Title: "t"},
	)
	if err == nil {
		t.Fatal("expected error when UpdateVideoPrivacy fails")
	}
	if !strings.Contains(err.Error(), "update video failed") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestPublishThumbnail_PNGMIME verifies that PNG thumbnails are accepted.
func TestPublishThumbnail_PNGMIME(t *testing.T) {
	var capturedContentType string

	mux := http.NewServeMux()
	mux.HandleFunc("/upload/youtube/v3/thumbnails/set", func(w http.ResponseWriter, r *http.Request) {
		capturedContentType = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/youtube/v3/videos", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestYouTubeService(srv)
	_, err := svc.PublishThumbnail(
		context.Background(), "token", "vid",
		[]byte("png-data"), "image/png", "unlisted", nil,
		models.YouTubePublishOptions{Title: "t"},
	)
	if err != nil {
		t.Fatalf("PublishThumbnail returned unexpected error: %v", err)
	}
	if capturedContentType != "image/png" {
		t.Errorf("expected Content-Type image/png, got %s", capturedContentType)
	}
}

// TestPublishThumbnail_ScheduledPublishAt verifies that when publishAt is
// provided with privacy=private, the videos.update payload includes the
// scheduled publish time.
func TestPublishThumbnail_ScheduledPublishAt(t *testing.T) {
	var capturedBody []byte

	mux := http.NewServeMux()
	mux.HandleFunc("/upload/youtube/v3/thumbnails/set", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/youtube/v3/videos", func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestYouTubeService(srv)
	futureTime := time.Now().UTC().Add(24 * time.Hour)

	_, err := svc.PublishThumbnail(
		context.Background(), "token", "vid",
		[]byte("data"), "image/jpeg", "private", &futureTime,
		models.YouTubePublishOptions{Title: "Scheduled Video"},
	)
	if err != nil {
		t.Fatalf("PublishThumbnail returned unexpected error: %v", err)
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(capturedBody, &payload); err != nil {
		t.Fatalf("failed to decode videos.update body: %v", err)
	}
	status, ok := payload["status"].(map[string]interface{})
	if !ok {
		t.Fatal("expected status object in videos.update body")
	}
	if status["privacyStatus"] != "private" {
		t.Errorf("expected privacyStatus=private, got %v", status["privacyStatus"])
	}
	if status["publishAt"] == nil {
		t.Error("expected publishAt in status")
	}
}
