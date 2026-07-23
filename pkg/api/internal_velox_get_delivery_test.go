package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// fakeDeliveryStorage is the in-package ExternalDeliveryStore
// fake: exposes BOTH the Insert surface (POST handler) AND a
// GetByID method (the new GET handler) so the GET tests can
// seed rows directly. Production code uses
// *repository.ExternalDeliveryRepository which satisfies both
// surfaces structurally.
type fakeDeliveryStorage struct {
	rows       map[string]*models.ExternalDelivery
	lookupErr  error
	insertErr  error
	lastLookup string
}

func newFakeDeliveryStorage() *fakeDeliveryStorage {
	return &fakeDeliveryStorage{rows: map[string]*models.ExternalDelivery{}}
}

func (f *fakeDeliveryStorage) GetByID(_ context.Context, id string) (*models.ExternalDelivery, error) {
	f.lastLookup = id
	if f.lookupErr != nil {
		return nil, f.lookupErr
	}
	d, ok := f.rows[id]
	if !ok {
		return nil, nil
	}
	return d, nil
}

func (f *fakeDeliveryStorage) Insert(_ context.Context, e *models.ExternalDelivery, _ []byte) (*models.ExternalDelivery, error) {
	if f.insertErr != nil {
		return nil, f.insertErr
	}
	f.rows[e.ID] = e
	return e, nil
}

// fakeDestinationStorage is the stub used by newVeloxTestRouter
// to satisfy the route-guard inside registerInternalVeloxRoutes.
// The GET handler under test does NOT consult destinations, so the
// stub returns (nil, nil) on every lookup. Production code paths
// that hit the destinations store never exercise this stub (they
// use mockExternalDestinations in internal_velox_deliver_test.go).
type fakeDestinationStorage struct{}

func (f *fakeDestinationStorage) GetByID(_ context.Context, _ string) (*models.ExternalDestination, error) {
	return nil, nil
}

// Create is required by ExternalDestinationStore since the
// POST /internal/v1/deliveries cut added it. The GET handler
// under test never reaches Create (the POST tests use the
// richer fakeDestinationEnv in internal_velox_deliveries_test.go);
// this stub returns nil so the compile-time interface check
// and chi route-mount guard pass without touching a real DB.
func (f *fakeDestinationStorage) ListByWorkspace(_ context.Context, _ int64, _ bool) ([]models.ExternalDestination, error) {
	return nil, nil
}
func (f *fakeDestinationStorage) Delete(_ context.Context, _ string) error {
	return nil
}
func (f *fakeDestinationStorage) Create(_ context.Context, _ *models.ExternalDestination) error {
	return nil
}

// UpdateEnabled + UpdateDefaultMetadata stubs satisfy the
// expanded ExternalDestinationStore interface (PATCH endpoint
// expansion). The GET handler under test does NOT exercise
// either verb; stubs return nil so vet succeeds without forcing
// fixture refactors.
func (f *fakeDestinationStorage) UpdateEnabled(_ context.Context, _ string, _ bool) error {
	return nil
}
func (f *fakeDestinationStorage) UpdateDefaultMetadata(_ context.Context, _ string, _ json.RawMessage) error {
	return nil
}

// UpdateEnabledAndDefaults is the combined-verb stub. The
// GET-delivery handler does NOT exercise this verb; stub returns
// nil so vet succeeds without forcing unrelated fixture refactors.
func (f *fakeDestinationStorage) UpdateEnabledAndDefaults(_ context.Context, _ string, _ *bool, _ json.RawMessage) error {
	return nil
}

// compile-time assertion the fake satisfies the production
// interfaces. If the GET handler expands the interface I'll
// catch the drift here.
var (
	_ ExternalDeliveryStore = (*fakeDeliveryStorage)(nil)
)

// seedRow installs a delivery row at the given id with the
// supplied status + error/platform fields populated per the
// test's scenario. Returns the row id for assertions.
func (f *fakeDeliveryStorage) seedRow(id string, status models.ExternalDeliveryStatus, lastErrCode, lastErrMsg, platformMediaID, platformURL string, completedAt *time.Time) {
	row := &models.ExternalDelivery{
		ID:           id,
		SourceSystem: "velox",
		Status:       status,
	}
	if lastErrCode != "" {
		s := lastErrCode
		row.LastErrorCode = &s
	}
	if lastErrMsg != "" {
		s := lastErrMsg
		row.LastErrorMessage = &s
	}
	if platformMediaID != "" {
		s := platformMediaID
		row.PlatformMediaID = &s
	}
	if platformURL != "" {
		s := platformURL
		row.PlatformURL = &s
	}
	row.CompletedAt = completedAt
	f.rows[id] = row
}

