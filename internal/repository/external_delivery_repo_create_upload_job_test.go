package repository

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// fixtureUploadJob returns a minimal upload job usable by the
// CreateUploadJobAndLink tests. The repository fills in defaults
// (e.g. TargetsJSON) and the SQL expectations only inspect the
// executed statements, not the exact bind values.
func fixtureUploadJob() *models.UploadJob {
	return &models.UploadJob{
		UserID:      42,
		WorkspaceID: 7,
		SourceType:  models.UploadJobSourceVeloxArtifact,
		SourceID:    "https://velox.example/artifact",
		Status:      models.UploadJobStatusPending,
		Title:       "title",
		Caption:     "caption",
	}
}

// TestExternalDeliveryRepository_CreateUploadJobAndLink_HappyPath
// verifies the atomic transaction commits when the upload_job INSERT
// succeeds and the external_deliveries claim UPDATE matches exactly
// one accepted, unlinked row.
func TestExternalDeliveryRepository_CreateUploadJobAndLink_HappyPath(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	now := time.Now()
	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO upload_jobs`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).
			AddRow(123, now, now))
	mock.ExpectExec(`UPDATE external_deliveries`).
		WithArgs("sdel_01JDEF", int64(123), "worker-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	repo := NewExternalDeliveryRepository(db)
	jobID, err := repo.CreateUploadJobAndLink(context.Background(), fixtureUploadJob(), "sdel_01JDEF", "worker-1")
	if err != nil {
		t.Fatalf("CreateUploadJobAndLink: want nil, got %v", err)
	}
	if jobID != 123 {
		t.Errorf("CreateUploadJobAndLink: jobID = %d; want 123", jobID)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("mock expectations: %v", err)
	}
}

// TestExternalDeliveryRepository_CreateUploadJobAndLink_AlreadyClaimed
// verifies the second worker loses the race: the upload_job INSERT
// returns an row, but the claim UPDATE affects 0 rows, so the
// method rolls back and returns ErrExternalDeliveryAlreadyClaimed.
func TestExternalDeliveryRepository_CreateUploadJobAndLink_AlreadyClaimed(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	now := time.Now()
	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO upload_jobs`).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).
			AddRow(124, now, now))
	mock.ExpectExec(`UPDATE external_deliveries`).
		WithArgs("sdel_01JCLAIMED", int64(124), "worker-2").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()

	repo := NewExternalDeliveryRepository(db)
	jobID, err := repo.CreateUploadJobAndLink(context.Background(), fixtureUploadJob(), "sdel_01JCLAIMED", "worker-2")
	if err == nil {
		t.Fatalf("CreateUploadJobAndLink: want ErrExternalDeliveryAlreadyClaimed, got nil (jobID=%d)", jobID)
	}
	if !errors.Is(err, ErrExternalDeliveryAlreadyClaimed) {
		t.Errorf("CreateUploadJobAndLink: want ErrExternalDeliveryAlreadyClaimed, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("mock expectations: %v", err)
	}
}

// TestExternalDeliveryRepository_CreateUploadJobAndLink_InsertFails
// verifies that a failure during the upload_job INSERT rolls back the
// transaction and surfaces the error, leaving external_deliveries
// untouched (no UPDATE is attempted).
func TestExternalDeliveryRepository_CreateUploadJobAndLink_InsertFails(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO upload_jobs`).
		WillReturnError(sql.ErrConnDone)
	mock.ExpectRollback()

	repo := NewExternalDeliveryRepository(db)
	jobID, err := repo.CreateUploadJobAndLink(context.Background(), fixtureUploadJob(), "sdel_01JINSERTFAIL", "worker-3")
	if err == nil {
		t.Fatalf("CreateUploadJobAndLink: want error, got nil (jobID=%d)", jobID)
	}
	if errors.Is(err, ErrExternalDeliveryAlreadyClaimed) {
		t.Errorf("CreateUploadJobAndLink: insert failure should not map to ErrExternalDeliveryAlreadyClaimed")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("mock expectations: %v", err)
	}
}
