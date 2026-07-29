package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// -----------------------------------------------------------------------
// Test fixtures (in-file; mirrors the patterns in
// internal_velox_validate_test.go).
// -----------------------------------------------------------------------

// mockGroupLookup carries the data + counter for the two
// GroupStore methods the resolve-target handler reaches
// (FindByID, ListAccountsInGroup). Adapter pattern with
// depth-0 direct overrides so the handler reaches the
// per-field toggles.
type mockGroupLookup struct {
	findByIDResult     *models.Group
	findByIDErr        error
	findByIDCalls      int
	listAccountsResult []int64
	listAccountsErr    error
	listAccountsCalls  int
}

type groupStoreAdapter struct {
	GroupStore
	m *mockGroupLookup
}

func (a *groupStoreAdapter) FindByID(_ int64) (*models.Group, error) {
	a.m.findByIDCalls++
	return a.m.findByIDResult, a.m.findByIDErr
}

func (a *groupStoreAdapter) ListAccountsInGroup(_ int64) ([]int64, error) {
	a.m.listAccountsCalls++
	return a.m.listAccountsResult, a.m.listAccountsErr
}

// Compose all OTHER GroupStore methods as no-ops so the adapter
// satisfies the interface signature even when tests don't
// exercise them. Errors aren't returned; tests stop short of
// surfacing depth-1 promoted methods through a future handler
// use case.
func (a *groupStoreAdapter) Create(_ *models.Group) error { return nil }
func (a *groupStoreAdapter) Update(_ *models.Group) error { return nil }
func (a *groupStoreAdapter) Delete(_ int64) error         { return nil }
func (a *groupStoreAdapter) ListByWorkspace(_ int64) ([]models.Group, error) {
	return nil, nil
}
func (a *groupStoreAdapter) ValidateAccountOwnership(_ int64, _ int64, _ []int64) ([]int64, error) {
	return nil, nil
}
func (a *groupStoreAdapter) SetAccounts(_ int64, _ []int64) error { return nil }

func wrapGroupLookup(m *mockGroupLookup) GroupStore {
	return &groupStoreAdapter{m: m}
}

// mockWorkspaceLookupResolve is the workspace-side mock for the
// resolve-target handler. It overrides the three reachable
// methods (FindByID, FindChannel, ListChannels) directly so
// there's no nil-embedded-interface risk.
type mockWorkspaceLookupResolve struct {
	findByIDResult     *models.Workspace
	findByIDErr        error
	findByIDCalls      int
	findChannelResult  *models.WorkspaceChannel
	findChannelErr     error
	findChannelCalls   int
	listChannelsResult []models.WorkspaceChannel
	listChannelsErr    error
	listChannelsCalls  int
}

type workspaceStoreAdapterResolve struct {
	WorkspaceStore
	m *mockWorkspaceLookupResolve
}

func (a *workspaceStoreAdapterResolve) FindByID(_ int64) (*models.Workspace, error) {
	a.m.findByIDCalls++
	return a.m.findByIDResult, a.m.findByIDErr
}

func (a *workspaceStoreAdapterResolve) FindChannel(_ context.Context, _ int64, _ int64) (*models.WorkspaceChannel, error) {
	a.m.findChannelCalls++
	return a.m.findChannelResult, a.m.findChannelErr
}

func (a *workspaceStoreAdapterResolve) ListChannels(_ context.Context, _ int64) ([]models.WorkspaceChannel, error) {
	a.m.listChannelsCalls++
	return a.m.listChannelsResult, a.m.listChannelsErr
}

func wrapWorkspaceLookupResolve(m *mockWorkspaceLookupResolve) WorkspaceStore {
	return &workspaceStoreAdapterResolve{m: m}
}

func wrapUserLookupResolve(m *mockUserLookup) UserStore {
	return &userStoreAdapter{m: m}
}

