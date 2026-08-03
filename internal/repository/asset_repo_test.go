package repository_test

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

func TestMediaAssetRepository_ListVisibleInWorkspaceScopesByMembership(t *testing.T) {
	db, mock := newThumbnailProjectMockDB(t)
	repo := repository.NewMediaAssetRepository(db)
	ids := []string{"00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000002"}
	now := time.Now().UTC()
	mock.ExpectQuery(`SELECT id, user_id, upload_key`).
		WithArgs(int64(7), pq.Array(ids), string(models.MediaAssetStatusReady)).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "user_id", "upload_key", "bucket", "content_type", "size_bytes", "status",
			"sha256", "error_message", "expires_at", "created_at", "updated_at",
			"duration_seconds", "width", "height", "fps", "has_audio", "video_codec", "audio_codec", "probed_at",
		}).AddRow(
			"00000000-0000-4000-8000-000000000001", int64(1), "uploads/1/a.jpg", "media",
			"image/jpeg", int64(2048), string(models.MediaAssetStatusReady),
			"sha", "", now.Add(24*time.Hour), now, now,
			nil, nil, nil, nil, nil, "", "", nil,
		))
	got, err := repo.ListVisibleInWorkspace(context.Background(), 7, ids)
	if err != nil {
		t.Fatalf("ListVisibleInWorkspace: %v", err)
	}
	if len(got) != 1 || got[0].ID != ids[0] || got[0].UserID != 1 {
		t.Fatalf("unexpected result: %+v", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestMediaAssetRepository_ListVisibleInWorkspaceRejectsBadWorkspace(t *testing.T) {
	db, mock := newThumbnailProjectMockDB(t)
	repo := repository.NewMediaAssetRepository(db)
	_, err := repo.ListVisibleInWorkspace(context.Background(), 0, []string{"00000000-0000-4000-8000-000000000001"})
	if err == nil {
		t.Fatal("want error for invalid workspace id")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestMediaAssetRepository_ListVisibleInWorkspaceEmptyIDsReturnsNil(t *testing.T) {
	db, mock := newThumbnailProjectMockDB(t)
	repo := repository.NewMediaAssetRepository(db)
	got, err := repo.ListVisibleInWorkspace(context.Background(), 7, nil)
	if err != nil || got != nil {
		t.Fatalf("want (nil, nil), got (%v, %v)", got, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
