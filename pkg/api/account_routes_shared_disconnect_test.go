package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

func TestHandleDeleteAccount_UsesAtomicDisconnectDecision(t *testing.T) {
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
		disconnectPlatformAccountFn: func(context.Context, int64) (bool, bool, error) {
			calls++
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

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/accounts/21", nil)
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

func TestHandleDeleteAccount_AtomicLastChannelRevokesGrant(t *testing.T) {
	owner := ownedAccountFixture(1, models.PlatformYouTube)
	connectionID := int64(55)
	owner.OAuthConnectionID = &connectionID
	store := &mockUserStore{
		findPlatformAccountFn: func(int64) (*models.PlatformAccount, error) { return owner, nil },
		disconnectPlatformAccountFn: func(context.Context, int64) (bool, bool, error) {
			return true, true, nil
		},
	}
	var revokeCalls int
	vault := &mockCredentialVault{
		revokeFn: func(context.Context, int64) error {
			revokeCalls++
			return nil
		},
	}
	r := newTestRouter(&mockProvider{platform: models.PlatformYouTube}, store, "", WithCredentialVault(vault))

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/accounts/21", nil)
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status: want 204, got %d: %s", w.Code, w.Body.String())
	}
	if revokeCalls != 1 {
		t.Fatalf("last-channel vault.Revoke calls: want 1, got %d", revokeCalls)
	}
}
