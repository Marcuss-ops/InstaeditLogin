package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// fakeAuthEmailStore implements AuthEmailStore for handler tests.
type fakeAuthEmailStore struct {
	users   map[string]fakeUser
	nextID  int64
}

type fakeUser struct {
	email        string
	name         string
	passwordHash string // plaintext for test simplicity
	verified     bool
	userID       int64
	tokens       map[string]string // token -> purpose
}

func newFakeAuthEmailStore() *fakeAuthEmailStore {
	return &fakeAuthEmailStore{
		users:  make(map[string]fakeUser),
		nextID: 1,
	}
}

func (f *fakeAuthEmailStore) Register(email, password, name string) (int64, string, error) {
	if _, ok := f.users[email]; ok {
		return 0, "", services.ErrEmailAlreadyTaken
	}
	if len(password) < 8 {
		return 0, "", services.ErrPasswordTooShort
	}
	hasDigit := false
	for _, c := range password {
		if c >= '0' && c <= '9' {
			hasDigit = true
			break
		}
	}
	if !hasDigit {
		return 0, "", services.ErrPasswordNoDigit
	}
	id := f.nextID
	f.nextID++
	f.users[email] = fakeUser{
		email:        email,
		name:         name,
		passwordHash: password,
		verified:     false,
		userID:       id,
		tokens:       make(map[string]string),
	}
	return id, "fake-jwt-" + email, nil
}

func (f *fakeAuthEmailStore) Login(email, password string) (int64, string, error) {
	u, ok := f.users[email]
	if !ok {
		return 0, "", services.ErrInvalidPassword
	}
	if u.passwordHash != password {
		return 0, "", services.ErrInvalidPassword
	}
	if !u.verified {
		return 0, "", services.ErrEmailNotVerified
	}
	return u.userID, "fake-jwt-" + email, nil
}

func (f *fakeAuthEmailStore) IssueVerificationToken(userID int64, email string) (string, error) {
	tok := "verify-tok-" + email
	if u, ok := f.users[email]; ok {
		u.tokens[tok] = "verify"
		f.users[email] = u
	}
	return tok, nil
}

func (f *fakeAuthEmailStore) VerifyEmail(token string) (int64, error) {
	for _, u := range f.users {
		if _, ok := u.tokens[token]; ok {
			u.verified = true
			f.users[u.email] = u
			return u.userID, nil
		}
	}
	return 0, services.ErrInvalidPassword
}

func (f *fakeAuthEmailStore) IssueResetToken(email string) (string, error) {
	if _, ok := f.users[email]; !ok {
		return "", services.ErrInvalidPassword
	}
	tok := "reset-tok-" + email
	u := f.users[email]
	u.tokens[tok] = "reset"
	f.users[email] = u
	return tok, nil
}

// MagicLinkSignupOrLookup is the SPRINT 1.2 magic-link path.
// Idempotent on email: creates a new fake user if absent and
// returns (userID, workspaceID=1, nil). Tests that need a
// specific id can seed f.users[email] ahead of time.
func (f *fakeAuthEmailStore) MagicLinkSignupOrLookup(email string) (int64, int64, error) {
	u, ok := f.users[email]
	if !ok {
		id := f.nextID
		f.nextID++
		f.users[email] = fakeUser{
			email:        email,
			name:         email,
			passwordHash: "",
			verified:     true, // magic-link authenticates the email
			userID:       id,
			tokens:       make(map[string]string),
		}
		return id, 1, nil
	}
	return u.userID, 1, nil
}

