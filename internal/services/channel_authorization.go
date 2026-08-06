// Package services — ChannelAuthorizationService.
//
// Why this service exists (Task 1/10 of the OAuth atomic-flip plan):
//
// Before this commit, the OAuth callback handler called two services
// sequentially:
//
//  1. repository.FinalizeAttach (an internal tx that UPSERTed
//     oauth_connections and promoted platform_accounts.status to
//     'active'),
//  2. credentials.VaultAPI.Save (a separate, NON-transactional write
//     that encrypted and persisted the token row).
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
//     provider_subject_id) when the provider supplies a stable grant
//     subject (Google `sub` for YouTube); legacy providers continue to
//     use provider_resource_id — returns oauth_connection_id.
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
//   - migration 053 retargeted the tokens table to FK oauth_connection_id;
//     migration 085 made the legacy channel reference nullable. Every token
//     row still carries oauth_connection_id; modern grant tokens intentionally
//     leave platform_account_id NULL.
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
	"errors"
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

// ErrOAuthRefreshTokenRequired indicates that a first YouTube authorization
// did not produce the offline refresh grant required for background publishing.
// The callback layer maps this sentinel to reauth_required instead of allowing
// AuthorizeChannel to promote the account to active.
var ErrOAuthRefreshTokenRequired = errors.New("oauth refresh token required for first YouTube authorization")

// defaultYouTubeOAuthClientKey is the migration 099 default label applied
// to oauth_connections rows whose grant was NOT issued by a pool client
// (legacy single-client deployments). AuthorizeChannel writes it when the
// callback carries no oauth_client_key so the column never holds an
// empty string and the refresh side (vault → registry.Resolve) treats
// the grant as the legacy client continuation.
const defaultYouTubeOAuthClientKey = "youtube_pool_a"

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
		oauthClientKey string,
		scopes []string,
		tokens ...*models.TokenData,
	) (int64, error)
}

// Compile-time guard.
var _ ChannelAuthorizer = (*ChannelAuthorizationService)(nil)

// Compile-time guard. Catches only WHOLE-FILE-DELETION
// regressions: a sweeping refactor that removes every reference
// to IsEligibleForActivePromotion from this file trips the build.
// Inline-map rewiring of the call site slips past this guard —
// that class is caught at runtime by the 4 status-rejection
// sqlmock integration tests in channel_authorization_test.go AND
// by the EligibilityGate spy test. See `eligibilityGate` below
// and TestAuthorizeChannel_EligibilityGateActuallyCalled_RejectsInlineMapRegression
// for the runtime defence layers.
var _ = IsEligibleForActivePromotion

// eligibilityGate is the package-level function-pointer indirection
// AuthorizeChannel routes through. Defaults to
// IsEligibleForActivePromotion (production behaviour unchanged).
// Tests swap it for a spy to prove AuthorizeChannel actually
// consults the gate — see
// TestAuthorizeChannel_EligibilityGateActuallyCalled_RejectsInlineMapRegression.
// Without the indirection the "inline-map while leaving var _ intact"
// regression class slips past every status-rejection test because
// those tests assert the REJECTION outcome, not WHICH gate produced it.
// Two complementary guards — the var _ above catches wholesale
// reference deletion at compile time; this pointer indirection
// catches rewire-at-call-site at runtime.
var eligibilityGate = IsEligibleForActivePromotion

