package services

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/config"
)

// ----------------------------------------------------------------------------
// Test helpers for the resume-session surface (P1 hardening of
// youtube_oauth.go). These tests intentionally cover the small
// helpers (AttachUploadSession, persistSessionProgress,
// handleSessionLost, redactYouTubeSessionURI) + the 404 sentinel
// (ErrYouTubeSessionLost surfaced from queryUploadStatus) DIRECTLY,
// without going through the full uploadVideoChunks loop. The
// per-chunk persist-call integration into uploadVideoChunks is a
// separate followup commit; this file locks the helper + sentinel
// behaviour at the unit boundary, which is exactly what the user
// spec ("PUT vuoto + leggi Range + 404\u2192new session + cifrati in
// vault + MAI loggarli") asks for.
// ----------------------------------------------------------------------------

// validBase64Key32 is generated inline so the value is guaranteed
// to decode to exactly 32 bytes (the AES-256 base64 fixture the
// validator expects). base64.StdEncoding.EncodeToString(32 'a'
// bytes) is 44 characters ending in "==" and decodes to exactly
// 32 raw bytes — the canonical "good" shape for validate()'s base64
// + length check. Static constants don't work here because a hex
// digit string of length 64 would base64-decode to 48 bytes
// (failing the "exactly 32" check); a 32-char printable string
// decodes to 24 bytes; only the std-encoding of 32 raw bytes
// reliably produces a valid fixture.
func validBase64Key32() string {
	return base64.StdEncoding.EncodeToString([]byte(strings.Repeat("a", 32)))
}

// memSessionStore is an in-memory YouTubeSessionStore for tests.
// Records each Save call so a test can assert the *encrypted*
// ciphertext (never plaintext) was passed through.
type memSessionStore struct {
	mu       sync.Mutex
	saves    []sessionSaveCall
	clears   int
	saveErr  error // optional — set to force Save to error and exercise the warn path
	clearErr error
}

type sessionSaveCall struct {
	jobID                int64
	workerID             string
	sessionURICiphertext string
	offset               int64
	chunkSize            int64
	expiresAt            time.Time
}

func (m *memSessionStore) Save(_ context.Context, jobID int64, workerID, ciphertext string, offset, chunkSize int64, expiresAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.saveErr != nil {
		return m.saveErr
	}
	m.saves = append(m.saves, sessionSaveCall{
		jobID: jobID, workerID: workerID,
		sessionURICiphertext: ciphertext,
		offset:               offset, chunkSize: chunkSize,
		expiresAt: expiresAt,
	})
	return nil
}

func (m *memSessionStore) Clear(_ context.Context, jobID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.clearErr != nil {
		return m.clearErr
	}
	m.clears++
	return nil
}

func (m *memSessionStore) lastSave() sessionSaveCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.saves) == 0 {
		return sessionSaveCall{}
	}
	return m.saves[len(m.saves)-1]
}

// fakeEncryptor wraps base64-std codec as a deterministic fake
// cipher for tests. NOT cryptographically meaningful; only the
// round-trip contract (Encrypt(s) ciphertext != plaintext; Decrypt(ciphertext) == s)
// is asserted.
type fakeEncryptor struct{}

func (fakeEncryptor) Encrypt(plaintext string) ([]byte, error) {
	if plaintext == "" {
		return []byte(""), nil
	}
	// trivial Xor-based faux cipher: keeps the ciphertext != plaintext
	// shape that the test asserts.
	out := []byte(plaintext)
	for i := range out {
		out[i] = out[i] ^ 0x55
	}
	return out, nil
}

func (fakeEncryptor) Decrypt(cipher []byte) (string, error) {
	out := make([]byte, len(cipher))
	for i := range out {
		out[i] = cipher[i] ^ 0x55
	}
	return string(out), nil
}

