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
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
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
// per-grant advisory-lock discipline used by credential refreshes AND runs
// the P1 channel cleanup (group memberships, publishable destinations,
// future-job cancellation) in the same transaction as the status flip.
//
// The guarded fallback below (CountActiveAccountsOnConnection +
// UpdatePlatformAccount) is TEST-ONLY: it flips the status but performs NO
// P1 cleanup. Production always wires the real UserRepository (which
// implements this interface), so the cleanup contract is guaranteed there;
// the fallback exists only to keep lightweight mock stores source-compatible.
type secureDisconnectStore interface {
	DisconnectPlatformAccount(ctx context.Context, accountID int64) (lastOnGrant bool, handled bool, err error)
}

// oauthGrantDisconnectStore is the optional repository capability for the
// explicit "disconnect Google account" action. It is intentionally separate
// from secureDisconnectStore: deleting one channel must preserve a shared
// grant, while this endpoint deliberately disconnects every channel attached
// to the grant in one transaction.
type oauthGrantDisconnectStore interface {
	DisconnectOAuthGrantTx(ctx context.Context, oauthConnectionID int64) error
}

type oauthGrantDisconnectWithRevocationStore interface {
	DisconnectOAuthGrantWithAccountRevocationTx(ctx context.Context, oauthConnectionID, accountID int64, expectedProvider string, revoke func(context.Context, *sql.Tx) error) error
}

// handleDeleteOAuthGrant disconnects the complete OAuth grant associated with
// an owned platform account. The repository performs the grant/account/token/
// outbox/audit mutations in one transaction; this handler only authenticates
// ownership and resolves the grant id from the account selected by the user.
func (r *Router) handleDeleteOAuthGrant(w http.ResponseWriter, req *http.Request) {
	id, ok := parsePathIDAsInt64(w, req, "id")
	if !ok {
		return
	}
	account, _, ok := r.loadOwnAccountByID(w, req, id)
	if !ok {
		return
	}
	if account.OAuthConnectionID == nil || *account.OAuthConnectionID <= 0 {
		writeError(w, http.StatusConflict, "account has no OAuth grant")
		return
	}
	if account.Platform != models.PlatformYouTube {
		writeError(w, http.StatusNotImplemented, "remote OAuth grant revocation is only supported for YouTube")
		return
	}
	store, ok := r.userRepo.(oauthGrantDisconnectStore)
	if !ok {
		writeError(w, http.StatusNotImplemented, "OAuth grant disconnect is not configured")
		return
	}
	if r.oauthGrantRevoker == nil {
		writeError(w, http.StatusServiceUnavailable, "OAuth grant revocation is not configured")
		return
	}
	if revokingStore, ok := r.userRepo.(oauthGrantDisconnectWithRevocationStore); ok {
		reader, readerOK := r.vault.(RefreshTokenTxReader)
		if !readerOK {
			writeError(w, http.StatusServiceUnavailable, "OAuth refresh-token access is not configured")
			return
		}
		err := revokingStore.DisconnectOAuthGrantWithAccountRevocationTx(req.Context(), *account.OAuthConnectionID, account.ID, models.PlatformYouTube, func(ctx context.Context, tx *sql.Tx) error {
			refreshToken, err := reader.GetRefreshTokenForOAuthConnectionTx(ctx, tx, *account.OAuthConnectionID)
			if err != nil || refreshToken == "" {
				return fmt.Errorf("OAuth grant revocation token is unavailable")
			}
			revokeCtx, cancel := context.WithTimeout(ctx, services.OAuthGrantRevocationTimeout)
			defer cancel()
			err = r.oauthGrantRevoker.RevokeGrant(revokeCtx, refreshToken)
			if err == nil || errors.Is(err, services.OAuthGrantRevocationAlreadyCompleted) {
				return nil
			}
			var revocationErr *services.OAuthGrantRevocationError
			if !errors.As(err, &revocationErr) {
				return &services.OAuthGrantRevocationError{Class: services.OAuthGrantRevocationPermanent, Cause: err}
			}
			return err
		})
		if err != nil {
			writeOAuthGrantDisconnectError(w, err)
			return
		}
	} else {
		// Compatibility fallback for legacy/test stores. Production's
		// UserRepository implements the transaction-aware capability above.
		reader, readerOK := r.vault.(RefreshTokenReader)
		if !readerOK {
			writeError(w, http.StatusServiceUnavailable, "OAuth refresh-token access is not configured")
			return
		}
		refreshToken, err := reader.GetRefreshToken(req.Context(), id)
		if err != nil || refreshToken == "" {
			writeError(w, http.StatusServiceUnavailable, "OAuth grant revocation token is unavailable")
			return
		}
		revokeCtx, cancel := context.WithTimeout(req.Context(), services.OAuthGrantRevocationTimeout)
		err = r.oauthGrantRevoker.RevokeGrant(revokeCtx, refreshToken)
		cancel()
		if err != nil && !errors.Is(err, services.OAuthGrantRevocationAlreadyCompleted) {
			var revocationErr *services.OAuthGrantRevocationError
			if !errors.As(err, &revocationErr) {
				err = &services.OAuthGrantRevocationError{Class: services.OAuthGrantRevocationPermanent, Cause: err}
			}
			writeOAuthGrantDisconnectError(w, err)
			return
		}
		if err := store.DisconnectOAuthGrantTx(req.Context(), *account.OAuthConnectionID); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to disconnect OAuth grant")
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeOAuthGrantDisconnectError(w http.ResponseWriter, err error) {
	var revocationErr *services.OAuthGrantRevocationError
	if errors.As(err, &revocationErr) && revocationErr.IsTransient() {
		if revocationErr.RetryAfter > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(int(revocationErr.RetryAfter/time.Second)))
		}
		writeError(w, http.StatusServiceUnavailable, "remote OAuth revocation is temporarily unavailable; retry later")
		return
	}
	if strings.Contains(err.Error(), "token is unavailable") {
		writeError(w, http.StatusServiceUnavailable, "OAuth grant revocation token is unavailable")
		return
	}
	if errors.As(err, &revocationErr) {
		writeError(w, http.StatusBadGateway, "remote OAuth revocation failed")
		return
	}
	// Errors without the typed provider classification are local repository
	// failures (for example commit/rollback errors), not remote failures.
	writeError(w, http.StatusInternalServerError, "failed to disconnect OAuth grant")
}

