package repository_test

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// TestYouTubeVideoEditRepository_CategoryIDRoundTrip pins the extended
// session contract's category_id through the repository read/write
// paths: FindByID must decode the column from the canonical projection
// (youtubeVideoEditSelectColumns) and Update must persist it back on
// the same row ($7), so the by-project GET and the covers hub keep
// serving the value stamped at session creation (migration 127).
//
// The test runs the full cycle FindByID → Update → FindByID with a
// changed value so a future refactor that drops category_id from the
// projection, the UPDATE set-list, or the scan order fails loudly.
func TestYouTubeVideoEditRepository_CategoryIDRoundTrip(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	repo := repository.NewYouTubeVideoEditRepository(db)
	now := time.Now().UTC()

	rowColumns := []string{
		"id", "workspace_id", "platform_account_id", "youtube_video_id",
		"velox_project_id", "source_thumbnail_url", "category_id", "thumbnail_media_id",
		"desired_privacy", "publish_at", "status", "last_error",
		"actual_privacy", "youtube_sync_status",
		"created_at", "updated_at",
	}
	findQuery := `(?s)SELECT id, workspace_id, platform_account_id, youtube_video_id, velox_project_id, source_thumbnail_url, category_id, thumbnail_media_id, desired_privacy, publish_at, status, last_error, actual_privacy, youtube_sync_status, created_at, updated_at FROM youtube_video_edits WHERE id = \$1`

	// Phase 1 — FindByID decodes the seeded category_id.
	mock.ExpectQuery(findQuery).
		WithArgs("ytes_rt_1").
		WillReturnRows(sqlmock.NewRows(rowColumns).AddRow(
			"ytes_rt_1", 7, 42, "fwFGQglE9c0", "ve_rt_1",
			"https://i.ytimg.com/vi/fwFGQglE9c0/hqdefault.jpg", "24", nil,
			"private", nil, "editing", "",
			nil, nil,
			now.Add(-time.Hour), now,
		))

	edit, err := repo.FindByID(context.Background(), "ytes_rt_1")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if edit == nil {
		t.Fatal("FindByID returned nil edit")
	}
	if edit.CategoryID != "24" {
		t.Fatalf("FindByID category_id: want 24, got %q", edit.CategoryID)
	}

	// Phase 2 — Update persists a changed category_id on the same row
	// ($7 in the SET list), never dropped by a partial write.
	edit.CategoryID = "22"
	updatedAt := now.Add(time.Second)
	edit.UpdatedAt = updatedAt
	mock.ExpectExec(`(?s)UPDATE youtube_video_edits SET .*category_id = \$7.*WHERE id = \$1`).
		WithArgs(
			"ytes_rt_1", int64(7), int64(42), "fwFGQglE9c0", "ve_rt_1",
			"https://i.ytimg.com/vi/fwFGQglE9c0/hqdefault.jpg", "22", nil,
			"private", nil, "editing", "",
			updatedAt,
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	if err := repo.Update(context.Background(), edit); err != nil {
		t.Fatalf("Update: %v", err)
	}

	// Phase 3 — FindByID re-reads the updated value back off the row.
	mock.ExpectQuery(findQuery).
		WithArgs("ytes_rt_1").
		WillReturnRows(sqlmock.NewRows(rowColumns).AddRow(
			"ytes_rt_1", 7, 42, "fwFGQglE9c0", "ve_rt_1",
			"https://i.ytimg.com/vi/fwFGQglE9c0/hqdefault.jpg", "22", nil,
			"private", nil, "editing", "",
			nil, nil,
			now.Add(-time.Hour), updatedAt,
		))

	reloaded, err := repo.FindByID(context.Background(), "ytes_rt_1")
	if err != nil {
		t.Fatalf("FindByID (reload): %v", err)
	}
	if reloaded == nil {
		t.Fatal("FindByID (reload) returned nil edit")
	}
	if reloaded.CategoryID != "22" {
		t.Fatalf("reloaded category_id: want 22, got %q", reloaded.CategoryID)
	}
	if !reloaded.UpdatedAt.Equal(updatedAt) {
		t.Fatalf("reloaded updated_at: want %v, got %v", updatedAt, reloaded.UpdatedAt)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
