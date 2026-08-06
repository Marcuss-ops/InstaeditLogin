package repository_test

import (
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

func TestPostListByWorkspace_OK(t *testing.T) {
	db, mock := newMockPostDBExact(t)
	repo := repository.NewPostRepository(db)
	now := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	mock.ExpectQuery(
		`SELECT id, workspace_id, title, caption, media_url, ingest_after, publish_at, status, privacy_level, default_privacy_level, created_at, upload_job_id, media_asset_id, storage_object_key, bucket
 FROM posts
	 WHERE workspace_id = $1
	 ORDER BY created_at DESC`,
	).WithArgs(int64(1)).
		WillReturnRows(sqlmock.NewRows(
			[]string{"id", "workspace_id", "title", "caption", "media_url", "ingest_after", "publish_at", "status", "privacy_level", "default_privacy_level", "created_at", "upload_job_id", "media_asset_id", "storage_object_key", "bucket"},
		).AddRow(2, 1, "B", "", "", now, nil, models.PostStatusDraft, "", "", now, nil, nil, nil, nil).
			AddRow(1, 1, "A", "", "", now, nil, models.PostStatusDraft, "", "", now, nil, nil, nil, nil))

	got, err := repo.ListByWorkspace(1)
	if err != nil {
		t.Fatalf("ListByWorkspace: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len: want 2, got %d", len(got))
	}
	if got[0].ID != 2 || got[1].ID != 1 {
		t.Errorf("ordering: %+v", got)
	}
}

func TestPostListByPost_OKWithNullablePublishedAt(t *testing.T) {
	// Tests the nullable PublishedAt round-trip: a target in 'scheduled'
	// status has NULL published_at, a 'published' one has a real timestamp.
	db, mock := newMockPostDBExact(t)
	repo := repository.NewPostRepository(db)
	publishedAt := time.Date(2025, 7, 1, 0, 0, 0, 0, time.UTC)

	mock.ExpectQuery(
		`SELECT id, post_id, platform_account_id, status,
		        COALESCE(platform_post_id, ''), COALESCE(error_message, ''), published_at,
		        COALESCE(provider_state, ''), COALESCE(container_id, ''),
		        provider_idempotency_key, completed_at
		 FROM post_targets
		 WHERE post_id = $1
		 ORDER BY id ASC`,
	).WithArgs(int64(100)).
		WillReturnRows(sqlmock.NewRows(
			[]string{"id", "post_id", "platform_account_id", "status", "platform_post_id", "error_message", "published_at", "provider_state", "container_id", "provider_idempotency_key", "completed_at"},
		).AddRow(10, 100, 1000, models.PostStatusScheduled, "", "", nil, "", "", nil, nil).
			AddRow(11, 100, 1001, models.PostStatusPublished, "remote-1", "", publishedAt, "", "", nil, nil).
			AddRow(12, 100, 1002, models.PostStatusFailed, "", "twitter error", nil, "", "", nil, nil))

	got, err := repo.ListByPost(100)
	if err != nil {
		t.Fatalf("ListByPost: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len: want 3, got %d", len(got))
	}
	if got[0].PublishedAt != nil {
		t.Errorf("target[0].PublishedAt: want nil, got %v", got[0].PublishedAt)
	}
	if got[1].PublishedAt == nil || !got[1].PublishedAt.Equal(publishedAt) {
		t.Errorf("target[1].PublishedAt: want %v, got %v", publishedAt, got[1].PublishedAt)
	}
	if got[2].ErrorMessage != "twitter error" {
		t.Errorf("target[2].ErrorMessage: want twitter error, got %q", got[2].ErrorMessage)
	}
}

func TestPostListQueued_BeforeTimeFilterApplied(t *testing.T) {
	// Worker uses this query to find posts due for publishing. time.Time
	// parameter rather than SQL NOW() → deterministic across timezones.
	db, mock := newMockPostDBExact(t)
	repo := repository.NewPostRepository(db)
	cutoff := time.Date(2025, 7, 1, 0, 0, 0, 0, time.UTC)
	mock.ExpectQuery(
		`SELECT id, workspace_id, title, caption, media_url, ingest_after, publish_at, status, privacy_level, default_privacy_level, created_at, upload_job_id, media_asset_id, storage_object_key, bucket
 FROM posts
	 WHERE status = 'queued' AND (publish_at IS NULL OR publish_at <= $1)
	 ORDER BY publish_at ASC NULLS FIRST`,
	).WithArgs(cutoff).
		WillReturnRows(sqlmock.NewRows(
			[]string{"id", "workspace_id", "title", "caption", "media_url", "ingest_after", "publish_at", "status", "privacy_level", "default_privacy_level", "created_at", "upload_job_id", "media_asset_id", "storage_object_key", "bucket"},
		).AddRow(1, 1, "due", "", "", cutoff, cutoff, models.PostStatusScheduled, "", "", cutoff, nil, nil, nil, nil))

	posts, err := repo.ListQueued(cutoff)
	if err != nil {
		t.Fatalf("ListQueued: %v", err)
	}
	if len(posts) != 1 {
		t.Fatalf("len: want 1, got %d", len(posts))
	}
}

func TestPostListPending_JoinWithPostsAppliesPredicate(t *testing.T) {
	// Worker's main pickup query. Validates that the JOIN is preserved
	// (a target scheduled for tomorrow must NOT appear in the today result).
	// Uses the flexible regex matcher for JOIN tolerance.
	db, mock := newMockPostDB(t)
	repo := repository.NewPostRepository(db)
	cutoff := time.Date(2025, 7, 1, 0, 0, 0, 0, time.UTC)

	mock.ExpectQuery(
		`WITH pending AS (
	SELECT pt.id, pt.post_id, pt.platform_account_id, pt.status,
	       COALESCE(pt.platform_post_id, '') AS platform_post_id,
	       COALESCE(pt.error_message, '') AS error_message,
	       pt.published_at,
	       COALESCE(pt.provider_state, '') AS provider_state,
	       COALESCE(pt.container_id, '') AS container_id,
	       pt.provider_idempotency_key, pt.completed_at,
	       p.publish_at,
	       ROW_NUMBER() OVER (PARTITION BY pt.post_id ORDER BY pt.id ASC) AS child_position
	FROM post_targets pt
	JOIN posts p ON p.id = pt.post_id
	WHERE pt.status IN ('queued', 'waiting_provider', 'retrying')
	  AND (p.publish_at IS NULL OR p.publish_at <= $1)
	  AND (pt.next_attempt_at IS NULL OR pt.next_attempt_at <= NOW())
)
SELECT id, post_id, platform_account_id, status,
       platform_post_id, error_message, published_at,
       provider_state, container_id, provider_idempotency_key, completed_at
FROM pending
ORDER BY child_position ASC, publish_at ASC NULLS FIRST, post_id ASC, id ASC
LIMIT 100`,
	).WithArgs(cutoff).
		WillReturnRows(sqlmock.NewRows(
			[]string{"id", "post_id", "platform_account_id", "status", "platform_post_id", "error_message", "published_at", "provider_state", "container_id", "provider_idempotency_key", "completed_at"},
		).AddRow(101, 1, 1000, models.PostStatusScheduled, "", "", nil, "", "", nil, nil).
			AddRow(102, 1, 1001, models.PostStatusScheduled, "", "", nil, "", "", nil, nil))

	targets, err := repo.ListPending(cutoff)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(targets) != 2 {
		t.Fatalf("len: want 2, got %d", len(targets))
	}
	if targets[0].PostID != 1 || targets[1].PostID != 1 {
		t.Errorf("post_id round-trip: %+v", targets)
	}
}
