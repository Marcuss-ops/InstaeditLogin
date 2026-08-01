package services

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// -----------------------------------------------------------------------------
// PULL_FROM_FILE / chunked-upload test harness (Taglio 4.x addendum).
// Mirrors the snapshot tests of /v2/post/publish/video/init/, the
// chunked-PUT protocol's Content-Range header, and the
// /v2/post/publish/video/upload/complete/ call. The happy-path test
// overrides svc.chunkSize to 1024 bytes so we can exercise 3 chunks
// (1024-byte chunks on a 3072-byte video) instead of allocating
// 10MB+ payloads for unit tests.
// -----------------------------------------------------------------------------

// pullFromFileMockServer builds an httptest server with the four
// endpoints PULL_FROM_FILE expects: a video source, the TikTok init
// endpoint, a chunk-upload endpoint (registered post-bind), and the
// TikTok complete endpoint. Returns the server + the chunks handler
// (for assertion on uploaded ranges) + bindable endpoints on the mux
// so tests can override per-call behaviour (e.g., inject a 4xx).
func pullFromFileMockServer(t *testing.T) (*httptest.Server, *pullFromFileHandlers) {
	t.Helper()
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	h := &pullFromFileHandlers{
		mux:            mux,
		srv:            srv,
		chunksReceived: []chunkCall{},
	}
	h.bindDefaults()
	return srv, h
}

type chunkCall struct {
	rangeHeader string
	authHeader  string
	method      string
	byteCount   int64
}

type pullFromFileHandlers struct {
	mux            *http.ServeMux
	srv            *httptest.Server
	chunksReceived []chunkCall

	// OnInit is invoked by /v2/.../init/'s handler with the raw
	// request body AND the *http.Request BEFORE the response is
	// written. Tests use it to capture/assert on the JSON shape the
	// service sends to TikTok (body) and on transport-layer details
	// (Authorization header) without re-registering the mux pattern
	// (which would conflict with bindDefaults). Optional — nil is a
	// no-op.
	OnInit func(rawBody []byte, r *http.Request)

	// Pluggable behaviour (overridden per-test if needed).
	sourceVideoBytes  []byte
	sourceVideoStatus int
	initStatus        int
	initBody          []byte
	chunkStatus       int
	completeStatus    int
}

// bindDefaults registers the 4 endpoints with reasonable defaults:
//
//	/source-video               → 200 OK + 3072 zero-fills
//	/v2/.../init/               → 200 OK + JSON with upload_url mapped to /chunk-upload
//	/chunk-upload               → 200 OK + record call
//	/v2/.../upload/complete/    → 200 OK
func (h *pullFromFileHandlers) bindDefaults() {
	h.sourceVideoBytes = bytes.Repeat([]byte{0}, 3072) // 3× 1024 chunks when chunkSize=1024
	h.sourceVideoStatus = http.StatusOK
	h.initStatus = http.StatusOK
	h.chunkStatus = http.StatusOK
	h.completeStatus = http.StatusOK

	h.mux.HandleFunc("/source-video", func(w http.ResponseWriter, r *http.Request) {
		if h.sourceVideoStatus != http.StatusOK {
			w.WriteHeader(h.sourceVideoStatus)
			w.Write([]byte(`{"error":"source_unreachable"}`))
			return
		}
		w.Header().Set("Content-Type", "video/mp4")
		w.Write(h.sourceVideoBytes)
	})
	h.mux.HandleFunc("/v2/post/publish/video/init/", h.handleInit)
	h.mux.HandleFunc("/chunk-upload", h.handleChunk)
	h.mux.HandleFunc("/v2/post/publish/video/upload/complete/", h.handleComplete)
}

func (h *pullFromFileHandlers) handleInit(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	if h.OnInit != nil {
		h.OnInit(body, r)
	}
	if h.initStatus != http.StatusOK {
		w.WriteHeader(h.initStatus)
		w.Write([]byte(`{"error":{"code":"internal_error","message":"platform rejected init"}}`))
		return
	}
	if h.initBody != nil {
		w.Write(h.initBody)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"data":{"publish_id":"v_pub_file_1","upload_url":"%s/chunk-upload"}}`, h.srv.URL)
}

func (h *pullFromFileHandlers) handleChunk(w http.ResponseWriter, r *http.Request) {
	if r.Method != "PUT" {
		http.Error(w, "want PUT", http.StatusMethodNotAllowed)
		return
	}
	w.WriteHeader(h.chunkStatus)
	n, _ := io.Copy(io.Discard, r.Body)
	h.chunksReceived = append(h.chunksReceived, chunkCall{
		rangeHeader: r.Header.Get("Content-Range"),
		authHeader:  r.Header.Get("Authorization"),
		method:      r.Method,
		byteCount:   n,
	})
}

func (h *pullFromFileHandlers) handleComplete(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "want POST", http.StatusMethodNotAllowed)
		return
	}
	body, _ := io.ReadAll(r.Body)
	w.WriteHeader(h.completeStatus)
	// Echo back the publish_id so the test can parse + assert.
	fmt.Fprintf(w, `{"data":{"publish_id":"%s"}}`, "v_pub_file_1")
	_ = body
}

// newTestTikTokServiceWithChunkSize mirrors newTestTikTokService but
// also sets svc.chunkSize so chunked upload tests can exercise
// small-byte videos (default chunkSize=0 → 10MB would otherwise force
// a 10MB source allocation). Same package so direct field access is
// available.
func newTestTikTokServiceWithChunkSize(srv *httptest.Server, chunkSize int) *TikTokOAuthService {
	svc := newTestTikTokService(srv)
	svc.chunkSize = chunkSize
	return svc
}

type pullFromFileCase struct {
	name        string
	chunkSize   int
	accessToken string
	setup       func(h *pullFromFileHandlers)
	assert      func(t *testing.T, h *pullFromFileHandlers, initBody []byte, publishID, state string, err error)
}

func runPullFromFileCase(t *testing.T, tc pullFromFileCase) {
	t.Helper()
	srv, h := pullFromFileMockServer(t)
	defer srv.Close()
	if tc.setup != nil {
		tc.setup(h)
	}
	chunkSize := 1024
	if tc.chunkSize > 0 {
		chunkSize = tc.chunkSize
	}
	accessToken := tc.accessToken
	if accessToken == "" {
		accessToken = "tt-access-token"
	}
	var initBody []byte
	prevOnInit := h.OnInit
	h.OnInit = func(body []byte, r *http.Request) {
		initBody = body
		if prevOnInit != nil {
			prevOnInit(body, r)
		}
	}
	payload := models.PublishPayload{
		Text:         tc.name,
		VideoURL:     srv.URL + "/source-video",
		PrivacyLevel: "PUBLIC_TO_EVERYONE",
		Source:       models.PublishSourcePULLFromFile,
	}
	svc := newTestTikTokServiceWithChunkSize(srv, chunkSize)
	publishID, state, err := svc.StartPublish(context.Background(), accessToken, "tt-1", payload)
	tc.assert(t, h, initBody, publishID, state, err)
}
