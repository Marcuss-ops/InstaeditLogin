//go:build e2e

// Package e2e bundles the Task 9/10 Definition of Done end-to-end
// suite. The harness spins up Postgres via testcontainers-go and
// three in-process httptest fakes for Google Drive, YouTube, and
// Velox. Each subtest under TestPipelineE2E exercises one
// acceptance criterion from the source document.
//
// Build tag: tests in this package are gated behind `-tags=e2e`
// so `go test ./...` does NOT run them by default (Docker +
// ~3-5 s of container spin-up is not part of the developer inner
// loop). Operators / CI invoke `make test-e2e`.
package e2e

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // pgx stdlib driver for sql.Open("pgx", ...)
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// statusResumeIncomplete is the YouTube resumable-upload protocol's
// mid-stream response code. Go's stdlib has no constant for it
// (https://developers.google.com/youtube/v3/guides/resumable_uploads
// states: "After each chunk upload, the server returns HTTP 308
// Resume Incomplete"). Defined centrally so handler + client agree.
const statusResumeIncomplete = 308

// E2EHarness is the shared fixture for TestPipelineE2E. It spins up
// Postgres via testcontainers-go (already in go.mod) and exposes
// in-process httptest fakes for Drive, YouTube, and Velox. The 7
// t.Run subtests under TestPipelineE2E reuse this harness.
//
// Spec divergence note: the Task 9/10 source document asks for
// docker-compose with MinIO + Postgres + fakes. We drop the MinIO
// testcontainer to keep the e2e suite dependency-light and ship
// it as a tracked follow-up (Task 9.10 follow-up). In-process
// verify-policy + internal/services/storage_test.go cover the S3
// write/read path; only ~10 lines of additional test plumbing
// are pending once we decide to add MinIO + aws-sdk-v2.
type E2EHarness struct {
	t *testing.T

	pgContainer testcontainers.Container
	pgDB        *sql.DB
	pgURL       string

	driveFake   *fakeDriveServer
	youTubeFake *fakeYouTubeServer
	veloxFake   *fakeVeloxServer

	HTTPClient *http.Client
}

// NewE2EHarness spins up a Postgres container + applies the e2e
// schema bootstrap + boots the 3 httptest fakes. Returns nil on
// Docker-unavailable so the runner can `t.Skip` cleanly instead of
// failing.
func NewE2EHarness(t *testing.T) *E2EHarness {
	t.Helper()

	h := &E2EHarness{t: t}
	h.driveFake = newFakeDriveServer()
	h.youTubeFake = newFakeYouTubeServer()
	h.veloxFake = newFakeVeloxServer()

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	pgC, err := tcpostgres.RunContainer(ctx,
		testcontainers.WithImage("postgres:17-alpine"),
		tcpostgres.WithDatabase("instaedit_e2e"),
		tcpostgres.WithUsername("postgres"),
		tcpostgres.WithPassword("postgres"),
	)
	if err != nil {
		t.Skipf("testcontainers: cannot start Postgres (Docker unavailable?): %v", err)
		return nil
	}
	h.pgContainer = pgC

	pgURL, err := pgC.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		_ = pgC.Terminate(context.Background())
		t.Fatalf("postgres connection string: %v", err)
	}
	h.pgURL = pgURL

	db, err := sql.Open("pgx", pgURL)
	if err != nil {
		_ = pgC.Terminate(context.Background())
		t.Fatalf("sql.Open: %v", err)
	}
	// Pin the pool to 4 conns: testcontainers-go postgres with
	// ~1 listener + ~4 simultaneous INSERTs is plenty.
	db.SetMaxOpenConns(4)
	h.pgDB = db

	if err := applyE2ESchema(h.pgDB); err != nil {
		_ = h.pgDB.Close()
		_ = pgC.Terminate(context.Background())
		t.Fatalf("applyE2ESchema: %v", err)
	}

	h.HTTPClient = &http.Client{
		Transport: rewriteRoundTripper(h.driveFake.URL, h.youTubeFake.URL),
		Timeout:   30 * time.Second,
	}

	t.Logf("E2EHarness ready: postgres=%s drive=%s youtube=%s velox=%s",
		pgURL, h.driveFake.URL, h.youTubeFake.URL, h.veloxFake.URL)
	return h
}

// Close brings down containers + closes the sql.DB + fake servers.
// Safe to call multiple times.
func (h *E2EHarness) Close() {
	if h == nil {
		return
	}
	if h.pgDB != nil {
		_ = h.pgDB.Close()
	}
	if h.pgContainer != nil {
		_ = h.pgContainer.Terminate(context.Background())
	}
	if h.driveFake != nil {
		h.driveFake.Close()
	}
	if h.youTubeFake != nil {
		h.youTubeFake.Close()
	}
	if h.veloxFake != nil {
		h.veloxFake.Close()
	}
}

// ResetFakes wipes the per-subtest mutable state on the fakes.
func (h *E2EHarness) ResetFakes() {
	h.driveFake.Reset()
	h.youTubeFake.Reset()
	h.veloxFake.Reset()
}

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
	mu        sync.Mutex
	crashAt   int64 // 0 = never crash; >0 = crash every request
	chunkHits int64 // atomic counter for chunk PUT calls
}

