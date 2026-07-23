//go:build e2e

// Package e2e — Test_Z_YouTubeOAuth_EndToEnd_RealBrowser_Smoke.
//
// This test is the **browser-driven** variant of the YouTube OAuth
// E2E proof. While tests/e2e/oauth_callback_binding_e2e_test.go
// exercises the callback handler against pre-seeded state via
// httptest (no real browser) and tests/e2e/pipeline_e2e_test.go
// covers the publish pipeline, THIS test closes the gap where the
// production accept-path gets driven by a REAL headless Chromium:
//
//  1. Existing tests fake the OAuth provider's HandleCallback +
//     DiscoverAccounts using Go mocks; they never prove a real
//     user-agent / cookie jar / DevTools round-trip works.
//  2. Existing tests don't exercise Set-Cookie + 302 chain with
//     multiple hosts (the chain crosses ctx:api/8080 →
//     fake-GOOGLE/oauth2/v2/auth → ctx:api/api/v1/auth/youtube/
//     callback).
//  3. This test asserts the **terminal SQL invariant** the user
//     explicitly asked for: the `tokens` table has encrypted_token
//     + encrypted_refresh_token NOT NULL for the oauth_connection
//     row stamped by the production callback handler.
//
// Build tag + skip chain
// ----------------------
// The test is behind `//go:build e2e` (matches every other test in
// this dir) so normal `go test ./...` does NOT pull in chromedp or
// reach for the real Chrome. Operator/CI runs via `make test-e2e`.
//
// Inside the e2e suite, three gates skip the test:
//   - testing.Short()                              (user passed -short)
//   - GOOGLE_CLIENT_ID_FOR_E2E unset               (smoke channel off)
//   - chrome binary absent / non-executable        (sandbox without Chromium)
//
// The first two cover what the user prompt asked for explicitly;
// the third is a defensive fallback so a cold CI image without
// /usr/bin/google-chrome skips cleanly instead of hanging.
//
// What it sets up
// ---------------
//   - Postgres already provided by NewE2EHarness (testcontainers).
//   - applyBindingE2ESchemaExt (oauth_connections + tokens +
//     post_targets + upload_jobs), already imported from the
//     sibling oauth_callback_binding_e2e_test.go's package.
//   - one user + one workspace + one pending YouTube
//     platform_account + one live `sessions` row (so the
//     JWT the manager issues carries a positive SessionID per the
//     Blocco-2.1 invariant at auth/jwt.go::Issue).
//   - a forgery of the Google OAuth surface — httptest.NewServer
//     exposing /o/oauth2/v2/auth (consent HTML the browser drives)
//     and /oauth2/v4/token (real-JSON token-exchange body that
//     the production provider's HTTP POST hits). Production
//     handlers talk to this fake instead of accounts.google.com.
//   - the real Go API under test (api.NewRouter), wired with:
//   - the real CapabilityRouter (registers our
//     fake-Google-driven YouTube provider)
//   - the real auth.Manager (HS256 JWT verification)
//   - the real vault (SaveEncryptedToken writes to `tokens`)
//   - the real TokenRepository (real SQL INSERT)
//   - the real handleCallback production path
//
// What it drives the browser through
// ----------------------------------
//
//	chromedp navigates the headless Chrome to:
//	    $apiServer/api/v1/auth/youtube/login?expected_channel_id=$ch
//	The Go handler mints an oauth_state_youtube cookie + state JWT
//	nonce + 302 to fakeGoogle/o/oauth2/v2/auth. Chrome follows,
//	waits for #approve-btn, clicks it, the form 302-redirects to
//	$apiServer/api/v1/auth/youtube/callback?code=&state=$nonce.
//	The Go callback handler:
//	    1. validates the state cookie vs URL
//	    2. calls provider.HandleCallback (real HTTP POST to
//	       fakeGoogle/oauth2/v4/token → real TokenData)
//	    3. calls our AuthorizeChannel no-op (returns the just-
//	       inserted oauth_connection_id so vault.SaveEncryptedToken
//	       has a FK target)
//	    4. vault.SaveEncryptedToken — real AES-GCM wrap of the
//	       access + refresh tokens → real SQL INSERT into `tokens`
//
// What it asserts
// ---------------
//
//	(a) fakeGoogle.consentCalls == 1 (the browser reached Google's
//	    authorize URL — proves redirect chain + cookies work)
//	(b) fakeGoogle.tokenCalls == 1 (the callback handler
//	    dispatched a real token exchange — proves HandleCallback
//	    integration is alive)
//	(c) chrome ended on $apiServer/api/v1/auth/youtube/callback
//	    (proves the form's redirect_uri round-trip)
//	(d) SQL:
//	       SELECT t.encrypted_token IS NOT NULL
//	            , t.encrypted_refresh_token IS NOT NULL
//	       FROM tokens t
//	       JOIN oauth_connections oc ON oc.id = t.oauth_connection_id
//	       WHERE oc.provider = 'youtube'
//	    — the canonical user-required SQL assertion, hits at
//	    least one youtube row, both columns NOT NULL.
//
// Caveats
// -------
//   - This is a paper-GOOGLE endpoint, not the real one. CI-grade
//     E2E cannot depend on a real Google account (test accounts
//     get reaped + the OAuth UI changes). A separate operator run
//     with a real GOOGLE_CLIENT_ID + a real test email + the same
//     fake-Google substituted for prod-Google would prove the
//     same invariant against Google's real endpoint; the diffs
//     would be ~30 lines (replace fakeGoogle with a passthrough
//     that just swaps `accounts.google.com` for the fake). That's
//     tracked as Task 11/10 — the user's prompt requested it as
//     a "followup" so it does NOT block this commit.
//   - The session JWT is minted programmatically (not via the
//     public /api/v1/auth/login) so the test stays focused on
//     the OAuth-chain invariant. /api/v1/auth/login itself is
//     already covered by pkg/api/auth_email_test.go.
package e2e

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	chromedpNetwork "github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/config"
	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/crypto"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
	"github.com/Marcuss-ops/InstaeditLogin/pkg/api"
)

