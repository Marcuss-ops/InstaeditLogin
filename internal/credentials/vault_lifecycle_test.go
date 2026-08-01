package credentials

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// statusTrackingTokenStore adds only the non-transactional grant-status
// interface to the existing vault test store. Keeping it out of
// mockTokenStore preserves the SQL expectations of older Renew tests that
// intentionally do not exercise grant-status persistence.
type statusTrackingTokenStore struct {
	*mockTokenStore
	statusCalls int
	oauthID     int64
	status      string
	lastError   string
	statusRepo  *repository.TokenRepository
}

func (s *statusTrackingTokenStore) UpdateOAuthConnectionStatus(ctx context.Context, oauthConnectionID int64, status, lastError string) error {
	s.statusCalls++
	s.oauthID = oauthConnectionID
	s.status = status
	s.lastError = lastError
	if s.statusRepo != nil {
		return s.statusRepo.UpdateOAuthConnectionStatus(ctx, oauthConnectionID, status, lastError)
	}
	return nil
}

var _ GrantStatusStore = (*statusTrackingTokenStore)(nil)

// TestVault_Lifecycle_InvalidGrantMarksGrantReauthRequired verifies the
// complete vault-side invalid_grant contract: the refresh is attempted once,
// the lock transaction rolls back, and only the redacted application error
// classification is persisted on the OAuth connection.
func TestVault_Lifecycle_InvalidGrantMarksGrantReauthRequired(t *testing.T) {
	v, mock, base := newTestVault(t)
	store := &statusTrackingTokenStore{
		mockTokenStore: base,
		statusRepo:     repository.NewTokenRepository(v.db),
	}
	v.store = store
	const accountID int64 = 502
	expired := newEncryptedToken(t, v, accountID, -time.Minute, "refresh-before-revocation")
	base.seedToken(expired)

	expectOauthConnLookup(mock, accountID, accountID)
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT oauth_connection_id FROM platform_accounts WHERE id = $1 AND oauth_connection_id IS NOT NULL FOR UPDATE`).
		WithArgs(accountID).
		WillReturnRows(sqlmock.NewRows([]string{"oauth_connection_id"}).AddRow(accountID))
	mock.ExpectExec("SELECT pg_advisory_xact_lock($1)").
		WithArgs(accountID).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()
	mock.ExpectExec(`UPDATE oauth_connections
	    SET status = $2::text,
	        last_refresh_error = NULLIF($3::text, ''),
	        last_refresh_at = CASE WHEN $2::text = 'active' THEN NOW() ELSE last_refresh_at END,
	        updated_at = NOW()
	  WHERE id = $1`).
		WithArgs(accountID, models.AccountStatusReauthRequired, "invalid_grant").
		WillReturnResult(sqlmock.NewResult(0, 1))

	providerErr := errors.New("google token endpoint: invalid_grant (provider detail must not be persisted)")
	_, err := v.Renew(context.Background(), accountID, models.TokenTypeBearer,
		func(context.Context, string) (*models.TokenData, error) {
			return nil, providerErr
		})
	if err == nil || !errors.Is(err, providerErr) {
		t.Fatalf("Renew error: want wrapped invalid_grant, got %v", err)
	}
	if store.statusCalls != 1 {
		t.Fatalf("grant status calls: want 1, got %d", store.statusCalls)
	}
	if store.oauthID != accountID || store.status != models.AccountStatusReauthRequired || store.lastError != "invalid_grant" {
		t.Fatalf("grant status: want (%d, %q, invalid_grant), got (%d, %q, %q)",
			accountID, models.AccountStatusReauthRequired, store.oauthID, store.status, store.lastError)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sqlmock expectations: %v", err)
	}
}

// TestVault_Lifecycle_RevokeSharedConnectionIsolation verifies that revoking
// one OAuth grant deletes its single shared token row while leaving another
// connection untouched. Two platform accounts can point at connection 700;
// the vault must resolve and delete by oauth_connection_id, never by an
// account-id prefix or by deleting every token for the user.
func TestVault_Lifecycle_RevokeSharedConnectionIsolation(t *testing.T) {
	v, mock, store := newTestVault(t)
	const (
		channelA         int64 = 601
		channelB         int64 = 602
		sharedConnection int64 = 700
		otherConnection  int64 = 701
	)
	store.accountToConn = map[int64]int64{
		channelA: sharedConnection,
		channelB: sharedConnection,
	}
	if store.accountToConn[channelA] != sharedConnection || store.accountToConn[channelB] != sharedConnection {
		t.Fatal("both YouTube channels must resolve to the same oauth_connection_id")
	}
	store.seedToken(&models.Token{
		OAuthConnectionID:     sharedConnection,
		TokenType:             models.TokenTypeBearer,
		EncryptedToken:        []byte("shared-access-ciphertext"),
		EncryptedRefreshToken: []byte("shared-refresh-ciphertext"),
	})
	store.seedToken(&models.Token{
		OAuthConnectionID: otherConnection,
		TokenType:         models.TokenTypeBearer,
		EncryptedToken:    []byte("other-access-ciphertext"),
	})

	expectOauthConnLookup(mock, channelA, sharedConnection)
	if err := v.Revoke(context.Background(), channelA); err != nil {
		t.Fatalf("revoke shared grant: %v", err)
	}
	if got, err := store.FindLatestToken(sharedConnection, models.TokenTypeBearer); err != nil {
		t.Fatalf("read revoked shared grant: %v", err)
	} else if got != nil {
		t.Fatal("revoking a shared OAuth connection must remove its token")
	}
	if got, err := store.FindLatestToken(otherConnection, models.TokenTypeBearer); err != nil {
		t.Fatalf("read unrelated grant: %v", err)
	} else if got == nil {
		t.Fatal("revoking one OAuth connection must not remove another connection")
	}
	// Channel B still resolves to the same grant lineage after channel A's
	// revoke; the shared grant has been removed for both channels, while the
	// unrelated connection remains intact.
	if got := store.accountToConn[channelB]; got != sharedConnection {
		t.Fatalf("channel B oauth_connection_id after revoke: want %d, got %d", sharedConnection, got)
	}
	if got, err := store.FindLatestToken(sharedConnection, models.TokenTypeBearer); err != nil {
		t.Fatalf("read shared grant through channel B lineage: %v", err)
	} else if got != nil {
		t.Fatal("channel B must observe the revoked shared grant as deleted")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sqlmock expectations: %v", err)
	}
}
