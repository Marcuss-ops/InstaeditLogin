package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// TestHandleListAccounts_Happy proves the closed endpoint contract:
// 200 + {"accounts":[{id,platform,platform_user_id,username,status,created_at}]}.
// NO user_id / workspace_id in the response (the wire shape is the
// spec'd one, not a mirror of models.PlatformAccount).
func TestHandleListAccounts_Happy(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	fixtures := twoAccountFixtures()
	store := &mockUserStore{
		listFn: func(userID int64, platform string) ([]*models.PlatformAccount, error) {
			// Mirrors the production contract: no platform filter when
			// the handler passes "".
			if platform != "" {
				t.Errorf("handler must request ALL platforms (pass empty filter), got platform=%q", platform)
			}
			// User must come from the JWT (uid=1), NOT from query.
			if userID != 1 {
				t.Errorf("handler must use JWT-derived userID; got userID=%d (cross-tenant leak risk)", userID)
			}
			return fixtures, nil
		},
	}
	r := newTestRouter(svc, store, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Accounts []struct {
			ID             int64     `json:"id"`
			Platform       string    `json:"platform"`
			PlatformUserID string    `json:"platform_user_id"`
			Username       string    `json:"username"`
			Status         string    `json:"status"`
			CreatedAt      time.Time `json:"created_at"`
			// The following are EXPLICITLY forbidden by the contract:
			UserID    int64  `json:"user_id,omitempty"`
			UpdatedAt string `json:"updated_at,omitempty"`
			LastError string `json:"last_error_code,omitempty"`
			Metadata  any    `json:"metadata,omitempty"`
		} `json:"accounts"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(resp.Accounts) != 2 {
		t.Fatalf("accounts length: want 2, got %d", len(resp.Accounts))
	}
	// First account (instagram).
	if resp.Accounts[0].ID != 21 {
		t.Errorf("accounts[0].id: want 21, got %d", resp.Accounts[0].ID)
	}
	if resp.Accounts[0].Platform != "instagram" {
		t.Errorf("accounts[0].platform: want instagram, got %s", resp.Accounts[0].Platform)
	}
	if resp.Accounts[0].PlatformUserID != "1784deadbeef" {
		t.Errorf("accounts[0].platform_user_id: want 1784deadbeef, got %s", resp.Accounts[0].PlatformUserID)
	}
	if resp.Accounts[0].Username != "alice_ig" {
		t.Errorf("accounts[0].username: want alice_ig, got %s", resp.Accounts[0].Username)
	}
	if resp.Accounts[0].Status != models.AccountStatusActive {
		t.Errorf("accounts[0].status: want active, got %s", resp.Accounts[0].Status)
	}
	if resp.Accounts[0].CreatedAt.IsZero() {
		t.Errorf("accounts[0].created_at: want non-zero, got zero value")
	}
	// Forbidden fields must NOT appear in any account item.
	for i, a := range resp.Accounts {
		if a.UserID != 0 {
			t.Errorf("accounts[%d].user_id leaked: %d (the SPA must NEVER see internal user id)", i, a.UserID)
		}
		if a.UpdatedAt != "" {
			t.Errorf("accounts[%d].updated_at leaked: %q (not in spec'd response shape)", i, a.UpdatedAt)
		}
		if a.LastError != "" {
			t.Errorf("accounts[%d].last_error_code leaked: %q (not in spec'd response shape)", i, a.LastError)
		}
		if a.Metadata != nil {
			t.Errorf("accounts[%d].metadata leaked: %v (internal PlatformAccount metadata)", i, a.Metadata)
		}
	}
}

// TestHandleListAccounts_EmptyList_ReturnsAccountsArrayKey proves the
// wrapper key is always present even when there are zero connections.
// SPA JSON decoders rely on `accounts` being an array, never null —
// returning {"accounts": null} would crash `accounts.map(...)` in the
// /connections page.
func TestHandleListAccounts_SafeReauthCodeOnly(t *testing.T) {
	fixtures := []*models.PlatformAccount{
		{ID: 21, UserID: 1, Platform: "youtube", PlatformUserID: "UC-shared", Username: "shared", Status: models.AccountStatusReauthRequired, LastErrorCode: "SHARED_GRANT_REAUTH_REQUIRED", LastErrorMessage: "provider SQL token=secret must never leak"},
		{ID: 22, UserID: 1, Platform: "youtube", PlatformUserID: "UC-unknown", Username: "unknown", Status: models.AccountStatusReauthRequired, LastErrorCode: "provider_secret_code", LastErrorMessage: "provider body with secret"},
	}
	store := &mockUserStore{listFn: func(int64, string) ([]*models.PlatformAccount, error) { return fixtures, nil }}
	r := newTestRouter(&mockProvider{platform: "youtube"}, store, "")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "SHARED_GRANT_REAUTH_REQUIRED") {
		t.Fatalf("safe shared-grant code missing from response: %s", body)
	}
	if strings.Contains(body, "provider_secret_code") || strings.Contains(body, "provider body with secret") || strings.Contains(body, "last_error_message") {
		t.Fatalf("provider/SQL details leaked in response: %s", body)
	}
}

func TestHandleListAccounts_EmptyList_ReturnsAccountsArrayKey(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	store := &mockUserStore{
		listFn: func(userID int64, platform string) ([]*models.PlatformAccount, error) {
			return []*models.PlatformAccount{}, nil
		},
	}
	r := newTestRouter(svc, store, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200 (empty list, NOT 404), got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]json.RawMessage
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	raw, ok := resp["accounts"]
	if !ok {
		t.Fatal("response MUST contain the 'accounts' key even when empty (SPA relies on it being an array)")
	}
	// RawMessage of "null" means the handler returned accounts: nil
	// instead of accounts: [] — decode and assert []interface{}.
	var arr []interface{}
	if err := json.Unmarshal(raw, &arr); err != nil {
		t.Fatalf("'accounts' must always be a JSON array (got %s): %v", string(raw), err)
	}
	if len(arr) != 0 {
		t.Fatalf("'accounts' should be empty array, got %d items", len(arr))
	}
}

// TestHandleListAccounts_NoSession_401 proves the r.protected chain
// rejects unauthenticated requests before reaching the handler. The
// handler itself has its own defence-in-depth check (writeError 401
// if identity is nil) so the test never reaches it — but we lock the
// behaviour at the route level here so a future refactor that swaps
// r.protected for something else (e.g. a custom middleware) won't
// silently bypass the auth requirement.
func TestHandleListAccounts_NoSession_401(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	store := &mockUserStore{
		listFn: func(userID int64, platform string) ([]*models.PlatformAccount, error) {
			t.Errorf("ListPlatformAccountsByUser MUST NOT be called without a session (data leak risk); got userID=%d", userID)
			return nil, nil
		},
	}
	r := newTestRouter(svc, store, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts", nil)
	w := httptest.NewRecorder()
	// NO withBearerJWT — session-less probe.
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("no-session /api/v1/accounts: want 401, got %d: %s", w.Code, w.Body.String())
	}
}

// TestHandleListAccounts_IgnoresQueryUserIDAndWorkspace is the
// security-binding test for this endpoint. An attacker MUST NOT be
// able to read another user's accounts by appending ?user_id=999 to
// the URL. The handler must derive user_id from auth context only
// and silently ignore (or strip) any user_id/workspace_id query
// params. The listFn captures the user_id call to assert the JWT
// user wins over the query.
func TestHandleListAccounts_IgnoresQueryUserIDAndWorkspace(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	var listFnUserID int64
	var listFnCalled bool
	store := &mockUserStore{
		listFn: func(userID int64, platform string) ([]*models.PlatformAccount, error) {
			listFnUserID = userID
			listFnCalled = true
			return []*models.PlatformAccount{}, nil
		},
	}
	r := newTestRouter(svc, store, "")

	// Attacker tries ?user_id=999&workspace_id=42 while presenting a
	// legitimate JWT for user 1.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts?user_id=999&workspace_id=42", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200 (auth from JWT, query ignored), got %d: %s", w.Code, w.Body.String())
	}
	if !listFnCalled {
		t.Fatal("ListPlatformAccountsByUser must be called even when query params are present (the cancel-out is identity-based, not query-based)")
	}
	if listFnUserID != 1 {
		t.Errorf("SQL filter used userID=%d, want 1 (JWT-derived). Query ?user_id=999 MUST NOT leak across tenants.", listFnUserID)
	}
}

// TestHandleGetAccount_Happy proves the closed endpoint contract: 200 +
// the 6-field wire shape, no internal PlatformAccount columns leaking.
func TestHandleGetAccount_Happy(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	owner := ownedAccountFixture(1, "instagram")
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			if id != 21 {
				t.Errorf("handler called FindPlatformAccountByID with id=%d, want 21 (path param)", id)
			}
			return owner, nil
		},
	}
	r := newTestRouter(svc, store, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/21", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		ID             int64     `json:"id"`
		Platform       string    `json:"platform"`
		PlatformUserID string    `json:"platform_user_id"`
		Username       string    `json:"username"`
		Status         string    `json:"status"`
		CreatedAt      time.Time `json:"created_at"`
		UserID         int64     `json:"user_id,omitempty"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if resp.ID != 21 || resp.Platform != "instagram" || resp.Username != "alice_instagram" {
		t.Errorf("response shape mismatch: %+v", resp)
	}
	if resp.UserID != 0 {
		t.Errorf("internal user_id leaked: %d", resp.UserID)
	}
}

// TestHandleGetAccount_NotFound_404 covers both the genuine-not-found
// and the cross-tenant cases under one roof (the loadOwnAccountByID
// helper collapses them by design — 404 prevents existence leaks).
func TestHandleGetAccount_NotFound_404(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return nil, nil // genuine not-found
		},
	}
	r := newTestRouter(svc, store, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/999", nil)
	w := httptest.NewRecorder()
	// JWT for user 1, but no row exists for id=999.
	jwt := issueTestJWT(t, 1)
	req.Header.Set("Authorization", "Bearer "+jwt)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404 (account not found), got %d: %s", w.Code, w.Body.String())
	}
}

