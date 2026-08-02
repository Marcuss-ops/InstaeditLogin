package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

func TestHandleCallback_FirstYouTubeAuthorizationWithoutRefreshTokenMarksReauth(t *testing.T) {
	svc := &mockProvider{
		platform: "youtube",
		handleCallback: func(context.Context, string, string) (*models.PlatformProfile, *models.TokenData, error) {
			return &models.PlatformProfile{
					PlatformUserID: "UCabcdefghijklmnopqrstuv",
					Username:       "New YouTube Channel",
				}, &models.TokenData{
					AccessToken: "youtube-access",
					TokenType:   models.TokenTypeBearer,
					ExpiresIn:   3600,
					// RefreshToken intentionally omitted: this is the
					// first-authorization regression case.
				}, nil
		},
	}
	var marked struct {
		calls   int
		account int64
		code    string
		message string
	}
	store := &mockUserStore{
		attachFn: func(userID int64, profile *models.PlatformProfile, platform string) (*models.PlatformAccount, error) {
			return &models.PlatformAccount{
				ID: 10, UserID: userID, Platform: platform,
				PlatformUserID: profile.PlatformUserID, Username: profile.Username,
				Status: models.AccountStatusPendingAuthorization,
			}, nil
		},
		markReauthRequiredFn: func(_ context.Context, accountID int64, code, message string) error {
			marked.calls++
			marked.account = accountID
			marked.code = code
			marked.message = message
			return nil
		},
	}
	authorizer := &fakeChannelAuthorizer{authorizeErr: services.ErrOAuthRefreshTokenRequired}
	r := newTestRouter(svc, store, "", WithChannelAuthorizer(authorizer))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/youtube/callback?code=abc&state=test-state", nil)
	setOAuthStateCookieForTest(req, "youtube", "test-state")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("missing first-connection refresh token: want 422, got %d: %s", w.Code, w.Body.String())
	}
	if marked.calls != 1 || marked.account != 10 {
		t.Fatalf("MarkReauthRequired: want one call for account 10, got calls=%d account=%d", marked.calls, marked.account)
	}
	if marked.code != "refresh_token_required" {
		t.Errorf("reauth code: want refresh_token_required, got %q", marked.code)
	}
	if !strings.Contains(marked.message, "offline refresh token") {
		t.Errorf("reauth message should explain offline consent, got %q", marked.message)
	}
	if authorizer.authorizeCalls.Load() != 1 {
		t.Errorf("AuthorizeChannel calls: want 1, got %d", authorizer.authorizeCalls.Load())
	}
}

func TestHandleCallback_FirstYouTubeAuthorization_ReauthPersistenceFailureIsServerError(t *testing.T) {
	svc := &mockProvider{
		platform: "youtube",
		handleCallback: func(context.Context, string, string) (*models.PlatformProfile, *models.TokenData, error) {
			return &models.PlatformProfile{PlatformUserID: "UCabcdefghijklmnopqrstuv", Username: "YouTube Channel"}, &models.TokenData{
				AccessToken: "youtube-access",
				TokenType:   models.TokenTypeBearer,
				ExpiresIn:   3600,
			}, nil
		},
	}
	store := &mockUserStore{
		attachFn: func(userID int64, profile *models.PlatformProfile, platform string) (*models.PlatformAccount, error) {
			return &models.PlatformAccount{ID: 10, UserID: userID, Platform: platform, PlatformUserID: profile.PlatformUserID}, nil
		},
		markReauthRequiredFn: func(context.Context, int64, string, string) error {
			return fmt.Errorf("reauth state database unavailable")
		},
	}
	authorizer := &fakeChannelAuthorizer{authorizeErr: services.ErrOAuthRefreshTokenRequired}
	r := newTestRouter(svc, store, "", WithChannelAuthorizer(authorizer))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/youtube/callback?code=abc&state=test-state", nil)
	setOAuthStateCookieForTest(req, "youtube", "test-state")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("reauth persistence failure: want 500, got %d: %s", w.Code, w.Body.String())
	}
	if authorizer.tokenWriteCount() != 0 {
		t.Fatalf("reauth persistence failure must not report a successful credential write; got %d", authorizer.tokenWriteCount())
	}
}

func TestHandleCallback_FirstYouTubeDiscoverer_ReauthPersistenceFailureIsServerError(t *testing.T) {
	svc := &mockDiscoverableProvider{
		mockProvider: mockProvider{
			platform: "youtube",
			handleCallback: func(context.Context, string, string) (*models.PlatformProfile, *models.TokenData, error) {
				return &models.PlatformProfile{PlatformUserID: "google-subject", Username: "Google Account"}, &models.TokenData{
					AccessToken: "youtube-access",
					TokenType:   models.TokenTypeBearer,
					ExpiresIn:   3600,
				}, nil
			},
		},
		discoverFn: func(context.Context, string, string) ([]*services.DiscoveredAccount, error) {
			return []*services.DiscoveredAccount{{
				Profile: models.PlatformProfile{PlatformUserID: "UCfirstconnect", Username: "First Channel"},
			}}, nil
		},
	}
	store := &mockUserStore{
		attachFn: func(userID int64, profile *models.PlatformProfile, platform string) (*models.PlatformAccount, error) {
			return &models.PlatformAccount{ID: 41, UserID: userID, Platform: platform, PlatformUserID: profile.PlatformUserID}, nil
		},
		markReauthRequiredFn: func(context.Context, int64, string, string) error {
			return fmt.Errorf("reauth state database unavailable")
		},
	}
	authorizer := &fakeChannelAuthorizer{authorizeErr: services.ErrOAuthRefreshTokenRequired}
	r := newTestRouter(svc, store, "", WithChannelAuthorizer(authorizer))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/youtube/callback?code=abc&state=test-state", nil)
	setOAuthStateCookieForTest(req, "youtube", "test-state")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("discoverer reauth persistence failure: want 500, got %d: %s", w.Code, w.Body.String())
	}
	if authorizer.tokenWriteCount() != 0 {
		t.Fatalf("discoverer reauth persistence failure must not report a successful credential write; got %d", authorizer.tokenWriteCount())
	}
}

