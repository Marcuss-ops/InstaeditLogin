package api

import (
	"net/http"

	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// AdminChannelsResponse is the JSON body for GET /admin/channels.
// Per-row + headline counts so the dashboard renders "active 187 /
// reauth_required 13" alongside the table without a second roundtrip.
type AdminChannelsResponse struct {
	Counts    repository.AdminChannelCounts     `json:"counts"`
	Channels  []repository.AdminChannelRow      `json:"channels"`
	Generated int64                             `json:"generated_at_unix"`
}

// handleAdminChannels (GET /admin/channels) returns the per-platform
// account table plus the headline counts. Filters via query
// parameters:
//
//	?status=<active|expired|reauth_required|revoked|disconnected|error>
//	?platform=<instagram|youtube|...>
//
// Unrecognised values yield a 422 so a typo doesn't silently return
// the whole table. The default (no filters) is the full
// reauth-required-priority sort — operators see who needs attention
// first, then can drill into a specific platform with ?platform=youtube.
//
// Authz: requireAdmin (gates all /admin/* routes).
func (r *Router) handleAdminChannels(w http.ResponseWriter, req *http.Request) {
	if r.adminStore == nil {
		writeError(w, http.StatusNotImplemented, "admin store not configured")
		return
	}
	statusFilter := req.URL.Query().Get("status")
	platformFilter := req.URL.Query().Get("platform")

	if statusFilter != "" {
		switch statusFilter {
		case "active", "expired", "reauth_required", "revoked", "disconnected", "error":
			// valid
		default:
			writeError(w, http.StatusUnprocessableEntity, "unknown status filter: "+statusFilter)
			return
		}
	}

	counts, err := r.adminStore.ChannelCounts(req.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load channel counts: "+err.Error())
		return
	}
	channels, err := r.adminStore.ListChannelsForOps(req.Context(), statusFilter, platformFilter, 500)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not list channels: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, AdminChannelsResponse{
		Counts:    counts,
		Channels:  channels,
		Generated: nowUnix(),
	})
}

// handleAdminChannelsCSV (GET /admin/channels.csv) streams the same
// rows as a CSV via the D4.a streaming helper. Same query parameters
// as the JSON handler; the file's header row is the union of
// AdminChannelRow's wire-friendly fields.
func (r *Router) handleAdminChannelsCSV(w http.ResponseWriter, req *http.Request) {
	if r.adminStore == nil {
		writeError(w, http.StatusNotImplemented, "admin store not configured")
		return
	}
	statusFilter := req.URL.Query().Get("status")
	platformFilter := req.URL.Query().Get("platform")
	channels, err := r.adminStore.ListChannelsForOps(req.Context(), statusFilter, platformFilter, 500)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not list channels: "+err.Error())
		return
	}

	_, csvw, flush, err := writeAdminCSV(w, "channels")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "csv writer init failed")
		return
	}
	_ = csvw.Write([]string{
		"platform_account_id", "user_id", "platform", "username", "status",
		"connected_at", "last_validated_at", "last_refresh_at", "reauth_required_at",
		"last_error_code", "last_error_message",
	})
	for _, ch := range channels {
		_ = csvw.Write([]string{
			itoa(ch.PlatformAccountID),
			itoa(ch.UserID),
			ch.Platform,
			ch.Username,
			ch.Status,
			formatTimePtr(ch.ConnectedAt),
			formatTimePtr(ch.LastValidatedAt),
			formatTimePtr(ch.LastRefreshAt),
			formatTimePtr(ch.ReauthRequiredAt),
			ch.LastErrorCode,
			ch.LastErrorMessage,
		})
	}
	if err := flush(); err != nil {
		slogCSVStreamError("channels", err)
	}
}
