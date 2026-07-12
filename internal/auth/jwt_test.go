package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

const testSecret = "test-jwt-secret-must-be-long-enough-for-hs256"

func TestIssueAndVerify(t *testing.T) {
	m := NewManager(testSecret, 24)
	tok, jti, exp, err := m.Issue(42)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if tok == "" || jti == "" || exp.IsZero() {
		t.Fatal("expected non-empty token, jti, expiry")
	}
	if got := time.Until(exp); got < 23*time.Hour || got > 25*time.Hour {
		t.Fatalf("ttl outside expected window: %s", got)
	}
	uid, err := m.Verify(tok)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if uid != 42 {
		t.Fatalf("uid: want 42 got %d", uid)
	}
}

func TestIssueRejectsInvalidID(t *testing.T) {
	m := NewManager(testSecret, 24)
	if _, _, _, err := m.Issue(0); err == nil {
		t.Fatal("expected error for zero user id")
	}
	if _, _, _, err := m.Issue(-1); err == nil {
		t.Fatal("expected error for negative user id")
	}
}

func TestVerifyEmptyToken(t *testing.T) {
	m := NewManager(testSecret, 24)
	if _, err := m.Verify(""); err == nil {
		t.Fatal("expected error for empty token")
	}
}

func TestVerifyWrongSecret(t *testing.T) {
	m1 := NewManager(testSecret, 24)
	tok, _, _, _ := m1.Issue(7)
	m2 := NewManager("a-different-secret-with-32-bytes-of-content", 24)
	if _, err := m2.Verify(tok); err == nil {
		t.Fatal("expected error when verifying with wrong secret")
	}
}

func TestVerifyInvalidSignature(t *testing.T) {
	m := NewManager(testSecret, 24)
	if _, err := m.Verify("not.a.real.jwt"); err == nil {
		t.Fatal("expected error for malformed token")
	}
}

func TestMiddleware_RejectsMissing(t *testing.T) {
	// Taglio 1.1: missing Authorization header → 401. No lenient fallback.
	m := NewManager(testSecret, 24)
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	h := m.Middleware(next)
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
	if called {
		t.Fatal("next handler should not have been called")
	}
}

func TestMiddleware_RejectsInvalidScheme(t *testing.T) {
	m := NewManager(testSecret, 24)
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Authorization", "Token xyz")
	w := httptest.NewRecorder()
	m.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
}

func TestMiddleware_RejectsBogusBearer(t *testing.T) {
	// A Bearer prefix with an unparseable token MUST be rejected with 401,
	// not silently allowed through (Taglio 1.1: no lenient mode).
	m := NewManager(testSecret, 24)
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Authorization", "Bearer this.is.bogus")
	w := httptest.NewRecorder()
	m.Middleware(next).ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
	if called {
		t.Fatal("next handler should not have been called")
	}
}

func TestMiddleware_AllowValidToken(t *testing.T) {
	m := NewManager(testSecret, 24)
	tok, _, _, _ := m.Issue(99)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uid, ok := UserIDFromContext(r.Context())
		if !ok || uid != 99 {
			t.Fatalf("context uid: want 99, got %v ok=%v", uid, ok)
		}
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	m.Middleware(next).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
}
