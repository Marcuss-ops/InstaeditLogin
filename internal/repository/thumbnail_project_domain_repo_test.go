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

func TestThumbnailProjectRepository_CreateAssetValidatesBeforeSQL(t *testing.T) {
	db, mock := newThumbnailProjectMockDB(t)
	repo := repository.NewThumbnailProjectRepository(db)
	err := repo.CreateAsset(context.Background(), 7, &models.ThumbnailProjectAsset{
		ProjectID: "project-1", MediaID: "bad", Role: models.ThumbnailProjectAssetRoleLogo,
	})
	if !errors.Is(err, repository.ErrThumbnailProjectInvalid) {
		t.Fatalf("want invalid asset error, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestThumbnailProjectRepository_CreateExportValidatesBeforeSQL(t *testing.T) {
	db, mock := newThumbnailProjectMockDB(t)
	repo := repository.NewThumbnailProjectRepository(db)
	err := repo.CreateExport(context.Background(), 7, &models.ThumbnailExport{
		ProjectID: "project-1", RevisionID: "revision-1", MediaID: "bad",
		ContentType: models.ThumbnailProjectExportContentTypePNG,
		Width:       1920, Height: 1080, SHA256: make([]byte, 32), RendererVersion: "renderer-1",
	})
	if !errors.Is(err, repository.ErrThumbnailProjectInvalid) {
		t.Fatalf("want invalid export error, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestThumbnailProjectRepository_CreateAssignmentValidatesBeforeSQL(t *testing.T) {
	db, mock := newThumbnailProjectMockDB(t)
	repo := repository.NewThumbnailProjectRepository(db)
	err := repo.CreateAssignment(context.Background(), &models.ThumbnailAssignment{
		WorkspaceID: 7, ProjectID: "project-1", ExportID: "export-1", PlatformAccountID: 4,
		Platform: "tiktok", YouTubeVideoID: "video-1",
	})
	if !errors.Is(err, repository.ErrThumbnailProjectInvalid) {
		t.Fatalf("want invalid assignment error, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestThumbnailProjectRepository_UpdateExportStatusValidatesWorkspace(t *testing.T) {
	db, mock := newThumbnailProjectMockDB(t)
	repo := repository.NewThumbnailProjectRepository(db)
	err := repo.UpdateExportStatus(context.Background(), 0, "export-1", models.ThumbnailProjectExportStatusReady, "", make([]byte, 32), 10, "renderer-1")
	if !errors.Is(err, repository.ErrThumbnailProjectInvalid) {
		t.Fatalf("want invalid workspace error, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestThumbnailProjectRepository_UpdateExportStatusValidatesFailedError(t *testing.T) {
	db, mock := newThumbnailProjectMockDB(t)
	repo := repository.NewThumbnailProjectRepository(db)
	err := repo.UpdateExportStatus(context.Background(), 7, "export-1", " failed ", " ", nil, 0, " renderer-1 ")
	if !errors.Is(err, repository.ErrThumbnailProjectInvalid) {
		t.Fatalf("want invalid failed status error, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestThumbnailProjectRepository_FindExportScopesByWorkspace(t *testing.T) {
	db, mock := newThumbnailProjectMockDB(t)
	repo := repository.NewThumbnailProjectRepository(db)
	mock.ExpectQuery(`SELECT id, project_id, revision_id, media_id`).
		WithArgs(int64(7), "export-1", models.ThumbnailProjectStatusDeleted).
		WillReturnRows(sqlmock.NewRows([]string{"id", "project_id", "revision_id", "media_id", "content_type", "width", "height", "file_size", "sha256", "renderer_version", "status", "last_error", "created_at"}).
			AddRow("export-1", "project-1", "revision-1", "00000000-0000-0000-0000-000000000001", "image/png", 1920, 1080, 10, make([]byte, 32), "renderer-1", "ready", "", time.Now().UTC()))
	got, err := repo.FindExport(context.Background(), 7, "export-1")
	if err != nil || got == nil {
		t.Fatalf("FindExport: got=%+v err=%v", got, err)
	}
	if got.ProjectID != "project-1" {
		t.Fatalf("project id: %s", got.ProjectID)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