// buildResolveTargetRouter wires a fresh Router with the three
// mock lookups + token. VeloxModule.Register mounts the
// resolve-target route only when GroupStore + WorkspaceStore +
// UserStore are non-nil; this fixture satisfies the gate.
func buildResolveTargetRouter(group *mockGroupLookup, ws *mockWorkspaceLookupResolve, user *mockUserLookup, token string) *Router {
	dst := &mockExternalDestinationStore{
		GetByIDResult: &models.ExternalDestination{
			ID:                "extdst_01JUNUSED",
			SourceSystem:      "velox",
			WorkspaceID:       1,
			PlatformAccountID: 1,
			Enabled:           true,
			CreatedAt:         time.Now(),
			UpdatedAt:         time.Now(),
		},
	}
	r := &Router{
		externalDestinations: dst,
		workspaceStore:       wrapWorkspaceLookupResolve(ws),
		userRepo:             wrapUserLookupResolve(user),
		groupStore:           wrapGroupLookup(group),
		veloxAPIToken:        token,
	}
	return r
}

// runResolveTarget wires an httptest request to the resolve
// target handler and returns the recorded response.
func runResolveTarget(t *testing.T, group *mockGroupLookup, ws *mockWorkspaceLookupResolve, user *mockUserLookup, token, body, authHeader string) *httptest.ResponseRecorder {
	t.Helper()
	r := buildResolveTargetRouter(group, ws, user, token)
	mux := chi.NewRouter()
	mux.Method(http.MethodPost, "/internal/v1/destinations/resolve-target",
		r.internalVeloxAuth(http.HandlerFunc(r.handleResolveTargetInternalDestination)))
	var bodyReader *bytes.Reader
	if body == "" {
		bodyReader = bytes.NewReader(nil)
	} else {
		bodyReader = bytes.NewReader([]byte(body))
	}
	req := httptest.NewRequest(http.MethodPost,
		"/internal/v1/destinations/resolve-target", bodyReader)
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

// -----------------------------------------------------------------------
// Tests
// -----------------------------------------------------------------------

func TestResolveTarget_MissingAuthHeader(t *testing.T) {
	ws := &mockWorkspaceLookupResolve{}
	user := &mockUserLookup{}
	group := &mockGroupLookup{}
	w := runResolveTarget(t, group, ws, user, testVeloxAPIToken,
		mustJSON(t, map[string]interface{}{"workspace_id": 1, "platform": "youtube", "target": map[string]interface{}{"type": "channel", "platform_account_id": 381}}), "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
	if ws.findByIDCalls != 0 || user.findPlatformAccountByIDCalls != 0 || group.findByIDCalls != 0 {
		t.Errorf("downstream lookups must NOT fire when auth is missing")
	}
}

func TestResolveTarget_WrongToken(t *testing.T) {
	ws := &mockWorkspaceLookupResolve{}
	user := &mockUserLookup{}
	group := &mockGroupLookup{}
	w := runResolveTarget(t, group, ws, user, testVeloxAPIToken, "", "Bearer wrong-token-aaaaaaaaaaaaaaaaaaaa")
	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d", w.Code)
	}
	if ws.findByIDCalls != 0 {
		t.Errorf("workspace.findByIDCalls: want 0; got %d", ws.findByIDCalls)
	}
}

func TestResolveTarget_HappyChannelByAccountID(t *testing.T) {
	ws := &mockWorkspaceLookupResolve{
		findByIDResult:    &models.Workspace{ID: 12, OwnerID: 1},
		findChannelResult: &models.WorkspaceChannel{WorkspaceID: 12, PlatformAccountID: 381, Enabled: true},
	}
	user := &mockUserLookup{
		findPlatformAccountByIDResult: &models.PlatformAccount{
			ID: 381, Platform: "youtube", PlatformUserID: "UCxxxxxxxx",
			Username: "Wrestling Discovery", Status: models.AccountStatusActive,
		},
	}
	group := &mockGroupLookup{}
	body := mustJSON(t, map[string]interface{}{
		"workspace_id": 12, "platform": "youtube",
		"target": map[string]interface{}{"type": "channel", "platform_account_id": 381},
	})
	w := runResolveTarget(t, group, ws, user, testVeloxAPIToken, body, "Bearer "+testVeloxAPIToken)
	if w.Code != http.StatusOK {
		t.Fatalf("happy path: want 200, got %d (body=%q)", w.Code, w.Body.String())
	}
	var resp VeloxResolveTargetResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Valid {
		t.Errorf("Valid: want true; got false (response=%+v)", resp)
	}
	if resp.DestinationID != "instaedit_youtube" {
		t.Errorf("DestinationID: want instaedit_youtube; got %q", resp.DestinationID)
	}
	if len(resp.ResolvedTargets) != 1 {
		t.Fatalf("ResolvedTargets len: want 1; got %d (%+v)", len(resp.ResolvedTargets), resp.ResolvedTargets)
	}
	got := resp.ResolvedTargets[0]
	if got.PlatformAccountID != 381 || got.ChannelID != "UCxxxxxxxx" || got.Status != "active" || !got.Enabled {
		t.Errorf("entry shape mismatch: %+v", got)
	}
	if got.ChannelName != "Wrestling Discovery" {
		t.Errorf("ChannelName: want Wrestling Discovery; got %q", got.ChannelName)
	}
}

func TestResolveTarget_HappyChannelByChannelID(t *testing.T) {
	ws := &mockWorkspaceLookupResolve{
		findByIDResult: &models.Workspace{ID: 12, OwnerID: 1},
		listChannelsResult: []models.WorkspaceChannel{
			{WorkspaceID: 12, PlatformAccountID: 100, Enabled: true},
			{WorkspaceID: 12, PlatformAccountID: 381, Enabled: true},
		},
		findChannelResult: &models.WorkspaceChannel{WorkspaceID: 12, PlatformAccountID: 381, Enabled: true},
	}
	user := &mockUserLookup{
		findPlatformAccountByIDResult: &models.PlatformAccount{
			ID: 381, Platform: "youtube", PlatformUserID: "UCxxxxxxxx",
			Username: "Wrestling Discovery", Status: models.AccountStatusActive,
		},
	}
	group := &mockGroupLookup{}
	body := mustJSON(t, map[string]interface{}{
		"workspace_id": 12, "platform": "youtube",
		"target": map[string]interface{}{"type": "channel", "channel_id": "UCxxxxxxxx"},
	})
	w := runResolveTarget(t, group, ws, user, testVeloxAPIToken, body, "Bearer "+testVeloxAPIToken)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (body=%q)", w.Code, w.Body.String())
	}
	var resp VeloxResolveTargetResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Valid || len(resp.ResolvedTargets) != 1 {
		t.Errorf("happy channel_id: %+v", resp)
	}
}

