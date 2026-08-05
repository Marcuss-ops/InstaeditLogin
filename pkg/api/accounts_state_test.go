package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

func TestClassifyAccountStatus(t *testing.T) {
	tests := []struct {
		name        string
		status      string
		wantState   AccountState
		wantPublish bool
	}{
		{name: "active is valid", status: models.AccountStatusActive, wantState: AccountStateValid, wantPublish: true},
		{name: "legacy connected is valid", status: "connected", wantState: AccountStateValid, wantPublish: true},
		{name: "expired requires reconnect", status: models.AccountStatusExpired, wantState: AccountStateReconnectRequired},
		{name: "reauth requires reconnect", status: models.AccountStatusReauthRequired, wantState: AccountStateReconnectRequired},
		{name: "pending authorization requires reconnect", status: models.AccountStatusPendingAuthorization, wantState: AccountStateReconnectRequired},
		{name: "error requires reconnect", status: models.AccountStatusError, wantState: AccountStateReconnectRequired},
		{name: "suspended is not publishable", status: models.AccountStatusSuspended, wantState: AccountStateSuspended},
		{name: "disconnected is deleted", status: models.AccountStatusDisconnected, wantState: AccountStateDeleted},
		{name: "revoked is deleted", status: models.AccountStatusRevoked, wantState: AccountStateDeleted},
		{name: "legacy deleted alias is deleted", status: "deleted", wantState: AccountStateDeleted},
		{name: "unknown fails closed", status: "provider_unknown", wantState: AccountStateReconnectRequired},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state, publishable := classifyAccountStatus(tt.status)
			if state != tt.wantState {
				t.Fatalf("state: got %q, want %q", state, tt.wantState)
			}
			if publishable != tt.wantPublish {
				t.Fatalf("publishable: got %v, want %v", publishable, tt.wantPublish)
			}
		})
	}
}

func TestAccountListItemFromAccountIncludesStableState(t *testing.T) {
	item := accountListItemFromAccount(&models.PlatformAccount{
		ID:            42,
		Platform:      models.PlatformYouTube,
		Username:      "needs-reconnect",
		Status:        models.AccountStatusReauthRequired,
		LastErrorCode: "SHARED_GRANT_REAUTH_REQUIRED",
	})

	if item.AccountState != AccountStateReconnectRequired {
		t.Fatalf("account_state: got %q, want %q", item.AccountState, AccountStateReconnectRequired)
	}
	if item.IsPublishable {
		t.Fatal("reauth_required account must not be publishable")
	}
	if item.Status != models.AccountStatusReauthRequired {
		t.Fatalf("legacy status changed: got %q", item.Status)
	}
	if item.LastErrorCode != "SHARED_GRANT_REAUTH_REQUIRED" {
		t.Fatalf("last_error_code: got %q", item.LastErrorCode)
	}
}

func TestAccountListItemFromAccountExposesLanguageWithoutMetadata(t *testing.T) {
	item := accountListItemFromAccount(&models.PlatformAccount{
		Status:   models.AccountStatusActive,
		Metadata: models.Metadata{"language": " pl ", "manager": "private"},
	})

	if item.Language != "pl" {
		t.Fatalf("language: got %q, want %q", item.Language, "pl")
	}
}

