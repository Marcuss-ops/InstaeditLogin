package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/go-chi/chi/v5"
)

const testVeloxAPIToken = "test-velox-secret-token-fixed-value"

// -----------------------------------------------------------------------
// Mocks (in-file to keep the test self-contained)
// -----------------------------------------------------------------------

// mockExternalDestinationStore implements ExternalDestinationStore
// for tests. Toggle the per-field return values to drive each
// scenario.
type mockExternalDestinationStore struct {
	GetByIDResult *models.ExternalDestination
	GetByIDErr    error
	GetByIDCalls  int
}

func (m *mockExternalDestinationStore) GetByID(_ context.Context, _ string) (*models.ExternalDestination, error) {
	m.GetByIDCalls++
	return m.GetByIDResult, m.GetByIDErr
}

// Create satisfies ExternalDestinationStore. Required since the
// POST /internal/v1/deliveries cut added Create to the
// interface. The validate handler under test never reaches
// Create (POST flows use the richer fakeDestinationEnv in
// internal_velox_deliveries_test.go); this stub returns nil
// so the compile-time interface check passes without touching
// a real DB.
func (m *mockExternalDestinationStore) Create(_ context.Context, _ *models.ExternalDestination) error {
	return nil
}

// ListByWorkspace + Delete satisfy the expanded ExternalDestinationStore
// interface (Step 6). The validate tests do not exercise these methods;
// stubs return empty/nil so the interface is satisfied.
func (m *mockExternalDestinationStore) ListByWorkspace(_ context.Context, _ int64, _ bool) ([]models.ExternalDestination, error) {
	return nil, nil
}
func (m *mockExternalDestinationStore) Delete(_ context.Context, _ string) error {
	return nil
}

// UpdateEnabledAndDefaults is the combined-verb stub. The validate
// handler does NOT exercise this verb; stub returns nil so vet
// succeeds without forcing unrelated fixture refactors.
func (m *mockExternalDestinationStore) UpdateEnabledAndDefaults(_ context.Context, _ string, _ *bool, _ json.RawMessage) error {
	return nil
}

// mockWorkspaceLookup holds the test data + call counter for the
// ONE WorkspaceStore method the validate handler reaches
// (FindByID). The adapter wraps it so the lookup-edge failure
// surface is O(1) rather than implementing every WorkspaceStore
// method verbosely.
type mockWorkspaceLookup struct {
	findByIDResult *models.Workspace
	findByIDErr    error
	findByIDCalls  int
}

// workspaceStoreAdapter embeds the full WorkspaceStore interface
// (nil-receiver methods for the methods the handler doesn't call —
// those would panic if exercised). The adapter ALSO carries a
// pointer to mockWorkspaceLookup so the ONE method the handler
// reaches can be overridden as a direct method (depth-0 shadows
// depth-1 promoted method, avoiding the ambiguous-selector
// compile error that blocks the obvious two-interface-embed
// pattern).
type workspaceStoreAdapter struct {
	WorkspaceStore
	m *mockWorkspaceLookup
}

// FindByID is the depth-0 direct override. It shadows the
// promoted WorkspaceStore.FindByID and is what the production
// handler reaches.
func (a *workspaceStoreAdapter) FindByID(_ int64) (*models.Workspace, error) {
	a.m.findByIDCalls++
	return a.m.findByIDResult, a.m.findByIDErr
}

// FindChannel + ListChannels are required to satisfy the production
// WorkspaceStore interface which (*deliveries.TargetResolver).resolveSavedDestination
// calls at the binding check (after FindByID + FindPlatformAccountByID).
//
// Without these overrides a method call promotes to the nil embedded
// interface and SIGSEGVs (NOT the usual Go nil-pointer panic). The
// happy-path validate test intentionally has no per-account binding
// rows; (nil, nil) is the correct semantic — the resolver treats
// binding==nil as "no workspace-side disable" and proceeds with the
// eligibility check.
func (a *workspaceStoreAdapter) FindChannel(_ context.Context, _ int64, _ int64) (*models.WorkspaceChannel, error) {
	return nil, nil
}

