package repository_test

import (
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// regexMatcher is a sqlmock.QueryMatcher that allows whitespace-tolerant
// JOIN and SELECT matchers where exact whitespace would be brittle.
func regexMatcher() sqlmock.QueryMatcher {
	return sqlmock.QueryMatcherFunc(func(expectedSQL, actualSQL string) error {
		// Trim spaces around the pattern so a multi-line expected query
		// matches when the runner folds whitespace.
		expected := regexp.MustCompile(`\s+`).ReplaceAllString(expectedSQL, `\s+`)
		re, err := regexp.Compile(expected)
		if err == nil && re.MatchString(actualSQL) {
			return nil
		}
		// Fall back to exact-string equality. We do NOT call
		// sqlmock.QueryMatcherEqual here because it's a var (QueryMatcher
		// interface), not a function — invoking it as `sqlmock.QueryMatcherEqual(a, b)`
		// is a compile error. The plain `==` is what sqlmock's default
		// matcher does internally.
		if expectedSQL == actualSQL {
			return nil
		}
		return fmt.Errorf("sqlmock: query mismatch (regex or exact)\nwant: %s\ngot:  %s", expectedSQL, actualSQL)
	})
}

// newMockPostDB like newMockWorkspaceDB but with the regex-flex matcher.
// Use for queries whose whitespace might vary (ListPending JOIN).
func newMockPostDB(t *testing.T) (*sql.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(regexMatcher()))
	if err != nil {
		t.Fatalf("create sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, mock
}

// newMockPostDBExact returns a sqlmock with strict equality matcher.
// Use for queries where exact whitespace matters (Create, Update, etc.).
func newMockPostDBExact(t *testing.T) (*sql.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatalf("create sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, mock
}

func TestPostCreate_AtomicTx_Happy(t *testing.T) {
	// Taglio 5.0 STEP 1: Create writes posts + ALL post_targets + ALL
	// outbox_events in ONE transaction. The production code does TWO
	// separate target loops (first fills target.ID via RETURNING,
	// second writes outbox rows referencing target.ID). Mock order:
	//   Begin, INSERT posts RETURNING (id=100),
	//   INSERT post_targets (target A → id=200),
	//   INSERT post_targets (target B → id=201),
	//   INSERT outbox_events (target=200), INSERT outbox_events (target=201),
	//   Commit.
	db, mock := newMockPostDBExact(t)
	repo := repository.NewPostRepository(db)
	now := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)

	mock.ExpectBegin()
	mock.ExpectQuery(
		`INSERT INTO posts (workspace_id, title, caption, media_url, scheduled_at, status)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING id, created_at`,
	).WithArgs(int64(1), "hello", "world", "", (*time.Time)(nil), models.PostStatusDraft).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(100, now))
	// Target A: id=200 from RETURNING (first iteration of targets loop).
	mock.ExpectQuery(
		`INSERT INTO post_targets (post_id, platform_account_id, status)
		 VALUES ($1, $2, $3)
		 RETURNING id`,
	).WithArgs(int64(100), int64(10), models.PostStatusDraft).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(200))
	// Target B: id=201 from RETURNING (second iteration of targets loop).
	// BOTH post_targets must INSERT before ANY outbox INSERT because
	// t.ID must be populated for both targets before the outbox loop runs.
	mock.ExpectQuery(
		`INSERT INTO post_targets (post_id, platform_account_id, status)
		 VALUES ($1, $2, $3)
		 RETURNING id`,
	).WithArgs(int64(100), int64(11), models.PostStatusDraft).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(201))
	// Outbox loop now: target 0's outbox first, target 1's second.
	mock.ExpectExec(
		`INSERT INTO outbox_events (aggregate_type, aggregate_id, event_type, payload)
		 VALUES ($1, $2, $3, $4::jsonb)`,
	).WithArgs(
		"post_target", int64(200), "post_target.publish_requested",
		sqlmock.AnyArg(),
	).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(
		`INSERT INTO outbox_events (aggregate_type, aggregate_id, event_type, payload)
		 VALUES ($1, $2, $3, $4::jsonb)`,
	).WithArgs(
		"post_target", int64(201), "post_target.publish_requested",
		sqlmock.AnyArg(),
	).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	post := &models.Post{
		WorkspaceID: 1, Title: "hello", Caption: "world",
		Status: models.PostStatusDraft,
	}
	targets := []*models.PostTarget{
		{PlatformAccountID: 10, Status: models.PostStatusDraft},
		{PlatformAccountID: 11, Status: models.PostStatusDraft},
	}
	if err := repo.Create(post, targets); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if post.ID != 100 {
		t.Errorf("post.ID: want 100, got %d", post.ID)
	}
	if !post.CreatedAt.Equal(now) {
		t.Errorf("post.CreatedAt: want %v, got %v", now, post.CreatedAt)
	}
	if targets[0].PostID != 100 || targets[0].ID != 200 {
		t.Errorf("target[0]: %+v", targets[0])
	}
	if targets[1].PostID != 100 || targets[1].ID != 201 {
		t.Errorf("target[1]: %+v", targets[1])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestPostCreate_EmptyTargets_OKSkipsTargetInserts(t *testing.T) {
	// Empty targets: no post_target INSERT, no outbox INSERT. The tx still
	// commits with just the post row.
	db, mock := newMockPostDBExact(t)
	repo := repository.NewPostRepository(db)
	now := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)

	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO posts (workspace_id, title, caption, media_url, scheduled_at, status)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING id, created_at`).
		WithArgs(int64(1), "draft", "", "", (*time.Time)(nil), models.PostStatusDraft).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(100, now))
	// No target insert expectations — we pass nil/empty targets.
	// No outbox insert expectations either — no targets means no outbox events.
	mock.ExpectCommit()

	if err := repo.Create(&models.Post{
		WorkspaceID: 1, Title: "draft", Status: models.PostStatusDraft,
	}, nil); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestPostRepository_Create_TxRollback(t *testing.T) {
	// Critical tx test: first post_target INSERT fails → tx.Rollback
	// called (no orphan post visible, no orphan target visible, no orphan
	// outbox). The deferred rollback propagates the error.
	db, mock := newMockPostDBExact(t)
	repo := repository.NewPostRepository(db)
	now := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)

	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO posts (workspace_id, title, caption, media_url, scheduled_at, status)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING id, created_at`).
		WithArgs(int64(1), "hello", "", "", (*time.Time)(nil), models.PostStatusDraft).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(100, now))
	mock.ExpectQuery(`INSERT INTO post_targets (post_id, platform_account_id, status)
		 VALUES ($1, $2, $3)
		 RETURNING id`).
		WithArgs(int64(100), int64(10), models.PostStatusDraft).
		WillReturnError(errors.New("unique violation on (post_id, platform_account_id)"))
	mock.ExpectRollback()

	err := repo.Create(
		&models.Post{WorkspaceID: 1, Title: "hello", Status: models.PostStatusDraft},
		[]*models.PostTarget{
			{PlatformAccountID: 10, Status: models.PostStatusDraft},
		},
	)
	if err == nil {
		t.Fatal("expected error from failing INSERT, got nil")
	}
	if !strings.Contains(err.Error(), "unique violation") {
		t.Errorf("error should preserve underlying message: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations (rollback should have been called): %v", err)
	}
}

