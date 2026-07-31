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
	mock.ExpectQuery("SELECT post_id FROM post_targets WHERE id = $1").
		WithArgs(int64(200)).
		WillReturnRows(sqlmock.NewRows([]string{"post_id"}).AddRow(int64(100)))
	mock.ExpectQuery("SELECT id FROM post_targets WHERE id = $1 FOR UPDATE").
		WithArgs(int64(200)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(200)))
	mock.ExpectQuery("SELECT id FROM posts WHERE id = $1 FOR UPDATE").
		WithArgs(int64(100)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(100)))
	mock.ExpectExec("UPDATE post_targets SET status = 'queued', error_message = '' WHERE id = $1 AND status = 'failed'").
		WithArgs(int64(200)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT status FROM post_targets WHERE id = $1").
		WithArgs(int64(200)).
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("dead_letter"))
	mock.ExpectRollback()

	err = repository.NewPostRepository(db).RetryTarget(200)
	if err == nil {
		t.Fatal("RetryTarget: expected terminal target rejection, got nil")
	}
	if !errors.Is(err, repository.ErrPostTargetTransitionStale) {
		t.Fatalf("RetryTarget: expected ErrPostTargetTransitionStale, got %v", err)
	}
	if errors.Is(err, repository.ErrPostTargetNotFound) {
		t.Fatalf("RetryTarget: terminal target must not be reported as not found, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sqlmock expectations: %v", err)
	}
}
