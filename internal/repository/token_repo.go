package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

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

const insertTokenSQL = `INSERT INTO tokens (
                platform_account_id, oauth_connection_id, token_type,
                encrypted_access_token, encrypted_token, encrypted_refresh_token,
                access_token_expires_at, expires_at, refresh_token_expires_at)
         VALUES (NULLIF($1::BIGINT, 0), $2::BIGINT, $3::VARCHAR, $4::BYTEA, $4::BYTEA,
                 COALESCE($5::BYTEA, (SELECT encrypted_refresh_token FROM tokens WHERE oauth_connection_id = $2::BIGINT AND token_type = $3::VARCHAR ORDER BY created_at DESC LIMIT 1)),
                 $6::TIMESTAMPTZ, $6::TIMESTAMPTZ,
                 COALESCE($7::TIMESTAMPTZ, (SELECT refresh_token_expires_at FROM tokens WHERE oauth_connection_id = $2::BIGINT AND token_type = $3::VARCHAR ORDER BY created_at DESC LIMIT 1)))
         ON CONFLICT (oauth_connection_id, token_type) DO UPDATE SET
                 platform_account_id = EXCLUDED.platform_account_id,
                 encrypted_access_token = EXCLUDED.encrypted_access_token,
                 encrypted_token = EXCLUDED.encrypted_token,
                 encrypted_refresh_token = COALESCE(EXCLUDED.encrypted_refresh_token, tokens.encrypted_refresh_token),
                 access_token_expires_at = EXCLUDED.access_token_expires_at,
                 expires_at = EXCLUDED.expires_at,
                 refresh_token_expires_at = COALESCE(EXCLUDED.refresh_token_expires_at, tokens.refresh_token_expires_at)
         RETURNING id, created_at`

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

	err = tx.QueryRow(insertTokenSQL,
		token.PlatformAccountID, token.OAuthConnectionID, token.TokenType,
		accessCiphertext(token), nullableCiphertext(token.EncryptedRefreshToken),
		accessExpiresAt(token), token.RefreshTokenExpiresAt,
	).Scan(&token.ID, &token.CreatedAt)
	if err != nil {
		return fmt.Errorf("failed to save token: %w", err)
	}
	if err = updateGrantScopesTx(tx, token.OAuthConnectionID, grantedScopes(token)); err != nil {
		return err
	}
	if _, err = tx.Exec(
		`DELETE FROM tokens WHERE oauth_connection_id = $1 AND token_type = $2 AND id <> $3`,
		token.OAuthConnectionID, token.TokenType, token.ID,
	); err != nil {
		return fmt.Errorf("failed to prune older tokens: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit save tx: %w", err)
	}
	return nil
}