// AuthorizeChannel is the one and only entry point that flips
// platform_accounts.status to 'active'.
//
// oauthClientKey is the YouTube OAuth Client Pool label that issued the
// grant ("youtube_pool_a" / "youtube_pool_b", from the signed OAuth
// state). It is persisted on the oauth_connections row so the refresh
// side always renews with the SAME client that issued the token. An
// empty key (legacy single-client callers: connect-link, non-pool
// callbacks, non-YouTube providers) falls back to the migration 099
// default youtube_pool_a.
//
// R7 — idempotent reconnect: the YouTube upsert is keyed on
// (user_id, provider, provider_subject_id), so re-authorising the SAME
// channel+subject (with any pool client) UPDATES the existing
// oauth_connections row in place and returns the SAME id. The token
// rows for that connection are pruned + re-inserted inside the same tx
// (SaveTokenTx), keeping the "one channel → one active connection →
// one canonical refresh token" invariant. The DO UPDATE SET
// oauth_client_key = EXCLUDED.oauth_client_key makes a reconnect
// through a different pool client flip the row to the client that
// actually issued the new grant — never leaving a mismatch between the
// stored key and the issuer.
//
// Deliberate no-go (migration 100, oauth_active_channel_client_uq):
// reconnecting a channel with a DIFFERENT Google subject while the
// (channel, client) pair already has an active grant is rejected by
// the partial unique index — the operator must disconnect the stale
// grant first. This is intentional: two live grants for one channel
// would double-count refresh tokens against Google's cap.
func (s *ChannelAuthorizationService) AuthorizeChannel(
	ctx context.Context,
	accountID int64,
	expectedChannelID string,
	oauthClientKey string,
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
	//
	// Task 2/10: routed through the package-level VerifyChannelIdentity
	// helper so this gate and the publish_worker's pre-upload check
	// share a single source of truth. Either a future Provider
	// (re-)introduction on this layer OR an unrelated pre-publish
	// refactor cannot drift the two into different behaviours.
	if expectedChannelID != "" && tokens[0].AccessToken != "" {
		if bindErr := VerifyChannelIdentity(ctx, s.binder, tokens[0].AccessToken, expectedChannelID); bindErr != nil {
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
		var refreshExpiresAt *time.Time
		if td.RefreshTokenExpiresIn > 0 {
			exp := time.Now().Add(time.Duration(td.RefreshTokenExpiresIn) * time.Second)
			refreshExpiresAt = &exp
		}
		// Keep the resource hint for legacy providers. Modern YouTube
		// grants are adjusted to NULL after the platform row is loaded
		// below, because their credential identity is the shared OAuth
		// connection rather than one discovered channel.
		encrypted[i] = &models.Token{
			PlatformAccountID:     accountID,
			TokenType:             td.TokenType,
			EncryptedAccessToken:  encAccess,
			EncryptedToken:        encAccess,
			EncryptedRefreshToken: encRefresh,
			AccessTokenExpiresAt:  expiresAt,
			ExpiresAt:             expiresAt,
			RefreshTokenExpiresAt: refreshExpiresAt,
			GrantedScopes:         td.Scopes,
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

	// Load platform + provider resource id + user id + current status.
	// The token's ProviderSubjectID is the grant identity for modern
	// YouTube authorizations; providerResourceID remains the channel/resource
	// hint stored for legacy compatibility and observability.
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
			return 0, fmt.Errorf("channel authorization: canonicalize legacy X alias: %w", canonicalizeErr)
		}
		if affected, affectedErr := result.RowsAffected(); affectedErr != nil {
			return 0, fmt.Errorf("channel authorization: inspect X alias canonicalization: %w", affectedErr)
		} else if affected == 0 {
			return 0, fmt.Errorf("channel authorization: canonical Twitter account already exists for account %d", accountID)
		}
	}
	// Eligibility gate. Routes through the package-level
	// `eligibilityGate` function-pointer indirection (default =
	// services.IsEligibleForActivePromotion — see
	// internal/services/eligibility_gate.go for the allow-list
	// policy + the explicit-exclusion rationale per status).
	// AuthorizeChannel is the SOLE current caller; any future
	// caller (worker reconnect handler, admin re-auth tool,
	// etc.) MUST route through the same pointer so the gate cannot
	// drift between consumers. The error message format below
	// is consumed by sqlmock-bound integration tests in
	// channel_authorization_test.go (asserting "not eligible for
	// active promotion" + the offending status literal) —
	// changing the format without updating those tests will fail CI.
	// The indirection (vs calling IsEligibleForActivePromotion
	// directly here) lets
	// TestAuthorizeChannel_EligibilityGateActuallyCalled_RejectsInlineMapRegression
	// swap the gate for a spy and assert AuthorizeChannel actually
	// consulted it — closing a regression class the var _ compile-
	// time guard cannot catch in isolation.
	if !eligibilityGate(currentStatus) {
		return 0, fmt.Errorf("channel authorization: account %d is in status %q which is not eligible for active promotion (allowed: pending_authorization, active, reauth_required)",
			accountID, currentStatus)
	}
	// A new YouTube row must have an offline refresh grant before it can
	// become active. Google may return an access token without
	// refresh_token when offline consent was not granted; treating that
	// response as active would create an account that cannot publish after
	// the access token expires. Reconnects from active/reauth_required are
	// intentionally allowed here: TokenRepository.SaveTokenTx uses COALESCE
	// to retain the existing encrypted refresh token when Google omits it.
	if platform == models.PlatformYouTube &&
		currentStatus == models.AccountStatusPendingAuthorization &&
		tokens[0].RefreshToken == "" {
		return 0, ErrOAuthRefreshTokenRequired
	}

	// (4) UPSERT oauth_connections. Modern YouTube callbacks use the
	// stable Google subject so every channel from the same grant reuses
	// one oauth_connection row. Other/legacy providers retain the
	// resource-keyed compatibility path. Returns oauth_connection_id.
	// scopes is wrapped in pq.Array because oauth_connections.scopes
	// is a TEXT[] column — lib/pq serialises the slice correctly,
	// whereas the bare []string would surface a
	// "converting argument $4 type: unsupported type []string"
	// driver error.
	//
	// R7: the pool client that issued the grant is part of the row
	// (oauth_client_key, migration 099) so the refresh side resolves
	// the client from the stored key. The column is NOT NULL DEFAULT
	// 'youtube_pool_a', so legacy callers that never set the key get
	// the honest legacy label instead of an empty string.
	clientKey := oauthClientKey
	if clientKey == "" {
		clientKey = defaultYouTubeOAuthClientKey
	}
	var upsertErr error
	if platform == models.PlatformYouTube && tokens[0].ProviderSubjectID != "" {
		upsertErr = tx.QueryRowContext(ctx,
			`INSERT INTO oauth_connections (user_id, provider, provider_subject_id, provider_resource_id, oauth_client_key, scopes, granted_scopes, last_validated_at)
			 VALUES ($1, $2, $3, $4, $5, $6, $6, NOW())
			 ON CONFLICT (user_id, provider, provider_subject_id) WHERE provider_subject_id <> ''
			 DO UPDATE SET provider_resource_id = EXCLUDED.provider_resource_id,
			               oauth_client_key = EXCLUDED.oauth_client_key,
			               scopes = EXCLUDED.scopes,
			               granted_scopes = EXCLUDED.granted_scopes,
			               last_validated_at = NOW(),
			               updated_at = NOW()
			 RETURNING id`,
			userID, platform, tokens[0].ProviderSubjectID, providerResourceID, clientKey, pq.Array(scopes),
		).Scan(&oauthConnectionID)
	} else {
		upsertErr = tx.QueryRowContext(ctx,
			`INSERT INTO oauth_connections (user_id, provider, provider_resource_id, scopes, granted_scopes, last_validated_at)
			 VALUES ($1, $2, $3, $4, $4, NOW())
			 ON CONFLICT (user_id, provider, provider_resource_id) WHERE provider_subject_id = ''
			 DO UPDATE SET scopes = EXCLUDED.scopes,
			               granted_scopes = EXCLUDED.granted_scopes,
			               last_validated_at = NOW(),
			               updated_at = NOW()
			 RETURNING id`,
			userID, platform, providerResourceID, pq.Array(scopes),
		).Scan(&oauthConnectionID)
	}
	if upsertErr != nil {
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
		if platform == models.PlatformYouTube && tokens[i].ProviderSubjectID != "" {
			// The channel remains linked through
			// platform_accounts.oauth_connection_id; do not make a
			// shared grant appear owned by this one channel.
			t.PlatformAccountID = 0
		}
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
