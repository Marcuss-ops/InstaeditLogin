package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

const consentTestChannel = "UCabcdefghijklmnopqrstuv"

// newConsentCaptureRouter wires a YouTube mock provider whose login
// URL builder records the OAuthLoginOptions, plus the given user
// store, so the R7 consent decision can be asserted. No pool registry
// is wired unless the caller passes WithYouTubeOAuthClientRegistry.
func newConsentCaptureRouter(t *testing.T, store *mockUserStore, opts ...RouterOption) (*mockProvider, *Router, *services.OAuthLoginOptions) {
	t.Helper()
	var got services.OAuthLoginOptions
	svc := &mockProvider{
		platform: "youtube",
		loginURL: "https://auth.youtube.com/oauth",
		loginWithOptionsFn: func(state string, options services.OAuthLoginOptions) string {
			got = options
			return "https://auth.youtube.com/oauth?state=" + state
		},
	}
	r := newTestRouter(svc, store, "", opts...)
	return svc, r, &got
}

func doYouTubeLogin(t *testing.T, r *Router, query string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/youtube/login?"+query, nil)
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusFound {
		t.Fatalf("want 302, got %d: %s", w.Code, w.Body.String())
	}
	return w
}

// healthyYouTubeFixture returns an active YouTube account for
// consentTestChannel whose grant is active, carries the force-ssl
// scope and was issued by youtube_pool_b. grantFn lets a test mutate
// the grant (missing scope, error, …); nil returns the healthy grant.
func healthyYouTubeFixture(grantFn func(*models.OAuthConnection) error) *mockUserStore {
	connID := int64(77)
	account := &models.PlatformAccount{
		ID: 10, UserID: 1, Platform: models.PlatformYouTube,
		PlatformUserID: consentTestChannel, Status: models.AccountStatusActive,
		OAuthConnectionID: &connID,
	}
	grant := &models.OAuthConnection{
		ID:             connID,
		Status:         models.AccountStatusActive,
		OAuthClientKey: "youtube_pool_b",
		GrantedScopes:  []string{services.YouTubeForceSSLScope},
	}
	return &mockUserStore{
		listFn: func(userID int64, platform string) ([]*models.PlatformAccount, error) {
			return []*models.PlatformAccount{account}, nil
		},
		findOAuthConnectionFn: func(ctx context.Context, id int64) (*models.OAuthConnection, error) {
			if grantFn != nil {
				if err := grantFn(grant); err != nil {
					return nil, err
				}
			}
			return grant, nil
		},
	}
}

// TestHandleLogin_YouTube_HealthyAccount_SkipsConsent is the R7 core:
// a channel-pinned reconnect whose account + grant are healthy must
// NOT force prompt=consent — prompt=select_account reuses Google's
// cached grant and returns the SAME refresh token (no new token
// burned against the 100-per-client cap).
func TestHandleLogin_YouTube_HealthyAccount_SkipsConsent(t *testing.T) {
	_, r, got := newConsentCaptureRouter(t, healthyYouTubeFixture(nil))
	doYouTubeLogin(t, r, "expected_channel_id="+consentTestChannel)

	if got.SelectAccount != true {
		t.Errorf("SelectAccount: want true (channel-pinned reconnect always asks for the account picker), got %v", got.SelectAccount)
	}
	if got.ForceConsent {
		t.Error("ForceConsent: want false for a healthy active grant (select_account suffices; Google reuses the cached grant)")
	}
}

