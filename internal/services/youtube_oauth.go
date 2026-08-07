package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/config"
	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// YouTubeOAuthService implements the YouTube provider. Taglio 2.1:
//
// Capabilities exposed:
//   - OAuthProvider (Google OAuth 2.0 with offline access)
//   - ContentValidator (video required)
//   - Publisher (resumable upload protocol)
//   - AccountManager (Validate / Revoke)
type YouTubeOAuthService struct {
	cfg        *config.Config
	httpClient *http.Client
	clock      func() time.Time
	// uploadOpts (P1#6) — every chunked-PUT retry + backoff knob.
	// Populated from cfg in NewYouTubeOAuthService; tests override
	// backoff/sleep via the unexported uploadDeps fields.
	uploadOpts youTubeUploadOptions
	// uploadDeps (P1#6) — test-injectable backoff/sleep functions.
	// nil in production: NewYouTubeOAuthService installs the
	// defaults (computeYouTubeBackoff + defaultYouTubeSleep).
	uploadDeps *youTubeUploadDeps
	// sessionStore persists the resumable-upload session URI + offset
	// across worker crashes (P1#5 / migration 048). Wired in
	// NewYouTubeOAuthService from *repository.UploadJobRepository
	// (concrete type kept out of this struct via the
	// YouTubeSessionStore narrow interface). Optional in tests.
	sessionStore YouTubeSessionStore
	// sessionEncryptor wraps the YouTube session URI before
	// persistence. Required when sessionStore != nil: storing the
	// plaintext URI in upload_jobs.youtube_session_uri defeats the
	// "credential-adjacent" intent of migration 048 + the
	// json:"-" redaction on the Go side. nil encryptor on a nil
	// store is the production default (the publish path doesn't
	// need it for single-shot uploads); nil encryptor on a non-nil
	// store surfaces as a constructor error.
	sessionEncryptor SessionEncryptor
	// sessionJobID + sessionWorkerID are stamped onto every
	// sessionStore.* call so the CAS in SaveYouTubeSession /
	// ClearYouTubeSession can refuse a write against a row that
	// has been re-claimed (or lease-expired) by another worker.
	// Defaults to empty; the upload worker injects both via
	// SetSessionContext before calling Publish/StartPublish.
	sessionJobID    int64
	sessionWorkerID string
	// pool (YouTube OAuth Client Pool, R4) is the optional A/B client
	// registry used by RefreshOAuthToken. When wired (bootstrap), every
	// YouTube refresh resolves the grant's oauth_client_key (stamped on
	// ctx by CredentialVault.Renew) against this registry and refreshes
	// with the EXACT client that issued the token. nil keeps the legacy
	// single-client refresh path (cfg.Auth.YouTubeClientID) untouched.
	pool *YouTubeOAuthClientRegistry
}

// NewYouTubeOAuthService creates a new YouTubeOAuthService. Accepts optional
// ProviderDependencies for HTTP client injection.
func NewYouTubeOAuthService(cfg *config.Config, deps ...ProviderDependencies) (*YouTubeOAuthService, error) {
	if cfg.Auth.YouTubeClientID == "" {
		return nil, nil // provider disabled
	}
	var dep ProviderDependencies
	if len(deps) > 0 {
		dep = deps[0]
	}
	opts := loadYouTubeUploadOptions(cfg)
	return &YouTubeOAuthService{
		cfg:        cfg,
		httpClient: dep.resolveHTTPClient(),
		clock:      dep.resolveClock(),
		uploadOpts: opts,
		uploadDeps: loadYouTubeUploadDeps(opts),
	}, nil
}

// SetYouTubeOAuthPool wires the optional YouTube OAuth Client Pool
// registry onto the service so RefreshOAuthToken refreshes each grant
// with the client that issued it (resolved via the grant's
// oauth_client_key). Nil (default) keeps the legacy single-client
// refresh path. The registry never exposes client secrets.
func (s *YouTubeOAuthService) SetYouTubeOAuthPool(pool *YouTubeOAuthClientRegistry) {
	s.pool = pool
}