func TestResolveTarget_ChannelIDNoMatch(t *testing.T) {
	ws := &mockWorkspaceLookupResolve{
		findByIDResult:     &models.Workspace{ID: 12, OwnerID: 1},
		listChannelsResult: []models.WorkspaceChannel{{WorkspaceID: 12, PlatformAccountID: 100, Enabled: true}},
	}
	user := &mockUserLookup{}
	group := &mockGroupLookup{}
	body := mustJSON(t, map[string]interface{}{
		"workspace_id": 12, "platform": "youtube",
		"target": map[string]interface{}{"type": "channel", "channel_id": "UCnotbound"},
	})
	w := runResolveTarget(t, group, ws, user, testVeloxAPIToken, body, "Bearer "+testVeloxAPIToken)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("TARGET_NOT_AVAILABLE: want 422, got %d (body=%q)", w.Code, w.Body.String())
	}
	var resp VeloxResolveTargetResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.ErrorCode != "TARGET_NOT_AVAILABLE" {
		t.Errorf("error_code: want TARGET_NOT_AVAILABLE; got %q", resp.ErrorCode)
	}
}

func TestResolveTarget_DisabledBinding(t *testing.T) {
	ws := &mockWorkspaceLookupResolve{
		findByIDResult:    &models.Workspace{ID: 12, OwnerID: 1},
		findChannelResult: &models.WorkspaceChannel{WorkspaceID: 12, PlatformAccountID: 381, Enabled: false},
	}
	user := &mockUserLookup{
		findPlatformAccountByIDResult: &models.PlatformAccount{
			ID: 381, Platform: "youtube", PlatformUserID: "UCxxx",
			Username: "Disabled Channel", Status: models.AccountStatusActive,
		},
	}
	group := &mockGroupLookup{}
	body := mustJSON(t, map[string]interface{}{
		"workspace_id": 12, "platform": "youtube",
		"target": map[string]interface{}{"type": "channel", "platform_account_id": 381},
	})
	w := runResolveTarget(t, group, ws, user, testVeloxAPIToken, body, "Bearer "+testVeloxAPIToken)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("disabled: want 422, got %d", w.Code)
	}
	var resp VeloxResolveTargetResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.ErrorCode != "TARGET_NOT_AVAILABLE" {
		t.Errorf("error_code: want TARGET_NOT_AVAILABLE; got %q", resp.ErrorCode)
	}
	if len(resp.ResolvedTargets) != 1 || resp.ResolvedTargets[0].TargetErrorCode != "TARGET_NOT_AVAILABLE" {
		t.Errorf("expected 1 entry with TARGET_NOT_AVAILABLE per-target; got %+v", resp.ResolvedTargets)
	}
}