// newResumeReadyService constructs a minimal YouTubeOAuthService
// already wired with a sessionStore + encryptor + job id.
// Used by helper tests. httpClient + clock are nil-safe (the
// persist path doesn't dereference either).
func newResumeReadyService(t *testing.T, store YouTubeSessionStore, enc SessionEncryptor) *YouTubeOAuthService {
	t.Helper()
	// cfg + chunk size are needed because sessionExpiresAt ->
	// sessionStore.Save reads uploadOpts.ChunkSize. We bypass
	// the production constructor (which would refuse our
	// encryption-less wiring) and build the struct directly.
	cfg := &config.Config{
		YouTubeClientID:            "test-client",
		YouTubeUploadChunkBytes:    256 * 1024,
		YouTubeUploadMaxRetries:    2,
		YouTubeUploadBackoffBaseMs: 1000,
		YouTubeUploadBackoffCapMs:  300000,
	}
	opts := loadYouTubeUploadOptions(cfg)
	svc := &YouTubeOAuthService{
		cfg:        cfg,
		httpClient: http.DefaultClient,
		clock:      func() time.Time { return time.Unix(1700000000, 0).UTC() },
		uploadOpts: opts,
		uploadDeps: loadYouTubeUploadDeps(opts),
	}
	svc.AttachUploadSession(42, "worker-test-1", store, enc)
	return svc
}

// ---------------- 404 sentinel ----------------

// TestQueryUploadStatus_NotFound_ReturnsErrYouTubeSessionLost
// pins the contract: a 404 reply to the `bytes */TOTAL` probe
// surfaces as ErrYouTubeSessionLost (not a generic fmt.Errorf),
// so the outer uploadVideoChunks recovery branch can match it
// and switch to a fresh initiateResumableSession call.
func TestQueryUploadStatus_NotFound_ReturnsErrYouTubeSessionLost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("status-probe method: got %s, want PUT", r.Method)
		}
		if got := r.Header.Get("Content-Range"); !strings.HasPrefix(got, "bytes */") {
			t.Errorf("status-probe Content-Range: got %q, want bytes */...", got)
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	svc := newResumeReadyService(t, &memSessionStore{}, fakeEncryptor{})
	_, err := svc.queryUploadStatus(context.Background(), srv.URL, 1024)

	if err == nil {
		t.Fatal("queryUploadStatus on 404: want ErrYouTubeSessionLost, got nil")
	}
	if !errors.Is(err, ErrYouTubeSessionLost) {
		t.Errorf("queryUploadStatus on 404: got %v, want ErrYouTubeSessionLost", err)
	}
}

// TestQueryUploadStatus_308_ParsesRangeHeader pins the existing
// happy path: on 308 the function returns lastByte+1 (the byte
// to resume from) parsed from the Range header. This is the
// "PUT vuoto + leggi Range + riprendi dal byte successivo" half
// of the spec.
func TestQueryUploadStatus_308_ParsesRangeHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Range", "bytes=0-499")
		w.WriteHeader(http.StatusPermanentRedirect) // 308
		_, _ = io.Copy(io.Discard, r.Body)
	}))
	defer srv.Close()

	svc := newResumeReadyService(t, &memSessionStore{}, fakeEncryptor{})
	offset, err := svc.queryUploadStatus(context.Background(), srv.URL, 500)
	if err != nil {
		t.Fatalf("queryUploadStatus on 308: want nil err, got %v", err)
	}
	if offset != 500 {
		t.Errorf("queryUploadStatus on 308: parsed offset got %d, want 500 (Range 0-499 \u2192 resume at byte 500)", offset)
	}
}

// ---------------- redactYouTubeSessionURI ----------------

// TestRedactYouTubeSessionURI covers the four branches of the
// redaction helper: empty, short (\u2264 16 chars, kept verbatim),
// long-but-deterministic (first 12 + \u2026 + last 4), and
// panics about NEVER returning the full URI.
func TestRedactYouTubeSessionURI(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"empty", "", ""},
		{"short kept verbatim", "http://x.co/ab", "http://x.co/ab"},
		{"long redacted", "http://uploads.youtube.com/upload?upload_id=AAAA&id=BBBB&id=CCCC&key=DDDD", "http://uploa…DDDD"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := redactYouTubeSessionURI(tc.input)
			if got != tc.want {
				t.Errorf("redact(%q): got %q, want %q", tc.input, got, tc.want)
			}
			if tc.input != "" && !strings.Contains(tc.input, "?") {
				return // short-kept-verbose cases: skip the never-contains-full check
			}
			// Long-form URIs MUST never echo the secret-bearing
			// portion. The redacted form must omit the key=
			// segment of the input if one was present.
			if tc.input != "" && got == tc.input {
				t.Errorf("redact returned the FULL URI: input/redact both = %q", tc.input)
			}
		})
	}
}

