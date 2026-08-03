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

// driveTokenInfoURL is the production Google tokeninfo endpoint used
// by VerifyDriveTokenIsReadonly (Task 3/10). Constants stay in one
// place so a future redirect (e.g. oauth2.googleapis.com/v3 vs
// oauth2.googleapis.com/v1) updates one symbol, not every call site.
const driveTokenInfoURL = "https://oauth2.googleapis.com/v3/tokeninfo"

// GoogleDriveOAuthService implements the OAuth flow for Google Drive.
// It is used only to read (import) video files from a user's Drive;
// it does not publish content, so it implements only OAuthProvider.
type GoogleDriveOAuthService struct {
	cfg        *config.Config
	httpClient *http.Client
	clock      func() time.Time
	// tokenInfoURL is the test-only override for the Google tokeninfo
	// endpoint. Production code reads driveTokenInfoURL and ignores
	// this field; tests set it to an httptest.Server URL to exercise
	// VerifyDriveTokenIsReadonly against a stable, offline fixture.
	tokenInfoURL string
}

// NewGoogleDriveOAuthService creates a new GoogleDriveOAuthService.
// Returns (nil, nil) when the provider is disabled (no client id).
func NewGoogleDriveOAuthService(cfg *config.Config, deps ...ProviderDependencies) (*GoogleDriveOAuthService, error) {
	if cfg.Auth.GoogleDriveClientID == "" {
		return nil, nil
	}
	var dep ProviderDependencies
	if len(deps) > 0 {
		dep = deps[0]
	}
	return &GoogleDriveOAuthService{
		cfg:        cfg,
		httpClient: dep.resolveHTTPClient(),
		clock:      dep.resolveClock(),
	}, nil
}

func (s *GoogleDriveOAuthService) now() time.Time {
	if s.clock != nil {
		return s.clock()
	}
	return time.Now()
}

// Name returns the platform identifier.
func (s *GoogleDriveOAuthService) Name() string { return "google-drive" }

// GetLoginURL builds the Google OAuth authorization URL with the
// drive.readonly scope so the batch-crawler can enumerate folder
// contents at install time (the canonical scope for arbitrary-folder
// reads; restricted per Google's OAuth taxonomy).
func (s *GoogleDriveOAuthService) GetLoginURL(state string) string {
	return s.GetLoginURLWithOptions(state, OAuthLoginOptions{})
}

// GetLoginURLWithOptions builds the Google OAuth authorization URL.
// Google Drive does not use OAuthLoginOptions; options are ignored.
//
// Scope choice:
//   - drive.readonly — required for folder-level listing (the
//     crawler walks arbitrary folders and downloads files inside
//     them). This is the smallest scope that satisfies the
//     production batch-import flow; we proved away from the
//     alternative `drive.file` (Picker-only; cannot enumerate
//     folder contents) and from the full `drive` (exposes every
//     file in the operator's Drive — much wider audit surface).
//     Approved in the consent-screen as a Restricted scope per
//     Task 3/10 (see docs/OAUTH-PRODUCTION.md).
//   - userinfo.profile — operator display name in the dashboard.
func (s *GoogleDriveOAuthService) GetLoginURLWithOptions(state string, _ OAuthLoginOptions) string {
	params := url.Values{}
	params.Set("client_id", s.cfg.Auth.GoogleDriveClientID)
	params.Set("redirect_uri", s.cfg.Auth.GoogleDriveRedirectURI)
	params.Set("state", state)
	params.Set("scope", canonicalDriveReadonlyScope+" "+userinfoProfileScope)
	params.Set("response_type", "code")
	params.Set("access_type", "offline")
	params.Set("prompt", "consent")
	return "https://accounts.google.com/o/oauth2/v2/auth?" + params.Encode()
}