func TestResolveTarget_ReauthRequired(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name string
		pa   *models.PlatformAccount
	}{
		{"status_enum", &models.PlatformAccount{ID: 381, Platform: "youtube", PlatformUserID: "UCxxx", Username: "R1", Status: models.AccountStatusReauthRequired}},
		{"reauth_required_at", &models.PlatformAccount{ID: 381, Platform: "youtube", PlatformUserID: "UCxxx", Username: "R2", Status: models.AccountStatusActive, ReauthRequiredAt: &now}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ws := &mockWorkspaceLookupResolve{
				findByIDResult:    &models.Workspace{ID: 12, OwnerID: 1},
				findChannelResult: &models.WorkspaceChannel{WorkspaceID: 12, PlatformAccountID: 381, Enabled: true},
			}
			user := &mockUserLookup{findPlatformAccountByIDResult: tc.pa}
			group := &mockGroupLookup{}
			body := mustJSON(t, map[string]interface{}{
				"workspace_id": 12, "platform": "youtube",
				"target": map[string]interface{}{"type": "channel", "platform_account_id": 381},
			})
			w := runResolveTarget(t, group, ws, user, testVeloxAPIToken, body, "Bearer "+testVeloxAPIToken)
			if w.Code != http.StatusUnprocessableEntity {
				t.Fatalf("reauth: want 422, got %d (body=%q)", w.Code, w.Body.String())
			}
			var resp VeloxResolveTargetResponse
			_ = json.Unmarshal(w.Body.Bytes(), &resp)
			if resp.ErrorCode != "BLOCKED_AUTH" {
				t.Errorf("error_code: want BLOCKED_AUTH; got %q", resp.ErrorCode)
			}
		})
	}
}

func TestResolveTarget_RevokedStatus(t *testing.T) {
	cases := []struct{ name, status string }{
		{"revoked", models.AccountStatusRevoked},
		{"disconnected", models.AccountStatusDisconnected},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ws := &mockWorkspaceLookupResolve{
				findByIDResult:    &models.Workspace{ID: 12, OwnerID: 1},
				findChannelResult: &models.WorkspaceChannel{WorkspaceID: 12, PlatformAccountID: 381, Enabled: true},
			}
			user := &mockUserLookup{
				findPlatformAccountByIDResult: &models.PlatformAccount{
					ID: 381, Platform: "youtube", PlatformUserID: "UCxxx",
					Username: "Revoked", Status: tc.status,
				},
			}
			group := &mockGroupLookup{}
			body := mustJSON(t, map[string]interface{}{
				"workspace_id": 12, "platform": "youtube",
				"target": map[string]interface{}{"type": "channel", "platform_account_id": 381},
			})
			w := runResolveTarget(t, group, ws, user, testVeloxAPIToken, body, "Bearer "+testVeloxAPIToken)
			if w.Code != http.StatusUnprocessableEntity {
				t.Fatalf("%s: want 422, got %d", tc.name, w.Code)
			}
			var resp VeloxResolveTargetResponse
			_ = json.Unmarshal(w.Body.Bytes(), &resp)
			if resp.ErrorCode != "TARGET_NOT_AVAILABLE" {
				t.Errorf("%s: error_code want TARGET_NOT_AVAILABLE; got %q", tc.name, resp.ErrorCode)
			}
		})
	}
}

