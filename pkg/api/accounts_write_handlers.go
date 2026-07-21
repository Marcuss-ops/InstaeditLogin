package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// isTokenExpired matches the canonical error string produced by
// vault.Get on a stored-but-expired token. The vault's internal
// isExpiryError helper (lowercase, package-private) is the source
// of truth; we probe with substring equality rather than introducing
// a typed sentinel to avoid an interface dependency in the HTTP
// layer.
func isTokenExpired(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "expired")
}

// validateAccountRequest is the JSON body handler handleValidateAccount
// decodes. The only field today is Canary (bool, body key "canary");
// when false the 4-step pipeline defaults to the cheap path (steps 1-3
// only). Tests that don't supply a body pass the empty / unknown-path
// branch harmlessly (json.Decode error is silently ignored).
type validateAccountRequest struct {
	Canary bool `json:"canary,omitempty"`
}

// validateAccountResponse is the 200 OK body handler handleValidateAccount
// writes on the 4-step pipeline's success path. The embedded
// accountListItem shape mirrors every other /accounts/{id} response
// surface so the SPA can render the same shape on every code path.
// CanaryVideoID + CanaryUploadedChannelID are populated only when the
// caller set body.canary=true AND step 4 succeeded end-to-end (i.e.
// the canary was uploaded AND snippet.channelId matched the platform
// account row's expected channel).
type validateAccountResponse struct {
	accountListItem
	CanaryVideoID           string `json:"canary_video_id,omitempty"`
	CanaryUploadedChannelID string `json:"canary_uploaded_channel_id,omitempty"`
}

// auditAccountEvent fires a typed audit log entry, nil-safe (the
// auditLogStore is optional in tests / dev). Captures the
// WHO/WHAT/WHEN trio an operator needs to reconstruct the action.
// eventType is one of {account.reauth_required, account.disconnected}.
func (r *Router) auditAccountEvent(ctx context.Context, eventType string, identity auth.Identity, account *models.PlatformAccount) {
	if r.auditLogStore == nil {
		return
	}
	actor := strconv.FormatInt(identity.UserID(), 10)
	resource := strconv.FormatInt(account.ID, 10)
	_ = r.auditLogStore.Log(ctx, eventType, actor, "platform_account", resource, map[string]interface{}{
		"platform":         account.Platform,
		"platform_user_id": account.PlatformUserID,
	})
}

