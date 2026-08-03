package repository_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

func TestThumbnailProjectRepository_SaveSnapshot_DeduplicatesWithoutAdvancingVersion(t *testing.T) {
	db, mock := newThumbnailProjectMockDB(t)
	repo := repository.NewThumbnailProjectRepository(db)
	now := time.Now().UTC()
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT version, status`).WithArgs(int64(7), "thumbproj_test").
		WillReturnRows(sqlmock.NewRows([]string{"version", "status"}).AddRow(2, "draft"))
	mock.ExpectQuery(`SELECT id, project_id, revision_number`).WithArgs("thumbproj_test", sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id", "project_id", "revision_number", "schema_version", "snapshot_json", "snapshot_sha256", "renderer_version", "created_by", "created_at"}).
			AddRow("thumbrev_existing", "thumbproj_test", 1, 1, []byte(`{"canvas":{},"objects":[]}`), make([]byte, 32), "renderer-1", 11, now))
	mock.ExpectCommit()

	result, err := repo.SaveSnapshot(context.Background(), 7, "thumbproj_test", models.ThumbnailProjectSnapshot{
		SchemaVersion: 1, SnapshotJSON: []byte(`{ "objects": [], "canvas": {} }`), RendererVersion: "renderer-1", BaseVersion: 2,
	}, 11)
	if err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}
	if !result.Deduplicated || result.Version != 2 || result.RevisionID != "thumbrev_existing" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestThumbnailProjectRepository_SaveSnapshot_ConflictRollsBack(t *testing.T) {
	db, mock := newThumbnailProjectMockDB(t)
	repo := repository.NewThumbnailProjectRepository(db)
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT version, status`).WithArgs(int64(7), "thumbproj_test").
		WillReturnRows(sqlmock.NewRows([]string{"version", "status"}).AddRow(4, "draft"))
	mock.ExpectRollback()

	_, err := repo.SaveSnapshot(context.Background(), 7, "thumbproj_test", models.ThumbnailProjectSnapshot{
		SchemaVersion: 1, SnapshotJSON: []byte(`{"canvas":{},"objects":[]}`), RendererVersion: "renderer-1", BaseVersion: 3,
	}, 11)
	if !errors.Is(err, repository.ErrThumbnailProjectConflict) {
		t.Fatalf("want conflict, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestThumbnailProjectRepository_RestoreRevision_CreatesNewRevision(t *testing.T) {
	db, mock := newThumbnailProjectMockDB(t)
	repo := repository.NewThumbnailProjectRepository(db)
	now := time.Now().UTC()
	hash := make([]byte, 32)
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT version, status`).WithArgs(int64(7), "thumbproj_test").
		WillReturnRows(sqlmock.NewRows([]string{"version", "status"}).AddRow(2, "draft"))
	mock.ExpectQuery(`SELECT id, project_id, revision_number`).WithArgs("thumbproj_test", "thumbrev_old").
		WillReturnRows(sqlmock.NewRows([]string{"id", "project_id", "revision_number", "schema_version", "snapshot_json", "snapshot_sha256", "renderer_version", "created_by", "created_at"}).
			AddRow("thumbrev_old", "thumbproj_test", 1, 1, []byte(`{"canvas":{},"objects":[]}`), hash, "renderer-1", 11, now))
	mock.ExpectQuery(`SELECT COALESCE\(MAX\(revision_number\), 0\)`).WithArgs("thumbproj_test").
		WillReturnRows(sqlmock.NewRows([]string{"coalesce"}).AddRow(2))
	mock.ExpectExec(`INSERT INTO thumbnail_project_revisions`).
		WithArgs(sqlmock.AnyArg(), "thumbproj_test", int64(2), 1, sqlmock.AnyArg(), hash, "renderer-2", int64(11), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE thumbnail_projects`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), int64(7), "thumbproj_test", int64(2)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	result, err := repo.RestoreRevision(context.Background(), 7, "thumbproj_test", "thumbrev_old", 2, 11, "renderer-2")
	if err != nil {
		t.Fatalf("RestoreRevision: %v", err)
	}
	if result.RevisionID == "thumbrev_old" || result.RevisionNumber != 2 || result.Version != 3 || result.SnapshotSHA256 == "" {
		t.Fatalf("unexpected restore result: %+v", result)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
