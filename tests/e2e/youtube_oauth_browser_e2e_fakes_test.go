//go:build e2e

package e2e

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

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
			"scope":         "https://www.googleapis.com/auth/youtube.upload https://www.googleapis.com/auth/youtube.readonly https://www.googleapis.com/auth/youtube.force-ssl openid email profile",
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
	q.Set("scope", "https://www.googleapis.com/auth/youtube.upload https://www.googleapis.com/auth/youtube.readonly https://www.googleapis.com/auth/youtube.force-ssl openid email profile")
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

func (a *browserSmokeChannelAuthorizer) AuthorizeChannel(_ context.Context, accountID int64, expectedChannelID string, _ string, scopes []string, _ ...*models.TokenData) (int64, error) {
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
