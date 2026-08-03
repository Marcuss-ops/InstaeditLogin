package repository_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

func newThumbnailProjectMockDB(t *testing.T) (*sql.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}

	t.Cleanup(func() { _ = db.Close() })
	return db, mock
}

func TestThumbnailProjectRepository_CreateIsProviderIndependent(t *testing.T) {
	db, mock := newThumbnailProjectMockDB(t)
	repo := repository.NewThumbnailProjectRepository(db)
	now := time.Now().UTC()
	mock.ExpectExec(`INSERT INTO thumbnail_projects`).
		WithArgs("thumbproj_test", int64(7), int64(11), "Cover", "", 1920, 1080,
			models.ThumbnailProjectStatusDraft, int64(1), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	project := &models.ThumbnailProject{
		ID: "thumbproj_test", WorkspaceID: 7, CreatedBy: 11, Name: " Cover ",
		CanvasWidth: 1920, CanvasHeight: 1080, CreatedAt: now, UpdatedAt: now,
	}
	if err := repo.Create(context.Background(), project); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if project.Status != models.ThumbnailProjectStatusDraft || project.Version != 1 {
		t.Fatalf("defaults not applied: %+v", project)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestThumbnailProjectRepository_FindScopesByWorkspace(t *testing.T) {
	db, mock := newThumbnailProjectMockDB(t)
	repo := repository.NewThumbnailProjectRepository(db)
	now := time.Now().UTC()
	mock.ExpectQuery(`SELECT id, workspace_id, created_by, name, description,`).
		WithArgs(int64(7), "thumbproj_test").
		WillReturnRows(sqlmock.NewRows([]string{"id", "workspace_id", "created_by", "name", "description", "canvas_width", "canvas_height", "status", "current_revision_id", "preview_media_id", "latest_export_id", "version", "created_at", "updated_at"}).
			AddRow("thumbproj_test", 7, 11, "Cover", "", 1920, 1080, "draft", nil, nil, nil, 1, now, now))

	project, err := repo.FindByID(context.Background(), 7, "thumbproj_test")
	if err != nil || project == nil {
		t.Fatalf("FindByID: project=%+v err=%v", project, err)
	}
	if project.WorkspaceID != 7 {
		t.Fatalf("workspace: got %d", project.WorkspaceID)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestThumbnailProjectRepository_UpdateCAS_Conflict(t *testing.T) {
	db, mock := newThumbnailProjectMockDB(t)
	repo := repository.NewThumbnailProjectRepository(db)
	mock.ExpectExec(`UPDATE thumbnail_projects`).
		WithArgs("Cover", "", 1920, 1080, models.ThumbnailProjectStatusDraft, int64(7), "thumbproj_test", int64(3), models.ThumbnailProjectStatusDeleted).
		WillReturnResult(sqlmock.NewResult(0, 0))

	err := repo.UpdateCAS(context.Background(), &models.ThumbnailProject{
		ID: "thumbproj_test", WorkspaceID: 7, CreatedBy: 11, Name: "Cover",
		CanvasWidth: 1920, CanvasHeight: 1080, Status: models.ThumbnailProjectStatusDraft,
	}, 3)
	if !errors.Is(err, repository.ErrThumbnailProjectConflict) {
		t.Fatalf("want ErrThumbnailProjectConflict, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestThumbnailProjectRepository_UpdateStatusCAS_UsesWorkspaceAndVersion(t *testing.T) {
	db, mock := newThumbnailProjectMockDB(t)
	repo := repository.NewThumbnailProjectRepository(db)
	mock.ExpectExec(`UPDATE thumbnail_projects`).
		WithArgs(models.ThumbnailProjectStatusArchived, int64(7), "thumbproj_test", int64(2), models.ThumbnailProjectStatusDeleted).
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := repo.UpdateStatusCAS(context.Background(), 7, "thumbproj_test", models.ThumbnailProjectStatusArchived, 2); err != nil {
		t.Fatalf("UpdateStatusCAS: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
