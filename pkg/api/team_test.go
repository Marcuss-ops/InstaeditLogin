package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// fakeTeamStore implements TeamStore for handler tests.
type fakeTeamStore struct {
	members map[int64]map[int64]string // workspaceID -> userID -> role
	invites map[string]*models.WorkspaceInvite
	nextID  int64
}

func newFakeTeamStore() *fakeTeamStore {
	return &fakeTeamStore{
		members: make(map[int64]map[int64]string),
		invites: make(map[string]*models.WorkspaceInvite),
		nextID:  10,
	}
}

func (f *fakeTeamStore) AddMember(wsID, userID int64, role string) error {
	if f.members[wsID] == nil {
		f.members[wsID] = make(map[int64]string)
	}
	f.members[wsID][userID] = role
	return nil
}
func (f *fakeTeamStore) RemoveMember(wsID, userID int64) error {
	if m, ok := f.members[wsID]; ok {
		delete(m, userID)
	}
	return nil
}
func (f *fakeTeamStore) ListMembers(wsID int64) ([]models.WorkspaceMember, error) {
	var out []models.WorkspaceMember
	for uid, role := range f.members[wsID] {
		out = append(out, models.WorkspaceMember{
			WorkspaceID: wsID, UserID: uid, Role: role, Email: "u@t.com", Name: "User",
		})
	}
	return out, nil
}
func (f *fakeTeamStore) GetRole(wsID, userID int64) (string, error) {
	if m, ok := f.members[wsID]; ok {
		return m[userID], nil
	}
	return "", nil
}
func (f *fakeTeamStore) IsAdmin(wsID, userID int64) (bool, error) {
	r, _ := f.GetRole(wsID, userID)
	return r == "admin", nil
}
func (f *fakeTeamStore) CreateInvite(wsID, invitedBy int64, email, role string) (*models.WorkspaceInvite, error) {
	id := f.nextID
	f.nextID++
	inv := &models.WorkspaceInvite{
		ID: id, WorkspaceID: wsID, Email: email, Role: role,
		Token: "tok-" + email, InvitedBy: invitedBy,
	}
	f.invites[inv.Token] = inv
	return inv, nil
}
func (f *fakeTeamStore) FindInviteByToken(token string) (*models.WorkspaceInvite, error) {
	return f.invites[token], nil
}
func (f *fakeTeamStore) AcceptInvite(token string, userID int64) error {
	inv := f.invites[token]
	if inv == nil {
		return nil
	}
	f.AddMember(inv.WorkspaceID, userID, inv.Role)
	return nil
}

// -----------------------------------------------------------------------
//  Tests
// -----------------------------------------------------------------------

func TestHandleListMembers(t *testing.T) {
	store := newFakeTeamStore()
	store.AddMember(1, 42, "admin")
	store.AddMember(1, 43, "editor")

	router := chi.NewRouter()
	r := &Router{teamStore: store}
	r.mux = router
	r.registerTeamRoutes()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/1/members", nil)
	ctx := auth.WithIdentity(req.Context(), auth.NewUserIdentity(42, 1, 1))
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d (body: %s)", w.Code, w.Body.String())
	}

	var resp map[string][]models.WorkspaceMember
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp["members"]) != 2 {
		t.Errorf("members count: want 2, got %d", len(resp["members"]))
	}
}

func TestHandleListMembers_NotMember(t *testing.T) {
	store := newFakeTeamStore()
	store.AddMember(1, 42, "admin")

	router := chi.NewRouter()
	r := &Router{teamStore: store}
	r.mux = router
	r.registerTeamRoutes()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/1/members", nil)
	ctx := auth.WithIdentity(req.Context(), auth.NewUserIdentity(99, 1, 1))
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("status: want 403, got %d", w.Code)
	}
}

func TestHandleCreateInvite_AdminOnly(t *testing.T) {
	store := newFakeTeamStore()
	store.AddMember(1, 42, "admin")
	store.AddMember(1, 43, "editor")

	router := chi.NewRouter()
	r := &Router{teamStore: store}
	r.mux = router
	r.registerTeamRoutes()

	body := map[string]string{"email": "new@example.com", "role": "editor"}
	b, _ := json.Marshal(body)

	// Editor attempts invite → 403.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/1/invites", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	ctx := auth.WithIdentity(req.Context(), auth.NewUserIdentity(43, 1, 1))
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("editor invite: want 403, got %d", w.Code)
	}

	// Admin invites → 201.
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/1/invites", bytes.NewReader(b))
	req2.Header.Set("Content-Type", "application/json")
	ctx2 := auth.WithIdentity(req2.Context(), auth.NewUserIdentity(42, 1, 1))
	req2 = req2.WithContext(ctx2)
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req2)
	if w2.Code != http.StatusCreated {
		t.Errorf("admin invite: want 201, got %d", w2.Code)
	}
}

func TestHandleRemoveMember(t *testing.T) {
	store := newFakeTeamStore()
	store.AddMember(1, 42, "admin")
	store.AddMember(1, 43, "editor")

	router := chi.NewRouter()
	r := &Router{teamStore: store}
	r.mux = router
	r.registerTeamRoutes()

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/workspaces/1/members/43", nil)
	ctx := auth.WithIdentity(req.Context(), auth.NewUserIdentity(42, 1, 1))
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("status: want 204, got %d (body: %s)", w.Code, w.Body.String())
	}

	members, _ := store.ListMembers(1)
	if len(members) != 1 {
		t.Errorf("members after removal: want 1, got %d", len(members))
	}
}

func TestHandleRemoveMember_CannotRemoveSelf(t *testing.T) {
	store := newFakeTeamStore()
	store.AddMember(1, 42, "admin")

	router := chi.NewRouter()
	r := &Router{teamStore: store}
	r.mux = router
	r.registerTeamRoutes()

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/workspaces/1/members/42", nil)
	ctx := auth.WithIdentity(req.Context(), auth.NewUserIdentity(42, 1, 1))
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("self-remove: want 400, got %d", w.Code)
	}
}

func TestHandleAcceptInvite(t *testing.T) {
	store := newFakeTeamStore()
	store.AddMember(1, 42, "admin")

	inv, _ := store.CreateInvite(1, 42, "invitee@example.com", "editor")

	// Create a real auth manager so r.protected() works.
	authMgr := auth.NewManager("test-team-secret", 24)

	router := chi.NewRouter()
	r := &Router{teamStore: store, auth: authMgr}
	r.mux = router
	r.registerTeamRoutes()

	// Issue a real JWT for user 99. SPRINT 7.1: must carry positive
	// sessionID so Manager.Verify accepts it (the team routes are
	// now behind r.protected which uses the same Verify contract).
	jwt, _, _, _ := authMgr.IssueAccess(99, 1, 1)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/invites/"+inv.Token, nil)
	req.Header.Set("Authorization", "Bearer "+jwt)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("accept invite: want 200, got %d (body: %s)", w.Code, w.Body.String())
	}

	role, _ := store.GetRole(1, 99)
	if role != "editor" {
		t.Errorf("accepted role: want editor, got %s", role)
	}
}

func TestRequireWorkspaceRole_NoAuth(t *testing.T) {
	store := newFakeTeamStore()
	store.AddMember(1, 42, "admin")

	router := chi.NewRouter()
	r := &Router{teamStore: store}
	r.mux = router
	r.registerTeamRoutes()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/1/members", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("no auth: want 401, got %d", w.Code)
	}
}
