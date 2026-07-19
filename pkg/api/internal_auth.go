package api

import (
	"crypto/subtle"
	"log/slog"
	"net/http"
	"strings"
)

// internalVeloxAuth is the service-to-service Bearer-token gate
// for /internal/v1/* routes. Velox (and future external
// integrations like Dropbox) authenticate with a static shared
// secret stamped at deploy time as the VELOX_API_TOKEN env var;
// the constant-time compare happens here so a deployed + Rotated
// token stops being valid the moment the env var rolls.
//
// Why a SEPARATE middleware from r.protected / r.auth.Middleware:
//
//   1. r.protected expects a JWT (cookie or Authorization Bearer
//      shaped like an InstaEdit JWT). A static shared secret
//      has no signature, no expiry, no per-tenant claim — the
//      JWT parser path would either reject or, worse, succeed
//      against a forged token.
//   2. CSRF middleware (cookie-jar) doesn't apply — this is
//      server-to-server with no browser involvement, so the
//      CSRF nonce cookie is noise (Velox can't access our
//      cookies anyway).
//   3. /admin/* uses a JWT-deposited Identity.IsAdmin() gate;
//      Velox has no user context, so admin is the wrong axis.
//
// Failure modes:
//
//   - VELOX_API_TOKEN empty at process start → 503 Service
//     Unavailable + an error log so operators can fix the env
//     var without chasing a mysterious 403.
//   - Authorization header absent OR not "Bearer <token>" →
//     401 MissingToken.
//   - Authorization header present but token mismatches →
//     401 TokenMismatch. Same status code as missing so a
//     peer probing the endpoint can't tell "no attempt"
//     from "wrong attempt" — reduces the oracle surface.
//
// Constant-time compare (crypto/subtle.ConstantTimeCompare)
// prevents timing-based token recovery. The two strings MUST
// be the same length for the function to return 1; unequal
// lengths short-circuit to 0. Length leak is acceptable here
// because the legitimate token has a fixed-length random hex
// (32 chars from a 16-byte secret → boot-time rotation
// lengthens if a higher-entropy deployment requires it).
//
// Response format parity: 401/503 paths use the standard
// writeError JSON envelope so Velox gets a uniform
// application/json response shape regardless of which path
// fired (unlike http.Error which writes text/plain and
// breaks contract parity with the handler paths).
func (r *Router) internalVeloxAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if r.veloxAPIToken == "" {
			slog.Error("velox service auth not configured: VELOX_API_TOKEN empty — refusing request")
			writeError(w, http.StatusServiceUnavailable, "service auth not configured")
			return
		}
		authHeader := req.Header.Get(authHeaderAuthorization)
		if authHeader == "" {
			writeError(w, http.StatusUnauthorized, "missing Authorization header")
			return
		}
		const prefix = "Bearer "
		if len(authHeader) <= len(prefix) ||
			!strings.EqualFold(authHeader[:len(prefix)], prefix) {
			writeError(w, http.StatusUnauthorized, "malformed Authorization header")
			return
		}
		token := authHeader[len(prefix):]
		// subtle.ConstantTimeCompare on byte slices prevents
		// timing-based recovery. Length-mismatch short-circuits
		// to 0 (acceptable per file-level doc-comment).
		provided := []byte(token)
		expected := []byte(r.veloxAPIToken)
		if subtle.ConstantTimeCompare(provided, expected) != 1 {
			// Same status code as the missing-token path so
			// an attacker probing the endpoint can't tell
			// "no attempt" from "wrong attempt" — reduces
			// the oracle surface.
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, req)
	})
}

// authHeaderAuthorization is the canonical HTTP header name for
// service-to-service bearer auth. Kept package-private so the
// middleware can't be accidentally called with a wrong casing
// somewhere else in pkg/api (HTTP header names are
// case-insensitive but case matters in our test fixtures).
const authHeaderAuthorization = "Authorization"

// WithVeloxAPIToken wires the static shared secret for the
// Velox service-to-service routes. The token is loaded from
// env at boot (cmd/server/main.go reads cfg.VeloxAPIToken via
// internal/config) and passed via this option — main.go should
// NOT read the env directly in main; it should pipe through
// internal/config then this Router option so tests can inject
// an in-memory token without touching globals.
//
// When a Router constructed via this option has VELOX_API_TOKEN
// empty AND the /internal/v1 routes wired, the setup() helper
// refuses to register the routes — boot-time fail-fast so
// operators notice the misconfiguration rather than discovering
// it at first traffic.
func WithVeloxAPIToken(token string) RouterOption {
	return func(r *Router) { r.veloxAPIToken = token }
}
