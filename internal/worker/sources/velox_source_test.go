package sources

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// testFixtureSHA is the canonical test artifact's SHA. Computed
// at init time from testFixtureBody (shared by every test that
// needs the expected hex). Tests that intentionally want a
// MISMATCH compute their own divergent hash via deriveMismatchHex.
var (
	testFixtureBody = []byte("test artifact bytes for VeloxSource httptest server")
	testFixtureSHA  = sha256Hex(testFixtureBody)
)

// sha256Hex is a tiny stdlib helper used by the test fixture
// declaration. Returns lowercase hex per the canonical form
// (matches what the VeloxSource.Interface.go verifies via
// verifySHAConstantTime).
func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	out := make([]byte, 64)
	const hexChars = "0123456789abcdef"
	for i := 0; i < 32; i++ {
		out[2*i] = hexChars[sum[i]>>4]
		out[2*i+1] = hexChars[sum[i]&0x0F]
	}
	return string(out)
}

// newVeloxTestServer builds an httptest.Server that mimics a Velox
// download endpoint. The handlers return canned responses per call:
//   - GET  /artifact/<id>: serves testFixtureBody with Content-Length
//     and ETag.
//   - HEAD /artifact/<id>: returns 200 + Content-Length + Content-Type +
//     ETag (the response shape Inspect reads).
//
// Tests can mutate srv.serveBody to swap the body (size / sha tests)
// or srv.statusOverride to force non-2xx for the error-path cases.
type veloxTestServer struct {
	*httptest.Server
	mu            sync.Mutex
	serveBody     []byte
	etag          string
	statusOverride int // 0 → use default 200; non-zero → return this status
	contentType   string
}

func newVeloxTestServer(t *testing.T) *veloxTestServer {
	t.Helper()
	srv := &veloxTestServer{
		serveBody:   append([]byte(nil), testFixtureBody...),
		etag:        `"fixture-etag-v1"`,
		contentType: "video/mp4",
	}
	srv.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		srv.mu.Lock()
		defer srv.mu.Unlock()
		if !strings.HasPrefix(r.URL.Path, "/artifact/") {
			http.NotFound(w, r)
			return
		}
		status := 200
		if srv.statusOverride != 0 {
			status = srv.statusOverride
		}
		w.Header().Set("Content-Type", srv.contentType)
		w.Header().Set("ETag", srv.etag)
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(srv.serveBody)))
		w.WriteHeader(status)
		if r.Method == http.MethodHead {
			return
		}
		_, _ = w.Write(srv.serveBody)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// helper that returns a fake UploadJob with download_url pointing
// at the test server's /artifact/<foo> endpoint.
func makeVeloxJob(t *testing.T, srv *veloxTestServer) *models.UploadJob {
	t.Helper()
	return &models.UploadJob{
		ID:         42,
		UserID:     1,
		SourceType: models.UploadJobSourceVeloxArtifact,
		SourceID:   srv.URL + "/artifact/test",
	}
}

// drainReader drains rc fully into a buffer (simulating storage.Upload).
// Returns the bytes read + any error.
func drainReader(t *testing.T, rc io.ReadCloser) ([]byte, error) {
	t.Helper()
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, rc); err != nil {
		return nil, err
	}
	if err := rc.Close(); err != nil {
		return buf.Bytes(), err
	}
	return buf.Bytes(), nil
}

// =============================================================================
// user-spec-mandated test cases (≥7)
// =============================================================================

// 1. Happy path: Open returns 200 + size + sha match → upload succeeds.
// User spec: "Persiste in InstaEdit storage via storage.Upload(ctx,
// reader, key). Ritorna SourceMetadata{sha256, size, mime}".
func TestVeloxSource_Open_HappyPath(t *testing.T) {
	srv := newVeloxTestServer(t)
	src := NewVeloxSource(nil)
	job := makeVeloxJob(t, srv)
	ctx := context.Background()

	stream, err := src.Open(ctx, job, int64(len(testFixtureBody)), testFixtureSHA)
	if err != nil {
		t.Fatalf("Open happy: %v", err)
	}
	body, _ := drainReader(t, stream)
	if !bytes.Equal(body, testFixtureBody) {
		t.Errorf("drained bytes mismatch (got %d, want %d)", len(body), len(testFixtureBody))
	}
	actualSize, actualSHA, _ := stream.Result()
	if actualSize != int64(len(testFixtureBody)) {
		t.Errorf("actual size: want %d, got %d", len(testFixtureBody), actualSize)
	}
	if actualSHA != testFixtureSHA {
		t.Errorf("actual sha: want %s, got %s", testFixtureSHA, actualSHA)
	}
	if err := verifySizeExact(actualSize, int64(len(testFixtureBody))); err != nil {
		t.Errorf("verifySizeExact: %v", err)
	}
	if err := verifySHAConstantTime(testFixtureSHA, actualSHA); err != nil {
		t.Errorf("verifySHAConstantTime: %v", err)
	}
}

