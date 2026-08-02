package api

import (
	"encoding/json"
	"net/http"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// handleSyncAccount forces a refresh of the remote resource snapshot
// for the given account. The snapshot caches channel stats, profile,
// and branding so the frontend doesn't trigger a provider API call on
// every render. POST /accounts/{id}/sync bypasses the 10-minute cache.
//
// When snapshotStore is nil, returns 501. When the provider does not
// implement AccountDetailsProvider, returns 400.
func (r *Router) handleSyncAccount(w http.ResponseWriter, req *http.Request) {
	if r.snapshotStore == nil {
		writeError(w, http.StatusNotImplemented, "snapshot store not configured")
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

	detailsProvider, ok := r.capabilities.AccountDetails(account.Platform)
	if !ok {
		writeError(w, http.StatusBadRequest, "platform "+account.Platform+" does not support account details")
		return
	}

	// Retrieve the access token from the vault.
	token, err := r.vault.Get(req.Context(), account.ID, models.TokenTypeBearer)
	if err != nil {
		// Fall back to other token types.
		token, err = r.vault.Get(req.Context(), account.ID, models.TokenTypeLongLived)
		if err != nil {
			token, err = r.vault.Get(req.Context(), account.ID, models.TokenTypeShortLived)
			if err != nil {
				writeError(w, http.StatusUnauthorized, "no valid token found for this account")
				return
			}
		}
	}

	details, err := detailsProvider.GetAccountDetails(req.Context(), token.AccessToken, account.PlatformUserID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to fetch account details: "+err.Error())
		return
	}

	// Build the snapshot from the details response.
	snap := &repository.AccountResourceSnapshot{
		PlatformAccountID: account.ID,
		ResourceType:      details.ResourceType,
		Profile: map[string]any{
			"display_name": details.DisplayName,
			"handle":       details.Handle,
			"description":  details.Description,
			"avatar_url":   details.AvatarURL,
			"banner_url":   details.BannerURL,
			"public_url":   details.PublicURL,
			"external_id":  details.ExternalID,
		},
		FetchedAt: details.FetchedAt,
	}

	// Metrics → statistics JSONB.
	stats := make(map[string]any)
	for _, m := range details.Metrics {
		stats[m.Key] = map[string]any{
			"label":         m.Label,
			"value":         m.Value,
			"display_value": m.DisplayValue,
		}
	}
	snap.Statistics = stats

	// Platform-specific properties → content JSONB.
	if details.Properties != nil {
		snap.Content = details.Properties
	}

	if err := r.snapshotStore.UpsertSnapshot(snap); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save snapshot: "+err.Error())
		return
	}

	// Persist the daily metric history row. Best-effort: a failure here
	// should not break the sync response.
	if r.metricHistoryStore != nil {
		_ = r.metricHistoryStore.UpsertDaily(account.ID, details.FetchedAt, metricsToPoint(details.Metrics))
		r.storeYouTubeEarnings(req.Context(), account, token.AccessToken)
	}

	writeJSON(w, http.StatusOK, details)
}

// handleUpdateAccount (PATCH /api/v1/accounts/{id}) allows partial
// updates to a platform account, including metadata fields like
// language. Currently supports:
//   - metadata.language (ISO 639-1 code or free text)
func (r *Router) handleUpdateAccount(w http.ResponseWriter, req *http.Request) {
	id, ok := parsePathIDAsInt64(w, req, "id")
	if !ok {
		return
	}
	account, _, ok := r.loadOwnAccountByID(w, req, id)
	if !ok {
		return
	}
	if r.userRepo == nil {
		writeError(w, http.StatusInternalServerError, "user repository not configured")
		return
	}
	var body struct {
		Metadata map[string]any `json:"metadata,omitempty"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if body.Metadata == nil {
		writeError(w, http.StatusBadRequest, "metadata object required")
		return
	}
	// Merge onto existing metadata
	if account.Metadata == nil {
		account.Metadata = make(models.Metadata)
	}
	for k, v := range body.Metadata {
		account.Metadata[k] = v
	}
	if err := r.userRepo.UpdatePlatformAccount(account); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update account: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":       account.ID,
		"platform": account.Platform,
		"username": account.Username,
		"status":   account.Status,
		"metadata": account.Metadata,
	})
}
