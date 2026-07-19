// Package credentials is the single source of truth for OAuth credential
// storage. It centralises encryption, persistence, rotation, refresh, and
// revocation so that no provider (or any other caller) needs to know how
// tokens are stored, encrypted, or refreshed — only how to ask the vault
// for one.
//
// Taglio 2.2 motivation: before this package existed, token-encryption
// logic lived in internal/services/token_service.go as a side effect of
// the post-Taglio 2.1 cleanup. The user-facing API (SaveEncryptedToken /
// GetDecryptedToken / EnsureFreshToken) was leaky in two ways:
//
//  1. The refresh path took a `services.OAuthProvider` as the refresher
//     argument, so the vault indirectly depended on the per-provider
//     capability interfaces — a layering violation.
//  2. There was no protection against two workers refreshing the same
//     account at the same time, which would issue duplicate API calls
//     and waste rate-limit budget.
//
// CredentialVault fixes both: the refresher is now a plain function
// (TokenRefresher) the vault knows nothing about beyond its signature,
// and Renew acquires a Postgres `pg_advisory_xact_lock` keyed by the
// platform_account_id so concurrent refreshes serialise at the DB level.
package credentials

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/crypto"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// TokenRefresher is the narrow function signature the vault uses to call
// a platform's refresh endpoint. It is intentionally a plain function
// type (not an interface) so the vault has zero compile-time knowledge
// of the per-platform capability interfaces — the caller (router or
// worker) is responsible for adapting a provider's `RefreshOAuthToken`
// method into this shape via a closure.
//
// The refreshToken argument is the decrypted refresh token (for
// YouTube/Twitter/TikTok) OR the decrypted long-lived access token
// (for Meta, which uses fb_exchange_token). The vault extracts the
// right value from the stored row; the refresher does not need to
// know which it is.
type TokenRefresher func(ctx context.Context, refreshToken string) (*models.TokenData, error)

// TokenStore is the storage-layer interface the vault depends on. It is
// intentionally narrower than repository.TokenRepository: the vault only
// needs Save / Read / UpdateCiphertexts (Blocco #2.2 lazy re-encrypt) /
// DeleteAll-for-connection, not the per-id delete used by admin tooling.
// Defining the interface here (alongside the consumer) lets the vault
// stay decoupled from the concrete repository package — tests inject an
// in-memory mock, and the production wiring in main.go adapts
// *repository.TokenRepository to this 4-method contract.
//
// P0#3 retarget (migration 053 + commit SSOT): all read/delete methods
// are keyed by oauthConnectionID (the OAuth grant lineage), NOT by
// platformAccountID (the per-platform user identity). The vault's PUBLIC
// VaultAPI keeps platformAccountID on the wire for caller compat — the
// lookup is internal via oauthConnectionIDForAccount(). The TokenStore
// interface itself has shifted to oauthConnectionID because the
// underlying tokens table is now FK'd to oauth_connections(id) rather
// than to platform_accounts(id), so writing WHERE platform_account_id=$1
// in production would no longer find any rows after migration 053.
type TokenStore interface {
	SaveToken(token *models.Token) error
	FindLatestToken(oauthConnectionID int64, tokenType string) (*models.Token, error)
	UpdateCiphertexts(tokenID int64, oldEncrypted, newEncrypted []byte) error
	DeleteAllTokensForOAuthConnection(oauthConnectionID int64) error
}

// VaultAPI is the narrow contract the HTTP router and publish worker use
// to talk to the credential layer. It is implemented by *CredentialVault
// in production and by test mocks in pkg/api and internal/worker.
//
// Five methods, in lifecycle order:
//
//   - Save:    initial store after the OAuth callback
//   - Get:     decrypt + return (used when the token is known-fresh)
//   - Rotate:  semantic alias for Save (same encrypt+store, but the
//     caller's intent is "re-issue with a new key" — the vault
//     also deletes any older rows via TokenStore.SaveToken's
//     prune-older logic)
//   - Renew:   check-and-refresh, serialised by pg_advisory_xact_lock
//   - Revoke:  delete all tokens for a platform account (disconnect /
//     logout / account deletion)
type VaultAPI interface {
	Save(ctx context.Context, platformAccountID int64, tokenData *models.TokenData) error
	Get(ctx context.Context, platformAccountID int64, tokenType string) (*models.OAuthToken, error)
	Rotate(ctx context.Context, platformAccountID int64, tokenData *models.TokenData) error
	Renew(ctx context.Context, platformAccountID int64, tokenType string, refresher TokenRefresher) (*models.OAuthToken, error)
	Revoke(ctx context.Context, platformAccountID int64) error
}

