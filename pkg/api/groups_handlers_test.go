package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// =============================================================================
// GET /api/v1/groups — handleListGroups regression tests.
//
// Background:
//   The SPA at VeloxFrontend/web/src/app/views/DashboardView.tsx calls
//   GET /api/v1/groups WITHOUT any query string. Before this patch
//   the backend required ?workspace_id=… and replied 400 → React Query
//   `isError` → Dashboard rendered "Impossibile caricare i gruppi.
//   Riprova più tardi.".
//
//   The fix relaxes handleListGroups to fall back to the active
//   workspace stamped on the JWT/API-key identity. The cases below
//   cover the explicit + implicit paths, the cross-owner guard, the
//   empty-state contract, the bad-query guard, and the no-JWT 401.
// =============================================================================

// groupsTestRouter wires a Router with the supplied store pair. The
// workspace store is configured so FindByID(id) returns a workspace
// owned by `ownerID` if id matches `ownedWorkspaceID`; for any other
// id it returns a workspace owned by `foreignOwnerID` (forcing the
// cross-owner 404 path). Tests that want a different ownership shape
// override findByIDFn directly.
func groupsTestRouter(t *testing.T, gStore *mockGroupStore, wsStore *mockWorkspaceStore) *Router {
	t.Helper()
	return mustNewRouterWithDefaults(
		services.NewCapabilityRouter(),
		&mockUserStore{},
		auth.NewManager(testJWTSecret, 24),
		"https://app.instaedit.org",
		nil,
		WithGroupStore(gStore),
		WithWorkspaceStore(wsStore),
	)
}