// handleValidateAccount runs the 4-step /accounts/{id}/validate pipeline
// (the operator's "is this YouTube OAuth grant REALLY ready to upload?"
// check) on YouTube platforms, falling back to the pre-C2 token-
// freshness probe for any non-YouTube platform OR for any test /
// deployment that hasn't yet wired WithYouTubeService.
//
// The 4 steps, in order, are:
//
//  1. refresh-grant  — vault.Renew exchanges the stored refresh token
//     for a fresh access token. invalid_grant → 422 +
//     status='reauth_required' + MarkReauthRequired on platform_account.
//     Transient (network, 5xx) → 500, leave status unchanged.
//
//  2. tokeninfo      — GetTokenInfo on the fresh access token (Google's
//     oauth2/v3/tokeninfo public introspection endpoint). Three hard
//     reauth signals: Google's 400 invalid_token, info.Aud ≠
//     cfg.YouTubeClientID (Production-vs-Testing drift), info
//     missing youtube.upload OR youtube.readonly. Transient (network,
//     decode) → 500.
//
//  3. channel-binding — ValidateChannelBinding paginated
//     channels.list(mine=true) comparison against
//     platform_account.platform_user_id. ErrYouTubeChannelMismatch →
//     422 + reauth; transient → 500.
//
//  4. canary (opt-in via body.canary=true) — uploads a private
//     INSTAEDIT-OAUTH-CANARY-{channel}-{ts} probe video via the
//     resumable upload protocol, then verifies snippet.channelId
//     equals the platform_account's expected channel. Bind-mismatch
//     OR ErrYouTubeCanaryRejected → 422 + reauth; transient → 500.
//
// On any 422, MarkReauthRequired stamps the platform_account row with
// the failing step's code + wrapped message, auditAccountEvent tags
// the request, and the response carries the structured error in
// writeError.
//
// On success, status flips back to 'active', reauth_required_at is
// cleared (caller could be re-flipped on next failure), and the
// canary fields (when applicable) surface to the SPA so the operator
// can audit the YouTube-Studio video id.
func (r *Router) handleValidateAccount(w http.ResponseWriter, req *http.Request) {
	id, ok := parsePathIDAsInt64(w, req, "id")
	if !ok {
		return
	}
	account, identity, ok := r.loadOwnAccountByID(w, req, id)
	if !ok {
		return
	}

	var body validateAccountRequest
	if req.ContentLength > 0 {
		_ = json.NewDecoder(req.Body).Decode(&body)
	}

	// 4-step pipeline today is YouTube-only. Non-YouTube platforms +
	// test setups that haven't wired WithYouTubeService fall back to
	// the legacy token-freshness probe (preserves the pre-C2 contract).
	if r.youTubeSvc == nil || account.Platform != models.PlatformYouTube {
		r.handleValidateAccountLegacy(w, req, account)
		return
	}

	ctx := req.Context()

	// === STEP 1: refresh-grant ===
	refreshed, err := r.vault.Renew(ctx, account.ID, models.TokenTypeBearer,
		r.youTubeSvc.RefreshOAuthToken) // method value = credentials.TokenRefresher
	if err != nil {
		if isInvalidGrantError(err) {
			r.flagReauthAndRespond(w, ctx, account, identity, "refresh_grant_invalid", err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "vault renew failed: "+err.Error())
		return
	}
	accessToken := refreshed.AccessToken

	// === STEP 2: tokeninfo scope + aud check ===
	info, tiErr := r.youTubeSvc.GetTokenInfo(ctx, accessToken)
	if tiErr != nil {
		if isGoogleTokenInfoRejection(tiErr) {
			r.flagReauthAndRespond(w, ctx, account, identity, "tokeninfo_rejected", tiErr.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "youtube tokeninfo failed: "+tiErr.Error())
		return
	}
	if info.Aud != r.youTubeSvc.ClientID() {
		r.flagReauthAndRespond(w, ctx, account, identity, "tokeninfo_aud_mismatch",
			fmt.Sprintf("tokeninfo.aud=%q cfg.YouTubeClientID=%q", info.Aud, r.youTubeSvc.ClientID()))
		return
	}
	if !info.HasUpload || !info.HasReadonly {
		r.flagReauthAndRespond(w, ctx, account, identity, "tokeninfo_scope_missing",
			fmt.Sprintf("HasUpload=%v HasReadonly=%v scope=%q", info.HasUpload, info.HasReadonly, info.Scope))
		return
	}

	// === STEP 3: paginated channel binding ===
	if cbErr := r.youTubeSvc.ValidateChannelBinding(ctx, accessToken, account.PlatformUserID); cbErr != nil {
		if errors.Is(cbErr, services.ErrYouTubeChannelMismatch) {
			r.flagReauthAndRespond(w, ctx, account, identity, "channel_binding_mismatch", cbErr.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "youtube channel binding failed: "+cbErr.Error())
		return
	}

	// === STEP 4: optional canary upload ===
	var canary *services.CanaryUploadResult
	if body.Canary {
		canary, err = r.youTubeSvc.CanaryUpload(ctx, accessToken, account.PlatformUserID)
		if err != nil {
			if errors.Is(err, services.ErrYouTubeChannelMismatch) ||
				errors.Is(err, services.ErrYouTubeCanaryRejected) {
				r.flagReauthAndRespond(w, ctx, account, identity, "canary_rejected", err.Error())
				return
			}
			writeError(w, http.StatusInternalServerError, "youtube canary upload failed: "+err.Error())
			return
		}
	}

	// ALL STEPS PASS — flip last_validated_at + status='active' + clear reauth flags.
	now := time.Now()
	account.LastValidatedAt = &now
	account.Status = models.AccountStatusActive
	account.ReauthRequiredAt = nil
	account.LastErrorCode = ""
	account.LastErrorMessage = ""
	if err := r.userRepo.UpdatePlatformAccount(account); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update account: "+err.Error())
		return
	}

	resp := validateAccountResponse{
		accountListItem: accountListItem{
			ID:             account.ID,
			Platform:       account.Platform,
			PlatformUserID: account.PlatformUserID,
			Username:       account.Username,
			Status:         account.Status,
			CreatedAt:      account.CreatedAt,
		},
	}
	if canary != nil {
		resp.CanaryVideoID = canary.VideoID
		resp.CanaryUploadedChannelID = canary.UploadedChannelID
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleValidateAccountLegacy preserves the pre-C2 token-freshness
// probe. Called when r.youTubeSvc is nil (test setup) OR
// account.Platform is not YouTube. Behaviour — including the
// active/expired/reauth_required status mapping, the per-provider
// TokenPolicy lookup, and the audit / persist pairing — is
// byte-identical to the pre-C2 handler so every pre-existing
// TestHandleValidateAccount_* test passes unchanged.
func (r *Router) handleValidateAccountLegacy(w http.ResponseWriter, req *http.Request, account *models.PlatformAccount) {
	now := time.Now()
	account.LastValidatedAt = &now

	var tokenTypes []string
	if tp, ok := r.capabilities.TokenPolicy(account.Platform); ok {
		tokenTypes = tp.PreferredTokenTypes()
	} else {
		tokenTypes = services.DefaultTokenTypes()
	}
	active := false
	expired := false
	for _, tt := range tokenTypes {
		_, err := r.vault.Get(req.Context(), account.ID, tt)
		switch {
		case err == nil:
			active = true
		case isTokenExpired(err):
			expired = true
		}
	}
	switch {
	case active:
		account.Status = models.AccountStatusActive
	case expired:
		account.Status = models.AccountStatusExpired
	default:
		account.Status = models.AccountStatusReauthRequired
	}
	if err := r.userRepo.UpdatePlatformAccount(account); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update account: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, accountListItem{
		ID:             account.ID,
		Platform:       account.Platform,
		PlatformUserID: account.PlatformUserID,
		Username:       account.Username,
		Status:         account.Status,
		CreatedAt:      account.CreatedAt,
	})
}

// flagReauthAndRespond is the 422-mapping helper for every 4-step failure.
// Stamps the platform_account row with status='reauth_required' +
// reauth_required_at = NOW (via MarkReauthRequired on UserStore) +
// last_error_code/message (structured) for the operator dashboard; emits
// the canonical "account.reauth_required" audit event (idempotent); and
// writes the structured error body. Best-effort: a MarkReauthRequired
// failure is logged at WARN but does not block the 422 response. Mirrors
// the existing pre-C2 attachDiscoveredAccounts → MarkReauthRequired
// pattern at line ~1377 so the SPA-side rendering stays consistent.
func (r *Router) flagReauthAndRespond(w http.ResponseWriter, ctx context.Context,
	account *models.PlatformAccount, identity auth.Identity,
	code string, message string) {
	if err := r.userRepo.MarkReauthRequired(ctx, account.ID, code, message); err != nil {
		slog.WarnContext(ctx, "handleValidateAccount: MarkReauthRequired failed (best-effort)",
			"account_id", account.ID, "code", code, "error", err)
	}
	r.auditAccountEvent(ctx, "account.reauth_required", identity, account)

	now := time.Now()
	account.LastValidatedAt = &now
	account.Status = models.AccountStatusReauthRequired
	account.ReauthRequiredAt = &now
	account.LastErrorCode = code
	account.LastErrorMessage = message
	if err := r.userRepo.UpdatePlatformAccount(account); err != nil {
		slog.WarnContext(ctx, "handleValidateAccount: UpdatePlatformAccount failed after reauth flag",
			"account_id", account.ID, "error", err)
	}

	writeError(w, http.StatusUnprocessableEntity,
		fmt.Sprintf("account validation failed (%s): %s", code, message))
}

// isInvalidGrantError classifies a vault.Renew / refresh failure as
// "the operator must re-consent". Substring match on Google's
// canonical "invalid_grant" error code (RFC 6749 §5.2). Same
// fragility pattern as isHardRejection4xxStatus in the services
// package: prefers stable error-shape strings to typed sentinels
// because the upstream credential vault emits wrapped errors from
// many sub-layers. Long-term fix: have vault.Renew return a
// typed sentinel ErrInvalidGrant so callers can switch on errors.Is.
// Tracked as follow-up; the string match is correct enough for
// the 4-step pipeline's correctness today.
func isInvalidGrantError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "invalid_grant")
}

// isGoogleTokenInfoRejection classifies a GetTokenInfo failure as
// "Google said the token is bad" (HTTP 400 invalid_token) versus
// "the request never reached Google" (network / decode). The
// substring "400" matches the upstream's `fmt.Errorf("youtube
// tokeninfo returned %d: %s", resp.StatusCode, string(body))`
// shape. Same fragility pattern as isInvalidGrantError; same
// long-term fix (typed sentinel `ErrGoogleTokenInfoInvalid`).
func isGoogleTokenInfoRejection(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "400")
}

