package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// TestHandleSyncAccount_Happy proves POST /accounts/{id}/sync returns 200
// with the fetched details when the provider implements
// AccountDetailsProvider.
func TestHandleSyncAccount_Happy(t *testing.T) {
	svc := &mockDetailProvider{
		mockProvider: mockProvider{platform: "youtube"},
		detailsFn: func(ctx context.Context, accessToken, platformUserID string) (*models.AccountDetails, error) {
			return &models.AccountDetails{
				ResourceType: "channel",
				ExternalID:   platformUserID,
				DisplayName:  "Synced Channel",
				Metrics: []models.AccountMetric{
					{Key: "subscribers", Label: "Subscribers", Value: 5000, DisplayValue: "5.0K"},
				},
				FetchedAt: time.Now(),
			}, nil
		},
	}
	owner := ownedAccountFixture(1, "youtube")
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return owner, nil
		},
	}
	vault := &mockCredentialVault{
		getFn: func(ctx context.Context, id int64, tt string) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "test-token"}, nil
		},
	}
	snapStore := &mockSnapshotStore{}
	r := newTestRouter(svc, store, "", WithCredentialVault(vault), WithSnapshotStore(snapStore))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/21/sync", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("sync: want 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		ResourceType string `json:"resource_type"`
		DisplayName  string `json:"display_name"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode sync response: %v", err)
	}
	if resp.DisplayName != "Synced Channel" {
		t.Errorf("display_name: want Synced Channel, got %q", resp.DisplayName)
	}
}

// TestAccountSync_RefreshesStaleSnapshot proves that POST /accounts/{id}/sync
// fetches fresh details from the provider and overwrites a stale snapshot.
func TestAccountSync_RefreshesStaleSnapshot(t *testing.T) {
	callCount := 0
	svc := &mockDetailProvider{
		mockProvider: mockProvider{platform: "youtube"},
		detailsFn: func(ctx context.Context, accessToken, platformUserID string) (*models.AccountDetails, error) {
			callCount++
			return &models.AccountDetails{
				ResourceType: "channel",
				ExternalID:   platformUserID,
				DisplayName:  "Fresh Channel Name",
				Metrics: []models.AccountMetric{
					{Key: "subscribers", Label: "Subscribers", Value: 9999, DisplayValue: "10.0K"},
				},
				FetchedAt: time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC),
			}, nil
		},
	}
	owner := ownedAccountFixture(1, "youtube")
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return owner, nil
		},
	}
	vault := &mockCredentialVault{
		getFn: func(ctx context.Context, id int64, tt string) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "test-token"}, nil
		},
	}
	var upserted *repository.AccountResourceSnapshot
	snapStore := &mockSnapshotStore{
		staleFn: func(platformAccountID int64, maxAge time.Duration) (bool, error) {
			return true, nil
		},
		upsertFn: func(snap *repository.AccountResourceSnapshot) error {
			upserted = snap
			return nil
		},
	}
	r := newTestRouter(svc, store, "", WithCredentialVault(vault), WithSnapshotStore(snapStore))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/21/sync", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("sync: want 200, got %d: %s", w.Code, w.Body.String())
	}
	if callCount != 1 {
		t.Errorf("provider called %d times, want 1", callCount)
	}
	if upserted == nil {
		t.Fatal("snapshot was not upserted")
	}
	if upserted.PlatformAccountID != 21 {
		t.Errorf("upserted platform_account_id: want 21, got %d", upserted.PlatformAccountID)
	}
	if upserted.ResourceType != "channel" {
		t.Errorf("upserted resource_type: want channel, got %q", upserted.ResourceType)
	}

	var resp struct {
		DisplayName string `json:"display_name"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode sync response: %v", err)
	}
	if resp.DisplayName != "Fresh Channel Name" {
		t.Errorf("display_name: want Fresh Channel Name, got %q", resp.DisplayName)
	}
}