func TestResolveTarget_ChannelBindingMismatch(t *testing.T) {
	ws := &mockWorkspaceLookupResolve{
		findByIDResult:    &models.Workspace{ID: 12, OwnerID: 1},
		findChannelResult: &models.WorkspaceChannel{WorkspaceID: 12, PlatformAccountID: 381, Enabled: true},
	}
	user := &mockUserLookup{
		findPlatformAccountByIDResult: &models.PlatformAccount{
			ID: 381, Platform: "youtube", PlatformUserID: "UC_real_owner",
			Username: "Real Owner", Status: models.AccountStatusActive,
		},
	}
	group := &mockGroupLookup{}
	body := mustJSON(t, map[string]interface{}{
		"workspace_id": 12, "platform": "youtube",
		"target": map[string]interface{}{"type": "channel", "platform_account_id": 381, "channel_id": "UC_expected_different"},
	})
	w := runResolveTarget(t, group, ws, user, testVeloxAPIToken, body, "Bearer "+testVeloxAPIToken)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("binding mismatch: want 422, got %d", w.Code)
	}
	var resp VeloxResolveTargetResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.ErrorCode != "BLOCKED_AUTH" {
		t.Errorf("error_code: want BLOCKED_AUTH; got %q", resp.ErrorCode)
	}
	if len(resp.ResolvedTargets) != 1 || resp.ResolvedTargets[0].ChannelID != "UC_real_owner" {
		t.Errorf("entry should reflect actual binding; got %+v", resp.ResolvedTargets)
	}
}

// TestResolveTarget_GroupHappy exercises group expansion with
// 2 active+enabled member accounts.
//
// It pins the per-id adapter contract: perWorkspaceChannelAdapter
// MUST override FindByID + ListChannels so the embedded
// (nil) WorkspaceStore methods don't panic, and FindChannel
// uses the per-account-id lookup. Without the overrides the
// group test panics on the first ws.FindByID call from the
// resolve-target handler. (Code-reviewer caught this in the
// first review pass.)
func TestResolveTarget_GroupHappy(t *testing.T) {
	perIDMock := &perIDUserLookup{
		rows: map[int64]*models.PlatformAccount{
			381: {ID: 381, Platform: "youtube", PlatformUserID: "UC1", Username: "Channel 1", Status: models.AccountStatusActive},
			382: {ID: 382, Platform: "youtube", PlatformUserID: "UC2", Username: "Channel 2", Status: models.AccountStatusActive},
		},
	}
	perIDAdapter := &perIDUserAdapter{m: perIDMock}

	perWSChannelMock := &perWorkspaceChannelLookup{
		rows: map[int64]*models.WorkspaceChannel{
			381: {WorkspaceID: 12, PlatformAccountID: 381, Enabled: true},
			382: {WorkspaceID: 12, PlatformAccountID: 382, Enabled: true},
		},
	}
	perWSAdapter := &perWorkspaceChannelAdapter{
		m:              perWSChannelMock,
		fixedWorkspace: &models.Workspace{ID: 12, OwnerID: 1},
	}

	group := &mockGroupLookup{
		findByIDResult:     &models.Group{ID: 27, WorkspaceID: 12, Name: "Top Channels"},
		listAccountsResult: []int64{381, 382},
	}

	// We can't use runResolveTarget because the perWorkspaceChannelAdapter
	// routing requires a custom Router wiring path; build the
	// Router + mux inline.
	r := &Router{
		externalDestinations: &mockExternalDestinationStore{
			GetByIDResult: &models.ExternalDestination{ID: "extdst_01JUNUSED", Enabled: true},
		},
		workspaceStore: perWSAdapter.asWorkspaceStore(),
		userRepo:       perIDAdapter.asUserStore(),
		groupStore:     wrapGroupLookup(group),
		veloxAPIToken:  testVeloxAPIToken,
	}
	mux := chi.NewRouter()
	mux.Method(http.MethodPost, "/internal/v1/destinations/resolve-target",
		r.internalVeloxAuth(http.HandlerFunc(r.handleResolveTargetInternalDestination)))
	body := mustJSON(t, map[string]interface{}{
		"workspace_id": 12, "platform": "youtube",
		"target": map[string]interface{}{"type": "group", "group_id": 27},
	})
	req := httptest.NewRequest(http.MethodPost,
		"/internal/v1/destinations/resolve-target", bytes.NewReader([]byte(body)))
	req.Header.Set("Authorization", "Bearer "+testVeloxAPIToken)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("group happy: want 200, got %d (body=%q)", w.Code, w.Body.String())
	}
	var resp VeloxResolveTargetResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if !resp.Valid {
		t.Errorf("Valid: want true; got %+v", resp)
	}
	if len(resp.ResolvedTargets) != 2 {
		t.Errorf("ResolvedTargets len: want 2; got %d (%+v)", len(resp.ResolvedTargets), resp.ResolvedTargets)
	}
}