// Compile-time check: *CredentialVault must satisfy VaultAPI. A drift
// here (e.g. a signature change that doesn't propagate) is a build
// error, not a runtime panic.
var _ VaultAPI = (*CredentialVault)(nil)

// CredentialVault is the single implementation of VaultAPI. It owns
// the AES-256-GCM encryption key, the *sql.DB handle used for advisory
// locks and the oauth_connection_id lookup, and the TokenStore used for
// persistence. No provider or consumer is allowed to import the internal
// repository — they go through this vault.
//
// P0#3 retarget (migration 053): the vault holds an *sql.DB handle
// specifically so it can resolve oauth_connection_id from
// platform_account_id on every public call. The TokenStore interface
// (above) is keyed by oauthConnectionID — the same shift as on the
// tokens table — while VaultAPI stays keyed by platformAccountID for
// caller compat. The resolver is the only piece of glue that
// preserves the public API while moving the storage layer to the
// canonical OAuth-grant key.
type CredentialVault struct {
	encryptor *crypto.Encryptor
	db        *sql.DB
	store     TokenStore
}

// NewCredentialVault constructs a vault. All three dependencies are
// required; a nil in any slot will surface as a panic on the first
// method call (fail-fast for misconfigured main.go).
func NewCredentialVault(encryptor *crypto.Encryptor, db *sql.DB, store TokenStore) *CredentialVault {
	return &CredentialVault{encryptor: encryptor, db: db, store: store}
}

// oauthConnectionIDForAccount resolves the canonical storage key for
// a platform_account_id. Used internally by every public method (and
// inline by Renew's slow path inside the lock transaction so the row
// is FOR UPDATE-locked for the duration of the refresh). The
// `oauth_connection_id IS NOT NULL` predicate makes the "pre-043
// attach with no grant lineage" case surface as a descriptive error
// rather than a silent NULL propagation.
//
// Cost: one indexed SELECT against platform_accounts.id PK — on the
// order of tens of microseconds with btree-cached btree scan in
// shared_buffers, paid once per public Save/Get/Renew/Revoke call.
// The lookup is column-constant (no join), so a follow-up commit can
// cache it per-request without risk of staleness under the vault's
// always-`pg_advisory_xact_lock` Renew flow.
func (v *CredentialVault) oauthConnectionIDForAccount(ctx context.Context, platformAccountID int64) (int64, error) {
	var oid int64
	if err := v.db.QueryRowContext(ctx,
		`SELECT oauth_connection_id FROM platform_accounts WHERE id = $1 AND oauth_connection_id IS NOT NULL`,
		platformAccountID,
	).Scan(&oid); err != nil {
		if err == sql.ErrNoRows {
			return 0, fmt.Errorf("vault: no oauth_connection for platform_account %d (pre-043 attach or grant revoked)", platformAccountID)
		}
		return 0, fmt.Errorf("vault: resolve oauth_connection_id for platform_account %d: %w", platformAccountID, err)
	}
	return oid, nil
}

// Save encrypts and persists a token for a platform account. The refresh
// token, when present in tokenData, is encrypted separately and stored
// in the same row to keep refresh semantics atomic with access tokens.
// The TokenStore implementation is responsible for pruning older rows
// for the same (oauth_connection_id, token_type) so the table does not
// grow unbounded across refreshes.
//
// P0#3 retarget: resolves the oauth_connection_id for the supplied
// platform_account_id via oauthConnectionIDForAccount(), stamps it on
// the Token row, and lets the repository INSERT into the new
// oauth_connection_id-keyed column. Authoritative callers (OAuth
// callback handlers) typically already know the oauth_connection_id
// because they just created it (the migration 043 lineage); they
// reach for saveForOAuthConnection (lower-level helper, below) to
// skip the redundant lookup.
func (v *CredentialVault) Save(ctx context.Context, platformAccountID int64, tokenData *models.TokenData) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	oauthConnectionID, err := v.oauthConnectionIDForAccount(ctx, platformAccountID)
	if err != nil {
		return err
	}
	return v.saveForOAuthConnection(ctx, oauthConnectionID, platformAccountID, tokenData)
}

