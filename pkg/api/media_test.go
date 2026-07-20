package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// --- mockMediaStore ----------------------------------------------------------

// mockMediaStore is a small in-memory MediaStore for the media endpoint
// tests. Mirrors the pattern of mockPostStore / mockWorkspaceStore.
type mockMediaStore struct {
	assets map[string]*models.MediaAsset
	// errOnCreate / errOnFind / errOnMark let each test inject a
	// specific failure without rebuilding the mock.
	errOnCreate  error
	errOnFind    error
	errOnMarkRdy error
}

func newMockMediaStore() *mockMediaStore {
	return &mockMediaStore{assets: map[string]*models.MediaAsset{}}
}

func (m *mockMediaStore) Create(a *models.MediaAsset) error {
	if m.errOnCreate != nil {
		return m.errOnCreate
	}
	if a.ID == "" {
		a.ID = "00000000-0000-4000-8000-000000000001"
	}
	if a.Status == "" {
		a.Status = models.MediaAssetStatusPending
	}
	now := time.Now()
	a.CreatedAt = now
	a.UpdatedAt = now
	m.assets[a.ID] = a
	return nil
}

func (m *mockMediaStore) FindByID(id string) (*models.MediaAsset, error) {
	if m.errOnFind != nil {
		return nil, m.errOnFind
	}
	a, ok := m.assets[id]
	if !ok {
		return nil, nil
	}
	return a, nil
}

func (m *mockMediaStore) MarkReady(id, sha256 string, sizeBytes int64, contentType string) error {
	if m.errOnMarkRdy != nil {
		return m.errOnMarkRdy
	}
	a, ok := m.assets[id]
	if !ok {
		return errors.New("not found")
	}
	a.Status = models.MediaAssetStatusReady
	a.SHA256 = sha256
	a.SizeBytes = sizeBytes
	a.ContentType = contentType
	a.UpdatedAt = time.Now()
	return nil
}

func (m *mockMediaStore) MarkFailed(id, reason string) error {
	a, ok := m.assets[id]
	if !ok {
		return errors.New("not found")
	}
	a.Status = models.MediaAssetStatusFailed
	a.ErrorMessage = reason
	a.UpdatedAt = time.Now()
	return nil
}

// MarkFailedWithReason mirrors MarkFailed for the diagnose-friendly
// variant. The mock doesn't log (production logs via slog); tests
// that need to assert "the cause was preserved" can attach a
// captures reconcile path…
func (m *mockMediaStore) MarkFailedWithReason(id, reason string, cause error) error {
	_ = cause // cause is logged in production; not asserted in the happy-path tests.
	return m.MarkFailed(id, reason)
}

// --- mockStorageProvider -----------------------------------------------------

// mockStorageProvider implements services.StorageProvider for the
// media endpoint tests. SignUpload returns a stable UploadGrant;
// VerifyUpload is configurable per-test via the verifyFn hook.
type mockStorageProvider struct {
	providerName string
	signFn       func(key string) *services.UploadGrant
	verifyFn     func(key string) (string, int64, error)
	assetURLFn   func(key string) string
}

func newMockStorageProvider() *mockStorageProvider {
	return &mockStorageProvider{
		providerName: "mock",
		signFn: func(key string) *services.UploadGrant {
			return &services.UploadGrant{
				UploadURL: "https://mock-s3.example.com/" + key + "?X-Amz-Signature=mock",
				MediaURL:  "https://mock-s3.example.com/" + key,
				ExpiresAt: time.Now().Add(15 * time.Minute),
			}
		},
		verifyFn: func(key string) (string, int64, error) {
			return "image/jpeg", 1024, nil
		},
		assetURLFn: func(key string) string {
			return "https://mock-s3.example.com/" + key
		},
	}
}

