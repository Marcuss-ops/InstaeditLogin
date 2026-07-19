package repository

import (
	"database/sql"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

func TestCreateIfSourceAbsentCreatesCanonicalJob(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	repo := NewUploadJobRepository(db)
	driveAccountID := int64(77)
	scheduledAt := time.Date(2026, 7, 18, 10, 0, 0, 0, time.UTC)
	createdAt := time.Date(2026, 7, 18, 9, 0, 0, 0, time.UTC)
	updatedAt := createdAt
	job := &models.UploadJob{
		UserID:         3,
		WorkspaceID:    9,
		SourceType:     models.UploadJobSourceAuthenticatedDrive,
		SourceID:       "drive-file-123",
		DriveAccountID: &driveAccountID,
		Title:          "Title",
		Caption:        "Caption",
		Targets:        []int64{8, 4, 8},
		Status:         models.UploadJobStatusPending,
		PublishAt:    &scheduledAt,
	}

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`SELECT pg_advisory_xact_lock(hashtext($1)::bigint)`)).
		WithArgs(sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT EXISTS`).
		WithArgs(job.UserID, string(job.SourceType), job.SourceID, job.DriveAccountID, []byte(`[4,8]`)).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO upload_jobs`)).
		WithArgs(
			job.UserID,
			job.WorkspaceID,
			string(job.SourceType),
			job.SourceID,
			job.DriveAccountID,
			sql.NullString{},
			job.Title,
			job.Caption,
			[]byte(`[4,8]`),
			string(job.Status),
			sql.NullTime{Time: scheduledAt, Valid: true},
		).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).AddRow(55, createdAt, updatedAt))
	mock.ExpectCommit()

	created, err := repo.CreateIfSourceAbsent(job)
	if err != nil {
		t.Fatalf("CreateIfSourceAbsent: %v", err)
	}
	if !created {
		t.Fatal("expected job to be created")
	}
	if job.ID != 55 {
		t.Fatalf("expected ID 55, got %d", job.ID)
	}
	if len(job.Targets) != 2 || job.Targets[0] != 4 || job.Targets[1] != 8 {
		t.Fatalf("targets were not canonicalized: %#v", job.Targets)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestCreateIfSourceAbsentSkipsExistingSource(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	repo := NewUploadJobRepository(db)
	job := &models.UploadJob{
		UserID:      3,
		WorkspaceID: 9,
		SourceType:  models.UploadJobSourceAuthenticatedDrive,
		SourceID:    "drive-file-123",
		Targets:     []int64{4},
		Status:      models.UploadJobStatusPending,
	}

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`SELECT pg_advisory_xact_lock(hashtext($1)::bigint)`)).
		WithArgs(sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT EXISTS`).
		WithArgs(job.UserID, string(job.SourceType), job.SourceID, job.DriveAccountID, []byte(`[4]`)).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectCommit()

	created, err := repo.CreateIfSourceAbsent(job)
	if err != nil {
		t.Fatalf("CreateIfSourceAbsent: %v", err)
	}
	if created {
		t.Fatal("expected duplicate source to be skipped")
	}
	if job.ID != 0 {
		t.Fatalf("skipped job should not receive an ID, got %d", job.ID)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestSuppressPendingDuplicates(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	repo := NewUploadJobRepository(db)
	mock.ExpectExec(`WITH ranked AS`).
		WithArgs(int64(3), duplicateUploadJobMessage).
		WillReturnResult(sqlmock.NewResult(0, 2))

	count, err := repo.SuppressPendingDuplicates(3)
	if err != nil {
		t.Fatalf("SuppressPendingDuplicates: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected 2 suppressed jobs, got %d", count)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
