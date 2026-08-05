package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// TestHandleDeleteAccountData_Happy_AtomicTombstone_204 pins the production
// path: the repository transaction runs (via the optional store), the remote
// YouTube revoke callback is provided and invoked, and the endpoint answers
// 204.
func TestHandleDeleteAccountData_Happy_AtomicTombstone_204(t *testing.T) {
	owner := ownedAccountFixture(1, models.PlatformYouTube)
	connID := int64(55)
	owner.OAuthConnectionID = &connID

	var storeAccountID int64
	var remoteRevokeCalls int
	var revokedToken string
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return owner, nil
		},
		permanentlyDeleteAccountFn: func(ctx context.Context, accountID int64, revoke func(context.Context, *sql.Tx) error) (bool, error) {
			storeAccountID = accountID
			if revoke == nil {
				t.Fatal("revoke callback must be provided for a YouTube account with an OAuth connection")
			}
			if err := revoke(ctx, nil); err != nil {
				return false, err
			}
			return true, nil
		},
	}
	vault := &mockCredentialVault{
		getRefreshTokenFn: func(ctx context.Context, id int64) (string, error) {
			return "yt-decoded-refresh-token", nil
		},
	}
	r := newTestRouter(&mockProvider{platform: models.PlatformYouTube}, store, "", WithCredentialVault(vault))
	r.youtubeRevoker = &fakeYouTubeRevoker{
		revokeFn: func(ctx context.Context, token string) error {
			remoteRevokeCalls++
			revokedToken = token
			return nil
		},
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/accounts/21/data", strings.NewReader(`{"confirmation":"alice_youtube"}`))
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d: %s", w.Code, w.Body.String())
	}
	if storeAccountID != 21 {
		t.Errorf("store called with accountID=%d, want 21", storeAccountID)
	}
	if remoteRevokeCalls != 1 {
		t.Errorf("remote YouTube revoke calls: want 1, got %d", remoteRevokeCalls)
	}
	if revokedToken != "yt-decoded-refresh-token" {
		t.Errorf("revoked token: want yt-decoded-refresh-token, got %q", revokedToken)
	}
}

// TestHandleDeleteAccountData_SharedGrant_SiblingActive_NoRevoke pins the
// shared-grant guarantee at the handler level: the endpoint still answers
// 204 when the store decides (inside its transaction) that an active sibling
// remains — the remote revoker must not be called.
func TestHandleDeleteAccountData_SharedGrant_SiblingActive_NoRevoke(t *testing.T) {
	owner := ownedAccountFixture(1, models.PlatformYouTube)
	connID := int64(55)
	owner.OAuthConnectionID = &connID
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return owner, nil
		},
		// The store is the source of truth for the sibling decision; it does
		// NOT invoke the callback (sibling still active on the grant).
		permanentlyDeleteAccountFn: func(ctx context.Context, accountID int64, revoke func(context.Context, *sql.Tx) error) (bool, error) {
			return true, nil
		},
	}
	vault := &mockCredentialVault{
		getRefreshTokenFn: func(ctx context.Context, id int64) (string, error) {
			return "yt-decoded-refresh-token", nil
		},
	}
	r := newTestRouter(&mockProvider{platform: models.PlatformYouTube}, store, "", WithCredentialVault(vault))
	r.youtubeRevoker = &fakeYouTubeRevoker{
		revokeFn: func(ctx context.Context, token string) error {
			t.Fatal("remote revoke MUST NOT be called while an active sibling uses the grant")
			return nil
		},
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/accounts/21/data", strings.NewReader(`{"confirmation":"alice_youtube"}`))
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d: %s", w.Code, w.Body.String())
	}
}

// TestHandleDeleteAccountData_NonYouTube_NoRevokeCallback pins that only
// YouTube accounts with an OAuth connection receive a revoke callback.
func TestHandleDeleteAccountData_NonYouTube_NoRevokeCallback(t *testing.T) {
	owner := ownedAccountFixture(1, "instagram")
	connID := int64(55)
	owner.OAuthConnectionID = &connID
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return owner, nil
		},
		permanentlyDeleteAccountFn: func(ctx context.Context, accountID int64, revoke func(context.Context, *sql.Tx) error) (bool, error) {
			if revoke != nil {
				t.Fatal("revoke callback must be nil for a non-YouTube account")
			}
			return true, nil
		},
	}
	r := newTestRouter(&mockProvider{platform: "instagram"}, store, "")

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/accounts/21/data", strings.NewReader(`{"confirmation":"alice_instagram"}`))
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d: %s", w.Code, w.Body.String())
	}
}