func TestResolveTarget_GroupEmpty(t *testing.T) {
	ws := &mockWorkspaceLookupResolve{
		findByIDResult: &models.Workspace{ID: 12, OwnerID: 1},
	}
	user := &mockUserLookup{}
	group := &mockGroupLookup{
		findByIDResult:     &models.Group{ID: 27, WorkspaceID: 12, Name: "Empty"},
		listAccountsResult: []int64{},
	}
	body := mustJSON(t, map[string]interface{}{
		"workspace_id": 12, "platform": "youtube",
		"target": map[string]interface{}{"type": "group", "group_id": 27},
	})
	w := runResolveTarget(t, group, ws, user, testVeloxAPIToken, body, "Bearer "+testVeloxAPIToken)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("GROUP_EMPTY: want 422, got %d (body=%q)", w.Code, w.Body.String())
	}
	var resp VeloxResolveTargetResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.ErrorCode != "GROUP_EMPTY" {
		t.Errorf("error_code: want GROUP_EMPTY; got %q", resp.ErrorCode)
	}
}

func TestResolveTarget_GroupWrongWorkspace(t *testing.T) {
	ws := &mockWorkspaceLookupResolve{
		findByIDResult: &models.Workspace{ID: 12, OwnerID: 1},
	}
	user := &mockUserLookup{}
	group := &mockGroupLookup{
		findByIDResult:     &models.Group{ID: 27, WorkspaceID: 99, Name: "Alien"},
		listAccountsResult: []int64{381},
	}
	body := mustJSON(t, map[string]interface{}{
		"workspace_id": 12, "platform": "youtube",
		"target": map[string]interface{}{"type": "group", "group_id": 27},
	})
	w := runResolveTarget(t, group, ws, user, testVeloxAPIToken, body, "Bearer "+testVeloxAPIToken)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("cross-workspace group: want 422, got %d", w.Code)
	}
	var resp VeloxResolveTargetResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.ErrorCode != "TARGET_NOT_AVAILABLE" {
		t.Errorf("error_code: want TARGET_NOT_AVAILABLE; got %q", resp.ErrorCode)
	}
	if group.listAccountsCalls != 0 {
		t.Errorf("ListAccountsInGroup must NOT be called when group is in a different workspace; got %d calls", group.listAccountsCalls)
	}
}

func TestResolveTarget_Validation_BadJSON(t *testing.T) {
	ws := &mockWorkspaceLookupResolve{}
	user := &mockUserLookup{}
	group := &mockGroupLookup{}
	w := runResolveTarget(t, group, ws, user, testVeloxAPIToken, "{not-json", "Bearer "+testVeloxAPIToken)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("bad JSON: want 400, got %d", w.Code)
	}
}

func TestResolveTarget_Validation_AmbiguousChannel(t *testing.T) {
	ws := &mockWorkspaceLookupResolve{
		findByIDResult: &models.Workspace{ID: 12, OwnerID: 1},
	}
	user := &mockUserLookup{}
	group := &mockGroupLookup{}
	body := mustJSON(t, map[string]interface{}{
		"workspace_id": 12, "platform": "youtube",
		"target": map[string]interface{}{
			"type": "channel", "platform_account_id": 381, "channel_id": "UCxxx",
		},
	})
	w := runResolveTarget(t, group, ws, user, testVeloxAPIToken, body, "Bearer "+testVeloxAPIToken)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("ambiguous channel: want 422, got %d", w.Code)
	}
}