// TestHandleSyncAccount_NoSnapshotStore_501 proves sync returns 501 when
// snapshot store is not wired.
func TestHandleSyncAccount_NoSnapshotStore_501(t *testing.T) {
	svc := &mockDetailProvider{
		mockProvider: mockProvider{platform: "youtube"},
	}
	owner := ownedAccountFixture(1, "youtube")
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return owner, nil
		},
	}
	r := newTestRouter(svc, store, "")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/21/sync", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNotImplemented {
		t.Fatalf("sync without snapshot store: want 501, got %d: %s", w.Code, w.Body.String())
	}
}

// TestHandleSyncAccount_CrossTenant_404 proves cross-tenant isolation.
func TestHandleSyncAccount_CrossTenant_404(t *testing.T) {
	svc := &mockDetailProvider{
		mockProvider: mockProvider{platform: "youtube"},
	}
	crossTenant := ownedAccountFixture(999, "youtube")
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return crossTenant, nil
		},
	}
	snapStore := &mockSnapshotStore{}
	r := newTestRouter(svc, store, "", WithSnapshotStore(snapStore))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/21/sync", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant sync: want 404, got %d: %s", w.Code, w.Body.String())
	}
}

// TestHandleAccountContent_Happy proves GET /accounts/{id}/content
// returns paginated content from the provider.
func TestHandleAccountContent_Happy(t *testing.T) {
	svc := &mockDetailProvider{
		mockProvider: mockProvider{platform: "youtube"},
		contentFn: func(ctx context.Context, accessToken, platformUserID string, cursor string, limit int, privacy string) (*models.AccountContentPage, error) {
			return &models.AccountContentPage{
				Items: []models.AccountContentItem{
					{
						ExternalID: "vid1",
						Title:      "Test Video",
						PublicURL:  "https://youtube.com/watch?v=vid1",
						Metrics: []models.AccountMetric{
							{Key: "views", Label: "Views", Value: 1000, DisplayValue: "1.0K"},
						},
					},
				},
				NextCursor: "next-page-token",
			}, nil
		},
	}
	owner := ownedAccountFixture(1, "youtube")
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return owner, nil
		},
	}
	vault := &mockCredentialVault{
		getFn: func(ctx context.Context, id int64, tt string) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "test-token"}, nil
		},
	}
	r := newTestRouter(svc, store, "", WithCredentialVault(vault))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/21/content?limit=10", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("content: want 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Items []struct {
			ExternalID string `json:"external_id"`
			Title      string `json:"title"`
		} `json:"items"`
		NextCursor string `json:"next_cursor"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode content response: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("items: want 1, got %d", len(resp.Items))
	}
	if resp.Items[0].ExternalID != "vid1" {
		t.Errorf("item external_id: want vid1, got %q", resp.Items[0].ExternalID)
	}
	if resp.NextCursor != "next-page-token" {
		t.Errorf("next_cursor: want next-page-token, got %q", resp.NextCursor)
	}
}

// TestHandleAccountContent_CrossTenant_404 proves cross-tenant isolation.
func TestHandleAccountContent_CrossTenant_404(t *testing.T) {
	svc := &mockDetailProvider{
		mockProvider: mockProvider{platform: "youtube"},
	}
	crossTenant := ownedAccountFixture(999, "youtube")
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return crossTenant, nil
		},
	}
	r := newTestRouter(svc, store, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/21/content", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant content: want 404, got %d: %s", w.Code, w.Body.String())
	}
}

// TestHandleAccountContent_PrivacyFilter_400 proves the handler rejects
// unknown privacy values before touching the provider.
func TestHandleAccountContent_PrivacyFilter_400(t *testing.T) {
	svc := &mockDetailProvider{
		mockProvider: mockProvider{platform: "youtube"},
	}
	owner := ownedAccountFixture(1, "youtube")
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return owner, nil
		},
	}
	vault := &mockCredentialVault{
		getFn: func(ctx context.Context, id int64, tt string) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "test-token"}, nil
		},
	}
	r := newTestRouter(svc, store, "", WithCredentialVault(vault))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/21/content?privacy=secret", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid privacy: want 400, got %d: %s", w.Code, w.Body.String())
	}
}

// TestOAuthCallback_YoutubeChannelAttachesChannelID proves that the
// generalized attachDiscoveredAccounts creates PlatformAccounts with
// the real YouTube channel ID (not the Google user ID) and persists
// the root bearer token via the atomic channel authorizer.
func TestOAuthCallback_YoutubeChannelAttachesChannelID(t *testing.T) {
	var attachedProfile *models.PlatformProfile

	svc := &mockDiscoverableProvider{
		mockProvider: mockProvider{
			platform: "youtube",
			handleCallback: func(ctx context.Context, state, code string) (*models.PlatformProfile, *models.TokenData, error) {
				return &models.PlatformProfile{
						PlatformUserID: "google-user-id-123",
						Username:       "Google User",
					}, &models.TokenData{
						AccessToken:  "bearer-token-abc",
						RefreshToken: "refresh-xyz",
						TokenType:    models.TokenTypeBearer,
						ExpiresIn:    3600,
					}, nil
			},
		},
		discoverFn: func(ctx context.Context, accessToken, platformUserID string) ([]*services.DiscoveredAccount, error) {
			return []*services.DiscoveredAccount{
				{
					Profile:  models.PlatformProfile{PlatformUserID: "UCrealchannelID123", Username: "My YouTube Channel"},
					Metadata: models.Metadata{},
				},
			}, nil
		},
	}
	store := &mockUserStore{
		attachFn: func(userID int64, profile *models.PlatformProfile, platform string) (*models.PlatformAccount, error) {
			attachedProfile = profile
			return &models.PlatformAccount{
				ID:             42,
				UserID:         userID,
				Platform:       platform,
				PlatformUserID: profile.PlatformUserID,
				Username:       profile.Username,
				Status:         models.AccountStatusActive,
			}, nil
		},
	}
	authorizer := &fakeChannelAuthorizer{}
	r := newTestRouter(svc, store, "", WithChannelAuthorizer(authorizer))

	state := "test-state"
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/youtube/callback?code=test-code&state="+state, nil)
	w := httptest.NewRecorder()
	setOAuthStateCookieForTest(req, "youtube", state)
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("callback: want 200, got %d: %s", w.Code, w.Body.String())
	}

	// The attached profile must carry the REAL YouTube channel ID.
	if attachedProfile == nil {
		t.Fatal("AttachPlatformAccount was not called")
	}
	if attachedProfile.PlatformUserID != "UCrealchannelID123" {
		t.Errorf("PlatformUserID: want UCrealchannelID123, got %q (BUG: Google user ID used instead of channel ID)", attachedProfile.PlatformUserID)
	}
	if attachedProfile.Username != "My YouTube Channel" {
		t.Errorf("Username: want My YouTube Channel, got %q", attachedProfile.Username)
	}

	// The atomic authorizer must receive exactly one call for the
	// attached account with the root bearer token. No
	// expected_channel_id was supplied, so the binder sees an empty
	// hint and falls through to the channels.list(mine=true) lookup.
	if authorizer.authorizeCalls.Load() != 1 {
		t.Fatalf("AuthorizeChannel calls: want 1, got %d", authorizer.authorizeCalls.Load())
	}
	if authorizer.lastAccountID != 42 {
		t.Errorf("authorizer accountID: want 42, got %d", authorizer.lastAccountID)
	}
	if authorizer.lastExpectedCh != "" {
		t.Errorf("lastExpectedCh: want empty (no expected_channel_id hint), got %q", authorizer.lastExpectedCh)
	}
	if authorizer.tokenWriteCount() != 1 {
		t.Fatalf("token writes: want 1, got %d: %+v", authorizer.tokenWriteCount(), authorizer.tokenWrites)
	}
	written := authorizer.tokenWrites[0]
	if written.AccountID != 42 || written.TokenType != models.TokenTypeBearer || written.AccessToken != "bearer-token-abc" || written.RefreshToken != "refresh-xyz" {
		t.Errorf("token write: want (accountID=42, tokenType=bearer, access=bearer-token-abc, refresh=refresh-xyz), got %+v", written)
	}
}

// TestOAuthCallback_FacebookPageToken_SupplementalSaved proves that
// the generalized attachDiscoveredAccounts still correctly saves
// Facebook Page Access Tokens as supplemental tokens via the atomic
// channel authorizer.
func TestOAuthCallback_FacebookPageToken_SupplementalSaved(t *testing.T) {
	svc := &mockDiscoverableProvider{
		mockProvider: mockProvider{
			platform: "facebook",
			handleCallback: func(ctx context.Context, state, code string) (*models.PlatformProfile, *models.TokenData, error) {
				return &models.PlatformProfile{
						PlatformUserID: "fb-user-123",
						Username:       "FB User",
					}, &models.TokenData{
						AccessToken: "long-lived-token",
						TokenType:   models.TokenTypeLongLived,
						ExpiresIn:   60 * 24 * 60 * 60,
					}, nil
			},
		},
		discoverFn: func(ctx context.Context, accessToken, platformUserID string) ([]*services.DiscoveredAccount, error) {
			return []*services.DiscoveredAccount{
				{
					Profile: models.PlatformProfile{PlatformUserID: "page-456", Username: "My FB Page"},
					SupplementalTokens: []*models.TokenData{
						{AccessToken: "page-token-789", TokenType: models.TokenTypePageAccess, ExpiresIn: 60 * 60 * 24 * 365 * 10, Scopes: []string{"pages_manage_posts", "pages_read_engagement", "pages_show_list"}},
					},
				},
			}, nil
		},
	}
	store := &mockUserStore{
		attachFn: func(userID int64, profile *models.PlatformProfile, platform string) (*models.PlatformAccount, error) {
			return &models.PlatformAccount{
				ID:             10,
				UserID:         userID,
				Platform:       platform,
				PlatformUserID: profile.PlatformUserID,
				Username:       profile.Username,
				Status:         models.AccountStatusActive,
			}, nil
		},
	}
	authorizer := &fakeChannelAuthorizer{}
	r := newTestRouter(svc, store, "", WithChannelAuthorizer(authorizer))

	state := "fb-state"
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/facebook/callback?code=fb-code&state="+state, nil)
	w := httptest.NewRecorder()
	setOAuthStateCookieForTest(req, "facebook", state)
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("callback: want 200, got %d: %s", w.Code, w.Body.String())
	}

	// The atomic authorizer should receive the single discovered
	// account and persist both the root long-lived token and the page
	// access token in the same call.
	if authorizer.authorizeCalls.Load() != 1 {
		t.Fatalf("AuthorizeChannel calls: want 1, got %d", authorizer.authorizeCalls.Load())
	}
	if authorizer.lastAccountID != 10 {
		t.Errorf("authorizer accountID: want 10, got %d", authorizer.lastAccountID)
	}

	// Build a map keyed by token type for stable assertions.
	writtenByType := make(map[string]fakeAuthTokenWrite)
	authorizer.mu.Lock()
	for _, tw := range authorizer.tokenWrites {
		writtenByType[tw.TokenType] = tw
	}
	authorizer.mu.Unlock()

	if len(writtenByType) != 2 {
		t.Fatalf("expected 2 saved tokens (root + page), got %d: %+v", len(writtenByType), authorizer.tokenWrites)
	}

	longLived := writtenByType[models.TokenTypeLongLived]
	if longLived.AccountID != 10 || longLived.AccessToken != "long-lived-token" {
		t.Errorf("root long-lived token not written as expected: %+v", longLived)
	}
	pageAccess := writtenByType[models.TokenTypePageAccess]
	if pageAccess.AccountID != 10 || pageAccess.AccessToken != "page-token-789" {
		t.Errorf("page access token not written as supplemental: %+v", pageAccess)
	}
}