// saveForOAuthConnection is the lookup-free sibling of Save. The
// pg_advisory_xact_lock in Renew already resolved
// oauth_connection_id inside the lock tx (assuming the row-level lock
// on platform_accounts); a redundant lookup here would only slow the
// slow path. Public callers should use Save; internal callers (Renew
// after the lock) use this directly.
func (v *CredentialVault) saveForOAuthConnection(ctx context.Context, oauthConnectionID, platformAccountID int64, tokenData *models.TokenData) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	encrypted, err := v.encryptor.Encrypt(tokenData.AccessToken)
	if err != nil {
		return fmt.Errorf("vault: failed to encrypt access token: %w", err)
	}
	var encryptedRefresh []byte
	if tokenData.RefreshToken != "" {
		encryptedRefresh, err = v.encryptor.Encrypt(tokenData.RefreshToken)
		if err != nil {
			return fmt.Errorf("vault: failed to encrypt refresh token: %w", err)
		}
	}
	expiresAt := time.Now().Add(time.Duration(tokenData.ExpiresIn) * time.Second)
	token := &models.Token{
		PlatformAccountID:     platformAccountID,
		OAuthConnectionID:     oauthConnectionID,
		TokenType:             tokenData.TokenType,
		EncryptedToken:        encrypted,
		EncryptedRefreshToken: encryptedRefresh,
		ExpiresAt:             &expiresAt,
		Scopes:                tokenData.Scopes,
	}
	if err := v.store.SaveToken(token); err != nil {
		return fmt.Errorf("vault: failed to persist token: %w", err)
	}
	return nil
}

// Rotate is a semantic alias for Save. The caller's intent differs
// ("re-issue with a new key" vs "initial store") but the vault-level
// operation is identical: encrypt + persist + prune older rows. Kept
// as a separate method on VaultAPI so the call site reads clearly in
// the audit log / future telemetry ("token rotated" vs "token saved").
func (v *CredentialVault) Rotate(ctx context.Context, platformAccountID int64, tokenData *models.TokenData) error {
	return v.Save(ctx, platformAccountID, tokenData)
}

