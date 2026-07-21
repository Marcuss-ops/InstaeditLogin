package api

import (
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/getsentry/sentry-go"
)

// Recovery middleware (Blocco #5.3).
//
// recoverMiddleware catches panics on the wrapped handler chain and
// surfaces them through one of two paths:
//
//   - When `r.sentryHub != nil` (operator set SENTRY_DSN at startup):
//     the panic value is cloned into a per-request Sentry hub
//     (so tags / user context don't leak across requests) and
//     sentry.CaptureException is called. The middleware also
//     writes 500 + JSON to the client.
//   - When `r.sentryHub == nil` (operator left SENTRY_DSN blank):
//     plain `defer recover()` + slog.Error + writeJSON 500. No
//     outbound traffic.
//
// We use the main sentry-go API (CurrentHub / Clone / Recover)
// rather than the legacy `sentryhttp` subpackage — that subpackage
// was removed in v0.40+. The main-package pattern is identical
// semantically: a per-request hub clone isolates context, and
// hub.Recover() preserves the goroutine frames in the SDK's
// native Stacktrace surface (CaptureException on a wrapped
// fmt.Errorf would only surface the formatted message with no
// Sentry-side frames).
//
// Placement in the chain: OUTERMOST, BEFORE rate-limiter / CORS /
// logging. The recovery middleware must wrap anything that might
// panic, including middleware bodies themselves (a buggy
// rate-limiter should not crash the process). Logged as `r.mux →
// recover(rateLimiter(corsMiddleware(loggingMiddleware(r.mux))))`
// in Setup().
//
// Per-request behaviour on panic:
//  1. recover() captures the panic value.
//  2. (optional) hub.Recover sends to Sentry (with goroutine stack).
//  3. slog.Error with debug.Stack() (local backup of the frames).
//  4. writeJSON 500 + {"error":"internal server error"}.
//  5. Handler chain stops; subsequent middlewares don't run.
//  6. Process does NOT exit; next request is served normally.
func (r *Router) recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		defer func() {
			rec := recover()
			if rec == nil {
				return
			}
			stack := debug.Stack()
			if r.sentryHub != nil {
				// Per-request hub clone: isolates breadcrumbs +
				// user context + tags from the global hub so
				// concurrent panics in different requests don't
				// cross-contaminate. We use hub.Recover (not
				// CaptureException) because Recover preserves the
				// goroutine stack via the SDK's
				// interfaces.Stacktrace machinery;
				// CaptureException on a wrapped fmt.Errorf would
				// surface only the formatted string with no
				// Sentry-side frames.
				hub := r.sentryHub.Clone()
				hub.Recover(rec)
			}
			slog.Error("recovery: panic in handler",
				"panic", rec,
				"path", req.URL.Path,
				"method", req.Method,
				"stack", string(stack))
			writeJSON(w, http.StatusInternalServerError,
				map[string]string{"error": "internal server error"})
		}()
		next.ServeHTTP(w, req)
	})
}

// WithSentryHub wires the Sentry hub (or nil) into the router so
// the recovery middleware can read it via Router.sentryHub.
// Production wiring in internal/bootstrap passes the captured
// sentry.CurrentHub() result when SENTRY_DSN is set; tests pass nil.
func WithSentryHub(hub *sentry.Hub) RouterOption {
	return func(r *Router) { r.sentryHub = hub }
}
