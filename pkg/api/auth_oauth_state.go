package api

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"time"
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

func OAuthStateCookieName(provider string) string { return oauthStateCookiePrefix + provider }

// OAuthStateExpectedChannelCookieName returns the sibling cookie name used
// when /api/v1/auth/{provider}/login is invoked with
// ?expected_channel_id=. The cookie is HttpOnly Secure SameSite=Lax with
// MaxAge matching the state cookie; it's deleted together with the state
// cookie on successful verifyOAuthState. Kept outside the URL state
// parameter (which Google echoes back verbatim, so we keep it a pure
// CSRF nonce).
func OAuthStateExpectedChannelCookieName(provider string) string {
	return oauthStateCookiePrefix + provider + oauthStateExpectedChannelSuffix
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