// withBearerJWTForWorkspace is the explicit-ws counterpart to
// withBearerJWT (common_test.go), which always mints a JWT with
// wsID=1, sessionID=1. Several regression tests below need a
// different wsID to exercise the cross-owner / explicit-query paths
// without colliding with the default (user 1 owns workspace 1).
func withBearerJWTForWorkspace(t *testing.T, req *http.Request, userID, workspaceID int64) {
	t.Helper()
	mgr := auth.NewManager(testJWTSecret, 24)
	tok, _, _, err := mgr.IssueAccess(userID, workspaceID, 1)
	if err != nil {
		t.Fatalf("issue access jwt (user=%d ws=%d session=1): %v", userID, workspaceID, err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
}

// userOwnedWorkspaceStore returns a mockWorkspaceStore that resolves
// `ownedID` to a workspace owned by `ownerID` and everything else to
// a workspace owned by a DIFFERENT user (for the cross-owner test).
func userOwnedWorkspaceStore(ownedID, ownerID, foreignOwnerID int64) *mockWorkspaceStore {
	return &mockWorkspaceStore{
		findByIDFn: func(id int64) (*models.Workspace, error) {
			if id == ownedID {
				return &models.Workspace{ID: ownedID, OwnerID: ownerID}, nil
			}
			return &models.Workspace{ID: id, OwnerID: foreignOwnerID}, nil
		},
	}
}

// readGroupsEnvelope decodes the {"groups":[…]} envelope returned by
// handleListGroups. Empty responses are normalised to "[]" by the
// handler so this helper unwinds either shape uniformly to []value.
func readGroupsEnvelope(t *testing.T, body io.Reader) []models.Group {
	t.Helper()
	var env struct {
		Groups []models.Group `json:"groups"`
	}
	if err := json.NewDecoder(body).Decode(&env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	return env.Groups
}

// TestHandleListGroups_EmptyQueryValue_BehavesAsNoQuery locks in the
// behaviour for "?workspace_id=" (empty value) — Go's url.Values.Get
// collapses a present-but-empty key to "", and the handler must
// treat it identically to a fully absent query (fall back to the
// JWT workspace). A future refactor that distinguishes "missing"
// vs "empty" must not silently change this — see the comment on
// handleListGroups.
func TestHandleListGroups_EmptyQueryValue_BehavesAsNoQuery(t *testing.T) {
	const (
		userID  = int64(1)
		wsOwned = int64(1)
	)
	var capturedWS int64
	gStore := &mockGroupStore{
		listByWorkspaceFn: func(workspaceID int64) ([]models.Group, error) {
			capturedWS = workspaceID
			return []models.Group{
				{ID: 5, WorkspaceID: workspaceID, Name: "Editoriale"},
			}, nil
		},
	}
	wsStore := userOwnedWorkspaceStore(wsOwned, userID, 99)
	r := groupsTestRouter(t, gStore, wsStore)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/groups?workspace_id=", nil)
	withBearerJWTForWorkspace(t, req, userID, wsOwned)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("empty-value query: want 200, got %d: %s", w.Code, w.Body.String())
	}
	if capturedWS != wsOwned {
		t.Errorf("empty-value query must fall back to identity (wsID=%d), got %d", wsOwned, capturedWS)
	}
}

// TestHandleListGroups_NoQueryFallsBackToJWTWorkspace proves the
// regression case that motivated this fix: the SPA calls GET
// /api/v1/groups with NO ?workspace_id. The handler must resolve to
// the active workspace stamped on the JWT by Manager.Verify.
func TestHandleListGroups_NoQueryFallsBackToJWTWorkspace(t *testing.T) {
	const (
		userID  = int64(1)
		wsOwned = int64(1) // JWT wsID is 1; workspace 1 is owned by user 1
	)
	var capturedWS int64
	gStore := &mockGroupStore{
		listByWorkspaceFn: func(workspaceID int64) ([]models.Group, error) {
			capturedWS = workspaceID
			return []models.Group{
				{ID: 10, WorkspaceID: workspaceID, Name: "Editoriale"},
				{ID: 11, WorkspaceID: workspaceID, Name: "Marketing"},
			}, nil
		},
	}
	wsStore := userOwnedWorkspaceStore(wsOwned, userID, 99)
	r := groupsTestRouter(t, gStore, wsStore)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/groups", nil)
	withBearerJWTForWorkspace(t, req, userID, wsOwned)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("no-query happy path: want 200, got %d: %s", w.Code, w.Body.String())
	}
	if capturedWS != wsOwned {
		t.Errorf("ListByWorkspace called with wsID=%d, want %d (identity fallback must forward the JWT ws)", capturedWS, wsOwned)
	}
	groups := readGroupsEnvelope(t, w.Body)
	if len(groups) != 2 {
		t.Fatalf("groups: want 2 entries, got %d", len(groups))
	}
	if groups[0].Name != "Editoriale" || groups[1].Name != "Marketing" {
		t.Errorf("group names mismatch: %+v", groups)
	}
}

// TestHandleListGroups_ExplicitQueryOverridesIdentity verifies that an
// explicit ?workspace_id=… is honored EVEN IF it differs from the
// JWT's wsID. The ownership check still applies (so cross-owner
// 404 fires when the caller does not own the requested workspace)
// — see TestHandleListGroups_ExplicitQueryCrossOwner_404 below.
func TestHandleListGroups_ExplicitQueryOverridesIdentity(t *testing.T) {
	const (
		userID       = int64(1)
		jwtWS        = int64(1)  // JWT carries wsID=1
		explicitWS   = int64(7)  // Caller asks ?workspace_id=7 (also owned by user 1)
	)
	var capturedWS int64
	gStore := &mockGroupStore{
		listByWorkspaceFn: func(workspaceID int64) ([]models.Group, error) {
			capturedWS = workspaceID
			return []models.Group{
				{ID: 20, WorkspaceID: workspaceID, Name: "Solo Group"},
			}, nil
		},
	}
	wsStore := userOwnedWorkspaceStore(explicitWS, userID, 99)
	r := groupsTestRouter(t, gStore, wsStore)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/groups?workspace_id=7", nil)
	withBearerJWTForWorkspace(t, req, userID, jwtWS)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("explicit-query happy path: want 200, got %d: %s", w.Code, w.Body.String())
	}
	if capturedWS != explicitWS {
		t.Errorf("ListByWorkspace called with wsID=%d, want explicit %d", capturedWS, explicitWS)
	}
}

// TestHandleListGroups_ExplicitQueryCrossOwner_404 asserts the
// ownership guard is preserved when an explicit ?workspace_id
// targets a workspace the caller does NOT own. Existence-leak
// avoidance collapses "group not found" and "workspace not yours"
// into 404.
func TestHandleListGroups_ExplicitQueryCrossOwner_404(t *testing.T) {
	const (
		userID     = int64(1)
		jwtWS      = int64(1)  // JWT wsID=1
		foreignWS  = int64(99) // Caller asks ?workspace_id=99 (owned by user 999)
	)
	gStore := &mockGroupStore{
		listByWorkspaceFn: func(workspaceID int64) ([]models.Group, error) {
			t.Errorf("ListByWorkspace MUST NOT be called when the requested workspace is foreign-owned; got wsID=%d", workspaceID)
			return nil, nil
		},
	}
	wsStore := userOwnedWorkspaceStore(jwtWS, userID, 999) // foreign workspace owner = 999
	r := groupsTestRouter(t, gStore, wsStore)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/groups?workspace_id=99", nil)
	withBearerJWTForWorkspace(t, req, userID, jwtWS)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("cross-owner explicit query: want 404, got %d: %s", w.Code, w.Body.String())
	}
}

