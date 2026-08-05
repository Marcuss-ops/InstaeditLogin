package services

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestYouTubePutChunk_NormalUploadDoesNotSetContentType(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Content-Type"); got != "" {
			t.Errorf("normal upload Content-Type: got %q, want empty", got)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"video-id"}`))
	}))
	defer server.Close()

	svc := newTestYouTubeService(server)
	videoID, _, retryable, err := svc.putChunk(
		context.Background(),
		server.URL,
		[]byte("normal-upload-bytes"),
		"bytes 0-18/19",
		19,
	)
	if err != nil {
		t.Fatalf("putChunk: %v", err)
	}
	if retryable {
		t.Fatal("putChunk returned retryable=true on successful response")
	}
	if videoID != "video-id" {
		t.Fatalf("video id: got %q, want video-id", videoID)
	}
}
