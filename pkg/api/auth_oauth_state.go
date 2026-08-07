package api

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

const (
	oauthStateCookiePrefix = "oauth_state_"
	oauthStateMaxAge       = 10 * time.Minute
)

// oauthStateExpectedChannelSuffix is appended to oauth_state_{provider}
// to form the sibling cookie that round-trips an optional
// expected_channel_id across the OAuth callback. Kept distinct from the
// state cookie (which holds the pure-CSRF nonce) so the URL state param
// remains a 32-byte base64url random — verified by
// TestHandleLogin_RedirectsToProviderURL (length 43 invariant).
const oauthStateExpectedChannelSuffix = "_expected_channel"

// oauthStateOAuthClientSuffix is appended to oauth_state_{provider} to
// form the sibling cookie that round-trips the YouTube OAuth Client
// Pool client key (youtube_pool_a / youtube_pool_b) from handleLogin to
// the callback. The pool JWT state deliberately does NOT carry the
// client key (jwt_oauth_state.go): the key lives only in this HttpOnly
// cookie, bound to the flow by the "<jti>:<clientKey>" prefix, so the
// callback exchanges the code with EXACTLY the client that built the
// consent URL.
const oauthStateOAuthClientSuffix = "_oauth_client"

// oauthStateRedirectSuffix is appended to oauth_state_{provider} to
// form the sibling cookie that round-trips an optional ?redirect=/app/...
// SPA path (e.g. the Groups "Aggiungi canale" button) from handleLogin
// to the callback. The callback then lands the operator on that page
// instead of the default /app/linking. The URL state param stays a pure
// CSRF nonce; this HttpOnly cookie is the only path for the return
// target to round-trip. Single-use: deleted on read, exactly like the
// expected-channel sibling cookie.
const oauthStateRedirectSuffix = "_redirect"

func OAuthStateCookieName(provider string) string {
	return oauthStateCookiePrefix + models.NormalizePlatformIdentifier(provider)
}

// OAuthStateExpectedChannelCookieName returns the sibling cookie name used
// when /api/v1/auth/{provider}/login is invoked with
// ?expected_channel_id=. The cookie is HttpOnly Secure SameSite=Lax with
// MaxAge matching the state cookie; it's deleted together with the state
// cookie on successful verifyOAuthState. Kept outside the URL state
// parameter (which Google echoes back verbatim, so we keep it a pure
// CSRF nonce).
func OAuthStateExpectedChannelCookieName(provider string) string {
	return oauthStateCookiePrefix + models.NormalizePlatformIdentifier(provider) + oauthStateExpectedChannelSuffix
}

// OAuthStateOAuthClientCookieName returns the sibling cookie name used
// by the YouTube OAuth Client Pool flow: it round-trips the pool client
// key selected at login time to the callback. For provider "youtube"
// this is oauth_state_youtube_oauth_client. The cookie is HttpOnly
// Secure SameSite=None (cross-site redirect back from Google) with
// MaxAge matching the signed state; it's deleted on the callback once
// the key has been consumed. The value format is "<jti>:<clientKey>"
// where jti is the single-use nonce of the signed oauth-flow state —
// the prefix binds the key to THIS exact flow so a stale sibling cookie
// from a previous OAuth round-trip cannot steer a new flow to the wrong
// client.
func OAuthStateOAuthClientCookieName(provider string) string {
	return oauthStateCookiePrefix + models.NormalizePlatformIdentifier(provider) + oauthStateOAuthClientSuffix
}

// OAuthStateRedirectCookieName returns the sibling cookie name used
// when /api/v1/auth/{provider}/login is invoked with a validated
// ?redirect=/app/... path: it round-trips the desired post-OAuth SPA
// landing page to the callback. Same HttpOnly / Secure / SameSite=None
// attributes as the other sibling cookies; deleted on read.
func OAuthStateRedirectCookieName(provider string) string {
	return oauthStateCookiePrefix + models.NormalizePlatformIdentifier(provider) + oauthStateRedirectSuffix
}

// isValidOAuthRedirectPath restricts post-OAuth return targets to
// same-origin SPA paths under /app/. Everything else (external URLs,
// protocol-relative hosts, backslashes, query strings, fragments,
// path traversal) is rejected so a crafted ?redirect= can never turn
// the callback into an open redirect. ".." / "." segments are
// rejected explicitly: the browser would resolve /app/../admin to
// /admin before the SPA router ever sees it, which defeats the
// prefix check.
func isValidOAuthRedirectPath(s string) bool {
	if len(s) < len("/app/") || len(s) > 256 {
		return false
	}
	if !strings.HasPrefix(s, "/app/") {
		return false
	}
	for _, segment := range strings.Split(s, "/") {
		if segment == ".." || segment == "." {
			return false
		}
	}
	for _, r := range s[len("/app/"):] {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '/', r == '-', r == '_', r == '.':
		default:
			return false
		}
	}
	return true
}