func TestHandleListAccounts_ExposesStableStateAndPublishability(t *testing.T) {
	accounts := []*models.PlatformAccount{
		{ID: 1, UserID: 7, Platform: models.PlatformYouTube, PlatformUserID: "UC-valid", Username: "valid", Status: models.AccountStatusActive},
		{ID: 2, UserID: 7, Platform: models.PlatformYouTube, PlatformUserID: "UC-reauth", Username: "reauth", Status: models.AccountStatusReauthRequired},
		{ID: 3, UserID: 7, Platform: models.PlatformYouTube, PlatformUserID: "UC-suspended", Username: "suspended", Status: models.AccountStatusSuspended},
		{ID: 4, UserID: 7, Platform: models.PlatformYouTube, PlatformUserID: "UC-deleted", Username: "deleted", Status: models.AccountStatusDisconnected},
	}
	store := &mockUserStore{
		listFn: func(userID int64, platform string) ([]*models.PlatformAccount, error) {
			if userID != 7 || platform != "" {
				t.Fatalf("unexpected list scope: user=%d platform=%q", userID, platform)
			}
			return accounts, nil
		},
	}
	r := newTestRouter(&mockProvider{platform: models.PlatformYouTube}, store, "")
	// include_deleted=true exposes the full lifecycle vocabulary — the
	// default (P0) hides deleted-state accounts (covered by
	// TestHandleListAccounts_HidesDeletedStateByDefault).
	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts?include_deleted=true", nil)
	req = req.WithContext(auth.WithIdentity(req.Context(), auth.NewUserIdentity(7, 1, 1)))
	w := httptest.NewRecorder()

	r.handleListAccounts(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}

	var response struct {
		Accounts []struct {
			ID            int64        `json:"id"`
			AccountState  AccountState `json:"account_state"`
			IsPublishable bool         `json:"is_publishable"`
		} `json:"accounts"`
	}
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.Accounts) != len(accounts) {
		t.Fatalf("accounts: got %d, want %d", len(response.Accounts), len(accounts))
	}

	want := map[int64]struct {
		state       AccountState
		publishable bool
	}{
		1: {AccountStateValid, true},
		2: {AccountStateReconnectRequired, false},
		3: {AccountStateSuspended, false},
		4: {AccountStateDeleted, false},
	}
	for _, item := range response.Accounts {
		expected, ok := want[item.ID]
		if !ok {
			t.Fatalf("unexpected account id %d", item.ID)
		}
		if item.AccountState != expected.state || item.IsPublishable != expected.publishable {
			t.Errorf("account %d: got state=%q publishable=%v, want state=%q publishable=%v", item.ID, item.AccountState, item.IsPublishable, expected.state, expected.publishable)
		}
	}
}

// TestHandleListAccounts_HidesDeletedStateByDefault locks the P0
// contract: the plain GET /api/v1/accounts (no flag) must NOT return
// accounts classified as account_state="deleted" (status
// disconnected/revoked/legacy deleted aliases). They only appear with
// ?include_deleted=true.
func TestHandleListAccounts_HidesDeletedStateByDefault(t *testing.T) {
	accounts := []*models.PlatformAccount{
		{ID: 1, UserID: 7, Platform: models.PlatformYouTube, PlatformUserID: "UC-valid", Username: "valid", Status: models.AccountStatusActive},
		{ID: 4, UserID: 7, Platform: models.PlatformYouTube, PlatformUserID: "UC-disconnected", Username: "disconnected", Status: models.AccountStatusDisconnected},
		{ID: 5, UserID: 7, Platform: models.PlatformYouTube, PlatformUserID: "UC-revoked", Username: "revoked", Status: models.AccountStatusRevoked},
		{ID: 6, UserID: 7, Platform: models.PlatformYouTube, PlatformUserID: "UC-deleted-alias", Username: "deleted-alias", Status: "deleted"},
		{ID: 7, UserID: 7, Platform: models.PlatformYouTube, PlatformUserID: "UC-cancelled", Username: "cancelled", Status: "cancelled"},
		{ID: 8, UserID: 7, Platform: models.PlatformYouTube, PlatformUserID: "UC-canceled", Username: "canceled", Status: "canceled"},
	}
	store := &mockUserStore{
		listFn: func(userID int64, platform string) ([]*models.PlatformAccount, error) {
			return accounts, nil
		},
	}
	r := newTestRouter(&mockProvider{platform: models.PlatformYouTube}, store, "")

	decodeIDs := func(req *http.Request) []int64 {
		req = req.WithContext(auth.WithIdentity(req.Context(), auth.NewUserIdentity(7, 1, 1)))
		w := httptest.NewRecorder()
		r.handleListAccounts(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
		}
		var response struct {
			Accounts []struct {
				ID int64 `json:"id"`
			} `json:"accounts"`
		}
		if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		ids := make([]int64, 0, len(response.Accounts))
		for _, a := range response.Accounts {
			ids = append(ids, a.ID)
		}
		return ids
	}

	// Default: only the active account; every deleted-state alias is hidden.
	ids := decodeIDs(httptest.NewRequest(http.MethodGet, "/api/v1/accounts", nil))
	if len(ids) != 1 || ids[0] != 1 {
		t.Fatalf("default list must hide deleted-state accounts: got %v, want [1]", ids)
	}

	// Flag variants (true / 1 / yes, case-insensitive) include everything.
	for _, flag := range []string{"include_deleted=true", "include_deleted=1", "include_deleted=yes", "include_deleted=TRUE"} {
		ids := decodeIDs(httptest.NewRequest(http.MethodGet, "/api/v1/accounts?"+flag, nil))
		if len(ids) != len(accounts) {
			t.Fatalf("flag %q: got %d accounts, want %d: %v", flag, len(ids), len(accounts), ids)
		}
	}
}
