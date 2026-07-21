package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// Setup wires the application's route table.  Route registration is
// now delegated to bounded-context modules; this method only keeps the
// top-level cross-cutting concerns (health/readiness, metrics, CORS,
// rate-limiting, logging, recovery and security headers).
func (r *Router) Setup() http.Handler {
	r.mux = chi.NewRouter()

	NewAdminModule(r).Register(r.mux)
	NewVeloxModule(r).Register(r.mux)

	// Public / health probes are mounted before the auth module so the
	// route table stays easy to scan top-down.
	r.mux.Method(http.MethodGet, "/api/v1/health", http.HandlerFunc(r.handleHealth))
	r.mux.Method(http.MethodGet, "/ready", http.HandlerFunc(r.handleReady))

	NewAuthModule(r).Register(r.mux)
	NewMediaModule(r).Register(r.mux)
	NewPublishingModule(r).Register(r.mux)
	NewBillingModule(r).Register(r.mux)

	r.mux.Method(http.MethodGet, "/api/v1/metrics", http.HandlerFunc(r.handleMetrics))

	// FASE 1.2: rate limiter is the outermost middleware so it
	// protects ALL routes (public + protected) from abuse.
	//
	// Blocco #5.3 — the panic-catching recovery wrapper sits
	// OUTSIDE the rate-limit + CORS + logging chain so panics
	// inside ANY of those middleware bodies (not just the
	// terminal handler) get caught. The wrapper is a no-op for
	// happy-path requests (passthrough to rate-limiter) and
	// recovers + writes 500 only on panic.
	// securityHeaders is OUTSIDE the rate-limit + CORS + logging chain
	// so its decisions are independent of those middlewares' behaviour.
	// It is INSIDE recover so a panic inside its handler still gets
	// caught + logged + translated to a 500.
	rateLimitAndBelow := r.securityHeadersMiddleware(
		r.rateLimiter.middleware(r.corsMiddleware(r.loggingMiddleware(r.mux))),
	)
	return r.recoverMiddleware(rateLimitAndBelow)
}
