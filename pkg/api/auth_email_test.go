package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// fakeAuthEmailStore implements AuthEmailStore for handler tests.
type fakeAuthEmailStore struct {
	users  map[string]fakeUser
	nextID int64
}

type fakeUser struct {
	email        string
	name         string
	passwordHash string // plaintext for test simplicity
	userID       int64
}

func newFakeAuthEmailStore() *fakeAuthEmailStore {
	return &fakeAuthEmailStore{
		users:  make(map[string]fakeUser),
		nextID: 1,
	}
}

func (f *fakeAuthEmailStore) Register(email, password, name string) (*models.User, int64, error) {
	if _, ok := f.users[email]; ok {
		return nil, 0, services.ErrEmailAlreadyTaken
	}
	if len(password) < 8 {
		return nil, 0, services.ErrPasswordTooShort
	}
	hasDigit := false
	for _, c := range password {
		if c >= '0' && c <= '9' {
			hasDigit = true
			break
		}
	}
	if !hasDigit {
		return nil, 0, services.ErrPasswordNoDigit
	}
	id := f.nextID
	f.nextID++
	f.users[email] = fakeUser{
		email:        email,
		name:         name,
		passwordHash: password,
		userID:       id,
	}
	return &models.User{ID: id, Email: email, Name: name}, id, nil
}

func (f *fakeAuthEmailStore) Login(email, password string) (*models.User, int64, error) {
	u, ok := f.users[email]
	if !ok {
		return nil, 0, services.ErrInvalidPassword
	}
	if u.passwordHash != password {
		return nil, 0, services.ErrInvalidPassword
	}
	return &models.User{ID: u.userID, Email: email, Name: u.name}, u.userID, nil
}

// -----------------------------------------------------------------------
//  Tests
// -----------------------------------------------------------------------

func TestHandleRegister_HappyPath(t *testing.T) {
	store := newFakeAuthEmailStore()
	r := newAuthEmailTestRouter(store)

	body := map[string]string{"email": "new@example.com", "password": "password1", "name": "New User"}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Admin-Token", testAdminToken)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("status: want 201, got %d (body: %s)", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["email"] != "new@example.com" {
		t.Errorf("email: want new@example.com, got %v", resp["email"])
	}
	if _, ok := resp["user_id"]; !ok {
		t.Error("response missing user_id")
	}
}

func TestHandleRegister_DuplicateEmail(t *testing.T) {
	store := newFakeAuthEmailStore()
	r := newAuthEmailTestRouter(store)

	body := map[string]string{"email": "dupe@example.com", "password": "password1", "name": "First"}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Admin-Token", testAdminToken)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("first register: want 201, got %d", w.Code)
	}

	// Duplicate.
	body2 := map[string]string{"email": "dupe@example.com", "password": "password1", "name": "Second"}
	b2, _ := json.Marshal(body2)
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", bytes.NewReader(b2))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("X-Admin-Token", testAdminToken)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusConflict {
		t.Errorf("duplicate register: want 409, got %d", w2.Code)
	}
}

func TestHandleRegister_WeakPassword(t *testing.T) {
	store := newFakeAuthEmailStore()
	r := newAuthEmailTestRouter(store)

	body := map[string]string{"email": "weak@example.com", "password": "abc", "name": "Weak"}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Admin-Token", testAdminToken)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status: want 400, got %d", w.Code)
	}
}

// TestHandleRegister_InviteOnly guards the invite-only beta gate:
// missing or wrong X-Admin-Token must always return 403, regardless
// of body content. The router is constructed with a non-empty admin
// token (see newAuthEmailTestRouter) so the constant-time compare
// runs against testAdminToken.
func TestHandleRegister_InviteOnly(t *testing.T) {
	store := newFakeAuthEmailStore()
	r := newAuthEmailTestRouter(store)

	body := map[string]string{"email": "x@example.com", "password": "password1", "name": "X"}
	b, _ := json.Marshal(body)

	// Missing header.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("missing token: want 403, got %d (body: %s)", w.Code, w.Body.String())
	}

	// Wrong header.
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", bytes.NewReader(b))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("X-Admin-Token", "definitely-wrong")
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusForbidden {
		t.Errorf("wrong token: want 403, got %d (body: %s)", w2.Code, w2.Body.String())
	}
}

// TestHandleRegister_EmptyConfigDisabled asserts the empty-token
// fail-closed posture: when the Router's adminInviteToken is empty
// (operator forgot to set ADMIN_INVITE_TOKEN), registration is
// unconditionally 403.
func TestHandleRegister_EmptyConfigDisabled(t *testing.T) {
	store := newFakeAuthEmailStore()
	r := &Router{authEmailSvc: store, sessionsSvc: &fakeSessionsStore{}}
	r.mux = chi.NewRouter()
	r.registerAuthEmailRoutes()

	body := map[string]string{"email": "x@example.com", "password": "password1", "name": "X"}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Admin-Token", "anything")
	w := httptest.NewRecorder()
	// *Router has no ServeHTTP — call through r.mux (the *chi.Mux).
	r.mux.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("empty config: want 403, got %d", w.Code)
	}
}

