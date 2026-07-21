package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequestIDMiddleware_GeneratesAndMirrorsID(t *testing.T) {
	r := &Router{}
	var ctxID string
	handler := r.requestIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ctxID = requestIDFromContext(req.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	id := w.Header().Get("X-Request-ID")
	if id == "" {
		t.Fatal("expected X-Request-ID response header")
	}
	if len(id) < 16 {
		t.Fatalf("request_id looks too short: %q", id)
	}
	if ctxID != id {
		t.Fatalf("expected context request_id %q to match header %q", ctxID, id)
	}
}

func TestRequestIDMiddleware_ReusesIncomingHeader(t *testing.T) {
	r := &Router{}
	var ctxID string
	handler := r.requestIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ctxID = requestIDFromContext(req.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-ID", "client-correlation-id")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if got := w.Header().Get("X-Request-ID"); got != "client-correlation-id" {
		t.Fatalf("expected request_id to be reused, got %q", got)
	}
	if ctxID != "client-correlation-id" {
		t.Fatalf("expected context request_id to be reused, got %q", ctxID)
	}
}

func TestRequestIDMiddleware_RejectsInvalidIncomingID(t *testing.T) {
	r := &Router{}
	var ctxID string
	handler := r.requestIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ctxID = requestIDFromContext(req.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-ID", "bad id with spaces and <html>")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	id := w.Header().Get("X-Request-ID")
	if id == "bad id with spaces and <html>" {
		t.Fatalf("invalid request_id was accepted: %q", id)
	}
	if id == "" {
		t.Fatal("expected a generated request_id")
	}
	if ctxID != id {
		t.Fatalf("expected context request_id %q to match header %q", ctxID, id)
	}
}

func TestLogAndError_SanitizesClientResponseAndLogsFullError(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(old)

	r := &Router{}
	mux := http.NewServeMux()
	mux.HandleFunc("/boom", func(w http.ResponseWriter, req *http.Request) {
		logAndError(w, req, "database blew up", errors.New("postgres connection refused: host=db internal"))
	})

	// Wire request ID so the context carries one.
	handler := r.requestIDMiddleware(mux)
	req := httptest.NewRequest(http.MethodGet, "/boom", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}

	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON response: %v", err)
	}
	if body["error"] != "Internal Server Error" {
		t.Fatalf("expected generic error, got %q", body["error"])
	}
	reqID := body["request_id"]
	if reqID == "" {
		t.Fatal("expected request_id in response body")
	}

	logOutput := buf.String()
	if !strings.Contains(logOutput, "database blew up") {
		t.Fatalf("expected log to contain message, got %s", logOutput)
	}
	if !strings.Contains(logOutput, "postgres connection refused") {
		t.Fatalf("expected log to contain full error, got %s", logOutput)
	}
	if !strings.Contains(logOutput, reqID) {
		t.Fatalf("expected log to contain request_id, got %s", logOutput)
	}
}
