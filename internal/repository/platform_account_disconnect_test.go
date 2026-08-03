package repository_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

func expectDisconnectTransaction(mock sqlmock.Sqlmock, accountID, oauthConnectionID int64, activeSiblings int64) {
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT oauth_connection_id
	   FROM platform_accounts
	  WHERE id = $1
	  FOR UPDATE`).
		WithArgs(accountID).
		WillReturnRows(sqlmock.NewRows([]string{"oauth_connection_id"}).AddRow(oauthConnectionID))
	mock.ExpectExec("SELECT pg_advisory_xact_lock($1)").
		WithArgs(oauthConnectionID).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`UPDATE platform_accounts
	    SET status = 'disconnected',
	        connected_at = NULL,
	        last_error_code = 'DISCONNECTED',
	        last_error_message = 'account disconnected by user',
	        updated_at = NOW()
	  WHERE id = $1`).
		WithArgs(accountID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT COUNT(*)
		   FROM platform_accounts
		  WHERE oauth_connection_id = $1
		    AND status = 'active'`).
		WithArgs(oauthConnectionID).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(activeSiblings))
	mock.ExpectCommit()
}

func TestUserRepository_DisconnectPlatformAccount_PreservesGrantForActiveSibling(t *testing.T) {
	db, mock := newMockUserDB(t)
	repo := repository.NewUserRepository(db)
	expectDisconnectTransaction(mock, 21, 55, 1)

	lastOnGrant, handled, err := repo.DisconnectPlatformAccount(context.Background(), 21)
	if err != nil {
		t.Fatalf("DisconnectPlatformAccount: %v", err)
	}
	if !handled {
		t.Fatal("DisconnectPlatformAccount must report the operation as handled")
	}
	if lastOnGrant {
		t.Fatal("grant must be preserved while an active sibling remains")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestUserRepository_DisconnectPlatformAccount_AllowsRevokeForLastChannel(t *testing.T) {
	db, mock := newMockUserDB(t)
	repo := repository.NewUserRepository(db)
	expectDisconnectTransaction(mock, 21, 55, 0)

	lastOnGrant, handled, err := repo.DisconnectPlatformAccount(context.Background(), 21)
	if err != nil {
		t.Fatalf("DisconnectPlatformAccount: %v", err)
	}
	if !handled || !lastOnGrant {
		t.Fatalf("last channel result: handled=%v lastOnGrant=%v, want true/true", handled, lastOnGrant)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestUserRepository_DisconnectPlatformAccount_LegacyAccountSkipsGrantLock(t *testing.T) {
	db, mock := newMockUserDB(t)
	repo := repository.NewUserRepository(db)
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT oauth_connection_id
	   FROM platform_accounts
	  WHERE id = $1
	  FOR UPDATE`).
		WithArgs(int64(21)).
		WillReturnRows(sqlmock.NewRows([]string{"oauth_connection_id"}).AddRow(sql.NullInt64{}))
	mock.ExpectExec(`UPDATE platform_accounts
	    SET status = 'disconnected',
	        connected_at = NULL,
	        last_error_code = 'DISCONNECTED',
	        last_error_message = 'account disconnected by user',
	        updated_at = NOW()
	  WHERE id = $1`).
		WithArgs(int64(21)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	lastOnGrant, handled, err := repo.DisconnectPlatformAccount(context.Background(), 21)
	if err != nil {
		t.Fatalf("DisconnectPlatformAccount: %v", err)
	}
	if !handled || lastOnGrant {
		t.Fatalf("legacy result: handled=%v lastOnGrant=%v, want true/false", handled, lastOnGrant)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}
