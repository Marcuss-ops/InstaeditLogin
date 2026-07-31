// Package credentials provides PostgreSQL-backed OAuth credential fixtures for
// integration and E2E tests. The helpers intentionally target the current
// production lineage: platform_accounts -> oauth_connections -> tokens.
package credentials

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/lib/pq"

	"github.com/Marcuss-ops/InstaeditLogin/internal/crypto"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

const fixtureKeyBase64 = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="

var fixtureScopes = []string{
	"https://www.googleapis.com/auth/youtube.upload",
	"https://www.googleapis.com/auth/youtube.readonly",
	"https://www.googleapis.com/auth/youtube.force-ssl",
}

// CredentialFixture identifies the production OAuth lineage and token row
// created by SeedRefreshableBearerToken.
type CredentialFixture struct {
	OAuthConnectionID int64
	TokenID           int64
}

// ResolveOAuthConnectionID returns the OAuth grant lineage for a platform
// account, creating and linking the canonical oauth_connections row when the
// account has no lineage yet. The operation is transactional and fail-fast:
// schema or foreign-key drift is reported through the test immediately.
func ResolveOAuthConnectionID(tb testing.TB, db *sql.DB, platformAccountID int64) int64 {
	tb.Helper()
	if db == nil {
		tb.Fatal("credential fixture: nil database")
	}

	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		tb.Fatalf("credential fixture: begin OAuth lineage transaction: %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	connectionID := resolveOAuthConnectionIDTx(tb, tx, platformAccountID)
	if err := tx.Commit(); err != nil {
		tb.Fatalf("credential fixture: commit OAuth lineage %d for account %d: %v", connectionID, platformAccountID, err)
	}
	return connectionID
}

func resolveOAuthConnectionIDTx(tb testing.TB, tx *sql.Tx, platformAccountID int64) int64 {
	tb.Helper()
	ctx := context.Background()
	var (
		userID          int64
		platform        string
		platformUserID  string
		oauthConnection sql.NullInt64
	)
	if err := tx.QueryRowContext(ctx, `
		SELECT user_id, platform, platform_user_id, oauth_connection_id
		  FROM platform_accounts
		 WHERE id = $1
	 FOR UPDATE`, platformAccountID).Scan(&userID, &platform, &platformUserID, &oauthConnection); err != nil {
		tb.Fatalf("credential fixture: resolve platform account %d: %v", platformAccountID, err)
	}
	if oauthConnection.Valid {
		return oauthConnection.Int64
	}

	var connectionID int64
	err := tx.QueryRowContext(ctx, `
		INSERT INTO oauth_connections (user_id, provider, provider_resource_id)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (user_id, provider, provider_resource_id) DO NOTHING
		 RETURNING id`, userID, platform, platformUserID).Scan(&connectionID)
	if err == sql.ErrNoRows {
		err = tx.QueryRowContext(ctx, `
			SELECT id
			  FROM oauth_connections
			 WHERE user_id = $1 AND provider = $2 AND provider_resource_id = $3`,
			userID, platform, platformUserID).Scan(&connectionID)
	}
	if err != nil {
		tb.Fatalf("credential fixture: create OAuth lineage for account %d: %v", platformAccountID, err)
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE platform_accounts
		   SET oauth_connection_id = $1, updated_at = NOW()
		 WHERE id = $2`, connectionID, platformAccountID); err != nil {
		tb.Fatalf("credential fixture: link OAuth lineage %d to account %d: %v", connectionID, platformAccountID, err)
	}
	return connectionID
}

// NewFixtureEncryptor returns the deterministic AES-256 encryptor used by
// SeedRefreshableBearerToken. Tests that wire a real CredentialVault can use
// this same encryptor to decrypt the fixture without duplicating the key.
func NewFixtureEncryptor() (*crypto.Encryptor, error) {
	return crypto.NewEncryptor(1, map[uint32]string{1: fixtureKeyBase64})
}

// SeedRefreshableBearerToken inserts a decryptable, future-expiring YouTube
// bearer token into the production tokens table. Both ciphertext columns are
// encrypted with a deterministic test key; no plaintext token is persisted.
// It first resolves the platform account's oauth_connection_id, so the
// fixture exercises the same lineage required by CredentialVault.Renew.
func SeedRefreshableBearerToken(tb testing.TB, db *sql.DB, platformAccountID int64) CredentialFixture {
	tb.Helper()
	if db == nil {
		tb.Fatal("credential fixture: nil database")
	}
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		tb.Fatalf("credential fixture: begin bearer token fixture transaction: %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	connectionID := resolveOAuthConnectionIDTx(tb, tx, platformAccountID)

	encryptor, err := NewFixtureEncryptor()
	if err != nil {
		tb.Fatalf("credential fixture: create deterministic encryptor: %v", err)
	}
	accessCiphertext, err := encryptor.Encrypt("fixture-access-token")
	if err != nil {
		tb.Fatalf("credential fixture: encrypt access token: %v", err)
	}
	refreshCiphertext, err := encryptor.Encrypt("fixture-refresh-token")
	if err != nil {
		tb.Fatalf("credential fixture: encrypt refresh token: %v", err)
	}

	var (
		tokenID   int64
		createdAt time.Time
	)
	if err := tx.QueryRowContext(context.Background(), `
		INSERT INTO tokens
		    (platform_account_id, oauth_connection_id, token_type,
		     encrypted_token, encrypted_refresh_token, expires_at, scopes)
		 VALUES ($1, $2, $3, $4, $5, NOW() + INTERVAL '5 minutes', $6)
		 RETURNING id, created_at`,
		platformAccountID,
		connectionID,
		models.TokenTypeBearer,
		accessCiphertext,
		refreshCiphertext,
		pq.Array(fixtureScopes),
	).Scan(&tokenID, &createdAt); err != nil {
		tb.Fatalf("credential fixture: seed bearer token for account %d: %v", platformAccountID, err)
	}
	if err := tx.Commit(); err != nil {
		tb.Fatalf("credential fixture: commit bearer token fixture for account %d: %v", platformAccountID, err)
	}

	return CredentialFixture{OAuthConnectionID: connectionID, TokenID: tokenID}
}

// CountTokensForAccount returns the number of production tokens attached to a
// platform account through its OAuth lineage. Missing lineage is a valid zero
// result; query/schema errors are fatal because silently skipping them masks
// credential fixture drift.
func CountTokensForAccount(tb testing.TB, db *sql.DB, platformAccountID int64) int {
	tb.Helper()
	if db == nil {
		tb.Fatal("credential fixture: nil database")
	}
	var count int
	if err := db.QueryRow(`
		SELECT COUNT(*)
		  FROM tokens
		 WHERE platform_account_id = $1`, platformAccountID).Scan(&count); err != nil {
		tb.Fatalf("credential fixture: count tokens for account %d: %v", platformAccountID, err)
	}
	return count
}