// HandleCallback exchanges the authorization code for an access token
// and fetches the user's Google profile.
func (s *GoogleDriveOAuthService) HandleCallback(ctx context.Context, state, code string) (*models.PlatformProfile, *models.TokenData, error) {
	tokenResp, err := s.exchangeCodeForToken(ctx, code)
	if err != nil {
		return nil, nil, fmt.Errorf("google drive token exchange: %w", err)
	}
	// Task 3/10 H1: fail fast on a wrong-scope token before we burn
	// another API call on /userinfo. The OAuth redirect already
	// constrained the LOGIN URL's scope parameter to drive.readonly
	// (the canonicalDriveReadonlyScope const), so a wrong-scope
	// response here means either a Google regression OR an operator
	// who manually edited the redirect URI. Either way the channel
	// won't work for folder enumeration, so reject now and surface a
	// clear remediation message instead of letting the first Drive
	// download fail 6 months later.
	//
	// Policy split (thinker-with-files-gemini Task 3/10 H1 review):
	//   - ErrDriveTokenScopeMismatch → BLOCK (deterministic; no token persisted upstream)
	//   - ErrDriveTokenEmpty         → BLOCK (programming error, won't happen post-exchange)
	//   - transient (network, 401 from tokeninfo, malformed JSON) → WARN + PROCEED
	//     so a one-off Google outage doesn't permanently brick the dashboard;
	//     the OAuth consent screen already forced the right scope so the
	//     downstream download will likely succeed anyway.
	if verifyErr := s.VerifyDriveTokenIsReadonly(ctx, tokenResp.AccessToken); verifyErr != nil {
		if errors.Is(verifyErr, ErrDriveTokenScopeMismatch) {
			return nil, nil, fmt.Errorf("google drive oauth callback scope check: %w", verifyErr)
		}
		slog.Warn("google drive tokeninfo verification failed (non-fatal); proceeding with OAuth callback",
			slog.Any("err", verifyErr))
	}
	profile, err := s.getUserInfo(ctx, tokenResp.AccessToken)
	if err != nil {
		return nil, nil, fmt.Errorf("google drive user info: %w", err)
	}
	return profile, &models.TokenData{
		AccessToken:           tokenResp.AccessToken,
		RefreshToken:          tokenResp.RefreshToken,
		TokenType:             models.TokenTypeBearer,
		ExpiresIn:             tokenResp.ExpiresIn,
		RefreshTokenExpiresIn: tokenResp.RefreshTokenExpiresIn,
		Scopes:                nonEmptyScopes(tokenResp.Scope),
	}, nil
}

// RefreshOAuthToken refreshes a Google Drive access token. The
// named err return powers the shared RecordTokenRefreshMetrics defer
// (same pattern as the YouTube path) so the periodic token-refresh
// sweep and the delivery path emit token_refresh_success_total /
// token_refresh_error_total for google-drive.
func (s *GoogleDriveOAuthService) RefreshOAuthToken(ctx context.Context, refreshToken string) (result *models.TokenData, err error) {
	defer RecordTokenRefreshMetrics(models.PlatformGoogleDrive, &err)
	if refreshToken == "" {
		return nil, fmt.Errorf("google drive refresh: empty refresh token")
	}
	body := url.Values{}
	body.Set("client_id", s.cfg.Auth.GoogleDriveClientID)
	body.Set("client_secret", s.cfg.Auth.GoogleDriveClientSecret)
	body.Set("refresh_token", refreshToken)
	body.Set("grant_type", "refresh_token")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://oauth2.googleapis.com/token", strings.NewReader(body.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("google drive refresh request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		// Preserve Google's stable OAuth error code in the shared typed,
		// redacted representation. invalid_grant unwraps to the common
		// credentials.ErrInvalidGrant sentinel.
		return nil, fmt.Errorf("google drive refresh: %w", credentials.ParseOAuthTokenError(resp.StatusCode, respBody))
	}
	var tr googleDriveTokenResponse
	if err := json.Unmarshal(respBody, &tr); err != nil {
		return nil, fmt.Errorf("google drive refresh parse: %w", err)
	}
	if tr.RefreshToken == "" {
		tr.RefreshToken = refreshToken
	}
	return &models.TokenData{
		AccessToken:           tr.AccessToken,
		RefreshToken:          tr.RefreshToken,
		TokenType:             models.TokenTypeBearer,
		ExpiresIn:             tr.ExpiresIn,
		RefreshTokenExpiresIn: tr.RefreshTokenExpiresIn,
		Scopes:                nonEmptyScopes(tr.Scope),
	}, nil
}

