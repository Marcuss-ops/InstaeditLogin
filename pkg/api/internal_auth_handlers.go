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
//  1. r.protected expects a JWT (cookie or Authorization Bearer
//     shaped like an InstaEdit JWT). A static shared secret
//     has no signature, no expiry, no per-tenant claim — the
//     JWT parser path would either reject or, worse, succeed
//     against a forged token.
//  2. CSRF middleware (cookie-jar) doesn't apply — this is
//     server-to-server with no browser involvement, so the
//     CSRF nonce cookie is noise (Velox can't access our
//     cookies anyway).
//  3. /admin/* uses a JWT-deposited Identity.IsAdmin() gate;
//     Velox has no user context, so admin is the wrong axis.
//
// Failure modes:
//
//   - VELOX_API_TOKEN empty at process start → 503 Service
//     Unavailable + an error log so operators can fix the env
//     var without chasing a mysterious 403.
//
//   - Authorization header absent OR malformed (not "Bearer
//     <token>" shape, case-insensitive) → 401 Unauthorized
//     with the JSON body `{"error":"missing or malformed
//     Authorization header"}`. Semantically the peer has not
//     attempted authentication, so 401 — "you need to
//     authenticate" — is the right code.
//
//   - Authorization header well-formed but token mismatches
//     → 403 Forbidden with the JSON body `{"error":"token
//     mismatch"}`. The peer DID authenticate (presented a
//     well-formed header with a credential), but the
//     credential is wrong. RFC 7235 maps this to 403.
//
//     Why 401/403 rather than collapsing both to 401: the
//     Velox contract treats 401 as a per-request retry hint
//     ("missing header — maybe your client library is set up
//     wrong, don't bust the queue retrying") and 403 as a
//     deploy-time escalations ("token doesn't match — page
//     the operator and rotate VELOX_API_TOKEN on both
//     sides"). Operators read 403 spikes in the metrics
//     pipeline and page immediately; 401 spikes are
//     diagnostic-only and don't page. The bucket separation
//     matters more here than the small oracle surface of
//     "yes/no token guess".
//
// Constant-time compare (crypto/subtle.ConstantTimeCompare)
// prevents timing-based token recovery. The two strings MUST
// be the same length for the function to return 1; unequal
// lengths short-circuit to 0. Length leak is acceptable here
// because the legitimate token has a fixed-length random hex
// (32 chars from a 16-byte secret → boot-time rotation
// lengthens if a higher-entropy deployment requires it).
//
// Response format parity: 401/403/503 paths use the standard
// writeError JSON envelope so Velox gets a uniform
// application/json response shape regardless of which path
// fired (unlike http.Error which writes text/plain and
// breaks contract parity with the handler paths).
//
// Forward-compat: the 401-missing / 403-mismatch split is
// Velox-specific (maps "you need to authenticate" vs "you
// tried with a wrong credential" to RFC 7235 + the Velox
// retry/escalation contract). This DEVIATES from the
// GitHub/Stripe/AWS convention where wrong API keys return
// 401. The internal_velox contract doc (docs/ENDPOINTS.md
// "Internal /internal/v1 contract") MUST tag this difference
// explicitly so a future provider (Dropbox is mentioned in
// the /internal/v1 design doc) that drops in expecting
// standard HTTP semantics knows to opt back into 401-for-both
// via a dedicated `WithWrongTokenStatus(http.StatusUnauthorized)`
// Router option — see TODO-velox-auth-status-config follow-up.
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
		//
		// 403 (NOT 401) here per the failure-mode rationale in
		// the file-level doc-comment: the peer DID present a
		// well-formed Authorization header, so this maps to
		// "authenticated attempt rejected" rather than
		// "authentication required".
		provided := []byte(token)
		expected := []byte(r.veloxAPIToken)
		if subtle.ConstantTimeCompare(provided, expected) != 1 {
			writeError(w, http.StatusForbidden, "token mismatch")
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

// WithCsrfMiddleware wires the CSRF middleware used to wrap
// the user-facing /api/v1/integrations/velox/destinations
// endpoint. Production wiring in cmd/server/main.go will
// pass auth.NewCSRF(r.csrfConfig(), http.NotFoundHandler())
// so the registered route gets the project's canonical CSRF
// chain. Tests pass a passthrough func to bypass CSRF
// verification. When empty (nil), the route is mounted
// WITHOUT CSRF — acceptable in dev/staging; production MUST
// wire this.
func WithCsrfMiddleware(mw func(http.Handler) http.Handler) RouterOption {
	return func(r *Router) { r.csrfMiddleware = mw }
}

// WithAuthMiddleware wires the JWT-auth middleware used to
// wrap the user-facing /api/v1/integrations/velox/destinations
// endpoint. Production wiring in cmd/server/main.go will
// pass r.auth.Middleware. Tests pass a passthrough func
// to bypass JWT verification (and inject Identity directly
// into request context via auth.IdentityToContext helper).
// Same nil-on-not-wired semantics as WithCsrfMiddleware.
func WithAuthMiddleware(mw func(http.Handler) http.Handler) RouterOption {
	return func(r *Router) { r.authMiddleware = mw }
}
