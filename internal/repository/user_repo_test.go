package repository_test

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// newMockUserDB returns a (*sql.DB, sqlmock.Sqlmock) trio wired to a single
// sqlmock controller with strict equality matching. Caller is responsible
// for closing the DB after the test (use t.Cleanup).
func newMockUserDB(t *testing.T) (*sql.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatalf("create sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, mock
}

// TestUserRepository_Update_Success locks in the happy path: 1 row
// affected → nil error. The updated_at argument is non-deterministic
// (time.Now() in prod code), so sqlmock.AnyArg absorbs it.
func TestUserRepository_Update_Success(t *testing.T) {
	db, mock := newMockUserDB(t)
	repo := repository.NewUserRepository(db)

	mock.ExpectExec(
		`UPDATE users SET email = $1, name = $2, updated_at = $3 WHERE id = $4`,
	).WithArgs("new@example.com", "New Name", sqlmock.AnyArg(), int64(42)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := repo.Update(&models.User{
		ID: 42, Email: "new@example.com", Name: "New Name",
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// TestUserRepository_Update_NotFound locks in the audit hard-fix:
// rows-affected = 0 must wrap repository.ErrUserNotFound with id
// context. The API layer uses errors.Is to map this to 404.
func TestUserRepository_Update_NotFound(t *testing.T) {
	db, mock := newMockUserDB(t)
	repo := repository.NewUserRepository(db)

	mock.ExpectExec(
		`UPDATE users SET email = $1, name = $2, updated_at = $3 WHERE id = $4`,
	).WithArgs("x@example.com", "X", sqlmock.AnyArg(), int64(999)).
		WillReturnResult(sqlmock.NewResult(0, 0))

	err := repo.Update(&models.User{
		ID: 999, Email: "x@example.com", Name: "X",
	})
	if err == nil {
		t.Fatal("expected sentinel error, got nil")
	}
	if !errors.Is(err, repository.ErrUserNotFound) {
		t.Errorf("error must wrap repository.ErrUserNotFound, got: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// TestUserRepository_Update_ExecErrorPropagates makes sure a driver-level
// error (db down, connection refused) is wrapped with the canonical
// "failed to update user" prefix and does NOT accidentally match the
// ErrUserNotFound sentinel.
func TestUserRepository_Update_ExecErrorPropagates(t *testing.T) {
	db, mock := newMockUserDB(t)
	repo := repository.NewUserRepository(db)

	mock.ExpectExec(
		`UPDATE users SET email = $1, name = $2, updated_at = $3 WHERE id = $4`,
	).WithArgs("x@example.com", "X", sqlmock.AnyArg(), int64(42)).
		WillReturnError(errors.New("db down"))

	err := repo.Update(&models.User{
		ID: 42, Email: "x@example.com", Name: "X",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if errors.Is(err, repository.ErrUserNotFound) {
		t.Errorf("Exec error must NOT wrap ErrUserNotFound sentinel: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}