func (s *GoogleDriveOAuthService) exchangeCodeForToken(ctx context.Context, code string) (*googleDriveTokenResponse, error) {
	body := url.Values{}
	body.Set("client_id", s.cfg.Auth.GoogleDriveClientID)
	body.Set("client_secret", s.cfg.Auth.GoogleDriveClientSecret)
	body.Set("code", code)
	body.Set("grant_type", "authorization_code")
	body.Set("redirect_uri", s.cfg.Auth.GoogleDriveRedirectURI)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://oauth2.googleapis.com/token", strings.NewReader(body.Encode()))
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
		return nil, credentials.ParseOAuthTokenError(resp.StatusCode, respBody)
	}
	var tr googleDriveTokenResponse
	if err := json.Unmarshal(respBody, &tr); err != nil {
		return nil, fmt.Errorf("token parse: %w", err)
	}
	return &tr, nil
}

func (s *GoogleDriveOAuthService) getUserInfo(ctx context.Context, accessToken string) (*models.PlatformProfile, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.googleapis.com/oauth2/v2/userinfo", nil)
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
		PlatformUserID: result.ID,
		Username:       result.Name,
		Name:           result.Name,
		Email:          result.Email,
	}, nil
}

type googleDriveTokenResponse struct {
	AccessToken           string `json:"access_token"`
	TokenType             string `json:"token_type"`
	ExpiresIn             int64  `json:"expires_in"`
	Scope                 string `json:"scope"`
	RefreshToken          string `json:"refresh_token"`
	RefreshTokenExpiresIn int64  `json:"refresh_token_expires_in"`
}

// canonicalDriveReadonlyScope is the exact scope string Google hands
// out in tokeninfo responses for the drive.readonly grant. Used as
// the equality target in VerifyDriveTokenIsReadonly so a substring
// match against "drive.readonly" can't be tricked by a hypothetical
// drive.readonly2 / drive.readonly.alt future scope.
const canonicalDriveReadonlyScope = "https://www.googleapis.com/auth/drive.readonly"

// canonicalDriveWriteScope is reserved for the InstaEdit EXPORTER
// (GoogleDriveDeliveryAdapter / GoogleDriveDestination) which must
// upload files into the operator's Drive. The IMPORT path (this
// file's OAuth flow) deliberately does NOT request this scope:
// `drive` is **restricted** per Google's OAuth taxonomy (deeper
// audit than `drive.readonly`, exposes every file in the
// operator's Drive) and is unnecessary for folder readout, which
// is the only Importer-surface requirement. The exporter requests
// `drive` on its own (separate) OAuth client so its login URL is
// independent of this Importer's.
//
// The downstream verifier (VerifyDriveTokenIsReadonly) is the strict
// gate: it accepts ONLY the canonical `drive.readonly` token claim.
// Acceptance of `drive` (write) is intentionally disabled so a
// future regression that flips the GetLoginURLWithOptions scope
// literal would be caught at the tokeninfo runtime check rather
// than leaving a token with wrong-scope entitlements in the vault.
// This constant stays defined because the exporter flow references
// the SAME URL string at its own scope declaration; keeping the
// spelling in one place stops the two surfaces from drifting.
const canonicalDriveWriteScope = "https://www.googleapis.com/auth/drive"

// userinfoProfileScope is the companion scope InstaEdit always
// requests alongside drive.readonly so the dashboard can show the
// operator's Google display name + avatar. Declared as its own const
// (Task 3/10 M2) so the canonical-scope comparison in
// VerifyDriveTokenIsReadonly stays focused on drive.readonly and
// this companion scope isn't accidentally string-matched into the
// verifier's equality check.
const userinfoProfileScope = "https://www.googleapis.com/auth/userinfo.profile"

