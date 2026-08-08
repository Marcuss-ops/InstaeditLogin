package repository_test

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

func TestYouTubeVideoEditRepository_ListCoversByGroupAccounts(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	repo := repository.NewYouTubeVideoEditRepository(db)
	now := time.Now().UTC()
	media := "00000000-0000-0000-0000-000000000042"
	title := "Cover per video test"

	mock.ExpectQuery(`SELECT tp\.id, tp\.workspace_id`).
		WithArgs(int64(7), models.ThumbnailProjectStatusDeleted, "{42,43}").
		WillReturnRows(sqlmock.NewRows([]string{
			"tp.id", "tp.workspace_id", "tp.name", "tp.status",
			"tp.preview_media_id", "tp.latest_export_id", "tp.version",
			"tp.created_at", "tp.updated_at",
			"yve.id", "yve.platform_account_id", "yve.youtube_video_id",
			"yve.velox_project_id", "yve.thumbnail_media_id",
			"yve.source_thumbnail_url", "yve.status", "yve.draft_title",
			"yve.created_at", "yve.updated_at",
		}).
			AddRow("ytes_cover_1", 7, "YouTube cover", "ready",
				media, nil, 2, now.Add(-time.Hour), now,
				"ytes_cover_1", 42, "fwFGQglE9c0", "ve_cover_1", nil,
				"", "editing", title,
				now.Add(-time.Hour), now).
			AddRow("ytes_cover_2", 7, "YouTube cover", "archived",
				nil, nil, 5, now.Add(-2*time.Hour), now.Add(-time.Hour),
				"ytes_cover_2", 43, "PlradxPxWy0", "ve_cover_2", nil,
				"", "published", nil,
				now.Add(-2*time.Hour), now.Add(-time.Hour)))

	covers, err := repo.ListCoversByGroupAccounts(context.Background(), 7, []int64{42, 43})
	if err != nil {
		t.Fatalf("ListCoversByGroupAccounts: %v", err)
	}
	if len(covers) != 2 {
		t.Fatalf("covers: want 2, got %d", len(covers))
	}
	first := covers[0]
	if first.ProjectID != "ytes_cover_1" || first.VeloxProjectID != "ve_cover_1" {
		t.Errorf("first cover identity wrong: %+v", first)
	}
	if first.ProjectStatus != models.ThumbnailProjectStatusReady {
		t.Errorf("first status: want ready, got %q", first.ProjectStatus)
	}
	if first.PreviewMediaID == nil || *first.PreviewMediaID != media {
		t.Errorf("first preview_media_id: want %q, got %v", media, first.PreviewMediaID)
	}
	if first.DraftTitle == nil || *first.DraftTitle != title {
		t.Errorf("first draft_title: want %q, got %v", title, first.DraftTitle)
	}
	second := covers[1]
	if second.ProjectStatus != models.ThumbnailProjectStatusArchived || second.EditStatus != "published" {
		t.Errorf("second status: want archived/published, got %q/%q", second.ProjectStatus, second.EditStatus)
	}
	if second.DraftTitle != nil {
		t.Errorf("second draft_title: want nil, got %v", second.DraftTitle)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestYouTubeVideoEditRepository_ListCoversByGroupAccounts_EmptyInputs(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	repo := repository.NewYouTubeVideoEditRepository(db)
	covers, err := repo.ListCoversByGroupAccounts(context.Background(), 0, []int64{42})
	if err != nil || covers != nil {
		t.Fatalf("zero workspace: want (nil, nil), got covers=%v err=%v", covers, err)
	}
	covers, err = repo.ListCoversByGroupAccounts(context.Background(), 7, nil)
	if err != nil || covers != nil {
		t.Fatalf("empty accounts: want (nil, nil), got covers=%v err=%v", covers, err)
	}
}