// TestHandleGetAccount_CrossTenant_404 is the workspace-isolation
// canary: an account owned by user 999 MUST NOT be returned when the
// caller is user 1. The 404 (not 403) is critical — 403 would confirm
// to a probe that the id exists but is cross-tenant, leaking the
// existence of accounts in other user boundaries.
func TestHandleGetAccount_CrossTenant_404(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	crossTenant := ownedAccountFixture(999, "instagram")
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return crossTenant, nil // exists, but owned by user 999
		},
	}
	r := newTestRouter(svc, store, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/21", nil)
	w := httptest.NewRecorder()
	// Caller is user 1.
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant probe MUST return 404 (not 403), got %d: %s", w.Code, w.Body.String())
	}
	// Defence-in-depth: response body must NOT echo the cross-tenant
	// owner's id. Plain "account not found" string is the only safe form.
	if strings.Contains(w.Body.String(), "999") {
		t.Errorf("response leaks owned_by user id in body: %s", w.Body.String())
	}
}

// TestHandleGetAccount_NoSession_401 proves r.protected rejects the
// request before the handler runs. The handler's own nil-identity 401
// is defence-in-depth (loadOwnAccountByID returns 401 on nil identity)
// but the route-level middleware is the primary gate.
func TestHandleGetAccount_NoSession_401(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			t.Errorf("FindPlatformAccountByUser MUST NOT be called without a session (data leak risk); got id=%d", id)
			return nil, nil
		},
	}
	r := newTestRouter(svc, store, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/21", nil)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req) // NO JWT — session-less probe

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("no-session /accounts/21: want 401, got %d: %s", w.Code, w.Body.String())
	}
}

