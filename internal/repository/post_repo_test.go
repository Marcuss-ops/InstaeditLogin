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
	mock.ExpectQuery(
		`INSERT INTO post_targets (post_id, platform_account_id, status)
		 VALUES ($1, $2, $3)
		 RETURNING id`,
	).WithArgs(int64(100), int64(10), models.PostStatusDraft).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(200))
	mock.ExpectQuery(
		`INSERT INTO post_targets (post_id, platform_account_id, status)
		 VALUES ($1, $2, $3)
		 RETURNING id`,
	).WithArgs(int64(100), int64(11), models.PostStatusDraft).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(201))
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

func TestPostCreate_TargetInsertFails_RollsBack(t *testing.T) {
	// Critical tx test: second INSERT fails → tx.Rollback called (no orphan
	// post visible, no orphan target visible).
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
			// The second target wouldn't be touched because the first fails.
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

func TestPostUpdate_HappyReturnsNil(t *testing.T) {
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
		MediaURL: "url", ScheduledAt: &now, Status: models.PostStatusScheduled,
	}
	if err := repo.Update(post); err != nil {
		t.Fatalf("Update: %v", err)
	}
}func TestPostUpdate_ZeroRowsAffected_WrapsErrPostUnauthorized(t *testing.T) {
	// The whole point of feat(repo): surface rows-affected = 0 as a real
	// error so cross-workspace updates don't silently succeed AND so the
	// API layer can map the typed sentinel via errors.Is to 404.
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
		 SET status = $1, platform_post_id = $2, error_message = $3, published_at = $4
		 WHERE id = $5`,
	).WithArgs(models.PostStatusPublished, "remote-123", "", &now, int64(200)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	tgt := &models.PostTarget{
		ID: 200, Status: models.PostStatusPublished,
		PlatformPostID: "remote-123", PublishedAt: &now,
	}
	if err := repo.UpdateStatus(tgt); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
}func TestPostUpdateStatus_ZeroRowsAffected_WrapsErrPostTargetNotFound(t *testing.T) {
	// Same defensive check as Update: a 0-rows-affected response means the
	// target id is stale and the worker would otherwise see a phantom OK.
	// Sentinel (ErrPostTargetNotFound) lets the worker drop the phantom.
	db, mock := newMockPostDBExact(t)
	repo := repository.NewPostRepository(db)

	mock.ExpectExec(
		`UPDATE post_targets
	 SET status = $1, platform_post_id = $2, error_message = $3, published_at = $4
	 WHERE id = $5`,
	).WithArgs(models.PostStatusFailed, "", "publish error", (*time.Time)(nil), int64(999)).
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

	// Crucially test BOTH the populated ScheduledAt path AND the nil path
	// (lib/pq + driver.Valuer interplay is non-trivial).
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
	if p.ScheduledAt == nil || !p.ScheduledAt.Equal(now) {
		t.Errorf("ScheduledAt: want %v, got %v", now, p.ScheduledAt)
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
	if p.ScheduledAt != nil {
		t.Errorf("ScheduledAt: want nil, got %v", p.ScheduledAt)
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
		`SELECT id, post_id, platform_account_id, status, platform_post_id, error_message, published_at
		 FROM post_targets
		 WHERE post_id = $1
		 ORDER BY id ASC`,
	).WithArgs(int64(100)).
		WillReturnRows(sqlmock.NewRows(
			[]string{"id", "post_id", "platform_account_id", "status", "platform_post_id", "error_message", "published_at"},
		).AddRow(10, 100, 1000, models.PostStatusScheduled, "", "", nil).
			AddRow(11, 100, 1001, models.PostStatusPublished, "remote-1", "", publishedAt).
			AddRow(12, 100, 1002, models.PostStatusFailed, "", "twitter error", nil))

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

func TestPostListScheduled_BeforeTimeFilterApplied(t *testing.T) {
	// Worker uses this query to find posts due for publishing. time.Time
	// parameter rather than SQL NOW() → deterministic across timezones.
	db, mock := newMockPostDBExact(t)
	repo := repository.NewPostRepository(db)
	cutoff := time.Date(2025, 7, 1, 0, 0, 0, 0, time.UTC)

	mock.ExpectQuery(
		`SELECT id, workspace_id, title, caption, media_url, scheduled_at, status, created_at
		 FROM posts
		 WHERE status = 'scheduled' AND scheduled_at <= $1
		 ORDER BY scheduled_at ASC`,
	).WithArgs(cutoff).
		WillReturnRows(sqlmock.NewRows(
			[]string{"id", "workspace_id", "title", "caption", "media_url", "scheduled_at", "status", "created_at"},
		).AddRow(1, 1, "due", "", "", cutoff, models.PostStatusScheduled, cutoff))

	posts, err := repo.ListScheduled(cutoff)
	if err != nil {
		t.Fatalf("ListScheduled: %v", err)
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
		        pt.platform_post_id, pt.error_message, pt.published_at
		 FROM post_targets pt
		 JOIN posts p ON p.id = pt.post_id
		 WHERE pt.status = 'scheduled' AND p.scheduled_at <= $1
		 ORDER BY p.scheduled_at ASC`,
	).WithArgs(cutoff).
		WillReturnRows(sqlmock.NewRows(
			[]string{"id", "post_id", "platform_account_id", "status", "platform_post_id", "error_message", "published_at"},
		).AddRow(101, 1, 1000, models.PostStatusScheduled, "", "", nil).
			AddRow(102, 1, 1001, models.PostStatusScheduled, "", "", nil))

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
