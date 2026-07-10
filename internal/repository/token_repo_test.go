package repository_test

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// newMockTokenDB returns a (*sql.DB, sqlmock.Sqlmock) trio wired to a
// single sqlmock controller with strict equality matching. Caller is
// responsible for closing the DB after the test (use t.Cleanup).
func newMockTokenDB(t *testing.T) (*sql.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatalf("create sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, mock
}

// TestTokenRepository_DeleteToken_Success locks in the happy path:
// exactly 1 row deleted → nil error.
func TestTokenRepository_DeleteToken_Success(t *testing.T) {
	db, mock := newMockTokenDB(t)
	repo := repository.NewTokenRepository(db)

	mock.ExpectExec(`DELETE FROM tokens WHERE id = $1`).
		WithArgs(int64(42)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := repo.DeleteToken(42); err != nil {
		t.Fatalf("DeleteToken: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// TestTokenRepository_DeleteToken_NotFound locks in the audit hard-fix:
// rows-affected = 0 must wrap repository.ErrTokenNotFound with id
// context. Used by revoke / disconnect flows that should fail loudly
// on stale ids.
func TestTokenRepository_DeleteToken_NotFound(t *testing.T) {
	db, mock := newMockTokenDB(t)
	repo := repository.NewTokenRepository(db)

	mock.ExpectExec(`DELETE FROM tokens WHERE id = $1`).
		WithArgs(int64(999)).
		WillReturnResult(sqlmock.NewResult(0, 0))

	err := repo.DeleteToken(999)
	if err == nil {
		t.Fatal("expected sentinel error, got nil")
	}
	if !errors.Is(err, repository.ErrTokenNotFound) {
		t.Errorf("error must wrap repository.ErrTokenNotFound, got: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// TestTokenRepository_DeleteAllTokensForPlatformAccount_Success covers
// the bulk-delete happy path: 3 rows for the platform_account → nil.
// Different from the singleton path because RowsAffected can be any
// positive integer here.
func TestTokenRepository_DeleteAllTokensForPlatformAccount_Success(t *testing.T) {
	db, mock := newMockTokenDB(t)
	repo := repository.NewTokenRepository(db)

	mock.ExpectExec(`DELETE FROM tokens WHERE platform_account_id = $1`).
		WithArgs(int64(7)).
		WillReturnResult(sqlmock.NewResult(0, 3))

	if err := repo.DeleteAllTokensForPlatformAccount(7); err != nil {
		t.Fatalf("DeleteAllTokensForPlatformAccount: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// TestTokenRepository_DeleteAllTokensForPlatformAccount_NotFound locks
// in the audit contract: zero rows match → ErrTokenNotFound wrapped
// with platform_account_id. Callers (logout / revoke) should treat
// this as a non-fatal idempotent no-op via errors.Is.
func TestTokenRepository_DeleteAllTokensForPlatformAccount_NotFound(t *testing.T) {
	db, mock := newMockTokenDB(t)
	repo := repository.NewTokenRepository(db)

	mock.ExpectExec(`DELETE FROM tokens WHERE platform_account_id = $1`).
		WithArgs(int64(999)).
		WillReturnResult(sqlmock.NewResult(0, 0))

	err := repo.DeleteAllTokensForPlatformAccount(999)
	if err == nil {
		t.Fatal("expected sentinel error, got nil")
	}
	if !errors.Is(err, repository.ErrTokenNotFound) {
		t.Errorf("error must wrap repository.ErrTokenNotFound, got: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}