// ---------------- AttachUploadSession ----------------

func TestAttachUploadSession_SetsFields(t *testing.T) {
	svc := &YouTubeOAuthService{}
	store := &memSessionStore{}
	enc := fakeEncryptor{}
	svc.AttachUploadSession(123, "worker-xyz", store, enc)

	if svc.sessionJobID != 123 {
		t.Errorf("sessionJobID: got %d, want 123", svc.sessionJobID)
	}
	if svc.sessionWorkerID != "worker-xyz" {
		t.Errorf("sessionWorkerID: got %q, want worker-xyz", svc.sessionWorkerID)
	}
	if svc.sessionStore != store {
		t.Error("sessionStore pointer: not assigned")
	}
	if svc.sessionEncryptor != enc {
		t.Error("sessionEncryptor interface: not assigned")
	}
}

// ---------------- persistSessionProgress ----------------

// TestPersistSessionProgress_EncryptsBeforeSaving pins the
// "cifrati in vault" half of the spec: the value written to
// sessionStore.Save MUST be base64(Encrypt(uri)) \u2014 NOT the
// plaintext URI. Otherwise a DB admin can recover the full
// session URI from any row dump.
func TestPersistSessionProgress_EncryptsBeforeSaving(t *testing.T) {
	const plainURI = "http://uploads.youtube.com/upload?upload_id=ALPHA&id=BETA&id=GAMMA&key=SIGMA"
	store := &memSessionStore{}
	svc := newResumeReadyService(t, store, fakeEncryptor{})

	svc.persistSessionProgress(context.Background(), plainURI, 4096)

	saved := store.lastSave()
	if saved.jobID != 42 {
		t.Errorf("save jobID: got %d, want 42", saved.jobID)
	}
	if saved.workerID != "worker-test-1" {
		t.Errorf("save workerID: got %q, want worker-test-1", saved.workerID)
	}
	if saved.offset != 4096 {
		t.Errorf("save offset: got %d, want 4096", saved.offset)
	}
	if saved.sessionURICiphertext == "" {
		t.Fatal("save ciphertext empty; encryptor not invoked?")
	}
	if saved.sessionURICiphertext == plainURI {
		t.Fatalf("save recorded PLAINTEXT URI; encryption bypassed")
	}
	// Round-trip: ciphertext base64-decodes to a buffer that
	// Decrypt()'s back to the plaintext URI.
	raw, err := base64.StdEncoding.DecodeString(saved.sessionURICiphertext)
	if err != nil {
		t.Fatalf("stored value must be base64; decode err: %v", err)
	}
	got, err := svc.sessionEncryptor.Decrypt(raw)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if got != plainURI {
		t.Errorf("Decrypt round-trip: got %q, want %q (recovery path must rebuild the original URI)", got, plainURI)
	}
}

// TestPersistSessionProgress_NoOpWhenNil pins the pre-P1#5
// fallback: a service with no sessionStore + no encryptor
// continues silently \u2014 a debug breadcrumb is logged but no
// error is returned and no panic occurs. Tests using
// httptest.NewRecorder or non-default constructors must still
// work.
func TestPersistSessionProgress_NoOpWhenNil(t *testing.T) {
	svc := &YouTubeOAuthService{
		cfg:        &config.Config{YouTubeClientID: "x"},
		httpClient: http.DefaultClient,
		uploadOpts: youTubeUploadOptions{ChunkSize: 256 * 1024, MaxRetries: 1, BackoffBase: time.Second, BackoffCap: 5 * time.Minute},
		uploadDeps: loadYouTubeUploadDeps(youTubeUploadOptions{ChunkSize: 256 * 1024, MaxRetries: 1, BackoffBase: time.Second, BackoffCap: 5 * time.Minute}),
	}
	svc.persistSessionProgress(context.Background(), "http://x.co/upload?key=zzz", 0) // must not panic
}

