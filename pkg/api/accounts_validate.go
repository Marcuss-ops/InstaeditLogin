package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
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
//     cfg.Auth.YouTubeClientID (Production-vs-Testing drift), info
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

	var accessToken string
	var info *services.YouTubeTokenInfo
	var err error
	if r.youtubeCredentialResolver != nil {
		// Shared grant path: refresh + tokeninfo are singleflighted by
		// oauth_connection_id; account-specific binding stays below.
		var validation *services.YouTubeGrantValidation
		validation, err = r.youtubeCredentialResolver.ValidateAccount(ctx, account)
		if err == nil {
			accessToken = validation.Token.AccessToken
			info = validation.Info
		}
	} else {
		// Legacy compatibility for test/deployment wiring that has not
		// supplied the shared resolver.
		var refreshed *models.OAuthToken
		refreshed, err = r.vault.Renew(ctx, account.ID, models.TokenTypeBearer, r.youTubeSvc.RefreshOAuthToken)
		if err == nil {
			accessToken = refreshed.AccessToken
			info, err = r.youTubeSvc.GetTokenInfo(ctx, accessToken)
		}
	}
	if err != nil {
		switch {
		case isInvalidGrantError(err) || errors.Is(err, credentials.ErrYouTubeInvalidGrant):
			r.flagReauthAndRespond(w, ctx, account, identity, "refresh_grant_invalid", err.Error())
		case errors.Is(err, services.ErrYouTubeCredentialAudience):
			r.flagReauthAndRespond(w, ctx, account, identity, "tokeninfo_aud_mismatch", err.Error())
		case errors.Is(err, services.ErrYouTubeCredentialScope):
			r.flagReauthAndRespond(w, ctx, account, identity, "tokeninfo_scope_missing", err.Error())
		case errors.Is(err, services.ErrYouTubeCredentialTokenInfo) || isGoogleTokenInfoRejection(err):
			r.flagReauthAndRespond(w, ctx, account, identity, "tokeninfo_rejected", err.Error())
		default:
			writeError(w, http.StatusInternalServerError, "youtube credential validation failed: "+err.Error())
		}
		return
	}
	if info == nil {
		writeError(w, http.StatusInternalServerError, "youtube tokeninfo unavailable")
		return
	}
	// Enforce the P0 force-ssl scope contract on BOTH credential paths.
	// GetTokenInfo only PARSES the tokeninfo scope; the resolver's
	// ValidateAccount enforces it, so the legacy path (resolver == nil)
	// must apply the same guard here — otherwise a grant without
	// youtube.force-ssl would pass step 2 and fail later on
	// thumbnails.set / videos.update / metadata writes / livestream.
	if !info.HasUpload || !info.HasReadonly || !info.HasForceSSL {
		scopeErr := fmt.Errorf("%w: HasUpload=%v HasReadonly=%v HasForceSSL=%v scope=%q",
			services.ErrYouTubeCredentialScope, info.HasUpload, info.HasReadonly, info.HasForceSSL, info.Scope)
		r.flagReauthAndRespond(w, ctx, account, identity, "tokeninfo_scope_missing", scopeErr.Error())
		return
	}
	if r.youtubeCredentialResolver != nil {
		if err := r.youtubeCredentialResolver.ValidateChannelBinding(ctx, account, accessToken); err != nil {
			if errors.Is(err, services.ErrYouTubeChannelMismatch) {
				r.flagReauthAndRespond(w, ctx, account, identity, "channel_binding_mismatch", err.Error())
				return
			}
			writeError(w, http.StatusInternalServerError, "youtube channel binding failed: "+err.Error())
			return
		}
	}

	// === STEP 3: paginated channel binding ===
	if r.youtubeCredentialResolver == nil {
		if cbErr := r.youTubeSvc.ValidateChannelBinding(ctx, accessToken, account.PlatformUserID); cbErr != nil {
			if errors.Is(cbErr, services.ErrYouTubeChannelMismatch) {
				r.flagReauthAndRespond(w, ctx, account, identity, "channel_binding_mismatch", cbErr.Error())
				return
			}
			writeError(w, http.StatusInternalServerError, "youtube channel binding failed: "+cbErr.Error())
			return
		}
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
			// ErrYouTubeCanaryInvalidMedia: the canary payload
			// (application/octet-stream) was rejected as invalid media.
			// The token and grant are fine, so this is a TRANSIENT
			// signal — NOT reauth_required. The operator dashboard
			// sees "canary invalid media" but the account stays active.
			if errors.Is(err, services.ErrYouTubeCanaryInvalidMedia) {
				writeError(w, http.StatusUnprocessableEntity, "canary invalid media: "+err.Error())
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
		accountListItem: accountListItemFromAccount(account),
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
	writeJSON(w, http.StatusOK, accountListItemFromAccount(account))
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

// isInvalidGrantError classifies only the typed OAuth grant error. Provider
// error strings are not stable classification contracts and must never be
// parsed to decide whether an account needs reauthorization.
func isInvalidGrantError(err error) bool {
	return err != nil && errors.Is(err, credentials.ErrInvalidGrant)
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
	writeJSON(w, http.StatusOK, accountListItemFromAccount(account))
}