// TestHandleListGroups_BadQueryParseIs400 covers the unparseable
// ?workspace_id branch — non-numeric or sign-mismatched values
// must surface as 400 BEFORE any DB lookup.
func TestHandleListGroups_BadQueryParseIs400(t *testing.T) {
	const (
		userID = int64(1)
		wsWS   = int64(1)
	)
	gStore := &mockGroupStore{
		listByWorkspaceFn: func(workspaceID int64) ([]models.Group, error) {
			t.Errorf("ListByWorkspace MUST NOT be called with bad query; got wsID=%d", workspaceID)
			return nil, nil
		},
	}
	wsStore := userOwnedWorkspaceStore(wsWS, userID, 99)
	r := groupsTestRouter(t, gStore, wsStore)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/groups?workspace_id=abc", nil)
	withBearerJWTForWorkspace(t, req, userID, wsWS)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("bad query parse: want 400, got %d: %s", w.Code, w.Body.String())
	}
}

// TestHandleListGroups_EmptyReturns200EmptyArray locks in the
// empty-state contract: a workspace with zero groups must return
// 200 + {"groups":[]} so the SPA renders its empty-state copy
// rather than a 404 "workspace not found"-shaped error.
func TestHandleListGroups_EmptyReturns200EmptyArray(t *testing.T) {
	const (
		userID = int64(1)
		wsWS   = int64(1)
	)
	gStore := &mockGroupStore{
		listByWorkspaceFn: func(workspaceID int64) ([]models.Group, error) {
			// simulate "no rows" — repo legitimately returns (nil, nil)
			return nil, nil
		},
	}
	wsStore := userOwnedWorkspaceStore(wsWS, userID, 99)
	r := groupsTestRouter(t, gStore, wsStore)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/groups", nil)
	withBearerJWTForWorkspace(t, req, userID, wsWS)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("empty happy path: want 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := w.Body.String(); !strings.Contains(got, `"groups":[]`) {
		t.Errorf("empty envelope must serialise as \"groups\":[], got %q", got)
	}
}

// TestHandleListGroups_RepoErrorSurfacesAs500 covers the unhappy
// repo path: ListByWorkspace returning a non-nil error maps to 500
// via the handler's mapGroupError→InternalServerError fallback.
// This guards the fallback path against regressions where a caller
// would silently swallow the error.
func TestHandleListGroups_RepoErrorSurfacesAs500(t *testing.T) {
	const (
		userID = int64(1)
		wsWS   = int64(1)
	)
	gStore := &mockGroupStore{
		listByWorkspaceFn: func(workspaceID int64) ([]models.Group, error) {
			return nil, errors.New("simulated db outage")
		},
	}
	wsStore := userOwnedWorkspaceStore(wsWS, userID, 99)
	r := groupsTestRouter(t, gStore, wsStore)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/groups", nil)
	withBearerJWTForWorkspace(t, req, userID, wsWS)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("repo error: want 500, got %d: %s", w.Code, w.Body.String())
	}
}

// TestHandleListGroups_NoJWT_401 verifies the auth middleware
// short-circuits before the handler body — the identity fallback
// MUST NOT be reachable without an authenticated session.
func TestHandleListGroups_NoJWT_401(t *testing.T) {
	gStore := &mockGroupStore{
		listByWorkspaceFn: func(workspaceID int64) ([]models.Group, error) {
			t.Errorf("ListByWorkspace MUST NOT be called without a JWT")
			return nil, nil
		},
	}
	wsStore := &mockWorkspaceStore{} // default stub
	r := groupsTestRouter(t, gStore, wsStore)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/groups", nil)
	// No withBearerJWT — unauthenticated probe.
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("no-JWT: want 401, got %d: %s", w.Code, w.Body.String())
	}
}

// Note: the nil-groupStore guard inside handleListGroups (the early
// `if r.groupStore == nil` writeError 501) is NOT reachable from
// HTTP — AuthModule (modules.go:175 gates the broader Register;
// modules.go:601 re-checks per-route) only mounts GET /api/v1/groups
// when GroupStore != nil. It is kept inside the handler as
// defence-in-depth for direct unit-test invocations and for any
// future route that bypasses AuthModule. The handler is exported to
// the package's *_test.go files so a direct call here would work,
// but the regression coverage we care about lives on the reachable
// paths above.

// Compile-time assertion: *mockGroupStore satisfies the production
// GroupStore interface. Keeps the test file self-contained against
// future signature changes (mirrors the `var _ UserStore = …` pattern
// used across the package).
var _ GroupStore = (*mockGroupStore)(nil)