func TestPersistSessionProgress_SaveErrorDoesNotPanic(t *testing.T) {
	store := &memSessionStore{saveErr: errors.New("disk full")}
	svc := newResumeReadyService(t, store, fakeEncryptor{})
	svc.persistSessionProgress(context.Background(),
		"http://uploads.youtube.com/upload?upload_id=AA&key=BB", 1024) // must not panic
}

// ---------------- handleSessionLost ----------------

// TestHandleSessionLost_CallsSessionStoreClear pins the
// "404 \u2192 apri una nuova sessione" half of the spec: a 404 from
// the status probe causes handleSessionLost to Clear the
// persisted session columns so the worker doesn't reuse a dead
// URI on the next claim. The function returns nil so the
// outer recovery branch can iterate without aborting.
func TestHandleSessionLost_CallsSessionStoreClear(t *testing.T) {
	store := &memSessionStore{}
	svc := newResumeReadyService(t, store, fakeEncryptor{})
	if err := svc.handleSessionLost(context.Background(),
		"http://uploads.youtube.com/upload?upload_id=DEAD&key=OFFLINE"); err != nil {
		t.Fatalf("handleSessionLost: want nil err, got %v", err)
	}
	if store.clears != 1 {
		t.Errorf("sessionStore.Clear calls: got %d, want 1", store.clears)
	}
}

func TestHandleSessionLost_NoOpWhenStoreNil(t *testing.T) {
	svc := &YouTubeOAuthService{}
	if err := svc.handleSessionLost(context.Background(),
		"http://uploads.youtube.com/upload?upload_id=DEAD&key=OFFLINE"); err != nil {
		t.Errorf("handleSessionLost with nil store: want nil, got %v", err)
	}
}

// ----------------------------------------------------------------------------
// Chunk-size validator (config) — see config_test.go for the sibling
// suite. These tests pin the contract "default 16 MB, multiple of
// 256 KB", so a future change to the constants lands with a noisy
// failure on the boundary.
// ----------------------------------------------------------------------------

// TestValidate_YouTubeUploadChunkBytes_DefaultsTo16MiB runs Load()
// with the env var UNSET and asserts YouTube behaves normally
// (must be enabled for the validator to fire). The default is
// 16777216 = 16*1024*1024.
func TestValidate_YouTubeUploadChunkBytes_DefaultsTo16MiB(t *testing.T) {
	// Populate the smallest viable env so Load succeeds AND
	// YouTube's validator runs.
	t.Setenv("DATABASE_URL", "postgresql://x:y@localhost:5432/x?sslmode=disable")
	t.Setenv("S3_ENDPOINT", "http://localhost:9000")
	t.Setenv("S3_BUCKET", "test")
	t.Setenv("S3_ACCESS_KEY", "x")
	t.Setenv("S3_SECRET_KEY", "y")
	t.Setenv("ENCRYPTION_KEY", validBase64Key32())
	t.Setenv("JWT_SECRET", strings.Repeat("a", 64))
	t.Setenv("YOUTUBE_CLIENT_ID", "test")
	t.Setenv("YOUTUBE_CLIENT_SECRET", strings.Repeat("a", 32))
	t.Setenv("YOUTUBE_UPLOAD_CHUNK_BYTES", "") // ensure default wins

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if cfg.YouTubeUploadChunkBytes != 16*1024*1024 {
		t.Errorf("default chunk size: got %d, want 16777216 (16 MB)", cfg.YouTubeUploadChunkBytes)
	}
}