// 2. Size mismatch (over): server sends more bytes than expected
// → PermanentError{Code:"ARTIFACT_SIZE_MISMATCH"} after the drain.
func TestVeloxSource_Open_SizeMismatchOver(t *testing.T) {
	srv := newVeloxTestServer(t)
	srv.mu.Lock()
	srv.serveBody = append([]byte(nil), testFixtureBody...)
	srv.serveBody = append(srv.serveBody, []byte("EXTRA_PADDING_50_BYTES_xxxxxxxxxxxxxxxxxx")...)
	srv.mu.Unlock()

	src := NewVeloxSource(nil)
	job := makeVeloxJob(t, srv)
	stream, err := src.Open(context.Background(), job,
		int64(len(testFixtureBody)), testFixtureSHA)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// Drain — the limitExpected cap will early-EOF the read
	// when the underlying body has more than expectedSize+1 bytes.
	body, _ := drainReader(t, stream)
	actualSize, _, _ := stream.Result()
	if actualSize == int64(len(testFixtureBody)) {
		t.Errorf("expected truncated read; got full size %d", actualSize)
	}
	if err := verifySizeExact(actualSize, int64(len(testFixtureBody))); err == nil {
		t.Errorf("size mismatch over: want error, got nil")
	} else {
		var pe PermanentError
		if !errors.As(err, &pe) {
			t.Errorf("error type: want PermanentError, got %T", err)
		} else if pe.Code != CodeArtifactSizeMismatch {
			t.Errorf("Code: want %s, got %s", CodeArtifactSizeMismatch, pe.Code)
		}
	}
	if int64(len(body)) != actualSize {
		t.Errorf("body len %d != Result size %d", len(body), actualSize)
	}
}

// 3. Size mismatch (short): server sends fewer bytes than expected
// → PermanentError on post-Read compare.
func TestVeloxSource_Open_SizeMismatchShort(t *testing.T) {
	srv := newVeloxTestServer(t)
	srv.mu.Lock()
	shortBody := testFixtureBody[:len(testFixtureBody)-5] // 5 bytes short
	srv.serveBody = append([]byte(nil), shortBody...)
	srv.mu.Unlock()

	src := NewVeloxSource(nil)
	job := makeVeloxJob(t, srv)
	stream, err := src.Open(context.Background(), job,
		int64(len(testFixtureBody)), testFixtureSHA)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	_, _ = drainReader(t, stream)
	actualSize, _, _ := stream.Result()
	if actualSize == int64(len(testFixtureBody)) {
		t.Errorf("expected short size; got expectedSize %d", actualSize)
	}
	if err := verifySizeExact(actualSize, int64(len(testFixtureBody))); err == nil {
		t.Errorf("size mismatch short: want error, got nil")
	} else {
		var pe PermanentError
		if !errors.As(err, &pe) {
			t.Errorf("error type: want PermanentError, got %T", err)
		} else if pe.Code != CodeArtifactSizeMismatch {
			t.Errorf("Code: want %s, got %s", CodeArtifactSizeMismatch, pe.Code)
		}
	}
}

// 4. SHA256 mismatch: server sends different bytes than expected
// → PermanentError{Code:"ARTIFACT_SHA256_MISMATCH"} on post-Read
// constant-time compare.
func TestVeloxSource_Open_SHA256Mismatch(t *testing.T) {
	srv := newVeloxTestServer(t)
	srv.mu.Lock()
	// Same size — different bytes. Reader sees the alternated
	// body, computes a different SHA, post-Read compare fails.
	altered := append([]byte(nil), testFixtureBody...)
	for i := range altered {
		altered[i] ^= 0x55 // flip every byte's low bits
	}
	srv.serveBody = altered
	srv.mu.Unlock()

	src := NewVeloxSource(nil)
	job := makeVeloxJob(t, srv)
	stream, err := src.Open(context.Background(), job,
		int64(len(testFixtureBody)), testFixtureSHA)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	_, _ = drainReader(t, stream)
	actualSize, actualSHA, _ := stream.Result()
	if err := verifySizeExact(actualSize, int64(len(testFixtureBody))); err != nil {
		// size matches; sha differs — should NOT trip size check.
		t.Errorf("size matches; unexpected size error: %v", err)
	}
	if err := verifySHAConstantTime(testFixtureSHA, actualSHA); err == nil {
		t.Errorf("sha mismatch: want error, got nil")
	} else {
		var pe PermanentError
		if !errors.As(err, &pe) {
			t.Errorf("error type: want PermanentError, got %T", err)
		} else if pe.Code != CodeArtifactSHA256Mismatch {
			t.Errorf("Code: want %s, got %s", CodeArtifactSHA256Mismatch, pe.Code)
		}
	}
}

