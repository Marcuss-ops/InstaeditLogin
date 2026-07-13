package repository

import (
	"database/sql"
	"fmt"

	"github.com/lib/pq"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// TokenRepository handles CRUD operations for encrypted tokens.
type TokenRepository struct {
	db *sql.DB
}

// NewTokenRepository creates a new TokenRepository.
func NewTokenRepository(db *sql.DB) *TokenRepository {
	return &TokenRepository{db: db}
}

// SaveToken saves a new encrypted token for a platform account and prunes
// older rows for the same (platform_account_id, token_type) so the table does
// not grow unbounded across refreshes. Encrypted refresh tokens are stored
// alongside access tokens when present (PostgreSQL treats nil []byte as NULL bytea).
func (r *TokenRepository) SaveToken(token *models.Token) error {
	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin save tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	err = tx.QueryRow(
		`INSERT INTO tokens (platform_account_id, token_type, encrypted_token, encrypted_refresh_token, expires_at, scopes)
		 VALUES ($1, $2, $3, $4, $5, $6) RETURNING id, created_at`,
		token.PlatformAccountID, token.TokenType, token.EncryptedToken,
		token.EncryptedRefreshToken, token.ExpiresAt, pq.Array(token.Scopes),
	).Scan(&token.ID, &token.CreatedAt)
	if err != nil {
		return fmt.Errorf("failed to save token: %w", err)
	}

	if _, err = tx.Exec(
		`DELETE FROM tokens WHERE platform_account_id = $1 AND token_type = $2 AND id <> $3`,
		token.PlatformAccountID, token.TokenType, token.ID,
	); err != nil {
		return fmt.Errorf("failed to prune older tokens: %w", err)
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit save tx: %w", err)
	}
	return nil
}

// FindLatestToken finds the most recent token for a platform account of a given type.
// The encrypted refresh token is included (may be nil if the platform did not issue one).
func (r *TokenRepository) FindLatestToken(platformAccountID int64, tokenType string) (*models.Token, error) {
	token := &models.Token{}
	err := r.db.QueryRow(
		`SELECT id, platform_account_id, token_type, encrypted_token, encrypted_refresh_token, expires_at, scopes, created_at
		 FROM tokens
		 WHERE platform_account_id = $1 AND token_type = $2
		 ORDER BY created_at DESC LIMIT 1`,
		platformAccountID, tokenType,
	).Scan(&token.ID, &token.PlatformAccountID, &token.TokenType,
		&token.EncryptedToken, &token.EncryptedRefreshToken, &token.ExpiresAt, pq.Array(&token.Scopes), &token.CreatedAt)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to find latest token: %w", err)
	}
	return token, nil
}

// DeleteToken deletes a single token by ID. Returns ErrTokenNotFound
// (wrapped with id context) when no row matches — the API layer can
// map to 404 via errors.Is. Used by revoke / disconnect flows that
// should fail loudly on stale ids.
func (r *TokenRepository) DeleteToken(tokenID int64) error {
	result, err := r.db.Exec(`DELETE FROM tokens WHERE id = $1`, tokenID)
	if err != nil {
		return fmt.Errorf("failed to delete token: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to read rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: id=%d", ErrTokenNotFound, tokenID)
	}
	return nil
}

// DeleteAllTokensForPlatformAccount removes all tokens for a given
// platform account. Returns ErrTokenNotFound (wrapped with
// platform_account_id context) when zero rows match — this is the
// legitimate "account has no tokens" idempotent case, e.g. on user
// logout. Callers in revoke/disconnect flows should use
// errors.Is(err, ErrTokenNotFound) to treat this as non-fatal.
func (r *TokenRepository) DeleteAllTokensForPlatformAccount(platformAccountID int64) error {
	result, err := r.db.Exec(`DELETE FROM tokens WHERE platform_account_id = $1`, platformAccountID)
	if err != nil {
		return fmt.Errorf("failed to delete tokens for platform account: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to read rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: platform_account_id=%d", ErrTokenNotFound, platformAccountID)
	}
	return nil
}

// UpdateCiphertexts atomically replaces the encrypted_token column
// for a single token row, with optimistic-concurrency guarding: the
// UPDATE only fires if the row's current encrypted_token still
// matches oldEncrypted. This is the lazy re-encrypt primitive the
// vault uses on the Get() path when a row is stamped with a
// non-active key id (or a legacy pre-Sprint-5.3 ciphertext).
//
// Concurrency contract: two workers reading the same stale row
// race here. Worker A wins the UPDATE (its oldEncrypted matches),
// row is now stamped with the active key. Worker B's UPDATE
// affects 0 rows (its oldEncrypted no longer matches the new
// state) and the method returns a "ciphertext stale" error.
// The vault logs and ignores that error — the row was already
// upgraded by A, so B's work is redundant.
//
// Returning the error is also a debugging signal: a non-zero rate
// of "ciphertext stale" errors means many concurrent re-encrypts
// are racing, which suggests a hot key (or a bug in the rotation
// flow). Operators should see the log line and know what to look
// at.
func (r *TokenRepository) UpdateCiphertexts(tokenID int64, oldEncrypted, newEncrypted []byte) error {
	result, err := r.db.Exec(
		`UPDATE tokens SET encrypted_token = $1 WHERE id = $2 AND encrypted_token = $3`,
		newEncrypted, tokenID, oldEncrypted,
	)
	if err != nil {
		return fmt.Errorf("failed to update ciphertext: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to read rows affected: %w", err)
	}
	if n == 0 {
		// Either the row was deleted (rare, possible) or another
		// worker already upgraded the ciphertext. Both are
		// non-fatal for the vault's Get() caller, which swallows
		// this specific error.
		return fmt.Errorf("ciphertext stale: another re-encrypt already applied (id=%d)", tokenID)
	}
	return nil
}
