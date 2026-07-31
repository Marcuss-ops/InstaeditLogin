package repository_test

import (
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

func TestPostRepository_RetryTarget_RejectsTerminalTarget(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE post_targets SET status = 'queued', error_message = '' WHERE id = $1 AND status = 'failed'").
		WithArgs(int64(200)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()

	err = repository.NewPostRepository(db).RetryTarget(200)
	if err == nil {
		t.Fatal("RetryTarget: expected terminal target rejection, got nil")
	}
	if !errors.Is(err, repository.ErrPostTargetNotFound) {
		t.Fatalf("RetryTarget: expected ErrPostTargetNotFound, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sqlmock expectations: %v", err)
	}
}
