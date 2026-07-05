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

// SaveToken saves a new encrypted token for a platform account.
func (r *TokenRepository) SaveToken(token *models.Token) error {
	err := r.db.QueryRow(
		`INSERT INTO tokens (platform_account_id, token_type, encrypted_token, expires_at, scopes)
		 VALUES ($1, $2, $3, $4, $5) RETURNING id, created_at`,
		token.PlatformAccountID, token.TokenType, token.EncryptedToken,
		token.ExpiresAt, token.Scopes,
	).Scan(&token.ID, &token.CreatedAt)

	if err != nil {
		return fmt.Errorf("failed to save token: %w", err)
	}
	return nil
}

// FindLatestToken finds the most recent token for a platform account of a given type.
func (r *TokenRepository) FindLatestToken(platformAccountID int64, tokenType string) (*models.Token, error) {
	token := &models.Token{}
	err := r.db.QueryRow(
		`SELECT id, platform_account_id, token_type, encrypted_token, expires_at, scopes, created_at
		 FROM tokens 
		 WHERE platform_account_id = $1 AND token_type = $2
		 ORDER BY created_at DESC LIMIT 1`,
		platformAccountID, tokenType,
	).Scan(&token.ID, &token.PlatformAccountID, &token.TokenType,
		&token.EncryptedToken, &token.ExpiresAt, &token.Scopes, &token.CreatedAt)

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
