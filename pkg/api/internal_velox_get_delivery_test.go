package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// -----------------------------------------------------------------------
// Existing v0 fixtures (preserved verbatim for backward compat).
// -----------------------------------------------------------------------

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

// -----------------------------------------------------------------------
// Spec §8 fixtures (new — supports the target/privacy/publish_status/
// thumbnail_status/youtube_video_id fields).
// -----------------------------------------------------------------------

// seedRowExt installs an ExternalDelivery row with the full
// field set surfaced via the new spec §8 GET handler
// (ExternalDestinationID + Metadata + platform media id +
// canonical YouTube alias). The 4 extra string params mirror
// the v0 seedRow shape so existing tests can pass "" when they
// don't care.
func (f *fakeDeliveryStorage) seedRowExt(
	id string,
	status models.ExternalDeliveryStatus,
	externalDestinationID string,
	lastErrCode, lastErrMsg, platformMediaID, platformURL string,
	completedAt *time.Time,
	metadata json.RawMessage,
) {
	row := &models.ExternalDelivery{
		ID:                    id,
		SourceSystem:          "velox",
		ExternalDestinationID: externalDestinationID,
		Status:                status,
		Metadata:              metadata,
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

// fakeDestinationStorageExtended substitutes fakeDestinationStorage
// in the new §8 fixtures; supports a seeded rows map so the
// target-resolution FK chain returns real ExternalDestination rows.
type fakeDestinationStorageExtended struct {
	rows map[string]*models.ExternalDestination
}

func (f *fakeDestinationStorageExtended) GetByID(_ context.Context, id string) (*models.ExternalDestination, error) {
	if d, ok := f.rows[id]; ok {
		return d, nil
	}
	return nil, nil
}
func (f *fakeDestinationStorageExtended) ListByWorkspace(_ context.Context, _ int64, _ bool) ([]models.ExternalDestination, error) {
	return nil, nil
}
func (f *fakeDestinationStorageExtended) Delete(_ context.Context, _ string) error { return nil }
func (f *fakeDestinationStorageExtended) Create(_ context.Context, _ *models.ExternalDestination) error {
	return nil
}
func (f *fakeDestinationStorageExtended) UpdateEnabledAndDefaults(_ context.Context, _ string, _ *bool, _ json.RawMessage) error {
	return nil
}

// fakeUserStoreSpec8 backs the spec §8 target resolution FK chain
// (FindPlatformAccountByID surfaces real rows; other interface
// methods are no-op stubs so accidentally reaching one doesn't
// panic the test).
//
// UserStore interface signatures (production: pkg/api/router.go):
//   - AttachPlatformAccount(userID, profile, platform) (*PlatformAccount, error)
//   - ListPlatformAccountsByUser(userID, platform) ([]*PlatformAccount, error)
//   - ListFilteredYouTubeAccounts(userID, workspaceID, group, lang, manager) …
//   - FindPlatformAccountByID(id) …
//   - FindPlatformAccount(platform, platformUserID) …
//   - UpdatePlatformAccount(account) error
//
// None of the UserStore methods take context.Context (legacy
// interface; the repo wraps the DB internally). My prior stub set
// had ctx on FindPlatformAccountByID — removed.
type fakeUserStoreSpec8 struct {
	UserStore
	rows map[int64]*models.PlatformAccount
}

func (f *fakeUserStoreSpec8) FindPlatformAccountByID(id int64) (*models.PlatformAccount, error) {
	if pa, ok := f.rows[id]; ok {
		return pa, nil
	}
	return nil, nil
}

// Stub methods below are no-ops (returning nil/empty) so the
// fake satisfies the UserStore interface even when the handler
// doesn't exercise them in our §8-target tests. Specifically:
// AttachPlatformAccount, ListPlatformAccountsByUser,
// ListFilteredYouTubeAccounts, FindPlatformAccount,
// UpdatePlatformAccount — none are reached by
// handleGetInternalDelivery.resolveDeliveryTarget.
func (f *fakeUserStoreSpec8) AttachPlatformAccount(_ int64, _ *models.PlatformProfile, _ string) (*models.PlatformAccount, error) {
	return nil, nil
}
func (f *fakeUserStoreSpec8) ListPlatformAccountsByUser(_ int64, _ string) ([]*models.PlatformAccount, error) {
	return nil, nil
}
func (f *fakeUserStoreSpec8) ListFilteredYouTubeAccounts(_ int64, _ *int64, _, _, _ string) ([]*models.PlatformAccount, error) {
	return nil, nil
}
func (f *fakeUserStoreSpec8) FindPlatformAccount(_, _ string) (*models.PlatformAccount, error) {
	return nil, nil
}
func (f *fakeUserStoreSpec8) UpdatePlatformAccount(_ *models.PlatformAccount) error {
	return nil
}

// fakeWorkspaceStoreSpec8 backs the spec §8 target resolution FK
// chain (FindByID + FindChannel surface real rows; methods not
// used by the handler are no-op stubs so accidentally reaching one
// doesn't panic the test).
//
// WorkspaceStore interface signatures (production: pkg/api/router.go):
//   - Create(w *models.Workspace) error             — NO ctx (legacy interface)
//   - FindByID(id int64) (*models.Workspace, error)
//   - ListByOwner(ownerID int64) ([]models.Workspace, error)
//   - Delete(id int64) error
//   - AttachChannel(ctx, wsID, acctID, groupName string) (*models.WorkspaceChannel, error)
//   - ListChannels(ctx, wsID) ([]models.WorkspaceChannel, error)
//   - UpdateChannel(ctx, wsID, acctID, groupName *string, enabled *bool) error
//   - DetachChannel(ctx, wsID, acctID) error
//   - FindChannel(ctx, wsID, acctID) (*models.WorkspaceChannel, error)
//
// Mismatch with my prior stub set: Create/FindByID/ListByOwner/Delete
// don't take ctx (legacy interface predates chi/v5); AttachChannel
// returns *models.WorkspaceChannel (not just error). All fixed below.
type fakeWorkspaceStoreSpec8 struct {
	WorkspaceStore
	workspaces map[int64]*models.Workspace
	bindings   map[string]*models.WorkspaceChannel
}

func (f *fakeWorkspaceStoreSpec8) FindByID(id int64) (*models.Workspace, error) {
	if w, ok := f.workspaces[id]; ok {
		return w, nil
	}
	return nil, nil
}
func (f *fakeWorkspaceStoreSpec8) FindChannel(_ context.Context, wsID, accountID int64) (*models.WorkspaceChannel, error) {
	key := wsKey(wsID, accountID)
	if b, ok := f.bindings[key]; ok {
		return b, nil
	}
	return nil, nil
}
func (f *fakeWorkspaceStoreSpec8) ListChannels(_ context.Context, _ int64) ([]models.WorkspaceChannel, error) {
	return nil, nil
}
func (f *fakeWorkspaceStoreSpec8) AttachChannel(_ context.Context, _, _ int64, _ string) (*models.WorkspaceChannel, error) {
	return nil, nil
}
func (f *fakeWorkspaceStoreSpec8) DetachChannel(_ context.Context, _, _ int64) error {
	return nil
}
func (f *fakeWorkspaceStoreSpec8) UpdateChannel(_ context.Context, _, _ int64, _ *string, _ *bool) error {
	return nil
}
func (f *fakeWorkspaceStoreSpec8) ListByOwner(_ int64) ([]models.Workspace, error) {
	return nil, nil
}
func (f *fakeWorkspaceStoreSpec8) Delete(_ int64) error {
	return nil
}
func (f *fakeWorkspaceStoreSpec8) Create(_ *models.Workspace) error {
	return nil
}

// wsKey is the composite key for the (workspace_id, platform_account_id)
// map in fakeWorkspaceStoreSpec8.bindings — needs to round-trip
// int64s with no risk of collision across workspaces. fmt.Sprintf
// handles negative values + MinInt64 correctly; the previous
// custom formatInt was fragile around two's-complement edge cases.
func wsKey(workspaceID, accountID int64) string {
	return fmt.Sprintf("%d:%d", workspaceID, accountID)
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

// newVeloxTestRouterWithDeps extends newVeloxTestRouter to wire the
// full set of dependencies the spec §8 handler reaches
// (ExternalDeliveryStore + ExternalDestinationStore + UserStore +
// WorkspaceStore). Used by TestHandleGetInternalDelivery_Spec8_*
// tests that need target resolution to fire.
func newVeloxTestRouterWithDeps(
	t *testing.T,
	deliveries ExternalDeliveryStore,
	destinations ExternalDestinationStore,
	userStore UserStore,
	workspaceStore WorkspaceStore,
	token string,
) *Router {
	t.Helper()
	r := &Router{
		mux:                  chi.NewRouter(),
		externalDestinations: destinations,
		externalDeliveries:   deliveries,
		userRepo:             userStore,
		workspaceStore:       workspaceStore,
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

// -----------------------------------------------------------------------
// Existing v0 tests (preserved verbatim for backward compat).
// -----------------------------------------------------------------------

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

// -----------------------------------------------------------------------
// Spec §8 tests (the new shape).
// -----------------------------------------------------------------------

// TestHandleGetInternalDelivery_Spec8_PublishStatus_MappingExhaustive
// pins the 11-value → 6-value mapping every (status, publish_status)
// pair. Exhaustive table so a future enum extension fails loudly.
func TestHandleGetInternalDelivery_Spec8_PublishStatus_MappingExhaustive(t *testing.T) {
	cases := []struct {
		in   models.ExternalDeliveryStatus
		want string
	}{
		{models.ExternalDeliveryStatusAccepted, "waiting_thumbnail"},
		{models.ExternalDeliveryStatusDownloading, "waiting_thumbnail"},
		{models.ExternalDeliveryStatusArtifactVerified, "waiting_thumbnail"},
		{models.ExternalDeliveryStatusIngestCompleted, "waiting_thumbnail"},
		{models.ExternalDeliveryStatusPublishing, "waiting_thumbnail"},
		{models.ExternalDeliveryStatusQueued, "scheduled"},
		{models.ExternalDeliveryStatusRetryWait, "retry_wait"},
		{models.ExternalDeliveryStatusBlockedAuth, "blocked"},
		{models.ExternalDeliveryStatusPublished, "published"},
		{models.ExternalDeliveryStatusFailed, "failed"},
		{models.ExternalDeliveryStatusDeadLetter, "failed"},
	}
	for _, tc := range cases {
		t.Run(string(tc.in), func(t *testing.T) {
			if got := mapExternalDeliveryStatusToPublishStatus(tc.in); got != tc.want {
				t.Errorf("ExternalDeliveryStatus(%q) → publish_status want %q, got %q",
					tc.in, tc.want, got)
			}
		})
	}
}

// TestHandleGetInternalDelivery_Spec8_ThumbnailStatus_MappingExhaustive
// pins the 11-value → 3-value (= pending|applied|failed) mapping
// every (status, thumbnail_status) pair.
func TestHandleGetInternalDelivery_Spec8_ThumbnailStatus_MappingExhaustive(t *testing.T) {
	cases := []struct {
		in   models.ExternalDeliveryStatus
		want string
	}{
		{models.ExternalDeliveryStatusAccepted, "pending"},
		{models.ExternalDeliveryStatusDownloading, "pending"},
		{models.ExternalDeliveryStatusArtifactVerified, "pending"},
		{models.ExternalDeliveryStatusIngestCompleted, "pending"},
		{models.ExternalDeliveryStatusPublishing, "pending"},
		{models.ExternalDeliveryStatusQueued, "pending"},
		{models.ExternalDeliveryStatusRetryWait, "pending"},
		{models.ExternalDeliveryStatusBlockedAuth, "failed"},
		{models.ExternalDeliveryStatusPublished, "applied"},
		{models.ExternalDeliveryStatusFailed, "failed"},
		{models.ExternalDeliveryStatusDeadLetter, "failed"},
	}
	for _, tc := range cases {
		t.Run(string(tc.in), func(t *testing.T) {
			if got := mapExternalDeliveryStatusToThumbnailStatus(tc.in); got != tc.want {
				t.Errorf("ExternalDeliveryStatus(%q) → thumbnail_status want %q, got %q",
					tc.in, tc.want, got)
			}
		})
	}
}

// TestHandleGetInternalDelivery_Spec8_Target_Resolved exercises
// the full FK chain: external_deliveries → external_destinations →
// platform_accounts → workspace_channels. Asserts that all four
// target fields are populated correctly.
func TestHandleGetInternalDelivery_Spec8_Target_Resolved(t *testing.T) {
	store := newFakeDeliveryStorage()
	store.seedRowExt(
		"sdel_01JABC", models.ExternalDeliveryStatusAccepted,
		"extdst_12_381",
		"", "", "", "", nil,
		nil, // no metadata → privacy=""
	)

	destinations := &fakeDestinationStorageExtended{
		rows: map[string]*models.ExternalDestination{
			"extdst_12_381": {
				ID:                "extdst_12_381",
				WorkspaceID:       12,
				PlatformAccountID: 381,
				Enabled:           true,
			},
		},
	}
	userStore := &fakeUserStoreSpec8{
		rows: map[int64]*models.PlatformAccount{
			381: {
				ID:             381,
				Platform:       models.PlatformYouTube,
				PlatformUserID: "UCxxxxxxxx",
				Username:       "Wrestling Discovery",
				Status:         models.AccountStatusActive,
			},
		},
	}
	workspaceStore := &fakeWorkspaceStoreSpec8{
		workspaces: map[int64]*models.Workspace{
			12: {ID: 12, OwnerID: 1},
		},
		bindings: map[string]*models.WorkspaceChannel{
			wsKey(12, 381): {
				WorkspaceID:       12,
				PlatformAccountID: 381,
				Enabled:           true,
			},
		},
	}

	r := newVeloxTestRouterWithDeps(t, store, destinations, userStore, workspaceStore, "secret-token")
	w := testSendRequest(t, r, http.MethodGet, "/internal/v1/deliveries/sdel_01JABC", "Bearer secret-token")

	if w.Code != http.StatusOK {
		t.Fatalf("target resolved: want 200, got %d (body=%q)", w.Code, w.Body.String())
	}
	var got VeloxGetDeliveryResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Target.PlatformAccountID != 381 {
		t.Errorf("Target.PlatformAccountID: want 381, got %d", got.Target.PlatformAccountID)
	}
	if got.Target.ChannelID != "UCxxxxxxxx" {
		t.Errorf("Target.ChannelID: want UCxxxxxxxx, got %q", got.Target.ChannelID)
	}
	if got.Target.ChannelName != "Wrestling Discovery" {
		t.Errorf("Target.ChannelName: want Wrestling Discovery, got %q", got.Target.ChannelName)
	}
	if !got.Target.Enabled {
		t.Errorf("Target.Enabled: want true, got false")
	}
}

// TestHandleGetInternalDelivery_Spec8_Target_PartialResolution
// pins the partial-fidelity behaviour: destination exists but
// platform_account row missing → target resolves only to
// platform_account_id; channel_id/channel_name stay empty;
// operator dashboard surfaces "binding missing; reconcile needed".
func TestHandleGetInternalDelivery_Spec8_Target_PartialResolution(t *testing.T) {
	store := newFakeDeliveryStorage()
	store.seedRowExt(
		"sdel_01JABC", models.ExternalDeliveryStatusAccepted,
		"extdst_12_381",
		"", "", "", "", nil,
		nil,
	)

	destinations := &fakeDestinationStorageExtended{
		rows: map[string]*models.ExternalDestination{
			"extdst_12_381": {ID: "extdst_12_381", WorkspaceID: 12, PlatformAccountID: 381, Enabled: true},
		},
	}
	userStore := &fakeUserStoreSpec8{
		rows: map[int64]*models.PlatformAccount{}, // empty → missing row
	}
	workspaceStore := &fakeWorkspaceStoreSpec8{
		bindings: map[string]*models.WorkspaceChannel{}, // empty → missing binding
	}

	r := newVeloxTestRouterWithDeps(t, store, destinations, userStore, workspaceStore, "secret-token")
	w := testSendRequest(t, r, http.MethodGet, "/internal/v1/deliveries/sdel_01JABC", "Bearer secret-token")

	if w.Code != http.StatusOK {
		t.Fatalf("partial resolution: want 200 (handler tolerates partial chain), got %d", w.Code)
	}
	var got VeloxGetDeliveryResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Target.PlatformAccountID != 381 {
		t.Errorf("Target.PlatformAccountID: want 381 (resolved via destination), got %d", got.Target.PlatformAccountID)
	}
	if got.Target.ChannelID != "" {
		t.Errorf("Target.ChannelID: want empty (platform_account row missing), got %q", got.Target.ChannelID)
	}
	if got.Target.ChannelName != "" {
		t.Errorf("Target.ChannelName: want empty (platform_account row missing), got %q", got.Target.ChannelName)
	}
	if got.Target.Enabled {
		t.Errorf("Target.Enabled: want false (binding missing), got true")
	}
}

// TestHandleGetInternalDelivery_Spec8_PrivacyFromMetadata pins
// that a JSONB metadata block with privacy_status="private" is
// surfaced verbatim on the response. Uses newVeloxTestRouter
// (no FK-chain fixtures) because the privacy helper doesn't
// reach them — keeps the test minimal.
func TestHandleGetInternalDelivery_Spec8_PrivacyFromMetadata(t *testing.T) {
	store := newFakeDeliveryStorage()
	meta := json.RawMessage(`{"privacy_status":"private","title":"Sample Title","description":"Sample Desc"}`)
	store.seedRowExt("sdel_01JABC", models.ExternalDeliveryStatusQueued,
		"", // no external_destination_id → target empty
		"", "", "", "", nil,
		meta,
	)

	r := newVeloxTestRouter(t, store, "secret-token")
	w := testSendRequest(t, r, http.MethodGet, "/internal/v1/deliveries/sdel_01JABC", "Bearer secret-token")

	if w.Code != http.StatusOK {
		t.Fatalf("privacy from metadata: want 200, got %d", w.Code)
	}
	var got VeloxGetDeliveryResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Privacy != "private" {
		t.Errorf("Privacy: want private, got %q", got.Privacy)
	}
	if got.PublishStatus != "scheduled" {
		t.Errorf("PublishStatus: want scheduled (queued in 11-status → §8 scheduled), got %q", got.PublishStatus)
	}
	if got.ThumbnailStatus != "pending" {
		t.Errorf("ThumbnailStatus: want pending (queued is in-flight), got %q", got.ThumbnailStatus)
	}
}

// TestHandleGetInternalDelivery_Spec8_Privacy_MalformedMetadata
// pins the lenient parser: malformed JSON → privacy=""
// (NOT a 500). Mirrors the operator-recover contract.
func TestHandleGetInternalDelivery_Spec8_Privacy_MalformedMetadata(t *testing.T) {
	store := newFakeDeliveryStorage()
	store.seedRowExt("sdel_01JABC", models.ExternalDeliveryStatusAccepted,
		"", "", "", "", "", nil,
		json.RawMessage(`{this is not valid json`),
	)

	r := newVeloxTestRouter(t, store, "secret-token")
	w := testSendRequest(t, r, http.MethodGet, "/internal/v1/deliveries/sdel_01JABC", "Bearer secret-token")

	if w.Code != http.StatusOK {
		t.Fatalf("malformed metadata: want 200 (lenient parser), got %d", w.Code)
	}
	var got VeloxGetDeliveryResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Privacy != "" {
		t.Errorf("Privacy: want empty (malformed metadata), got %q", got.Privacy)
	}
}

// TestHandleGetInternalDelivery_Spec8_PublishedAlias_YoutubeVideoID
// pins that published rows populate BOTH the new youtube_video_id
// field AND the legacy platform_media_id field with the same
// value (they alias the same source column).
func TestHandleGetInternalDelivery_Spec8_PublishedAlias_YoutubeVideoID(t *testing.T) {
	completedAt := time.Date(2026, 7, 29, 9, 4, 12, 0, time.UTC)
	store := newFakeDeliveryStorage()
	store.seedRowExt(
		"sdel_01JABC", models.ExternalDeliveryStatusPublished,
		"",
		"", "", "AbCd1234", "https://www.youtube.com/watch?v=AbCd1234",
		&completedAt,
		nil,
	)

	r := newVeloxTestRouter(t, store, "secret-token")
	w := testSendRequest(t, r, http.MethodGet, "/internal/v1/deliveries/sdel_01JABC", "Bearer secret-token")

	if w.Code != http.StatusOK {
		t.Fatalf("published alias: want 200, got %d", w.Code)
	}
	var got VeloxGetDeliveryResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.YouTubeVideoID != "AbCd1234" {
		t.Errorf("YouTubeVideoID: want AbCd1234, got %q", got.YouTubeVideoID)
	}
	if got.PlatformMediaID != "AbCd1234" {
		t.Errorf("PlatformMediaID (legacy alias): want AbCd1234, got %q", got.PlatformMediaID)
	}
	if got.PlatformMediaID != got.YouTubeVideoID {
		t.Errorf("PlatformMediaID (%q) and YouTubeVideoID (%q) must alias the same value",
			got.PlatformMediaID, got.YouTubeVideoID)
	}
	if got.PublishStatus != "published" {
		t.Errorf("PublishStatus: want published, got %q", got.PublishStatus)
	}
	if got.ThumbnailStatus != "applied" {
		t.Errorf("ThumbnailStatus: want applied, got %q", got.ThumbnailStatus)
	}
	if got.PublishedAt == nil || !got.PublishedAt.Equal(completedAt) {
		t.Errorf("PublishedAt: want %v (from completedAt), got %v", completedAt, got.PublishedAt)
	}
}

// TestHandleGetInternalDelivery_Spec8_FailedBlockedStatus ensures
// blocked_auth / failed / dead_letter rows map to the correct
// (publish_status, thumbnail_status) pair.
func TestHandleGetInternalDelivery_Spec8_FailedBlockedStatus(t *testing.T) {
	cases := []struct {
		name           string
		status         models.ExternalDeliveryStatus
		wantPublish    string
		wantThumbnail  string
	}{
		{"blocked_auth",
			models.ExternalDeliveryStatusBlockedAuth, "blocked", "failed"},
		{"failed",
			models.ExternalDeliveryStatusFailed, "failed", "failed"},
		{"dead_letter",
			models.ExternalDeliveryStatusDeadLetter, "failed", "failed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeDeliveryStorage()
			store.seedRowExt("sdel_01JABC", tc.status,
				"", "", "", "", "", nil, nil)

			r := newVeloxTestRouter(t, store, "secret-token")
			w := testSendRequest(t, r, http.MethodGet, "/internal/v1/deliveries/sdel_01JABC", "Bearer secret-token")
			if w.Code != http.StatusOK {
				t.Fatalf("status: want 200, got %d", w.Code)
			}
			var got VeloxGetDeliveryResponse
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if got.PublishStatus != tc.wantPublish {
				t.Errorf("PublishStatus: want %q, got %q", tc.wantPublish, got.PublishStatus)
			}
			if got.ThumbnailStatus != tc.wantThumbnail {
				t.Errorf("ThumbnailStatus: want %q, got %q", tc.wantThumbnail, got.ThumbnailStatus)
			}
		})
	}
}