// TestHandleDeleteAccountData_Fallback_TombstonesViaUpdatePlatformAccount pins
// the test-only fallback: a store without the atomic capability still
// tombstones the account through UpdatePlatformAccount (status='deleted',
// username='[deleted]', metadata={}) so the row disappears from the list
// endpoint's default filter.
func TestHandleDeleteAccountData_Fallback_TombstonesViaUpdatePlatformAccount(t *testing.T) {
	owner := ownedAccountFixture(1, "instagram")
	var updatedAccount *models.PlatformAccount
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return owner, nil
		},
		updatePlatformAccountFn: func(a *models.PlatformAccount) error {
			updatedAccount = a
			return nil
		},
	}
	r := newTestRouter(&mockProvider{platform: "instagram"}, store, "")

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/accounts/21/data", strings.NewReader(`{"confirmation":"alice_instagram"}`))
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("want 204, got %d: %s", w.Code, w.Body.String())
	}
	if updatedAccount == nil {
		t.Fatal("UpdatePlatformAccount was NOT called by the fallback path")
	}
	if updatedAccount.Status != models.AccountStatusDeleted {
		t.Errorf("status: want %q, got %q", models.AccountStatusDeleted, updatedAccount.Status)
	}
	if updatedAccount.Username != "[deleted]" {
		t.Errorf("username: want [deleted], got %q", updatedAccount.Username)
	}
	if updatedAccount.Metadata == nil || len(updatedAccount.Metadata) != 0 {
		t.Errorf("metadata: want empty map, got %v", updatedAccount.Metadata)
	}
}

// TestHandleDeleteAccountData_AlreadyDeleted_IsIdempotent returns 204 without
// requiring the original channel name or invoking the mutation store again.
func TestHandleDeleteAccountData_AlreadyDeleted_IsIdempotent(t *testing.T) {
	owner := ownedAccountFixture(1, models.PlatformYouTube)
	owner.Status = models.AccountStatusDeleted
	owner.Username = "[deleted]"
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return owner, nil
		},
		permanentlyDeleteAccountFn: func(ctx context.Context, accountID int64, revoke func(context.Context, *sql.Tx) error) (bool, error) {
			t.Fatal("already-deleted account must not invoke the mutation store")
			return false, nil
		},
	}
	r := newTestRouter(&mockProvider{platform: models.PlatformYouTube}, store, "")
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/accounts/21/data", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("already-deleted delete: want 204, got %d: %s", w.Code, w.Body.String())
	}
}

// TestHandleDeleteAccountData_CrossTenant_404 is the workspace-isolation
// canary: a cross-tenant probe must 404 before any store mutation.
func TestHandleDeleteAccountData_CrossTenant_404(t *testing.T) {
	crossTenant := ownedAccountFixture(999, "instagram")
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return crossTenant, nil
		},
		permanentlyDeleteAccountFn: func(ctx context.Context, accountID int64, revoke func(context.Context, *sql.Tx) error) (bool, error) {
			t.Fatal("PermanentlyDeleteAccountTx MUST NOT be called for a cross-tenant probe")
			return false, nil
		},
	}
	r := newTestRouter(&mockProvider{platform: "instagram"}, store, "")

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/accounts/21/data", strings.NewReader(`{"confirmation":"alice_youtube"}`))
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant delete: want 404, got %d: %s", w.Code, w.Body.String())
	}
}

// TestHandleDeleteAccountData_NoSession_401: r.protected rejects the
// session-less probe before any DB work.
func TestHandleDeleteAccountData_NoSession_401(t *testing.T) {
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			t.Error("FindPlatformAccountByID MUST NOT be called without a session")
			return nil, nil
		},
	}
	r := newTestRouter(&mockProvider{platform: "instagram"}, store, "")

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/accounts/21/data", strings.NewReader(`{"confirmation":"alice_youtube"}`))
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req) // NO JWT

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("no-session delete: want 401, got %d: %s", w.Code, w.Body.String())
	}
}

// TestHandleDeleteAccountData_RemoteRevokePermanent_502 pins the typed error
// mapping: a permanent provider revocation failure surfaces 502 and the
// delete is rolled back (the store reports the error).
func TestHandleDeleteAccountData_RemoteRevokePermanent_502(t *testing.T) {
	owner := ownedAccountFixture(1, models.PlatformYouTube)
	connID := int64(55)
	owner.OAuthConnectionID = &connID
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return owner, nil
		},
		permanentlyDeleteAccountFn: func(ctx context.Context, accountID int64, revoke func(context.Context, *sql.Tx) error) (bool, error) {
			return false, &services.OAuthGrantRevocationError{
				Class: services.OAuthGrantRevocationPermanent,
				Cause: errors.New("google revoke rejected the token"),
			}
		},
	}
	r := newTestRouter(&mockProvider{platform: models.PlatformYouTube}, store, "")

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/accounts/21/data", strings.NewReader(`{"confirmation":"alice_youtube"}`))
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("permanent revocation failure: want 502, got %d: %s", w.Code, w.Body.String())
	}
}

// TestHandleDeleteAccountData_StoreError_500 pins the generic local-failure
// mapping for non-revocation repository errors.
func TestHandleDeleteAccountData_StoreError_500(t *testing.T) {
	owner := ownedAccountFixture(1, "instagram")
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return owner, nil
		},
		permanentlyDeleteAccountFn: func(ctx context.Context, accountID int64, revoke func(context.Context, *sql.Tx) error) (bool, error) {
			return false, errors.New("db unreachable")
		},
	}
	r := newTestRouter(&mockProvider{platform: "instagram"}, store, "")

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/accounts/21/data", strings.NewReader(`{"confirmation":"alice_instagram"}`))
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("store error: want 500, got %d: %s", w.Code, w.Body.String())
	}
}