func (m *mockStorageProvider) Provider() string { return m.providerName }
func (m *mockStorageProvider) SignUpload(_ context.Context, _ int64, key, _ string, _ int64, _ time.Duration) (*services.UploadGrant, error) {
	return m.signFn(key), nil
}
func (m *mockStorageProvider) VerifyUpload(_ context.Context, key string) (string, int64, error) {
	return m.verifyFn(key)
}
func (m *mockStorageProvider) AssetURL(key string) string { return m.assetURLFn(key) }

// --- helpers -----------------------------------------------------------------

func newMediaTestRouter(media MediaStore, storage StorageProvider) *Router {
	return NewRouter(
		services.NewCapabilityRouter(),
		&mockUserStore{},
		auth.NewManager(testJWTSecret, 24),
		"",
		nil,
		WithMediaStore(media),
		WithStorageProvider(storage),
		WithMaxUploadBytes(200*1024*1024),
	)
}

// --- /presign ----------------------------------------------------------------

func TestMedia_Presign_Happy_ReturnsAssetIDAndSignedURL(t *testing.T) {
	r := newMediaTestRouter(newMockMediaStore(), newMockStorageProvider())
	body := `{"filename":"hello.jpg","content_type":"image/jpeg","size_bytes":1024}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/media/presign", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 42)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp PresignMediaResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.AssetID == "" {
		t.Error("asset_id should be set")
	}
	if !strings.HasPrefix(resp.UploadURL, "https://mock-s3.example.com/") {
		t.Errorf("upload_url should be the mock signed URL, got %q", resp.UploadURL)
	}
	if resp.UploadMethod != http.MethodPut {
		t.Errorf("upload_method: want PUT, got %q", resp.UploadMethod)
	}
	if resp.MaxSizeBytes != 200*1024*1024 {
		t.Errorf("max_size_bytes: want 200MiB, got %d", resp.MaxSizeBytes)
	}
}

func TestMedia_Presign_RejectsBadContentType_422(t *testing.T) {
	r := newMediaTestRouter(newMockMediaStore(), newMockStorageProvider())
	body := `{"filename":"evil.html","content_type":"text/html","size_bytes":1024}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/media/presign", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d", w.Code)
	}
}

func TestMedia_Presign_RejectsZeroSize_422(t *testing.T) {
	r := newMediaTestRouter(newMockMediaStore(), newMockStorageProvider())
	body := `{"filename":"x.jpg","content_type":"image/jpeg","size_bytes":0}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/media/presign", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d", w.Code)
	}
}

func TestMedia_Presign_RejectsTooLarge_422(t *testing.T) {
	r := newMediaTestRouter(newMockMediaStore(), newMockStorageProvider())
	r.maxUploadBytes = 100 // 100 bytes cap
	body := `{"filename":"big.jpg","content_type":"image/jpeg","size_bytes":10000}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/media/presign", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d", w.Code)
	}
}

