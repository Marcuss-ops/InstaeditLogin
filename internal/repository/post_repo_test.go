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
		// matches when the runner folds whitespace. QuoteMeta first so SQL
		// metacharacters ($1 placeholders, single quotes, parentheses) are
		// not interpreted as regex syntax.
		expected := regexp.MustCompile(`\s+`).ReplaceAllString(regexp.QuoteMeta(expectedSQL), `\s+`)
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
		`INSERT INTO posts (workspace_id, title, caption, media_url, ingest_after, publish_at, default_privacy_level, privacy_level, status, upload_job_id, media_asset_id, storage_object_key, bucket)
 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
 ON CONFLICT (upload_job_id) WHERE upload_job_id IS NOT NULL DO NOTHING
 RETURNING id, created_at, upload_job_id`,
	).WithArgs(int64(1), "hello", "world", "", sqlmock.AnyArg(), (*time.Time)(nil), "", "", models.PostStatusDraft, nil, sql.NullString{}, sql.NullString{}, sql.NullString{}).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "upload_job_id"}).AddRow(100, now, nil))
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
	mock.ExpectQuery(`INSERT INTO posts (workspace_id, title, caption, media_url, ingest_after, publish_at, default_privacy_level, privacy_level, status, upload_job_id, media_asset_id, storage_object_key, bucket)
 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
 ON CONFLICT (upload_job_id) WHERE upload_job_id IS NOT NULL DO NOTHING
 RETURNING id, created_at, upload_job_id`).
		WithArgs(int64(1), "draft", "", "", sqlmock.AnyArg(), (*time.Time)(nil), "", "", models.PostStatusDraft, nil, sql.NullString{}, sql.NullString{}, sql.NullString{}).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "upload_job_id"}).AddRow(100, now, nil))
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
	mock.ExpectQuery(`INSERT INTO posts (workspace_id, title, caption, media_url, ingest_after, publish_at, default_privacy_level, privacy_level, status, upload_job_id, media_asset_id, storage_object_key, bucket)
 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
 ON CONFLICT (upload_job_id) WHERE upload_job_id IS NOT NULL DO NOTHING
 RETURNING id, created_at, upload_job_id`).
		WithArgs(int64(1), "hello", "", "", sqlmock.AnyArg(), (*time.Time)(nil), "", "", models.PostStatusDraft, nil, sql.NullString{}, sql.NullString{}, sql.NullString{}).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "upload_job_id"}).AddRow(100, now, nil))
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
		 SET title = $1, caption = $2, media_url = $3, publish_at = $4, privacy_level = $5, default_privacy_level = $6, status = $7, media_asset_id = $8, storage_object_key = $9, bucket = $10
		 WHERE id = $11 AND workspace_id = $12`,
	).WithArgs("new", "cap", "url", &now, "", "", models.PostStatusScheduled, sql.NullString{}, sql.NullString{}, sql.NullString{}, int64(100), int64(1)).
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
	 SET title = $1, caption = $2, media_url = $3, publish_at = $4, privacy_level = $5, default_privacy_level = $6, status = $7, media_asset_id = $8, storage_object_key = $9, bucket = $10
	 WHERE id = $11 AND workspace_id = $12`,
	).WithArgs("x", "", "", (*time.Time)(nil), "", "", models.PostStatusDraft, sql.NullString{}, sql.NullString{}, sql.NullString{}, int64(999), int64(7)).
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
		 SET title = $1, caption = $2, media_url = $3, publish_at = $4, privacy_level = $5, default_privacy_level = $6, status = $7, media_asset_id = $8, storage_object_key = $9, bucket = $10
		 WHERE id = $11 AND workspace_id = $12`).WithArgs("x", "", "", (*time.Time)(nil), "", "", models.PostStatusDraft, sql.NullString{}, sql.NullString{}, sql.NullString{}, int64(100), int64(7)).
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

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT post_id FROM post_targets WHERE id = $1`).WithArgs(int64(200)).
		WillReturnRows(sqlmock.NewRows([]string{"post_id"}).AddRow(100))
	mock.ExpectQuery(`SELECT id FROM post_targets WHERE id = $1 FOR UPDATE`).WithArgs(int64(200)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(200))
	mock.ExpectQuery(`SELECT id FROM posts WHERE id = $1 FOR UPDATE`).WithArgs(int64(100)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(100))
	mock.ExpectExec(
		`UPDATE post_targets
		 SET status = $1, platform_post_id = $2, error_message = $3, published_at = $4,
		     provider_state = $6, container_id = $7
		 WHERE id = $5
		   AND (status = $1 OR status NOT IN ('published', 'partially_published', 'failed', 'dlq'))`,
	).WithArgs(models.PostStatusPublished, "remote-123", "", &now, int64(200), "", "").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT status FROM post_targets WHERE post_id = $1 ORDER BY id ASC`).WithArgs(int64(100)).
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow(models.PostStatusPublished))
	mock.ExpectExec(`UPDATE posts SET status = $1 WHERE id = $2`).WithArgs(models.PostStatusPublished, int64(100)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

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

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT post_id FROM post_targets WHERE id = $1`).WithArgs(int64(999)).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectRollback()

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
		`SELECT id, workspace_id, title, caption, media_url, ingest_after, publish_at, status, privacy_level, default_privacy_level, created_at, upload_job_id, media_asset_id, storage_object_key, bucket
 FROM posts
	 WHERE ($1::bigint = 0 OR workspace_id = $1) AND id = $2`,
	).WithArgs(int64(0), int64(100)).
		WillReturnRows(sqlmock.NewRows(
			[]string{"id", "workspace_id", "title", "caption", "media_url", "ingest_after", "publish_at", "status", "privacy_level", "default_privacy_level", "created_at", "upload_job_id", "media_asset_id", "storage_object_key", "bucket"},
		).AddRow(100, 1, "scheduled", "cap", "url", now, now, models.PostStatusScheduled, "", "", time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC), nil, nil, nil, nil))

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
	now := time.Now()
	mock.ExpectQuery(
		`SELECT id, workspace_id, title, caption, media_url, ingest_after, publish_at, status, privacy_level, default_privacy_level, created_at, upload_job_id, media_asset_id, storage_object_key, bucket
 FROM posts
	 WHERE ($1::bigint = 0 OR workspace_id = $1) AND id = $2`,
	).WithArgs(int64(0), int64(1)).
		WillReturnRows(sqlmock.NewRows(
			[]string{"id", "workspace_id", "title", "caption", "media_url", "ingest_after", "publish_at", "status", "privacy_level", "default_privacy_level", "created_at", "upload_job_id", "media_asset_id", "storage_object_key", "bucket"},
		).AddRow(1, 1, "draft", "", "", now, nil, models.PostStatusDraft, "", "", now, nil, nil, nil, nil))

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
		`SELECT id, workspace_id, title, caption, media_url, ingest_after, publish_at, status, privacy_level, default_privacy_level, created_at, upload_job_id, media_asset_id, storage_object_key, bucket
 FROM posts
	 WHERE ($1::bigint = 0 OR workspace_id = $1) AND id = $2`,
	).WithArgs(int64(0), int64(999)).
		WillReturnError(sql.ErrNoRows)

	p, err := repo.FindByID(999)
	if err != nil {
		t.Fatalf("FindByID expected nil err for ErrNoRows, got %v", err)
	}
	if p != nil {
		t.Errorf("FindByID expected nil post, got %+v", p)
	}
}

