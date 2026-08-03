// Package-level note: the /accounts/{id} write handlers are split per
// responsibility across three files (split-by-concern, 2026-08):
//
//	accounts_validate.go     — validate/reauth domain: handleValidateAccount
//	                          (4-step pipeline) + handleValidateAccountLegacy
//	                          + handleReconnectAccount + flagReauthAndRespond +
//	                          validateAccountRequest/Response + isTokenExpired /
//	                          isInvalidGrantError / isGoogleTokenInfoRejection
//	accounts_sync.go         — handleSyncAccount (snapshot refresh) +
//	                          handleUpdateAccount (metadata PATCH)
//	accounts_write_handlers.go — this file: auditAccountEvent (shared audit
//	                          helper used by validate/reauth/disconnect) +
//	                          handleDeleteAccount (soft-disconnect)
package api

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

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

// secureDisconnectStore atomically marks one account disconnected and decides
// whether its OAuth grant has any active siblings left. Production's
// repository implementation serializes this operation with the same
// per-grant advisory-lock discipline used by credential refreshes. The
// optional interface keeps lightweight test stores and legacy integrations
// source-compatible; they use the guarded fallback below.
type secureDisconnectStore interface {
	DisconnectPlatformAccount(ctx context.Context, accountID int64) (lastOnGrant bool, handled bool, err error)
}

// handleDeleteAccount soft-disconnects a platform account. Steps:
//
//  1. loadOwnAccountByID (auth + ownership + 404 on cross-tenant).
//  2. Shared-grant awareness (P0): migrations 084/085 let several
//     platform_accounts share one oauth_connection (one Google grant,
//     many channels). Count the still-active sibling channels on the
//     same grant, excluding this account. Only when this is the LAST
//     active channel (count == 0) do we revoke the grant:
//     a. best-effort remote revoke at the provider (YouTube only) —
//     revoking the refresh token on Google would otherwise kill
//     every sibling channel, so it is gated on last-on-grant too;
//     b. vault.Revoke → deletes every encrypted token row for the
//     connection. Idempotent: the vault swallows ErrTokenNotFound.
//     When siblings remain active, BOTH revokes are skipped so the
//     grant keeps working for them.
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
// Accounts without an oauth_connection (pre-043 legacy attach or an
// already-revoked grant) skip the grant work entirely — there is
// nothing to revoke — and disconnect cleanly.
//
// Workspace unlinking is implicit in the status flip: the workspace
// channel listings and the delivery target resolver treat
// 'disconnected' like 'deleted' (accounts_state.go /
// target_resolver_group.go), so the channel disappears from every
// publishable surface without touching workspace_channels rows (the
// audit row stays for compliance).
//
// Production performs the status transition and sibling decision in one
// transaction under a per-grant advisory lock. This prevents concurrent
// disconnects from both observing an active sibling and orphaning the last
// grant. Remote-revoke failure remains non-fatal: the local disconnect has
// already committed and the local vault cleanup still runs.
func (r *Router) handleDeleteAccount(w http.ResponseWriter, req *http.Request) {
	id, ok := parsePathIDAsInt64(w, req, "id")
	if !ok {
		return
	}
	account, identity, ok := r.loadOwnAccountByID(w, req, id)
	if !ok {
		return
	}
	ctx := req.Context()

	// Shared-grant awareness: production atomically disconnects this row
	// and decides whether it is the last active channel. Test/legacy stores
	// without the optional operation use the original fail-closed count path.
	lastOnGrant := false
	handledDisconnect := false
	if secureStore, ok := r.userRepo.(secureDisconnectStore); ok {
		var err error
		lastOnGrant, handledDisconnect, err = secureStore.DisconnectPlatformAccount(ctx, account.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to disconnect account: "+err.Error())
			return
		}
	}
	if !handledDisconnect {
		if account.OAuthConnectionID != nil {
			activeSiblings, err := r.userRepo.CountActiveAccountsOnConnection(ctx, *account.OAuthConnectionID, account.ID)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "failed to inspect shared grant: "+err.Error())
				return
			}
			lastOnGrant = activeSiblings == 0
		}
	}

	if lastOnGrant {
		// Best-effort remote revocation at the provider BEFORE the
		// local vault.Revoke (which deletes the token material needed
		// to revoke). Google's oauth2.googleapis.com/revoke accepts
		// the refresh token; the YouTubeRevoker contract requires
		// exactly that decoded value. Gated on last-on-grant: revoking
		// the refresh token at Google would also kill every sibling
		// channel still using the grant. A remote-revoke failure must
		// not block the disconnect.
		if account.Platform == models.PlatformYouTube && r.youtubeRevoker != nil {
			if reader, ok := r.vault.(RefreshTokenReader); ok {
				if refreshToken, rerr := reader.GetRefreshToken(ctx, account.ID); rerr == nil && refreshToken != "" {
					if revErr := r.youtubeRevoker.Revoke(ctx, refreshToken); revErr != nil {
						slog.WarnContext(ctx, "best-effort remote YouTube token revoke failed (continuing with local disconnect)",
							"platform_account_id", account.ID, "error", revErr)
					}
				}
			}
		}
		if err := r.vault.Revoke(ctx, account.ID); err != nil {
			writeError(w, http.StatusInternalServerError, "vault revoke failed: "+err.Error())
			return
		}
	}

	if !handledDisconnect {
		account.Status = models.AccountStatusDisconnected
		account.ConnectedAt = nil
		account.LastErrorCode = "DISCONNECTED"
		account.LastErrorMessage = "account disconnected by user"
		if err := r.userRepo.UpdatePlatformAccount(account); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to update account: "+err.Error())
			return
		}
	} else {
		// Keep the in-memory object consistent for audit metadata and any
		// response middleware even though the repository already committed it.
		account.Status = models.AccountStatusDisconnected
		account.ConnectedAt = nil
		account.LastErrorCode = "DISCONNECTED"
		account.LastErrorMessage = "account disconnected by user"
	}
	r.auditAccountEvent(ctx, "account.disconnected", identity, account)
	w.WriteHeader(http.StatusNoContent)
}