// ClientID returns the YouTube OAuth client_id this service was
// configured with (cfg.Auth.YouTubeClientID). Used by pkg/api/handlers.go
// handleValidateAccount to compare Google's tokeninfo `aud` against
// the configured client — a Production-but-issued-for-Testing token
// carries a mismatched aud and is a hard reauth signal (the 4-step
// pipeline's STEP 2 guard). Returns "" if the service hasn't been
// fully constructed (defensive — the production wiring wires
// cfg.Auth.YouTubeClientID at NewYouTubeOAuthService time).
func (s *YouTubeOAuthService) ClientID() string {
	if s == nil || s.cfg == nil {
		return ""
	}
	return s.cfg.Auth.YouTubeClientID
}

// now returns the current time via the injected clock, or time.Now as default.
func (s *YouTubeOAuthService) now() time.Time {
	if s.clock != nil {
		return s.clock()
	}
	return time.Now()
}

func (s *YouTubeOAuthService) Name() string { return models.PlatformYouTube }

// PreferredTokenTypes declares that YouTube stores the OAuth grant as a
// bearer token. Validation checks bearer first, then falls back to the
// other common token types for backwards compatibility.
func (s *YouTubeOAuthService) PreferredTokenTypes() []string {
	return []string{
		models.TokenTypeBearer,
		models.TokenTypeShortLived,
		models.TokenTypeLongLived,
	}
}

// Compile-time assertion (matches the YouTubeChannelBinder /
// YouTubeCanaryUploader guard pattern below). Caught by `go vet`,
// not at runtime.
var _ error = (*ErrChannelListSafetyCap)(nil)

// Compile-time assertion: YouTubeOAuthService satisfies the
// services.YouTubeChannelBinder capability interface. Caught by
// `go vet`, not at runtime.
var _ YouTubeChannelBinder = (*YouTubeOAuthService)(nil)
var _ YouTubeCanaryUploader = (*YouTubeOAuthService)(nil)

// P1 (Blocco #1 followup) — youtube_privacy_updater.go adds the
// post-upload privacy-transition cast used by PublishWorker in
// Phase 2 (skip-reupload path). The assertion keeps the contract
// honest: a future refactor that renames UpdateVideoPrivacy or
// changes its signature would surface here at vet time instead of
// at runtime on a real publish tick.
var _ YouTubePrivacyUpdater = (*YouTubeOAuthService)(nil)
var _ OAuthGrantRevoker = (*YouTubeOAuthService)(nil)

func (s *YouTubeOAuthService) GetLoginURL(state string) string {
	return s.GetLoginURLWithOptions(state, OAuthLoginOptions{})
}

// youtubeOAuthScopes (Bug #3 fix) is the SINGLE SOURCE OF TRUTH for the
// consent-screen scope list requested by every YouTube OAuth redirect.
//
// Least-privilege rationale (each scope earns its keep):
//
//	youtube.upload     — videos.insert (chunked-PUT resumable upload).
//	                     The single scope strictly required to push
//	                     bytes to YouTube.
//	youtube.readonly   — channels.list?mine=true (channel binding guard
//	                     in ValidateChannelBinding) + videos.list
//	                     (processing-status poll). Read-only; no
//	                     quotable writes.
//	youtube.force-ssl  — thumbnails.set (custom thumbnail upload) +
//	                     videos.update (privacyStatus + publishAt +
//	                     snippet title/description). youtube.upload
//	                     alone does NOT grant write access to video
//	                     metadata, so both thumbnail and publish
//	                     rows REQUIRE this scope.
//	openid + email + profile — OIDC identity (user id, email, name).
//
// We deliberately do NOT request any of the YouTube Analytics scopes
// (yt-analytics-monetary.readonly, yt-analytics.readonly). The publish
// pipeline (upload, thumbnail, schedule, publish) does not consume
// earnings / RPM / CPM / impressions data; requesting them would (a)
// trigger a Google brand-verification re-review under the OAuth
// verification policy without delivering any functional gain, and
// (b) widen the consent screen to a "Sensitive scope" tier that
// requires justification under Google's 2024 OAuth verification
// classification. Re-introduction of an Analytics scope to this
// constant is treated as a blocking change.
//
// Mirrors:
//   - cmd/oauth-scope-canary/main.go::canonicalScopes (operator canary
//     diffs Google's tokeninfo against this set every scheduled run)
//   - docs/OAUTH-PRODUCTION.md "Step 3 -- declare the scopes
//     (minimum set)" + "Code-side guard" (public-facing mirror)
//
// A PR that edits this constant without updating either the canary
// canonicalScopes OR the docs table should be rejected.
// youtubeOAuthScopes is defined in youtube_oauth_policy.go so consent,
// token introspection, and credential resolution share one policy source.