// TestPostCreate_ZeroIngestAfter_AutoStampsBeforeBind is the regression
// guard for the latent production bug fixed by the post_repo.go::Create
// IsZero() gate. History: the prior binding unconditionally passed
// `post.IngestAfter` to $5. If a caller zero-initialised Post (the
// common case for the API layer that constructs &models.Post{} then
// sets only workspace_id/title/status), SQL received
// '0001-01-01 00:00:00 UTC' instead of NOW() — a footgun the
// docstring's stated rationale ("we pass an explicit NOW() here…")
// silently violated. The fix gates the binding: caller-supplied
// non-zero is honoured verbatim; caller-supplied zero is replaced
// with Go-side time.Now().UTC() so SQL NEVER receives the Go zero.
// The SQL column's NOT NULL DEFAULT NOW() remains as the safety-net
// path for direct-SQL writers (psql scripts, admin tooling).
//
// Branches exercised:
//  1. Zero IngestAfter in → a non-zero time gets bound (auto-stamp).
//  2. Caller-supplied non-zero IngestAfter in → that exact value
//     gets bound verbatim (override-respecting).
func TestPostCreate_ZeroIngestAfter_AutoStampsBeforeBind(t *testing.T) {
	// ── Branch 1: zero IngestAfter → auto-stamp path ───────────────────
	// Caller passes &models.Post{} with no IngestAfter set (the API-layer
	// default construction pattern). The gate must rewrite post.IngestAfter
	// to a non-zero time.Now().UTC() BEFORE the SQL bind, so sqlmock
	// receives non-zero and the row NEVER lands as the Go zero value.
	t.Run("zero_ingest_after_is_auto_stamped", func(t *testing.T) {
		db, mock := newMockPostDBExact(t)
		repo := repository.NewPostRepository(db)
		before := time.Now().UTC()
		now := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)

		mock.ExpectBegin()
		mock.ExpectQuery(
			`INSERT INTO posts (workspace_id, title, caption, media_url, ingest_after, publish_at, default_privacy_level, privacy_level, status, upload_job_id, media_asset_id, storage_object_key, bucket)
 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
 ON CONFLICT (upload_job_id) WHERE upload_job_id IS NOT NULL DO NOTHING
 RETURNING id, created_at, upload_job_id`,
		// sqlmock.AnyArg at position $5 is sufficient for the auto-stamp
		// branch: the production code's `if post.IngestAfter.IsZero()`
		// gate rewrites $5 to a non-zero time.Time before sqlmock sees
		// it, so the bracket assertion below catches the zero regression.
		// A wrong-TYPE regression (e.g. future bug sends string "now")
		// would still slip past AnyArg; the type assertion in the
		// post-call bracket (post.IngestAfter, Location, etc.) catches
		// that too because the bind result is whatever the gate stamped.
		).WithArgs(int64(1), "auto-stamp", "", "", sqlmock.AnyArg(), (*time.Time)(nil), "", "", models.PostStatusDraft, nil, sql.NullString{}, sql.NullString{}, sql.NullString{}).
			WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "upload_job_id"}).AddRow(100, now, nil))
		mock.ExpectCommit()

		post := &models.Post{
			WorkspaceID: 1, Title: "auto-stamp", Status: models.PostStatusDraft,
			// IngestAfter intentionally NOT set → zero value.
		}
		if err := repo.Create(post, nil); err != nil {
			t.Fatalf("Create: %v", err)
		}
		after := time.Now().UTC()

		if post.IngestAfter.IsZero() {
			t.Fatalf("post.IngestAfter zero after Create — gate failed to auto-stamp")
		}
		if post.IngestAfter.Before(before) || post.IngestAfter.After(after) {
			t.Errorf("post.IngestAfter=%v not bracketed by [%v, %v] (gate should stamp inside this window)",
				post.IngestAfter, before, after)
		}
		// UTC explicit check: a future regression that drops the
		// `.UTC()` from the gate would still produce a value inside
		// the [before, after] bracket (local and UTC happen to have
		// the same wall-clock value in this millisecond), so the
		// bracket alone wouldn't catch it. Asserting Location()
		// catches a silent `.Local()` regression loudly.
		if post.IngestAfter.Location() != time.UTC {
			t.Errorf("post.IngestAfter.Location=%v, want UTC (a future regression that drops the gate's `.UTC()` would slip past the bracket alone)",
				post.IngestAfter.Location())
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("unmet expectations: %v", err)
		}
	})

	// ── Branch 2: non-zero IngestAfter → override-respecting path ──────
	// Caller explicitly sets IngestAfter (e.g. the drive_batch crawler
	// that back-dates import timestamps). The gate MUST respect the
	// caller's value verbatim — no rewriting, no clamping.
	t.Run("explicit_ingest_after_is_honoured_verbatim", func(t *testing.T) {
		db, mock := newMockPostDBExact(t)
		repo := repository.NewPostRepository(db)
		explicitTime := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
		now := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)

		mock.ExpectBegin()
		mock.ExpectQuery(
			`INSERT INTO posts (workspace_id, title, caption, media_url, ingest_after, publish_at, default_privacy_level, privacy_level, status, upload_job_id, media_asset_id, storage_object_key, bucket)
 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
 ON CONFLICT (upload_job_id) WHERE upload_job_id IS NOT NULL DO NOTHING
 RETURNING id, created_at, upload_job_id`,
		).WithArgs(int64(2), "override", "", "", explicitTime, (*time.Time)(nil), "", "", models.PostStatusDraft, nil, sql.NullString{}, sql.NullString{}, sql.NullString{}).
			WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "upload_job_id"}).AddRow(200, now, nil))
		mock.ExpectCommit()

		post := &models.Post{
			WorkspaceID: 2, Title: "override", Status: models.PostStatusDraft,
			IngestAfter: explicitTime,
		}
		if err := repo.Create(post, nil); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if !post.IngestAfter.Equal(explicitTime) {
			t.Errorf("post.IngestAfter=%v, want verbatim %v (gate must NOT rewrite a non-zero value)",
				post.IngestAfter, explicitTime)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Errorf("unmet expectations: %v", err)
		}
	})
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
				`INSERT INTO posts (workspace_id, title, caption, media_url, ingest_after, publish_at, default_privacy_level, privacy_level, status, upload_job_id, media_asset_id, storage_object_key, bucket)
 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
 ON CONFLICT (upload_job_id) WHERE upload_job_id IS NOT NULL DO NOTHING
 RETURNING id, created_at, upload_job_id`,
			).WithArgs(int64(1), "title", "", "", sqlmock.AnyArg(), (*time.Time)(nil), "", "", models.PostStatusDraft, nil, sql.NullString{}, sql.NullString{}, sql.NullString{}).
				WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "upload_job_id"}).AddRow(postID, now, nil))
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