// 5. Inspect HEAD: server returns 200 + Content-Length + ETag +
// Content-Type → metadata extracted.
func TestVeloxSource_Inspect_HEADHappy(t *testing.T) {
	srv := newVeloxTestServer(t)
	src := NewVeloxSource(nil)
	job := makeVeloxJob(t, srv)
	meta, err := src.Inspect(context.Background(), job)
	if err != nil {
		t.Fatalf("Inspect happy: %v", err)
	}
	if meta == nil {
		t.Fatal("Inspect returned nil metadata for happy body")
	}
	if meta.SizeBytes != int64(len(testFixtureBody)) {
		t.Errorf("SizeBytes: want %d, got %d", len(testFixtureBody), meta.SizeBytes)
	}
	if meta.MimeType != "video/mp4" {
		t.Errorf("MimeType: want video/mp4, got %s", meta.MimeType)
	}
	if meta.ETag != `"fixture-etag-v1"` {
		t.Errorf("ETag: want %q, got %q", `"fixture-etag-v1"`, meta.ETag)
	}
}

// 6. Inspect HEAD 404: returns error (worker should retry as
// transient — not a PermanentError).
func TestVeloxSource_Inspect_HEAD404(t *testing.T) {
	srv := newVeloxTestServer(t)
	srv.mu.Lock()
	srv.statusOverride = http.StatusNotFound
	srv.mu.Unlock()
	src := NewVeloxSource(nil)
	job := makeVeloxJob(t, srv)
	_, err := src.Inspect(context.Background(), job)
	if err == nil {
		t.Fatal("Inspect 404: want error, got nil")
	}
	var pe PermanentError
	if errors.As(err, &pe) {
		t.Errorf("404 should be transient, not PermanentError: %v", err)
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error message should mention 404: %v", err)
	}
}

// 7. Open GET 5xx: returns typed transient error (worker should
// retry — not a PermanentError).
func TestVeloxSource_Open_GET5xx(t *testing.T) {
	srv := newVeloxTestServer(t)
	srv.mu.Lock()
	srv.statusOverride = http.StatusInternalServerError
	srv.mu.Unlock()
	src := NewVeloxSource(nil)
	job := makeVeloxJob(t, srv)
	_, err := src.Open(context.Background(), job,
		int64(len(testFixtureBody)), testFixtureSHA)
	if err == nil {
		t.Fatal("Open 5xx: want error, got nil")
	}
	var pe PermanentError
	if errors.As(err, &pe) {
		t.Errorf("5xx should be transient, not PermanentError: %v", err)
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error message should mention 500: %v", err)
	}
}

// =============================================================================
// additional edge-case tests (recommended by reviewer for total coverage)
// =============================================================================

// 8. resolveDownloadURL routing per SourceID shape.
func TestResolveDownloadURL(t *testing.T) {
	cases := []struct {
		name string
		job  *models.UploadJob
		want string
	}{
		{
			name: "nil job",
			job:  nil,
			want: "",
		},
		{
			name: "empty SourceID",
			job:  &models.UploadJob{},
			want: "",
		},
		{
			name: "http URL",
			job:  &models.UploadJob{SourceID: "http://example.com/artifact"},
			want: "http://example.com/artifact",
		},
		{
			name: "https URL",
			job:  &models.UploadJob{SourceID: "https://velox.internal/a/b"},
			want: "https://velox.internal/a/b",
		},
		{
			name: "non-URL SourceID (ULID-shaped)",
			job:  &models.UploadJob{SourceID: "artifact_01JXYZ"},
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveDownloadURL(tc.job); got != tc.want {
				t.Errorf("resolveDownloadURL(%s): want %q, got %q", tc.name, tc.want, got)
			}
		})
	}
}