// GetLoginURLWithOptions builds the Google authorize URL using the
// legacy single-client config (cfg.Auth.YouTubeClientID/
// YouTubeRedirectURI). YouTube OAuth Client Pool deployments route
// through GetLoginURLWithPoolClient instead; this method keeps the
// pre-pool behaviour for callers that never wire a registry (admin
// connect-link, legacy tests).
func (s *YouTubeOAuthService) GetLoginURLWithOptions(state string, options OAuthLoginOptions) string {
	return s.GetLoginURLWithPoolClient(state, options, nil)
}

// GetLoginURLWithPoolClient builds the Google authorize URL for a pool
// client (YouTube OAuth Client Pool). The client's client_id and
// redirect_uri replace the legacy single-client config; every other
// parameter (canonical scope set, access_type=offline, prompt,
// login_hint) is identical to GetLoginURLWithOptions. A nil client
// falls back to the legacy single-client config — the handler only
// passes non-nil clients selected from the pool registry, so the
// consent URL is always built against the client that will later
// exchange the code.
func (s *YouTubeOAuthService) GetLoginURLWithPoolClient(state string, options OAuthLoginOptions, client *YouTubeOAuthClientConfig) string {
	clientID, redirectURI := s.cfg.Auth.YouTubeClientID, s.cfg.Auth.YouTubeRedirectURI
	if client != nil {
		clientID, redirectURI = client.ClientID, client.RedirectURI
	}
	params := url.Values{}
	params.Set("client_id", clientID)
	params.Set("redirect_uri", redirectURI)
	params.Set("state", state)
	// Bug #3 fix: pass the canonical scope set (no yt-analytics
	// scopes) directly. See youtubeOAuthScopes above for the full
	// per-scope rationale.
	params.Set("scope", youtubeOAuthScopes)
	params.Set("response_type", "code")
	params.Set("access_type", "offline")
	params.Set("include_granted_scopes", "true")

	if options.ForceConsent || options.SelectAccount {
		var prompts []string
		if options.SelectAccount {
			prompts = append(prompts, "select_account")
		}
		if options.ForceConsent {
			prompts = append(prompts, "consent")
		}
		params.Set("prompt", strings.Join(prompts, " "))
	}

	if options.LoginHint != "" {
		params.Set("login_hint", options.LoginHint)
	}

	return "https://accounts.google.com/o/oauth2/v2/auth?" + params.Encode()
}

func (s *YouTubeOAuthService) HandleCallback(ctx context.Context, state, code string) (*models.PlatformProfile, *models.TokenData, error) {
	return s.HandleCallbackWithClient(ctx, state, code, nil)
}