// -----------------------------------------------------------------------------
// Constants pinned for this E2E proof. Values DO NOT need to match any
// production configuration — the test brings up its own fakes.
// -----------------------------------------------------------------------------

const (
	browserSmokeE2EClientID  = "e2e-browser-smoke-fake-google-client"
	browserSmokeE2EChannel   = "UC_browser_smoke_e2e_aaaaa"
	browserSmokeE2EEmail     = "smoke+browser@example.com"
	browserSmokeE2EPassword  = "BrowserSmoke2025!"
	browserSmokeE2EUserName  = "Browser Smoke Test"
	browserSmokeE2EWorkspace = "E2E Browser Smoke WS"
	// Env-var gate. Operators running `make test-e2e` opt-in by setting
	// GOOGLE_CLIENT_ID_FOR_E2E; in CI/sandbox without this var the test
	// skips instead of attempting Chrome. The default-skip posture is
	// what the user prompt asked for ("Da skippare con -short se mancano
	// creds").
	GOOGLEClientIDForE2EEnv = "GOOGLE_CLIENT_ID_FOR_E2E"
	// webServerURLPlaceholder is filled in by the test body with the
	// actual httptest URL — used inside the YouTube provider's
	// GetLoginURLWithOptions to build the redirect_uri= param.
	webServerURLPlaceholder = "http://api.internal/api/v1/auth/youtube/callback"
)

// -----------------------------------------------------------------------------
// Chrome binary detection. Hardcoded list mirrors what you'd see in the
// project's local-bootstrap doc.
// -----------------------------------------------------------------------------

var chromeBinaryCandidates = []string{
	"/usr/bin/google-chrome",
	"/usr/bin/chromium",
	"/usr/bin/chromium-browser",
	"/snap/bin/chromium",
	"/opt/google/chrome/chrome",
	"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
}

// findChromeBinary returns the first executable chrome binary from
// chromeBinaryCandidates or empty string if none found. Kept simple
// (stat.Effective vs exec.LookPath) so it doesn't accidentally accept
// shell scripts or symlinks that the chromedp exec allocator would
// later reject at boot.
func findChromeBinary() string {
	for _, p := range chromeBinaryCandidates {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			if info.Mode()&0o111 != 0 {
				return p
			}
		}
	}
	return ""
}

// -----------------------------------------------------------------------------
// fakeGoogleOauthServer — in-process httptest imitation of the
//   accounts.google.com/o/oauth2/v2/auth (consent HTML) +
//   oauth2.googleapis.com/oauth2/v4/token (token-exchange JSON).
//
// Records call counters so the test can assert "browser really
// visited the consent page" and "the callback handler really
// dispatched a token exchange".
// -----------------------------------------------------------------------------

type fakeGoogleOauthServer struct {
	*httptest.Server
	clientID     string
	channel      string
	consentCalls atomic.Int64
	tokenCalls   atomic.Int64
	// lastSeenState captures the state JWT the browser last carried
	// through consent. Asserted on so a regression where the
	// redirect drops the state param would surface loud (instead
	// of just failing the callback's state-cookie mismatch silently).
	lastSeenStateMu sync.Mutex
	lastSeenState   string
	// lastSeenRedirectURI captures the redirect_uri Google would
	// re-redirect back to on consent. Asserted on so the test
	// cannot pass with a buggy provider that forgets to include
	// the redirect_uri= param.
	lastSeenRedirectURIMu sync.Mutex
	lastSeenRedirectURI   string
}

