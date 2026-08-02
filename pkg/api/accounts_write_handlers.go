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
