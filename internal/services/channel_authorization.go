// Package services — ChannelAuthorizationService.
//
// Why this service exists (Task 1/10 of the OAuth atomic-flip plan):
//
// Before this commit, the OAuth callback handler called two services
// sequentially:
//
//	1. repository.FinalizeAttach (an internal tx that UPSERTed
//	   oauth_connections and promoted platform_accounts.status to
//	   'active'),
//	2. credentials.VaultAPI.Save (a separate, NON-transactional write
//	   that encrypted and persisted the token row).
//
// If step 2 failed AFTER step 1 succeeded, the platform_account row
// was already marked 'active' while the tokens table held ZERO rows
// for it — a "looks ready, has no credentials" failure that surfaced
// as api_oauth_credentials_missing on the next publish attempt. For
// a fleet of 200 channels this is unacceptable.
//
// ChannelAuthorizationService.AuthorizeChannel merges the two writes
// into ONE database transaction. Any failure between BEGIN and COMMIT
// rolls BOTH writes back, leaving the platform_account in its
// pre-call state (typically 'pending_authorization' or
// 'reauth_required'). The status='active' flip is now provably
// co-resident with the encrypted-token row.
//
// Atomic flow (matches the user's Task 1/10 spec):
//
//  1. (Optional) channels.list(mine=true) guard when expectedChannelID
//     is non-empty AND a YouTubeChannelBinder is wired. Mismatch
//     returns ErrYouTubeChannelMismatch (mapped to 422 by the HTTP
//     layer).
//  2. Pre-encrypt every supplied TokenData — encryption failures
//     abort BEFORE BEGIN, never touching the DB.
//  3. BEGIN tx.
//  4. UPSERT oauth_connections keyed on (user_id, provider,
//     provider_resource_id) — returns oauth_connection_id.
//  5. INSERT one row into tokens per encrypted TokenData (via
//     credentials.TokenStore.SaveTokenTx, a tx-aware variant that
//     also prunes older rows inside the same tx).
//  6. UPDATE platform_accounts to status='active' and link the FK.
//  7. COMMIT.
//
// Errors at steps (4..6) → ROLLBACK via the deferred safety net;
// the platform_account keeps its previous status, and zero rows
// are written to tokens / oauth_connections.
//
// Schema preconditions (one-liner to keep future edits honest):
//
//   - migration 043 created oauth_connections + the FK from
//     platform_accounts; oauth_connection_id is the canonical
//     OAuth-grant lineage key.
//   - migration 053 retargeted the tokens table to FK oauth_connection_id
//     and SET NOT NULL on that column. Do NOT relax the NOT NULL
//     without revisiting this service — the atomic flow assumes
//     every token row carries an oauth_connection_id.
//
// Invariants enforced by this service:
//
//   - tokens[0] (the principal token) is ALWAYS first; the binder
//     pre-tx guard uses tokens[0].AccessToken. handlers.go builds
//     channelTokens = [principal] + supplementals so this ordering
//     is structural, not coincidental.
//   - On success, exactly ONE oauth_connections.upsert + N
//     tokens INSERTs + ONE platform_accounts UPDATE fire inside
//     the SAME tx.
//   - On failure at any step, ZERO rows of the three writes commit.
package services

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/lib/pq"

	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/crypto"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// ChannelAuthorizationService is the SINGLE gate that flips
// platform_accounts.status to 'active'. See package doc.
type ChannelAuthorizationService struct {
	db        *sql.DB
	encryptor *crypto.Encryptor
	store     credentials.TokenStore
	// binder is OPTIONAL — nil for non-YouTube providers that
	// have no channels.list(mine=true) check. When non-nil AND a
	// non-empty expectedChannelID is supplied, the service calls
	// binder.ValidateChannelBinding as an authoritative pre-tx
	// guard. The defensive bind call exists because a future
	// provider path could omit the discoverer-side check — this
	// is the one and only place the validation needs to fire.
	binder YouTubeChannelBinder
}

// NewChannelAuthorizationService wires the service. Pass binder=nil
// for providers that have no channels.list(mine=true) check (every
// non-YouTube provider today).
func NewChannelAuthorizationService(
	db *sql.DB,
	enc *crypto.Encryptor,
	store credentials.TokenStore,
	binder YouTubeChannelBinder,
) *ChannelAuthorizationService {
	return &ChannelAuthorizationService{
		db:        db,
		encryptor: enc,
		store:     store,
		binder:    binder,
	}
}