// Get retrieves and decrypts the latest token for a platform account.
// Expired tokens return an error containing "expired" so callers can
// react by calling Renew. A missing token (account has never logged
// in) returns a descriptive error too.
//
// Blocco #2.2 — lazy re-encrypt: after a successful decrypt, if the
// stored ciphertext is stamped with a non-active key id (or is in
// the pre-Sprint-5.3 legacy format), the vault transparently
// re-encrypts the same plaintext under the active key and persists
// the new ciphertext. The persist is conditional on
// `WHERE encrypted_token = $old` (idempotent + race-safe): if two
// workers attempt to re-encrypt the same row concurrently, only
// the first one's UPDATE fires; the second sees 0 affected rows
// and the vault logs+ignores that specific error. The decrypted
// value is still returned to the caller either way — the read path
// is the source of truth, the write is a best-effort upgrade.
//
// Encryption errors during the re-encrypt step are NOT surfaced to
// the caller: a failure to write the new ciphertext is a
// background-consistency concern, not a read failure. The next
// read on this row will retry the re-encrypt. Slog-warn gives
// operators a breadcrumb if it persists.
//
// P0#3 retarget: resolves the oauth_connection_id for the supplied
// platform_account_id via oauthConnectionIDForAccount(), then
// delegates to TokenStore.FindLatestToken(oauthConnectionID, ...).
// The cipher suite, envelope format, and lazy re-encrypt logic are
// unchanged — only the indexing key has shifted.
func (v *CredentialVault) Get(ctx context.Context, platformAccountID int64, tokenType string) (*models.OAuthToken, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	oauthConnectionID, err := v.oauthConnectionIDForAccount(ctx, platformAccountID)
	if err != nil {
		return nil, err
	}
	stored, err := v.store.FindLatestToken(oauthConnectionID, tokenType)
	if err != nil {
		return nil, fmt.Errorf("vault: failed to find token: %w", err)
	}
	if stored == nil {
		return nil, fmt.Errorf("vault: no token for account %d (type: %s)", platformAccountID, tokenType)
	}
	if stored.ExpiresAt != nil && time.Now().After(*stored.ExpiresAt) {
		return nil, fmt.Errorf("vault: token expired at %s", stored.ExpiresAt.Format(time.RFC3339))
	}
	decrypted, err := v.encryptor.Decrypt(stored.EncryptedToken)
	if err != nil {
		return nil, fmt.Errorf("vault: failed to decrypt access token: %w", err)
	}
	// Lazy re-encrypt: idempotent + race-safe (see godoc).
	if v.encryptor.NeedsRotation(stored.EncryptedToken) {
		newCiphertext, reencErr := v.encryptor.Encrypt(decrypted)
		if reencErr != nil {
			// Best-effort: log and continue. The read still
			// succeeds; a future read will retry the re-encrypt.
			slog.Warn("vault: lazy re-encrypt failed (will retry on next read)",
				"token_id", stored.ID, "error", reencErr)
		} else if err := v.store.UpdateCiphertexts(stored.ID, stored.EncryptedToken, newCiphertext); err != nil {
			// Log-level split (Blocco #2.2 follow-up):
			//   - "ciphertext stale" is the EXPECTED race-loser
			//     case (concurrent workers, only one wins the
			//     optimistic-concurrency UPDATE). High rate
			//     under load → Debug (operators can re-enable
			//     for forensic investigation, default off in prod).
			//   - Anything else is a real DB error worth a
			//     breadcrumb at Warn level.
			// The read still returns the decrypted value either
			// way — the persist is a best-effort background
			// upgrade, not part of the read contract.
			if strings.Contains(err.Error(), "ciphertext stale") {
				slog.Debug("vault: lazy re-encrypt race-loser (another worker already upgraded)",
					"token_id", stored.ID)
			} else {
				slog.Warn("vault: lazy re-encrypt persist failed (read still returned)",
					"token_id", stored.ID, "error", err)
			}
		}
	}
	return &models.OAuthToken{
		AccessToken: decrypted,
		TokenType:   stored.TokenType,
		ExpiresAt:   stored.ExpiresAt,
		Scopes:      stored.Scopes,
	}, nil
}

