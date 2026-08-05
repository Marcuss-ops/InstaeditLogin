package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

func TestHandleDeleteOAuthGrant_UsesTransactionalStore(t *testing.T) {
	grantID := int64(55)
	account := ownedAccountFixture(1, models.PlatformYouTube)
	account.OAuthConnectionID = &grantID
	var gotGrantID int64
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			if id != 21 {
				t.Fatalf("account id: got %d, want 21", id)
			}
			return account, nil
		},
		disconnectOAuthGrantFn: func(ctx context.Context, got int64) error {
			gotGrantID = got
			return nil
		},
	}
	r := newTestRouter(&mockProvider{platform: models.PlatformYouTube}, store, "")

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/accounts/21/oauth-grant", nil)
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status: got %d, want 204: %s", w.Code, w.Body.String())
	}
	if gotGrantID != grantID {
		t.Fatalf("grant id: got %d, want %d", gotGrantID, grantID)
	}
}

func TestHandleDeleteOAuthGrant_DoesNotDisconnectForeignAccount(t *testing.T) {
	account := ownedAccountFixture(999, models.PlatformYouTube)
	grantID := int64(55)
	account.OAuthConnectionID = &grantID
	called := false
	store := &mockUserStore{
		findPlatformAccountFn: func(int64) (*models.PlatformAccount, error) { return account, nil },
		disconnectOAuthGrantFn: func(context.Context, int64) error {
			called = true
			return nil
		},
	}
	r := newTestRouter(&mockProvider{platform: models.PlatformYouTube}, store, "")

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/accounts/21/oauth-grant", nil)
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404: %s", w.Code, w.Body.String())
	}
	if called {
		t.Fatal("transactional grant disconnect must not run for a foreign account")
	}
}

func TestHandleDeleteOAuthGrant_PropagatesStoreFailure(t *testing.T) {
	account := ownedAccountFixture(1, models.PlatformYouTube)
	grantID := int64(55)
	account.OAuthConnectionID = &grantID
	store := &mockUserStore{
		findPlatformAccountFn: func(int64) (*models.PlatformAccount, error) { return account, nil },
		disconnectOAuthGrantFn: func(context.Context, int64) error {
			return fmt.Errorf("transaction rolled back")
		},
	}
	r := newTestRouter(&mockProvider{platform: models.PlatformYouTube}, store, "")

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/accounts/21/oauth-grant", nil)
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status: got %d, want 500: %s", w.Code, w.Body.String())
	}
}
