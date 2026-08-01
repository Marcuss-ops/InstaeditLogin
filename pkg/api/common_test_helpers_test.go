package api

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

const testJWTSecret = "test-jwt-secret-must-be-long-enough-for-hs256"

func withBearerJWT(t *testing.T, req *http.Request, userID int64) {
	t.Helper()
	req.Header.Set("Authorization", "Bearer "+issueTestJWT(t, userID))
}

// newTestRouter builds a Router wired with a mock provider and store.
//
// Taglio 2.2: the Router takes a CapabilityRouter (per-capability lookups)
// plus a CredentialVault (via WithCredentialVault). The default vault
// is a no-op mock that succeeds on save/revoke and errors on get/renew —
// that is what most tests (login, callback happy path, workspace, post
// CRUD) want. Tests that exercise the publish path or want to force a
// save/renew error override via WithCredentialVault(&mockCredentialVault{...})
// in opts.
// mustNewRouterWithDefaults wraps MustNewRouter and supplies the
// required dependencies that production wiring always injects.
// Use it in tests that previously called MustNewRouter directly.
func mustNewRouterWithDefaults(
	capabilities *services.CapabilityRouter,
	userRepo UserStore,
	authManager *auth.Manager,
	frontendURL string,
	allowedOrigins []string,
	opts ...RouterOption,
) *Router {
	return MustNewRouter(
		capabilities,
		userRepo,
		authManager,
		frontendURL,
		allowedOrigins,
		append([]RouterOption{
			WithOneTimeCodeStore(NewInMemoryOneTimeCodeStore(60 * time.Second)),
			WithCredentialVault(&mockCredentialVault{}),
			WithIdempotencyStore(newMockIdempotencyStore()),
			WithConnectLinkNonceStore(newFakeConnectLinkNonceStore()),
			WithChannelAuthorizer(&fakeChannelAuthorizer{}),
		}, opts...)...,
	)
}

func newTestRouter(
	platformSvc services.NameProvider,
	store *mockUserStore,
	frontendURL string,
	opts ...RouterOption,
) *Router {
	capRouter := services.NewCapabilityRouter()
	capRouter.Register(platformSvc.Name(), platformSvc)
	capRouter.Register("instagram", platformSvc)
	capRouter.Register("tiktok", platformSvc)
	capRouter.Register("twitter", platformSvc)
	otc := NewInMemoryOneTimeCodeStore(60 * time.Second)
	idemStore := newMockIdempotencyStore()
	connectLinkNonceStore := newFakeConnectLinkNonceStore()
	// Note: the sweeper goroutine leaks until the test binary exits —
	// acceptable for unit tests; the 1s ticker has no observable effect
	// on test behaviour and the OS reclaims everything on process exit.
	defaultVault := &mockCredentialVault{}
	return MustNewRouter(
		capRouter,
		store,
		auth.NewManager(testJWTSecret, 24),
		frontendURL,
		nil,
		append([]RouterOption{
			WithOneTimeCodeStore(otc),
			WithCredentialVault(defaultVault),
			WithIdempotencyStore(idemStore),
			WithConnectLinkNonceStore(connectLinkNonceStore),
			// Task 1/10 — atomic OAuth finalize. newTestRouter
			// wires a default fakeChannelAuthorizer that
			// independently records every token write in
			// tokenWrites. Tests assert len(tokenWrites) for
			// the cipher-write count semantic and override
			// the canonical seam via WithChannelAuthorizer
			// only when they need specific failure injection
			// (e.g. TestHandleCallback_AuthorizeChannelError).
			WithChannelAuthorizer(&fakeChannelAuthorizer{}),
		}, opts...)...,
	)
}

// issueTestJWT mints a JWT carrying (userID, workspaceID=1, sessionID=1).
// SPRINT 7.1 couples /auth/{provider}/* to a session-gating middleware
// (oauthSessionRedirect) that calls Manager.Verify on the Authorization
// header or session cookie. Manager.Verify rejects any token with
// UserID<=0 || WorkspaceID<=0 || SessionID<=0, so the legacy
// `Issue(userID)` path (which signs with wsID=0, sessionID=0) no longer
// produces an acceptable token. IssueAccess requires all three IDs
// positive; tests that previously relied on Issue(userID) implicitly
// expected the OAuth layer to ignore the Authorization header — that
// assumption no longer holds.
func issueTestJWT(t *testing.T, userID int64) string {
	t.Helper()
	authMgr := auth.NewManager(testJWTSecret, 24)
	tok, _, _, err := authMgr.IssueAccess(userID, 1, 1)
	if err != nil {
		t.Fatalf("issue access jwt (user=%d, ws=1, session=1): %v", userID, err)
	}
	return tok
}