// handleDeleteAccount is the DEPRECATED DELETE /api/v1/accounts/{id}
// route. The account-lifecycle audit found the DELETE method misleading:
// it performed a soft disconnection, not a deletion. The explicit commands
// are now POST /api/v1/accounts/{id}/disconnect (soft, row kept for
// audit) and DELETE /api/v1/accounts/{id}/data (permanent). The old
// route answers 410 Gone with guidance instead of silently soft-deleting.
func (r *Router) handleDeleteAccount(w http.ResponseWriter, req *http.Request) {
	writeError(w, http.StatusGone, "DELETE /api/v1/accounts/{id} is removed; use POST /api/v1/accounts/{id}/disconnect (disconnect, keeps history) or DELETE /api/v1/accounts/{id}/data (permanent delete)")
}

// handleDisconnectAccount soft-disconnects a platform account — the
// explicit POST /api/v1/accounts/{id}/disconnect command. Steps:
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
//     shared grant keeps working for them.
//  3. Disconnect (P1): status='disconnected' on the account row +
//     last_error_code='DISCONNECTED' for operator dashboards, in the
//     same transaction the repository removes the account from every
//     group, from the publishable destinations (workspace_channels)
//     and cancels its future jobs (post_targets → draft + parent
//     aggregates recomputed). The row stays so the audit trail
//     (user_id, platform, platform_user_id, connected_at) is
//     preserved — the hard-delete/tombstone endpoint
//     (DELETE /api/v1/accounts/{id}/data) is the separate permanent
//     removal path.
//  4. Audit log (account.disconnected), nil-safe.
//
// Accounts without an oauth_connection (pre-043 legacy attach or an
// already-revoked grant) skip the grant work entirely — there is
// nothing to revoke — and disconnect cleanly.
//
// Production performs the status transition, sibling decision and
// cleanup in one transaction under a per-grant advisory lock. This
// prevents concurrent disconnects from both observing an active
// sibling and orphaning the last grant. Remote-revoke failure remains
// non-fatal: the local disconnect has already committed and the local
// vault cleanup still runs.
func (r *Router) handleDisconnectAccount(w http.ResponseWriter, req *http.Request) {
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