// SaveTokenTx writes and prunes a token inside a caller-owned transaction.
func (r *TokenRepository) SaveTokenTx(ctx context.Context, tx *sql.Tx, token *models.Token) error {
	if tx == nil {
		return fmt.Errorf("save token (tx): nil tx")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, insertTokenSQL,
		token.PlatformAccountID, token.OAuthConnectionID, token.TokenType,
		accessCiphertext(token), nullableCiphertext(token.EncryptedRefreshToken),
		accessExpiresAt(token), token.RefreshTokenExpiresAt,
	).Scan(&token.ID, &token.CreatedAt); err != nil {
		return fmt.Errorf("failed to save token (tx): %w", err)
	}
	if err := updateGrantScopesTxContext(ctx, tx, token.OAuthConnectionID, grantedScopes(token)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM tokens WHERE oauth_connection_id = $1 AND token_type = $2 AND id <> $3`,
		token.OAuthConnectionID, token.TokenType, token.ID,
	); err != nil {
		return fmt.Errorf("failed to prune older tokens (tx): %w", err)
	}
	return nil
}

// UpdateOAuthConnectionStatus records grant-level refresh health. lastError is
// an application classification, never a provider response or token value.
const updateOAuthConnectionStatusSQL = `UPDATE oauth_connections
	    SET status = $2::text,
	        last_refresh_error = NULLIF($3::text, ''),
	        last_refresh_at = CASE WHEN $2::text = 'active' THEN NOW() ELSE last_refresh_at END,
	        updated_at = NOW()
	  WHERE id = $1`

type contextExecutor interface {
	ExecContext(context.Context, string, ...interface{}) (sql.Result, error)
}

func updateOAuthConnectionStatusExec(ctx context.Context, exec contextExecutor, oauthConnectionID int64, status, lastError string) error {
	result, err := exec.ExecContext(ctx, updateOAuthConnectionStatusSQL,
		oauthConnectionID, status, lastError,
	)
	if err != nil {
		return fmt.Errorf("update OAuth connection status: %w", err)
	}
	if n, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("read OAuth connection status rows affected: %w", err)
	} else if n == 0 {
		return fmt.Errorf("OAuth connection %d not found", oauthConnectionID)
	}
	return nil
}

func (r *TokenRepository) UpdateOAuthConnectionStatus(ctx context.Context, oauthConnectionID int64, status, lastError string) error {
	return updateOAuthConnectionStatusExec(ctx, r.db, oauthConnectionID, status, lastError)
}

// UpdateOAuthConnectionStatusTx records grant health in a caller-owned
// transaction so token persistence and grant state share one commit boundary.
func (r *TokenRepository) UpdateOAuthConnectionStatusTx(ctx context.Context, tx *sql.Tx, oauthConnectionID int64, status, lastError string) error {
	if tx == nil {
		return fmt.Errorf("update OAuth connection status: nil tx")
	}
	return updateOAuthConnectionStatusExec(ctx, tx, oauthConnectionID, status, lastError)
}

// FindLatestToken reads canonical columns and normalizes legacy aliases for
// callers that still use the pre-083 model fields.
func (r *TokenRepository) FindLatestToken(oauthConnectionID int64, tokenType string) (*models.Token, error) {
	token := &models.Token{}
	var platformAccountID sql.NullInt64
	err := r.db.QueryRow(
		`SELECT t.id, t.oauth_connection_id, t.platform_account_id, t.token_type,
		        t.encrypted_access_token, t.encrypted_token, t.encrypted_refresh_token,
		        t.access_token_expires_at, t.expires_at, t.refresh_token_expires_at,
		        COALESCE(NULLIF(oc.granted_scopes, '{}'::TEXT[]), oc.scopes), t.created_at
		   FROM tokens t
		   LEFT JOIN oauth_connections oc ON oc.id = t.oauth_connection_id
		  WHERE t.oauth_connection_id = $1 AND t.token_type = $2
		  ORDER BY t.created_at DESC LIMIT 1`,
		oauthConnectionID, tokenType,
	).Scan(&token.ID, &token.OAuthConnectionID, &platformAccountID, &token.TokenType,
		&token.EncryptedAccessToken, &token.EncryptedToken, &token.EncryptedRefreshToken,
		&token.AccessTokenExpiresAt, &token.ExpiresAt, &token.RefreshTokenExpiresAt,
		pq.Array(&token.GrantedScopes), &token.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to find latest token: %w", err)
	}
	if platformAccountID.Valid {
		token.PlatformAccountID = platformAccountID.Int64
	}
	normalizeTokenAliases(token)
	return token, nil
}

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

func (r *TokenRepository) DeleteAllTokensForOAuthConnection(oauthConnectionID int64) error {
	result, err := r.db.Exec(`DELETE FROM tokens WHERE oauth_connection_id = $1`, oauthConnectionID)
	if err != nil {
		return fmt.Errorf("failed to delete tokens for oauth connection: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to read rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: oauth_connection_id=%d", ErrTokenNotFound, oauthConnectionID)
	}
	return nil
}

func (r *TokenRepository) UpdateCiphertexts(tokenID int64, oldEncrypted, newEncrypted []byte) error {
	result, err := r.db.Exec(
		`UPDATE tokens
		    SET encrypted_access_token = $1, encrypted_token = $1
		  WHERE id = $2
		    AND COALESCE(encrypted_access_token, encrypted_token) = $3`,
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
		return fmt.Errorf("ciphertext stale: another re-encrypt already applied (id=%d)", tokenID)
	}
	return nil
}

func updateGrantScopesTx(tx *sql.Tx, oauthConnectionID int64, scopes []string) error {
	if len(scopes) == 0 {
		return nil
	}
	if _, err := tx.Exec(
		`UPDATE oauth_connections SET granted_scopes = $2, scopes = $2, updated_at = NOW() WHERE id = $1`,
		oauthConnectionID, pq.Array(scopes),
	); err != nil {
		return fmt.Errorf("failed to update OAuth grant scopes: %w", err)
	}
	return nil
}

func updateGrantScopesTxContext(ctx context.Context, tx *sql.Tx, oauthConnectionID int64, scopes []string) error {
	if len(scopes) == 0 {
		return nil
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE oauth_connections SET granted_scopes = $2, scopes = $2, updated_at = NOW() WHERE id = $1`,
		oauthConnectionID, pq.Array(scopes),
	); err != nil {
		return fmt.Errorf("failed to update OAuth grant scopes (tx): %w", err)
	}
	return nil
}

func accessCiphertext(token *models.Token) []byte {
	if len(token.EncryptedAccessToken) > 0 {
		return token.EncryptedAccessToken
	}
	return token.EncryptedToken
}

func nullableCiphertext(ciphertext []byte) []byte {
	if len(ciphertext) == 0 {
		return nil
	}
	return ciphertext
}

func accessExpiresAt(token *models.Token) *time.Time {
	if token.AccessTokenExpiresAt != nil {
		return token.AccessTokenExpiresAt
	}
	return token.ExpiresAt
}

func grantedScopes(token *models.Token) []string {
	if len(token.GrantedScopes) > 0 {
		return token.GrantedScopes
	}
	return token.Scopes
}

func normalizeTokenAliases(token *models.Token) {
	if len(token.EncryptedAccessToken) == 0 {
		token.EncryptedAccessToken = token.EncryptedToken
	}
	if len(token.EncryptedToken) == 0 {
		token.EncryptedToken = token.EncryptedAccessToken
	}
	if token.AccessTokenExpiresAt == nil {
		token.AccessTokenExpiresAt = token.ExpiresAt
	}
	if token.ExpiresAt == nil {
		token.ExpiresAt = token.AccessTokenExpiresAt
	}
	if len(token.Scopes) == 0 {
		token.Scopes = append([]string(nil), token.GrantedScopes...)
	}
	if len(token.GrantedScopes) == 0 {
		token.GrantedScopes = append([]string(nil), token.Scopes...)
	}
}
