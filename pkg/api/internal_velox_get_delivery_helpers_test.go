package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/go-chi/chi/v5"
)

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
