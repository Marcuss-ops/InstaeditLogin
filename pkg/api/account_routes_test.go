package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
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
