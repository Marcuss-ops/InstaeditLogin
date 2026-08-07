package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

func TestHandleDisconnectAccount_Happy_204(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	owner := ownedAccountFixture(1, "instagram")
	connID := int64(55)
	owner.OAuthConnectionID = &connID

	var revokeCalled bool
	var revokeAccountID int64
	var countConnection, countExclude int64
	var countCalled bool
	var updatedAccount *models.PlatformAccount
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return owner, nil
		},
		countActiveOnConnectionFn: func(ctx context.Context, oauthConnectionID, excludeAccountID int64) (int64, error) {
			countCalled = true
			countConnection = oauthConnectionID
			countExclude = excludeAccountID
			return 0, nil // last active channel on the grant → revoke is safe
		},
		updatePlatformAccountFn: func(a *models.PlatformAccount) error {
			updatedAccount = a
			return nil
		},
	}
	vault := &mockCredentialVault{
		revokeFn: func(ctx context.Context, platformAccountID int64) error {
			revokeCalled = true
			revokeAccountID = platformAccountID
			return nil
		},
	}
	r := newTestRouter(svc, store, "", WithCredentialVault(vault))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/21/disconnect", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("want 204 No Content, got %d: %s", w.Code, w.Body.String())
	}
	if !countCalled {
		t.Fatal("CountActiveAccountsOnConnection was NOT called — shared-grant check skipped")
	}
	if countConnection != 55 || countExclude != 21 {
		t.Errorf("CountActiveAccountsOnConnection: want (55, 21), got (%d, %d)", countConnection, countExclude)
	}
	if !revokeCalled {
		t.Fatal("vault.Revoke was NOT called — local token cleanup skipped")
	}
	if revokeAccountID != 21 {
		t.Errorf("vault.Revoke called with accountID=%d, want 21", revokeAccountID)
	}
	if updatedAccount == nil {
		t.Fatal("UpdatePlatformAccount was NOT called — soft-disconnect not stamped")
	}
	if updatedAccount.Status != models.AccountStatusDisconnected {
		t.Errorf("status: want disconnected, got %s", updatedAccount.Status)
	}
	if updatedAccount.LastErrorCode != "DISCONNECTED" {
		t.Errorf("last_error_code: want DISCONNECTED, got %s", updatedAccount.LastErrorCode)
	}
	if updatedAccount.ConnectedAt != nil {
		t.Errorf("connected_at: want nil after disconnect, got %v", updatedAccount.ConnectedAt)
	}
}

// TestHandleDisconnectAccount_VaultRevokeError_500 covers the failure path:
// vault.Revoke errors ⇒ 500, account row NOT updated, cross-handler
// state machine stays consistent.
func TestHandleDisconnectAccount_VaultRevokeError_500(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	owner := ownedAccountFixture(1, "instagram")
	connID := int64(55)
	owner.OAuthConnectionID = &connID
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return owner, nil
		},
		// Last active channel on the grant → vault.Revoke runs and fails.
		countActiveOnConnectionFn: func(ctx context.Context, oauthConnectionID, excludeAccountID int64) (int64, error) {
			return 0, nil
		},
		updatePlatformAccountFn: func(a *models.PlatformAccount) error {
			t.Errorf("UpdatePlatformAccount MUST NOT be called when vault.Revoke fails (transaction consistency); got status=%s", a.Status)
			return nil
		},
	}
	vault := &mockCredentialVault{
		revokeFn: func(ctx context.Context, platformAccountID int64) error {
			return fmt.Errorf("simulated vault DB error")
		},
	}
	r := newTestRouter(svc, store, "", WithCredentialVault(vault))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/21/disconnect", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("vault.Revoke error: want 500, got %d: %s", w.Code, w.Body.String())
	}
}

