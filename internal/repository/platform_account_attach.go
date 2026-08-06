package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// FinalizeAttach (P2 — admin connect-link) creates (or reuses) the
// oauth_connections row that anchors the platform_account ↔ encrypted
// token storage relationship, and promotes the platform_account from
// 'pending_authorization' (the CSV-import reset state) to 'active'
// with a fresh connected_at. Called by the OAuth callback AFTER a
// successful AttachPlatformAccount + vault.Save wire-up so the
// flow order is:
//
//  1. AttachPlatformAccount (creates platform_accounts row, NULL
//     oauth_connection_id, status='pending_authorization')
//  2. FinalizeAttach (UPSERT oauth_connections; UPDATE
//     platform_accounts.oauth_connection_id + status +
//     connected_at; in one tx so a partial failure can't leave
//     the FK dangling)
//  3. vault.Save (FK oauth_connection_id is now set in
//     platform_accounts so the FK from tokens → oauth_connections
//     resolves)
//
// Idempotent on (user_id, provider, provider_resource_id) via ON
// CONFLICT DO UPDATE so a re-authorize for the same channel flips
// status back to 'active' + refreshes connected_at + scopes
// without losing the existing oauth_connection row.
//
// Returns the oauth_connection_id used so the caller can verify
// what was stamped onto platform_accounts.
func (r *UserRepository) FinalizeAttach(ctx context.Context, accountID int64, scopes []string) (int64, error) {
	if accountID <= 0 {
		return 0, fmt.Errorf("finalize attach: accountID must be > 0 (got %d)", accountID)
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("finalize attach: begin tx: %w", err)
	}
	defer func() {
		// Rollback is a no-op after a successful Commit; this is
		// the standard pgx/database/sql idiom.
		_, _ = tx.ExecContext(ctx, "SELECT 1")
		_ = tx.Rollback()
	}()

	var (
		platform           string
		providerResourceID string
		userID             int64
	)
	if err := tx.QueryRowContext(ctx,
		`SELECT platform, platform_user_id, user_id FROM platform_accounts WHERE id = $1`,
		accountID,
	).Scan(&platform, &providerResourceID, &userID); err != nil {
		return 0, fmt.Errorf("finalize attach: load account %d: %w", accountID, err)
	}
	if userID <= 0 {
		return 0, fmt.Errorf("finalize attach: platform_accounts.user_id is zero for account %d", accountID)
	}
	storedPlatform := platform
	platform = models.NormalizePlatformIdentifier(platform)
	if storedPlatform == models.PlatformX && platform == models.PlatformTwitter {
		result, canonicalizeErr := tx.ExecContext(ctx,
			`UPDATE platform_accounts
			    SET platform = $1, updated_at = NOW()
			  WHERE id = $2
			    AND user_id = $3
			    AND platform = $4
			    AND NOT EXISTS (
				      SELECT 1 FROM platform_accounts
				       WHERE user_id = $3 AND platform = $1 AND platform_user_id = $5
			    )`,
			models.PlatformTwitter, accountID, userID, models.PlatformX, providerResourceID,
		)
		if canonicalizeErr != nil {
			return 0, fmt.Errorf("finalize attach: canonicalize legacy X alias: %w", canonicalizeErr)
		}
		if affected, affectedErr := result.RowsAffected(); affectedErr != nil {
			return 0, fmt.Errorf("finalize attach: inspect X alias canonicalization: %w", affectedErr)
		} else if affected == 0 {
			return 0, fmt.Errorf("finalize attach: canonical Twitter account already exists for account %d", accountID)
		}
	}

	// UPSERT oauth_connections. The unique key (user_id, provider,
	// provider_resource_id) makes this idempotent across rechannels
	// of the same grant (e.g. if a manager reconsents after a
	// token rotation). pgx v5 stdlib binds Go []string → TEXT[]
	// natively through its default type map; textual literal
	// formatting is NOT needed.
	var oauthConnID int64
	if err := tx.QueryRowContext(ctx,
		`INSERT INTO oauth_connections (user_id, provider, provider_resource_id, scopes, last_validated_at)
		 VALUES ($1, $2, $3, $4, NOW())
		 ON CONFLICT (user_id, provider, provider_resource_id)
		 DO UPDATE SET scopes = EXCLUDED.scopes, last_validated_at = NOW(), updated_at = NOW()
		 RETURNING id`,
		userID, platform, providerResourceID, scopes,
	).Scan(&oauthConnID); err != nil {
		return 0, fmt.Errorf("finalize attach: upsert oauth_connections: %w", err)
	}

	// Promote the platform_account: link the FK, status='active',
	// connected_at=NOW(). connected_at is what the dashboard's
	// "last successful auth" freshness field reads from.
	if _, err := tx.ExecContext(ctx,
		`UPDATE platform_accounts
		     SET oauth_connection_id = $1,
		         status = 'active',
		         connected_at = NOW(),
		         last_validated_at = NOW(),
		         reauth_required_at = NULL,
		         last_error_code = NULL,
		         last_error_message = NULL,
		         updated_at = NOW()
		   WHERE id = $2`,
		oauthConnID, accountID,
	); err != nil {
		return 0, fmt.Errorf("finalize attach: update platform_accounts %d: %w", accountID, err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("finalize attach: commit: %w", err)
	}
	return oauthConnID, nil
}

// AttachPlatformAccount links an OAuth platform profile to an EXISTING
// user identified by userID. It does NOT create users — SPRINT 7.1
// (P0#14) closed the OAuth-auto-create gap; users are created via the
// product onboarding flow (email/password register) before they
// can ever hit /api/v1/auth/{provider}/callback.
//
// Behaviour:
//   - If (platform, platform_user_id) does not exist → INSERT a new
//     platform_accounts row bound to userID. Returns the new row.
//     NEW rows default to status='pending_authorization' (NOT
//     'active') so the row CANNOT be observed in the
//     "active-but-no-oauth_connection-and-no-token" state that the
//     Task 1/10 OAuth-atomic-flip guarantee forbids. The companion
//     flip to 'active' is the sole responsibility of
//     services.ChannelAuthorizationService.AuthorizeChannel: it
//     UPSERTs oauth_connections, writes the encrypted token row,
//     and UPDATE-promotes status='active' inside ONE transaction.
//     A process crash between AttachPlatformAccount (commit) and
//     AuthorizeChannel (BEGIN) leaves the row in
//     'pending_authorization' — recoverable by retrying the OAuth
//     callback via /admin/channels/{id}/connect-link, never by
//     silently leaving a phantom-active row.
//   - If (platform, platform_user_id) exists AND existing.UserID == userID
//     → idempotent: update the username in place (provider-side renames
//     do happen) and return the existing row. Status is not touched:
//     AuthorizeChannel is the sole gate that flips pending → active;
//     a same-user re-link will go through AuthorizeChannel again and
//     naturally re-validate.
//   - If (platform, platform_user_id) exists AND existing.UserID != userID
//     → return ErrAccountAlreadyLinked. We never silently rebind a
//     platform identity to a different session user; that's an
//     account-takeover vector. The operator's runbook is for the
//     human owner of the existing link to disconnect via
//     DELETE /api/v1/accounts/{id} before re-link is possible.
//
// userID > 0 is enforced (SPRINT 2.1 + Taglio 1.1): a zero user id
// means the caller hijacked a sessionless request, which is the
// exact scenario this method is designed to refuse.
func (r *UserRepository) AttachPlatformAccount(userID int64, profile *models.PlatformProfile, platform string) (*models.PlatformAccount, error) {
	if userID <= 0 {
		return nil, fmt.Errorf("attach platform account: invalid user id %d (must be > 0)", userID)
	}
	if profile == nil {
		return nil, fmt.Errorf("attach platform account: nil profile")
	}
	if profile.PlatformUserID == "" {
		return nil, fmt.Errorf("attach platform account: empty platform_user_id")
	}
	platform = models.NormalizePlatformIdentifier(platform)
	if platform == "" {
		return nil, fmt.Errorf("attach platform account: empty platform")
	}

	existing, err := r.FindPlatformAccount(platform, profile.PlatformUserID)
	if err != nil {
		return nil, fmt.Errorf("attach platform account: lookup: %w", err)
	}
	if existing != nil {
		// A legacy `x` row is read through the canonical Twitter lookup.
		// Promote that same row in place only when no canonical duplicate
		// exists; the WHERE guard makes this safe under concurrent relinks
		// and leaves a pre-existing canonical row untouched.
		if platform == models.PlatformTwitter {
			if _, err := r.db.Exec(
				`UPDATE platform_accounts
				 SET platform = $1, updated_at = $2
				 WHERE id = $3 AND user_id = $5 AND platform = $4
				   AND NOT EXISTS (
					 SELECT 1 FROM platform_accounts
					 WHERE user_id = $5 AND platform = $1 AND platform_user_id = $6
				   )`,
				models.PlatformTwitter, time.Now(), existing.ID, models.PlatformX,
				userID, profile.PlatformUserID,
			); err != nil {
				return nil, fmt.Errorf("attach platform account: canonicalize legacy X alias: %w", err)
			}
			existing.Platform = models.PlatformTwitter
		}

		if existing.UserID != userID {
			// 409 surface echoes this message verbatim — keep it minimal:
			// do NOT embed profile.PlatformUserID (provider-scoped stable
			// id that the requester already knows) or any PII that would
			// otherwise leak to a stranger's logs.
			return nil, fmt.Errorf("%w: platform=%s owned_by=%d requested_by=%d",
				ErrAccountAlreadyLinked, platform, existing.UserID, userID)
		}
		// Same user — idempotent re-link. Refresh username if the
		// provider says it's changed.
		if profile.Username != "" && profile.Username != existing.Username {
			if _, err := r.db.Exec(
				`UPDATE platform_accounts SET username = $1, updated_at = $2 WHERE id = $3`,
				profile.Username, time.Now(), existing.ID,
			); err != nil {
				return nil, fmt.Errorf("attach platform account: update username: %w", err)
			}
			existing.Username = profile.Username
		}
		return existing, nil
	}

	// No prior link — create bound to the authenticated user.
	// Default status to 'pending_authorization' (NOT 'active'):
	// services.ChannelAuthorizationService.AuthorizeChannel is the
	// sole atomic gate that flips pending → active together with
	// the oauth_connections upsert + tokens write. Defaulting to
	// 'active' here would re-introduce the pre-Task-1/10
	// "active row without oauth_connection_id and without
	// encrypted token" failure mode that the user's DoD explicitly
	// forbids ("status='active' means: channel ID verified, OAuth
	// connection present, refresh token encrypted recoverable").
	account := &models.PlatformAccount{
		UserID:         userID,
		Platform:       platform,
		PlatformUserID: profile.PlatformUserID,
		Username:       profile.Username,
		Status:         models.AccountStatusPendingAuthorization,
	}
	now := time.Now()
	account.ConnectedAt = &now
	if err := r.CreatePlatformAccount(account); err != nil {
		return nil, fmt.Errorf("attach platform account: create: %w", err)
	}
	return account, nil
}
