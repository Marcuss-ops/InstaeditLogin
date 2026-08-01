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
		ID:       42,
		Platform: models.PlatformYouTube,
		Username: "needs-reconnect",
		Status:   models.AccountStatusReauthRequired,
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
	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts", nil)
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
