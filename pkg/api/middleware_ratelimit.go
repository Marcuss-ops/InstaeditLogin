// Package api — per-tier rate-limit middleware factories
// (SPRINT 2.2).
//
// The Router is composed with one factory per tier. Each factory
// resolves the scope (from the request's identity, IP, or a
// fixed string), calls RateLimitService.Check, sets the
// X-RateLimit-* response headers, and emits 429 + Retry-After on
// over-budget. The factories are intentionally small so the
// middleware chain is easy to audit.
//
// Composition order rationale (outermost → innermost):
//
//   1. rateLimiter.middleware (existing) — per-IP anon/auth budget
//      (in-memory). Catches "burst from one IP" before any other
//      auth / DB work.
//   2. CORS / logging.
//   3. (per-route) WorkspacePostLimit — per-workspace POST budget
//      (Postgres). Mounted on POST /api/v1/posts. Requires JWT
//      identity stamped by auth.Middleware.
//   4. (per-route) APIKeyReadLimit — per-API-key read budget
//      (Postgres). Mounted on the GETs of /api/v1/api-keys/*.
//      Requires ApiKeyIdentity stamped by Authenticator.
//   5. (per-route) MediaPresignLimit — per-endpoint coarse
//      backstop (in-memory). Mounted on POST /api/v1/media/presign.
//   6. (per-route) OAuthStartLimit — per-IP OAuth start budget
//      (in-memory). Mounted on GET /api/v1/auth/{provider}/login.
//
// All four factories use the same header contract; a 429 always
// carries Retry-After. The success path carries X-RateLimit-Limit,
// X-RateLimit-Remaining, and X-RateLimit-Reset for observability.

package api

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// rateLimitHeaders writes the X-RateLimit-* response headers.
// limit is the per-minute budget; remaining is the tokens left
// after this request; resetAt is the unix timestamp when the
// window refills. Always called before the response is written,
// even on 429.
func rateLimitHeaders(w http.ResponseWriter, limit int, remaining int, resetAt time.Time) {
	w.Header().Set("X-RateLimit-Limit", strconv.Itoa(limit))
	w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
	w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(resetAt.Unix(), 10))
}

// writeRateLimitExceeded writes a 429 with the X-RateLimit-* +
// Retry-After headers. The Retry-After is the seconds-until-reset
// rounded up so the client doesn't retry inside the same window.
func writeRateLimitExceeded(w http.ResponseWriter, limit int, resetAt time.Time) {
	retry := int(time.Until(resetAt).Seconds())
	if retry < 1 {
		retry = 1
	}
	rateLimitHeaders(w, limit, 0, resetAt)
	w.Header().Set("Retry-After", strconv.Itoa(retry))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusTooManyRequests)
	_, _ = w.Write([]byte(`{"error":"rate limit exceeded"}`))
}

// WorkspacePostLimit enforces 60 POSTs/min/workspace on the
// /api/v1/posts route group. Requires the JWT/cookie auth
// middleware to have stamped a UserIdentity; if no identity is
// present the middleware is a no-op (the auth layer will 401
// the request on its own).
func WorkspacePostLimit(svc *services.RateLimitService) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := auth.IdentityFromContext(r.Context())
			if id == nil || id.IsAPIKey() {
				// API-key callers don't write posts via this
				// endpoint (they publish through the publish
				// worker, not the dashboard). Bypass.
				next.ServeHTTP(w, r)
				return
			}
			tier := services.WorkspacePostLimit(id.WorkspaceID())
			allowed, remaining, resetAt, _ := svc.Check(r.Context(), tier, tier.Limit)
			if !allowed {
				writeRateLimitExceeded(w, tier.Limit, resetAt)
				return
			}
			rateLimitHeaders(w, tier.Limit, remaining, resetAt)
			next.ServeHTTP(w, r)
		})
	}
}

// APIKeyReadLimit enforces 600 reads/min/api-key on the
// /api/v1/api-keys/* route group. Requires the api-key
// Authenticator to have stamped an ApiKeyIdentity. JWT-authenticated
// dashboard users are passed through (they have a different
// path that goes through the per-workspace tier).
func APIKeyReadLimit(svc *services.RateLimitService) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := auth.IdentityFromContext(r.Context())
			if id == nil {
				next.ServeHTTP(w, r)
				return
			}
			if !id.IsAPIKey() || id.KeyID() <= 0 {
				// Not an API-key caller — JWT dashboard users
				// are not rate-limited at this tier.
				next.ServeHTTP(w, r)
				return
			}
			tier := services.APIKeyReadLimit(id.KeyID())
			allowed, remaining, resetAt, _ := svc.Check(r.Context(), tier, tier.Limit)
			if !allowed {
				writeRateLimitExceeded(w, tier.Limit, resetAt)
				return
			}
			rateLimitHeaders(w, tier.Limit, remaining, resetAt)
			next.ServeHTTP(w, r)
		})
	}
}

// MediaPresignLimit enforces 30/min on POST /api/v1/media/presign.
// In-memory coarse backstop — the same scope is shared across all
// callers. The real per-workspace / per-user limit is layered in
// when the per-workspace tier is added to /media (deferred).
func MediaPresignLimit(svc *services.RateLimitService) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tier := services.MediaPresignLimit()
			allowed, remaining, resetAt, _ := svc.Check(r.Context(), tier, tier.Limit)
			if !allowed {
				writeRateLimitExceeded(w, tier.Limit, resetAt)
				return
			}
			rateLimitHeaders(w, tier.Limit, remaining, resetAt)
			next.ServeHTTP(w, r)
		})
	}
}

// OAuthStartLimit enforces 20/min/IP on GET /api/v1/auth/{provider}/login.
// In-memory coarse backstop. The real per-IP gate is the edge
// tier (Cloudflare/reverse proxy) — see docs/OPERATIONS.md.
func OAuthStartLimit(svc *services.RateLimitService) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := extractIP(r)
			tier := services.OAuthStartLimit(ip)
			allowed, remaining, resetAt, _ := svc.Check(r.Context(), tier, tier.Limit)
			if !allowed {
				slog.Info("oauth start rate-limited", "ip", ip, "limit", tier.Limit)
				writeRateLimitExceeded(w, tier.Limit, resetAt)
				return
			}
			rateLimitHeaders(w, tier.Limit, remaining, resetAt)
			next.ServeHTTP(w, r)
		})
	}
}
