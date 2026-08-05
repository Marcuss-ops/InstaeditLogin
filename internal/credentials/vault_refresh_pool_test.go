package credentials

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// TestVault_Renew_StampsOAuthClientKey_PoolB pins the R4 chain
// platform_account_id → oauth_connection_id → oauth_client_key: the
// Renew slow path resolves the grant's pool client key inside the lock
// tx and stamps it on the ctx handed to the refresher, so the refresher
// can Resolve(key) and refresh with the EXACT client that issued the
// token. A pool B grant must reach the refresher as youtube_pool_b.
func TestVault_Renew_StampsOAuthClientKey_PoolB(t *testing.T) {
	v, mock, store := newTestVault(t)
	const accountID int64 = 610
	expired := newEncryptedToken(t, v, accountID, -1*time.Minute, "pool-b-refresh")
	store.seedToken(expired)

	expectOauthConnLookup(mock, accountID, accountID)
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT oauth_connection_id FROM platform_accounts WHERE id = $1 AND oauth_connection_id IS NOT NULL FOR UPDATE`).
		WithArgs(accountID).
		WillReturnRows(sqlmock.NewRows([]string{"oauth_connection_id"}).AddRow(accountID))
	mock.ExpectExec("SELECT pg_advisory_xact_lock($1)").
		WithArgs(accountID).
		WillReturnResult(sqlmock.NewResult(0, 0))
	// The grant's oauth_client_key resolves to pool B.
	expectOAuthClientKeyLookup(mock, accountID, "youtube_pool_b")
	mock.ExpectCommit()
	expectOauthConnLookup(mock, accountID, accountID)

	var stampedKey string
	_, err := v.Renew(context.Background(), accountID, models.TokenTypeBearer, func(ctx context.Context, refreshToken string) (*models.TokenData, error) {
		stampedKey = OAuthClientKeyFromContext(ctx)
		return &models.TokenData{AccessToken: "fresh", TokenType: models.TokenTypeBearer, ExpiresIn: 3600}, nil
	})
	if err != nil {
		t.Fatalf("Renew: %v", err)
	}
	if stampedKey != "youtube_pool_b" {
		t.Errorf("refresher ctx oauth_client_key: want youtube_pool_b, got %q", stampedKey)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("sqlmock expectations: %v", err)
	}
}

// TestVault_Renew_StampsOAuthClientKey_PoolA pins the same chain for a
// pool A grant: a pool A token must NEVER reach the refresher labelled
// as pool B (cross-pool refresh would surface as invalid_client from
// Google). Certifies the per-grant key is taken from the connection,
// not guessed or alternated.
func TestVault_Renew_StampsOAuthClientKey_PoolA(t *testing.T) {
	v, mock, store := newTestVault(t)
	const accountID int64 = 611
	expired := newEncryptedToken(t, v, accountID, -1*time.Minute, "pool-a-refresh")
	store.seedToken(expired)

	expectOauthConnLookup(mock, accountID, accountID)
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT oauth_connection_id FROM platform_accounts WHERE id = $1 AND oauth_connection_id IS NOT NULL FOR UPDATE`).
		WithArgs(accountID).
		WillReturnRows(sqlmock.NewRows([]string{"oauth_connection_id"}).AddRow(accountID))
	mock.ExpectExec("SELECT pg_advisory_xact_lock($1)").
		WithArgs(accountID).
		WillReturnResult(sqlmock.NewResult(0, 0))
	expectOAuthClientKeyLookup(mock, accountID, "youtube_pool_a")
	mock.ExpectCommit()
	expectOauthConnLookup(mock, accountID, accountID)

	var stampedKey string
	_, err := v.Renew(context.Background(), accountID, models.TokenTypeBearer, func(ctx context.Context, refreshToken string) (*models.TokenData, error) {
		stampedKey = OAuthClientKeyFromContext(ctx)
		return &models.TokenData{AccessToken: "fresh", TokenType: models.TokenTypeBearer, ExpiresIn: 3600}, nil
	})
	if err != nil {
		t.Fatalf("Renew: %v", err)
	}
	if stampedKey != "youtube_pool_a" {
		t.Errorf("refresher ctx oauth_client_key: want youtube_pool_a, got %q", stampedKey)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("sqlmock expectations: %v", err)
	}
}

// TestVault_Renew_OAuthClientKeyLookupFailure_FallsBackToLegacy pins the
// pre-migration-099 resilience: when the oauth_client_key column does
// not exist, resolveOAuthClientKey falls back to the legacy label
// (youtube_pool_a) at DEBUG and the refresh proceeds — a missing column
// must NEVER fail a refresh.
func TestVault_Renew_OAuthClientKeyLookupFailure_FallsBackToLegacy(t *testing.T) {
	v, mock, store := newTestVault(t)
	const accountID int64 = 612
	expired := newEncryptedToken(t, v, accountID, -1*time.Minute, "legacy-refresh")
	store.seedToken(expired)

	expectOauthConnLookup(mock, accountID, accountID)
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT oauth_connection_id FROM platform_accounts WHERE id = $1 AND oauth_connection_id IS NOT NULL FOR UPDATE`).
		WithArgs(accountID).
		WillReturnRows(sqlmock.NewRows([]string{"oauth_connection_id"}).AddRow(accountID))
	mock.ExpectExec("SELECT pg_advisory_xact_lock($1)").
		WithArgs(accountID).
		WillReturnResult(sqlmock.NewResult(0, 0))
	// Column missing → query errors → fallback to youtube_pool_a.
	mock.ExpectQuery(`SELECT oc.oauth_client_key
		   FROM oauth_connections oc
		  WHERE oc.id = $1
		    AND oc.provider = 'youtube'`).
		WithArgs(accountID).
		WillReturnError(sqlmock.ErrCancelled)
	mock.ExpectCommit()
	expectOauthConnLookup(mock, accountID, accountID)

	var stampedKey string
	_, err := v.Renew(context.Background(), accountID, models.TokenTypeBearer, func(ctx context.Context, refreshToken string) (*models.TokenData, error) {
		stampedKey = OAuthClientKeyFromContext(ctx)
		return &models.TokenData{AccessToken: "fresh", TokenType: models.TokenTypeBearer, ExpiresIn: 3600}, nil
	})
	if err != nil {
		t.Fatalf("Renew must succeed even when oauth_client_key resolution fails (pre-099 DB): %v", err)
	}
	if stampedKey != "youtube_pool_a" {
		t.Errorf("fallback oauth_client_key: want youtube_pool_a, got %q", stampedKey)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("sqlmock expectations: %v", err)
	}
}