// 9. NewVeloxSource nil logger → defaults to slog.Default().
func TestNewVeloxSource_NilLoggerDefaults(t *testing.T) {
	s := NewVeloxSource(nil)
	if s == nil {
		t.Fatal("NewVeloxSource(nil): want non-nil struct")
	}
	if s.client == nil {
		t.Fatal("NewVeloxSource(nil): want non-nil client")
	}
	if s.logger == nil {
		t.Fatal("NewVeloxSource(nil): want non-nil logger (slog.Default fallback)")
	}
}

// 10. veloxArtifactStream Result before Close → ErrStreamNotClosed.
func TestVeloxArtifactStream_ResultBeforeClose(t *testing.T) {
	srv := newVeloxTestServer(t)
	src := NewVeloxSource(nil)
	job := makeVeloxJob(t, srv)
	stream, err := src.Open(context.Background(), job,
		int64(len(testFixtureBody)), testFixtureSHA)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// Defer close so we don't leak the underlying body.
	defer stream.Close()
	// Read just one byte so the hasher starts, but DON'T Close.
	buf := make([]byte, 1)
	if _, err := stream.Read(buf); err != nil {
		t.Fatalf("Read(1): %v", err)
	}
	if _, _, err := stream.Result(); !errors.Is(err, ErrStreamNotClosed) {
		t.Errorf("Result before Close: want ErrStreamNotClosed, got %v", err)
	}
}

// 11. veloxArtifactStream idempotent Close — second Close returns nil.
func TestVeloxArtifactStream_DoubleClose(t *testing.T) {
	srv := newVeloxTestServer(t)
	src := NewVeloxSource(nil)
	job := makeVeloxJob(t, srv)
	stream, err := src.Open(context.Background(), job,
		int64(len(testFixtureBody)), testFixtureSHA)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Errorf("first Close: %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Errorf("second Close (idempotent): want nil, got %v", err)
	}
}

// 12. Open with over-cap expected size → PermanentError immediately
// (no HTTP call). The veloxMaxArtifactBytes guard fires
// before the GET.
func TestVeloxSource_Open_OverCapExpectedSize(t *testing.T) {
	srv := newVeloxTestServer(t)
	src := NewVeloxSource(nil)
	job := makeVeloxJob(t, srv)
	// Force expected size over cap (2 GiB).
	over := veloxMaxArtifactBytes + 1
	_, err := src.Open(context.Background(), job, over, testFixtureSHA)
	if err == nil {
		t.Fatal("Open over cap: want error, got nil")
	}
	var pe PermanentError
	if !errors.As(err, &pe) {
		t.Errorf("error type: want PermanentError, got %T", err)
	}
	if pe.Code != CodeArtifactSizeMismatch {
		t.Errorf("Code: want %s, got %s", CodeArtifactSizeMismatch, pe.Code)
	}
}

// 13. Open with empty expected SHA → PermanentError (worker can't
// verify without it).
func TestVeloxSource_Open_EmptyExpectedSHA(t *testing.T) {
	srv := newVeloxTestServer(t)
	src := NewVeloxSource(nil)
	job := makeVeloxJob(t, srv)
	_, err := src.Open(context.Background(), job,
		int64(len(testFixtureBody)), "")
	if err == nil {
		t.Fatal("Open empty sha: want error, got nil")
	}
	var pe PermanentError
	if !errors.As(err, &pe) {
		t.Errorf("error type: want PermanentError, got %T", err)
	}
	if pe.Code != CodeArtifactSHA256Mismatch {
		t.Errorf("Code: want %s, got %s", CodeArtifactSHA256Mismatch, pe.Code)
	}
}

// 14. Inspect with empty download URL → error (worker shouldn't call).
func TestVeloxSource_Inspect_EmptyURL(t *testing.T) {
	src := NewVeloxSource(nil)
	job := &models.UploadJob{
		SourceType: models.UploadJobSourceVeloxArtifact,
		SourceID:   "not-a-url",
	}
	_, err := src.Inspect(context.Background(), job)
	if err == nil {
		t.Fatal("Inspect empty url: want error, got nil")
	}
}

// 15. TestNewVeloxSource_HTTPMechAcrossTimings — sanity-check that
// the http.Client timeout is configured to a sensible value (not
// the zero value). The worker's hot path must have a sane upper
// bound; a zero timeout would abort every call instantly.
func TestNewVeloxSource_HTTPTimeoutConfigured(t *testing.T) {
	s := NewVeloxSource(nil)
	if s.client.Timeout == 0 {
		t.Errorf("http.Client.Timeout is zero — would abort every Velox call instantly")
	}
	if s.client.Timeout < 30*time.Second {
		t.Errorf("http.Client.Timeout too short (%v) — multi-GiB artifacts would abort", s.client.Timeout)
	}
}
