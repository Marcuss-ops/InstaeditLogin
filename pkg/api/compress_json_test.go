package api

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGzipJSONMiddleware_CompressesJSONWhenAccepted(t *testing.T) {
	h := gzipJSONMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"items":[{"id":"one"}]}`))
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/media", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if got := res.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding: want gzip, got %q", got)
	}
	if !strings.Contains(res.Header().Get("Vary"), "Accept-Encoding") {
		t.Fatalf("Vary must include Accept-Encoding, got %q", res.Header().Get("Vary"))
	}
	reader, err := gzip.NewReader(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	_ = reader.Close()
	if string(body) != `{"items":[{"id":"one"}]}` {
		t.Fatalf("decoded body: got %q", body)
	}
}

func TestGzipJSONMiddlewareLeavesJSONUncompressedWithoutNegotiation(t *testing.T) {
	h := gzipJSONMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	res := httptest.NewRecorder()
	h.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/health", nil))
	if got := res.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding: want empty, got %q", got)
	}
	if res.Body.String() != `{"ok":true}` {
		t.Fatalf("body: got %q", res.Body.String())
	}
}

func TestGzipJSONMiddlewareDoesNotCompressNonJSON(t *testing.T) {
	h := gzipJSONMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("plain"))
	}))
	res := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	h.ServeHTTP(res, req)
	if res.Header().Get("Content-Encoding") != "" {
		t.Fatalf("text response must not be compressed")
	}
	if res.Body.String() != "plain" {
		t.Fatalf("body: got %q", res.Body.String())
	}
}

func TestGzipJSONMiddlewareDoesNotDoubleCompress(t *testing.T) {
	h := gzipJSONMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Encoding", "br")
		_, _ = w.Write([]byte("already encoded"))
	}))
	res := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	h.ServeHTTP(res, req)
	if res.Header().Get("Content-Encoding") != "br" {
		t.Fatalf("existing encoding must be preserved, got %q", res.Header().Get("Content-Encoding"))
	}
	if res.Body.String() != "already encoded" {
		t.Fatalf("body: got %q", res.Body.String())
	}
}

func TestGzipJSONMiddlewareDoesNotCompressNoContent(t *testing.T) {
	h := gzipJSONMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNoContent)
	}))
	res := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/health", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	h.ServeHTTP(res, req)
	if res.Header().Get("Content-Encoding") != "" {
		t.Fatalf("204 must not be compressed")
	}
}