// TestHandleLogin_YouTube_ReauthRequired_ForcesConsent pins the
// "grant reauth_required" consent-necessary case: an account in
// reauth_required must re-show the consent screen.
func TestHandleLogin_YouTube_ReauthRequired_ForcesConsent(t *testing.T) {
	store := healthyYouTubeFixture(nil)
	connID := int64(77)
	store.listFn = func(userID int64, platform string) ([]*models.PlatformAccount, error) {
		return []*models.PlatformAccount{{
			ID: 10, UserID: 1, Platform: models.PlatformYouTube,
			PlatformUserID: consentTestChannel, Status: models.AccountStatusReauthRequired,
			OAuthConnectionID: &connID,
		}}, nil
	}
	_, r, got := newConsentCaptureRouter(t, store)
	doYouTubeLogin(t, r, "expected_channel_id="+consentTestChannel)

	if !got.ForceConsent {
		t.Error("ForceConsent: want true for an account in reauth_required")
	}
	if !got.SelectAccount {
		t.Error("SelectAccount: want true (channel-pinned reconnect always asks for the account picker)")
	}
}

// TestHandleLogin_YouTube_UnknownChannel_ForcesConsent pins the
// "brand-new grant" consent-necessary case: no account row exists for
// the pinned channel, so Google must re-show consent to force the
// Brand-Account selection (a cached consent from ANOTHER channel would
// otherwise skip it and bind the grant to the wrong channel).
func TestHandleLogin_YouTube_UnknownChannel_ForcesConsent(t *testing.T) {
	store := &mockUserStore{
		listFn: func(userID int64, platform string) ([]*models.PlatformAccount, error) {
			return nil, nil
		},
	}
	_, r, got := newConsentCaptureRouter(t, store)
	doYouTubeLogin(t, r, "expected_channel_id="+consentTestChannel)

	if !got.ForceConsent {
		t.Error("ForceConsent: want true for a channel with no account row (brand-new grant)")
	}
}

// TestHandleLogin_YouTube_MissingScope_ForcesConsent pins the
// "scope mancanti" consent-necessary case: an active account whose
// grant lacks the force-ssl scope must re-show consent, otherwise
// Google reuses the cached approval and never grants the missing
// scope.
func TestHandleLogin_YouTube_MissingScope_ForcesConsent(t *testing.T) {
	store := healthyYouTubeFixture(func(g *models.OAuthConnection) error {
		g.GrantedScopes = []string{"https://www.googleapis.com/auth/youtube.upload"}
		return nil
	})
	_, r, got := newConsentCaptureRouter(t, store)
	doYouTubeLogin(t, r, "expected_channel_id="+consentTestChannel)

	if !got.ForceConsent {
		t.Error("ForceConsent: want true when the grant lacks the force-ssl scope")
	}
}

// TestHandleLogin_YouTube_GrantLookupFailure_ForcesConsent pins the
// fail-towards-consent posture: when the grant cannot be verified
// (store error), the handler must NOT skip consent.
func TestHandleLogin_YouTube_GrantLookupFailure_ForcesConsent(t *testing.T) {
	store := healthyYouTubeFixture(func(g *models.OAuthConnection) error {
		return context.DeadlineExceeded
	})
	_, r, got := newConsentCaptureRouter(t, store)
	doYouTubeLogin(t, r, "expected_channel_id="+consentTestChannel)

	if !got.ForceConsent {
		t.Error("ForceConsent: want true when the grant lookup fails (cannot verify health)")
	}
}

