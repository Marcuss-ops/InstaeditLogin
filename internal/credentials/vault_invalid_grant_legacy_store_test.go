package credentials

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

func TestVault_Renew_InvalidGrant_LegacyStoreFailsClosed(t *testing.T) {
	v, mock, store := newTestVault(t)
	const accountID int64 = 504
	store.seedToken(newEncryptedToken(t, v, accountID, -time.Minute, "refresh"))

	expectOauthConnLookup(mock, accountID, accountID)
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT oauth_connection_id FROM platform_accounts WHERE id = $1 AND oauth_connection_id IS NOT NULL FOR UPDATE`).WithArgs(accountID).WillReturnRows(sqlmock.NewRows([]string{"oauth_connection_id"}).AddRow(accountID))
	mock.ExpectExec("SELECT pg_advisory_xact_lock($1)").WithArgs(accountID).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()

	_, err := v.Renew(context.Background(), accountID, models.TokenTypeBearer, func(context.Context, string) (*models.TokenData, error) {
		return nil, fmt.Errorf("provider wrapper: %w", ErrInvalidGrant)
	})
	if err == nil || !errors.Is(err, ErrInvalidGrant) {
		t.Fatalf("expected typed invalid_grant on fail-closed path, got %v", err)
	}
	if !strings.Contains(err.Error(), "propagation unavailable") {
		t.Fatalf("expected fail-closed propagation error, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sqlmock expectations: %v", err)
	}
}
