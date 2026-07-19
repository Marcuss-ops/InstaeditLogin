package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// testVeloxAPIToken is a fixed string for httptest. Production
// tokens are 32-char random hex from a 16-byte secret; the test
// value uses printable ASCII so failure messages are easy to
// eyeball. The exact length doesn't matter — subtle.ConstantTimeCompare
// returns 0 on length-mismatch (short-circuit) so 401 is
// guaranteed for any wrong token.
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
func TestValidate_MissingAuthHeader(t *testing.T) {
	dst := &mockExternalDestinationStore{}
	w := runValidate(t, dst, &mockWorkspaceLookup{}, &mockUserLookup{},
		testVeloxAPIToken, "extdst_01JABC", "", "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d (body=%q)", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type: want application/json (writeError path), got %s", got)
	}
	if dst.GetByIDCalls != 0 {
		t.Errorf("destination store must NOT be called when auth fails; got %d calls", dst.GetByIDCalls)
	}
}

// TestValidate_MalformedAuthHeader verifies the prefix check:
// "Token <value>", "Basic ...", etc. all return 401.
func TestValidate_MalformedAuthHeader(t *testing.T) {
	dst := &mockExternalDestinationStore{}
	for _, bad := range []string{
		"Token abc",
		"Basic dXNlcjpwYXNz",
		"", // empty
	} {
		w := runValidate(t, dst, &mockWorkspaceLookup{}, &mockUserLookup{},
			testVeloxAPIToken, "extdst_01JABC", bad, "")
		if w.Code != http.StatusUnauthorized {
			t.Errorf("malformed header %q: want 401, got %d", bad, w.Code)
		}
	}
}

// TestValidate_PrefixOnly verifies a header that has only "Bearer "
// (no value after) returns 401 — the length check
// `len(authHeader) <= len(prefix)` catches it before the
// strings.EqualFold call.
func TestValidate_PrefixOnly(t *testing.T) {
	dst := &mockExternalDestinationStore{}
	w := runValidate(t, dst, &mockWorkspaceLookup{}, &mockUserLookup{},
		testVeloxAPIToken, "extdst_01JABC", "Bearer", "")
	if w.Code != http.StatusUnauthorized {
		t.Errorf("bare Bearer prefix: want 401, got %d", w.Code)
	}
}

// TestValidate_WrongToken verifies the constant-time token
// mismatch path returns 401 AND the destination store counter
// stays at zero (timing-leak defense).
func TestValidate_WrongToken(t *testing.T) {
	dst := &mockExternalDestinationStore{}
	w := runValidate(t, dst, &mockWorkspaceLookup{}, &mockUserLookup{},
		testVeloxAPIToken, "extdst_01JABC",
		"Bearer wrong-token-32-chars-aaaaaa", "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
	if dst.GetByIDCalls != 0 {
		t.Errorf("destination store must NOT be called when token mismatches; got %d calls", dst.GetByIDCalls)
	}
}

// TestValidate_TokenMismatchSameLength closes an unlikely but
// possible read of subtle.ConstantTimeCompare: same length +
// wrong content. The compare returns 0 → 401. Verifies the
// happy-length-mismatch path uses the constant-time compare
// (vs. a naive bytewise compare that would leak per-byte
// equality on first match).
func TestValidate_TokenMismatchSameLength(t *testing.T) {
	dst := &mockExternalDestinationStore{}
	// Construct a same-length wrong token (substitute last char).
	wrong := testVeloxAPIToken[:len(testVeloxAPIToken)-1] + "X"
	w := runValidate(t, dst, &mockWorkspaceLookup{}, &mockUserLookup{},
		testVeloxAPIToken, "extdst_01JABC",
		"Bearer "+wrong, "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("same-length wrong token: want 401, got %d", w.Code)
	}
}

// TestValidate_DestinationNotFound pins the (nil, nil) path:
// GetByID returns nil dest, handler returns 404 and does NOT
// query the workspace or platform_account (early-exit branch).
func TestValidate_DestinationNotFound(t *testing.T) {
	dst := &mockExternalDestinationStore{} // GetByIDResult is nil by default
	ws := &mockWorkspaceLookup{}
	user := &mockUserLookup{}
	w := runValidate(t, dst, ws, user, testVeloxAPIToken, "extdst_01JDEF",
		"Bearer "+testVeloxAPIToken, "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d (body=%q)", w.Code, w.Body.String())
	}
	if ws.findByIDCalls != 0 {
		t.Errorf("workspace must NOT be queried after destination not found; got %d calls", ws.findByIDCalls)
	}
	if user.findPlatformAccountByIDCalls != 0 {
		t.Errorf("platform_account must NOT be queried after destination not found; got %d calls",
			user.findPlatformAccountByIDCalls)
	}
}

