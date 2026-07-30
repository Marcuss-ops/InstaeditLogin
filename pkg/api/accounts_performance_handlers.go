// Package api — per-channel analytics endpoints.
//
// handleGetAccountPerformance replaces the legacy v0
// (accountPerformanceResponse) implementation with the canonical
// ChannelPerformanceResponse DTO shipped in Step 1 (analytics/contract.go)
// and the period resolver shipped in Step 3 (analytics/period_resolver.go).
//
// The handler is intentionally thin: it only authenticates, parses
// the days parameter, owns the period resolution and workspace
// ownership check, then delegates the data-shape assembly to
// assembleChannelPerformance (this same package).
//
// Error map is kept short so the SPA can wire each code to a
// predictable UX:
//   - 400  ?days= missing / unparseable / outside {7,14,28}
//   - 401  missing user identity (defence-in-depth on top of r.protected)
//   - 404  account not found OR belongs to a different user (no existence leak)
//   - 422  account is not a YouTube platform OR YouTube channel id missing
//   - 501  metric history store not wired (operator misconfigured)
//   - 500  history fetch failure (logged with request_id)
package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/Marcuss-ops/InstaeditLogin/internal/analytics"
)

// youtubePlatform is the wire-level platform string any OAuth-bound
// YouTube channel carries on PlatformAccount.Platform. Defined as a
// local const (rather than reading from internal/models) because
// every other handler in pkg/api that gates a YouTube-specific path
// uses the literal string — keeping this file aligned with that
// convention removes a future refactor's breakage risk if
// internal/models' exported Platform constants get renamed.
const youtubePlatform = "youtube"

// handleGetAccountPerformance returns the canonical per-channel
// analytics payload for the user's own account.
//
// The canonical wire shape (analytics.ChannelPerformanceResponse) is
// built by assembleChannelPerformance so this handler stays thin:
// authentication, parameter parsing, period resolution, workspace
// ownership, and platform-type check happen here; everything else
// (summary, comparison, daily-series gap-fill, top-videos stub,
// data freshness) lives in the assembler.
func (r *Router) handleGetAccountPerformance(w http.ResponseWriter, req *http.Request) {
	if r.metricHistoryStore == nil {
		writeError(w, http.StatusNotImplemented, "metric history store not configured")
		return
	}
	id, ok := parsePathIDAsInt64(w, req, "id")
	if !ok {
		return
	}
	account, _, ok := r.loadOwnAccountByID(w, req, id)
	if !ok {
		return
	}
	days, ok := parseAnalyticsDays(w, req)
	if !ok {
		return
	}
	if account.Platform != youtubePlatform {
		writeError(w, http.StatusUnprocessableEntity,
			"channel is not a YouTube account; the per-channel analytics view is YouTube-only")
		return
	}
	// Resolve the YouTube channel id BEFORE the DB call so a
	// re-link-required account does not pay for a 28-day history
	// query it cannot render. Empty id is a per-account data-quality
	// problem (the OAuth-binding record is missing), not a transient
	// condition the SPA should retry on.
	channelID := resolvedYouTubeChannelID(account)
	if channelID == "" {
		writeError(w, http.StatusUnprocessableEntity,
			"youtube channel id not bound; re-link the channel")
		return
	}
	period, err := analytics.Resolve(days)
	if err != nil {
		if errors.Is(err, analytics.ErrInvalidPeriod) {
			writeError(w, http.StatusBadRequest,
				"invalid days: "+strconv.Itoa(days)+" not in {7,14,28}")
			return
		}
		logAndError(w, req, "resolve period failed", err)
		return
	}
	// One repository call covers BOTH [previous_start, prev_end]
	// AND [current_start, current_end]; the assembler slices it in
	// memory. Doing two queries would risk in-flight drift (a row
	// written between calls would silently change the comparison)
	// and burns an extra DB roundtrip on a hot endpoint.
	history, err := r.metricHistoryStore.GetHistory(
		account.ID,
		period.PreviousStartDate,
		period.EndDate,
	)
	if err != nil {
		logAndError(w, req, "load performance history failed", err,
			"platform_account_id", account.ID, "days", days)
		return
	}
	// Anchor freshness to period.EndDate (NOT time.Now().UTC()).
	// The window is bounded by resolver-truncated midnight UTC
	// (period.EndDate = today 00:00:00); stamping `time.Now()` as
	// the freshness anchor produces nonsensical IsStale readings
	// (wall clock is hours past midnight while rowDate ≤ period.EndDate,
	// so generatedAt − lastRowDate spans the entire UTC day and
	// always exceeds the 10-min 7d TTL). Anchoring to period.EndDate
	// matches the SPA's semantic: "data was reconciled within TTL of
	// the period boundary, so it's fresh relative to this view".
	resp := assembleChannelPerformance(account, channelID, history, period, period.EndDate)
	writeJSON(w, http.StatusOK, resp)
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