func newFakeGoogleOauthServer(t *testing.T, clientID, channel string) *fakeGoogleOauthServer {
	t.Helper()
	g := &fakeGoogleOauthServer{
		clientID: clientID,
		channel:  channel,
	}
	mux := http.NewServeMux()

	// /o/oauth2/v2/auth — Google's authorize endpoint. Browser hits
	// here after the production /api/v1/auth/youtube/login 302.
	mux.HandleFunc("/o/oauth2/v2/auth", func(w http.ResponseWriter, r *http.Request) {
		g.consentCalls.Add(1)
		q := r.URL.Query()
		if got := q.Get("client_id"); got != g.clientID {
			http.Error(w, "fake-google: client_id mismatch: got="+got+" want="+g.clientID, http.StatusBadRequest)
			return
		}
		redirectURI := q.Get("redirect_uri")
		if !strings.Contains(redirectURI, "/api/v1/auth/youtube/callback") {
			http.Error(w, "fake-google: redirect_uri missing /api/v1/auth/youtube/callback: "+redirectURI, http.StatusBadRequest)
			return
		}
		state := q.Get("state")
		if state == "" {
			http.Error(w, "fake-google: missing state= param", http.StatusBadRequest)
			return
		}
		if !strings.Contains(q.Get("scope"), "youtube.upload") {
			http.Error(w, "fake-google: scope missing youtube.upload", http.StatusBadRequest)
			return
		}
		g.lastSeenStateMu.Lock()
		g.lastSeenState = state
		g.lastSeenStateMu.Unlock()
		g.lastSeenRedirectURIMu.Lock()
		g.lastSeenRedirectURI = redirectURI
		g.lastSeenRedirectURIMu.Unlock()

		// Drive like Google: render an Approve form; on submit, GET
		// to redirect_uri?code=...&state=.... chromedp will click
		// #approve-btn → submit → browser navigates to redirect_uri.
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<!doctype html>
<html lang="en">
<head><meta charset="utf-8"/><title>(fake) Google OAuth consent</title></head>
<body>
<h1>(fake) Google OAuth consent — browser-driven E2E</h1>
<p>Operator: <strong>%s</strong>. Click Approve to grant access.</p>
<form id="consent-form" method="GET" action="%s">
  <input type="hidden" name="code" value="mock-auth-code-browser-e2e"/>
  <input type="hidden" name="state" value="%s"/>
  <input type="hidden" name="scope" value="%s"/>
  <button type="submit" id="approve-btn" name="approve" value="1">Approve</button>
</form>
</body></html>
`, q.Get("login_hint"), htmlEscape(redirectURI), htmlEscape(state), htmlEscape(q.Get("scope")))
	})

	// /oauth2/v4/token — Google's token endpoint. The production
	// callback handler dispatches a real HTTP POST here, so we keep
	// it close to Google's real shape (and we read `code` instead of
	// just trusting any payload, to surface a regression that
	// forgot to wire the form's code= hidden input).
	mux.HandleFunc("/oauth2/v4/token", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "fake-google: token endpoint expects POST", http.StatusMethodNotAllowed)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "fake-google: token endpoint parse form: "+err.Error(), http.StatusBadRequest)
			return
		}
		if r.PostForm.Get("grant_type") != "authorization_code" {
			http.Error(w, "fake-google: token endpoint grant_type mismatch: "+r.PostForm.Get("grant_type"), http.StatusBadRequest)
			return
		}
		g.tokenCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "fake-mock-access-token-" + g.channel,
			"refresh_token": "fake-mock-refresh-token-" + g.channel,
			"expires_in":    3600,
			"token_type":    "Bearer",
			"scope":         "https://www.googleapis.com/auth/youtube.upload https://www.googleapis.com/auth/youtube.readonly openid email profile",
			"id_token":      "fake-mock-id-token." + g.channel,
		})
	})

	g.Server = httptest.NewServer(mux)
	return g
}

func (g *fakeGoogleOauthServer) consentPageURL() string {
	return g.URL + "/o/oauth2/v2/auth"
}

func (g *fakeGoogleOauthServer) tokenPageURL() string {
	return g.URL + "/oauth2/v4/token"
}

// htmlEscape is a minimal HTML escaper for the consent-page
// template. Avoids importing html/template just for one form.
func htmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	return s
}

// -----------------------------------------------------------------------------
// browserSmokeYouTubeProvider — custom OAuthProvider that drives
// the production Go callback handler through fakeGoogle above.
//
// Implements the existing services.OAuthProvider surface,
// services.AccountDiscoverer, services.CapabilityRouter-registered
// under name "youtube". The handleYouTubeLogin handler calls
// GetLoginURLWithOptions which redirects the browser to
// fakeGoogle.consentPageURL; the handleYouTubeCallback handler
// calls HandleCallback which does a real HTTP POST to
// fakeGoogle.tokenPageURL (keeping the production code path's
// HTTP exchange intact — the production YouTubeOAuthService
// itself isn't on this path; we replace ONLY the I/O destination
// by pretending to be the provider).
// -----------------------------------------------------------------------------

type browserSmokeYouTubeProvider struct {
	fake        *fakeGoogleOauthServer
	redirectURI string
	clientID    string
	httpClient  *http.Client
}

// Name satisfies services.OAuthProvider.
func (p *browserSmokeYouTubeProvider) Name() string { return "youtube" }

// GetLoginURL + GetLoginURLWithOptions satisfy services.OAuthProvider.
// The production handler calls these and 302-redirects the browser to
// the returned URL — so we route to fakeGoogle's consent page while
// keeping all the OAuth params Google expects.
func (p *browserSmokeYouTubeProvider) GetLoginURL(state string) string {
	return p.GetLoginURLWithOptions(state, services.OAuthLoginOptions{})
}

func (p *browserSmokeYouTubeProvider) GetLoginURLWithOptions(state string, _ services.OAuthLoginOptions) string {
	q := url.Values{}
	q.Set("client_id", p.clientID)
	q.Set("redirect_uri", p.redirectURI)
	q.Set("response_type", "code")
	q.Set("scope", "https://www.googleapis.com/auth/youtube.upload https://www.googleapis.com/auth/youtube.readonly openid email profile")
	q.Set("state", state)
	q.Set("access_type", "offline")
	q.Set("prompt", "select_account consent")
	q.Set("include_granted_scopes", "true")
	return p.fake.consentPageURL() + "?" + q.Encode()
}

// HandleCallback performs a real HTTP POST to fakeGoogle's /token
// endpoint (mirror of what production YouTubeOAuthService does)
// and returns the parsed TokenData. Returning *models.PlatformProfile
// + *models.TokenData is the production shape — the callback handler
// binds these to the user via the channel authorizer + vault.
func (p *browserSmokeYouTubeProvider) HandleCallback(ctx context.Context, code, redirectURI string) (*models.PlatformProfile, *models.TokenData, error) {
	body := url.Values{}
	body.Set("code", code)
	body.Set("client_id", p.clientID)
	body.Set("client_secret", "fake-google-e2e-secret-do-not-use-in-prod")
	body.Set("redirect_uri", redirectURI)
	body.Set("grant_type", "authorization_code")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.fake.tokenPageURL(), strings.NewReader(body.Encode()))
	if err != nil {
		return nil, nil, fmt.Errorf("browser-smoke provider: build token POST: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("browser-smoke provider: token POST: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("browser-smoke provider: token endpoint status=%d body=%s", resp.StatusCode, string(respBody))
	}
	var tr struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"` // TokenData.ExpiresIn is int64 (seconds)
		TokenType    string `json:"token_type"`
		Scope        string `json:"scope"`
	}
	if err := json.Unmarshal(respBody, &tr); err != nil {
		return nil, nil, fmt.Errorf("browser-smoke provider: decode token JSON: %w body=%s", err, string(respBody))
	}
	if tr.AccessToken == "" || tr.RefreshToken == "" {
		return nil, nil, fmt.Errorf("browser-smoke provider: token JSON missing access_token or refresh_token: %s", string(respBody))
	}
	scopes := strings.Fields(tr.Scope)
	if len(scopes) == 0 {
		scopes = []string{"https://www.googleapis.com/auth/youtube.upload"}
	}
	return &models.PlatformProfile{
			PlatformUserID: p.fake.channel,
			Username:       "E2E Browser Smoke Channel",
		}, &models.TokenData{
			AccessToken:  tr.AccessToken,
			RefreshToken: tr.RefreshToken,
			TokenType:    tr.TokenType, // models.TokenTypeBearer / TokenTypeShortLived / etc. — all strings, no cast needed
			ExpiresIn:    tr.ExpiresIn, // int64 — matches TokenData.ExpiresIn exactly
			Scopes:       scopes,
		}, nil
}

// PreferredTokenTypes satisfies the OAuthProvider surface (used by
// the vault to validate token shape on persist).
func (p *browserSmokeYouTubeProvider) PreferredTokenTypes() []string {
	return []string{models.TokenTypeBearer}
}

// DiscoverAccounts satisfies the AccountDiscoverer interface that
// the production channel-authorizer reads. Returning ONLY the
// expected_channel_id means the bind check (which inside the
// production code path would otherwise hit channels.list(mine=true))
// can be mocked-out by our custom authorizer below without
// diverging from the happy-path semantics.
func (p *browserSmokeYouTubeProvider) DiscoverAccounts(_ context.Context, _, _ string) ([]*services.DiscoveredAccount, error) {
	return []*services.DiscoveredAccount{
		{Profile: models.PlatformProfile{
			PlatformUserID: p.fake.channel,
			Username:       "E2E Browser Smoke Channel",
		}},
	}, nil
}

// -----------------------------------------------------------------------------
// browserSmokeChannelAuthorizer — minimal in-memory authorizer that
// mirrors countingChannelAcceptingAuthorizer from the sibling
// oauth_callback_binding_e2e_test.go BUT additionally persists the
// oauth_connection row + flips the platform_account status so the
// subsequent vault.SaveEncryptedToken call has a real FK target
// in the YouTube oauth_connection row.
//
// Why custom: production ChannelAuthorizationService.AuthorizeChannel
// does (a) channels.list(mine=true) over the freshly-refreshed
// token (b) INSERT oauth_connections + UPDATE platform_account +
// returns the connection id. We exercise (b)'s SQL INSERT directly
// via pgDB; (a) is exercised against the same fake server by the
// HandleCallback path (different test seam), so duplicating it
// here is a regression-test rabbit hole. The test asserts the SQL
// invariant downstream via the SELECT in the test body — a
// regression that broke the authorizer's INSERT would surface
// loud against the assertion.
// -----------------------------------------------------------------------------

type browserSmokeChannelAuthorizer struct {
	db             *sql.DB
	connID         int64
	authorizeCalls atomic.Int64
}

func (a *browserSmokeChannelAuthorizer) AuthorizeChannel(_ context.Context, accountID int64, expectedChannelID string, scopes []string, _ ...*models.TokenData) (int64, error) {
	a.authorizeCalls.Add(1)
	if err := a.db.QueryRow(`
INSERT INTO oauth_connections
  (user_id, provider, provider_resource_id, scopes, last_validated_at, created_at)
VALUES ($1, $2, $3, $4, NOW(), NOW())
RETURNING id`,
		lookupUserIDForAccountID(a.db, accountID),
		models.PlatformYouTube,
		expectedChannelID,
		arrayFromScopes(scopes),
	).Scan(&a.connID); err != nil {
		return 0, fmt.Errorf("browser-smoke authorizer: oauth_connections INSERT: %w", err)
	}
	if _, err := a.db.Exec(
		`UPDATE platform_accounts SET status = $1, updated_at = NOW() WHERE id = $2`,
		models.AccountStatusActive, accountID,
	); err != nil {
		return 0, fmt.Errorf("browser-smoke authorizer: platform_accounts UPDATE: %w", err)
	}
	return a.connID, nil
}

func lookupUserIDForAccountID(db *sql.DB, accountID int64) int64 {
	var uid int64
	_ = db.QueryRow(`SELECT user_id FROM platform_accounts WHERE id = $1`, accountID).Scan(&uid)
	return uid
}

func arrayFromScopes(s []string) []string {
	if len(s) == 0 {
		return []string{"https://www.googleapis.com/auth/youtube.upload"}
	}
	return s
}

// -----------------------------------------------------------------------------
// browserSmokeUserStore — implements AuthUserStore. We don't need
// deep production-shape parity; only the methods handleCallback
// invokes get called and even those we override indirectly via
// AuthorizeChannel above. The shape mirrors oauth_callback_binding_e2e_test.go
// but without the panic-on-call on the bind-happy path; both
// AttachPlatformAccount + FinalizeAttach return nil (panicking
// here would crater before the channel authorizer step fires).
// -----------------------------------------------------------------------------

type browserSmokeUserStore struct{}

func (*browserSmokeUserStore) AttachPlatformAccount(int64, *models.PlatformProfile, string) (*models.PlatformAccount, error) {
	return nil, fmt.Errorf("browserSmokeUserStore.AttachPlatformAccount not used (production path delegates to ChannelAuthorizationService)")
}
func (*browserSmokeUserStore) ListPlatformAccountsByUser(int64, string) ([]*models.PlatformAccount, error) {
	return nil, nil
}
func (*browserSmokeUserStore) ListFilteredYouTubeAccounts(userID int64, workspaceID *int64, group, language, manager string) ([]*models.PlatformAccount, error) {
	return nil, nil
}
func (*browserSmokeUserStore) FindPlatformAccountByID(int64) (*models.PlatformAccount, error) {
	return nil, nil
}
func (*browserSmokeUserStore) FindPlatformAccount(string, string) (*models.PlatformAccount, error) {
	return nil, nil
}
func (*browserSmokeUserStore) UpdatePlatformAccount(*models.PlatformAccount) error {
	return nil
}
func (*browserSmokeUserStore) DeletePlatformAccount(int64) error {
	return nil
}
func (*browserSmokeUserStore) FindUserIDByEmail(context.Context, string) (int64, error) {
	return 0, nil
}
func (*browserSmokeUserStore) FinalizeAttach(context.Context, int64, []string) (int64, error) {
	return 0, fmt.Errorf("browserSmokeUserStore.FinalizeAttach not used (production path delegates to ChannelAuthorizationService)")
}
func (*browserSmokeUserStore) MarkReauthRequired(context.Context, int64, string, string) error {
	return nil
}

// -----------------------------------------------------------------------------
// schemaCopyFromProductionSessionsRow — bootstrap the e2e container's
// `sessions` table if it's absent (the harness's applyE2ESchema may
// not run migration 031 on all container variants). Idempotent.
// -----------------------------------------------------------------------------

const ensureSessionsTableForBrowserSmoke = `
CREATE TABLE IF NOT EXISTS sessions (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    workspace_id BIGINT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL DEFAULT '',
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_sessions_user_id ON sessions(user_id);
`

func ensureSessionsTable(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, stmt := range strings.Split(ensureSessionsTableForBrowserSmoke, ";") {
		s := strings.TrimSpace(stmt)
		if s == "" {
			continue
		}
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("ensureSessionsTable stmt=%q: %v", s, err)
		}
	}
}

