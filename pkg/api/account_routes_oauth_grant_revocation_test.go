package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

func TestHandleDeleteOAuthGrant_RejectsNonYouTubeBeforeRevocation(t *testing.T) {
	grantID := int64(55)
	account := ownedAccountFixture(1, models.PlatformInstagram)
	account.OAuthConnectionID = &grantID
	remoteCalled := false
	localCalled := false
	store := &mockUserStore{
		findPlatformAccountFn: func(int64) (*models.PlatformAccount, error) { return account, nil },
		disconnectOAuthGrantFn: func(context.Context, int64) error {
			localCalled = true
			return nil
		},
	}
	vault := &mockCredentialVault{getRefreshTokenFn: func(context.Context, int64) (string, error) {
		return "provider-token", nil
	}}
	r := newTestRouter(&mockProvider{platform: models.PlatformInstagram}, store, "", WithCredentialVault(vault))
	r.oauthGrantRevoker = &fakeOAuthGrantRevoker{revokeFn: func(context.Context, string) error {
		remoteCalled = true
		return nil
	}}

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/accounts/21/oauth-grant", nil)
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNotImplemented {
		t.Fatalf("status: got %d, want 501", w.Code)
	}
	if remoteCalled || localCalled {
		t.Fatalf("non-YouTube grant must not revoke or disconnect: remote=%v local=%v", remoteCalled, localCalled)
	}
}

func TestHandleDeleteOAuthGrant_RevokesRemotelyBeforeLocalTransaction(t *testing.T) {
	grantID := int64(55)
	account := ownedAccountFixture(1, models.PlatformYouTube)
	account.OAuthConnectionID = &grantID
	const token = "decoded-refresh-token-never-log-this"
	var events []string
	store := &mockUserStore{
		findPlatformAccountFn: func(int64) (*models.PlatformAccount, error) { return account, nil },
		disconnectOAuthGrantFn: func(context.Context, int64) error {
			events = append(events, "local")
			return nil
		},
	}
	vault := &mockCredentialVault{getRefreshTokenFn: func(context.Context, int64) (string, error) {
		return token, nil
	}}
	r := newTestRouter(&mockProvider{platform: models.PlatformYouTube}, store, "", WithCredentialVault(vault))
	r.oauthGrantRevoker = &fakeOAuthGrantRevoker{revokeFn: func(_ context.Context, got string) error {
		if got != token {
			t.Fatalf("remote token: got %q, want %q", got, token)
		}
		events = append(events, "remote")
		return nil
	}}

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/accounts/21/oauth-grant", nil)
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status: got %d, want 204: %s", w.Code, w.Body.String())
	}
	if got := strings.Join(events, ","); got != "remote,local" {
		t.Fatalf("operation order: got %q, want remote,local", got)
	}
}

func TestHandleDeleteOAuthGrant_TransientRemoteFailureBlocksLocalCleanup(t *testing.T) {
	grantID := int64(55)
	account := ownedAccountFixture(1, models.PlatformYouTube)
	account.OAuthConnectionID = &grantID
	localCalled := false
	store := &mockUserStore{
		findPlatformAccountFn: func(int64) (*models.PlatformAccount, error) { return account, nil },
		disconnectOAuthGrantFn: func(context.Context, int64) error {
			localCalled = true
			return nil
		},
	}
	vault := &mockCredentialVault{getRefreshTokenFn: func(context.Context, int64) (string, error) {
		return "transient-refresh-token", nil
	}}
	r := newTestRouter(&mockProvider{platform: models.PlatformYouTube}, store, "", WithCredentialVault(vault))
	r.oauthGrantRevoker = &fakeOAuthGrantRevoker{revokeFn: func(context.Context, string) error {
		return &services.OAuthGrantRevocationError{StatusCode: http.StatusServiceUnavailable, Class: services.OAuthGrantRevocationTransient, RetryAfter: 9 * time.Second}
	}}

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/accounts/21/oauth-grant", nil)
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status: got %d, want 503: %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Retry-After"); got != "9" {
		t.Fatalf("Retry-After: got %q, want 9", got)
	}
	if localCalled {
		t.Fatal("local transaction must not run after a transient remote failure")
	}
	if strings.Contains(w.Body.String(), "transient-refresh-token") {
		t.Fatal("response exposed refresh token")
	}
}

