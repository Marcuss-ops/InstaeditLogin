package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// TestHandleDisconnectAccount_SharedGrant_SequentialLifecycle pins the P1
// shared-grant guarantee at the API surface: disconnecting A while sibling B
// is still active never touches the vault; only disconnecting the last
// channel B revokes the shared grant — exactly once.
func TestHandleDisconnectAccount_SharedGrant_SequentialLifecycle(t *testing.T) {
	active := map[int64]struct{}{21: {}, 22: {}}
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			owner := ownedAccountFixture(1, models.PlatformYouTube)
			owner.ID = id // the fixture hardcodes 21; honor the path id
			connectionID := int64(55)
			owner.OAuthConnectionID = &connectionID
			return owner, nil
		},
		// Stateful atomic store: models the production repo counting active
		// siblings on the shared grant inside its transaction.
		disconnectPlatformAccountTxFn: func(ctx context.Context, accountID int64, revoke func(context.Context, *sql.Tx) error) (bool, bool, error) {
			delete(active, accountID)
			if len(active) == 0 && revoke != nil {
				if err := revoke(ctx, nil); err != nil {
					return false, true, err
				}
			}
			return len(active) == 0, true, nil
		},
	}
	var revokeCalls int
	vault := &mockCredentialVault{
		getRefreshTokenFn: func(context.Context, int64) (string, error) {
			return "refresh-token", nil
		},
	}
	r := newTestRouter(&mockProvider{platform: models.PlatformYouTube}, store, "", WithCredentialVault(vault))
	r.youtubeRevoker = &fakeYouTubeRevoker{revokeFn: func(context.Context, string) error {
		revokeCalls++
		return nil
	}}

	disconnect := func(accountID string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+accountID+"/disconnect", nil)
		w := httptest.NewRecorder()
		withBearerJWT(t, req, 1)
		r.Setup().ServeHTTP(w, req)
		return w.Code
	}

	if code := disconnect("21"); code != http.StatusNoContent {
		t.Fatalf("disconnect A: want 204, got %d", code)
	}
	if revokeCalls != 0 {
		t.Fatalf("disconnect A: vault.Revoke must NOT run while sibling B is active (got %d calls)", revokeCalls)
	}

	if code := disconnect("22"); code != http.StatusNoContent {
		t.Fatalf("disconnect B: want 204, got %d", code)
	}
	if revokeCalls != 1 {
		t.Fatalf("disconnect B: remote revoke must run exactly once for the last channel (got %d calls)", revokeCalls)
	}
}

// TestHandleDeleteAccountData_SharedGrant_SequentialLifecycle pins the hard
// delete: deleting A while sibling B is active preserves the grant (the
// remote revoke callback is not invoked); deleting the last channel B
// triggers the remote Google revoke exactly once.
func TestHandleDeleteAccountData_SharedGrant_SequentialLifecycle(t *testing.T) {
	active := map[int64]struct{}{21: {}, 22: {}}
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			owner := ownedAccountFixture(1, models.PlatformYouTube)
			owner.ID = id // the fixture hardcodes 21; honor the path id
			connID := int64(55)
			owner.OAuthConnectionID = &connID
			return owner, nil
		},
		permanentlyDeleteAccountFn: func(ctx context.Context, accountID int64, revoke func(context.Context, *sql.Tx) error) (bool, error) {
			delete(active, accountID)
			// The production repo invokes revoke only when this is the last
			// active channel of the grant.
			if len(active) == 0 && revoke != nil {
				if err := revoke(ctx, nil); err != nil {
					return false, err
				}
			}
			return true, nil
		},
	}
	vault := &mockCredentialVault{
		getRefreshTokenFn: func(ctx context.Context, id int64) (string, error) {
			return "yt-shared-refresh-token", nil
		},
	}
	var remoteRevokeCalls int
	r := newTestRouter(&mockProvider{platform: models.PlatformYouTube}, store, "", WithCredentialVault(vault))
	r.youtubeRevoker = &fakeYouTubeRevoker{
		revokeFn: func(ctx context.Context, token string) error {
			remoteRevokeCalls++
			return nil
		},
	}

	hardDelete := func(accountID string) int {
		req := httptest.NewRequest(http.MethodDelete, "/api/v1/accounts/"+accountID+"/data", nil)
		w := httptest.NewRecorder()
		withBearerJWT(t, req, 1)
		r.Setup().ServeHTTP(w, req)
		return w.Code
	}

	if code := hardDelete("21"); code != http.StatusNoContent {
		t.Fatalf("hard delete A: want 204, got %d", code)
	}
	if remoteRevokeCalls != 0 {
		t.Fatalf("hard delete A: remote revoke must NOT run while sibling B is active (got %d calls)", remoteRevokeCalls)
	}

	if code := hardDelete("22"); code != http.StatusNoContent {
		t.Fatalf("hard delete B: want 204, got %d", code)
	}
	if remoteRevokeCalls != 1 {
		t.Fatalf("hard delete B: remote revoke must run exactly once for the last channel (got %d calls)", remoteRevokeCalls)
	}
}