func TestMedia_Presign_NoJWT_401(t *testing.T) {
	r := newMediaTestRouter(newMockMediaStore(), newMockStorageProvider())
	body := `{"filename":"x.jpg","content_type":"image/jpeg","size_bytes":1024}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/media/presign", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
}

// --- /complete ---------------------------------------------------------------

func TestMedia_Complete_Happy_TransitionsToReady(t *testing.T) {
	store := newMockMediaStore()
	// Task 6/10 — happy path now requires a non-empty SHA on the asset
	// (the presign client commits it locally). Use a fixed 64-hex SHA
	// so the assertion below (verify SHA propagated to mediaStore)
	// fires deterministically.
	const happyPathSHA = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	store.assets["abc"] = &models.MediaAsset{
		ID: "abc", UserID: 1, UploadKey: "uploads/1/x.jpg",
		ContentType: "image/jpeg", SizeBytes: 1024,
		SHA256:    happyPathSHA,
		Status:    models.MediaAssetStatusPending,
		ExpiresAt: time.Now().Add(1 * time.Hour),
	}
	storage := newMockStorageProvider()
	storage.verifyFn = func(key string) (string, int64, error) {
		return "image/jpeg", 1024, nil // matches the asset
	}
	r := newMediaTestRouter(store, storage)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/media/abc/complete", nil)
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if store.assets["abc"].Status != models.MediaAssetStatusReady {
		t.Errorf("status: want ready, got %s", store.assets["abc"].Status)
	}
	// Task 6/10 contract: the SHA committed at presign must be the
	// same SHA written to media_assets.sha256 by MarkReady. Without
	// this assertion a future refactor of mockMediaStore.MarkReady
	// that dropped the SHA on the way through could pass silently.
	if store.assets["abc"].SHA256 != happyPathSHA {
		t.Errorf("sha256 propagation: want %q, got %q", happyPathSHA, store.assets["abc"].SHA256)
	}
}

// TestMedia_Complete_EmptySHA_400 — Task 6/10 enforcement. The presign
// body omitted sha256 (SHA="" on the Create call), so /complete must
// reject with 400 BEFORE touching S3 + BEFORE writing to mediaStore.
// The asset row is transitioned to MarkFailed so a follow-up retry
// surfaces the failure class in the operator dashboard (unlike the
// pre-Task-6/10 behaviour where empty SHA slipped through silently
// via the SQL COALESCE(NULLIF($2,''), sha256)).
func TestMedia_Complete_EmptySHA_400(t *testing.T) {
	store := newMockMediaStore()
	store.assets["abc"] = &models.MediaAsset{
		ID: "abc", UserID: 1, UploadKey: "uploads/1/x.jpg",
		ContentType: "image/jpeg", SizeBytes: 1024,
		SHA256:    "", // empty on purpose — Task 6/10 reject path
		Status:    models.MediaAssetStatusPending,
		ExpiresAt: time.Now().Add(1 * time.Hour),
	}
	storage := newMockStorageProvider()
	// Track whether storage was consulted — Task 6/10 must short-circuit
	// BEFORE the HEAD to avoid wasted S3 round-trip + bandwidth on a
	// request that's already known-failing.
	headCalls := 0
	storage.verifyFn = func(key string) (string, int64, error) {
		headCalls++
		return "image/jpeg", 1024, nil
	}
	r := newMediaTestRouter(store, storage)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/media/abc/complete", nil)
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %s", w.Code, w.Body.String())
	}
	if headCalls != 0 {
		t.Errorf("storageProvider.VerifyUpload was called %d times; want 0 (Task 6/10 short-circuit before S3 HEAD for known-failing requests)", headCalls)
	}
	if !strings.Contains(w.Body.String(), "sha256 required") {
		t.Errorf("error message should mention the sha256 requirement; got %q", w.Body.String())
	}
	// MarkFailed is acceptable as a diagnostic trail (so the operator
	// dashboard surfaces the failure class); we don't assert success
	// to keep the test focused on the 400 + early-reject contract.
	if store.assets["abc"].Status == models.MediaAssetStatusReady {
		t.Errorf("status must NOT be ready when SHA is empty (would imply the empty-SHA slipped through MarkReady); got %s", store.assets["abc"].Status)
	}
}

func TestMedia_Complete_NotFound_404(t *testing.T) {
	r := newMediaTestRouter(newMockMediaStore(), newMockStorageProvider())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/media/does-not-exist/complete", nil)
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}

func TestMedia_Complete_CrossOwner_404(t *testing.T) {
	store := newMockMediaStore()
	store.assets["abc"] = &models.MediaAsset{
		ID: "abc", UserID: 999, UploadKey: "uploads/999/x.jpg",
		ContentType: "image/jpeg", SizeBytes: 1024,
		Status:    models.MediaAssetStatusPending,
		ExpiresAt: time.Now().Add(1 * time.Hour),
	}
	r := newMediaTestRouter(store, newMockStorageProvider())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/media/abc/complete", nil)
	withBearerJWT(t, req, 1) // attacker is user 1, asset belongs to 999
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404 (don't leak existence), got %d", w.Code)
	}
}

func TestMedia_Complete_SizeMismatch_422_AndFailed(t *testing.T) {
	store := newMockMediaStore()
	// Task 6/10 — set a non-empty SHA on the asset so the upfront
	// empty-SHA reject does not fire; this test exercises the
	// size-mismatch path which runs LATER in the handler.
	store.assets["abc"] = &models.MediaAsset{
		ID: "abc", UserID: 1, UploadKey: "uploads/1/x.jpg",
		ContentType: "image/jpeg", SizeBytes: 1024,
		SHA256:    "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		Status:    models.MediaAssetStatusPending,
		ExpiresAt: time.Now().Add(1 * time.Hour),
	}
	storage := newMockStorageProvider()
	storage.verifyFn = func(key string) (string, int64, error) {
		return "image/jpeg", 999, nil // size mismatch
	}
	r := newMediaTestRouter(store, storage)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/media/abc/complete", nil)
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d: %s", w.Code, w.Body.String())
	}
	if store.assets["abc"].Status != models.MediaAssetStatusFailed {
		t.Errorf("status: want failed, got %s", store.assets["abc"].Status)
	}
}

func TestMedia_Complete_ContentTypeMismatch_422(t *testing.T) {
	store := newMockMediaStore()
	// See TestMedia_Complete_SizeMismatch_422_AndFailed rationale:
	// pre-set SHA so the empty-SHA short-circuit doesn't fire first.
	store.assets["abc"] = &models.MediaAsset{
		ID: "abc", UserID: 1, UploadKey: "uploads/1/x.jpg",
		ContentType: "image/jpeg", SizeBytes: 1024,
		SHA256:    "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		Status:    models.MediaAssetStatusPending,
		ExpiresAt: time.Now().Add(1 * time.Hour),
	}
	storage := newMockStorageProvider()
	storage.verifyFn = func(key string) (string, int64, error) {
		return "image/png", 1024, nil // content-type mismatch
	}
	r := newMediaTestRouter(store, storage)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/media/abc/complete", nil)
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d: %s", w.Code, w.Body.String())
	}
}

func TestMedia_Complete_Expired_410(t *testing.T) {
	store := newMockMediaStore()
	store.assets["abc"] = &models.MediaAsset{
		ID: "abc", UserID: 1, UploadKey: "uploads/1/x.jpg",
		ContentType: "image/jpeg", SizeBytes: 1024,
		Status:    models.MediaAssetStatusPending,
		ExpiresAt: time.Now().Add(-1 * time.Hour), // already expired
	}
	r := newMediaTestRouter(store, newMockStorageProvider())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/media/abc/complete", nil)
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusGone {
		t.Fatalf("want 410, got %d: %s", w.Code, w.Body.String())
	}
}

func TestMedia_Complete_IdempotentIfAlreadyReady_200(t *testing.T) {
	store := newMockMediaStore()
	store.assets["abc"] = &models.MediaAsset{
		ID: "abc", UserID: 1, UploadKey: "uploads/1/x.jpg",
		ContentType: "image/jpeg", SizeBytes: 1024,
		Status:    models.MediaAssetStatusReady,
		ExpiresAt: time.Now().Add(1 * time.Hour),
	}
	r := newMediaTestRouter(store, newMockStorageProvider())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/media/abc/complete", nil)
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
}

// --- AssetURL contract (the chokepoint for SSRF prevention) -----------------

func TestStorageProvider_AssetURL_ReturnsTrustedInternalURL(t *testing.T) {
	storage := newMockStorageProvider()
	url := storage.AssetURL("uploads/1/abc.jpg")
	if !strings.HasPrefix(url, "https://mock-s3.example.com/") {
		t.Errorf("asset_url should be the trusted internal URL, got %q", url)
	}
}