// HandleCallbackWithClient exchanges the authorization code using the
// given pool client (YouTube OAuth Client Pool). The callback MUST use
// the client that built the consent URL — the authorization code was
// issued against that client_id + redirect_uri and Google rejects an
// exchange against a different client. The pkg/api handler resolves the
// client from the signed state's oauth_client_key and passes it here;
// a nil client falls back to the legacy single-client config.
func (s *YouTubeOAuthService) HandleCallbackWithClient(ctx context.Context, state, code string, client *YouTubeOAuthClientConfig) (*models.PlatformProfile, *models.TokenData, error) {
	slog.Info("YouTube: exchanging code for token", "oauth_client_key", clientKeyForLog(client))

	tokenResp, err := s.exchangeCodeForTokenWithClient(ctx, code, client)
	if err != nil {
		return nil, nil, fmt.Errorf("youtube token exchange: %w", err)
	}

	slog.Info("YouTube: fetching user info")
	profile, err := s.getUserInfo(ctx, tokenResp.AccessToken)
	if err != nil {
		return nil, nil, fmt.Errorf("youtube user info: %w", err)
	}

	tokenData := &models.TokenData{
		AccessToken:           tokenResp.AccessToken,
		RefreshToken:          tokenResp.RefreshToken,
		ProviderSubjectID:     profile.ProviderSubjectID,
		TokenType:             models.TokenTypeBearer,
		ExpiresIn:             tokenResp.ExpiresIn,
		RefreshTokenExpiresIn: tokenResp.RefreshTokenExpiresIn,
		Scopes:                nonEmptyScopes(tokenResp.Scope),
	}

	return profile, tokenData, nil
}

// clientKeyForLog returns a log-safe pool-client label: the client's
// Key when present, the legacy marker otherwise. Never includes
// credential material.
func clientKeyForLog(client *YouTubeOAuthClientConfig) string {
	if client == nil {
		return "legacy_single_client"
	}
	return client.Key
}

// Revoke calls Google's OAuth 2.0 token revocation endpoint. It remains as
// the compatibility method used by the legacy single-account disconnect path.
func (s *YouTubeOAuthService) Revoke(ctx context.Context, token string) error {
	return s.revokeToken(ctx, token)
}

// RevokeGrant implements the complete-grant revocation capability. The
// caller supplies the decoded refresh token from the credential vault; this
// method never logs or includes that token in an error.
func (s *YouTubeOAuthService) RevokeGrant(ctx context.Context, token string) error {
	return s.revokeToken(ctx, token)
}

func (s *YouTubeOAuthService) revokeToken(ctx context.Context, token string) error {
	if token == "" {
		return &OAuthGrantRevocationError{
			Class: OAuthGrantRevocationPermanent,
			Cause: errors.New("empty revocation token"),
		}
	}
	body := url.Values{}
	body.Set("token", token)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://oauth2.googleapis.com/revoke",
		strings.NewReader(body.Encode()))
	if err != nil {
		return &OAuthGrantRevocationError{Class: OAuthGrantRevocationPermanent, Cause: err}
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return &OAuthGrantRevocationError{
			Class: OAuthGrantRevocationTransient,
			Cause: err,
		}
	}
	defer resp.Body.Close()
	responseBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusOK {
		return nil
	}

	if resp.StatusCode == http.StatusBadRequest {
		var envelope struct {
			Code string `json:"error"`
		}
		_ = json.Unmarshal(responseBody, &envelope)
		envelope.Code = safeOAuthRevocationCode(envelope.Code)
		if envelope.Code == "invalid_token" {
			// Google has already rejected the grant. Treat this as an
			// idempotent success: local cleanup is safe and a retry after a
			// partially completed disconnect must not be blocked forever.
			return OAuthGrantRevocationAlreadyCompleted
		}
		return &OAuthGrantRevocationError{
			StatusCode: resp.StatusCode,
			Code:       envelope.Code,
			Class:      OAuthGrantRevocationPermanent,
		}
	}

	class := OAuthGrantRevocationPermanent
	if resp.StatusCode == http.StatusRequestTimeout || resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		class = OAuthGrantRevocationTransient
	}
	return &OAuthGrantRevocationError{
		StatusCode: resp.StatusCode,
		Class:      class,
		RetryAfter: ParseRetryAfter(resp.Header.Get("Retry-After"), time.Now()),
	}
}