func newFakeYouTubeServer() *fakeYouTubeServer {
	y := &fakeYouTubeServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("/upload/youtube/v3/videos", y.handleResumableUpload)
	mux.HandleFunc("/youtube/v3/videos", y.handleVideoList)
	y.Server = httptest.NewServer(mux)
	return y
}

func (y *fakeYouTubeServer) Reset() {
	atomic.StoreInt64(&y.crashAt, 0)
	atomic.StoreInt64(&y.chunkHits, 0)
	y.mu.Lock()
	y.mu.Unlock()
}

func (y *fakeYouTubeServer) handleResumableUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		w.Header().Set("Location", y.URL+"/session-xyz")
		w.WriteHeader(http.StatusOK)
		return
	}
	atomic.AddInt64(&y.chunkHits, 1)
	rangeHdr := r.Header.Get("Content-Range")
	if rangeHdr == "" {
		http.Error(w, "missing Content-Range", http.StatusBadRequest)
		return
	}
	if crash := atomic.LoadInt64(&y.crashAt); crash > 0 {
		// Simulate a mid-upload crash: hijack the connection and
		// close it so the client sees EOF on read.
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
	w.Header().Set("Range", rangeHdr)
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

// ----- route-rewriting RoundTripper ------------------------------------
//
// Production Go clients (services/auth, services/youtube_resumable)
// construct raw HTTPS URLs; this RoundTripper intercepts at the
// transport layer and rewrites *.googleapis.com to the in-process
// Drive/YouTube fakes. Without it, the suite can't exercise those
// code paths in-process.

type rewriteRT struct {
	driveURL   string
	youtubeURL string
}

func (rt *rewriteRT) RoundTrip(req *http.Request) (*http.Response, error) {
	u := req.URL.String()
	for _, prefix := range []string{"https://www.googleapis.com/", "https://oauth2.googleapis.com/"} {
		if strings.HasPrefix(u, prefix) {
			req2 := req.Clone(req.Context())
			rewritten := strings.Replace(u, prefix, rt.driveURL+"/", 1)
			parsed, err := url.Parse(rewritten)
			if err != nil {
				return nil, err
			}
			req2.URL = parsed
			return http.DefaultTransport.RoundTrip(req2)
		}
	}
	for _, prefix := range []string{"https://youtube.googleapis.com/", "https://www.youtube.com/"} {
		if strings.HasPrefix(u, prefix) {
			req2 := req.Clone(req.Context())
			rewritten := strings.Replace(u, prefix, rt.youtubeURL+"/", 1)
			parsed, err := url.Parse(rewritten)
			if err != nil {
				return nil, err
			}
			req2.URL = parsed
			return http.DefaultTransport.RoundTrip(req2)
		}
	}
	return http.DefaultTransport.RoundTrip(req)
}

func rewriteRoundTripper(driveURL, youtubeURL string) http.RoundTripper {
	return &rewriteRT{driveURL: driveURL, youtubeURL: youtubeURL}
}

// ----- helpers exposed to subtests via the harness ---------------------

// sha256Hex returns the hex-encoded SHA-256 of the byte slice.
func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// bytesEqual is a constant-time-friendly bytes comparison.
func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// applyE2ESchema bootstraps the minimal Postgres schema the e2e
// suite needs. We don't apply the production migration list
// because (a) the test only queries a handful of tables and (b)
// embedding the migration runner would force every test to
// materialize columns the suite never reads. CREATE TABLE IF NOT
// EXISTS keeps the bootstrap idempotent across re-runs.
func applyE2ESchema(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id BIGSERIAL PRIMARY KEY,
			email TEXT NOT NULL UNIQUE,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE TABLE IF NOT EXISTS workspaces (
			id BIGSERIAL PRIMARY KEY,
			name TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		// Channels = platform_accounts in production. The exact
		// column set isn't material for scenario_5 (which only
		// references ID) so we keep the boot surface minimal.
		`CREATE TABLE IF NOT EXISTS platform_accounts (
			id BIGSERIAL PRIMARY KEY,
			platform TEXT NOT NULL,
			platform_user_id TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		// posts: user_id + workspace_id + status + publish_at
		// cover scenario_5. Other columns are present for shape
		// parity with the production migration so any future
		// assertion that talks to posts won't trip on a missing
		// column.
		`CREATE TABLE IF NOT EXISTS posts (
			id BIGSERIAL PRIMARY KEY,
			user_id BIGINT NOT NULL,
			workspace_id BIGINT NOT NULL,
			title TEXT NOT NULL DEFAULT '',
			caption TEXT NOT NULL DEFAULT '',
			media_url TEXT NOT NULL DEFAULT '',
			thumbnail_url TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'scheduled',
			publish_at TIMESTAMPTZ NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		// post_targets: scenario_5 only exercises INSERT (no
		// SELECT) — minimal column set.
		`CREATE TABLE IF NOT EXISTS post_targets (
			id BIGSERIAL PRIMARY KEY,
			post_id BIGINT NOT NULL,
			platform_account_id BIGINT NOT NULL,
			status TEXT NOT NULL DEFAULT 'pending',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("apply schema stmt: %s: %w", trimForError(s), err)
		}
	}
	return nil
}

// trimForError shortens a SQL stmt for error messages so the test
// log stays readable when bootstrap fails.
func trimForError(s string) string {
	const max = 80
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