func TestHandleCallback_FirstYouTubeDiscovererWithoutRefreshTokenMarksReauth(t *testing.T) {
	svc := &mockDiscoverableProvider{
		mockProvider: mockProvider{
			platform: "youtube",
			handleCallback: func(context.Context, string, string) (*models.PlatformProfile, *models.TokenData, error) {
				return &models.PlatformProfile{PlatformUserID: "google-subject", Username: "Google Account"}, &models.TokenData{
					AccessToken: "youtube-access",
					TokenType:   models.TokenTypeBearer,
					ExpiresIn:   3600,
				}, nil
			},
		},
		discoverFn: func(context.Context, string, string) ([]*services.DiscoveredAccount, error) {
			return []*services.DiscoveredAccount{{
				Profile: models.PlatformProfile{PlatformUserID: "UCfirstconnect", Username: "First Channel"},
			}}, nil
		},
	}
	var marked struct {
		calls   int
		account int64
		code    string
	}
	store := &mockUserStore{
		attachFn: func(userID int64, profile *models.PlatformProfile, platform string) (*models.PlatformAccount, error) {
			return &models.PlatformAccount{
				ID: 41, UserID: userID, Platform: platform,
				PlatformUserID: profile.PlatformUserID, Status: models.AccountStatusPendingAuthorization,
			}, nil
		},
		markReauthRequiredFn: func(_ context.Context, accountID int64, code, _ string) error {
			marked.calls++
			marked.account = accountID
			marked.code = code
			return nil
		},
	}
	authorizer := &fakeChannelAuthorizer{authorizeErr: services.ErrOAuthRefreshTokenRequired}
	r := newTestRouter(svc, store, "", WithChannelAuthorizer(authorizer))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/youtube/callback?code=abc&state=test-state", nil)
	setOAuthStateCookieForTest(req, "youtube", "test-state")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("discoverer first connection without refresh token: want 422, got %d: %s", w.Code, w.Body.String())
	}
	if marked.calls != 1 || marked.account != 41 || marked.code != "refresh_token_required" {
		t.Fatalf("MarkReauthRequired: want one refresh_token_required call for account 41, got %+v", marked)
	}
	if authorizer.authorizeCalls.Load() != 1 {
		t.Fatalf("AuthorizeChannel calls: want 1, got %d", authorizer.authorizeCalls.Load())
	}
	if authorizer.tokenWriteCount() != 0 {
		t.Fatalf("missing first-connect refresh token must not write unusable credentials; got %d", authorizer.tokenWriteCount())
	}
}

func TestAcceptance_NonDiscovererUsesAtomicAuthorizer(t *testing.T) {
	svc := &mockProvider{
		platform:       "instagram",
		handleCallback: successCallback,
	}
	store := &mockUserStore{
		attachFn: successAttach,
	}
	authorizer := &fakeChannelAuthorizer{}
	r := newTestRouter(svc, store, "", WithChannelAuthorizer(authorizer))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/instagram/callback?code=abc&state=test-state", nil)
	setOAuthStateCookieForTest(req, "instagram", "test-state")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	// (1) exactly one AuthorizeChannel call on the non-discoverer path.
	if authorizer.authorizeCalls.Load() != 1 {
		t.Fatalf("AuthorizeChannel must be called exactly once on the non-discoverer path; got %d (legacy direct vault.Save path is BACK)", authorizer.authorizeCalls.Load())
	}
	// (2) argument shape: account.ID (single account), no YouTube
	// expected_channel (empty string), variadic token list contains
	// the principal TokenData. Scopes MAY be nil for the Instagram
	// happy-path fixture (successCallback omits the Scopes field)
	// and that is documented and OK — the production service passes
	// the slice through pq.Array, which serialises nil as NULL.
	if authorizer.lastAccountID != 10 {
		t.Errorf("lastAccountID: want 10 (from successAttach), got %d", authorizer.lastAccountID)
	}
	if authorizer.lastExpectedCh != "" {
		t.Errorf("lastExpectedCh: want \"\" (non-YouTube path; binder short-circuits), got %q", authorizer.lastExpectedCh)
	}
	if got := len(authorizer.lastTokens); got != 1 {
		t.Fatalf("lastTokens len: want 1 (principal token only on non-YouTube path), got %d", got)
	}
	if authorizer.lastTokens[0] == nil || authorizer.lastTokens[0].AccessToken != "at-secret" {
		t.Errorf("lastTokens[0]: want TokenData{AccessToken: \"at-secret\"}, got %+v", authorizer.lastTokens[0])
	}
	// (3) tokenWrites independent audit: exactly one cipher row
	// written for this single-account Instagram happy path.
	if n := authorizer.tokenWriteCount(); n != 1 {
		t.Errorf("tokenWrites len: want 1 (single principal token on non-YouTube path), got %d", n)
	}
	if w := authorizer.tokenWrites[0]; w.AccountID != 10 || w.TokenType != "bearer" || w.AccessToken != "at-secret" {
		t.Errorf("tokenWrites[0]: want (accountID=10, tokenType=bearer, access=at-secret), got %+v", w)
	}
}
