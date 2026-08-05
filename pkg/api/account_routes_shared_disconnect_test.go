package api

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

func TestHandleDisconnectAccount_UsesAtomicDisconnectDecision(t *testing.T) {
	owner := ownedAccountFixture(1, models.PlatformYouTube)
	connectionID := int64(55)
	owner.OAuthConnectionID = &connectionID
	calls := 0
	store := &mockUserStore{
		findPlatformAccountFn: func(int64) (*models.PlatformAccount, error) { return owner, nil },
		updatePlatformAccountFn: func(*models.PlatformAccount) error {
			t.Fatal("atomic disconnect must persist the lifecycle transition itself")
			return nil
		},
		countActiveOnConnectionFn: func(context.Context, int64, int64) (int64, error) {
			t.Fatal("atomic disconnect must not use the non-atomic count fallback")
			return 0, nil
		},
		disconnectPlatformAccountTxFn: func(_ context.Context, _ int64, revoke func(context.Context, *sql.Tx) error) (bool, bool, error) {
			calls++
			if revoke != nil {
				t.Fatal("shared-grant disconnect must not provide a revoke callback when a sibling remains")
			}
			return false, true, nil
		},
	}
	vault := &mockCredentialVault{
		revokeFn: func(context.Context, int64) error {
			t.Fatal("grant must be preserved when atomic operation reports active siblings")
			return nil
		},
	}
	r := newTestRouter(&mockProvider{platform: models.PlatformYouTube}, store, "", WithCredentialVault(vault))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/21/disconnect", nil)
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status: want 204, got %d: %s", w.Code, w.Body.String())
	}
	if calls != 1 {
		t.Fatalf("atomic disconnect calls: want 1, got %d", calls)
	}
}

func TestHandleDisconnectAccount_AtomicLastChannelRevokesGrant(t *testing.T) {
	owner := ownedAccountFixture(1, models.PlatformYouTube)
	connectionID := int64(55)
	owner.OAuthConnectionID = &connectionID
	store := &mockUserStore{
		findPlatformAccountFn: func(int64) (*models.PlatformAccount, error) { return owner, nil },
		disconnectPlatformAccountTxFn: func(ctx context.Context, _ int64, revoke func(context.Context, *sql.Tx) error) (bool, bool, error) {
			if revoke == nil {
				t.Fatal("last-channel disconnect must receive a transactional revoke callback")
			}
			if err := revoke(ctx, nil); err != nil {
				return false, true, err
			}
			return true, true, nil
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

	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/21/disconnect", nil)
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status: want 204, got %d: %s", w.Code, w.Body.String())
	}
	if revokeCalls != 1 {
		t.Fatalf("last-channel remote revoke calls: want 1, got %d", revokeCalls)
	}
}