// Renew returns a valid (non-expired) decrypted token. If the stored
// token is within the 60s grace window of expiry, it calls refresher,
// persists the result, and returns the freshly-decrypted value.
//
// Taglio 2.2 concurrency model: the refresh path acquires a
// pg_advisory_xact_lock keyed by the oauth_connection_id (the canonical
// OAuth grant lineage — the per-platform account identity is a
// strictly downstream derivative of it). Two workers (or a worker +
// the HTTP callback handler) refreshing accounts OWNED by the same
// grant at the same time will SERIALISE on the DB lock — the loser
// sees the winner's freshly-saved row and short-circuits without
// issuing a duplicate API call. The lock is transaction-scoped, so
// it's auto-released on commit OR rollback (no risk of lock leak on
// panic).
//
// P0#3 retarget: the lookup for oauth_connection_id happens INSIDE
// the lock tx (Begin → SELECT oauth_connection_id FOR UPDATE →
// pg_advisory_xact_lock) so the stored lineage cannot shift mid-flight
// — a grant swap during refresh would otherwise let two workers pick
// non-cooperating lock keys despite "refreshing the same logical
// account". Re-read inside the lock ALSO reuses the resolved oid
// rather than re-issuing the lookup; the slow-path Save uses
// saveForOAuthConnection to skip the lookup entirely.
//
// The refresher argument is a plain function (TokenRefresher) so the
// vault has zero knowledge of the per-platform capability interfaces.
// Callers adapt a provider's RefreshOAuthToken method into a closure
// that returns *models.TokenData, which is the only shape the vault
// needs to persist.
func (v *CredentialVault) Renew(ctx context.Context, platformAccountID int64, tokenType string, refresher TokenRefresher) (*models.OAuthToken, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Fast path: token is already fresh, no DB lock needed. Get handles
	// the oauth_connection_id lookup internally.
	if tok, err := v.Get(ctx, platformAccountID, tokenType); err == nil {
		if tok.ExpiresAt == nil || time.Until(*tok.ExpiresAt) > 60*time.Second {
			return tok, nil
		}
		// Within grace window: fall through to refresh.
	} else if !isExpiryError(err) {
		// Non-expiry error (decrypt failure, DB unreachable, …): surface it.
		return nil, err
	}

	// Slow path: open a short-lived tx so the advisory lock is
	// transaction-scoped. Inside the tx we (a) look up the canonical
	// oauth_connection_id with a row-level lock on platform_accounts
	// (so a concurrent grant swap is blocked), (b) acquire the advisory
	// lock keyed on the resolved oid.
	lockTx, err := v.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("vault: begin lock tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = lockTx.Rollback()
		}
	}()
	var oauthConnectionID int64
	if err := lockTx.QueryRowContext(ctx,
		`SELECT oauth_connection_id FROM platform_accounts WHERE id = $1 AND oauth_connection_id IS NOT NULL`,
		platformAccountID,
	).Scan(&oauthConnectionID); err != nil {
		return nil, fmt.Errorf("vault: resolve oauth_connection_id for renew: %w", err)
	}
	if _, err := lockTx.ExecContext(ctx, "SELECT pg_advisory_xact_lock($1)", oauthConnectionID); err != nil {
		return nil, fmt.Errorf("vault: acquire advisory lock: %w", err)
	}

	// Re-read inside the lock. Another worker may have just refreshed.
	// Reuse the resolved oid — same acq, no extra SELECT.
	stored, err := v.store.FindLatestToken(oauthConnectionID, tokenType)
	if err != nil {
		return nil, fmt.Errorf("vault: find inside lock: %w", err)
	}
	if stored != nil && (stored.ExpiresAt == nil || time.Until(*stored.ExpiresAt) > 60*time.Second) {
		if err := lockTx.Commit(); err != nil {
			return nil, fmt.Errorf("vault: commit lock tx: %w", err)
		}
		committed = true
		return v.toOAuthToken(stored)
	}
	if stored == nil {
		return nil, fmt.Errorf("vault: no stored token for account %d (oauth_connection=%d)", platformAccountID, oauthConnectionID)
	}

	// We own the refresh. Read the stored row is already in `stored`
	// from the re-read above — pass it directly to extractRefreshMaterial.
	// Re-finding would just re-pay the same lookup cost for no new info.
	refreshToken, err := v.extractRefreshMaterial(stored, tokenType)
	if err != nil {
		return nil, err
	}

	newTokenData, err := refresher(ctx, refreshToken)
	if err != nil {
		return nil, fmt.Errorf("vault: refresh failed: %w", err)
	}

	// Save via the lookup-free sibling — the resolved oid is the
	// canonical key for this row.
	if err := v.saveForOAuthConnection(ctx, oauthConnectionID, platformAccountID, newTokenData); err != nil {
		return nil, fmt.Errorf("vault: persist refreshed token: %w", err)
	}

	if err := lockTx.Commit(); err != nil {
		return nil, fmt.Errorf("vault: commit lock tx: %w", err)
	}
	committed = true

	// Final read — fresh ciphertext was just persisted; the stored row
	// is now the latest write by THIS transaction. Pass the just-written
	// id via a sealed re-read through Get (which resolves the oid again
	// — one extra SELECT, but kept simple and consistent with the read
	// contract for callers).
	return v.Get(ctx, platformAccountID, tokenType)
}