// handleReconnectAccount flags the account as needing reauth. The
// SPA reads status='reauth_required' on /connections and surfaces
// a "Reconnect to <Platform>" CTA. The actual OAuth round-trip
// happens via /api/v1/auth/{provider}/login → callback, which
// (because of SPRINT 7.1 idempotency in AttachPlatformAccount)
// re-binds the existing platform_accounts row in place — no
// duplicate row, no POST /accounts leak.
func (r *Router) handleReconnectAccount(w http.ResponseWriter, req *http.Request) {
	id, ok := parsePathIDAsInt64(w, req, "id")
	if !ok {
		return
	}
	account, identity, ok := r.loadOwnAccountByID(w, req, id)
	if !ok {
		return
	}
	now := time.Now()
	account.Status = models.AccountStatusReauthRequired
	account.ReauthRequiredAt = &now
	if err := r.userRepo.UpdatePlatformAccount(account); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update account: "+err.Error())
		return
	}
	r.auditAccountEvent(req.Context(), "account.reauth_required", identity, account)
	writeJSON(w, http.StatusOK, accountListItem{
		ID:             account.ID,
		Platform:       account.Platform,
		PlatformUserID: account.PlatformUserID,
		Username:       account.Username,
		Status:         account.Status,
		CreatedAt:      account.CreatedAt,
	})
}