func TestResolveTarget_Validation_UnsupportedPlatform(t *testing.T) {
	ws := &mockWorkspaceLookupResolve{
		findByIDResult: &models.Workspace{ID: 12, OwnerID: 1},
	}
	user := &mockUserLookup{}
	group := &mockGroupLookup{}
	body := mustJSON(t, map[string]interface{}{
		"workspace_id": 12, "platform": "tiktok",
		"target": map[string]interface{}{"type": "channel", "platform_account_id": 381},
	})
	w := runResolveTarget(t, group, ws, user, testVeloxAPIToken, body, "Bearer "+testVeloxAPIToken)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unsupported platform: want 422, got %d", w.Code)
	}
}

func TestResolveTarget_WorkspaceNotFound(t *testing.T) {
	ws := &mockWorkspaceLookupResolve{
		findByIDResult: nil,
	}
	user := &mockUserLookup{}
	group := &mockGroupLookup{}
	body := mustJSON(t, map[string]interface{}{
		"workspace_id": 99, "platform": "youtube",
		"target": map[string]interface{}{"type": "channel", "platform_account_id": 381},
	})
	w := runResolveTarget(t, group, ws, user, testVeloxAPIToken, body, "Bearer "+testVeloxAPIToken)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("workspace missing: want 422, got %d", w.Code)
	}
	var resp VeloxResolveTargetResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.ErrorCode != "TARGET_NOT_AVAILABLE" {
		t.Errorf("error_code: want TARGET_NOT_AVAILABLE; got %q", resp.ErrorCode)
	}
}

func mustJSON(t *testing.T, v interface{}) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("mustJSON marshal: %v", err)
	}
	return string(b)
}

// -----------------------------------------------------------------------
// Per-id lookup variants for tests that need the handler to
// call FindPlatformAccountByID / FindChannel multiple times
// with different arg sets (group expansion).
//
// perWorkspaceChannelAdapter "completes" the interface by
// overriding the three methods the handler actually reaches —
// the embedded (nil) WorkspaceStore would otherwise panic on
// any promoted-method dispatch. critical for TestResolveTarget_GroupHappy
// where the handler's first FindByID call lands on the
// adapter before the per-id FindChannel loop runs.
// -----------------------------------------------------------------------

type perIDUserLookup struct {
	rows map[int64]*models.PlatformAccount
	err  map[int64]error
}

func (m *perIDUserLookup) find(id int64) (*models.PlatformAccount, error) {
	if e, ok := m.err[id]; ok {
		return nil, e
	}
	r, ok := m.rows[id]
	if !ok {
		return nil, nil
	}
	return r, nil
}

type perIDUserAdapter struct {
	UserStore
	m *perIDUserLookup
}

func (a *perIDUserAdapter) FindPlatformAccountByID(id int64) (*models.PlatformAccount, error) {
	return a.m.find(id)
}

func (a *perIDUserAdapter) asUserStore() UserStore { return a }

type perWorkspaceChannelLookup struct {
	rows map[int64]*models.WorkspaceChannel
	err  map[int64]error
}

func (m *perWorkspaceChannelLookup) find(_ int64, accountID int64) (*models.WorkspaceChannel, error) {
	if e, ok := m.err[accountID]; ok {
		return nil, e
	}
	r, ok := m.rows[accountID]
	if !ok {
		return nil, nil
	}
	return r, nil
}

type perWorkspaceChannelAdapter struct {
	WorkspaceStore
	m *perWorkspaceChannelLookup
	// fixedWorkspace is the row FindByID returns. Required to
	// avoid panicking on the embedded (nil) WorkspaceStore
	// interface. ListChannels returns empty harmlessly because
	// the group test does not exercise the channel_id-resolution
	// branch.
	fixedWorkspace *models.Workspace
}

func (a *perWorkspaceChannelAdapter) FindByID(_ int64) (*models.Workspace, error) {
	return a.fixedWorkspace, nil
}

func (a *perWorkspaceChannelAdapter) FindChannel(_ context.Context, _ int64, accountID int64) (*models.WorkspaceChannel, error) {
	return a.m.find(0, accountID)
}

func (a *perWorkspaceChannelAdapter) ListChannels(_ context.Context, _ int64) ([]models.WorkspaceChannel, error) {
	return nil, nil
}

func (a *perWorkspaceChannelAdapter) asWorkspaceStore() WorkspaceStore { return a }