// ErrDriveTokenEmpty is the typed sentinel returned when the caller
// passes an empty access token to VerifyDriveTokenIsReadonly. Lets
// callers errors.Is against it to render a structured operator
// message ("your session expired; please reconnect") vs. a generic
// network error.
var ErrDriveTokenEmpty = errors.New("ERR_DRIVE_TOKEN_EMPTY")

// ErrDriveTokenScopeMismatch is the typed sentinel returned when the
// Google tokeninfo scope claim does NOT include the canonical
// drive.readonly scope. Distinguishes the most common Task 3/10
// regression (legacy drive.file scope) from transient network /
// endpoint errors so handlers can render the correct remediation.
var ErrDriveTokenScopeMismatch = errors.New("ERR_DRIVE_TOKEN_SCOPE_MISMATCH")

// VerifyDriveTokenIsReadonly hits the Google tokeninfo endpoint to
// confirm the supplied access token was issued with the
// `drive.readonly` scope. Returns nil if the canonical scope claim
// contains drive.readonly; otherwise an error describing the actual
// scope with a typed sentinel (ErrDriveTokenScopeMismatch or
// ErrDriveTokenEmpty) so callers can errors.Is the failure category.
//
// Per Task 3/10, this is the canonical "did the operator actually
// grant drive.readonly (vs the legacy drive.file) when the OAuth
// flow completed?" check.
//
// Production callers:
//   - HandleCallback invokes this with the access_token from the
//     just-completed exchangeCodeForToken response. The token is
//     non-empty by construction, so the ErrDriveTokenEmpty branch
//     is effectively unreachable from HandleCallback (left in as a
//     defensive guard, NOT dead code).
//   - Future ops-time / SLO monitors can poll the same endpoint.
//
// Not on the OAuth callback critical path constrains the LOGIN
// URL's scope parameter to drive.readonly so the exchange itself
// is forced into the right scope; tokeninfo here is a
// defense-in-depth runtime guard — a future regression that flips
// the GetLoginURLWithOptions scope literal would be caught by the
// verifier rather than leaving a non-functional token in the vault.
func (s *GoogleDriveOAuthService) VerifyDriveTokenIsReadonly(ctx context.Context, accessToken string) error {
	if accessToken == "" {
		return fmt.Errorf("%w: drive tokeninfo input rejected before HTTP call", ErrDriveTokenEmpty)
	}
	target := s.tokenInfoURL
	if target == "" {
		target = driveTokenInfoURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		target+"?access_token="+url.QueryEscape(accessToken),
		nil,
	)
	if err != nil {
		return err
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("drive tokeninfo request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("drive tokeninfo failed (status %d)", resp.StatusCode)
	}
	var parsed struct {
		Scope string `json:"scope"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return fmt.Errorf("drive tokeninfo parse: %w", err)
	}
	// Exact-token match is more conservative than substring: the
	// canonical scope claim is space-delimited; we split + compare
	// element-by-element against canonicalDriveReadonlyScope ONLY.
	// This rejects drive.file alone, the unrestricted `auth/drive`
	// (write access — reserved for the exporter), and hypothetical
	// future scopes like `drive.readonly2` / `drive.readonly.alt`
	// that would otherwise match a naive "drive.readonly" substring
	// check. The strict accept-list keeps the consent-screen
	// declaration consistent with the runtime guard: every scope
	// that gets through this check is audibly documented as
	// drive.readonly in `docs/OAUTH-PRODUCTION.md` Step 3.
	for _, scope := range strings.Fields(parsed.Scope) {
		if scope == canonicalDriveReadonlyScope {
			return nil
		}
	}
	return fmt.Errorf("%w: drive token scope does not include %q (got %q); refusing to use this token for folder-level Drive reads (the importer consumes only drive.readonly; drive.file and the unrestricted drive write scope are not sufficient)",
		ErrDriveTokenScopeMismatch, canonicalDriveReadonlyScope, parsed.Scope)
}
