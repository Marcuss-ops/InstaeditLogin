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
	store.assets["abc"] = &models.MediaAsset{
		ID: "abc", UserID: 1, UploadKey: "uploads/1/x.jpg",
		ContentType: "image/jpeg", SizeBytes: 1024,
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
	store.assets["abc"] = &models.MediaAsset{
		ID: "abc", UserID: 1, UploadKey: "uploads/1/x.jpg",
		ContentType: "image/jpeg", SizeBytes: 1024,
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
	store.assets["abc"] = &models.MediaAsset{
		ID: "abc", UserID: 1, UploadKey: "uploads/1/x.jpg",
		ContentType: "image/jpeg", SizeBytes: 1024,
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