// newVeloxTestRouter wires a Router with the deps the GET handler
// needs AND initializes mux so registerInternalVeloxRoutes() can
// mount the GET route on it. Mirrors the inline-construction
// pattern from buildDeliverRouter in internal_velox_deliver_test.go
// but adds mux: chi.NewRouter() — the runtime pkgs register routes
// ONTO this mux.
//
// We DELIBERATELY skip MustNewRouter(, WithOneTimeCodeStore(NewInMemoryOneTimeCodeStore(60 * time.Second))) because the GET handler under
// test does not depend on capRouter / auth.Manager / UserStore /
// frontendURL. Calling registerInternalVeloxRoutes() preserves
// the production route-guard semantics:
//   - externalDeliveries=nil OR veloxAPIToken=""  →  route NOT
//     mounted → chi returns 404 on any request.
//   - all deps configured → route mounted inside the
//     internalVeloxAuth middleware, so 401/403 fire BEFORE
//     the handler runs.
func newVeloxTestRouter(t *testing.T, deliveries ExternalDeliveryStore, token string) *Router {
	t.Helper()
	r := &Router{
		mux:                  chi.NewRouter(),
		externalDestinations: &fakeDestinationStorage{},
		externalDeliveries:   deliveries,
		veloxAPIToken:        token,
	}
	r.registerInternalVeloxRoutes()
	return r
}

// testSendRequest is a tiny helper that fires an HTTP request
// through the mux and returns the recorder. Avoids repeating
// the httptest boilerplate in every test case.
func testSendRequest(t *testing.T, r *Router, method, path, authHeader string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	w := httptest.NewRecorder()
	r.mux.ServeHTTP(w, req)
	return w
}

// TestHandleGetInternalDelivery_Happy_Accepted — sparse row,
// just the status + id serialised. last_error_code/platform
// fields must be omitted.
func TestHandleGetInternalDelivery_Happy_Accepted(t *testing.T) {
	store := newFakeDeliveryStorage()
	store.seedRow("sdel_01JABC", models.ExternalDeliveryStatusAccepted, "", "", "", "", nil)

	r := newVeloxTestRouter(t, store, "secret-token")
	w := testSendRequest(t, r, http.MethodGet, "/internal/v1/deliveries/sdel_01JABC", "Bearer secret-token")

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body=%s", w.Code, w.Body.String())
	}

	var got VeloxGetDeliveryResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Status != "accepted" {
		t.Errorf("Status = %q; want accepted", got.Status)
	}
	if got.RetryWaitReason != "" {
		t.Errorf("RetryWaitReason = %q; want empty for accepted row", got.RetryWaitReason)
	}
	if got.LastErrorCode != "" || got.LastErrorMessage != "" {
		t.Errorf("LastError* = %q/%q; want empty for accepted row",
			got.LastErrorCode, got.LastErrorMessage)
	}
	if got.PublishedAt != nil {
		t.Errorf("PublishedAt = %v; want nil for non-published row", got.PublishedAt)
	}
	body := w.Body.String()
	if strings.Contains(body, "retry_wait_reason") {
		t.Errorf("body should NOT contain retry_wait_reason for accepted row; got %s", body)
	}
}

// TestHandleGetInternalDelivery_Happy_RetryWait — populated row
// in retry_wait state. retry_wait_reason mirrors last_error_code.
func TestHandleGetInternalDelivery_Happy_RetryWait(t *testing.T) {
	store := newFakeDeliveryStorage()
	store.seedRow("sdel_01JABC", models.ExternalDeliveryStatusRetryWait,
		"auth_error", "401 invalid_grant from token endpoint", "", "", nil)

	r := newVeloxTestRouter(t, store, "secret-token")
	w := testSendRequest(t, r, http.MethodGet, "/internal/v1/deliveries/sdel_01JABC", "Bearer secret-token")

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body=%s", w.Code, w.Body.String())
	}
	var got VeloxGetDeliveryResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Status != "retry_wait" {
		t.Errorf("Status = %q; want retry_wait", got.Status)
	}
	if got.RetryWaitReason != "auth_error" {
		t.Errorf("RetryWaitReason = %q; want auth_error", got.RetryWaitReason)
	}
	if got.LastErrorCode != "auth_error" {
		t.Errorf("LastErrorCode = %q; want auth_error", got.LastErrorCode)
	}
	if got.LastErrorMessage != "401 invalid_grant from token endpoint" {
		t.Errorf("LastErrorMessage = %q; want 401 message", got.LastErrorMessage)
	}
}