// setOAuthRedirectCookie writes the sibling oauth_state_{provider}
// _redirect cookie that round-trips the validated ?redirect= SPA path
// from handleLogin to the callback. Issued only when handleLogin saw a
// valid /app/... path; cleared (single-use) on read by
// verifyOAuthRedirectCookie, and re-cleared by every login that does
// NOT carry a ?redirect= so a stale cookie from a failed flow can never
// steer a new flow's landing page.
func setOAuthRedirectCookie(w http.ResponseWriter, provider, redirectPath, cookieDomain string) {
	http.SetCookie(w, &http.Cookie{
		Name: OAuthStateRedirectCookieName(provider), Value: redirectPath, Path: "/",
		Domain: cookieDomain, HttpOnly: true, Secure: true, SameSite: http.SameSiteNoneMode,
		MaxAge: int(oauthStateMaxAge.Seconds()),
	})
}

// clearOAuthRedirectCookie deletes the sibling redirect cookie.
// handleLogin calls it on every flow that carries no (valid)
// ?redirect= so the return target is ALWAYS reset at login time —
// otherwise a stale cookie left behind by a failed flow (exchange
// error, 409/422 attach) would redirect the NEXT flow's callback to
// the wrong page.
func clearOAuthRedirectCookie(w http.ResponseWriter, provider, cookieDomain string) {
	http.SetCookie(w, &http.Cookie{
		Name: OAuthStateRedirectCookieName(provider), Value: "", Path: "/",
		Domain: cookieDomain, HttpOnly: true, Secure: true, SameSite: http.SameSiteNoneMode,
		MaxAge: -1, Expires: time.Unix(1, 0),
	})
}

// verifyOAuthRedirectCookie reads + deletes the sibling redirect cookie
// and returns the validated SPA path (or "" when absent/invalid). The
// value is re-validated on read so a forged cookie cannot steer the
// callback to an external host; a bad value silently falls back to the
// default /app/linking landing. Single-use: the cookie is cleared on
// read so a replay of the same callback cannot re-trigger the redirect.
func verifyOAuthRedirectCookie(w http.ResponseWriter, req *http.Request, provider, cookieDomain string) string {
	c, err := req.Cookie(OAuthStateRedirectCookieName(provider))
	if err != nil || c.Value == "" {
		return ""
	}
	clearOAuthRedirectCookie(w, provider, cookieDomain)
	if !isValidOAuthRedirectPath(c.Value) {
		return ""
	}
	return c.Value
}

