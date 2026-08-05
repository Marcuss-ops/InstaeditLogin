package repository_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

func newMockUserDB(t *testing.T) (*sql.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatalf("create sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, mock
}

func TestUserRepository_MarkOAuthConnectionAccountsReauthRequired_UsesGrantIDAndPreservesDisconnected(t *testing.T) {
	db, mock := newMockUserDB(t)
	repo := repository.NewUserRepository(db)

	mock.ExpectBegin()
	mock.ExpectExec("SELECT pg_advisory_xact_lock($1)").WithArgs(int64(45)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`UPDATE oauth_connections
	    SET status = 'reauth_required',
	        last_refresh_error = 'invalid_grant',
	        updated_at = NOW()
	  WHERE id = $1`).
		WithArgs(int64(45)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE platform_accounts
	    SET status = 'reauth_required',
	        reauth_required_at = NOW(),
	        last_error_code = $1,
	        last_error_message = $2,
	        updated_at = NOW()
	  WHERE oauth_connection_id = $3
	    AND status <> 'disconnected'`).
		WithArgs("SHARED_GRANT_REAUTH_REQUIRED", "Shared OAuth grant requires reauthorization", int64(45)).
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectCommit()

	if err := repo.MarkOAuthConnectionAccountsReauthRequired(context.Background(), 45, "SHARED_GRANT_REAUTH_REQUIRED", "Shared OAuth grant requires reauthorization"); err != nil {
		t.Fatalf("MarkOAuthConnectionAccountsReauthRequired: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestUserRepository_CountActiveAccountsOnConnection_CountsActiveSiblings(t *testing.T) {
	db, mock := newMockUserDB(t)
	repo := repository.NewUserRepository(db)

	mock.ExpectQuery(`SELECT COUNT(*)
	   FROM platform_accounts
	  WHERE oauth_connection_id = $1
	    AND id <> $2
	    AND status = 'active'`).
		WithArgs(int64(55), int64(21)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	got, err := repo.CountActiveAccountsOnConnection(context.Background(), 55, 21)
	if err != nil {
		t.Fatalf("CountActiveAccountsOnConnection: %v", err)
	}
	if got != 1 {
		t.Errorf("count: want 1, got %d", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestUserRepository_CountActiveAccountsOnConnection_ZeroWhenLastChannel(t *testing.T) {
	db, mock := newMockUserDB(t)
	repo := repository.NewUserRepository(db)

	mock.ExpectQuery(`SELECT COUNT(*)
	   FROM platform_accounts
	  WHERE oauth_connection_id = $1
	    AND id <> $2
	    AND status = 'active'`).
		WithArgs(int64(55), int64(21)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))

	got, err := repo.CountActiveAccountsOnConnection(context.Background(), 55, 21)
	if err != nil {
		t.Fatalf("CountActiveAccountsOnConnection: %v", err)
	}
	if got != 0 {
		t.Errorf("count: want 0, got %d", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestUserRepository_Update_Success(t *testing.T) {
	db, mock := newMockUserDB(t)
	repo := repository.NewUserRepository(db)
	mock.ExpectExec(`UPDATE users SET email = $1, name = $2, updated_at = $3 WHERE id = $4`).WithArgs("new@example.com", "New Name", sqlmock.AnyArg(), int64(42)).WillReturnResult(sqlmock.NewResult(0, 1))
	if err := repo.Update(&models.User{ID: 42, Email: "new@example.com", Name: "New Name"}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestUserRepository_Update_NotFound(t *testing.T) {
	db, mock := newMockUserDB(t)
	repo := repository.NewUserRepository(db)
	mock.ExpectExec(`UPDATE users SET email = $1, name = $2, updated_at = $3 WHERE id = $4`).WithArgs("x@example.com", "X", sqlmock.AnyArg(), int64(999)).WillReturnResult(sqlmock.NewResult(0, 0))
	err := repo.Update(&models.User{ID: 999, Email: "x@example.com", Name: "X"})
	if err == nil || !errors.Is(err, repository.ErrUserNotFound) {
		t.Fatalf("expected ErrUserNotFound, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestUserRepository_Update_ExecErrorPropagates(t *testing.T) {
	db, mock := newMockUserDB(t)
	repo := repository.NewUserRepository(db)
	mock.ExpectExec(`UPDATE users SET email = $1, name = $2, updated_at = $3 WHERE id = $4`).WithArgs("x@example.com", "X", sqlmock.AnyArg(), int64(42)).WillReturnError(errors.New("db down"))
	err := repo.Update(&models.User{ID: 42, Email: "x@example.com", Name: "X"})
	if err == nil || errors.Is(err, repository.ErrUserNotFound) {
		t.Fatalf("expected non-not-found DB error, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}
