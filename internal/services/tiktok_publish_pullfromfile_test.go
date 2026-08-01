package services

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// -----------------------------------------------------------------------------
// PULL_FROM_FILE / chunked-upload tests (Taglio 4.x addendum).
// These exercise the full chunked-PUT protocol on top of the shared
// mock harness in tiktok_publish_mock_test.go.
// -----------------------------------------------------------------------------

func TestTikTok_StartPublish_PULLFromFile(t *testing.T) {
	type sourceInfo struct {
		Source          string `json:"source"`
		VideoSize       int64  `json:"video_size"`
		ChunkSize       int64  `json:"chunk_size"`
		TotalChunkCount int64  `json:"total_chunk_count"`
	}

	cases := []pullFromFileCase{
		{
			name:      "HappyPath",
			chunkSize: 1024,
			assert: func(t *testing.T, h *pullFromFileHandlers, initBody []byte, publishID, state string, err error) {
				if err != nil {
					t.Fatalf("StartPublish: %v", err)
				}
				if publishID != "v_pub_file_1" {
					t.Errorf("publishID: want v_pub_file_1, got %q", publishID)
				}
				if state != "PROCESSING_UPLOAD" {
					t.Errorf("state: want PROCESSING_UPLOAD, got %q", state)
				}
				var parsed struct {
					SourceInfo sourceInfo `json:"source_info"`
				}
				_ = json.Unmarshal(initBody, &parsed)
				if parsed.SourceInfo.Source != "FILE_UPLOAD" {
					t.Errorf("init source: want FILE_UPLOAD, got %q", parsed.SourceInfo.Source)
				}
				if parsed.SourceInfo.VideoSize != 3072 || parsed.SourceInfo.ChunkSize != 3072 || parsed.SourceInfo.TotalChunkCount != 1 {
					t.Errorf("init body: want whole-file 3072-byte single chunk, got %+v", parsed.SourceInfo)
				}
				if len(h.chunksReceived) != 1 {
					t.Fatalf("chunks: want 1, got %d", len(h.chunksReceived))
				}
				if h.chunksReceived[0].rangeHeader != "bytes 0-3071/3072" {
					t.Errorf("chunk[0] Content-Range: want %q, got %q", "bytes 0-3071/3072", h.chunksReceived[0].rangeHeader)
				}
				if h.chunksReceived[0].byteCount != 3072 {
					t.Errorf("chunk[0] body size: want 3072, got %d", h.chunksReceived[0].byteCount)
				}
			},
		},
		{
			name:      "MultiChunk",
			chunkSize: 2 * 1024 * 1024,
			setup: func(h *pullFromFileHandlers) {
				h.sourceVideoBytes = bytes.Repeat([]byte{0}, 6*1024*1024)
			},
			assert: func(t *testing.T, h *pullFromFileHandlers, initBody []byte, publishID, state string, err error) {
				if err != nil {
					t.Fatalf("StartPublish: %v", err)
				}
				var parsed struct {
					SourceInfo sourceInfo `json:"source_info"`
				}
				_ = json.Unmarshal(initBody, &parsed)
				if parsed.SourceInfo.TotalChunkCount != 3 {
					t.Errorf("init total_chunk_count: want 3, got %d", parsed.SourceInfo.TotalChunkCount)
				}
				if len(h.chunksReceived) != 3 {
					t.Fatalf("chunks: want 3, got %d", len(h.chunksReceived))
				}
				wantRanges := []string{
					"bytes 0-2097151/6291456",
					"bytes 2097152-4194303/6291456",
					"bytes 4194304-6291455/6291456",
				}
				for i, want := range wantRanges {
					if h.chunksReceived[i].rangeHeader != want {
						t.Errorf("chunk[%d] Content-Range: want %q, got %q", i, want, h.chunksReceived[i].rangeHeader)
					}
					if h.chunksReceived[i].byteCount != 2*1024*1024 {
						t.Errorf("chunk[%d] body size: want %d, got %d", i, 2*1024*1024, h.chunksReceived[i].byteCount)
					}
				}
			},
		},
		{
			name:      "LastChunkPartial",
			chunkSize: 1024,
			setup: func(h *pullFromFileHandlers) {
				h.sourceVideoBytes = bytes.Repeat([]byte{0}, 1500)
			},
			assert: func(t *testing.T, h *pullFromFileHandlers, initBody []byte, publishID, state string, err error) {
				if err != nil {
					t.Fatalf("StartPublish: %v", err)
				}
				if len(h.chunksReceived) != 1 || h.chunksReceived[0].rangeHeader != "bytes 0-1499/1500" || h.chunksReceived[0].byteCount != 1500 {
					t.Errorf("chunk[0]: want 0-1499/1500 (1500 bytes), got %q (%d bytes)", h.chunksReceived[0].rangeHeader, h.chunksReceived[0].byteCount)
				}
			},
		},
		{
			name:      "InitFailure",
			chunkSize: 1024,
			setup: func(h *pullFromFileHandlers) {
				h.initStatus = http.StatusInternalServerError
			},
			assert: func(t *testing.T, h *pullFromFileHandlers, initBody []byte, publishID, state string, err error) {
				if err == nil || !strings.Contains(err.Error(), "init") {
					t.Fatalf("expected init error, got %v", err)
				}
				if len(h.chunksReceived) != 0 {
					t.Errorf("no chunks should be sent after init failure, got %d", len(h.chunksReceived))
				}
			},
		},
		{
			name:      "ChunkFailure",
			chunkSize: 1024,
			setup: func(h *pullFromFileHandlers) {
				h.chunkStatus = http.StatusBadRequest
			},
			assert: func(t *testing.T, h *pullFromFileHandlers, initBody []byte, publishID, state string, err error) {
				if err == nil || !strings.Contains(err.Error(), "chunk PUT") {
					t.Fatalf("expected chunk PUT error, got %v", err)
				}
			},
		},
		{
			name:      "CompleteFailure",
			chunkSize: 1024,
			setup: func(h *pullFromFileHandlers) {
				h.completeStatus = http.StatusInternalServerError
			},
			assert: func(t *testing.T, h *pullFromFileHandlers, initBody []byte, publishID, state string, err error) {
				if err == nil || !strings.Contains(err.Error(), "complete") {
					t.Fatalf("expected complete error, got %v", err)
				}
				if len(h.chunksReceived) != 1 {
					t.Errorf("expected 1 chunk sent before complete failed, got %d", len(h.chunksReceived))
				}
			},
		},
		{
			name:      "MissingUploadURL",
			chunkSize: 1024,
			setup: func(h *pullFromFileHandlers) {
				h.initBody = []byte(`{"data":{"publish_id":"v_pub_file_1","upload_url":""}}`)
			},
			assert: func(t *testing.T, h *pullFromFileHandlers, initBody []byte, publishID, state string, err error) {
				if err == nil || !strings.Contains(err.Error(), "upload_url") {
					t.Fatalf("expected upload_url error, got %v", err)
				}
			},
		},
		{
			name:      "SourceFetchFailure",
			chunkSize: 1024,
			setup: func(h *pullFromFileHandlers) {
				h.sourceVideoStatus = http.StatusNotFound
			},
			assert: func(t *testing.T, h *pullFromFileHandlers, initBody []byte, publishID, state string, err error) {
				if err == nil || !strings.Contains(err.Error(), "fetch video bytes") {
					t.Fatalf("expected fetch video bytes error, got %v", err)
				}
				if len(h.chunksReceived) != 0 {
					t.Error("chunks must not be sent on source-fetch failure")
				}
			},
		},
		{
			name:        "AuthHeaderOnInit",
			chunkSize:   4096,
			accessToken: "user-bearer-xyz",
			setup: func(h *pullFromFileHandlers) {
				var authSeen string
				h.OnInit = func(_ []byte, r *http.Request) {
					authSeen = r.Header.Get("Authorization")
				}
				t.Cleanup(func() {
					if authSeen != "Bearer user-bearer-xyz" {
						t.Errorf("Authorization: want %q, got %q", "Bearer user-bearer-xyz", authSeen)
					}
				})
			},
			assert: func(t *testing.T, h *pullFromFileHandlers, initBody []byte, publishID, state string, err error) {
				if err != nil {
					t.Fatalf("StartPublish: %v", err)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runPullFromFileCase(t, tc)
		})
	}
}

// TestTikTok_StartPublish_SourceEmpty_UsesPULLFromURL is the
// regression guard for the dual-path dispatcher: an empty Source
// field MUST continue to route through the legacy PULL_FROM_URL path
// (existing callers don't set the field). If a future refactor
// changes this default the test fails.
func TestTikTok_StartPublish_SourceEmpty_UsesPULLFromURL(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/post/publish/video/init/", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var parsed struct {
			SourceInfo struct {
				Source   string `json:"source"`
				VideoURL string `json:"video_url"`
			} `json:"source_info"`
		}
		_ = json.Unmarshal(body, &parsed)
		if parsed.SourceInfo.Source != "PULL_FROM_URL" {
			t.Errorf("empty Source must route to PULL_FROM_URL, got %q", parsed.SourceInfo.Source)
		}
		if parsed.SourceInfo.VideoURL == "" {
			t.Error("PULL_FROM_URL init must include video_url")
		}
		w.Write([]byte(`{"data":{"publish_id":"v_pub_url_1","status":"PROCESSING_UPLOAD"}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestTikTokService(srv)
	_, state, err := svc.StartPublish(context.Background(), "tok", "tt-1", models.PublishPayload{
		Text:         "default path",
		VideoURL:     "https://cdn.example.com/v.mp4",
		PrivacyLevel: "PUBLIC_TO_EVERYONE",
		// Source omitted on purpose.
	})
	if err != nil {
		t.Fatalf("StartPublish(empty Source): %v", err)
	}
	if state != "PROCESSING_UPLOAD" {
		t.Errorf("state: want PROCESSING_UPLOAD, got %q", state)
	}
}

// TestTikTok_StartPublish_PULLFromFile_AuthHeaderOnInit ensures the
// init request carries the user's Bearer access token (now also true
// for uploaded sessions — same Authorization contract as the
// PULL_FROM_URL one). Regression guard against an accidental swap to
// the client_key.
func TestTikTok_StartPublish_PULLFromFile_AuthHeaderOnInit(t *testing.T) {
	srv, h := pullFromFileMockServer(t)
	defer srv.Close()

	var authSeen string
	h.OnInit = func(_ []byte, r *http.Request) {
		authSeen = r.Header.Get("Authorization")
	}

	svc := newTestTikTokServiceWithChunkSize(srv, 4096)
	if _, _, err := svc.StartPublish(context.Background(), "user-bearer-xyz", "tt-1", models.PublishPayload{
		Text: "auth header", VideoURL: srv.URL + "/source-video",
		PrivacyLevel: "PUBLIC_TO_EVERYONE", Source: models.PublishSourcePULLFromFile,
	}); err != nil {
		t.Fatalf("StartPublish: %v", err)
	}
	if authSeen != "Bearer user-bearer-xyz" {
		t.Errorf("Authorization: want %q, got %q", "Bearer user-bearer-xyz", authSeen)
	}
}
