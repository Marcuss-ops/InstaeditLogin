package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/config"
	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// TestHandleValidateAccount_ActiveToken verifies the happy path: a
// valid short-lived token ⇒ 200 + status='active' + last_validated_at
// stamped on the row. The handler UPDATE must be issued (UpdatePlatformAccount
// is the persistence call we observe via the mock's updatePlatformAccountFn).
func TestHandleValidateAccount_ActiveToken(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	owner := ownedAccountFixture(1, "instagram")

	var updatedAccount *models.PlatformAccount
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return owner, nil
		},
		updatePlatformAccountFn: func(a *models.PlatformAccount) error {
			updatedAccount = a
			return nil
		},
	}
	vault := &mockCredentialVault{
		getFn: func(ctx context.Context, accountID int64, tokenType string) (*models.OAuthToken, error) {
			return validTokenFuture(), nil
		},
	}
	r := newTestRouter(svc, store, "", WithCredentialVault(vault))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/21/validate", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200 (active token), got %d: %s", w.Code, w.Body.String())
	}
	if updatedAccount == nil {
		t.Fatal("UpdatePlatformAccount was NOT called — last_validated_at not stamped")
	}
	if updatedAccount.Status != models.AccountStatusActive {
		t.Errorf("status: want active, got %s", updatedAccount.Status)
	}
	if updatedAccount.LastValidatedAt == nil || updatedAccount.LastValidatedAt.IsZero() {
		t.Errorf("last_validated_at was NOT stamped (status check passed but freshness row not updated)")
	}
}

// TestHandleValidateAccount_ExpiredToken verifies the expired path:
// vault returns "token expired at ..." ⇒ status='expired' on the
// UPDATE. The handler always returns 200 (validation IS the answer;
// caller reads status to react).
func TestHandleValidateAccount_ExpiredToken(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	owner := ownedAccountFixture(1, "instagram")

	var updatedAccount *models.PlatformAccount
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return owner, nil
		},
		updatePlatformAccountFn: func(a *models.PlatformAccount) error {
			updatedAccount = a
			return nil
		},
	}
	vault := &mockCredentialVault{
		getFn: func(ctx context.Context, accountID int64, tokenType string) (*models.OAuthToken, error) {
			return nil, fmt.Errorf("vault: token expired at 2020-01-01T00:00:00Z")
		},
	}
	r := newTestRouter(svc, store, "", WithCredentialVault(vault))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/21/validate", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200 (validation IS the answer; caller reads status), got %d: %s", w.Code, w.Body.String())
	}
	if updatedAccount.Status != models.AccountStatusExpired {
		t.Errorf("status: want expired, got %s", updatedAccount.Status)
	}
}

// TestHandleValidateAccount_ReauthRequired covers the fall-through case:
// vault returns a non-expiry error (DB error, decrypt failure) for both
// token types ⇒ status='reauth_required'. Proves the handler does
// NOT silently mark the row 'active' on a vault error path.
func TestHandleValidateAccount_ReauthRequired(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	owner := ownedAccountFixture(1, "instagram")

	var updatedAccount *models.PlatformAccount
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return owner, nil
		},
		updatePlatformAccountFn: func(a *models.PlatformAccount) error {
			updatedAccount = a
			return nil
		},
	}
	// Default mock returns "Get not implemented" (no expiry keyword).
	r := newTestRouter(svc, store, "")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/21/validate", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if updatedAccount.Status != models.AccountStatusReauthRequired {
		t.Errorf("status: want reauth_required (vault 'not implemented' is neither valid nor 'expired'), got %s", updatedAccount.Status)
	}
}

// TestHandleValidateAccount_CrossTenant_404: the ownership check MUST
// fire FIRST. vault.Get must NEVER be called for an account owned by
// another user.
func TestHandleValidateAccount_CrossTenant_404(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	crossTenant := ownedAccountFixture(999, "instagram")
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return crossTenant, nil
		},
		updatePlatformAccountFn: func(a *models.PlatformAccount) error {
			t.Errorf("UpdatePlatformAccount MUST NOT be called for cross-tenant Validate; got status=%s", a.Status)
			return nil
		},
	}
	vault := &mockCredentialVault{
		getFn: func(ctx context.Context, accountID int64, tokenType string) (*models.OAuthToken, error) {
			t.Errorf("vault.Get MUST NOT be called for cross-tenant Validate (data leak risk); got accountID=%d tokenType=%s", accountID, tokenType)
			return validTokenFuture(), nil
		},
	}
	r := newTestRouter(svc, store, "", WithCredentialVault(vault))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/21/validate", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant Validate: want 404 (NOT 200, NOT 403), got %d: %s", w.Code, w.Body.String())
	}
}