func TestHandleLogin_Success(t *testing.T) {
	store := newFakeAuthEmailStore()
	r := newAuthEmailTestRouter(store)

	// Register.
	body := map[string]string{"email": "login@example.com", "password": "password1", "name": "Login"}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Admin-Token", testAdminToken)
	r.ServeHTTP(httptest.NewRecorder(), req)

	store.users["login@example.com"] = fakeUser{
		email: "login@example.com", name: "Login", passwordHash: "password1",
		userID: 1,
	}

	// Login.
	loginBody := map[string]string{"email": "login@example.com", "password": "password1"}
	lb, _ := json.Marshal(loginBody)
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(lb))
	req2.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req2)

	if w.Code != http.StatusOK {
		t.Errorf("status: want 200, got %d (body: %s)", w.Code, w.Body.String())
	}
}

func TestHandleLogin_WrongPassword(t *testing.T) {
	store := newFakeAuthEmailStore()
	r := newAuthEmailTestRouter(store)

	// Register and verify.
	body := map[string]string{"email": "pwd@example.com", "password": "correct1", "name": "Pwd"}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Admin-Token", testAdminToken)
	r.ServeHTTP(httptest.NewRecorder(), req)

	store.users["pwd@example.com"] = fakeUser{
		email: "pwd@example.com", name: "Pwd", passwordHash: "correct1",
		userID: 1,
	}

	// Login with wrong password.
	loginBody := map[string]string{"email": "pwd@example.com", "password": "wrong1"}
	lb, _ := json.Marshal(loginBody)
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(lb))
	req2.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req2)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status: want 401, got %d", w.Code)
	}
}

// fakeSessionsStore is the SPRINT 7.4 (P0#14-blocco-1.4) test fixture
// for the SessionsStore interface. Returns fixed strings for the
// access/refresh tokens; cookies are written but never verified in
// these unit tests (they only check status codes + JSON bodies).
// Implemented as methods-on-pointer so it satisfies the interface
// structural typing cleanly.
type fakeSessionsStore struct {
	nextSessionID int64
}

func (f *fakeSessionsStore) Start(req services.StartSessionRequest) (*services.StartSessionResult, error) {
	if f.nextSessionID == 0 {
		f.nextSessionID = 1
	}
	f.nextSessionID++
	return &services.StartSessionResult{
		SessionID:        f.nextSessionID,
		AccessToken:      fmt.Sprintf("fake-access-token-u%d", req.UserID),
		AccessJTI:        fmt.Sprintf("fake-jti-%d", req.UserID),
		AccessExpiresAt:  time.Now().Add(15 * time.Minute),
		RefreshToken:     fmt.Sprintf("fake-refresh-token-u%d", req.UserID),
		RefreshHash:      []byte("fake-refresh-hash"),
		RefreshExpiresAt: time.Now().Add(30 * 24 * time.Hour),
	}, nil
}

func (f *fakeSessionsStore) Refresh(_ services.RefreshRequest) (*services.StartSessionResult, error) {
	f.nextSessionID++
	return &services.StartSessionResult{
		SessionID:        f.nextSessionID,
		AccessToken:      "fake-access-token-refresh",
		RefreshToken:     "fake-refresh-token-refresh",
		AccessExpiresAt:  time.Now().Add(15 * time.Minute),
		RefreshExpiresAt: time.Now().Add(30 * 24 * time.Hour),
	}, nil
}

func (f *fakeSessionsStore) Revoke(_, _ int64, _ string) error          { return nil }
func (f *fakeSessionsStore) RevokeAll(_ int64, _ string) (int64, error) { return 0, nil }
func (f *fakeSessionsStore) List(_ int64) ([]repository.Session, error) { return nil, nil }
func (f *fakeSessionsStore) WithdrawFromCookie(_ string) error          { return nil }
func (f *fakeSessionsStore) IsActive(_ int64) (bool, error)             { return true, nil }

// testAdminToken is the shared invite token used by the handler
// tests to authenticate against the public /register endpoint
// (which is now gated by WithAdminInviteToken). Production never
// sees this value.
const testAdminToken = "test-admin-token-32+chars-here-abcdef"

// newAuthEmailTestRouter creates a minimal Router with only the auth email
// routes wired, using the given fake store. SPRINT 7.4: also wires a
// fakeSessionsStore so the handlers (handleRegister / handleLoginEmail)
// can complete the session-bound JWT mint without dragging in a real
// *sql.DB-bound SessionRepository. Invite-only beta: the admin invite
// token is pre-set to testAdminToken so happy-path register tests can
// present the X-Admin-Token header.
func newAuthEmailTestRouter(store AuthEmailStore) *chi.Mux {
	r := &Router{
		authEmailSvc:     store,
		sessionsSvc:      &fakeSessionsStore{},
		adminInviteToken: testAdminToken,
	}
	r.mux = chi.NewRouter()
	r.registerAuthEmailRoutes()
	return r.mux
}
