package repository

import (
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/lib/pq"
)

func TestPostRepositoryListByWorkspacePage(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	when := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	mock.ExpectQuery(`SELECT id, workspace_id, title, caption, media_url, media_asset_id, storage_object_key, bucket`).
		WithArgs(int64(7), nil, int64(0), 3).
		WillReturnRows(sqlmock.NewRows([]string{"id", "workspace_id", "title", "caption", "media_url", "media_asset_id", "storage_object_key", "bucket", "privacy_level", "default_privacy_level", "ingest_after", "publish_at", "status", "upload_job_id", "created_at"}).
			AddRow(3, 7, "latest", "", "", nil, nil, nil, "", "", when, nil, models.PostStatusDraft, nil, when).
			AddRow(2, 7, "older", "", "", nil, nil, nil, "", "", when, nil, models.PostStatusDraft, nil, when).
			AddRow(1, 7, "oldest", "", "", nil, nil, nil, "", "", when, nil, models.PostStatusDraft, nil, when))
	posts, more, err := NewPostRepository(db).ListByWorkspacePage(7, nil, 0, 2)
	if err != nil || !more || len(posts) != 2 {
		t.Fatalf("page = %d, %v, %v", len(posts), more, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPostRepositoryListByWorkspacesPage(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	when := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	mock.ExpectQuery(`workspace_id = ANY`).
		WithArgs(pq.Array([]int64{7, 8}), nil, int64(0), 3).
		WillReturnRows(sqlmock.NewRows([]string{"id", "workspace_id", "title", "caption", "media_url", "media_asset_id", "storage_object_key", "bucket", "privacy_level", "default_privacy_level", "ingest_after", "publish_at", "status", "upload_job_id", "created_at"}).
			AddRow(3, 7, "latest", "", "", nil, nil, nil, "", "", when, nil, models.PostStatusDraft, nil, when).
			AddRow(2, 8, "older", "", "", nil, nil, nil, "", "", when, nil, models.PostStatusDraft, nil, when).
			AddRow(1, 7, "oldest", "", "", nil, nil, nil, "", "", when, nil, models.PostStatusDraft, nil, when))
	posts, more, err := NewPostRepository(db).ListByWorkspacesPage([]int64{7, 8}, nil, 0, 2)
	if err != nil || !more || len(posts) != 2 {
		t.Fatalf("page = %d, %v, %v", len(posts), more, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