// isValidYouTubeChannelID returns true for strings that look like a
// YouTube channel ID (e.g. UC_x5XG1OV2P6uZZ5FSM9Ttw): "UC" + 22 chars,
// drawn from the URL-safe alphabet [A-Za-z0-9_-]. Used server-side to
// reject malformed expected_channel_id query params before storing them
// in the round-trip cookie. Failure mode: silently drop the hint — the
// OAuth flow still proceeds without the binding assertion; the actual
// binding check happens inside attachDiscoveredAccounts.
func isValidYouTubeChannelID(s string) bool {
	if len(s) != 24 || !strings.HasPrefix(s, "UC") {
		return false
	}
	for _, r := range s[2:] {
		switch {
		case r >= 'A' && r <= 'Z',
			r >= 'a' && r <= 'z',
			r >= '0' && r <= '9',
			r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

func generateOAuthState(w http.ResponseWriter, provider, expectedChannelID, cookieDomain string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("oauth state rand failed: %w", err)
	}
	state := base64.RawURLEncoding.EncodeToString(b)
	http.SetCookie(w, &http.Cookie{
		Name: OAuthStateCookieName(provider), Value: state, Path: "/",
		Domain: cookieDomain, HttpOnly: true, Secure: true, SameSite: http.SameSiteNoneMode,
		MaxAge: int(oauthStateMaxAge.Seconds()),
	})
	if expectedChannelID != "" {
		// Sibling cookie carries the operator-supplied binding hint.
		// The URL state param stays a pure CSRF nonce (Google echoes
		// it back verbatim) and this HttpOnly cookie is the only path
		// for the hint to round-trip. Issued only when handleLogin
		// saw a validated ?expected_channel_id=; deleted on
		// verifyOAuthState.
		//
		// Value format: "<state_nonce>:<channelID>". The state prefix
		// binds the channel hint to the SAME flow — a stale sibling
		// cookie from a previous OAuth round-trip cannot silently
		// leak into a new one (e.g., operator clicked Connect without
		// ?expected_channel_id= after a previous abandoned flow).
		http.SetCookie(w, &http.Cookie{
			Name: OAuthStateExpectedChannelCookieName(provider), Value: state + ":" + expectedChannelID, Path: "/",
			Domain: cookieDomain, HttpOnly: true, Secure: true, SameSite: http.SameSiteNoneMode,
			MaxAge: int(oauthStateMaxAge.Seconds()),
		})
	}
	return state, nil
}

// verifyOAuthState checks the CSRF nonce against the
// oauth_state_{provider} cookie and (if present) reads + deletes the
// sibling oauth_state_{provider}_expected_channel cookie. The returned
// expectedChannelID is "" when no hint was set; a non-empty value means
// the operator told us which channel/resource the OAuth grant must
// bind to.

func verifyOAuthState(w http.ResponseWriter, req *http.Request, provider, stateParam, cookieDomain string) (string, error) {
	// During the domain migration browsers can temporarily send both the
	// legacy parent-domain cookie and the current host/domain cookie. Search
	// all same-name cookies for the exact nonce instead of trusting whichever
	// one net/http returns first.
	found := false
	for _, c := range req.Cookies() {
		if c.Name == OAuthStateCookieName(provider) && subtle.ConstantTimeCompare([]byte(c.Value), []byte(stateParam)) == 1 {
			found = true
			break
		}
	}
	if !found {
		if _, err := req.Cookie(OAuthStateCookieName(provider)); err != nil {
			return "", fmt.Errorf("oauth state cookie missing for provider %q", provider)
		}
		return "", fmt.Errorf("oauth state mismatch for provider %q (CSRF protection)", provider)
	}
	http.SetCookie(w, &http.Cookie{
		Name: OAuthStateCookieName(provider), Value: "", Path: "/",
		Domain: cookieDomain, HttpOnly: true, Secure: true, SameSite: http.SameSiteNoneMode,
		MaxAge: -1, Expires: time.Unix(1, 0),
	})
	expectedChannelID := ""
	if ec, ecErr := req.Cookie(OAuthStateExpectedChannelCookieName(provider)); ecErr == nil && ec.Value != "" {
		// Strip the "<state_nonce>:" prefix; only return the channel ID
		// when it matches the current flow's just-verified state
		// nonce. A stale sibling cookie from a previous OAuth
		// round-trip (different state) is silently ignored — the
		// operator must re-issue ?expected_channel_id= to bind it
		// explicitly. Defence-in-depth on top of the bearer-validated
		// channels.list(mine=true) check inside attachDiscoveredAccounts.
		// Also run the extracted channel ID through the same
		// isValidYouTubeChannelID gate handleLogin uses, so a malformed
		// value (e.g. someone forged "<state>:<bogus>:<extra>") cannot
		// pass through here — it would always 409 via the channels.list
		// mismatch anyway, but the gate keeps the error surface clean.
		if id, ok := strings.CutPrefix(ec.Value, stateParam+":"); ok && isValidYouTubeChannelID(id) {
			expectedChannelID = id
		}
		http.SetCookie(w, &http.Cookie{
			Name: OAuthStateExpectedChannelCookieName(provider), Value: "", Path: "/",
			Domain: cookieDomain, HttpOnly: true, Secure: true, SameSite: http.SameSiteNoneMode,
			MaxAge: -1, Expires: time.Unix(1, 0),
		})
	}
	return expectedChannelID, nil
}

// setOAuthClientCookie writes the sibling oauth_state_{provider}
// _oauth_client cookie that round-trips the YouTube OAuth Client Pool
// client key selected at login time. Value format is "<jti>:<clientKey>"
// — the jti of the signed oauth-flow state (returned by
// IssueOAuthFlowState) binds the key to THIS flow, mirroring the
// expected-channel sibling cookie. Same HttpOnly/Secure/SameSite=None
// attributes as the expected-channel cookie so the callback (arriving
// via the Google redirect) can read it.
func setOAuthClientCookie(w http.ResponseWriter, provider, jti, clientKey, cookieDomain string) {
	http.SetCookie(w, &http.Cookie{
		Name: OAuthStateOAuthClientCookieName(provider), Value: jti + ":" + clientKey, Path: "/",
		Domain: cookieDomain, HttpOnly: true, Secure: true, SameSite: http.SameSiteNoneMode,
		MaxAge: int(oauthStateMaxAge.Seconds()),
	})
}

// verifyOAuthClientCookie reads the sibling oauth_state_{provider}
// _oauth_client cookie in the callback and returns the pool client key
// it carries, but ONLY when the "<jti>:" prefix matches the jti of the
// just-verified oauth-flow state. A missing cookie, a stale cookie from
// a previous flow, or a forged value is rejected — the callback must
// fail closed (never exchange with a guessed/wrong client). The cookie
// is deleted on read, matching the expected-channel sibling behaviour:
// single-use, exactly like the state cookie itself, and never allowed
// to linger for the rest of its MaxAge.
func verifyOAuthClientCookie(w http.ResponseWriter, req *http.Request, provider, jti, cookieDomain string) (string, error) {
	deleteClientCookie := func() {
		http.SetCookie(w, &http.Cookie{
			Name: OAuthStateOAuthClientCookieName(provider), Value: "", Path: "/",
			Domain: cookieDomain, HttpOnly: true, Secure: true, SameSite: http.SameSiteNoneMode,
			MaxAge: -1, Expires: time.Unix(1, 0),
		})
	}
	ec, ecErr := req.Cookie(OAuthStateOAuthClientCookieName(provider))
	if ecErr != nil || ec.Value == "" {
		return "", fmt.Errorf("oauth client cookie missing for provider %q", provider)
	}
	deleteClientCookie()
	clientKey, ok := strings.CutPrefix(ec.Value, jti+":")
	if !ok || clientKey == "" || strings.Contains(clientKey, ":") {
		return "", fmt.Errorf("oauth client cookie does not match the current oauth flow for provider %q", provider)
	}
	return clientKey, nil
}
