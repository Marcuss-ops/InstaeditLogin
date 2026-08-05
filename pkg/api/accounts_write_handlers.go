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
//	                          handleDeleteAccount (deprecated 410) +
//	                          handleDisconnectAccount (explicit soft
//	                          disconnect) + handleDeleteAccountData
//	                          (permanent delete / tombstone)
package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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

type secureDisconnectTxStore interface {
	DisconnectPlatformAccountTx(ctx context.Context, accountID int64, revoke func(context.Context, *sql.Tx) error) (lastOnGrant bool, handled bool, err error)
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

func writeDisconnectError(w http.ResponseWriter, err error) {
	if strings.Contains(err.Error(), "remote revoke") || strings.Contains(err.Error(), "token is unavailable") {
		writeError(w, http.StatusServiceUnavailable, "OAuth grant revocation failed; retry the disconnect")
		return
	}
	writeError(w, http.StatusInternalServerError, "failed to disconnect account")
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

// permanentAccountDeleteStore is the optional repository capability behind
// DELETE /api/v1/accounts/{id}/data. Production's UserRepository tombstones
// the account and removes every channel-scoped artifact (groups, workspace
// channels, snapshots, future jobs) in one transaction, revoking the shared
// Google grant only when this is the last active channel. The guarded
// fallback below (tombstone via UpdatePlatformAccount) is TEST-ONLY — it
// flips the row but performs no channel-scoped cleanup, exactly like the
// secureDisconnectStore fallback.
type permanentAccountDeleteStore interface {
	PermanentlyDeleteAccountTx(ctx context.Context, accountID int64, revoke func(context.Context, *sql.Tx) error) (handled bool, err error)
}

// writeAccountDeleteError maps repository errors from the permanent-delete
// path. Remote revocation failures reuse the typed OAuth-grant mapping
// (503 transient / 502 permanent); local failures get a delete-specific
// 500 message.
func writeAccountDeleteError(w http.ResponseWriter, err error) {
	var revocationErr *services.OAuthGrantRevocationError
	if errors.As(err, &revocationErr) {
		if revocationErr.IsTransient() {
			if revocationErr.RetryAfter > 0 {
				w.Header().Set("Retry-After", strconv.Itoa(int(revocationErr.RetryAfter/time.Second)))
			}
			writeError(w, http.StatusServiceUnavailable, "remote OAuth revocation is temporarily unavailable; retry later")
			return
		}
		writeError(w, http.StatusBadGateway, "remote OAuth revocation failed")
		return
	}
	if strings.Contains(err.Error(), "token is unavailable") ||
		strings.Contains(err.Error(), "revoke is not configured") ||
		strings.Contains(err.Error(), "revocation is not configured") ||
		strings.Contains(err.Error(), "refresh-token access is not configured") {
		writeError(w, http.StatusServiceUnavailable, "OAuth grant revocation is unavailable; retry later")
		return
	}
	writeError(w, http.StatusInternalServerError, "failed to permanently delete account")
}

// handleDeleteAccountData is the permanent-delete / tombstone endpoint:
// DELETE /api/v1/accounts/{id}/data. It is deliberately STRONGER than the
// soft disconnect:
//
//  1. loadOwnAccountByID (auth + ownership + 404 on cross-tenant).
//  2. The repository runs one transaction that locks the account row (and
//     the shared grant, if any), removes the account from every group and
//     from publishable destinations, snapshots, caches, editor/batch records,
//     livestream configuration, and future jobs (post_targets → draft +
//     parent aggregates recomputed), then tombstones the row (status='deleted',
//     username='[deleted]', metadata='{}'). The row is NOT physically
//     deleted: historical publications (post_targets, livestreams,
//     thumbnail_assignments) keep referencing it, so the tombstone keeps
//     those foreign keys intact while the account disappears from every
//     normal query (GET /accounts already hides account_state=deleted).
//  3. Shared-grant awareness (migrations 084/085): when this is the LAST
//     active channel on its oauth_connection, the grant is revoked
//     remotely (YouTube, best-effort wiring via the vault TX reader),
//     its token rows are deleted and the oauth_connections row is
//     removed. When an active sibling still uses the grant, BOTH the grant
//     and the shared tokens are preserved so the sibling keeps working.
//  4. Outbox (platform_account.deleted) + audit (account_deleted) are
//     written inside the same transaction.
//
// A remote-revocation failure rolls the whole delete back and surfaces a
// typed 502/503; the local tombstone never silently proceeds with a live
// Google grant.
func (r *Router) handleDeleteAccountData(w http.ResponseWriter, req *http.Request) {
	id, ok := parsePathIDAsInt64(w, req, "id")
	if !ok {
		return
	}
	account, _, ok := r.loadOwnAccountByID(w, req, id)
	if !ok {
		return
	}

	// A completed tombstone is already the requested end state. Return success
	// before decoding a body so network retries are genuinely idempotent and
	// never re-run cleanup, audit, or provider revocation.
	if account.Status == models.AccountStatusDeleted {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Permanent deletion requires an explicit, exact-name confirmation.
	var body struct {
		Confirmation string `json:"confirmation"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "confirmation is required")
		return
	}
	expectedConfirmation := account.Username
	if expectedConfirmation == "" {
		expectedConfirmation = "#" + strconv.FormatInt(account.ID, 10)
	}
	if body.Confirmation != expectedConfirmation {
		writeError(w, http.StatusBadRequest, "confirmation must exactly match the channel name")
		return
	}
	ctx := req.Context()

	// Revoke callback (YouTube + vault TX reader wired): the repository
	// invokes it ONLY when this channel is the last active one on the grant
	// (and only if the account actually has an oauth_connection), while the
	// grant lock is held and before the token rows are removed.
	var revoke func(context.Context, *sql.Tx) error
	if account.OAuthConnectionID != nil && *account.OAuthConnectionID > 0 &&
		account.Platform == models.PlatformYouTube {
		revoke = func(ctx context.Context, tx *sql.Tx) error {
			if r.youtubeRevoker == nil {
				return fmt.Errorf("OAuth grant revocation is not configured")
			}
			reader, ok := r.vault.(RefreshTokenTxReader)
			if !ok {
				return fmt.Errorf("OAuth grant refresh-token access is not configured")
			}
			refreshToken, err := reader.GetRefreshTokenForOAuthConnectionTx(ctx, tx, *account.OAuthConnectionID)
			if err != nil || refreshToken == "" {
				return fmt.Errorf("OAuth grant revocation token is unavailable")
			}
			revokeCtx, cancel := context.WithTimeout(ctx, services.OAuthGrantRevocationTimeout)
			defer cancel()
			err = r.youtubeRevoker.Revoke(revokeCtx, refreshToken)
			if err == nil || errors.Is(err, services.OAuthGrantRevocationAlreadyCompleted) {
				return nil
			}
			var revocationErr *services.OAuthGrantRevocationError
			if !errors.As(err, &revocationErr) {
				return &services.OAuthGrantRevocationError{Class: services.OAuthGrantRevocationPermanent, Cause: err}
			}
			return err
		}
	}

	if store, ok := r.userRepo.(permanentAccountDeleteStore); ok {
		handled, err := store.PermanentlyDeleteAccountTx(ctx, account.ID, revoke)
		if err != nil {
			writeAccountDeleteError(w, err)
			return
		}
		if handled {
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}

	// Test/legacy fallback: tombstone the in-memory account and persist the
	// status flip. No channel-scoped cleanup here — the real cleanup lives
	// in the production repository transaction (see the interface comment).
	account.Status = models.AccountStatusDeleted
	account.Username = "[deleted]"
	account.Metadata = models.Metadata{}
	account.LastErrorCode = "DELETED"
	account.LastErrorMessage = "account permanently deleted by user"
	if err := r.userRepo.UpdatePlatformAccount(account); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to permanently delete account: "+err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
//     a. the transaction-aware repository callback remotely revokes the
//     provider grant (YouTube only) while the grant lock is held —
//     revoking the refresh token on Google would otherwise kill every
//     sibling channel, so it is gated on last-on-grant;
//     b. the same transaction marks oauth_connections disconnected and
//     deletes every encrypted token row for the connection.
//     When siblings remain active, neither remote nor local grant cleanup
//     runs, so the shared grant keeps working for them.
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
// Production performs the status transition, sibling decision, optional
// remote revoke, grant cleanup, and channel cleanup in one transaction
// under a per-grant advisory lock. This prevents concurrent disconnects
// from both observing an active sibling and orphaning the last grant. A
// remote-revoke failure aborts the transaction so the account remains
// retryable and no partial local state is committed.
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

	// Define the provider revoke callback once. The transaction-aware
	// repository invokes it only for the last active channel, while holding
	// the grant lock and before deleting the local token rows. Shared-grant
	// disconnects never enter this callback, preserving sibling tokens.
	var revoke func(context.Context, *sql.Tx) error
	if account.OAuthConnectionID != nil && *account.OAuthConnectionID > 0 &&
		account.Platform == models.PlatformYouTube && r.youtubeRevoker != nil {
		if reader, ok := r.vault.(RefreshTokenTxReader); ok {
			revoke = func(ctx context.Context, tx *sql.Tx) error {
				refreshToken, err := reader.GetRefreshTokenForOAuthConnectionTx(ctx, tx, *account.OAuthConnectionID)
				if err != nil || refreshToken == "" {
					return fmt.Errorf("OAuth grant revocation token is unavailable")
				}
				revokeCtx, cancel := context.WithTimeout(ctx, services.OAuthGrantRevocationTimeout)
				defer cancel()
				err = r.youtubeRevoker.Revoke(revokeCtx, refreshToken)
				if err == nil || errors.Is(err, services.OAuthGrantRevocationAlreadyCompleted) {
					return nil
				}
				var revocationErr *services.OAuthGrantRevocationError
				if !errors.As(err, &revocationErr) {
					return &services.OAuthGrantRevocationError{Class: services.OAuthGrantRevocationPermanent, Cause: err}
				}
				return err
			}
		}
	}

	lastOnGrant := false
	handledDisconnect := false
	if secureStore, ok := r.userRepo.(secureDisconnectTxStore); ok {
		var err error
		lastOnGrant, handledDisconnect, err = secureStore.DisconnectPlatformAccountTx(ctx, account.ID, revoke)
		if err != nil {
			writeDisconnectError(w, err)
			return
		}
	} else if secureStore, ok := r.userRepo.(secureDisconnectStore); ok {
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

	// Legacy fallback only: older test stores do not implement the
	// transaction-aware capability, so preserve their existing behavior.
	// Production never enters this branch; its repository invokes `revoke`
	// inside the transaction above.
	if !handledDisconnect && lastOnGrant {
		if account.Platform == models.PlatformYouTube && r.youtubeRevoker != nil {
			if reader, ok := r.vault.(RefreshTokenReader); ok {
				if refreshToken, readErr := reader.GetRefreshToken(ctx, account.ID); readErr == nil && refreshToken != "" {
					if remoteErr := r.youtubeRevoker.Revoke(ctx, refreshToken); remoteErr != nil {
						// Legacy behavior is best-effort for the remote provider;
						// local cleanup still proceeds.
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