func TestPostCreate_BeginTxFails_NoCommitOrRollback(t *testing.T) {
	db, mock := newMockPostDBExact(t)
	repo := repository.NewPostRepository(db)

	mock.ExpectBegin().WillReturnError(errors.New("dial timeout"))

	err := repo.Create(
		&models.Post{WorkspaceID: 1, Title: "hello", Status: models.PostStatusDraft},
		[]*models.PostTarget{{PlatformAccountID: 10}},
	)
	if err == nil {
		t.Fatal("expected error from Begin, got nil")
	}
	if !strings.Contains(err.Error(), "failed to begin create-post tx") {
		t.Errorf("error message: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestPostRepository_Update_Success(t *testing.T) {
	db, mock := newMockPostDBExact(t)
	repo := repository.NewPostRepository(db)
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)

	mock.ExpectExec(
		`UPDATE posts
		 SET title = $1, caption = $2, media_url = $3, scheduled_at = $4, status = $5
		 WHERE id = $6 AND workspace_id = $7`,
	).WithArgs("new", "cap", "url", &now, models.PostStatusScheduled, int64(100), int64(1)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	post := &models.Post{
		ID: 100, WorkspaceID: 1, Title: "new", Caption: "cap",
		MediaURL: "url", PublishAt: &now, Status: models.PostStatusScheduled,
	}
	if err := repo.Update(post); err != nil {
		t.Fatalf("Update: %v", err)
	}
}

// TestPostRepository_Update_NotFound covers the rows-affected=0 path:
// the wrapper must carry the typed sentinel so pkg/api can map via
// errors.Is, AND must retain id context for log lines.
func TestPostRepository_Update_NotFound(t *testing.T) {
	db, mock := newMockPostDBExact(t)
	repo := repository.NewPostRepository(db)

	mock.ExpectExec(
		`UPDATE posts
	 SET title = $1, caption = $2, media_url = $3, scheduled_at = $4, status = $5
	 WHERE id = $6 AND workspace_id = $7`,
	).WithArgs("x", "", "", (*time.Time)(nil), models.PostStatusDraft, int64(999), int64(7)).
		WillReturnResult(sqlmock.NewResult(0, 0))

	err := repo.Update(&models.Post{
		ID: 999, WorkspaceID: 7, Title: "x", Status: models.PostStatusDraft,
	})
	if err == nil {
		t.Fatal("expected tenant-isolation error, got nil")
	}
	if !errors.Is(err, repository.ErrPostUnauthorized) {
		t.Errorf("error must wrap repository.ErrPostUnauthorized sentinel, got: %v", err)
	}
	if !strings.Contains(err.Error(), "id=999") {
		t.Errorf("error should retain id in message for debuggability: %v", err)
	}
}

func TestPostUpdate_ExecErrorPropagates(t *testing.T) {
	db, mock := newMockPostDBExact(t)
	repo := repository.NewPostRepository(db)

	mock.ExpectExec(
		`UPDATE posts
		 SET title = $1, caption = $2, media_url = $3, scheduled_at = $4, status = $5
		 WHERE id = $6 AND workspace_id = $7`,
	).WithArgs("x", "", "", (*time.Time)(nil), models.PostStatusDraft, int64(100), int64(7)).
		WillReturnError(errors.New("db down"))

	err := repo.Update(&models.Post{
		ID: 100, WorkspaceID: 7, Title: "x", Status: models.PostStatusDraft,
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "failed to update post") {
		t.Errorf("error should be wrapped: %v", err)
	}
}

func TestPostUpdateStatus_Happy(t *testing.T) {
	db, mock := newMockPostDBExact(t)
	repo := repository.NewPostRepository(db)
	now := time.Date(2025, 7, 1, 0, 0, 0, 0, time.UTC)

	mock.ExpectExec(
		`UPDATE post_targets
		 SET status = $1, platform_post_id = $2, error_message = $3, published_at = $4,
		     provider_state = $6, container_id = $7
		 WHERE id = $5`,
	).WithArgs(models.PostStatusPublished, "remote-123", "", &now, int64(200), "", "").
		WillReturnResult(sqlmock.NewResult(0, 1))

	tgt := &models.PostTarget{
		ID: 200, Status: models.PostStatusPublished,
		PlatformPostID: "remote-123", PublishedAt: &now,
	}
	if err := repo.UpdateStatus(tgt); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
}

// TestPostRepository_UpdateStatus_StaleTarget covers rows-affected=0
// on post_target: the wrapper must carry the sentinel so the worker
// drops the phantom status transition.
func TestPostRepository_UpdateStatus_StaleTarget(t *testing.T) {
	db, mock := newMockPostDBExact(t)
	repo := repository.NewPostRepository(db)

	mock.ExpectExec(
		`UPDATE post_targets
	 SET status = $1, platform_post_id = $2, error_message = $3, published_at = $4,
	     provider_state = $6, container_id = $7
	 WHERE id = $5`,
	).WithArgs(models.PostStatusFailed, "", "publish error", (*time.Time)(nil), int64(999), "", "").
		WillReturnResult(sqlmock.NewResult(0, 0))

	err := repo.UpdateStatus(&models.PostTarget{
		ID: 999, Status: models.PostStatusFailed, ErrorMessage: "publish error",
	})
	if err == nil {
		t.Fatal("expected ghost-state error, got nil")
	}
	if !errors.Is(err, repository.ErrPostTargetNotFound) {
		t.Errorf("error must wrap repository.ErrPostTargetNotFound sentinel, got: %v", err)
	}
	if !strings.Contains(err.Error(), "id=999") {
		t.Errorf("error should retain id in message for debuggability: %v", err)
	}
}

// TestPostSave_Happy asserts that PostRepository.Save (the worker's
// "add another platform to an existing post" code path) correctly sets
// target.ID from RETURNING. Distinct from PostRepository.Create which is
// a tx-wrapped multi-row insert; Save is a single INSERT with no tx.
func TestPostSave_Happy(t *testing.T) {
	db, mock := newMockPostDBExact(t)
	repo := repository.NewPostRepository(db)

	mock.ExpectQuery(
		`INSERT INTO post_targets (post_id, platform_account_id, status)
		 VALUES ($1, $2, $3)
		 RETURNING id`,
	).WithArgs(int64(100), int64(20), models.PostStatusScheduled).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(456)))

	tgt := &models.PostTarget{
		PostID:            100,
		PlatformAccountID: 20,
		Status:            models.PostStatusScheduled,
	}
	if err := repo.Save(tgt); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if tgt.ID != 456 {
		t.Errorf("ID: want 456, got %d", tgt.ID)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestPostSave_DBError(t *testing.T) {
	db, mock := newMockPostDBExact(t)
	repo := repository.NewPostRepository(db)

	mock.ExpectQuery(
		`INSERT INTO post_targets (post_id, platform_account_id, status)
		 VALUES ($1, $2, $3)
		 RETURNING id`,
	).WithArgs(int64(100), int64(20), models.PostStatusScheduled).
		WillReturnError(errors.New("unique violation on (post_id, platform_account_id)"))

	err := repo.Save(&models.PostTarget{
		PostID:            100,
		PlatformAccountID: 20,
		Status:            models.PostStatusScheduled,
	})
	if err == nil {
		t.Fatal("expected error from Save, got nil")
	}
	if !strings.Contains(err.Error(), "unique violation") {
		t.Errorf("error should preserve underlying message: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestPostFindByID_FoundWithNullableTime(t *testing.T) {
	db, mock := newMockPostDBExact(t)
	repo := repository.NewPostRepository(db)
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)

	mock.ExpectQuery(
		`SELECT id, workspace_id, title, caption, media_url, scheduled_at, status, created_at
		 FROM posts
		 WHERE id = $1`,
	).WithArgs(int64(100)).
		WillReturnRows(sqlmock.NewRows(
			[]string{"id", "workspace_id", "title", "caption", "media_url", "scheduled_at", "status", "created_at"},
		).AddRow(100, 1, "scheduled", "cap", "url", now, models.PostStatusScheduled, time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)))

	p, err := repo.FindByID(100)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if p == nil {
		t.Fatal("post nil, want populated")
	}
	if p.PublishAt == nil || !p.PublishAt.Equal(now) {
		t.Errorf("PublishAt: want %v, got %v", now, p.PublishAt)
	}
	if p.Status != models.PostStatusScheduled {
		t.Errorf("Status: want scheduled, got %q", p.Status)
	}
}

func TestPostFindByID_NilScheduledAt_RoundTripsClean(t *testing.T) {
	db, mock := newMockPostDBExact(t)
	repo := repository.NewPostRepository(db)

	mock.ExpectQuery(
		`SELECT id, workspace_id, title, caption, media_url, scheduled_at, status, created_at
		 FROM posts
		 WHERE id = $1`,
	).WithArgs(int64(1)).
		WillReturnRows(sqlmock.NewRows(
			[]string{"id", "workspace_id", "title", "caption", "media_url", "scheduled_at", "status", "created_at"},
		).AddRow(1, 1, "draft", "", "", nil, models.PostStatusDraft, time.Now()))

	p, err := repo.FindByID(1)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if p == nil {
		t.Fatal("post nil")
	}
	if p.PublishAt != nil {
		t.Errorf("PublishAt: want nil, got %v", p.PublishAt)
	}
}

func TestPostFindByID_NotFoundReturnsNilNil(t *testing.T) {
	db, mock := newMockPostDBExact(t)
	repo := repository.NewPostRepository(db)

	mock.ExpectQuery(
		`SELECT id, workspace_id, title, caption, media_url, scheduled_at, status, created_at
		 FROM posts
		 WHERE id = $1`,
	).WithArgs(int64(999)).
		WillReturnError(sql.ErrNoRows)

	p, err := repo.FindByID(999)
	if err != nil {
		t.Fatalf("FindByID expected nil err for ErrNoRows, got %v", err)
	}
	if p != nil {
		t.Errorf("FindByID expected nil post, got %+v", p)
	}
}

func TestPostListByWorkspace_OK(t *testing.T) {
	db, mock := newMockPostDBExact(t)
	repo := repository.NewPostRepository(db)
	now := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)

	mock.ExpectQuery(
		`SELECT id, workspace_id, title, caption, media_url, scheduled_at, status, created_at
		 FROM posts
		 WHERE workspace_id = $1
		 ORDER BY created_at DESC`,
	).WithArgs(int64(1)).
		WillReturnRows(sqlmock.NewRows(
			[]string{"id", "workspace_id", "title", "caption", "media_url", "scheduled_at", "status", "created_at"},
		).AddRow(2, 1, "B", "", "", nil, models.PostStatusDraft, now).
			AddRow(1, 1, "A", "", "", nil, models.PostStatusDraft, now))

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
		`SELECT id, workspace_id, title, caption, media_url, scheduled_at, status, created_at
		 FROM posts
		 WHERE status = 'queued' AND scheduled_at <= $1
		 ORDER BY scheduled_at ASC`,
	).WithArgs(cutoff).
		WillReturnRows(sqlmock.NewRows(
			[]string{"id", "workspace_id", "title", "caption", "media_url", "scheduled_at", "status", "created_at"},
		).AddRow(1, 1, "due", "", "", cutoff, models.PostStatusScheduled, cutoff))

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
		`SELECT pt.id, pt.post_id, pt.platform_account_id, pt.status,
		        COALESCE(pt.platform_post_id, ''), COALESCE(pt.error_message, ''), pt.published_at,
		        COALESCE(pt.provider_state, ''), COALESCE(pt.container_id, ''),
		        pt.provider_idempotency_key, pt.completed_at
		 FROM post_targets pt
		 JOIN posts p ON p.id = pt.post_id
		 WHERE (pt.status = 'queued' OR pt.status = 'waiting_provider')
		   AND (p.scheduled_at IS NULL OR p.scheduled_at <= $1)
		 ORDER BY p.scheduled_at ASC NULLS FIRST`,
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

// TestPostCreate_ConcurrentGoroutines_NoSharedState covers the user's
// "transazioni concorrenti" requirement.
//
// What it tests: PostRepository has no shared mutable state — spinning up
// many goroutines, each with its own sqlmock and repo, succeeds with no
// panic, no leaked state.
//
// What it does NOT test: Postgres-level lock contention between honest
// concurrent writers against a real database. Use testcontainers-go + a
// real Postgres to exercise that, since sqlmock serializes queries globally
// on its internal gomock controller.
func TestPostCreate_ConcurrentGoroutines_NoSharedState(t *testing.T) {
	const numGoroutines = 5
	var wg sync.WaitGroup
	errs := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
			if err != nil {
				errs <- err
				return
			}
			defer db.Close()
			repo := repository.NewPostRepository(db)
			now := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
			postID := int64(100 + idx)
			tgtAID := int64(200 + idx*10)
			tgtBID := int64(201 + idx*10)

			mock.ExpectBegin()
			mock.ExpectQuery(
				`INSERT INTO posts (workspace_id, title, caption, media_url, scheduled_at, status)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING id, created_at`,
			).WithArgs(int64(1), "title", "", "", (*time.Time)(nil), models.PostStatusDraft).
				WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(postID, now))
			// Taglio 5.0 STEP 1: BOTH post_targets INSERT first (so the
			// RETURNING ids fill target.ID for both rows), THEN BOTH
			// outbox INSERTs.
			mock.ExpectQuery(
				`INSERT INTO post_targets (post_id, platform_account_id, status)
		 VALUES ($1, $2, $3)
		 RETURNING id`,
			).WithArgs(postID, int64(10+idx), models.PostStatusDraft).
				WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(tgtAID))
			mock.ExpectQuery(
				`INSERT INTO post_targets (post_id, platform_account_id, status)
		 VALUES ($1, $2, $3)
		 RETURNING id`,
			).WithArgs(postID, int64(11+idx), models.PostStatusDraft).
				WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(tgtBID))
			mock.ExpectExec(
				`INSERT INTO outbox_events (aggregate_type, aggregate_id, event_type, payload)
		 VALUES ($1, $2, $3, $4::jsonb)`,
			).WithArgs("post_target", tgtAID, "post_target.publish_requested", sqlmock.AnyArg()).
				WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectExec(
				`INSERT INTO outbox_events (aggregate_type, aggregate_id, event_type, payload)
		 VALUES ($1, $2, $3, $4::jsonb)`,
			).WithArgs("post_target", tgtBID, "post_target.publish_requested", sqlmock.AnyArg()).
				WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectCommit()

			if err := repo.Create(
				&models.Post{
					WorkspaceID: 1, Title: "title", Status: models.PostStatusDraft,
				},
				[]*models.PostTarget{
					{PlatformAccountID: int64(10 + idx), Status: models.PostStatusDraft},
					{PlatformAccountID: int64(11 + idx), Status: models.PostStatusDraft},
				},
			); err != nil {
				errs <- err
				return
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("goroutine error: %v", err)
	}
}