// TestHandleDisconnectAccount_CrossTenant_404 is the workspace-isolation
// canary: vault.Revoke MUST NOT be called and UpdatePlatformAccount
// MUST NOT be called for a cross-tenant probe. Existence-leak
// prevention: 404 (not 403).
func TestHandleDisconnectAccount_CrossTenant_404(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	crossTenant := ownedAccountFixture(999, "instagram")
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return crossTenant, nil
		},
		updatePlatformAccountFn: func(a *models.PlatformAccount) error {
			t.Errorf("UpdatePlatformAccount MUST NOT be called for cross-tenant delete; got status=%s", a.Status)
			return nil
		},
	}
	vault := &mockCredentialVault{
		revokeFn: func(ctx context.Context, platformAccountID int64) error {
			t.Errorf("vault.Revoke MUST NOT be called for cross-tenant delete (data leak risk); got accountID=%d", platformAccountID)
			return nil
		},
	}
	r := newTestRouter(svc, store, "", WithCredentialVault(vault))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/21/disconnect", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant delete: want 404, got %d: %s", w.Code, w.Body.String())
	}
}

// TestHandleDisconnectAccount_NoSession_401: r.protected rejects the
// session-less probe BEFORE any DB or vault work happens. The
// handler's own nil-identity 401 in loadOwnAccountByID is
// defence-in-depth.
func TestHandleDisconnectAccount_NoSession_401(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			t.Errorf("FindPlatformAccountByID MUST NOT be called without a session; got id=%d", id)
			return nil, nil
		},
		updatePlatformAccountFn: func(a *models.PlatformAccount) error {
			t.Errorf("UpdatePlatformAccount MUST NOT be called without a session")
			return nil
		},
	}
	vault := &mockCredentialVault{
		revokeFn: func(ctx context.Context, platformAccountID int64) error {
			t.Errorf("vault.Revoke MUST NOT be called without a session (token leak risk); got accountID=%d", platformAccountID)
			return nil
		},
	}
	r := newTestRouter(svc, store, "", WithCredentialVault(vault))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/21/disconnect", nil)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req) // NO JWT — session-less probe

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("no-session /accounts/21 disconnect: want 401, got %d: %s", w.Code, w.Body.String())
	}
}

// TestHandleDeleteAccount_Deprecated_410 pins the account-lifecycle audit
// fix: the old DELETE /api/v1/accounts/{id} route no longer performs a
// misleading soft-delete. It answers 410 Gone with guidance towards the
// explicit commands (POST /disconnect, DELETE /data) — a deliberate API
// contract break so no silent soft-deletion can happen on a "DELETE".
func TestHandleDeleteAccount_Deprecated_410(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	store := &mockUserStore{}
	r := newTestRouter(svc, store, "")

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/accounts/21", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusGone {
		t.Fatalf("deprecated DELETE /accounts/{id}: want 410 Gone, got %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "disconnect") || !strings.Contains(body, "/data") {
		t.Errorf("410 body must guide towards the explicit commands; got: %s", body)
	}
}

// TestHandleDisconnectAccount_YouTube_RemoteRevoke_BeforeLocalCleanup pins
// the P2 wiring: for a YouTube account with a YouTubeRevoker wired, the
// decoded refresh token is revoked on Google's endpoint BEFORE the local
// vault.Revoke deletes the token material.
func TestHandleDisconnectAccount_YouTube_RemoteRevoke_BeforeLocalCleanup(t *testing.T) {
	svc := &mockProvider{platform: "youtube"}
	owner := ownedAccountFixture(1, "youtube")
	connID := int64(55)
	owner.OAuthConnectionID = &connID

	var remoteRevokeCalls int
	var revokedToken string
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return owner, nil
		},
		// Last active channel on the grant → remote + local revoke run.
		countActiveOnConnectionFn: func(ctx context.Context, oauthConnectionID, excludeAccountID int64) (int64, error) {
			return 0, nil
		},
		updatePlatformAccountFn: func(a *models.PlatformAccount) error {
			return nil
		},
	}
	vault := &mockCredentialVault{
		revokeFn: func(ctx context.Context, platformAccountID int64) error {
			return nil
		},
		getRefreshTokenFn: func(ctx context.Context, platformAccountID int64) (string, error) {
			return "yt-decoded-refresh-token", nil
		},
	}
	r := newTestRouter(svc, store, "", WithCredentialVault(vault))
	r.youtubeRevoker = &fakeYouTubeRevoker{
		revokeFn: func(ctx context.Context, token string) error {
			remoteRevokeCalls++
			revokedToken = token
			return nil
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/21/disconnect", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("want 204 No Content, got %d: %s", w.Code, w.Body.String())
	}
	if remoteRevokeCalls != 1 {
		t.Fatalf("remote YouTube revoke: want 1 call, got %d", remoteRevokeCalls)
	}
	if revokedToken != "yt-decoded-refresh-token" {
		t.Errorf("remote revoke token: want yt-decoded-refresh-token, got %q", revokedToken)
	}
}