// ChannelAuthorizer is the narrow interface the HTTP router uses to
// invoke the atomic-flip primitive. Defined here (alongside the
// concrete implementation) so pkg/api and tests can type-assert
// without importing the concrete type or knowing how the underlying
// DB writes are sequenced.
type ChannelAuthorizer interface {
	AuthorizeChannel(
		ctx context.Context,
		accountID int64,
		expectedChannelID string,
		scopes []string,
		tokens ...*models.TokenData,
	) (int64, error)
}

// Compile-time guard.
var _ ChannelAuthorizer = (*ChannelAuthorizationService)(nil)

// AuthorizeChannel is the one and only entry point that flips
// platform_accounts.status to 'active'.
func (s *ChannelAuthorizationService) AuthorizeChannel(
	ctx context.Context,
	accountID int64,
	expectedChannelID string,
	scopes []string,
	tokens ...*models.TokenData,
) (oauthConnectionID int64, err error) {
	if accountID <= 0 {
		return 0, fmt.Errorf("channel authorization: accountID must be > 0 (got %d)", accountID)
	}
	if len(tokens) == 0 {
		return 0, fmt.Errorf("channel authorization: at least one TokenData required (account %d)", accountID)
	}
	for i, t := range tokens {
		if t == nil {
			return 0, fmt.Errorf("channel authorization: tokens[%d] is nil", i)
		}
	}
	if s.db == nil {
		return 0, fmt.Errorf("channel authorization: db is nil")
	}
	if s.encryptor == nil {
		return 0, fmt.Errorf("channel authorization: encryptor is nil")
	}
	if s.store == nil {
		return 0, fmt.Errorf("channel authorization: token store is nil")
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	// (1) channels.list(mine=true) guard. Uses tokens[0].AccessToken
	// per the package invariant ("tokens[0] = principal token").
	// The guard is intentionally a no-op when:
	//   - expectedChannelID is "" (user-driven OAuth via the
	//     generic /auth/login flow, without a connect-link hint);
	//   - binder is nil (every non-YouTube provider today);
	//   - the principal token's AccessToken is empty (mis-config).
	if expectedChannelID != "" && s.binder != nil && tokens[0].AccessToken != "" {
		if bindErr := s.binder.ValidateChannelBinding(ctx, tokens[0].AccessToken, expectedChannelID); bindErr != nil {
			return 0, fmt.Errorf("channel authorization: channel binding guard failed: %w", bindErr)
		}
	}

	// (2) Pre-encrypt every TokenData. Failures abort BEFORE BEGIN —
	// no DB writes when an encryption error surfaces. Encrypting
	// outside the tx lets us reuse the production Encryptor (the
	// cipher envelope format is unchanged from vault.Save).
	encrypted := make([]*models.Token, len(tokens))
	for i, td := range tokens {
		encAccess, encErr := s.encryptor.Encrypt(td.AccessToken)
		if encErr != nil {
			return 0, fmt.Errorf("channel authorization: encrypt access token %d: %w", i, encErr)
		}
		var encRefresh []byte
		if td.RefreshToken != "" {
			encRefresh, encErr = s.encryptor.Encrypt(td.RefreshToken)
			if encErr != nil {
				return 0, fmt.Errorf("channel authorization: encrypt refresh token %d: %w", i, encErr)
			}
		}
		var expiresAt *time.Time
		if td.ExpiresIn > 0 {
			exp := time.Now().Add(time.Duration(td.ExpiresIn) * time.Second)
			expiresAt = &exp
		}
		encrypted[i] = &models.Token{
			PlatformAccountID:     accountID,
			TokenType:             td.TokenType,
			EncryptedToken:        encAccess,
			EncryptedRefreshToken: encRefresh,
			ExpiresAt:             expiresAt,
			Scopes:                td.Scopes,
		}
	}

	// (3) BEGIN tx.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("channel authorization: begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	// Load platform + provider_resource_id + user_id + current
	// status — the keys for the oauth_connections UPSERT AND
	// the eligibility gate below.
	var (
		platform           string
		providerResourceID string
		userID             int64
		currentStatus      string
	)
	if scanErr := tx.QueryRowContext(ctx,
		`SELECT platform, platform_user_id, user_id, status
		   FROM platform_accounts
		  WHERE id = $1`,
		accountID,
	).Scan(&platform, &providerResourceID, &userID, &currentStatus); scanErr != nil {
		return 0, fmt.Errorf("channel authorization: load account %d: %w", accountID, scanErr)
	}
	if userID <= 0 {
		return 0, fmt.Errorf("channel authorization: platform_accounts.user_id is zero for account %d", accountID)
	}
	// Eligibility gate. Active transition is allowed ONLY from
	// states that the OAuth callback re-write makes meaningful:
	//   - pending_authorization: CSV-import reset (P2 — admin
	//     connect-link happy path).
	//   - active: refresh-on-the-same-grant via re-consent (P1).
	//   - reauth_required: previous refresh failed, operator
	//     clicked "reconnect" and the new code-exchange succeeded.
	// 'expired' is intentionally excluded: today the worker is
	// the only code path that mints an 'expired' status (via
	// vault.Renew) and reconnect-from-expired through the OAuth
	// callback first flips the row to reauth_required by the
	// disconnect → reconnect flow. Adding 'expired' here would
	// risk resurrecting an account whose grant has been lost;
	// widen this allow-list if a follow-up doesn't reintroduce
	// that risk.
	eligible := map[string]bool{
		models.AccountStatusPendingAuthorization: true,
		models.AccountStatusActive:               true,
		models.AccountStatusReauthRequired:       true,
	}
	if !eligible[currentStatus] {
		return 0, fmt.Errorf("channel authorization: account %d is in status %q which is not eligible for active promotion (allowed: pending_authorization, active, reauth_required)",
			accountID, currentStatus)
	}

	// (4) UPSERT oauth_connections. Idempotent on (user_id,
	// provider, provider_resource_id). On conflict refreshes
	// scopes + last_validated_at. Returns oauth_connection_id.
	// scopes is wrapped in pq.Array because oauth_connections.scopes
	// is a TEXT[] column — lib/pq serialises the slice correctly,
	// whereas the bare []string would surface a
	// "converting argument $4 type: unsupported type []string"
	// driver error.
	if upsertErr := tx.QueryRowContext(ctx,
		`INSERT INTO oauth_connections (user_id, provider, provider_resource_id, scopes, last_validated_at)
		 VALUES ($1, $2, $3, $4, NOW())
		 ON CONFLICT (user_id, provider, provider_resource_id)
		 DO UPDATE SET scopes = EXCLUDED.scopes,
		               last_validated_at = NOW(),
		               updated_at = NOW()
		 RETURNING id`,
		userID, platform, providerResourceID, pq.Array(scopes),
	).Scan(&oauthConnectionID); upsertErr != nil {
		return 0, fmt.Errorf("channel authorization: upsert oauth_connections: %w", upsertErr)
	}

	// (5) INSERT one row per encrypted TokenData via the
	// tx-aware TokenStore.SaveTokenTx — its internal pruner
	// (DELETE older rows for same (oauth_connection_id,
	// token_type)) runs ALSO inside the same tx. This mirrors
	// TokenRepository.SaveToken's contract while keeping the
	// promise that a roll-back drops the new rows AND the
	// pruned older rows together.
	for i, t := range encrypted {
		t.OAuthConnectionID = oauthConnectionID
		if saveErr := s.store.SaveTokenTx(ctx, tx, t); saveErr != nil {
			return 0, fmt.Errorf("channel authorization: save token %d: %w", i, saveErr)
		}
	}

	// (6) Promote the platform_account. Clearing
	// reauth_required_at + last_error_* is intentional: a
	// successful fresh authorize means the operator's
	// dashboard should drop the "needs reconnect" signal.
	if _, execErr := tx.ExecContext(ctx,
		`UPDATE platform_accounts
		    SET oauth_connection_id = $1,
		        status             = 'active',
		        connected_at       = NOW(),
		        last_validated_at  = NOW(),
		        reauth_required_at = NULL,
		        last_error_code    = NULL,
		        last_error_message = NULL,
		        updated_at         = NOW()
		  WHERE id = $2`,
		oauthConnectionID, accountID,
	); execErr != nil {
		return 0, fmt.Errorf("channel authorization: update platform_accounts %d: %w", accountID, execErr)
	}

	// (7) COMMIT. From this point on the platform_account is
	// provably 'active' AND has a fresh token row — the
	// principal invariant of the service.
	if commitErr := tx.Commit(); commitErr != nil {
		return 0, fmt.Errorf("channel authorization: commit: %w", commitErr)
	}
	committed = true
	return oauthConnectionID, nil
}