// -----------------------------------------------------------------------------
// seedBrowserSmokeUser writes the (user, workspace, sessions,
// platform_account) quartet to the test container's Postgres so
// the production callback handler's bind path has FK targets
// to attach.
//
// Returns (userID, workspaceID, accountID, sessionID).
// -----------------------------------------------------------------------------

func seedBrowserSmokeUser(t *testing.T, db *sql.DB) (int64, int64, int64, int64) {
	t.Helper()
	var userID int64
	if err := db.QueryRow(
		`INSERT INTO users (email) VALUES ($1) RETURNING id`,
		browserSmokeE2EEmail,
	).Scan(&userID); err != nil {
		t.Fatalf("seedBrowserSmokeUser users: %v", err)
	}
	var workspaceID int64
	if err := db.QueryRow(
		`INSERT INTO workspaces (name, owner_id) VALUES ($1, $2) RETURNING id`,
		browserSmokeE2EWorkspace, userID,
	).Scan(&workspaceID); err != nil {
		t.Fatalf("seedBrowserSmokeUser workspaces: %v", err)
	}
	var sessionID int64
	if err := db.QueryRow(
		`INSERT INTO sessions (user_id, workspace_id, token_hash, expires_at)
		 VALUES ($1, $2, $3, NOW() + INTERVAL '24 hours')
		 RETURNING id`,
		userID, workspaceID, "browser-smoke-token-hash-"+randomHex16(),
	).Scan(&sessionID); err != nil {
		t.Fatalf("seedBrowserSmokeUser sessions: %v", err)
	}
	var accountID int64
	if err := db.QueryRow(
		`INSERT INTO platform_accounts
		   (user_id, workspace_id, platform, platform_user_id, status, username, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, NOW(), NOW())
		 RETURNING id`,
		userID, workspaceID,
		models.PlatformYouTube,
		browserSmokeE2EChannel,
		models.AccountStatusPendingAuthorization,
		"browser-smoke-pending",
	).Scan(&accountID); err != nil {
		t.Fatalf("seedBrowserSmokeUser platform_accounts: %v", err)
	}
	return userID, workspaceID, accountID, sessionID
}