// RefreshOAuthToken exchanges a YouTube refresh token for a new access token.
func safeOAuthRevocationCode(code string) string {
	switch code {
	case "invalid_token", "invalid_request", "invalid_client":
		return code
	default:
		return ""
	}
}

func (s *YouTubeOAuthService) RefreshOAuthToken(ctx context.Context, refreshToken string) (result *models.TokenData, err error) {
	defer RecordTokenRefreshMetrics(models.PlatformYouTube, &err)
	// Pool-scoped observability (youtube_oauth_refresh_total{oauth_client_key,
	// result}): label the attempt with the pool client that issued the grant
	// — the key CredentialVault.Renew stamped on ctx — so the operator can
	// compute per-client success/failure rates. Consistent with the
	// invalid_grant metric (also labelled by the grant's stored key). An
	// empty key (non-vault caller) is normalized to
	// legacy_single_client inside RecordYouTubeOAuthRefreshMetrics.
	defer RecordYouTubeOAuthRefreshMetrics(credentials.OAuthClientKeyFromContext(ctx), &err)
	if refreshToken == "" {
		return nil, fmt.Errorf("youtube RefreshOAuthToken: empty refresh token")
	}

	// R4 — YouTube OAuth Client Pool: resolve the client from the
	// grant's oauth_client_key (stamped on ctx by vault.Renew) and
	// refresh with EXACTLY that client. Fail-closed: an unknown key is
	// an error — never fall back to a different client (refreshing a
	// pool A token with client B would surface as invalid_client /
	// invalid_grant from Google). A nil pool (legacy deployment) or
	// empty key (non-vault caller) falls back to the legacy single
	// client.
	client, err := s.poolClientForRefresh(ctx)
	if err != nil {
		return nil, err
	}

	slog.Info("YouTube: refreshing access token", "oauth_client_key", clientKeyForLog(client))
	body := url.Values{}
	if client != nil {
		body.Set("client_id", client.ClientID)
		body.Set("client_secret", client.ClientSecret)
	} else {
		body.Set("client_id", s.cfg.Auth.YouTubeClientID)
		body.Set("client_secret", s.cfg.Auth.YouTubeClientSecret)
	}
	body.Set("refresh_token", refreshToken)
	body.Set("grant_type", "refresh_token")

	tr, err := s.postTokenRequest(ctx, body)
	if err != nil {
		return nil, fmt.Errorf("youtube refresh: %w", err)
	}
	refresh := tr.RefreshToken
	if refresh == "" {
		refresh = refreshToken
	}
	return &models.TokenData{
		AccessToken:           tr.AccessToken,
		RefreshToken:          refresh,
		TokenType:             models.TokenTypeBearer,
		ExpiresIn:             tr.ExpiresIn,
		RefreshTokenExpiresIn: tr.RefreshTokenExpiresIn,
		Scopes:                nonEmptyScopes(tr.Scope),
	}, nil
}

// poolClientForRefresh resolves the pool client that must refresh the
// grant, from the oauth_client_key CredentialVault.Renew stamped on
// ctx.
//
//	key empty       → (nil, nil): legacy single-client caller
//	pool nil        → (nil, nil): legacy deployment (no pool wired)
//	key resolvable  → that client (never a different one)
//	key unknown     → error, fail-closed (cross-pool refresh refused)
//
// Fail-closed semantics mean a legacy grant stamped with the migration
// default youtube_pool_a on a deployment that configured ONLY pool B
// will refuse to refresh (the key does not resolve). That is intended:
// operators must keep pool A as the legacy client's continuation when
// enabling the pool, otherwise old grants would be silently refreshed
// with a client that never issued them.
//
// Never returns a client different from the one that issued the grant.
func (s *YouTubeOAuthService) poolClientForRefresh(ctx context.Context) (*YouTubeOAuthClientConfig, error) {
	key := credentials.OAuthClientKeyFromContext(ctx)
	if key == "" || s.pool == nil {
		return nil, nil
	}
	client, err := s.pool.Resolve(key)
	if err != nil {
		// Fail-closed: refuse to refresh with any other client. The
		// error is redacted (registry errors never carry secrets).
		return nil, fmt.Errorf("youtube refresh: %w", err)
	}
	return client, nil
}

