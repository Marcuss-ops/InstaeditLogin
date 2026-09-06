package repository_test

import (
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// TestPostRepository_MarkDriveRequiredFailed_RecomputesAggregate covers the
// Task 8/10.1 writeback success path: CAS on status='published' succeeds and
// the parent post aggregate is recomputed inside the same transaction (the
// parent must NOT keep reading 'published' after a child flips to the
// terminal drive_required_failed policy state).
func TestPostRepository_MarkDriveRequiredFailed_RecomputesAggregate(t *testing.T) {
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
	mock.ExpectExec("UPDATE post_targets\n SET status = $1::text::post_status,\n     error_message = $2,\n     last_error_code = 'DRIVE_REQUIRED',\n     completed_at = NOW()\n WHERE id = $3\n   AND status = 'published'::post_status").
		WithArgs(models.PostStatusDriveRequiredFailed, "drive_required policy violated: required Drive upload terminally failed", int64(200)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	// Aggregate recompute: the single target is now drive_required_failed
	// (a terminal failure for the resolver) → parent flips to 'failed'.
	mock.ExpectQuery("SELECT status FROM post_targets WHERE post_id = $1 ORDER BY id ASC").
		WithArgs(int64(100)).
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("drive_required_failed"))
	mock.ExpectExec("UPDATE posts SET status = $1 WHERE id = $2").
		WithArgs(models.PostStatusFailed, int64(100)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err = repository.NewPostRepository(db).MarkDriveRequiredFailed(200, "drive_required policy violated: required Drive upload terminally failed")
	if err != nil {
		t.Fatalf("MarkDriveRequiredFailed: expected success, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sqlmock expectations: %v", err)
	}
}

// TestPostRepository_MarkDriveRequiredFailed_CasLossIsStale covers the CAS
// loss path: the row moved past 'published' before the writeback (operator
// correction, manual retry). The state must not regress and the outcome is
// ErrPostTargetTransitionStale — an expected race the caller logs, not an
// alarm.
func TestPostRepository_MarkDriveRequiredFailed_CasLossIsStale(t *testing.T) {
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
	mock.ExpectExec("UPDATE post_targets\n SET status = $1::text::post_status,\n     error_message = $2,\n     last_error_code = 'DRIVE_REQUIRED',\n     completed_at = NOW()\n WHERE id = $3\n   AND status = 'published'::post_status").
		WithArgs(models.PostStatusDriveRequiredFailed, "drive_required policy violated", int64(200)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT status FROM post_targets WHERE id = $1").
		WithArgs(int64(200)).
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow("partially_published"))
	mock.ExpectRollback()

	err = repository.NewPostRepository(db).MarkDriveRequiredFailed(200, "drive_required policy violated")
	if err == nil {
		t.Fatal("MarkDriveRequiredFailed: expected CAS-loss error, got nil")
	}
	if !errors.Is(err, repository.ErrPostTargetTransitionStale) {
		t.Fatalf("MarkDriveRequiredFailed: expected ErrPostTargetTransitionStale, got %v", err)
	}
	if errors.Is(err, repository.ErrPostTargetNotFound) {
		t.Fatalf("MarkDriveRequiredFailed: CAS loss must not be reported as not found, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sqlmock expectations: %v", err)
	}
}
