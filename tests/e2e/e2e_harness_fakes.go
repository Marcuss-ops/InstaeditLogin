//go:build e2e

package e2e

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ----- in-process httptest fake servers --------------------------------

type fakeDriveServer struct {
	*httptest.Server
	mu        sync.Mutex
	files     map[string]*fakeDriveFileMeta // file_id → metadata
	listCalls int64                         // atomic counter
}

type fakeDriveFileMeta struct {
	id            string
	name          string
	parents       []string
	webViewLink   string
	appProperties map[string]string
}

func newFakeDriveServer() *fakeDriveServer {
	f := &fakeDriveServer{
		files: make(map[string]*fakeDriveFileMeta),
	}
	// Pre-load 201 dummy file IDs across 2 pages (100 + 101).
	for i := 0; i < 100; i++ {
		id := fmt.Sprintf("drive-file-page1-%03d", i)
		f.files[id] = &fakeDriveFileMeta{
			id:          id,
			name:        fmt.Sprintf("video_page1_%03d.mp4", i),
			parents:     []string{"folder_xxx"},
			webViewLink: "https://drive.google.com/file/d/" + id + "/view",
		}
	}
	for i := 0; i < 101; i++ {
		id := fmt.Sprintf("drive-file-page2-%03d", i)
		f.files[id] = &fakeDriveFileMeta{
			id:          id,
			name:        fmt.Sprintf("video_page2_%03d.mp4", i),
			parents:     []string{"folder_xxx"},
			webViewLink: "https://drive.google.com/file/d/" + id + "/view",
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/drive/v3/files", f.handleList)
	mux.HandleFunc("/drive/v3/files/", f.handleGet)
	mux.HandleFunc("/oauth/token", f.handleOAuthToken)
	f.Server = httptest.NewServer(mux)
	return f
}

// Reset clears the per-subtest mutable state.
func (f *fakeDriveServer) Reset() {
	atomic.StoreInt64(&f.listCalls, 0)
	f.mu.Lock()
	f.mu.Unlock()
}

// handleList emits page-1 on empty pageToken + page-2 on
// pageToken=page-2 + empty on pageToken=page-3. 201 files total
// across two pages.
func (f *fakeDriveServer) handleList(w http.ResponseWriter, r *http.Request) {
	atomic.AddInt64(&f.listCalls, 1)
	q := r.URL.Query()
	pageToken := q.Get("pageToken")

	f.mu.Lock()
	defer f.mu.Unlock()

	startIdx := 0
	endIdx := 100
	if pageToken == "page-2" {
		startIdx = 100
		endIdx = 201
	} else if pageToken == "page-3" {
		startIdx = 200
		endIdx = 200
	}

	allIDs := make([]string, 0, len(f.files))
	for id := range f.files {
		allIDs = append(allIDs, id)
	}
	// Map iteration order is deliberately randomized in Go. Sort before
	// slicing so page-1 and page-2 are stable, disjoint portions of the
	// 201-file fixture rather than overlapping random subsets.
	sort.Strings(allIDs)

	type fileEntry struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		WebViewLink string `json:"webViewLink"`
	}
	files := []fileEntry{}
	for _, id := range allIDs[startIdx:endIdx] {
		file := f.files[id]
		files = append(files, fileEntry{
			ID:          file.id,
			Name:        file.name,
			WebViewLink: file.webViewLink,
		})
	}
	resp := map[string]interface{}{"files": files}
	if pageToken == "" {
		resp["nextPageToken"] = "page-2"
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (f *fakeDriveServer) handleGet(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/drive/v3/files/")
	f.mu.Lock()
	defer f.mu.Unlock()
	file, ok := f.files[id]
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	resp := map[string]interface{}{
		"id":            file.id,
		"name":          file.name,
		"webViewLink":   file.webViewLink,
		"appProperties": file.appProperties,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (f *fakeDriveServer) handleOAuthToken(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"access_token":"fake-bearer","expires_in":3600}`)
}

// fetchListPage used by the subtests. Wraps the HTTP path with
// query-param filling + JSON decoding.
func (f *fakeDriveServer) fetchListPage(ctx context.Context, pageToken string) ([]string, string, error) {
	u := f.URL + "/drive/v3/files?pageSize=100"
	if pageToken != "" {
		u += "&pageToken=" + pageToken
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, "", err
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	var parsed struct {
		Files []struct {
			ID string `json:"id"`
		} `json:"files"`
		NextPageToken string `json:"nextPageToken"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, "", err
	}
	ids := make([]string, 0, len(parsed.Files))
	for _, f := range parsed.Files {
		ids = append(ids, f.ID)
	}
	return ids, parsed.NextPageToken, nil
}

// listCallCount returns the number of List calls observed.
func (f *fakeDriveServer) listCallCount() int64 {
	return atomic.LoadInt64(&f.listCalls)
}

// ----- fakeYouTubeServer -----

type fakeYouTubeServer struct {
	*httptest.Server
	mu         sync.Mutex
	crashAt    int64 // 0 = never crash; >0 = close after accepting this offset
	chunkHits  int64 // atomic counter for chunk PUT calls
	sessionSeq uint64
	sessions   map[string]resumableSession
}

type resumableSession struct {
	offset     int64
	totalBytes int64
}

func newFakeYouTubeServer() *fakeYouTubeServer {
	y := &fakeYouTubeServer{
		sessions: make(map[string]resumableSession),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/upload/youtube/v3/videos", y.handleResumableUpload)
	mux.HandleFunc("/session/", y.handleSessionPut)
	mux.HandleFunc("/youtube/v3/videos", y.handleVideoList)
	y.Server = httptest.NewServer(mux)
	return y
}

func (y *fakeYouTubeServer) Reset() {
	atomic.StoreInt64(&y.crashAt, 0)
	atomic.StoreInt64(&y.chunkHits, 0)
	atomic.StoreUint64(&y.sessionSeq, 0)
	y.mu.Lock()
	y.sessions = make(map[string]resumableSession)
	y.mu.Unlock()
}

func (y *fakeYouTubeServer) handleResumableUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sessionPath := fmt.Sprintf("/session/%d", atomic.AddUint64(&y.sessionSeq, 1))
	y.mu.Lock()
	y.sessions[sessionPath] = resumableSession{}
	y.mu.Unlock()

	// The session URI is unique per initiation and remains valid after
	// a simulated transport crash. Only this session's state changes.
	w.Header().Set("Location", y.URL+sessionPath)
	w.WriteHeader(http.StatusOK)
}

func (y *fakeYouTubeServer) handleSessionPut(w http.ResponseWriter, r *http.Request) {
	atomic.AddInt64(&y.chunkHits, 1)

	rangeHeader := r.Header.Get("Content-Range")
	if rangeHeader == "" {
		http.Error(w, "missing Content-Range", http.StatusBadRequest)
		return
	}

	// Serialize the complete session operation. This prevents two
	// concurrent PUTs from validating the same offset and then racing
	// their state updates, which could otherwise regress or skip the
	// persisted resumable boundary.
	y.mu.Lock()
	defer y.mu.Unlock()
	session, ok := y.sessions[r.URL.Path]
	if !ok {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	// A resumable client can query the server's current boundary
	// without sending a body. Validate the requested total once a
	// chunk has established the session's canonical total.
	if statusTotalText, ok := strings.CutPrefix(rangeHeader, "bytes */"); ok {
		statusTotal, err := strconv.ParseInt(statusTotalText, 10, 64)
		if err != nil || statusTotal <= 0 {
			http.Error(w, "invalid Content-Range status query", http.StatusBadRequest)
			return
		}
		if session.totalBytes != 0 && statusTotal != session.totalBytes {
			http.Error(w, fmt.Sprintf("expected total %d, got %d", session.totalBytes, statusTotal), http.StatusBadRequest)
			return
		}
		if session.offset > 0 {
			w.Header().Set("Range", fmt.Sprintf("bytes=0-%d", session.offset-1))
		}
		w.WriteHeader(statusResumeIncomplete)
		return
	}

	rangeSpec, ok := strings.CutPrefix(rangeHeader, "bytes ")
	if !ok {
		http.Error(w, "invalid Content-Range", http.StatusBadRequest)
		return
	}
	bounds, totalText, ok := strings.Cut(rangeSpec, "/")
	if !ok || strings.Contains(totalText, "/") {
		http.Error(w, "invalid Content-Range", http.StatusBadRequest)
		return
	}
	startText, endText, ok := strings.Cut(bounds, "-")
	if !ok || strings.Contains(endText, "-") {
		http.Error(w, "invalid Content-Range", http.StatusBadRequest)
		return
	}
	start, startErr := strconv.ParseInt(startText, 10, 64)
	end, endErr := strconv.ParseInt(endText, 10, 64)
	total, totalErr := strconv.ParseInt(totalText, 10, 64)
	if startErr != nil || endErr != nil || totalErr != nil || start < 0 || end < start || total <= end {
		http.Error(w, "invalid Content-Range", http.StatusBadRequest)
		return
	}
	if start != session.offset {
		http.Error(w, fmt.Sprintf("expected offset %d, got %d", session.offset, start), http.StatusRequestedRangeNotSatisfiable)
		return
	}
	if session.totalBytes != 0 && total != session.totalBytes {
		http.Error(w, fmt.Sprintf("expected total %d, got %d", session.totalBytes, total), http.StatusBadRequest)
		return
	}

	chunk, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read upload chunk", http.StatusBadRequest)
		return
	}
	expectedChunkLength := end - start + 1
	if int64(len(chunk)) != expectedChunkLength {
		http.Error(w, fmt.Sprintf("expected chunk length %d, got %d", expectedChunkLength, len(chunk)), http.StatusBadRequest)
		return
	}

	newOffset := end + 1
	session.offset = newOffset
	session.totalBytes = total
	y.sessions[r.URL.Path] = session

	if crash := atomic.LoadInt64(&y.crashAt); crash > 0 && newOffset >= crash {
		// The server accepted the chunk before the transport died. Keep
		// the session URI and committed offset so a new worker can issue
		// the next range against the same resumable session.
		if hj, ok := w.(http.Hijacker); ok {
			conn, _, err := hj.Hijack()
			if err == nil && conn != nil {
				_ = conn.Close()
				return
			}
		}
		http.Error(w, "crash", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Range", fmt.Sprintf("bytes=0-%d", newOffset-1))
	w.WriteHeader(statusResumeIncomplete)
}

func (y *fakeYouTubeServer) handleVideoList(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"items":[]}`)
}

// openResumableSession simulates step-1 of YouTube chunked upload.
func (y *fakeYouTubeServer) openResumableSession(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		y.URL+"/upload/youtube/v3/videos?uploadType=resumable&part=snippet,status",
		strings.NewReader(`{"snippet":{"title":"e2e"}}`))
	if err != nil {
		return "", err
	}
	req.Header.Set("X-Upload-Content-Type", "video/mp4")
	req.Header.Set("X-Upload-Content-Length", "5242880")
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("initiate: %d", resp.StatusCode)
	}
	return resp.Header.Get("Location"), nil
}

// putChunk simulates step-3 of YouTube chunked upload. Respects
// the crashAt setting: when >0, the connection is hijacked-closed
// (scenarios) so the client sees an EOF.
func (y *fakeYouTubeServer) putChunk(ctx context.Context, sessionURI string, body []byte, startByte, endByte, totalBytes int64) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, sessionURI, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", startByte, endByte, totalBytes))
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == statusResumeIncomplete {
		return nil
	}
	return fmt.Errorf("chunk PUT status %d", resp.StatusCode)
}

// chunkHitCount returns the number of chunk PUTs observed.
func (y *fakeYouTubeServer) chunkHitCount() int64 {
	return atomic.LoadInt64(&y.chunkHits)
}

// ----- fakeVeloxServer -----

type fakeVeloxServer struct {
	*httptest.Server
	mu              sync.Mutex
	deliveredSHA    map[string]string // idempotency_key → SHA stamped at first delivery
	callbacksPosted int64             // atomic
	callbackLog     []veloxCallbackEntry
}

type veloxCallbackEntry struct {
	URL       string
	Body      []byte
	Timestamp time.Time
}

func newFakeVeloxServer() *fakeVeloxServer {
	v := &fakeVeloxServer{
		deliveredSHA: make(map[string]string),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/source/artifact", v.handleArtifact)
	mux.HandleFunc("/v1/callback", v.handleCallback)
	v.Server = httptest.NewServer(mux)
	return v
}

func (v *fakeVeloxServer) Reset() {
	atomic.StoreInt64(&v.callbacksPosted, 0)
	v.mu.Lock()
	v.deliveredSHA = make(map[string]string)
	v.callbackLog = nil
	v.mu.Unlock()
}

// handleArtifact mirrors the production idempotency contract.
//
//   - first delivery (no entry in deliveredSHA): insert, return
//     200 with X-Instaedit-Artifact-Sha256 stamped in the header.
//
//   - same key + same SHA replay: lookup SHA, matches, return 200
//     (the SAME artifact body). No duplicate row, no side effects.
//
//   - same key + different SHA: lookup SHA, mismatches, return
//     409 conflict.
//
// The override X-Override-Sha256 simulates the "client sent an
// idempotent replay but with a different body" case. The actual
// body's SHA is computed live (sha256.Sum256(body)) and stamped in
// the X-Instaedit-Artifact-Sha256 response header.
func (v *fakeVeloxServer) handleArtifact(w http.ResponseWriter, r *http.Request) {
	idem := r.Header.Get("X-Idempotency-Key")
	overrideSHA := r.Header.Get("X-Override-Sha256")

	body := make([]byte, 16*1024)
	for i := range body {
		body[i] = 'A'
	}
	actual := sha256.Sum256(body)
	realSHA := hex.EncodeToString(actual[:])

	v.mu.Lock()
	prior, exists := v.deliveredSHA[idem]
	if !exists {
		stamped := realSHA
		if overrideSHA != "" {
			stamped = overrideSHA
		}
		v.deliveredSHA[idem] = stamped
		v.mu.Unlock()
		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("X-Instaedit-Artifact-Sha256", stamped)
		_, _ = w.Write(body)
		return
	}
	v.mu.Unlock()

	replaySHA := realSHA
	if overrideSHA != "" {
		replaySHA = overrideSHA
	}
	if replaySHA != prior {
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, fmt.Sprintf(`{"error":"sha_mismatch","expected":"%s","got":"%s"}`, prior, replaySHA))
		return
	}
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("X-Instaedit-Artifact-Sha256", prior)
	_, _ = w.Write(body)
}

func (v *fakeVeloxServer) handleCallback(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	v.mu.Lock()
	v.callbackLog = append(v.callbackLog, veloxCallbackEntry{
		URL:       r.URL.String(),
		Body:      body,
		Timestamp: time.Now(),
	})
	v.mu.Unlock()
	atomic.AddInt64(&v.callbacksPosted, 1)
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, `{"acknowledged":true}`)
}

// fetchArtifact invokes handleArtifact's HTTP path with the
// supplied idem-key + override-SHA.
func (v *fakeVeloxServer) fetchArtifact(ctx context.Context, idemKey, overrideSHA string) (body []byte, status int, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.URL+"/v1/source/artifact", nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("X-Idempotency-Key", idemKey)
	if overrideSHA != "" {
		req.Header.Set("X-Override-Sha256", overrideSHA)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, _ = io.ReadAll(resp.Body)
	return body, resp.StatusCode, nil
}

// simulateCallback invokes handleCallback's HTTP path.
func (v *fakeVeloxServer) simulateCallback(deliveryID string, payload []byte) error {
	req, err := http.NewRequest(http.MethodPost, v.URL+"/v1/callback?delivery_id="+deliveryID, strings.NewReader(string(payload)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("callback status %d", resp.StatusCode)
	}
	return nil
}

// signHMAC computes the SHA-256 HMAC of the body using
// sharedSecret, hex-encoded, prefixed with the canonical
// GitHub-webhook-style `sha256=` tag. Mirrors the production
// callback-verifier contract (internal/services/velox_callback_dispatcher.go
// + callback verifier); the E2E scenario 11 exercises the same
// shape so a future drift in production flips this test.
func (v *fakeVeloxServer) signHMAC(body []byte, sharedSecret string) string {
	h := hmac.New(sha256.New, []byte(sharedSecret))
	h.Write(body)
	return "sha256=" + hex.EncodeToString(h.Sum(nil))
}

// callVerifyHMAC is the canonical InstaEdit-side verifier. It
// recomputes the HMAC over body with the supplied secret and
// compares against the supplied signature via subtle.ConstantTimeCompare
// to avoid timing attacks. Returns nil on match, error on mismatch.
// Mirrors the production `handleCallback` body where the wrapper
// computes + compares.
func (v *fakeVeloxServer) callVerifyHMAC(body []byte, signature, sharedSecret string) error {
	expected := v.signHMAC(body, sharedSecret)
	if subtle.ConstantTimeCompare([]byte(expected), []byte(signature)) != 1 {
		return fmt.Errorf("hmac mismatch: expected %q, got %q", expected, signature)
	}
	return nil
}

// simulateSignedCallback combines sign + post in a single helper.
// Used by scenario 11's end-to-end check: a real InstaEdit handler
// receiving a real signed callback from Velox would compute +
// verify before acting on the body. The fake just records the
// callback; the assertion lives in scenario_11.
func (v *fakeVeloxServer) simulateSignedCallback(deliveryID string, payload []byte, sharedSecret string) error {
	req, err := http.NewRequest(http.MethodPost, v.URL+"/v1/callback?delivery_id="+deliveryID, strings.NewReader(string(payload)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hub-Signature-256", v.signHMAC(payload, sharedSecret))
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("signed callback status %d", resp.StatusCode)
	}
	return nil
}