// TestHandleListAccounts_SharedGrant_SiblingsRemainValid pins requirement
// "disconnecting/deleting A must not break B and C": after A leaves the
// grant (soft disconnect or tombstone), the accounts list still surfaces B
// and C as valid + publishable, while A itself is hidden.
func TestHandleListAccounts_SharedGrant_SiblingsRemainValid(t *testing.T) {
	connID := int64(55)
	mkAccount := func(id int64, platformUserID, username, status string) *models.PlatformAccount {
		acc := ownedAccountFixture(1, "youtube")
		acc.ID = id
		acc.PlatformUserID = platformUserID
		acc.Username = username
		acc.Status = status
		acc.OAuthConnectionID = &connID
		return acc
	}
	disconnectedA := mkAccount(21, "UC-a", "channel_a", models.AccountStatusDisconnected)
	tombstonedA := mkAccount(21, "UC-a", "[deleted]", models.AccountStatusDeleted)
	channelB := mkAccount(22, "UC-b", "channel_b", models.AccountStatusActive)
	channelC := mkAccount(23, "UC-c", "channel_c", models.AccountStatusActive)

	for name, tt := range map[string]struct {
		accountA *models.PlatformAccount
	}{
		"after soft disconnect":  {accountA: disconnectedA},
		"after permanent delete": {accountA: tombstonedA},
	} {
		t.Run(name, func(t *testing.T) {
			store := &mockUserStore{
				listFn: func(userID int64, platform string) ([]*models.PlatformAccount, error) {
					return []*models.PlatformAccount{tt.accountA, channelB, channelC}, nil
				},
			}
			r := newTestRouter(&mockProvider{platform: "youtube"}, store, "")
			req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts", nil)
			w := httptest.NewRecorder()
			withBearerJWT(t, req, 1)
			r.Setup().ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("list: want 200, got %d: %s", w.Code, w.Body.String())
			}
			var resp struct {
				Accounts []struct {
					ID            int64  `json:"id"`
					Username      string `json:"username"`
					AccountState  string `json:"account_state"`
					IsPublishable bool   `json:"is_publishable"`
				} `json:"accounts"`
			}
			if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
				t.Fatalf("decode list: %v", err)
			}
			if len(resp.Accounts) != 2 {
				t.Fatalf("accounts length: want 2 (A hidden, B and C visible), got %d: %s", len(resp.Accounts), w.Body.String())
			}
			for _, acc := range resp.Accounts {
				if acc.ID == 21 {
					t.Fatalf("A must be hidden from the list, got %+v", acc)
				}
				if acc.AccountState != string(AccountStateValid) || !acc.IsPublishable {
					t.Errorf("sibling %d must remain valid + publishable, got state=%q publishable=%v", acc.ID, acc.AccountState, acc.IsPublishable)
				}
			}
		})
	}
}