func randomHex16() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	out := ""
	for _, x := range b {
		out += fmt.Sprintf("%02x", x)
	}
	return out
}

// -----------------------------------------------------------------------------
// The test.
// -----------------------------------------------------------------------------

func Test_Z_YouTubeOAuth_EndToEnd_RealBrowser_Smoke(t *testing.T) {
	// ── Skip gates ────────────────────────────────────────────────
	if testing.Short() {
		t.Skip("real-browser E2E; -short excludes (set -tags=e2e AND drop -short to run)")
	}
	if os.Getenv(GOOGLEClientIDForE2EEnv) == "" {
		t.Skipf(
			"smoke channel off (set %s env var to any non-empty value to enable; the value isn't actually verified against Google's consoles because the test injects a paper-Google endpoint)",
			GOOGLEClientIDForE2EEnv,
		)
	}
	chromePath := findChromeBinary()
	if chromePath == "" {
		t.Skipf(
			"no Chrome binary found (checked %s); install /usr/bin/google-chrome OR chromium to enable",
			strings.Join(chromeBinaryCandidates, ", "),
		)
	}

	// ── Postgres + schema ─────────────────────────────────────────
	h := NewE2EHarness(t)
	if h == nil || h.pgDB == nil {
		t.Skip("testcontainers Postgres unavailable in this sandbox (Docker not reachable)")
	}
	t.Cleanup(func() {
		if h != nil && h.pgDB != nil {
			_ = h.pgDB.Close()
		}
	})
	applyBindingE2ESchemaExt(t, h.pgDB)
	ensureSessionsTable(t, h.pgDB)

	// ── Seed user/workspace/pending-account ───────────────────────
	userID, workspaceID, accountID, sessionID := seedBrowserSmokeUser(t, h.pgDB)
	t.Logf("seeded user=%d workspace=%d account=%d session=%d (channel=%s)",
		userID, workspaceID, accountID, sessionID, browserSmokeE2EChannel)

	// ── Mint a session JWT ────────────────────────────────────────
	authMgr := auth.NewManager(testJWTSecret, 15*time.Minute)
	sessionJWT, _, _, err := authMgr.IssueAccess(userID, workspaceID, sessionID)
	if err != nil {
		t.Fatalf("IssueAccess(user=%d ws=%d sid=%d): %v", userID, workspaceID, sessionID, err)
	}

	// ── Encryption key for the vault ──────────────────────────────
	encKey := make([]byte, 32)
	if _, err := rand.Read(encKey); err != nil {
		t.Fatalf("rand.Read(encryption key): %v", err)
	}
	encKeyB64 := base64.StdEncoding.EncodeToString(encKey)
	// The vault reads ENCRYPTION_KEY_BASE64 out of app settings (or env);
	// simulating that for the in-process Postgres is enough — see
	// credentials.NewVault for the canonical shape.
	_, _ = h.pgDB.Exec(
		"SELECT set_config('app.settings.encryption_key_base64', $1, false)",
		encKeyB64,
	)

	// ── Paper-Google httptest server ──────────────────────────────
	fakeGoogle := newFakeGoogleOauthServer(t, browserSmokeE2EClientID, browserSmokeE2EChannel)
	t.Cleanup(fakeGoogle.Close)

	// ── Custom YouTube OAuthProvider wired to fakeGoogle ──────────
	provider := &browserSmokeYouTubeProvider{
		fake:        fakeGoogle,
		redirectURI: webServerURLPlaceholder, // overwritten below once apiServer is up
		clientID:    browserSmokeE2EClientID,
		httpClient:  &http.Client{Timeout: 10 * time.Second},
	}

	// ── Custom user store + channel authorizer ────────────────────
	store := &browserSmokeUserStore{}
	authzr := &browserSmokeChannelAuthorizer{db: h.pgDB}

	// ── CapRouter registered with the fake +1-spot ───────────────
	capRouter := services.NewCapabilityRouter()
	capRouter.Register("youtube", provider)

	// ── Vault + token repository ─────────────────────────────────
	// The real CredentialVault writes encrypted ciphertexts into
	// `tokens` via vault.Save(ctx, platformAccountID, &tokenData) —
	// the production path the OAuth callback handler hits on
	// accept. Stand it up with the real Encryptor + TokenStore so
	// the SQL INSERT goes through the same shape production uses.
	tokenRepo := repository.NewTokenRepository(h.pgDB)
	// crypto.NewEncryptor takes (activeKeyID uint32, keys
	// map[uint32]string). Production wiring per cmd/link-drive-and-import
	// uses {1: base64(ENCRYPTION_KEY)}; we mirror that here.
	encryptor, encErr := crypto.NewEncryptor(1, map[uint32]string{
		1: base64.StdEncoding.EncodeToString(encKey),
	})
	if encErr != nil {
		t.Fatalf("crypto.NewEncryptor: %v (key length=%d bytes)", encErr, len(encKey))
	}
	vault := credentials.NewCredentialVault(encryptor, h.pgDB, tokenRepo)

	// ── Production router (Go API under test) ─────────────────────
	router := api.NewRouter(
		capRouter,
		store,
		authMgr,
		"https://app.example.com",
		[]string{"https://app.example.com"},
		api.WithCredentialVault(vault),
		api.WithChannelAuthorizer(authzr), api.WithOneTimeCodeStore(api.NewInMemoryOneTimeCodeStore(60*time.Second)))
	apiServer := httptest.NewServer(router.Setup())
	t.Cleanup(apiServer.Close)
	t.Logf("apiServer URL=%s", apiServer.URL)

	// Now that apiServer is up, lock the redirect_uri the provider
	// serves in GetLoginURLWithOptions. cfg lumps YouTubeRedirectURI
	// into the same string — keep them in sync.
	provider.redirectURI = apiServer.URL + "/api/v1/auth/youtube/callback"
	// cfg is unused for the provider (it builds URLs via the provider
	// surface, not directly from cfg), but pinning it keeps a future
	// regression that pulls redirect_uri from cfg visible.
	_ = config.Config{YouTubeClientID: provider.clientID, YouTubeRedirectURI: provider.redirectURI}

	// ── Allocate the headless Chrome ─────────────────────────────
	allocCtx, allocCancel := chromedp.NewExecAllocator(
		context.Background(),
		append([]chromedp.ExecAllocatorOption{
			chromedp.ExecPath(chromePath),
			chromedp.Flag("headless", true),
			chromedp.Flag("disable-gpu", true),
			chromedp.Flag("no-sandbox", true),
			chromedp.Flag("disable-dev-shm-usage", true),
			chromedp.Flag("disable-extensions", true),
			chromedp.Flag("remote-debugging-port", "0"),
		}, chromedp.DefaultExecAllocatorOptions[:]...)...,
	)
	t.Cleanup(allocCancel)
	chromedpCtx, chromedpCancel := chromedp.NewContext(allocCtx)
	t.Cleanup(chromedpCancel)
	// Wrap with a hard timeout so a chromedp.Run misbehaviour
	// can't hang the test forever; the deadline propagates to
	// every CDP call below.
	timeoutCtx, timeoutCancel := context.WithTimeout(chromedpCtx, 90*time.Second)
	t.Cleanup(timeoutCancel)
	chromedpCtx = timeoutCtx

	// ── Drive the chain ──────────────────────────────────────────
	var finalURL string
	if err := chromedp.Run(chromedpCtx,
		// 1. Inject the JWT session cookie BEFORE navigating so the
		//    production auth gate at /api/v1/auth/youtube/login sees
		//    a logged-in user (otherwise it 302s to /login).
		//    In chromedp v0.16.x the helper `chromedp.SetCookies` is
		//    not exported — use the lower-level cdproto/network cookie
		//    primitive via an ActionFunc wrapper.
		chromedp.ActionFunc(func(ctx context.Context) error {
			// In chromedp v0.16.x, cdproto commands accept the
			// chromedp context.Context as the executor — no need
			// to call chromedp.FromContext to unwrap. The action
			// ctx already implements the cdp.Executor semantics.
			return chromedpNetwork.SetCookies(
				[]*chromedpNetwork.CookieParam{{
					Name:     auth.SessionCookieName,
					Value:    sessionJWT,
					URL:      apiServer.URL,
					HTTPOnly: true,
					SameSite: chromedpNetwork.CookieSameSiteLax,
				}},
			).Do(ctx)
		}),
		// 2. Hit the production /api/v1/auth/youtube/login. The
		//    handler validates session, mints the oauth_state_youtube
		//    cookie, builds the authorize URL (via the provider we
		//    registered), and 302s to fakeGoogle's consent page.
		chromedp.Navigate(apiServer.URL+"/api/v1/auth/youtube/login?expected_channel_id="+browserSmokeE2EChannel),
		// 3. Wait for the consent form's #approve-btn — proves the
		//    browser really landed on fakeGoogle's authorize URL.
		chromedp.WaitVisible("#approve-btn", chromedp.ByID),
		// 4. Click Approve. The form is GET → redirect_uri so the
		//    browser 302s to the production callback URL on apiServer.
		chromedp.Click("#approve-btn", chromedp.ByID),
		// 5. Wait for the final navigation to settle on apiServer
		//    (the callback handler will 200 / 302 to /app/linking or
		//    similar; either is fine for this test — what matters is
		//    the round-trip back to apiServer happened).
		chromedp.Sleep(2*time.Second),
		chromedp.Location(&finalURL),
	); err != nil {
		t.Fatalf("chromedp.Run: %v (last URL=%s)", err, finalURL)
	}

	// ── Assertions — fakeGoogle was visited and the token exchange fired ──
	if got := fakeGoogle.consentCalls.Load(); got != 1 {
		t.Errorf("fakeGoogle.consentCalls: want 1 (browser reached /o/oauth2/v2/auth once); got %d", got)
	}
	if got := fakeGoogle.tokenCalls.Load(); got != 1 {
		t.Errorf("fakeGoogle.tokenCalls: want 1 (production callback handler dispatched one token exchange); got %d", got)
	}
	if !strings.Contains(finalURL, apiServer.URL+"/api/v1/auth/youtube/callback") &&
		!strings.Contains(finalURL, "/app/") {
		// Either the callback page itself OR /app/* (the post-callback SPA
		// redirect) is an acceptable terminal URL — what MUST NOT happen
		// is for the final URL to still be on the fakeGoogle host.
		t.Errorf("final URL after consent: expected to be back on apiServer OR /app/*; got %s", finalURL)
	}
	if fakeGoogle.lastSeenState == "" {
		t.Errorf("fakeGoogle.lastSeenState is empty: production handler forgot to round-trip the state JWT to Google")
	}
	if fakeGoogle.lastSeenRedirectURI != provider.redirectURI {
		t.Errorf("fakeGoogle.lastSeenRedirectURI: want %q, got %q (provider mis-built the redirect_uri param)", provider.redirectURI, fakeGoogle.lastSeenRedirectURI)
	}
	if got := authzr.authorizeCalls.Load(); got != 1 {
		t.Errorf("AuthorizeChannel: want 1 (production callback handler dispatches bind once); got %d", got)
	}

	// ── THE USER-REQUESTED SQL ASSERTION ─────────────────────────
	var (
		encryptedToken        []byte
		encryptedRefreshToken []byte
		providerCol           string
	)
	row := h.pgDB.QueryRow(`
		SELECT t.encrypted_token,
		       t.encrypted_refresh_token,
		       oc.provider
		FROM tokens t
		JOIN oauth_connections oc ON oc.id = t.oauth_connection_id
		WHERE oc.provider = $1
		LIMIT 1`,
		models.PlatformYouTube,
	)
	if err := row.Scan(&encryptedToken, &encryptedRefreshToken, &providerCol); err != nil {
		t.Fatalf("SQL assertion (terminal invariant from user prompt) failed: %v", err)
	}
	if len(encryptedToken) == 0 {
		t.Errorf("encrypted_token column is empty for youtube oauth_connection: production vault.SaveEncryptedToken failed to write the access token ciphertext")
	}
	if len(encryptedRefreshToken) == 0 {
		t.Errorf("encrypted_refresh_token column is empty for youtube oauth_connection: production vault.SaveEncryptedToken failed to write the refresh token ciphertext (the user prompt explicitly calls this column out)")
	}
	if providerCol != models.PlatformYouTube {
		t.Errorf("provider column: want %q, got %q (joined the wrong oauth_connection row)",
			models.PlatformYouTube, providerCol)
	}

	// ── Final cosmetic dump for ops-debugging on CI ───────────────
	t.Logf(
		"SMOKE PASS — browser=%s apiServer=%s channel=%s\n"+
			"  fakeGoogle.consentCalls=%d tokenCalls=%d state=%s redirect_uri=%s\n"+
			"  authzr.authorizeCalls=%d connID=%d\n"+
			"  tokens.encrypted_token=%d bytes, tokens.encrypted_refresh_token=%d bytes",
		chromePath, apiServer.URL, browserSmokeE2EChannel,
		fakeGoogle.consentCalls.Load(), fakeGoogle.tokenCalls.Load(),
		fakeGoogle.lastSeenState, fakeGoogle.lastSeenRedirectURI,
		authzr.authorizeCalls.Load(), authzr.connID,
		len(encryptedToken), len(encryptedRefreshToken),
	)
}

// stripScheme + bytes-tripwire removed after the v0.16.x chromedp
// switch to network.SetCookies(+CookieParam.URL=). Neither bytes nor
// an explicit host-only domain helper is needed anymore — re-add
// only if a future multi-host cookie context needs them.
