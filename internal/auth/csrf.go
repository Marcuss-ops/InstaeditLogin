// Package auth — CSRF protection via double-submit cookie.
//
// Threat model: the access JWT lives in an HttpOnly cookie, which is
// automatically attached by the browser on cross-origin XHR when the
// SPA sends credentials:'include' AND the server returns
// Access-Control-Allow-Credentials:true. Without CSRF, any malicious
// page could POST to /api/v1/posts with a victim's cookie attached.
//
// Double-submit pattern:
//   1. On the first cookie-authenticated response, the server sets a
//      NON-HttpOnly cookie `csrf_token` (readable by document.cookie).
//   2. The SPA reads it via document.cookie and echoes it on every
//      non-safe request (POST/PUT/DELETE/PATCH) as header X-CSRF-Token.
//   3. This middleware rejects any non-safe SESSION-COOKIE-BEARING
//      request whose cookie and header do not match (or where either
//      is missing).
//
// Exemptions:
//   - Safe methods (GET, HEAD, OPTIONS) never require CSRF.
//   - Bearer-token requests (Authorization: Bearer …) skip CSRF; they
//     are not cookie-authenticated and the developer who minted the
//     token is presumed to also be configuring the CORS origin.
//   - Unauthenticated requests (NO session cookie AND no Bearer):
//     auth.Middleware will reject them with 401. CSRF is moot when
//     there is no session-cookie context to authenticate — enforcing
//     CSRF here would just turn a 401 into a 403 for a request the
//     server already refuses to process. This narrows CSRF defense
//     precisely to the attack vector: state mutations leveraging an
//     EXISTING session cookie (the cross-site forgery scenario).
//   - /api/v1/auth/refresh and the OAuth callback endpoints (cookie
//     was just set on the response that pointed the user at them;
//     there is no pre-existing cookie-authenticated session yet).
//
// The CSRF token is regenerated on every successful login to ensure
// the post-login token cannot be guessed by a pre-login attacker.

package auth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
)

// safeMethods are the HTTP methods exempt from CSRF.
var safeMethods = map[string]struct{}{
	http.MethodGet:     {},
	http.MethodHead:    {},
	http.MethodOptions: {},
}

// CSRFConfig configures the CSRF middleware.
type CSRFConfig struct {
	// Secure toggles the `Secure` flag on the csrf_token cookie.
	// Should be true in production (HTTPS).
	Secure bool
	// Path is the cookie Path. Defaults to "/".
	Path string
	// CookieDomain optionally pins the cookie to a specific domain.
	CookieDomain string
	// SameSite defaults to Lax (recommended for same-origin SPA) or
	// None for cross-origin SPA + cross-site cookie. Empty string
	// defaults to Lax.
	SameSite http.SameSite
}

// NewCSRF returns a middleware that enforces the double-submit-cookie
// rule on non-safe requests. safeNext is called for safe methods;
// unsafeNext is called for non-safe methods when the CSRF check
// passes. Both share the same response writer / request.
func NewCSRF(cfg CSRFConfig, next http.Handler) http.Handler {
	if cfg.Path == "" {
		cfg.Path = "/"
	}
	sameSite := cfg.SameSite
	if sameSite == 0 {
		sameSite = http.SameSiteLaxMode
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Safe methods: just pass through.
		if _, ok := safeMethods[r.Method]; ok {
			next.ServeHTTP(w, r)
			return
		}
		// Bearer-token callers skip CSRF.
		if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
			next.ServeHTTP(w, r)
			return
		}
		// Unauthenticated callers (no SessionCookieName cookie) skip
		// CSRF — auth.Middleware will 401 them. CSRF defense is only
		// meaningful when a session cookie is present (the attack
		// vector is a cross-site request that the browser attaches
		// the victim's cookie to). Without a session cookie, there
		// is no auth context for CSRF to protect.
		if _, err := r.Cookie(SessionCookieName); err != nil {
			next.ServeHTTP(w, r)
			return
		}
		// Cookie callers: cookie value must equal header value.
		c, err := r.Cookie(CSRFTokenCookieName)
		if err != nil || c.Value == "" {
			rejectCSRF(w, "missing_csrf_cookie")
			return
		}
		hdr := r.Header.Get(CSRFHeader)
		if hdr == "" {
			rejectCSRF(w, "missing_csrf_header")
			return
		}
		// constant-time compare
		if !secureEqual(c.Value, hdr) {
			rejectCSRF(w, "csrf_mismatch")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// SetCSRFToken ensures the response sets a fresh csrf_token cookie
// (overwriting any prior one). The token is hex(rand 32B). Used by
// the login / refresh / exchange handlers.
func SetCSRFToken(w http.ResponseWriter, cfg CSRFConfig) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	tok := hex.EncodeToString(b)
	sameSite := cfg.SameSite
	if sameSite == 0 {
		sameSite = http.SameSiteLaxMode
	}
	http.SetCookie(w, &http.Cookie{
		Name:     CSRFTokenCookieName,
		Value:    tok,
		Path:     cfg.Path,
		Domain:   cfg.CookieDomain,
		HttpOnly: false,
		Secure:   cfg.Secure,
		SameSite: sameSite,
		// No MaxAge: this is a session cookie. SPA may persist a copy
		// in localStorage as a recovery but the server only enforces
		// presence + value match on each request.
	})
	return tok, nil
}

// ClearCSRFToken deletes the csrf_token cookie. Called on logout.
func ClearCSRFToken(w http.ResponseWriter, cfg CSRFConfig) {
	http.SetCookie(w, &http.Cookie{
		Name:     CSRFTokenCookieName,
		Value:    "",
		Path:     cfg.Path,
		Domain:   cfg.CookieDomain,
		HttpOnly: false,
		Secure:   cfg.Secure,
		SameSite: cfg.SameSite,
		MaxAge:   -1,
	})
}

func rejectCSRF(w http.ResponseWriter, reason string) {
	http.Error(w, "csrf rejected: "+reason, http.StatusForbidden)
}

// secureEqual is constant-time; avoids timing-leak. Both inputs are
// hex strings of equal expected length.
func secureEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := 0; i < len(a); i++ {
		v |= a[i] ^ b[i]
	}
	return v == 0
}

// ErrNoCSRFToken is exported for tests / handlers.
var ErrNoCSRFToken = errors.New("no csrf token")