// TestHandleLogin_YouTubePool_HealthyReconnect_ReusesConnectionClient
// is the pool-path R7 companion: a healthy reconnect must build the
// consent URL against the pool client that ISSUED the existing grant
// (Resolve on the grant's oauth_client_key), not SelectForNewConnection
// — reusing the client makes Google return the SAME refresh token,
// while a different client would mint a new grant and orphan the
// stored one. It also asserts the consent reduction applies on the
// pool path too.
func TestHandleLogin_YouTubePool_HealthyReconnect_ReusesConnectionClient(t *testing.T) {
	poolProvider := &mockPoolProvider{mockProvider: mockProvider{platform: "youtube"}}
	store := healthyYouTubeFixture(nil) // grant issued by youtube_pool_b
	nonceStore := newFakeConnectLinkNonceStore()
	r := newTestRouter(poolProvider, store, "",
		WithConnectLinkNonceStore(nonceStore),
		WithYouTubeOAuthClientRegistry(newTestPoolRegistry(t)),
	)

	doYouTubeLogin(t, r, "expected_channel_id="+consentTestChannel)

	if poolProvider.poolLoginClient == nil || poolProvider.poolLoginClient.Key != "youtube_pool_b" {
		t.Fatalf("healthy reconnect must reuse the grant's pool client youtube_pool_b, got %+v", poolProvider.poolLoginClient)
	}
	if poolProvider.poolLoginOptions.SelectAccount != true {
		t.Errorf("SelectAccount: want true, got %v", poolProvider.poolLoginOptions.SelectAccount)
	}
	if poolProvider.poolLoginOptions.ForceConsent {
		t.Error("ForceConsent: want false for a healthy reconnect on the pool path")
	}
}

// TestHandleLogin_YouTubePool_ReconnectKnownSubject_UsesLeastLoadedPool
// certifies the capacity-aware reconnect selection: when a channel's
// existing grant is reachable (even in a non-active state, e.g.
// reauth_required), the reconnect resolves the Google subject and
// passes it to SelectForNewConnection — so the LEAST-LOADED pool wins
// (youtube_pool_b here: 43 grants vs pool A's 48) instead of the
// deterministic youtube_pool_a first-client fallback. The consent
// stays forced (unhealthy grant → new grant must be minted).
func TestHandleLogin_YouTubePool_ReconnectKnownSubject_UsesLeastLoadedPool(t *testing.T) {
	poolProvider := &mockPoolProvider{mockProvider: mockProvider{platform: "youtube"}}
	connID := int64(77)
	store := &mockUserStore{
		listFn: func(userID int64, platform string) ([]*models.PlatformAccount, error) {
			return []*models.PlatformAccount{{
				ID: 10, UserID: 1, Platform: models.PlatformYouTube,
				PlatformUserID: consentTestChannel, Status: models.AccountStatusReauthRequired,
				OAuthConnectionID: &connID,
			}}, nil
		},
		findOAuthConnectionFn: func(ctx context.Context, id int64) (*models.OAuthConnection, error) {
			return &models.OAuthConnection{
				ID: connID, Status: models.AccountStatusActive,
				ProviderSubjectID: "google-subject-reconnect",
				OAuthClientKey:    "youtube_pool_a",
				GrantedScopes:     []string{services.YouTubeForceSSLScope},
			}, nil
		},
	}
	nonceStore := newFakeConnectLinkNonceStore()
	r := newTestRouter(poolProvider, store, "",
		WithConnectLinkNonceStore(nonceStore),
		WithYouTubeOAuthClientRegistry(newTestPoolRegistryWithUsage(t, map[string]int64{
			"youtube_pool_a": 48,
			"youtube_pool_b": 43,
		})),
	)

	doYouTubeLogin(t, r, "expected_channel_id="+consentTestChannel)

	// Least-loaded selection: pool B (43) has more headroom than pool A
	// (48) for THIS subject — the reconnect must land on B, not the
	// deterministic first client A.
	if poolProvider.poolLoginClient == nil || poolProvider.poolLoginClient.Key != "youtube_pool_b" {
		t.Fatalf("reconnect with known subject must select the least-loaded pool youtube_pool_b, got %+v", poolProvider.poolLoginClient)
	}
	if !poolProvider.poolLoginOptions.ForceConsent {
		t.Error("ForceConsent: want true (reauth_required grant must mint a new grant)")
	}
}

