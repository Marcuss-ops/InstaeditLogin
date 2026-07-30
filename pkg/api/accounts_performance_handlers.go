// Package api — per-channel analytics endpoints.
//
// handleGetAccountPerformance is the HTTP boundary for
// GET /api/v1/accounts/{platform_account_id}/performance?days=7|14|28.
//
// After Step 4 the handler is intentionally thin: it parses the
// path id + ?days= query, reads identity from the request context,
// then delegates to ChannelAnalyticsService.GetChannelPerformance
// and maps that service's typed errors to HTTP status codes. All
// business logic (ownership, platform-type check, YouTube channel
// id resolution, period resolution, history + video fetching,
// trending rank, DTO assembly) lives in the service so future
// callers (a worker that runs the same computation off-request,
// an admin tool that inspects a different account, a CLI export)
// reuse the rules without duplicating them.
//
// Error map:
//
//   - 400 ?days= missing / unparseable / outside {7,14,28}
//   - 401 missing user identity (defence-in-depth on r.protected)
//   - 404 service.ErrAccountNotVisible (covers missing + cross-tenant)
//   - 422 service.ErrNotYouTubePlatform OR service.ErrYouTubeChannelIDMissing
//   - 501 channel analytics service not wired (operator misconfigured)
//   - 500 service error (logged with request_id)
package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/Marcuss-ops/InstaeditLogin/internal/analytics"
	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
)

// youtubePlatform is the wire-level platform string any OAuth-bound
// YouTube channel carries on PlatformAccount.Platform. Defined as a
// local const (rather than reading from internal/models) because
// every other handler in pkg/api that gates a YouTube-specific path
// uses the literal string — keeping this file aligned with that
// convention removes a future refactor's breakage risk if
// internal/models' exported Platform constants get renamed.
//
// Component-level ChannelAnalyticsService checks the same literal
// (see pkg/api/channel_analytics_service.go).
const youtubePlatform = "youtube"

// handleGetAccountPerformance returns the canonical per-channel
// analytics payload for the user's own account. The handler is
// post-Step-4 a thin orchestrator: parameter parsing, identity
// extraction, service delegation, error mapping. Every nontrivial
// rule lives in ChannelAnalyticsService.
func (r *Router) handleGetAccountPerformance(w http.ResponseWriter, req *http.Request) {
	if r.channelAnalyticsService == nil {
		writeError(w, http.StatusNotImplemented, "channel analytics service not configured")
		return
	}
	id, ok := parsePathIDAsInt64(w, req, "id")
	if !ok {
		return
	}
	identity := auth.IdentityFromContext(req.Context())
	if identity == nil || identity.UserID() <= 0 {
		// Defence-in-depth: r.protected() should have already
		// rejected this with 401. If a future refactor accidentally
		// wires this handler without the middleware, refuse the
		// request rather than silently returning any user's data.
		writeError(w, http.StatusUnauthorized, "missing user identity")
		return
	}
	days, ok := parseAnalyticsDays(w, req)
	if !ok {
		return
	}

	resp, err := r.channelAnalyticsService.GetChannelPerformance(
		req.Context(),
		identity.UserID(),
		identity.WorkspaceID(),
		id,
		days,
	)
	if err == nil {
		writeJSON(w, http.StatusOK, resp)
		return
	}

	// Map typed service errors to HTTP status codes. Always return
	// a stable JSON shape so the SPA can branch on the code, never
	// on the message text.
	switch {
	case errors.Is(err, ErrAccountNotVisible):
		// No existence leak: 404 covers both missing and
		// cross-tenant probes (the service cannot distinguish them
		// to the caller; that's the security contract).
		writeError(w, http.StatusNotFound, "account not found")
	case errors.Is(err, ErrNotYouTubePlatform):
		writeError(w, http.StatusUnprocessableEntity,
			"channel is not a YouTube account; the per-channel analytics view is YouTube-only")
	case errors.Is(err, ErrYouTubeChannelIDMissing):
		writeError(w, http.StatusUnprocessableEntity,
			"youtube channel id not bound; re-link the channel")
	case errors.Is(err, analytics.ErrInvalidPeriod):
		// parseAnalyticsDays already screens this on the HTTP
		// boundary, but the service re-checks defensively. If it
		// somehow gets here the wire shape is still 400.
		writeError(w, http.StatusBadRequest,
			"invalid days: "+strconv.Itoa(days)+" not in {7,14,28}")
	default:
		logAndError(w, req, "channel analytics service failed", err,
			"platform_account_id", id, "days", days)
	}
}

// parseAnalyticsDays extracts and validates the ?days= query
// parameter against the canonical {7, 14, 28} closed set. The
// closure of this set is asserted by analytics.IsValidPeriod and the
// period_resolver_test.go contract; silently falling back to a
// default would mask client misconfiguration, so a missing /
// unparsable / out-of-range value is a 400, not a default.
func parseAnalyticsDays(w http.ResponseWriter, req *http.Request) (int, bool) {
	raw := req.URL.Query().Get("days")
	if raw == "" {
		writeError(w, http.StatusBadRequest,
			"missing days query parameter; must be one of 7, 14, 28")
		return 0, false
	}
	days, err := strconv.Atoi(raw)
	if err != nil {
		writeError(w, http.StatusBadRequest,
			"invalid days: "+raw+" (must be a positive integer ∈ {7,14,28})")
		return 0, false
	}
	if !analytics.IsValidPeriod(days) {
		writeError(w, http.StatusBadRequest,
			"invalid days: "+strconv.Itoa(days)+" must be one of 7, 14, 28")
		return 0, false
	}
	return days, true
}