func (a *workspaceStoreAdapter) ListChannels(_ context.Context, _ int64) ([]models.WorkspaceChannel, error) {
	return nil, nil
}

// Compile-time guarantee that workspaceStoreAdapter satisfies the
// production WorkspaceStore interface wired by VeloxModule. Catches
// signature drift on the production interface — same guard as
// fakeE2EWorkspace in internal_velox_e2e_helpers_test.go.
var _ WorkspaceStore = (*workspaceStoreAdapter)(nil)

// wrapWorkspaceLookup binds a mockWorkspaceLookup to a fresh
// adapter, returning a WorkspaceStore the Router.workspaceStore
// field can hold.
func wrapWorkspaceLookup(m *mockWorkspaceLookup) WorkspaceStore {
	return &workspaceStoreAdapter{m: m}
}

// mockUserLookup is the user-side analog: it carries the data +
// counter for the ONE UserStore method the handler reaches
// (FindPlatformAccountByID).
type mockUserLookup struct {
	findPlatformAccountByIDResult *models.PlatformAccount
	findPlatformAccountByIDErr    error
	findPlatformAccountByIDCalls  int
}

// userStoreAdapter mirrors workspaceStoreAdapter: embed
// UserStore + carry the mock lookup + depth-0 direct override.
type userStoreAdapter struct {
	UserStore
	m *mockUserLookup
}

// FindPlatformAccountByID is the depth-0 direct override.
func (a *userStoreAdapter) FindPlatformAccountByID(_ int64) (*models.PlatformAccount, error) {
	a.m.findPlatformAccountByIDCalls++
	return a.m.findPlatformAccountByIDResult, a.m.findPlatformAccountByIDErr
}

// wrapUserLookup binds a mockUserLookup to a fresh adapter,
// returning a UserStore the Router.userRepo field can hold.
func wrapUserLookup(m *mockUserLookup) UserStore {
	return &userStoreAdapter{m: m}
}

// -----------------------------------------------------------------------
// Router fixture builder
// -----------------------------------------------------------------------

// buildVeloxTestRouter wires a fresh Router with the test
// destination / workspace / user lookups + token. All Router
// fields are set to either the supplied value or zero; nothing
// else is shared with production code paths (no auth, no CSRF,
// no /ready, no admin).
func buildVeloxTestRouter(dst ExternalDestinationStore, wsLookup *mockWorkspaceLookup, userLookup *mockUserLookup, token string) *Router {
	r := &Router{
		externalDestinations: dst,
		workspaceStore:       wrapWorkspaceLookup(wsLookup),
		userRepo:             wrapUserLookup(userLookup),
		veloxAPIToken:        token,
	}
	return r
}

// runValidate wires an httptest request to the validate handler
// + Bearer middleware, returns the recorded response. Uses
// chi.Mux (the production routing library) to match handlers.go.
//
// Signature takes concrete *mockWorkspaceLookup + *mockUserLookup
// instead of interfaces so the test helpers don't have to define
// shared interfaces for one-method fakes.
func runValidate(t *testing.T, dst ExternalDestinationStore, wsLookup *mockWorkspaceLookup, userLookup *mockUserLookup, token, id, authHeader, query string) *httptest.ResponseRecorder {
	t.Helper()
	r := buildVeloxTestRouter(dst, wsLookup, userLookup, token)
	handler := r.internalVeloxAuth(http.HandlerFunc(r.handleValidateInternalDestination))
	mux := chi.NewRouter()
	mux.Method(http.MethodPost, "/internal/v1/destinations/{id}/validate", handler)

	url := "/internal/v1/destinations/" + id + "/validate"
	if query != "" {
		url += "?" + query
	}
	req := httptest.NewRequest(http.MethodPost, url, nil)
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

// -----------------------------------------------------------------------
// Tests
// -----------------------------------------------------------------------

// TestValidate_MissingAuthHeader verifies that an unauthenticated
// POST returns 401 and the destination store is NEVER called
// (defense-in-depth against oracle timing of the auth path).
// Also confirms Content-Type is application/json (writeError
// path), not text/plain (http.Error path) — content-type
// parity with the rest of pkg/api per code-review.