// TestHandleDisconnectAccount_YouTube_RemoteRevokeFailure_Still204 proves the
// remote revoke is best-effort: a provider-side failure must NOT block the
// local disconnect (vault.Revoke still runs and the account row flips).
func TestHandleDisconnectAccount_YouTube_RemoteRevokeFailure_Still204(t *testing.T) {
	svc := &mockProvider{platform: "youtube"}
	owner := ownedAccountFixture(1, "youtube")
	connID := int64(55)
	owner.OAuthConnectionID = &connID

	var localRevokeCalled bool
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return owner, nil
		},
		// Last active channel on the grant → remote revoke attempted.
		countActiveOnConnectionFn: func(ctx context.Context, oauthConnectionID, excludeAccountID int64) (int64, error) {
			return 0, nil
		},
		updatePlatformAccountFn: func(a *models.PlatformAccount) error {
			return nil
		},
	}
	vault := &mockCredentialVault{
		revokeFn: func(ctx context.Context, platformAccountID int64) error {
			localRevokeCalled = true
			return nil
		},
		getRefreshTokenFn: func(ctx context.Context, platformAccountID int64) (string, error) {
			return "yt-decoded-refresh-token", nil
		},
	}
	r := newTestRouter(svc, store, "", WithCredentialVault(vault))
	r.youtubeRevoker = &fakeYouTubeRevoker{
		revokeFn: func(ctx context.Context, token string) error {
			return fmt.Errorf("google revoke 400")
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/21/disconnect", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("remote-revoke failure must still yield 204, got %d: %s", w.Code, w.Body.String())
	}
	if !localRevokeCalled {
		t.Error("local vault.Revoke must run even when the remote revoke fails")
	}
}

// TestHandleDisconnectAccount_SharedGrant_SiblingActive_KeepsGrant pins the
// P0 shared-grant fix: disconnecting ONE channel of a grant still used by
// an active sibling MUST NOT revoke the grant — neither the remote
// provider revoke (which would kill the sibling's refresh token at Google)
// nor the local vault.Revoke (which would delete the sibling's token rows).
// The account still soft-disconnects (status='disconnected') so the row
// drops out of every publishable surface.
func TestHandleDisconnectAccount_SharedGrant_SiblingActive_KeepsGrant(t *testing.T) {
	svc := &mockProvider{platform: "youtube"}
	owner := ownedAccountFixture(1, "youtube")
	connID := int64(55)
	owner.OAuthConnectionID = &connID

	var revokeCalled, remoteRevokeCalled bool
	var countConnection, countExclude int64
	var updatedAccount *models.PlatformAccount
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return owner, nil
		},
		countActiveOnConnectionFn: func(ctx context.Context, oauthConnectionID, excludeAccountID int64) (int64, error) {
			countConnection = oauthConnectionID
			countExclude = excludeAccountID
			return 1, nil // one active sibling (e.g. WWE France) still uses the grant
		},
		updatePlatformAccountFn: func(a *models.PlatformAccount) error {
			updatedAccount = a
			return nil
		},
	}
	vault := &mockCredentialVault{
		revokeFn: func(ctx context.Context, platformAccountID int64) error {
			revokeCalled = true
			return nil
		},
		getRefreshTokenFn: func(ctx context.Context, platformAccountID int64) (string, error) {
			return "yt-decoded-refresh-token", nil
		},
	}
	r := newTestRouter(svc, store, "", WithCredentialVault(vault))
	r.youtubeRevoker = &fakeYouTubeRevoker{
		revokeFn: func(ctx context.Context, token string) error {
			remoteRevokeCalled = true
			return nil
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/21/disconnect", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("want 204 No Content, got %d: %s", w.Code, w.Body.String())
	}
	if countConnection != 55 || countExclude != 21 {
		t.Errorf("CountActiveAccountsOnConnection: want (55, 21), got (%d, %d)", countConnection, countExclude)
	}
	if revokeCalled {
		t.Error("vault.Revoke MUST NOT be called when an active sibling still uses the grant")
	}
	if remoteRevokeCalled {
		t.Error("remote YouTube revoke MUST NOT be called when an active sibling still uses the grant (would kill the sibling's refresh token)")
	}
	if updatedAccount == nil || updatedAccount.Status != models.AccountStatusDisconnected {
		t.Errorf("account must still soft-disconnect: got %+v", updatedAccount)
	}
}