// TestPostClaimQueuedTarget_Success covers the FASE 1.1 SKIP LOCKED
// atomic-claim happy path: a single row in 'queued' is locked via
// SELECT FOR UPDATE SKIP LOCKED and then transitioned to 'publishing'
// inside an explicit tx. The function returns (true, nil).
func TestPostClaimQueuedTarget_Success(t *testing.T) {
	db, mock := newMockPostDBExact(t)
	repo := repository.NewPostRepository(db)

	// FASE 1.1: claim is now a tx: BEGIN → SELECT FOR UPDATE SKIP
	// LOCKED → UPDATE → COMMIT.
	mock.ExpectBegin()
	mock.ExpectQuery(
		`SELECT id FROM post_targets
		 WHERE id = $1 AND status = 'queued'
		 FOR UPDATE SKIP LOCKED`,
	).WithArgs(int64(200)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(200))
	mock.ExpectExec(
		`UPDATE post_targets SET status = 'publishing' WHERE id = $1`,
	).WithArgs(int64(200)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	claimed, err := repo.ClaimQueuedTarget(200)
	if err != nil {
		t.Fatalf("ClaimQueuedTarget: %v", err)
	}
	if !claimed {
		t.Errorf("claimed: want true, got false (SELECT returned row → claim won)")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// TestPostClaimQueuedTarget_AlreadyClaimed covers the FASE 1.1 SKIP
// LOCKED loser path: when another worker/tx already holds a row lock
// on this row, SELECT FOR UPDATE SKIP LOCKED returns zero rows
// immediately (no blocking). The function returns (false, nil).
func TestPostClaimQueuedTarget_AlreadyClaimed(t *testing.T) {
	db, mock := newMockPostDBExact(t)
	repo := repository.NewPostRepository(db)

	mock.ExpectBegin()
	mock.ExpectQuery(
		`SELECT id FROM post_targets
		 WHERE id = $1 AND status = 'queued'
		 FOR UPDATE SKIP LOCKED`,
	).WithArgs(int64(200)).
		WillReturnError(sql.ErrNoRows)
	// On SKIP LOCKED miss, the tx is rolled back (deferred). No UPDATE or COMMIT.
	mock.ExpectRollback()

	claimed, err := repo.ClaimQueuedTarget(200)
	if err != nil {
		t.Fatalf("ClaimQueuedTarget: %v (must NOT error when another worker already claimed; the loser path is a normal skip)", err)
	}
	if claimed {
		t.Errorf("claimed: want false, got true (SKIP LOCKED returned no rows → claim lost)")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// TestPostClaimQueuedTarget_DBError covers the FASE 1.1 path where
// Begin() itself fails (DB unreachable). The error must surface so
// the worker can log and continue to the next target.
func TestPostClaimQueuedTarget_DBError(t *testing.T) {
	db, mock := newMockPostDBExact(t)
	repo := repository.NewPostRepository(db)

	mock.ExpectBegin().WillReturnError(errors.New("connection lost"))

	claimed, err := repo.ClaimQueuedTarget(200)
	if err == nil {
		t.Fatal("expected DB error to propagate, got nil")
	}
	if claimed {
		t.Errorf("claimed: want false on DB error, got true")
	}
	if !strings.Contains(err.Error(), "failed to begin claim tx") {
		t.Errorf("error should be wrapped: %v", err)
	}
}

// TestPostClaimQueuedTarget_RowsAffectedReadError is REMOVED in FASE
// 1.1 — the old RowsAffected() error path (connection interrupted
// between Exec and RowsAffected) no longer exists. The new
// tx-based claim has different failure modes (Begin error, SELECT
// error, UPDATE error, Commit error). Covered by the tests above
// and the new TestPostClaimQueuedTarget_CommitError below.
func TestPostClaimQueuedTarget_CommitError(t *testing.T) {
	db, mock := newMockPostDBExact(t)
	repo := repository.NewPostRepository(db)

	mock.ExpectBegin()
	mock.ExpectQuery(
		`SELECT id FROM post_targets
		 WHERE id = $1 AND status = 'queued'
		 FOR UPDATE SKIP LOCKED`,
	).WithArgs(int64(200)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(200))
	mock.ExpectExec(
		`UPDATE post_targets SET status = 'publishing' WHERE id = $1`,
	).WithArgs(int64(200)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit().WillReturnError(errors.New("commit failed"))

	_, err := repo.ClaimQueuedTarget(200)
	if err == nil {
		t.Fatal("expected Commit error to propagate, got nil")
	}
	if !strings.Contains(err.Error(), "failed to commit claim") {
		t.Errorf("error should preserve 'commit claim' context: %v", err)
	}
}

// --- FASE 1.1: ClaimPublishingTarget tests (ReconcileWorker claim) ---

// TestPostClaimPublishingTarget_Success covers the reconciler's claim
// happy path: SELECT FOR UPDATE SKIP LOCKED finds a row in status
// 'publishing' with non-null platform_post_id, locks it, commits the
// tx (releasing the row lock), and returns true.
func TestPostClaimPublishingTarget_Success(t *testing.T) {
	db, mock := newMockPostDBExact(t)
	repo := repository.NewPostRepository(db)

	mock.ExpectBegin()
	mock.ExpectQuery(
		`SELECT id FROM post_targets
		 WHERE id = $1 AND status = 'publishing' AND platform_post_id IS NOT NULL AND platform_post_id <> ''
		 FOR UPDATE SKIP LOCKED`,
	).WithArgs(int64(300)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(300))
	// ClaimPublishingTarget does NOT UPDATE status — it only locks +
	// commits. The status stays 'publishing'.
	mock.ExpectCommit()

	claimed, err := repo.ClaimPublishingTarget(300)
	if err != nil {
		t.Fatalf("ClaimPublishingTarget: %v", err)
	}
	if !claimed {
		t.Errorf("claimed: want true, got false")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// TestPostClaimPublishingTarget_AlreadyClaimed covers the reconciler
// loser path: SELECT FOR UPDATE SKIP LOCKED returns no rows because
// another reconciler replica already holds the row lock.
func TestPostClaimPublishingTarget_AlreadyClaimed(t *testing.T) {
	db, mock := newMockPostDBExact(t)
	repo := repository.NewPostRepository(db)

	mock.ExpectBegin()
	mock.ExpectQuery(
		`SELECT id FROM post_targets
		 WHERE id = $1 AND status = 'publishing' AND platform_post_id IS NOT NULL AND platform_post_id <> ''
		 FOR UPDATE SKIP LOCKED`,
	).WithArgs(int64(300)).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectRollback()

	claimed, err := repo.ClaimPublishingTarget(300)
	if err != nil {
		t.Fatalf("ClaimPublishingTarget: %v", err)
	}
	if claimed {
		t.Errorf("claimed: want false (SKIP LOCKED returned no rows), got true")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// --- FASE 1.1: concurrent claim race test ---

// TestPostClaimQueuedTarget_ConcurrentRace_TwoGoroutines_OneWinner
// simulates two goroutines racing to claim the SAME row. The first
// one to select locks the row; the second gets SKIP LOCKED → zero
// rows → returns (false, nil). Only ONE claim succeeds.
//
// This is the FASE 1.1 end-to-end invariant: exactly one publish
// per target, even with N worker replicas.
func TestPostClaimQueuedTarget_ConcurrentRace_TwoGoroutines_OneWinner(t *testing.T) {
	// Worker A: will win the claim (successful SELECT).
	dbA, mockA, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatalf("create sqlmock A: %v", err)
	}
	defer dbA.Close()
	repoA := repository.NewPostRepository(dbA)

	mockA.ExpectBegin()
	mockA.ExpectQuery(
		`SELECT id FROM post_targets
		 WHERE id = $1 AND status = 'queued'
		 FOR UPDATE SKIP LOCKED`,
	).WithArgs(int64(200)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(200))
	mockA.ExpectExec(
		`UPDATE post_targets SET status = 'publishing' WHERE id = $1`,
	).WithArgs(int64(200)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mockA.ExpectCommit()

	// Worker B: will lose the claim (SELECT returns no rows).
	dbB, mockB, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatalf("create sqlmock B: %v", err)
	}
	defer dbB.Close()
	repoB := repository.NewPostRepository(dbB)

	mockB.ExpectBegin()
	mockB.ExpectQuery(
		`SELECT id FROM post_targets
		 WHERE id = $1 AND status = 'queued'
		 FOR UPDATE SKIP LOCKED`,
	).WithArgs(int64(200)).
		WillReturnError(sql.ErrNoRows)
	mockB.ExpectRollback()

	// Use a mutex to deterministically order the two goroutines
	// so A always claims first and B always loses.
	var (
		mu        sync.Mutex
		firstCall = true
	)

	var wg sync.WaitGroup
	wg.Add(2)
	var (
		aWon, bWon bool
		aErr, bErr error
	)

	go func() {
		defer wg.Done()
		mu.Lock()
		if firstCall {
			firstCall = false
			mu.Unlock()
			aWon, aErr = repoA.ClaimQueuedTarget(200)
		} else {
			mu.Unlock()
			aWon, aErr = repoA.ClaimQueuedTarget(200)
		}
	}()

	go func() {
		defer wg.Done()
		// B claims after A — mutex ensures ordering.
		mu.Lock()
		if firstCall {
			firstCall = false
			mu.Unlock()
			bWon, bErr = repoB.ClaimQueuedTarget(200)
		} else {
			mu.Unlock()
			bWon, bErr = repoB.ClaimQueuedTarget(200)
		}
	}()

	wg.Wait()

	// Assertions: A won, B lost.
	if aErr != nil {
		t.Errorf("Worker A error: %v", aErr)
	}
	if !aWon {
		t.Error("Worker A should have won the claim (first SELECT)")
	}
	if bErr != nil {
		t.Fatalf("Worker B error: %v (loser path must be nil on skip)", bErr)
	}
	if bWon {
		t.Error("Worker B should have lost the claim (SKIP LOCKED on already-locked row)")
	}

	// Verify all expectations were met.
	if err := mockA.ExpectationsWereMet(); err != nil {
		t.Errorf("Worker A unmet expectations: %v", err)
	}
	if err := mockB.ExpectationsWereMet(); err != nil {
		t.Errorf("Worker B unmet expectations: %v", err)
	}
}
