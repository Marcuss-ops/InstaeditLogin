package api

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleCallback_MissingCode(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	store := &mockUserStore{}
	r := newTestRouter(svc, store, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/instagram/callback", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", w.Code)
	}
}

func TestHandleCallbackRouteForTest_YouTubeBindsProvider(t *testing.T) {
	svc := &mockProvider{platform: "youtube"}
	store := &mockUserStore{}
	r := newTestRouter(svc, store, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/youtube/callback", nil)
	w := httptest.NewRecorder()
	r.HandleOAuthCallbackRouteForTest().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("/api/v1/auth/youtube/callback via test seam: want 400 missing code, got %d: %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "unsupported provider") {
		t.Fatalf("test seam failed to bind provider from callback path: %s", w.Body.String())
	}
}

func TestHandleCallbackRouteForTest_YouTubeDispatchesProvider(t *testing.T) {
	svc := &mockProvider{
		platform: "youtube",
		handleCallback: func(context.Context, string, string) (*models.PlatformProfile, *models.TokenData, error) {
			return nil, nil, fmt.Errorf("youtube callback sentinel")
		},
	}
	r := newTestRouter(svc, &mockUserStore{}, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/youtube/callback?code=mock-code&state=test-state", nil)
	setOAuthStateCookieForTest(req, "youtube", "test-state")
	w := httptest.NewRecorder()
	r.HandleOAuthCallbackRouteForTest().ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("YouTube callback via test seam: want 500 from provider sentinel, got %d: %s", w.Code, w.Body.String())
	}
	if svc.handleCallbackCalls != 1 {
		t.Fatalf("YouTube provider HandleCallback calls: want 1, got %d", svc.handleCallbackCalls)
	}
	if strings.Contains(w.Body.String(), "unsupported provider") {
		t.Fatalf("test seam did not dispatch YouTube provider: %s", w.Body.String())
	}
}

func TestHandleCallback_UnsupportedProvider(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	store := &mockUserStore{}
	r := newTestRouter(svc, store, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/unknown/callback?code=abc", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}

func TestHandleCallback_HandleCallbackError(t *testing.T) {
	svc := &mockProvider{
		platform: "twitter",
		handleCallback: func(ctx context.Context, state, code string) (*models.PlatformProfile, *models.TokenData, error) {
			return nil, nil, fmt.Errorf("platform auth error")
		},
	}
	store := &mockUserStore{}
	r := newTestRouter(svc, store, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/twitter/callback?code=bad&state=test-state", nil)
	setOAuthStateCookieForTest(req, "twitter", "test-state")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleCallback_AttachError_409(t *testing.T) {
	svc := &mockProvider{
		platform:       "instagram",
		handleCallback: successCallback,
	}
	store := &mockUserStore{
		attachFn: func(userID int64, profile *models.PlatformProfile, platform string) (*models.PlatformAccount, error) {
			return nil, fmt.Errorf("%w: platform=%s owned_by=999 requested_by=%d",
				repository.ErrAccountAlreadyLinked, platform, userID)
		},
	}
	r := newTestRouter(svc, store, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/instagram/callback?code=abc&state=test-state", nil)
	setOAuthStateCookieForTest(req, "instagram", "test-state")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("want 409 Conflict, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "platform account") {
		t.Errorf("response body should explain the link conflict; got %q", w.Body.String())
	}
}

func TestHandleCallback_AttachError_500(t *testing.T) {
	svc := &mockProvider{
		platform:       "instagram",
		handleCallback: successCallback,
	}
	store := &mockUserStore{
		attachFn: func(userID int64, profile *models.PlatformProfile, platform string) (*models.PlatformAccount, error) {
			return nil, fmt.Errorf("db error")
		},
	}
	r := newTestRouter(svc, store, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/instagram/callback?code=abc&state=test-state", nil)
	setOAuthStateCookieForTest(req, "instagram", "test-state")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleCallback_AuthorizeChannelError(t *testing.T) {
	svc := &mockProvider{
		platform:       "instagram",
		handleCallback: successCallback,
	}
	store := &mockUserStore{
		attachFn: successAttach,
	}
	authorizer := &fakeChannelAuthorizer{
		authorizeErr: fmt.Errorf("token save error"),
	}
	r := newTestRouter(svc, store, "", WithChannelAuthorizer(authorizer))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/instagram/callback?code=abc&state=test-state", nil)
	setOAuthStateCookieForTest(req, "instagram", "test-state")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d", w.Code)
	}
	if authorizer.authorizeCalls.Load() != 1 {
		t.Fatalf("AuthorizeChannel must be called exactly once; got %d", authorizer.authorizeCalls.Load())
	}
	// Acceptance-closure on the legacy failure mode: zero cipher
	// writes when authorizeErr fires means the production ROLLBACK
	// along the row's pending_authorization stay is reproduced.
	if n := authorizer.tokenWriteCount(); n != 0 {
		t.Errorf("tokenWrites len on authorizeErr: want 0 (ROLLBACK semantic), got %d", n)
	}
}

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

func TestHandleCallback_Success_JSONResponse(t *testing.T) {
	svc := &mockProvider{
		platform:       "instagram",
		handleCallback: successCallback,
	}
	store := &mockUserStore{
		attachFn: successAttach,
	}
	r := newTestRouter(svc, store, "") // empty frontendURL → JSON

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/instagram/callback?code=abc&state=test-state", nil)
	setOAuthStateCookieForTest(req, "instagram", "test-state")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}

	var body map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	// SPRINT 7.1 (P0#14): the OAuth callback is now an "attach to
	// existing session" operation — no one-time code is issued, no
	// JWT is minted, and no user is auto-created. The typed JSON
	// response in CLI / test mode reports the link.
	if body["status"] != "connected" {
		t.Fatalf("status: want connected, got %v (SPRINT 7.1 contract)", body["status"])
	}
	if body["provider"] != "instagram" {
		t.Fatalf("provider: want instagram, got %v", body["provider"])
	}
	if _, present := body["code"]; present {
		t.Fatalf("code field must NOT appear in OAuth callback response (SPRINT 7.1: no one-time code path): %v", body)
	}
	if _, present := body["jwt"]; present {
		t.Fatalf("jwt field must NEVER appear (Taglio 1.2 + SPRINT 7.1): %v", body)
	}
	if uid, ok := body["user_id"].(float64); !ok || uid != 1 {
		t.Fatalf("user_id: want 1 (the session user), got %v (SPRINT 7.1: must equal JWT uid)", body["user_id"])
	}
	if accountID, ok := body["account_id"].(float64); !ok || accountID != 10 {
		t.Fatalf("account_id: want 10, got %v", body["account_id"])
	}
}

func TestHandleCallback_Facebook_SavesPageAccessToken(t *testing.T) {
	const userLongLivedToken = "user-long-lived-token"
	pages := []*services.DiscoveredAccount{
		{
			Profile: models.PlatformProfile{PlatformUserID: "page-1", Username: "Page One"},
			SupplementalTokens: []*models.TokenData{
				{AccessToken: "page-token-1", TokenType: models.TokenTypePageAccess, ExpiresIn: 60 * 60 * 24 * 365 * 10, Scopes: []string{"pages_manage_posts", "pages_read_engagement", "pages_show_list"}},
			},
		},
		{
			Profile: models.PlatformProfile{PlatformUserID: "page-2", Username: "Page Two"},
			SupplementalTokens: []*models.TokenData{
				{AccessToken: "page-token-2", TokenType: models.TokenTypePageAccess, ExpiresIn: 60 * 60 * 24 * 365 * 10, Scopes: []string{"pages_manage_posts", "pages_read_engagement", "pages_show_list"}},
			},
		},
	}

	svc := &mockDiscoverableProvider{
		mockProvider: mockProvider{
			platform: "facebook",
			handleCallback: func(ctx context.Context, state, code string) (*models.PlatformProfile, *models.TokenData, error) {
				return &models.PlatformProfile{PlatformUserID: "fb-user-123", Username: "FB User"}, &models.TokenData{
					AccessToken: userLongLivedToken,
					TokenType:   models.TokenTypeLongLived,
					ExpiresIn:   5184000,
				}, nil
			},
		},
		discoverFn: func(ctx context.Context, accessToken, platformUserID string) ([]*services.DiscoveredAccount, error) {
			if accessToken != userLongLivedToken {
				t.Errorf("DiscoverAccounts accessToken: want %q, got %q", userLongLivedToken, accessToken)
			}
			return pages, nil
		},
	}

	var attachCount int
	store := &mockUserStore{
		attachFn: func(userID int64, profile *models.PlatformProfile, platform string) (*models.PlatformAccount, error) {
			attachCount++
			return &models.PlatformAccount{
				ID:             int64(10 + attachCount),
				UserID:         userID,
				Platform:       platform,
				PlatformUserID: profile.PlatformUserID,
				Username:       profile.Username,
			}, nil
		},
	}
	// Task 1/10 — atomic OAuth finalize: token-write visibility is
	// owned by the fakeChannelAuthorizer (independent audit trail
	// in tokenWrites). The vault mock is no longer in this code
	// path's call chain so we don't even need WithCredentialVault
	// override for the cipher count.
	authorizer := &fakeChannelAuthorizer{}
	r := newTestRouter(svc, store, "", WithChannelAuthorizer(authorizer))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/facebook/callback?code=abc&state=test-state", nil)
	setOAuthStateCookieForTest(req, "facebook", "test-state")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}

	// Expect 4 token writes: page token + user token for each of the 2 pages.
	// The atomic AuthorizeChannel call records both principal
	// (user long-lived) AND supplemental (page access) tokens in the
	// SAME call — same surface contract as the legacy non-atomic
	// path that issued two separate r.vault.Save calls per page.
	if authorizer.tokenWriteCount() != 4 {
		t.Fatalf("want 4 token writes (2 page + 2 user), got %d: %+v", authorizer.tokenWriteCount(), authorizer.tokenWrites)
	}
	// Build a map keyed by (accountID, tokenType) to avoid relying on save order.
	writtenByType := make(map[int64]map[string]string)
	authorizer.mu.Lock()
	for _, w := range authorizer.tokenWrites {
		if writtenByType[w.AccountID] == nil {
			writtenByType[w.AccountID] = make(map[string]string)
		}
		writtenByType[w.AccountID][w.TokenType] = w.AccessToken
	}
	authorizer.mu.Unlock()
	for _, p := range pages {
		// The account IDs are generated by attachFn as 10, 11, ...
		// SupplementalTokens carry the page token — find by matching
		// the AccessToken from SupplementalTokens[0].
		var foundID int64
		expectedPageToken := p.SupplementalTokens[0].AccessToken
		for id, tokens := range writtenByType {
			if tokens[models.TokenTypePageAccess] == expectedPageToken {
				foundID = id
				break
			}
		}
		if foundID == 0 {
			t.Fatalf("missing page token save for page %s", p.Profile.PlatformUserID)
		}
		if writtenByType[foundID][models.TokenTypePageAccess] != expectedPageToken {
			t.Errorf("page %s: want page token %q, got %q", p.Profile.PlatformUserID, expectedPageToken, writtenByType[foundID][models.TokenTypePageAccess])
		}
		if writtenByType[foundID][models.TokenTypeLongLived] != userLongLivedToken {
			t.Errorf("page %s: want user token %q, got %q", p.Profile.PlatformUserID, userLongLivedToken, writtenByType[foundID][models.TokenTypeLongLived])
		}
	}
}

func TestHandleCallback_YouTube_OneChannel_OneSave(t *testing.T) {
	const bearerToken = "yt-bearer-token-1"
	channels := []*services.DiscoveredAccount{
		{Profile: models.PlatformProfile{PlatformUserID: "UCsoloChannel", Username: "Solo Channel"}},
	}
	svc := &mockDiscoverableProvider{
		mockProvider: mockProvider{
			platform: "youtube",
			handleCallback: func(ctx context.Context, state, code string) (*models.PlatformProfile, *models.TokenData, error) {
				return &models.PlatformProfile{PlatformUserID: "g-acc-1", Username: "G Acc"}, &models.TokenData{
					AccessToken: bearerToken, TokenType: models.TokenTypeBearer, ExpiresIn: 3600,
				}, nil
			},
		},
		discoverFn: func(ctx context.Context, accessToken, platformUserID string) ([]*services.DiscoveredAccount, error) {
			return channels, nil
		},
	}
	type saveCall struct {
		accountID int64
		token     string
	}
	store := &mockUserStore{
		attachFn: func(userID int64, profile *models.PlatformProfile, platform string) (*models.PlatformAccount, error) {
			return &models.PlatformAccount{
				ID: 10, UserID: userID, Platform: platform,
				PlatformUserID: profile.PlatformUserID, Username: profile.Username,
			}, nil
		},
	}
	// Task 1/10 — atomically via r.authorizer.AuthorizeChannel.
	// tokenWrites is the independent audit trail in the fake.
	authorizer := &fakeChannelAuthorizer{}
	r := newTestRouter(svc, store, "", WithChannelAuthorizer(authorizer))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/youtube/callback?code=abc&state=test-state", nil)
	setOAuthStateCookieForTest(req, "youtube", "test-state")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if authorizer.tokenWriteCount() != 1 {
		t.Fatalf("tokenWrites must be exactly 1 (single channel, atomic), got %d: %+v", authorizer.tokenWriteCount(), authorizer.tokenWrites)
	}
	w0 := authorizer.tokenWrites[0]
	if w0.AccountID != 10 || w0.AccessToken != bearerToken {
		t.Errorf("tokenWrites[0]: want (accountID=10, access=%q), got %+v", bearerToken, w0)
	}
}

func TestHandleCallback_YouTube_MultipleChannels_NoExpected_Conflict(t *testing.T) {
	channels := []*services.DiscoveredAccount{
		{Profile: models.PlatformProfile{PlatformUserID: "UCaaaaaaaaaaaaaaaaaaaaa1", Username: "Channel A"}},
		{Profile: models.PlatformProfile{PlatformUserID: "UCaaaaaaaaaaaaaaaaaaaaa2", Username: "Channel B"}},
	}
	svc := &mockDiscoverableProvider{
		mockProvider: mockProvider{
			platform: "youtube",
			handleCallback: func(ctx context.Context, state, code string) (*models.PlatformProfile, *models.TokenData, error) {
				return &models.PlatformProfile{PlatformUserID: "g-acc", Username: "G"}, &models.TokenData{
					AccessToken: "bearer", TokenType: models.TokenTypeBearer, ExpiresIn: 3600,
				}, nil
			},
		},
		discoverFn: func(ctx context.Context, accessToken, platformUserID string) ([]*services.DiscoveredAccount, error) {
			return channels, nil
		},
	}
	authorizer := &fakeChannelAuthorizer{}
	store := &mockUserStore{
		attachFn: func(userID int64, profile *models.PlatformProfile, platform string) (*models.PlatformAccount, error) {
			// attachFn must NOT be called when discovery is ambiguous —
			// if it is, the bug is back.
			return &models.PlatformAccount{
				ID: 10, UserID: userID, Platform: platform,
				PlatformUserID: profile.PlatformUserID,
			}, nil
		},
	}
	r := newTestRouter(svc, store, "", WithChannelAuthorizer(authorizer))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/youtube/callback?code=abc&state=test-state", nil)
	setOAuthStateCookieForTest(req, "youtube", "test-state")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("ambiguous grant: want 409 Conflict, got %d: %s", w.Code, w.Body.String())
	}
	if authorizer.tokenWriteCount() != 0 {
		t.Fatalf("ambiguous grant must NOT write tokens on ANY channel; got %d write(s)", authorizer.tokenWriteCount())
	}
	if authorizer.authorizeCalls.Load() != 0 {
		t.Fatalf("ambiguous grant must NOT invoke AuthorizeChannel at all (channels.list guard rejects pre-tx); got %d call(s)", authorizer.authorizeCalls.Load())
	}
	if !strings.Contains(w.Body.String(), "ambiguous") {
		t.Errorf("response body should explain the ambiguity, got %q", w.Body.String())
	}
}

func TestHandleCallback_YouTube_MultipleChannels_ExpectedMatches_OneSave(t *testing.T) {
	const expectedID = "UCaaaaaaaaaaaaaaaaaaaaa2"
	channels := []*services.DiscoveredAccount{
		{Profile: models.PlatformProfile{PlatformUserID: "UCaaaaaaaaaaaaaaaaaaaaa1", Username: "Channel A"}},
		{Profile: models.PlatformProfile{PlatformUserID: expectedID, Username: "Channel B"}},
		{Profile: models.PlatformProfile{PlatformUserID: "UCaaaaaaaaaaaaaaaaaaaaa3", Username: "Channel C"}},
	}
	svc := &mockDiscoverableProvider{
		mockProvider: mockProvider{
			platform: "youtube",
			handleCallback: func(ctx context.Context, state, code string) (*models.PlatformProfile, *models.TokenData, error) {
				return &models.PlatformProfile{PlatformUserID: "g-acc", Username: "G"}, &models.TokenData{
					AccessToken: "yt-bearer", TokenType: models.TokenTypeBearer, ExpiresIn: 3600,
				}, nil
			},
		},
		discoverFn: func(ctx context.Context, accessToken, platformUserID string) ([]*services.DiscoveredAccount, error) {
			return channels, nil
		},
	}
	// Fixed account-ID <-> channel-ID mapping so vault.Save can be
	// reverse-traced to the channel it was attached to.
	accountIDsByChannel := map[string]int64{
		"UCaaaaaaaaaaaaaaaaaaaaa1": 101,
		expectedID:                 102,
		"UCaaaaaaaaaaaaaaaaaaaaa3": 103,
	}
	attachedChannels := []string{}
	store := &mockUserStore{
		attachFn: func(userID int64, profile *models.PlatformProfile, platform string) (*models.PlatformAccount, error) {
			id, ok := accountIDsByChannel[profile.PlatformUserID]
			if !ok {
				return nil, fmt.Errorf("unexpected channel %q in attachFn", profile.PlatformUserID)
			}
			attachedChannels = append(attachedChannels, profile.PlatformUserID)
			return &models.PlatformAccount{
				ID: id, UserID: userID, Platform: platform,
				PlatformUserID: profile.PlatformUserID, Username: profile.Username,
			}, nil
		},
	}
	authorizer := &fakeChannelAuthorizer{}
	r := newTestRouter(svc, store, "", WithChannelAuthorizer(authorizer))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/youtube/callback?code=abc&state=test-state", nil)
	setOAuthStateCookieForTest(req, "youtube", "test-state")
	setOAuthExpectedChannelCookieForTest(req, "youtube", "test-state", expectedID)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if len(attachedChannels) != 1 {
		t.Fatalf("attachFn must be called exactly once (only expected channel); got %d calls for channels %v", len(attachedChannels), attachedChannels)
	}
	if attachedChannels[0] != expectedID {
		t.Errorf("attachFn must target expected channel %q; got %q", expectedID, attachedChannels[0])
	}
	if authorizer.tokenWriteCount() != 1 {
		t.Fatalf("tokenWrites must be exactly once; got %d: %+v", authorizer.tokenWriteCount(), authorizer.tokenWrites)
	}
	w0 := authorizer.tokenWrites[0]
	if w0.AccountID != accountIDsByChannel[expectedID] {
		t.Errorf("tokenWrites[0] accountID: want %d (channel %q), got %d", accountIDsByChannel[expectedID], expectedID, w0.AccountID)
	}
	if w0.AccessToken != "yt-bearer" {
		t.Errorf("tokenWrites[0] access: want yt-bearer, got %q", w0.AccessToken)
	}
}

func TestHandleCallback_YouTube_ExpectedNoMatch_Conflict(t *testing.T) {
	channels := []*services.DiscoveredAccount{
		{Profile: models.PlatformProfile{PlatformUserID: "UCaaaaaaaaaaaaaaaaaaaaa1", Username: "Channel A"}},
	}
	svc := &mockDiscoverableProvider{
		mockProvider: mockProvider{
			platform: "youtube",
			handleCallback: func(ctx context.Context, state, code string) (*models.PlatformProfile, *models.TokenData, error) {
				return &models.PlatformProfile{PlatformUserID: "g-acc", Username: "G"}, &models.TokenData{
					AccessToken: "bearer", TokenType: models.TokenTypeBearer, ExpiresIn: 3600,
				}, nil
			},
		},
		discoverFn: func(ctx context.Context, accessToken, platformUserID string) ([]*services.DiscoveredAccount, error) {
			return channels, nil
		},
	}
	authorizer := &fakeChannelAuthorizer{}
	store := &mockUserStore{
		attachFn: func(userID int64, profile *models.PlatformProfile, platform string) (*models.PlatformAccount, error) {
			return &models.PlatformAccount{ID: 10, UserID: userID, Platform: platform, PlatformUserID: profile.PlatformUserID}, nil
		},
	}
	r := newTestRouter(svc, store, "", WithChannelAuthorizer(authorizer))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/youtube/callback?code=abc&state=test-state", nil)
	setOAuthStateCookieForTest(req, "youtube", "test-state")
	setOAuthExpectedChannelCookieForTest(req, "youtube", "test-state", "UCaaaaaaaaaaaaaaaaaaaaaZ")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("mismatched expected: want 409, got %d: %s", w.Code, w.Body.String())
	}
	if authorizer.tokenWriteCount() != 0 {
		t.Fatalf("mismatch must NOT write tokens; got %d write(s)", authorizer.tokenWriteCount())
	}
	if authorizer.authorizeCalls.Load() != 0 {
		t.Fatalf("mismatch must NOT invoke AuthorizeChannel (channels.list guard rejects pre-tx); got %d call(s)", authorizer.authorizeCalls.Load())
	}
	if !strings.Contains(w.Body.String(), "does not match expected channel") {
		t.Errorf("response body should reference the mismatch, got %q", w.Body.String())
	}
}

func TestHandleCallback_Success_FrontendRedirect(t *testing.T) {
	svc := &mockProvider{
		platform:       "instagram",
		handleCallback: successCallback,
	}
	store := &mockUserStore{
		attachFn: successAttach,
	}
	r := newTestRouter(svc, store, "https://app.example.com")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/instagram/callback?code=abc&state=test-state", nil)
	setOAuthStateCookieForTest(req, "instagram", "test-state")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("want 302, got %d: %s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	// SPRINT 7.1 (P0#14): the redirect target is the SPA's connections
	// page with provider + status=connected query params — no one-time
	// code, no JWT. The session cookie that validated at the top of
	// the handler IS the active session.
	if !strings.Contains(loc, "https://app.example.com/app/linking?") {
		t.Fatalf("redirect URL must land on /app/linking (SPRINT 7.1): %s", loc)
	}
	if strings.Contains(loc, "jwt=") {
		t.Fatalf("JWT must never appear in the redirect URL: %s", loc)
	}
	if strings.Contains(loc, "code=") {
		t.Fatalf("one-time code must NOT appear in the OAuth callback redirect (SPRINT 7.1): %s", loc)
	}
	if !strings.Contains(loc, "provider=instagram") {
		t.Fatalf("expected provider=instagram in redirect params: %s", loc)
	}
	if !strings.Contains(loc, "status=connected") {
		t.Fatalf("expected status=connected in redirect params: %s", loc)
	}
}
