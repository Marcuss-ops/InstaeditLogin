package worker

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// TestVeloxSource_Name — registry-key contract: must return
// UploadJobSourceVeloxArtifact or registry.Resolves misroute.
func TestVeloxSource_Name(t *testing.T) {
	s := NewVeloxSource(nil)
	if got := s.Name(); got != models.UploadJobSourceVeloxArtifact {
		t.Fatalf("Name() = %q; want %q", got, models.UploadJobSourceVeloxArtifact)
	}
}

// TestVeloxSource_Inspect_EmptyURL — SourceID is empty / not a URL,
// Inspect should fail loud BEFORE the HTTP round-trip.
func TestVeloxSource_Inspect_EmptyURL(t *testing.T) {
	s := NewVeloxSource(nil)
	job := &models.UploadJob{SourceID: "not-a-url-12345"}
	_, err := s.Inspect(context.Background(), job)
	if err == nil {
		t.Fatal("Inspect with non-URL SourceID should fail")
	}
	if !strings.Contains(err.Error(), "empty download_url") {
		t.Fatalf("expected 'empty download_url' in error; got %v", err)
	}
}

// TestVeloxSource_Inspect_HEAD_OK — happy path: HEAD 200 returns
// SourceMetadata populated from Content-Length + Content-Type + ETag.
func TestVeloxSource_Inspect_HEAD_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			t.Errorf("expected HEAD; got %s", r.Method)
		}
		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("ETag", `"abc123"`)
		w.Header().Set("Content-Length", "1234567")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := NewVeloxSource(nil)
	job := &models.UploadJob{SourceID: srv.URL + "/artifact.mp4"}
	md, err := s.Inspect(context.Background(), job)
	if err != nil {
		t.Fatalf("Inspect returned unexpected error: %v", err)
	}
	if md == nil {
		t.Fatal("Inspect returned nil SourceMetadata on HEAD 200")
	}
	if md.SizeBytes != 1234567 {
		t.Errorf("SizeBytes = %d; want 1234567", md.SizeBytes)
	}
	if md.MimeType != "video/mp4" {
		t.Errorf("MimeType = %q; want video/mp4", md.MimeType)
	}
	if md.ETag != `"abc123"` {
		t.Errorf("ETag = %q; want %q", md.ETag, `"abc123"`)
	}
	// SHA256Hex intentionally empty per the source's contract
	// (defense-in-depth: never trust upstream-declared hashes).
	if md.SHA256Hex != "" {
		t.Errorf("SHA256Hex = %q; want empty (defense-in-depth)", md.SHA256Hex)
	}
}

// TestVeloxSource_Inspect_HEAD_404 — non-200 status surfaces as
// the worker-classified transient error so classifyUploadError can
// match on the status code for routing.
func TestVeloxSource_Inspect_HEAD_404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	s := NewVeloxSource(nil)
	job := &models.UploadJob{SourceID: srv.URL + "/missing.mp4"}
	_, err := s.Inspect(context.Background(), job)
	if err == nil {
		t.Fatal("Inspect HEAD 404 should fail")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Fatalf("expected 404 in error; got %v", err)
	}
}

// TestVeloxSource_Open_EmptyURL — non-URL SourceID fails loud
// before HTTP round-trip; matches the Inspect gate's contract.
func TestVeloxSource_Open_EmptyURL(t *testing.T) {
	s := NewVeloxSource(nil)
	job := &models.UploadJob{SourceID: "not-a-url-12345"}
	_, err := s.Open(context.Background(), job)
	if err == nil {
		t.Fatal("Open with non-URL SourceID should fail")
	}
}

// TestVeloxSource_Open_GET_OK — GET 200 returns a ReadCloser that
// drains the response body verbatim. worker-layer (not source)
// wraps this in TeeReader for SHA, so source must return the raw
// body otherwise SHA computation would double-hash.
func TestVeloxSource_Open_GET_OK(t *testing.T) {
	const body = "the artifact bytes"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET; got %s", r.Method)
		}
		w.Header().Set("Content-Type", "video/mp4")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	s := NewVeloxSource(nil)
	job := &models.UploadJob{SourceID: srv.URL + "/artifact.mp4"}
	rc, err := s.Open(context.Background(), job)
	if err != nil {
		t.Fatalf("Open returned unexpected error: %v", err)
	}
	defer rc.Close()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}
	if string(got) != body {
		t.Fatalf("returned body = %q; want %q", string(got), body)
	}
}

// TestVeloxSource_Open_GET_404 — non-200 surfaces; the resp.Body
// is closed inside Open before the error returns (no leaked fd).
func TestVeloxSource_Open_GET_404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	s := NewVeloxSource(nil)
	job := &models.UploadJob{SourceID: srv.URL + "/missing.mp4"}
	_, err := s.Open(context.Background(), job)
	if err == nil {
		t.Fatal("Open GET 404 should fail")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Fatalf("expected 404 in error; got %v", err)
	}
}