// TestHandleDisconnectAccount_CountError_FailClosed_500 pins the fail-closed
// branch: if the shared-grant inspection itself errors, the handler must
// refuse the disconnect (500) and MUST NOT touch the vault or the account
// row — deleting (or keeping) grant tokens on a guess is never safe.
func TestHandleDisconnectAccount_CountError_FailClosed_500(t *testing.T) {
	svc := &mockProvider{platform: "youtube"}
	owner := ownedAccountFixture(1, "youtube")
	connID := int64(55)
	owner.OAuthConnectionID = &connID

	var revokeCalled, updateCalled bool
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return owner, nil
		},
		countActiveOnConnectionFn: func(ctx context.Context, oauthConnectionID, excludeAccountID int64) (int64, error) {
			return 0, fmt.Errorf("db unreachable")
		},
		updatePlatformAccountFn: func(a *models.PlatformAccount) error {
			updateCalled = true
			return nil
		},
	}
	vault := &mockCredentialVault{
		revokeFn: func(ctx context.Context, platformAccountID int64) error {
			revokeCalled = true
			return nil
		},
	}
	r := newTestRouter(svc, store, "", WithCredentialVault(vault))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/21/disconnect", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("count error: want 500 (fail-closed), got %d: %s", w.Code, w.Body.String())
	}
	if revokeCalled {
		t.Error("vault.Revoke MUST NOT be called when the shared-grant check fails")
	}
	if updateCalled {
		t.Error("UpdatePlatformAccount MUST NOT be called when the shared-grant check fails")
	}
}

// TestHandleDisconnectAccount_NoConnection_SkipsGrantWork_204 pins the
// pre-043 / already-revoked legacy path: an account without an
// oauth_connection has no grant to revoke, so the disconnect must
// complete without touching the vault or the sibling-count query
// (previously vault.Revoke 500'd on its no-connection error).
func TestHandleDisconnectAccount_NoConnection_SkipsGrantWork_204(t *testing.T) {
	svc := &mockProvider{platform: "youtube"}
	owner := ownedAccountFixture(1, "youtube") // OAuthConnectionID nil

	var revokeCalled, countCalled bool
	var updatedAccount *models.PlatformAccount
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return owner, nil
		},
		countActiveOnConnectionFn: func(ctx context.Context, oauthConnectionID, excludeAccountID int64) (int64, error) {
			countCalled = true
			return 0, nil
		},
		updatePlatformAccountFn: func(a *models.PlatformAccount) error {
			updatedAccount = a
			return nil
		},
	}
	vault := &mockCredentialVault{
		revokeFn: func(ctx context.Context, platformAccountID int64) error {
			revokeCalled = true
			return nil
		},
	}
	r := newTestRouter(svc, store, "", WithCredentialVault(vault))
	r.youtubeRevoker = &fakeYouTubeRevoker{}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/21/disconnect", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("want 204 No Content, got %d: %s", w.Code, w.Body.String())
	}
	if countCalled {
		t.Error("CountActiveAccountsOnConnection MUST NOT be called for an account without oauth_connection_id")
	}
	if revokeCalled {
		t.Error("vault.Revoke MUST NOT be called for an account without oauth_connection_id (nothing to revoke)")
	}
	if updatedAccount == nil || updatedAccount.Status != models.AccountStatusDisconnected {
		t.Errorf("account must still soft-disconnect: got %+v", updatedAccount)
	}
}

// TestHandleGetAccount_WithSnapshot_ResourceIncluded proves that when a
// snapshot exists, the GET /accounts/{id} response includes a "resource"
// field with the cached details.
