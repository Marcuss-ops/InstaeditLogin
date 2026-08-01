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
// oauth_connection_id so concurrent refreshes serialise at the DB level.
package credentials

import (
	"context"
	"database/sql"
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
// DeleteAll-for-connection and the tx-aware save primitive, not the per-id
// delete used by admin tooling.
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
	// SaveTokenTx writes the supplied token row inside the caller's
	// open *sql.Tx. Credentials.TokenStore implementations run the
	// INSERT + DELETE pruner against the supplied tx so a caller
	// rollback drops both the new row AND the pruned older rows
	// together — preserving the original SaveToken contract under
	// atomic ownership. Callers MUST commit or roll back the tx
	// themselves; the store does not open or close it. Task 1/10
	// uses this primitive to roll the encrypted-token write inside
	// ChannelAuthorizationService.AuthorizeChannel's atomic flow.
	SaveTokenTx(ctx context.Context, tx *sql.Tx, token *models.Token) error
}

// GrantStatusStore is implemented by stores that persist OAuth-grant health.
// It is optional so in-memory/test stores and older integrations remain
// source-compatible while migration 083 rolls out.
type GrantStatusStore interface {
	UpdateOAuthConnectionStatus(ctx context.Context, oauthConnectionID int64, status, lastError string) error
}

// GrantStatusTxStore persists grant health in a caller-owned transaction.
// Production uses this during Renew so the refreshed token and active/error
// state commit or roll back together; older test stores may omit it.
type GrantStatusTxStore interface {
	UpdateOAuthConnectionStatusTx(ctx context.Context, tx *sql.Tx, oauthConnectionID int64, status, lastError string) error
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
	// clock is the "now" dependency. Production wires time.Now; tests
	// inject a fakeClock via SetClock so the TTL math (8-day refresh
	// rule, 60-second staleness grace) is deterministic. The field is
	// package-private on purpose: callers in cmd/server should never
	// need to swap it.
	clock func() time.Time
}

// NewCredentialVault constructs a vault. All three dependencies are
// required; a nil in any slot will surface as a panic on the first
// method call (fail-fast for misconfigured main.go).
func NewCredentialVault(encryptor *crypto.Encryptor, db *sql.DB, store TokenStore) *CredentialVault {
	return &CredentialVault{encryptor: encryptor, db: db, store: store, clock: time.Now}
}

// SetClock overrides the vault's "now" source. Intended for tests that
// need to drive the TTL math deterministically (e.g. fakeClock that
// "advances" 8 days for the refresh-token Production-vs-Testing tests).
// Production callers should leave the default time.Now alone.
func (v *CredentialVault) SetClock(clock func() time.Time) {
	if clock == nil {
		v.clock = time.Now
		return
	}
	v.clock = clock
}