// TestHandleGetInternalDelivery_Happy_Published — terminal
// success state with platform IDs + completed_at stamped.
// published_at MUST be set; platform URLs must surface.
func TestHandleGetInternalDelivery_Happy_Published(t *testing.T) {
	completedAt := time.Date(2026, 7, 20, 18, 3, 21, 0, time.UTC)
	store := newFakeDeliveryStorage()
	store.seedRow("sdel_01JABC", models.ExternalDeliveryStatusPublished,
		"", "",
		"dQw4w9WgXcQ", "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		&completedAt)

	r := newVeloxTestRouter(t, store, "secret-token")
	w := testSendRequest(t, r, http.MethodGet, "/internal/v1/deliveries/sdel_01JABC", "Bearer secret-token")

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body=%s", w.Code, w.Body.String())
	}
	var got VeloxGetDeliveryResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Status != "published" {
		t.Errorf("Status = %q; want published", got.Status)
	}
	if got.PlatformMediaID != "dQw4w9WgXcQ" {
		t.Errorf("PlatformMediaID = %q; want dQw4w9WgXcQ", got.PlatformMediaID)
	}
	if got.PlatformURL != "https://www.youtube.com/watch?v=dQw4w9WgXcQ" {
		t.Errorf("PlatformURL = %q; want youtube url", got.PlatformURL)
	}
	if got.PublishedAt == nil {
		t.Fatal("PublishedAt = nil; want completedAt timestamp for published row")
	}
	if got.PublishedAt != nil && !got.PublishedAt.Equal(completedAt) {
		t.Errorf("PublishedAt = %v; want %v", got.PublishedAt, completedAt)
	}
	// retry_wait_reason must be empty for published row even
	// though the same column is the reason source.
	if got.RetryWaitReason != "" {
		t.Errorf("RetryWaitReason = %q; want empty for published row", got.RetryWaitReason)
	}
}

// TestHandleGetInternalDelivery_NotFound — unknown id collapses
// to 404. Body uses standard writeError envelope.
func TestHandleGetInternalDelivery_NotFound(t *testing.T) {
	store := newFakeDeliveryStorage()
	r := newVeloxTestRouter(t, store, "secret-token")
	w := testSendRequest(t, r, http.MethodGet, "/internal/v1/deliveries/sdel_does_not_exist", "Bearer secret-token")

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want 404; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "delivery not found") {
		t.Errorf("body should mention 'delivery not found'; got %s", w.Body.String())
	}
}

// TestHandleGetInternalDelivery_StoreUnconfigured — when the
// router was built WITHOUT WithExternalDeliveryStore, the
// route-guard in registerInternalVeloxRoutes refuses to mount
// the GET route. The chi mux then returns 404 on any request
// that hits the path. Matches the same collapse-with-not-found
// semantic the validate handler uses for disabled destinations.
func TestHandleGetInternalDelivery_StoreUnconfigured(t *testing.T) {
	r := newVeloxTestRouter(t, nil, "secret-token")
	w := testSendRequest(t, r, http.MethodGet, "/internal/v1/deliveries/sdel_01JABC", "Bearer secret-token")

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want 404 (route-guard suppresses mount when store nil); body=%s",
			w.Code, w.Body.String())
	}
}

// TestHandleGetInternalDelivery_LookupFailure — repo returns
// non-nil error → 500. Body uses standard writeError shape.
func TestHandleGetInternalDelivery_LookupFailure(t *testing.T) {
	store := newFakeDeliveryStorage()
	store.lookupErr = errors.New("db connection reset")
	r := newVeloxTestRouter(t, store, "secret-token")
	w := testSendRequest(t, r, http.MethodGet, "/internal/v1/deliveries/sdel_01JABC", "Bearer secret-token")

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d; want 500; body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "delivery lookup failed") {
		t.Errorf("body should mention 'delivery lookup failed'; got %s", w.Body.String())
	}
}

// TestHandleGetInternalDelivery_AuthGated — the middleware
// returns 401 missing / 403 mismatch / 503 token-not-configured
// BEFORE the handler runs. Three assertions cover the spec.
func TestHandleGetInternalDelivery_AuthGated(t *testing.T) {
	store := newFakeDeliveryStorage()
	store.seedRow("sdel_01JABC", models.ExternalDeliveryStatusPublished,
		"", "", "x", "y", &time.Time{})

	// Sub-test 1: missing Authorization → 401.
	t.Run("missing_bearer", func(t *testing.T) {
		r := newVeloxTestRouter(t, store, "secret-token")
		w := testSendRequest(t, r, http.MethodGet, "/internal/v1/deliveries/sdel_01JABC", "")
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d; want 401", w.Code)
		}
	})

	// Sub-test 2: bearer mismatch → 403.
	t.Run("bearer_mismatch", func(t *testing.T) {
		r := newVeloxTestRouter(t, store, "secret-token")
		w := testSendRequest(t, r, http.MethodGet, "/internal/v1/deliveries/sdel_01JABC", "Bearer wrong-token")
		if w.Code != http.StatusForbidden {
			t.Fatalf("status = %d; want 403", w.Code)
		}
	})

	// Sub-test 3: empty token at boot → route-guard refuses to
	// mount the route (same reason as StoreUnconfigured) so chi
	// returns 404. The 503 path is only reachable when the route is
	// mounted manually without the guard (see runDeliver in
	// internal_velox_deliver_test.go). Production behaviour is what
	// this test covers — chi 404, NOT 503.
	t.Run("token_unconfigured", func(t *testing.T) {
		r := newVeloxTestRouter(t, store, "")
		w := testSendRequest(t, r, http.MethodGet, "/internal/v1/deliveries/sdel_01JABC", "Bearer anything")
		if w.Code != http.StatusNotFound {
			t.Fatalf("status = %d; want 404 (route-guard suppresses mount when token empty)", w.Code)
		}
	})
}