// TestHandleDisconnectAccount_Happy_204 verifies: 204 No Content + the
// shared-grant check ran (count == 0 → last channel on the grant) +
// vault.Revoke was called + account row was updated to
// status='disconnected' + auditLogStore fired (when present).
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
func TestHandleGetAccount_WithSnapshot_ResourceIncluded(t *testing.T) {
	svc := &mockProvider{platform: "youtube"}
	owner := ownedAccountFixture(1, "youtube")
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return owner, nil
		},
	}
	snap := &repository.AccountResourceSnapshot{
		PlatformAccountID: 21,
		ResourceType:      "channel",
		Profile: map[string]any{
			"external_id":  "UCtest123",
			"display_name": "Test Channel",
			"handle":       "@test",
			"avatar_url":   "https://example.com/avatar.jpg",
		},
		Statistics: map[string]any{
			"subscribers": map[string]any{
				"label":         "Subscribers",
				"value":         float64(125000),
				"display_value": "125.0K",
			},
		},
		FetchedAt: time.Now(),
	}
	snapStore := &mockSnapshotStore{
		getFn: func(id int64) (*repository.AccountResourceSnapshot, error) {
			return snap, nil
		},
	}
	r := newTestRouter(svc, store, "", WithSnapshotStore(snapStore))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/21", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		ID       int64  `json:"id"`
		Platform string `json:"platform"`
		Resource *struct {
			ResourceType string `json:"resource_type"`
			DisplayName  string `json:"display_name"`
		} `json:"resource"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Resource == nil {
		t.Fatal("resource field should be present when snapshot exists")
	}
	if resp.Resource.ResourceType != "channel" {
		t.Errorf("resource.resource_type: want channel, got %q", resp.Resource.ResourceType)
	}
	if resp.Resource.DisplayName != "Test Channel" {
		t.Errorf("resource.display_name: want Test Channel, got %q", resp.Resource.DisplayName)
	}
}

// TestHandleGetAccount_NoSnapshot_NoResource proves that when no snapshot
// exists, the response omits the resource field.
func TestHandleGetAccount_NoSnapshot_NoResource(t *testing.T) {
	svc := &mockProvider{platform: "youtube"}
	owner := ownedAccountFixture(1, "youtube")
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return owner, nil
		},
	}
	snapStore := &mockSnapshotStore{
		getFn: func(id int64) (*repository.AccountResourceSnapshot, error) {
			return nil, nil
		},
	}
	r := newTestRouter(svc, store, "", WithSnapshotStore(snapStore))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/21", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Resource *struct{} `json:"resource"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Resource != nil {
		t.Error("resource field should be nil/absent when no snapshot exists")
	}
}
