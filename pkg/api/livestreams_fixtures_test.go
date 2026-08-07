package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// Mocks
// ---------------------------------------------------------------------------

type mockLivestreamStore struct {
	createFn          func(ctx context.Context, ls *models.Livestream) error
	findByIDFn        func(ctx context.Context, id string) (*models.Livestream, error)
	listByWorkspaceFn func(ctx context.Context, workspaceID int64) ([]models.Livestream, error)
	updateFn          func(ctx context.Context, ls *models.Livestream) error
	deleteFn          func(ctx context.Context, id string) error
}

func (m *mockLivestreamStore) Create(ctx context.Context, ls *models.Livestream) error {
	if m.createFn == nil {
		return nil
	}
	return m.createFn(ctx, ls)
}
func (m *mockLivestreamStore) FindByID(ctx context.Context, id string) (*models.Livestream, error) {
	if m.findByIDFn != nil {
		return m.findByIDFn(ctx, id)
	}
	return nil, nil
}
func (m *mockLivestreamStore) ListByWorkspace(ctx context.Context, workspaceID int64) ([]models.Livestream, error) {
	if m.listByWorkspaceFn != nil {
		return m.listByWorkspaceFn(ctx, workspaceID)
	}
	return nil, nil
}
func (m *mockLivestreamStore) Update(ctx context.Context, ls *models.Livestream) error {
	if m.updateFn == nil {
		return nil
	}
	return m.updateFn(ctx, ls)
}
func (m *mockLivestreamStore) Delete(ctx context.Context, id string) error {
	if m.deleteFn == nil {
		return nil
	}
	return m.deleteFn(ctx, id)
}

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

const livestreamTestWorkspaceID = int64(7)

func livestreamTestAccount() *models.PlatformAccount {
	return &models.PlatformAccount{
		ID:             42,
		UserID:         1,
		Platform:       models.PlatformYouTube,
		PlatformUserID: "UC123",
		Username:       "testchannel",
		Status:         models.AccountStatusActive,
	}
}

func livestreamTestRouter(lsStore *mockLivestreamStore, account *models.PlatformAccount, ownerID int64) *Router {
	return livestreamTestRouterWithVault(lsStore, account, ownerID, &mockCredentialVault{
		getFn: func(ctx context.Context, platformAccountID int64, tokenType string) (*models.OAuthToken, error) {
			if account != nil && platformAccountID == account.ID {
				return &models.OAuthToken{TokenType: tokenType, Scopes: []string{"https://www.googleapis.com/auth/youtube.force-ssl"}}, nil
			}
			return nil, nil
		},
	})
}

// livestreamTestRouterWithVault wires a custom vault so the scope guard
// in handleCreateLivestream is exercised (and so tests can simulate a
// grant without the live scope, or a missing/expired grant).
func livestreamTestRouterWithVault(lsStore *mockLivestreamStore, account *models.PlatformAccount, ownerID int64, vault credentials.VaultAPI) *Router {
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			if account != nil && id == account.ID {
				return account, nil
			}
			return nil, nil
		},
	}
	wsStore := &mockWorkspaceStore{
		findByIDFn: func(id int64) (*models.Workspace, error) {
			if id == livestreamTestWorkspaceID {
				return &models.Workspace{ID: livestreamTestWorkspaceID, OwnerID: ownerID, Name: "Test Workspace"}, nil
			}
			return nil, nil
		},
		findChannelFn: func(ctx context.Context, wsID, accountID int64) (*models.WorkspaceChannel, error) {
			if wsID == livestreamTestWorkspaceID && account != nil && accountID == account.ID {
				return &models.WorkspaceChannel{WorkspaceID: wsID, PlatformAccountID: accountID, Enabled: true}, nil
			}
			return nil, nil
		},
	}
	return mustNewRouterWithDefaults(
		services.NewCapabilityRouter(),
		store,
		auth.NewManager(testJWTSecret, 24),
		"",
		nil, WithWorkspaceStore(wsStore),
		WithLivestreamStore(lsStore),
		WithCredentialVault(vault),
	)
}

func validLivestreamPayload() map[string]any {
	return map[string]any{
		"workspace_id":        livestreamTestWorkspaceID,
		"platform_account_id": int64(42),
		"title":               "WWE News 24/7",
		"privacy_status":      "unlisted",
		"playback_mode":       "loop_continuous",
		"schedule_type":       "manual",
		"resolution":          "1080p30",
	}
}

func doLivestreamRequest(t *testing.T, r *Router, method, path string, payload any) *httptest.ResponseRecorder {
	t.Helper()
	var body *bytes.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal payload: %v", err)
		}
		body = bytes.NewReader(raw)
	} else {
		body = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, body)
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	return w
}