func (s *YouTubeOAuthService) exchangeCodeForToken(ctx context.Context, code string) (*youtubeTokenResponse, error) {
	return s.exchangeCodeForTokenWithClient(ctx, code, nil)
}

// exchangeCodeForTokenWithClient performs the authorization-code
// exchange against the given pool client. A nil client falls back to
// the legacy single-client config; a non-nil client supplies its own
// client_id / client_secret / redirect_uri (the redirect_uri must match
// the one registered for that client on the Google Cloud console).
func (s *YouTubeOAuthService) exchangeCodeForTokenWithClient(ctx context.Context, code string, client *YouTubeOAuthClientConfig) (*youtubeTokenResponse, error) {
	clientID, clientSecret, redirectURI := s.cfg.Auth.YouTubeClientID, s.cfg.Auth.YouTubeClientSecret, s.cfg.Auth.YouTubeRedirectURI
	if client != nil {
		clientID, clientSecret, redirectURI = client.ClientID, client.ClientSecret, client.RedirectURI
	}
	body := url.Values{}
	body.Set("client_id", clientID)
	body.Set("client_secret", clientSecret)
	body.Set("code", code)
	body.Set("grant_type", "authorization_code")
	body.Set("redirect_uri", redirectURI)

	return s.postTokenRequest(ctx, body)
}

// postTokenRequest is the shared POST /token helper used by both the
// authorization-code exchange (exchangeCodeForToken) and the refresh
// flow (RefreshOAuthToken). The two flows differ only in the form
// body; the transport, status check and JSON parse are identical, so
// they live in one place to keep error semantics + quota behaviour
// consistent across both callers.
func (s *YouTubeOAuthService) postTokenRequest(ctx context.Context, body url.Values) (*youtubeTokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", "https://oauth2.googleapis.com/token",
		strings.NewReader(body.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		// Preserve the provider's stable OAuth error code in a typed,
		// redacted error. OAuthTokenError.Unwrap maps invalid_grant to
		// credentials.ErrInvalidGrant without exposing error_description.
		return nil, credentials.ParseOAuthTokenError(resp.StatusCode, respBody)
	}

	var tr youtubeTokenResponse
	if err := json.Unmarshal(respBody, &tr); err != nil {
		return nil, fmt.Errorf("token parse: %w", err)
	}
	return &tr, nil
}

func (s *YouTubeOAuthService) getUserInfo(ctx context.Context, accessToken string) (*models.PlatformProfile, error) {
	req, err := http.NewRequestWithContext(ctx, "GET",
		"https://www.googleapis.com/oauth2/v2/userinfo", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("user info failed (status %d)", resp.StatusCode)
	}

	var result struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Email   string `json:"email"`
		Picture string `json:"picture"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}

	return &models.PlatformProfile{
		PlatformUserID:    result.ID,
		ProviderSubjectID: result.ID,
		Username:          result.Name,
		Name:              result.Name,
		Email:             result.Email,
	}, nil
}

// -----------------------------------------------------------------------------
// Compile-time conformance to the central Platform Registry contract.
// Taglio 4.3.
// -----------------------------------------------------------------------------
var (
	_ OAuthProvider          = (*YouTubeOAuthService)(nil)
	_ ContentValidator       = (*YouTubeOAuthService)(nil)
	_ Publisher              = (*YouTubeOAuthService)(nil)
	_ AsyncPublisher         = (*YouTubeOAuthService)(nil)
	_ AccountDiscoverer      = (*YouTubeOAuthService)(nil)
	_ AccountDetailsProvider = (*YouTubeOAuthService)(nil)
	_ AccountContentProvider = (*YouTubeOAuthService)(nil)
)
