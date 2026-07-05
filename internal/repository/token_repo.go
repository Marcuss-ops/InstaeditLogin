package repository

import (
	"database/sql"
	"fmt"

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
		token.EncryptedRefreshToken, token.ExpiresAt, token.Scopes,
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
		&token.EncryptedToken, &token.EncryptedRefreshToken, &token.ExpiresAt, &token.Scopes, &token.CreatedAt)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to find latest token: %w", err)
	}
	return token, nil
}

// DeleteToken deletes a token by ID.
func (r *TokenRepository) DeleteToken(tokenID int64) error {
	_, err := r.db.Exec(`DELETE FROM tokens WHERE id = $1`, tokenID)
	if err != nil {
		return fmt.Errorf("failed to delete token: %w", err)
	}
	return nil
}

// DeleteAllTokensForPlatformAccount removes all tokens for a given platform account.
func (r *TokenRepository) DeleteAllTokensForPlatformAccount(platformAccountID int64) error {
	_, err := r.db.Exec(`DELETE FROM tokens WHERE platform_account_id = $1`, platformAccountID)
	if err != nil {
		return fmt.Errorf("failed to delete tokens for platform account: %w", err)
	}
	return nil
}
