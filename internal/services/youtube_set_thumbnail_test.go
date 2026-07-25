package services

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestSetThumbnail_HappyPath verifies that SetThumbnail posts the
// image bytes to the thumbnails.set endpoint and returns nil on a
// successful 200 response.
func TestSetThumbnail_HappyPath(t *testing.T) {
	const videoID = "abc123def4g"
	var capturedVideoID string
	var capturedContentType string
	var capturedBody []byte

	mux := http.NewServeMux()
	mux.HandleFunc("/upload/youtube/v3/thumbnails/set", func(w http.ResponseWriter, r *http.Request) {
		capturedVideoID = r.URL.Query().Get("videoId")
		capturedContentType = r.Header.Get("Content-Type")
		capturedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestYouTubeService(srv)

	image := []byte("fake-jpeg-bytes")
	mimeType := "image/jpeg"
	err := svc.SetThumbnail(context.Background(), "valid-token", videoID, mimeType, bytes.NewReader(image), int64(len(image)))
	if err != nil {
		t.Fatalf("SetThumbnail returned unexpected error: %v", err)
	}

	if capturedVideoID != videoID {
		t.Errorf("videoId: got %q, want %q", capturedVideoID, videoID)
	}
	if capturedContentType != mimeType {
		t.Errorf("Content-Type: got %q, want %q", capturedContentType, mimeType)
	}
	if !bytes.Equal(capturedBody, image) {
		t.Errorf("request body did not match uploaded image")
	}
}

// TestSetThumbnail_ClientErrors verifies that the expected HTTP status
// codes are mapped to descriptive errors without leaking the token.
func TestSetThumbnail_ClientErrors(t *testing.T) {
	cases := []struct {
		status     int
		body       string
		wantToken  string
		wantSubstr string
	}{
		{http.StatusUnauthorized, `{"error":"unauthorized"}`, "valid-token", "unauthorized (status 401)"},
		{http.StatusForbidden, `{"error":"forbidden"}`, "valid-token", "forbidden (status 403)"},
		{http.StatusNotFound, `{"error":"notFound"}`, "valid-token", "not found (status 404)"},
		{http.StatusTooManyRequests, `{"error":"rateLimitExceeded"}`, "valid-token", "rate limited (status 429)"},
		{http.StatusInternalServerError, `{"error":"internal"}`, "valid-token", "server error (status 500)"},
		{http.StatusBadGateway, `{"error":"bad_gateway"}`, "valid-token", "server error (status 502)"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(fmt.Sprintf("status_%d", tc.status), func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("/upload/youtube/v3/thumbnails/set", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				w.Write([]byte(tc.body))
			})

			srv := httptest.NewServer(mux)
			defer srv.Close()

			svc := newTestYouTubeService(srv)
			image := []byte("fake-jpeg-bytes")
			err := svc.SetThumbnail(context.Background(), tc.wantToken, "video123", "image/jpeg", bytes.NewReader(image), int64(len(image)))
			if err == nil {
				t.Fatalf("expected error for status %d, got nil", tc.status)
			}
			if !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.wantSubstr)
			}
			if strings.Contains(err.Error(), tc.wantToken) {
				t.Errorf("error must not leak access token; got %q", err.Error())
			}
		})
	}
}

// TestSetThumbnail_InputValidation verifies that invalid inputs are
// rejected before any network request is made.
func TestSetThumbnail_InputValidation(t *testing.T) {
	srv := httptest.NewServer(http.NewServeMux())
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	tests := []struct {
		name      string
		videoID   string
		mimeType  string
		size      int64
		wantError string
	}{
		{"empty video id", "", "image/jpeg", 100, "empty video id"},
		{"zero size", "vid", "image/jpeg", 0, "invalid image size"},
		{"oversized", "vid", "image/jpeg", 3 * 1024 * 1024, "2 MB"},
		{"unsupported type", "vid", "image/webp", 100, "unsupported content type"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			err := svc.SetThumbnail(context.Background(), "token", tc.videoID, tc.mimeType, bytes.NewReader([]byte("x")), tc.size)
			if err == nil {
				t.Fatalf("expected error for %s, got nil", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantError) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.wantError)
			}
		})
	}
}