// handleDeleteAccount soft-disconnects a platform account. Steps:
//
//  1. loadOwnAccountByID (auth + ownership + 404 on cross-tenant).
//  2. vault.Revoke → deletes every encrypted token row for the
//     account. Idempotent: the vault swallows ErrTokenNotFound.
//  3. Soft-disconnect: status='disconnected' on the account row +
//     last_error_code='DISCONNECTED' for operator dashboards. The
//     row stays so the audit trail (user_id, platform, platform_user_id,
//     connected_at) is preserved for compliance — a future Taglio adds
//     the workspace-level "data deletion" endpoint that hard-deletes
//     the row + scrubs the encrypted tokens.
//  4. Audit log (account.disconnected), nil-safe.
//
// post_targets that referenced this account remain unchanged in the
// schema: the publish driver will surface a "token revoked" failure
// on the next tick and stamp post_targets.status='failed' through
// the existing error-classification path. No handler-side bulk
// transition is needed (Taglio 1.4 contract is implicit failure via
// worker, not synchronous transition via handler).
//
// Best-effort remote revoke at the provider is NOT attempted here:
// no Revoker capability interface exists today. A future Taglio 1.4
// follow-up adds internal/services/provider.go's Revoker interface
// plus a concrete implementation per provider that supports it
// (Meta has /me/permissions; Twitter has POST oauth2/invalidate_token;
// Google has https://oauth2.googleapis.com/revoke).
func (r *Router) handleDeleteAccount(w http.ResponseWriter, req *http.Request) {
	id, ok := parsePathIDAsInt64(w, req, "id")
	if !ok {
		return
	}
	account, identity, ok := r.loadOwnAccountByID(w, req, id)
	if !ok {
		return
	}
	if err := r.vault.Revoke(req.Context(), account.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "vault revoke failed: "+err.Error())
		return
	}
	account.Status = models.AccountStatusDisconnected
	account.ConnectedAt = nil
	account.LastErrorCode = "DISCONNECTED"
	account.LastErrorMessage = "account disconnected by user"
	if err := r.userRepo.UpdatePlatformAccount(account); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update account: "+err.Error())
		return
	}
	r.auditAccountEvent(req.Context(), "account.disconnected", identity, account)
	w.WriteHeader(http.StatusNoContent)
}

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