func (f *fakeAuthEmailStore) ResetPassword(token, newPassword string) error {
	for _, u := range f.users {
		if _, ok := u.tokens[token]; ok {
			if len(newPassword) < 8 {
				return services.ErrPasswordTooShort
			}
			u.passwordHash = newPassword
			f.users[u.email] = u
			return nil
		}
	}
	return services.ErrInvalidPassword
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
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status: want 400, got %d", w.Code)
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
	r.ServeHTTP(httptest.NewRecorder(), req)

	// Verify email.
	store.users["login@example.com"] = fakeUser{
		email: "login@example.com", name: "Login", passwordHash: "password1",
		verified: true, userID: 1, tokens: make(map[string]string),
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

func TestHandleLogin_EmailNotVerified(t *testing.T) {
	store := newFakeAuthEmailStore()
	r := newAuthEmailTestRouter(store)

	// Register (not verified).
	body := map[string]string{"email": "unverified@example.com", "password": "password1", "name": "Unv"}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(httptest.NewRecorder(), req)

	// Login should fail.
	loginBody := map[string]string{"email": "unverified@example.com", "password": "password1"}
	lb, _ := json.Marshal(loginBody)
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(lb))
	req2.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req2)

	if w.Code != http.StatusForbidden {
		t.Errorf("status: want 403, got %d", w.Code)
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
	r.ServeHTTP(httptest.NewRecorder(), req)

	store.users["pwd@example.com"] = fakeUser{
		email: "pwd@example.com", name: "Pwd", passwordHash: "correct1",
		verified: true, userID: 1, tokens: make(map[string]string),
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

func TestHandleForgotPassword_Always200(t *testing.T) {
	store := newFakeAuthEmailStore()
	r := newAuthEmailTestRouter(store)

	// Even for non-existent users, always return 200.
	body := map[string]string{"email": "nobody@example.com"}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/forgot-password", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status: want 200, got %d", w.Code)
	}
}

func TestHandleResetPassword_Flow(t *testing.T) {
	store := newFakeAuthEmailStore()
	r := newAuthEmailTestRouter(store)

	// Register.
	body := map[string]string{"email": "reset@example.com", "password": "password1", "name": "Reset"}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(httptest.NewRecorder(), req)

	// Forgot password → get reset token.
	fpBody := map[string]string{"email": "reset@example.com"}
	fb, _ := json.Marshal(fpBody)
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/auth/forgot-password", bytes.NewReader(fb))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)

	var resp map[string]interface{}
	json.NewDecoder(w2.Body).Decode(&resp)
	tok, _ := resp["reset_token"].(string)
	if tok == "" {
		t.Fatal("no reset token returned")
	}

	// Reset password.
	rpBody := map[string]string{"token": tok, "new_password": "newpasswd1"}
	rb, _ := json.Marshal(rpBody)
	req3 := httptest.NewRequest(http.MethodPost, "/api/v1/auth/reset-password", bytes.NewReader(rb))
	req3.Header.Set("Content-Type", "application/json")
	w3 := httptest.NewRecorder()
	r.ServeHTTP(w3, req3)
	if w3.Code != http.StatusOK {
		t.Errorf("reset-password: want 200, got %d (body: %s)", w3.Code, w3.Body.String())
	}
}

func TestHandleVerifyEmail(t *testing.T) {
	store := newFakeAuthEmailStore()
	r := newAuthEmailTestRouter(store)

	// Register.
	body := map[string]string{"email": "v@example.com", "password": "password1", "name": "V"}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(httptest.NewRecorder(), req)

	// Get verification token.
	tok := "verify-tok-v@example.com"
	store.users["v@example.com"] = fakeUser{
		email: "v@example.com", name: "V", passwordHash: "password1",
		verified: false, userID: 1, tokens: map[string]string{tok: "verify"},
	}

	// Verify.
	vBody := map[string]string{"token": tok}
	vb, _ := json.Marshal(vBody)
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/auth/verify", bytes.NewReader(vb))
	req2.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req2)
	if w.Code != http.StatusOK {
		t.Errorf("verify: want 200, got %d (body: %s)", w.Code, w.Body.String())
	}
}

// newAuthEmailTestRouter creates a minimal Router with only the auth email
// routes wired, using the given fake store.
func newAuthEmailTestRouter(store AuthEmailStore) *chi.Mux {
	r := &Router{authEmailSvc: store}
	r.mux = chi.NewRouter()
	r.registerAuthEmailRoutes()
	return r.mux
}