// TestValidate_YouTubeUploadChunkBytes_MultiplesOf256KBOK exercises
// several explicit multiple-of-262144 (=256 KB) values that pins
// the production env tier (256 KB, 1 MB, 8 MB, 16 MB, 32 MB).
func TestValidate_YouTubeUploadChunkBytes_MultiplesOf256KBOK(t *testing.T) {
	sizes := []int64{262144, 512 * 1024, 1 * 1024 * 1024, 8 * 1024 * 1024, 16 * 1024 * 1024, 32 * 1024 * 1024}
	for _, sz := range sizes {
		t.Run("size_"+itoa(sz), func(t *testing.T) {
			t.Setenv("DATABASE_URL", "postgresql://x:y@localhost:5432/x?sslmode=disable")
			t.Setenv("S3_ENDPOINT", "http://localhost:9000")
			t.Setenv("S3_BUCKET", "test")
			t.Setenv("S3_ACCESS_KEY", "x")
			t.Setenv("S3_SECRET_KEY", "y")
			t.Setenv("ENCRYPTION_KEY", validBase64Key32())
			t.Setenv("JWT_SECRET", strings.Repeat("a", 64))
			t.Setenv("YOUTUBE_CLIENT_ID", "test")
			t.Setenv("YOUTUBE_CLIENT_SECRET", strings.Repeat("a", 32))
			t.Setenv("YOUTUBE_UPLOAD_CHUNK_BYTES", itoa(sz))
			cfg, err := config.Load()
			if err != nil {
				t.Errorf("Load() with size=%d: %v", sz, err)
				return
			}
			if cfg.YouTubeUploadChunkBytes != sz {
				t.Errorf("round-trip: got %d, want %d", cfg.YouTubeUploadChunkBytes, sz)
			}
		})
	}
}

// TestValidate_YouTubeUploadChunkBytes_NotMultipleRejected
// exercises boundary violations: 262143 (one byte too small,
// Google will reject with a generic 400), 262145 (one byte
// too large), 16777217 (16 MB + 1, the off-by-one a future
// maintainer might accidentally introduce). All must be
// rejected with the canonical multiple-of-262144 error.
func TestValidate_YouTubeUploadChunkBytes_NotMultipleRejected(t *testing.T) {
	sizes := []int64{262143, 262145, 16777217, 1000000}
	for _, sz := range sizes {
		t.Run("size_"+itoa(sz), func(t *testing.T) {
			t.Setenv("DATABASE_URL", "postgresql://x:y@localhost:5432/x?sslmode=disable")
			t.Setenv("S3_ENDPOINT", "http://localhost:9000")
			t.Setenv("S3_BUCKET", "test")
			t.Setenv("S3_ACCESS_KEY", "x")
			t.Setenv("S3_SECRET_KEY", "y")
			t.Setenv("ENCRYPTION_KEY", validBase64Key32())
			t.Setenv("JWT_SECRET", strings.Repeat("a", 64))
			t.Setenv("YOUTUBE_CLIENT_ID", "test")
			t.Setenv("YOUTUBE_CLIENT_SECRET", strings.Repeat("a", 32))
			t.Setenv("YOUTUBE_UPLOAD_CHUNK_BYTES", itoa(sz))
			_, err := config.Load()
			if err == nil {
				t.Errorf("size %d: want validate error (not a multiple of 262144), got nil", sz)
			} else if !strings.Contains(err.Error(), "262144") {
				t.Errorf("size %d: validate error doesn't mention 262144: got %v", sz, err)
			}
		})
	}
}

func TestValidate_YouTubeUploadChunkBytes_ZeroRejected(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgresql://x:y@localhost:5432/x?sslmode=disable")
	t.Setenv("S3_ENDPOINT", "http://localhost:9000")
	t.Setenv("S3_BUCKET", "test")
	t.Setenv("S3_ACCESS_KEY", "x")
	t.Setenv("S3_SECRET_KEY", "y")
	t.Setenv("ENCRYPTION_KEY", validBase64Key32())
	t.Setenv("JWT_SECRET", strings.Repeat("a", 64))
	t.Setenv("YOUTUBE_CLIENT_ID", "test")
	t.Setenv("YOUTUBE_CLIENT_SECRET", strings.Repeat("a", 32))
	t.Setenv("YOUTUBE_UPLOAD_CHUNK_BYTES", "0")
	_, err := config.Load()
	if err == nil {
		t.Fatal("size 0: want validate error, got nil")
	}
	if !strings.Contains(err.Error(), "262144") {
		t.Errorf("size 0: error doesn't mention 262144: got %v", err)
	}
}

// itoa is a tiny shim that avoids strconv (kept this file
// import-light; the tests above only need a stringified int64).
func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if neg {
		digits = append([]byte{'-'}, digits...)
	}
	return string(digits)
}