func TestHandleDeleteOAuthGrant_RemoteSuccessLocalFailureDoesNotExposeToken(t *testing.T) {
	grantID := int64(55)
	account := ownedAccountFixture(1, models.PlatformYouTube)
	account.OAuthConnectionID = &grantID
	const token = "remote-success-secret-token"
	store := &mockUserStore{
		findPlatformAccountFn: func(int64) (*models.PlatformAccount, error) { return account, nil },
		disconnectOAuthGrantFn: func(context.Context, int64) error {
			return fmt.Errorf("local transaction unavailable")
		},
	}
	vault := &mockCredentialVault{getRefreshTokenFn: func(context.Context, int64) (string, error) {
		return token, nil
	}}
	r := newTestRouter(&mockProvider{platform: models.PlatformYouTube}, store, "", WithCredentialVault(vault))
	r.oauthGrantRevoker = &fakeOAuthGrantRevoker{revokeFn: func(context.Context, string) error { return nil }}

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/accounts/21/oauth-grant", nil)
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status: got %d, want 500", w.Code)
	}
	if strings.Contains(w.Body.String(), token) {
		t.Fatal("response exposed refresh token after local failure")
	}
}

func TestHandleDeleteOAuthGrant_AlreadyRevokedCompletesLocalCleanup(t *testing.T) {
	grantID := int64(55)
	account := ownedAccountFixture(1, models.PlatformYouTube)
	account.OAuthConnectionID = &grantID
	localCalled := false
	store := &mockUserStore{
		findPlatformAccountFn: func(int64) (*models.PlatformAccount, error) { return account, nil },
		disconnectOAuthGrantFn: func(context.Context, int64) error {
			localCalled = true
			return nil
		},
	}
	vault := &mockCredentialVault{getRefreshTokenFn: func(context.Context, int64) (string, error) {
		return "already-revoked-token", nil
	}}
	r := newTestRouter(&mockProvider{platform: models.PlatformYouTube}, store, "", WithCredentialVault(vault))
	r.oauthGrantRevoker = &fakeOAuthGrantRevoker{revokeFn: func(context.Context, string) error {
		return services.OAuthGrantRevocationAlreadyCompleted
	}}

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/accounts/21/oauth-grant", nil)
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNoContent || !localCalled {
		t.Fatalf("already-revoked grant: status=%d localCalled=%v", w.Code, localCalled)
	}
}

func TestHandleDeleteOAuthGrant_InvalidRequestDoesNotDeleteLocally(t *testing.T) {
	grantID := int64(55)
	account := ownedAccountFixture(1, models.PlatformYouTube)
	account.OAuthConnectionID = &grantID
	localCalled := false
	store := &mockUserStore{
		findPlatformAccountFn: func(int64) (*models.PlatformAccount, error) { return account, nil },
		disconnectOAuthGrantFn: func(context.Context, int64) error {
			localCalled = true
			return nil
		},
	}
	vault := &mockCredentialVault{getRefreshTokenFn: func(context.Context, int64) (string, error) {
		return "invalid-request-token", nil
	}}
	r := newTestRouter(&mockProvider{platform: models.PlatformYouTube}, store, "", WithCredentialVault(vault))
	r.oauthGrantRevoker = &fakeOAuthGrantRevoker{revokeFn: func(context.Context, string) error {
		return &services.OAuthGrantRevocationError{StatusCode: http.StatusBadRequest, Code: "invalid_request", Class: services.OAuthGrantRevocationPermanent}
	}}

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/accounts/21/oauth-grant", nil)
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway || localCalled {
		t.Fatalf("invalid_request: status=%d localCalled=%v", w.Code, localCalled)
	}
	if strings.Contains(w.Body.String(), "invalid-request-token") {
		t.Fatal("response exposed refresh token")
	}
}

func TestHandleDeleteOAuthGrant_PermanentRemoteFailureDoesNotExposeDetails(t *testing.T) {
	grantID := int64(55)
	account := ownedAccountFixture(1, models.PlatformYouTube)
	account.OAuthConnectionID = &grantID
	store := &mockUserStore{findPlatformAccountFn: func(int64) (*models.PlatformAccount, error) { return account, nil }}
	vault := &mockCredentialVault{getRefreshTokenFn: func(context.Context, int64) (string, error) {
		return "permanent-secret-token", nil
	}}
	r := newTestRouter(&mockProvider{platform: models.PlatformYouTube}, store, "", WithCredentialVault(vault))
	r.oauthGrantRevoker = &fakeOAuthGrantRevoker{revokeFn: func(context.Context, string) error {
		return fmt.Errorf("provider payload contains permanent-secret-token")
	}}

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/accounts/21/oauth-grant", nil)
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("status: got %d, want 502", w.Code)
	}
	if strings.Contains(w.Body.String(), "permanent-secret-token") {
		t.Fatal("response exposed revocation details or token")
	}
}