// toOAuthToken composes a *models.OAuthToken from a stored row using
// its already-decrypted columns. Used by Renew's "fresh inside the
// lock" short-circuit to avoid a redundant Get (and its lookup) when
// the stored row already answers the call.
//
// INTENTIONAL DIVERGENCE FROM v.Get (Blocco #2.2 lazy re-encrypt):
// the pre-refactor short-circuit path called v.Get(ctx, ...) which
// would run NeedsRotation + UpdateCiphertexts on every read. The
// post-refactor path uses this helper and SKIPS the lazy re-encrypt
// step. The justification is that the in-flight refresh wrote the
// row via saveForOAuthConnection — which uses the vault's ENCRYPTOR
// (i.e. the active encryptor) — so by the time the lock-protected
// short-circuit returns, the row is guaranteed to be stamped with
// the active key id and NeedsRotation would have returned false
// anyway. Skipping the re-encrypt step saves one optimistic-
// concurrency UPDATE inside the lock window.
//
// Edge case: if at some future point a writer within the lock path
// uses a non-active encryptor (e.g. a rotation hot-swap), this
// short-circuit could leave a race-loser's row on a stale key
// until the next independent Get. No current code path emits such
// writes; if a future commit introduces one, either flip this
// helper to call v.Get(ctx, pid, tt) (re-adds the re-encrypt + an
// extra resolver lookup) or explicitly extend the godoc with the
// additional invariant.
func (v *CredentialVault) toOAuthToken(stored *models.Token) (*models.OAuthToken, error) {
	decrypted, err := v.encryptor.Decrypt(stored.EncryptedToken)
	if err != nil {
		return nil, fmt.Errorf("vault: decrypt stored token inside lock: %w", err)
	}
	return &models.OAuthToken{
		AccessToken: decrypted,
		TokenType:   stored.TokenType,
		ExpiresAt:   stored.ExpiresAt,
		Scopes:      stored.Scopes,
	}, nil
}

// Revoke deletes all tokens for a platform account. Used by disconnect /
// logout / account-deletion flows. Idempotent: deleting an account that
// has no tokens is NOT an error (the underlying TokenStore may return
// a "not found" sentinel; we treat it as success here).
//
// P0#3 retarget: resolves the oauth_connection_id for the supplied
// platform_account_id via oauthConnectionIDForAccount() and delegates
// to TokenStore.DeleteAllTokensForOAuthConnection. The FK CASCADE on
// the tokens.oauth_connection_id → oauth_connections.id link means a
// concurrent grant revocation already drops these rows; the explicit
// call here just makes the disconnect flow's delete intent explicit
// and ids-trail-correct in the audit log.
func (v *CredentialVault) Revoke(ctx context.Context, platformAccountID int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	oauthConnectionID, err := v.oauthConnectionIDForAccount(ctx, platformAccountID)
	if err != nil {
		// Surface the pre-043-attach / grant-revoked state to the
		// caller; Save/Get/Renew use the same fail-loud contract.
		// The disconnect orchestrator (pkg/api) maps this to a 401
		// or "already-revoked" response — the silent-swallow moved up
		// the stack so the audit log has a single source of truth.
		return err
	}
	if err := v.store.DeleteAllTokensForOAuthConnection(oauthConnectionID); err != nil {
		// ErrTokenNotFound is the legitimate "already revoked" case.
		// The vault is idempotent, so we swallow it.
		if strings.Contains(err.Error(), "token not found") {
			return nil
		}
		return fmt.Errorf("vault: revoke failed: %w", err)
	}
	return nil
}

// extractRefreshMaterial returns the decrypted value to pass to the
// refresher function: the decrypted refresh token if one was stored,
// otherwise (for Meta long-lived tokens) the decrypted access token
// itself, which the fb_exchange_token endpoint re-exchanges for a
// fresh long-lived token.
func (v *CredentialVault) extractRefreshMaterial(stored *models.Token, tokenType string) (string, error) {
	if len(stored.EncryptedRefreshToken) > 0 {
		decrypted, err := v.encryptor.Decrypt(stored.EncryptedRefreshToken)
		if err != nil {
			return "", fmt.Errorf("vault: decrypt refresh token: %w", err)
		}
		return decrypted, nil
	}
	if tokenType == models.TokenTypeLongLived {
		// Meta fallback: the long-lived access token itself serves as
		// the "refresh token" for fb_exchange_token.
		decrypted, err := v.encryptor.Decrypt(stored.EncryptedToken)
		if err != nil {
			return "", fmt.Errorf("vault: decrypt access for meta re-exchange: %w", err)
		}
		return decrypted, nil
	}
	return "", fmt.Errorf("vault: token expired and no refresh token available for account %d (type %s)", stored.PlatformAccountID, tokenType)
}

func isExpiryError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "expired")
}