// TestHandleLogin_YouTubePool_ReconnectCounterError_FailsClosed pins
// the capacity manager's fail-closed contract at the router layer: when
// the reconnect resolves a known subject but the usage counter cannot
// be queried (storage error), SelectForNewConnection must ERROR — the
// login must never silently fall back to a deterministic client (a
// wrong pool guess on a storage failure would mis-balance the fleet
// while the counter is down).
func TestHandleLogin_YouTubePool_ReconnectCounterError_FailsClosed(t *testing.T) {
	poolProvider := &mockPoolProvider{mockProvider: mockProvider{platform: "youtube"}}
	connID := int64(77)
	store := &mockUserStore{
		listFn: func(userID int64, platform string) ([]*models.PlatformAccount, error) {
			return []*models.PlatformAccount{{
				ID: 10, UserID: 1, Platform: models.PlatformYouTube,
				PlatformUserID: consentTestChannel, Status: models.AccountStatusReauthRequired,
				OAuthConnectionID: &connID,
			}}, nil
		},
		findOAuthConnectionFn: func(ctx context.Context, id int64) (*models.OAuthConnection, error) {
			return &models.OAuthConnection{
				ID: connID, Status: models.AccountStatusActive,
				ProviderSubjectID: "google-subject-reconnect",
				OAuthClientKey:    "youtube_pool_a",
				GrantedScopes:     []string{services.YouTubeForceSSLScope},
			}, nil
		},
	}
	nonceStore := newFakeConnectLinkNonceStore()
	reg, err := services.NewYouTubeOAuthClientRegistry([]services.YouTubeOAuthClientConfig{
		{Key: "youtube_pool_a", ClientID: "pool-a-client-id", ClientSecret: "pool-a-client-secret-at-least-32-chars!!", RedirectURI: "https://instaedit.example.com/oauth/youtube/callback"},
		{Key: "youtube_pool_b", ClientID: "pool-b-client-id", ClientSecret: "pool-b-client-secret-at-least-32-chars!!", RedirectURI: "https://instaedit.example.com/oauth/youtube/callback"},
	}, services.WithYouTubeOAuthClientUsageCounter(&fakePoolUsageCounter{countErr: context.DeadlineExceeded}))
	if err != nil {
		t.Fatalf("NewYouTubeOAuthClientRegistry: %v", err)
	}
	r := newTestRouter(poolProvider, store, "",
		WithConnectLinkNonceStore(nonceStore),
		WithYouTubeOAuthClientRegistry(reg),
	)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/youtube/login?expected_channel_id="+consentTestChannel, nil)
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("counter error on known-subject reconnect: want 500 (fail-closed), got %d: %s", w.Code, w.Body.String())
	}
	if poolProvider.poolLoginClient != nil {
		t.Errorf("no client must be selected when the capacity counter fails; got %+v", poolProvider.poolLoginClient)
	}
}

// TestHandleLogin_YouTubePool_NewConnectionSelectsCapacityClient pins
// the complement: a channel with no healthy grant still goes through
// SelectForNewConnection (youtube_pool_a — the deterministic
// no-counter fallback) AND keeps ForceConsent.
func TestHandleLogin_YouTubePool_NewConnectionSelectsCapacityClient(t *testing.T) {
	poolProvider := &mockPoolProvider{mockProvider: mockProvider{platform: "youtube"}}
	store := &mockUserStore{
		listFn: func(userID int64, platform string) ([]*models.PlatformAccount, error) {
			return nil, nil
		},
	}
	nonceStore := newFakeConnectLinkNonceStore()
	r := newTestRouter(poolProvider, store, "",
		WithConnectLinkNonceStore(nonceStore),
		WithYouTubeOAuthClientRegistry(newTestPoolRegistry(t)),
	)

	doYouTubeLogin(t, r, "expected_channel_id="+consentTestChannel)

	if poolProvider.poolLoginClient == nil || poolProvider.poolLoginClient.Key != "youtube_pool_a" {
		t.Fatalf("new connection must select via SelectForNewConnection (youtube_pool_a), got %+v", poolProvider.poolLoginClient)
	}
	if !poolProvider.poolLoginOptions.ForceConsent {
		t.Error("ForceConsent: want true for a channel with no healthy grant")
	}
}