// TestHandleValidateAccount_YouTube_MissingForceSSL_ReauthRequired pins
// the P0 force-ssl check in the 4-step YouTube pipeline: a grant with
// upload+readonly but WITHOUT youtube.force-ssl must be rejected with
// 422 + status='reauth_required' — it would pass the old check but then
// fail on thumbnails.set, videos.update, metadata writes and livestream.
//
// The test uses a real YouTubeOAuthService with a transport that rewrites
// all requests to a local test server returning the no-force-ssl scope.
func TestHandleValidateAccount_YouTube_MissingForceSSL_ReauthRequired(t *testing.T) {
	account := &models.PlatformAccount{
		ID:             42,
		UserID:         1,
		Platform:       models.PlatformYouTube,
		PlatformUserID: "UC123",
		Username:       "testchannel",
		Status:         models.AccountStatusActive,
	}
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return account, nil
		},
		markReauthRequiredFn: func(ctx context.Context, accountID int64, code, message string) error {
			return nil
		},
		updatePlatformAccountFn: func(a *models.PlatformAccount) error {
			return nil
		},
	}
	vault := &mockCredentialVault{
		renewFn: func(ctx context.Context, accountID int64, tokenType string, refresh credentials.TokenRefresher) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "fresh-token", TokenType: tokenType}, nil
		},
	}

	// Server returns tokeninfo with upload+readonly but NO force-ssl.
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"aud":"test-youtube-client-id","azp":"test-youtube-client-id","scope":"https://www.googleapis.com/auth/youtube.upload https://www.googleapis.com/auth/youtube.readonly openid email profile","expires_in":600}`)
	}))
	defer tokenSrv.Close()

	tokenSrvURL, _ := url.Parse(tokenSrv.URL)
	cfg := &config.Config{
		Auth: config.AuthConfig{
			YouTubeClientID:     "test-youtube-client-id",
			YouTubeClientSecret: "secret-01234567890123456789012345678901",
			YouTubeRedirectURI:  "http://localhost/callback",
		},
	}
	realSvc, svcErr := services.NewYouTubeOAuthService(cfg, services.ProviderDependencies{
		HTTPClient: &http.Client{Transport: &urlRewriter{base: tokenSrvURL}},
	})
	if svcErr != nil {
		t.Fatalf("NewYouTubeOAuthService: %v", svcErr)
	}

	r := mustNewRouterWithDefaults(
		services.NewCapabilityRouter(),
		store,
		auth.NewManager(testJWTSecret, 24),
		"",
		nil,
		WithYouTubeService(realSvc),
		WithCredentialVault(vault),
	)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/42/validate", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422 (reauth_required) when force-ssl scope is missing, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "tokeninfo_scope_missing") {
		t.Errorf("response must indicate tokeninfo_scope_missing, got: %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "HasForceSSL=false") {
		t.Errorf("response must mention HasForceSSL=false, got: %s", w.Body.String())
	}
}

// urlRewriter is a test RoundTripper that rewrites every request's
// Host/Scheme to point at a single test server base URL (mirrors the
// rewriteRoundTripper used in internal/services tests).
type urlRewriter struct {
	base *url.URL
}

func (rt *urlRewriter) RoundTrip(req *http.Request) (*http.Response, error) {
	rewritten := req.Clone(req.Context())
	rewritten.URL.Scheme = rt.base.Scheme
	rewritten.URL.Host = rt.base.Host
	return http.DefaultTransport.RoundTrip(rewritten)
}

// TestHandleReconnectAccount_Happy verifies status flips to
// 'reauth_required' + reauth_required_at is stamped. The status
// field in the response shape MUST reflect the new state.
func TestHandleReconnectAccount_Happy(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	owner := ownedAccountFixture(1, "instagram")

	var updatedAccount *models.PlatformAccount
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return owner, nil
		},
		updatePlatformAccountFn: func(a *models.PlatformAccount) error {
			updatedAccount = a
			return nil
		},
	}
	r := newTestRouter(svc, store, "")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/21/reconnect", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if updatedAccount == nil {
		t.Fatal("UpdatePlatformAccount was NOT called — reauth_required not stamped")
	}
	if updatedAccount.Status != models.AccountStatusReauthRequired {
		t.Errorf("status: want reauth_required, got %s", updatedAccount.Status)
	}
	if updatedAccount.ReauthRequiredAt == nil || updatedAccount.ReauthRequiredAt.IsZero() {
		t.Errorf("reauth_required_at was NOT stamped")
	}
}

// TestHandleReconnectAccount_CrossTenant_404: vault + DB writes MUST
// NOT happen for cross-tenant probes.
func TestHandleReconnectAccount_CrossTenant_404(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	crossTenant := ownedAccountFixture(999, "instagram")
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return crossTenant, nil
		},
		updatePlatformAccountFn: func(a *models.PlatformAccount) error {
			t.Errorf("UpdatePlatformAccount MUST NOT be called for cross-tenant reconnect (data leak risk); got status=%s", a.Status)
			return nil
		},
	}
	r := newTestRouter(svc, store, "")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/21/reconnect", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant reconnect: want 404, got %d: %s", w.Code, w.Body.String())
	}
}

// TestHandleValidateAccount_UsesProviderTokenPolicy proves that when a
// provider implements TokenPolicyProvider, handleValidateAccount checks
// only the declared token types.
func TestHandleValidateAccount_UsesProviderTokenPolicy(t *testing.T) {
	svc := &mockProvider{platform: "youtube"}
	owner := ownedAccountFixture(1, "youtube")
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return owner, nil
		},
	}
	vault := &mockCredentialVault{
		getFn: func(ctx context.Context, id int64, tt string) (*models.OAuthToken, error) {
			if tt == models.TokenTypeBearer {
				return &models.OAuthToken{AccessToken: "test-token"}, nil
			}
			return nil, fmt.Errorf("token not found")
		},
	}

	capRouter := services.NewCapabilityRouter()
	capRouter.Register("youtube", &mockTokenPolicyProvider{
		mockProvider:        *svc,
		preferredTokenTypes: []string{models.TokenTypeBearer},
	})

	r := mustNewRouterWithDefaults(capRouter, store, auth.NewManager(testJWTSecret, 24), "", nil,
		WithCredentialVault(vault),
		WithOneTimeCodeStore(NewInMemoryOneTimeCodeStore(60*time.Second)),
		WithIdempotencyStore(newMockIdempotencyStore()),
		WithConnectLinkNonceStore(newFakeConnectLinkNonceStore()),
		WithChannelAuthorizer(&fakeChannelAuthorizer{}),
	)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/21/validate", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode validate response: %v", err)
	}
	if resp.Status != models.AccountStatusActive {
		t.Errorf("status: want active, got %q", resp.Status)
	}
}

// TestHandleValidateAccount_BearerTokenRecognized proves the bug fix:
// handleValidateAccount now recognizes TokenTypeBearer tokens as valid,
// not just short_lived and long_lived.
func TestHandleValidateAccount_BearerTokenRecognized(t *testing.T) {
	svc := &mockProvider{platform: "youtube"}
	owner := ownedAccountFixture(1, "youtube")
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return owner, nil
		},
		updatePlatformAccountFn: func(a *models.PlatformAccount) error {
			return nil
		},
	}
	vault := &mockCredentialVault{
		getFn: func(ctx context.Context, id int64, tt string) (*models.OAuthToken, error) {
			if tt == models.TokenTypeBearer {
				return &models.OAuthToken{AccessToken: "valid"}, nil
			}
			return nil, fmt.Errorf("no token")
		},
	}
	r := newTestRouter(svc, store, "", WithCredentialVault(vault))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/21/validate", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)

	var capturedStatus string
	store.updatePlatformAccountFn = func(a *models.PlatformAccount) error {
		capturedStatus = a.Status
		return nil
	}

	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("validate: want 200, got %d: %s", w.Code, w.Body.String())
	}
	if capturedStatus != models.AccountStatusActive {
		t.Errorf("status: want active, got %q (BUG: bearer token not recognized)", capturedStatus)
	}
}