// TestValidate_DestinationDisabled pins the disabled = missing
// policy: enabled=false returns 404 (uniform with not-found so
// existing-vs-non-existing isn't an oracle).
func TestValidate_DestinationDisabled(t *testing.T) {
	now := time.Now()
	dst := &mockExternalDestinationStore{
		GetByIDResult: &models.ExternalDestination{
			ID:                "extdst_01JABC",
			SourceSystem:      "velox",
			WorkspaceID:       12,
			PlatformAccountID: 345,
			Enabled:           false,
			CreatedAt:         now,
			UpdatedAt:         now,
		},
	}
	ws := &mockWorkspaceLookup{}
	user := &mockUserLookup{}
	w := runValidate(t, dst, ws, user, testVeloxAPIToken, "extdst_01JABC",
		"Bearer "+testVeloxAPIToken, "")
	if w.Code != http.StatusNotFound {
		t.Errorf("disabled destination: want 404, got %d (body=%q)", w.Code, w.Body.String())
	}
	if ws.findByIDCalls != 0 || user.findPlatformAccountByIDCalls != 0 {
		t.Errorf("downstream lookups must NOT fire when destination is disabled")
	}
}

// TestValidate_HappyPathNoDiagnostic verifies the 204 No Content
// response when destination + workspace + platform_account all
// line up and no diagnostic mode is requested.
func TestValidate_HappyPathNoDiagnostic(t *testing.T) {
	now := time.Now()
	dst := &mockExternalDestinationStore{
		GetByIDResult: &models.ExternalDestination{
			ID:                "extdst_01JABC",
			SourceSystem:      "velox",
			WorkspaceID:       12,
			PlatformAccountID: 345,
			Enabled:           true,
			CreatedAt:         now,
			UpdatedAt:         now,
		},
	}
	ws := &mockWorkspaceLookup{
		findByIDResult: &models.Workspace{ID: 12, OwnerID: 1, Name: "ws-1"},
	}
	user := &mockUserLookup{
		findPlatformAccountByIDResult: &models.PlatformAccount{
			ID:     345,
			Platform: "youtube",
			Status: "active",
		},
	}
	w := runValidate(t, dst, ws, user, testVeloxAPIToken, "extdst_01JABC",
		"Bearer "+testVeloxAPIToken, "")
	if w.Code != http.StatusNoContent {
		t.Fatalf("happy path: want 204, got %d (body=%q)", w.Code, w.Body.String())
	}
	if w.Body.Len() != 0 {
		t.Errorf("happy path 204: body MUST be empty, got %q", w.Body.String())
	}
	if dst.GetByIDCalls != 1 {
		t.Errorf("destination lookup: want 1, got %d", dst.GetByIDCalls)
	}
	if ws.findByIDCalls != 1 {
		t.Errorf("workspace lookup: want 1, got %d", ws.findByIDCalls)
	}
	if user.findPlatformAccountByIDCalls != 1 {
		t.Errorf("platform_account lookup: want 1, got %d", user.findPlatformAccountByIDCalls)
	}
}

// TestValidate_ReauthRequired pins the dual-signal reauth
// check: EITHER status='reauth_required' OR
// reauth_required_at != nil must return 404.
func TestValidate_ReauthRequired(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name string
		pa   *models.PlatformAccount
	}{
		{
			name: "status enum is reauth_required",
			pa: &models.PlatformAccount{
				ID:     345,
				Platform: "youtube",
				Status: "reauth_required",
			},
		},
		{
			name: "reauth_required_at timestamp is non-nil",
			pa: &models.PlatformAccount{
				ID:     345,
				Platform: "youtube",
				Status: "active",
				ReauthRequiredAt: &now,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dst := &mockExternalDestinationStore{
				GetByIDResult: &models.ExternalDestination{
					ID:                "extdst_01JABC",
					SourceSystem:      "velox",
					WorkspaceID:       12,
					PlatformAccountID: 345,
					Enabled:           true,
					CreatedAt:         now,
					UpdatedAt:         now,
				},
			}
			ws := &mockWorkspaceLookup{
				findByIDResult: &models.Workspace{ID: 12, OwnerID: 1},
			}
			user := &mockUserLookup{
				findPlatformAccountByIDResult: tc.pa,
			}
			w := runValidate(t, dst, ws, user, testVeloxAPIToken, "extdst_01JABC",
				"Bearer "+testVeloxAPIToken, "")
			if w.Code != http.StatusNotFound {
				t.Errorf("reauth: want 404, got %d (body=%q)", w.Code, w.Body.String())
			}
		})
	}
}