var successCallback = func(ctx context.Context, state, code string) (*models.PlatformProfile, *models.TokenData, error) {
	return &models.PlatformProfile{
			PlatformUserID: "pf-123",
			Username:       "testuser",
			Name:           "Test User",
			Email:          "test@example.com",
		}, &models.TokenData{
			AccessToken: "at-secret",
			TokenType:   "bearer",
			ExpiresIn:   3600,
		}, nil
}

// successAttach models the SPRINT 7.1 connect path: the JWT's user_id
// (1) is the linkage target, never a freshly-allocated id from a
// FindOrCreateUserByPlatform query.
var successAttach = func(userID int64, profile *models.PlatformProfile, platform string) (*models.PlatformAccount, error) {
	return &models.PlatformAccount{
		ID:             10,
		UserID:         userID,
		Platform:       platform,
		PlatformUserID: profile.PlatformUserID,
		Username:       profile.Username,
	}, nil
}

func setOAuthStateCookieForTest(req *http.Request, provider, state string) {
	req.AddCookie(&http.Cookie{
		Name:     OAuthStateCookieName(provider),
		Value:    state,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

// setOAuthExpectedChannelCookieForTest mirrors setOAuthStateCookieForTest
// for the sibling oauth_state_{provider}_expected_channel cookie used by
// the YouTube P0 fix to round-trip ?expected_channel_id=UC... across
// the OAuth callback. The cookie value is "<state>:<channelID>" — the
// state nonce prefix binds the channel hint to the SAME flow so a
// stale sibling cookie from a previous OAuth round-trip cannot leak
// into a new one (the production code in handlers.go enforces this
// prefix check; this helper just mirrors the production format for
// tests).
func setOAuthExpectedChannelCookieForTest(req *http.Request, provider, state, channelID string) {
	req.AddCookie(&http.Cookie{
		Name:     OAuthStateExpectedChannelCookieName(provider),
		Value:    state + ":" + channelID,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

// ---------------------------------------------------------------------------
// CORS middleware tests
// ---------------------------------------------------------------------------

func newCORSTestRouter(allowedOrigins []string) *Router {
	return mustNewRouterWithDefaults(
		services.NewCapabilityRouter(),
		&mockUserStore{},
		auth.NewManager(testJWTSecret, 24),
		"",
		allowedOrigins,
	)
}

// twoAccountFixtures returns two synthetic accounts the list test
// uses as fixtures. The shape is exactly what ListPlatformAccountsByUser
// returns from the repo (subset of the full fixture model).
func twoAccountFixtures() []*models.PlatformAccount {
	t0 := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
	t1 := time.Date(2024, 7, 15, 9, 30, 0, 0, time.UTC)
	return []*models.PlatformAccount{
		{
			ID: 21, UserID: 1, Platform: "instagram",
			PlatformUserID: "1784deadbeef", Username: "alice_ig",
			Status: models.AccountStatusActive, CreatedAt: t0, UpdatedAt: t0,
		},
		{
			ID: 22, UserID: 1, Platform: "facebook",
			PlatformUserID: "1029384cafebabe", Username: "alice.fb.page",
			Status: models.AccountStatusActive, CreatedAt: t1, UpdatedAt: t1,
		},
	}
}

// ownedAccountFixture returns a synthetic account owned by ownerID —
// the template for the 4 happy-path tests below.
func ownedAccountFixture(ownerID int64, platform string) *models.PlatformAccount {
	now := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
	return &models.PlatformAccount{
		ID: 21, UserID: ownerID, Platform: platform,
		PlatformUserID: "pf-21", Username: "alice_" + platform,
		Status:    models.AccountStatusActive,
		CreatedAt: now, UpdatedAt: now,
	}
}

// validTokenFuture returns a non-nil OAuthToken that the mock vault
// hands back for "token is valid" cases in handleValidateAccount tests.
func validTokenFuture() *models.OAuthToken {
	exp := time.Now().Add(time.Hour)
	return &models.OAuthToken{
		AccessToken: "valid-token",
		TokenType:   models.TokenTypeShortLived,
		ExpiresAt:   &exp,
	}
}