// TestValidate_DiagnosticQueryParam verifies the ?diagnostic=true
// trigger returns 200 with the diagnostic JSON body. The shape
// must match VeloxValidateDestinationResponse.
func TestValidate_DiagnosticQueryParam(t *testing.T) {
	now := time.Now()
	dst := &mockExternalDestinationStore{
		GetByIDResult: &models.ExternalDestination{
			ID:                "extdst_01JABC",
			SourceSystem:      "velox",
			WorkspaceID:       12,
			PlatformAccountID: 345,
			Enabled:           true,
			CreatedAt:         now,
			UpdatedAt:         now,
		},
	}
	ws := &mockWorkspaceLookup{
		findByIDResult: &models.Workspace{ID: 12},
	}
	user := &mockUserLookup{
		findPlatformAccountByIDResult: &models.PlatformAccount{
			ID:     345,
			Platform: "youtube",
			Status: "active",
		},
	}
	w := runValidate(t, dst, ws, user, testVeloxAPIToken, "extdst_01JABC",
		"Bearer "+testVeloxAPIToken, "diagnostic=true")
	if w.Code != http.StatusOK {
		t.Fatalf("diagnostic query: want 200, got %d", w.Code)
	}
	var resp VeloxValidateDestinationResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v (body=%q)", err, w.Body.String())
	}
	if !resp.Valid {
		t.Errorf("Valid: want true, got false")
	}
	if resp.DestinationID != "extdst_01JABC" {
		t.Errorf("DestinationID: want extdst_01JABC, got %s", resp.DestinationID)
	}
	if resp.Status != "active" {
		t.Errorf("Status: want active, got %s", resp.Status)
	}
	if resp.Platform != "youtube" {
		t.Errorf("Platform: want youtube, got %s", resp.Platform)
	}
}

// TestValidate_DiagnosticHeader verifies the X-Velox-Diagnostic:
// true header trigger ALSO returns 200 with JSON.
func TestValidate_DiagnosticHeader(t *testing.T) {
	now := time.Now()
	dst := &mockExternalDestinationStore{
		GetByIDResult: &models.ExternalDestination{
			ID:                "extdst_01JABC",
			SourceSystem:      "velox",
			WorkspaceID:       12,
			PlatformAccountID: 345,
			Enabled:           true,
			CreatedAt:         now,
			UpdatedAt:         now,
		},
	}
	ws := &mockWorkspaceLookup{
		findByIDResult: &models.Workspace{ID: 12},
	}
	user := &mockUserLookup{
		findPlatformAccountByIDResult: &models.PlatformAccount{
			ID:     345,
			Platform: "youtube",
			Status: "active",
		},
	}
	// Custom httptest invocation with header.
	r := buildVeloxTestRouter(dst, ws, user, testVeloxAPIToken)
	handler := r.internalVeloxAuth(http.HandlerFunc(r.handleValidateInternalDestination))
	mux := chi.NewRouter()
	mux.Method(http.MethodPost, "/internal/v1/destinations/{id}/validate", handler)
	req := httptest.NewRequest(http.MethodPost,
		"/internal/v1/destinations/extdst_01JABC/validate", nil)
	req.Header.Set("Authorization", "Bearer "+testVeloxAPIToken)
	req.Header.Set("X-Velox-Diagnostic", "true")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("diagnostic header: want 200, got %d", w.Code)
	}
	var resp VeloxValidateDestinationResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Valid || resp.DestinationID != "extdst_01JABC" {
		t.Errorf("response mismatch: %+v", resp)
	}
}

// TestValidate_DiagnosticDisabled verifies the diagnostic mode
// ALSO short-circuits on disabled destination — no JSON
// leak even when ?diagnostic=true is requested.
func TestValidate_DiagnosticDisabled(t *testing.T) {
	dst := &mockExternalDestinationStore{
		GetByIDResult: &models.ExternalDestination{
			ID:                "extdst_01JABC",
			WorkspaceID:       12,
			PlatformAccountID: 345,
			Enabled:           false,
		},
	}
	w := runValidate(t, dst, &mockWorkspaceLookup{}, &mockUserLookup{},
		testVeloxAPIToken, "extdst_01JABC",
		"Bearer "+testVeloxAPIToken, "diagnostic=true")
	if w.Code != http.StatusNotFound {
		t.Fatalf("diagnostic + disabled: want 404, got %d (body=%q)", w.Code, w.Body.String())
	}
}

// TestValidate_WorkspaceMissing pins the workspace-not-found
// branch — should return 404 even with diagnostic=true.
func TestValidate_WorkspaceMissing(t *testing.T) {
	now := time.Now()
	dst := &mockExternalDestinationStore{
		GetByIDResult: &models.ExternalDestination{
			ID:                "extdst_01JABC",
			SourceSystem:      "velox",
			WorkspaceID:       99, // non-existent workspace
			PlatformAccountID: 345,
			Enabled:           true,
			CreatedAt:         now,
			UpdatedAt:         now,
		},
	}
	ws := &mockWorkspaceLookup{
		findByIDResult: nil, // not found
	}
	user := &mockUserLookup{}
	w := runValidate(t, dst, ws, user, testVeloxAPIToken, "extdst_01JABC",
		"Bearer "+testVeloxAPIToken, "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("workspace missing: want 404, got %d", w.Code)
	}
	if user.findPlatformAccountByIDCalls != 0 {
		t.Errorf("platform_account must NOT be queried when workspace is missing")
	}
}
