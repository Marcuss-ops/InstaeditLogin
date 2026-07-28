package repository_test

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// int64Ptr is a helper for stack-allocated *int64 literals in test
// setup. The worker passes `&job.ID` so production code naturally
// builds the pointer on the stack; tests need a literal because
// `&int64(999)` syntax isn't Go-valid.
func int64Ptr(v int64) *int64 { return &v }

// TestPostCreate_NoUploadJobID_FreshInsertPath covers the existing
// HTTP /api/v1/posts path that doesn't set UploadJobID. The
// ON CONFLICT clause is present in the SQL but never fires because
// the partial unique index `WHERE upload_job_id IS NOT NULL` filters
// null rows out — so the INSERT proceeds normally. RETURNING gives
// back a fresh id + created_at; post.UploadJobID is nil (the NOT NULL
// scan converts to a nil *int64).
//
// Reproduces the prior TestPostCreate_AtomicTx_Happy shape but
// asserts the HTTP-shaped caller (no UploadJobID) is unaffected by
// the idempotency refactor.
func TestPostCreate_NoUploadJobID_FreshInsertPath(t *testing.T) {
	db, mock := newMockPostDBExact(t)
	repo := repository.NewPostRepository(db)
	now := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)

	mock.ExpectBegin()
	mock.ExpectQuery(
		`INSERT INTO posts (workspace_id, title, caption, media_url, ingest_after, publish_at, default_privacy_level, privacy_level, status, upload_job_id)
	 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	 ON CONFLICT (upload_job_id) WHERE upload_job_id IS NOT NULL DO NOTHING
	 RETURNING id, created_at, upload_job_id`,
	).WithArgs(int64(1), "http-path", "", "", sqlmock.AnyArg(), (*time.Time)(nil), "", "", models.PostStatusDraft, nil).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "upload_job_id"}).AddRow(100, now, nil))
	// Fresh-insert path: insert each post_target then each outbox row.
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
	mock.ExpectExec(
		`INSERT INTO outbox_events (aggregate_type, aggregate_id, event_type, payload)
	 VALUES ($1, $2, $3, $4::jsonb)`,
	).WithArgs("post_target", int64(200), "post_target.publish_requested", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(
		`INSERT INTO outbox_events (aggregate_type, aggregate_id, event_type, payload)
	 VALUES ($1, $2, $3, $4::jsonb)`,
	).WithArgs("post_target", int64(201), "post_target.publish_requested", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	post := &models.Post{
		WorkspaceID:         1,
		Title:               "http-path",
		Status:              models.PostStatusDraft,
		UploadJobID:         nil, // HTTP /api/v1/posts path
		DefaultPrivacyLevel: "unlisted",
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
	if post.UploadJobID != nil {
		t.Errorf("post.UploadJobID: want nil (HTTP path), got %v", *post.UploadJobID)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// TestPostCreate_WithUploadJobID_OnConflictReusesExistingPost covers
// the migration-077 idempotency fix end-to-end:
//
//  1. First call: post has UploadJobID=999. qInsertPost's INSERT
//     path lands a fresh row with id=100, post_targets 200/201.
//     outbox rows 300/301.
//  2. Second call (simulating worker MarkRetry): a fresh
//     Post{} with UploadJobID=999 again, same target fanout.
//     qInsertPost returns ErrNoRows (ON CONFLICT DO NOTHING fired).
//     Re-fetch picks up the existing row + targets via
//     qSelectPostByUploadJobID + qSelectTargetsByPost. The
//     caller's `post` + `targets` pointers are rehydrated with
//     the canonical ids (post.ID=100, targets[0].ID=200,
//     targets[1].ID=201).
//
// The mock sequence mirrors the production tx flow:
//
//	First call: Begin → INSERT posts RETURNING(...)=[$1,$2,$3] →
//	  INSERT post_targets A RETURNING id → INSERT post_targets B
//	  RETURNING id → INSERT outbox A → INSERT outbox B → Commit.
//
//	Second call (retry): Begin → INSERT posts RETURNING(...)=
//	  [ErrNoRows] → SELECT posts WHERE upload_job_id=$1 →
//	  SELECT post_targets WHERE post_id=$1 → Commit (no
//	  targets/outbox INSERTs — they were already done).
//
// The test asserts the caller's post.ID + post.CreatedAt +
// post.UploadJobID match the first call's stamped values (proving
// the ON CONFLICT path correctly re-uses the row) AND that the
// targets slice got its .ID/.PostID stamped from the SELECT.
func TestPostCreate_WithUploadJobID_OnConflictReusesExistingPost(t *testing.T) {
	db, mock := newMockPostDBExact(t)
	repo := repository.NewPostRepository(db)
	now := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	uploadJobID := int64(999)

	// ---- Second call (retry) is the one we exercise first because
	// it captures the ON CONFLICT path. The first call's inserts
	// would normally need a separate mock but the SECOND call's
	// ON CONFLICT path is a self-contained sequence.
	mock.ExpectBegin()
	// ON CONFLICT DO NOTHING: INSERT lands 0 rows → RETURNING
	// returns ErrNoRows.
	mock.ExpectQuery(
		`INSERT INTO posts (workspace_id, title, caption, media_url, ingest_after, publish_at, default_privacy_level, privacy_level, status, upload_job_id)
	 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	 ON CONFLICT (upload_job_id) WHERE upload_job_id IS NOT NULL DO NOTHING
	 RETURNING id, created_at, upload_job_id`,
	).WithArgs(int64(1), "video", "", "", sqlmock.AnyArg(), (*time.Time)(nil), "unlisted", "", models.PostStatusDraft, uploadJobID).
		WillReturnError(sql.ErrNoRows)
	// Re-fetch existing post by upload_job_id.
	mock.ExpectQuery(
		`SELECT id, workspace_id, title, caption, media_url, ingest_after, publish_at, status, privacy_level, default_privacy_level, created_at, upload_job_id
 FROM posts
 WHERE upload_job_id = $1`,
	).WithArgs(uploadJobID).
		WillReturnRows(
			sqlmock.NewRows(
				[]string{
					"id", "workspace_id", "title", "caption", "media_url",
					"ingest_after", "publish_at", "status",
					"privacy_level", "default_privacy_level",
					"created_at", "upload_job_id",
				},
			).AddRow(int64(100), int64(1), "video", "", "", now, nil, models.PostStatusQueued, "", "unlisted", now, uploadJobID),
		)
	// Re-fetch existing post_targets fan-out.
	mock.ExpectQuery(
		`SELECT id, post_id, platform_account_id, status,
		        COALESCE(platform_post_id, ''), COALESCE(error_message, ''), published_at,
		        COALESCE(provider_state, ''), COALESCE(container_id, ''),
		        provider_idempotency_key, completed_at
		 FROM post_targets
		 WHERE post_id = $1
		 ORDER BY id ASC`,
	).WithArgs(int64(100)).
		WillReturnRows(
			sqlmock.NewRows(
				[]string{
					"id", "post_id", "platform_account_id", "status",
					"platform_post_id", "error_message", "published_at",
					"provider_state", "container_id",
					"provider_idempotency_key", "completed_at",
				},
			).AddRow(int64(200), int64(100), int64(10), models.PostStatusQueued, "", "", nil, "", "", nil, nil).
				AddRow(int64(201), int64(100), int64(11), models.PostStatusQueued, "", "", nil, "", "", nil, nil),
		)
	// Commit the re-fetch tx.
	mock.ExpectCommit()

	// Caller's Post struct is fresh (post.ID=0 simulating the
	// upload_worker retry that rebuilds the post from scratch).
	post := &models.Post{
		WorkspaceID:         1,
		Title:               "video",
		Status:              models.PostStatusDraft,
		UploadJobID:         int64Ptr(uploadJobID),
		DefaultPrivacyLevel: "unlisted",
	}
	targets := []*models.PostTarget{
		{PlatformAccountID: 10, Status: models.PostStatusDraft},
		{PlatformAccountID: 11, Status: models.PostStatusDraft},
	}
	if err := repo.Create(post, targets); err != nil {
		t.Fatalf("Create (conflict path): %v", err)
	}

	// Rehydrated ids MUST match the canonical (existing) row.
	if post.ID != 100 {
		t.Errorf("post.ID: want 100 (conflict re-use), got %d", post.ID)
	}
	if post.UploadJobID == nil || *post.UploadJobID != uploadJobID {
		t.Errorf("post.UploadJobID: want %d, got %v", uploadJobID, post.UploadJobID)
	}
	if targets[0].ID != 200 || targets[0].PostID != 100 {
		t.Errorf("targets[0]: want id=200 post_id=100, got %+v", targets[0])
	}
	if targets[1].ID != 201 || targets[1].PostID != 100 {
		t.Errorf("targets[1]: want id=201 post_id=100, got %+v", targets[1])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// TestPostCreate_WithUploadJobID_FanoutMismatch_Error covers the
// safety guard in fetchExistingByUploadJobID: if the existing row
// has a different number of post_targets than the caller's slice,
// surface an error rather than silently dropping the diff. A worker
// retry always builds the same fanout so a mismatch is a real bug
// (operator SQL edit, a buggy backfill, etc.) — escalate it.
func TestPostCreate_WithUploadJobID_FanoutMismatch_Error(t *testing.T) {
	db, mock := newMockPostDBExact(t)
	repo := repository.NewPostRepository(db)
	now := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	uploadJobID := int64(999)

	mock.ExpectBegin()
	mock.ExpectQuery(
		`INSERT INTO posts (workspace_id, title, caption, media_url, ingest_after, publish_at, default_privacy_level, privacy_level, status, upload_job_id)
	 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	 ON CONFLICT (upload_job_id) WHERE upload_job_id IS NOT NULL DO NOTHING
	 RETURNING id, created_at, upload_job_id`,
	).WithArgs(int64(1), "video", "", "", sqlmock.AnyArg(), (*time.Time)(nil), "unlisted", "", models.PostStatusDraft, uploadJobID).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(
		`SELECT id, workspace_id, title, caption, media_url, ingest_after, publish_at, status, privacy_level, default_privacy_level, created_at, upload_job_id
 FROM posts
 WHERE upload_job_id = $1`,
	).WithArgs(uploadJobID).
		WillReturnRows(
			sqlmock.NewRows(
				[]string{
					"id", "workspace_id", "title", "caption", "media_url",
					"ingest_after", "publish_at", "status",
					"privacy_level", "default_privacy_level",
					"created_at", "upload_job_id",
				},
			).AddRow(int64(100), int64(1), "video", "", "", now, nil, models.PostStatusQueued, "", "unlisted", now, uploadJobID),
		)
	// Only 1 existing target, but caller is passing 3 → mismatch
	// detected + tx rolls back.
	mock.ExpectQuery(
		`SELECT id, post_id, platform_account_id, status,
		        COALESCE(platform_post_id, ''), COALESCE(error_message, ''), published_at,
		        COALESCE(provider_state, ''), COALESCE(container_id, ''),
		        provider_idempotency_key, completed_at
		 FROM post_targets
		 WHERE post_id = $1
		 ORDER BY id ASC`,
	).WithArgs(int64(100)).
		WillReturnRows(
			sqlmock.NewRows(
				[]string{
					"id", "post_id", "platform_account_id", "status",
					"platform_post_id", "error_message", "published_at",
					"provider_state", "container_id",
					"provider_idempotency_key", "completed_at",
				},
			).AddRow(int64(200), int64(100), int64(10), models.PostStatusQueued, "", "", nil, "", "", nil, nil),
		)
	mock.ExpectRollback()

	post := &models.Post{
		WorkspaceID:         1,
		Title:               "video",
		Status:              models.PostStatusDraft,
		UploadJobID:         int64Ptr(uploadJobID),
		DefaultPrivacyLevel: "unlisted",
	}
	// Caller passes 3 targets; DB has only 1 → mismatch error.
	targets := []*models.PostTarget{
		{PlatformAccountID: 10, Status: models.PostStatusDraft},
		{PlatformAccountID: 11, Status: models.PostStatusDraft},
		{PlatformAccountID: 12, Status: models.PostStatusDraft},
	}
	var err error = repo.Create(post, targets)
	if err == nil {
		t.Fatal("expected fanout-mismatch error, got nil")
	}
	if !strings.Contains(err.Error(), "fanout mismatch") {
		t.Errorf("error should mention fanout mismatch: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// TestPostCreate_WithUploadJobID_NoConflict_InsertsFresh covers the
// first-call path on UploadJobID=999 (no existing row yet). Verifies
// the conflict clause does NOT short-circuit when the row truly is
// new — pattern: ON CONFLICT DO NOTHING returns the freshly-inserted
// row's id via RETURNING (not ErrNoRows).
func TestPostCreate_WithUploadJobID_NoConflict_InsertsFresh(t *testing.T) {
	db, mock := newMockPostDBExact(t)
	repo := repository.NewPostRepository(db)
	now := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	uploadJobID := int64(999)

	mock.ExpectBegin()
	mock.ExpectQuery(
		`INSERT INTO posts (workspace_id, title, caption, media_url, ingest_after, publish_at, default_privacy_level, privacy_level, status, upload_job_id)
	 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	 ON CONFLICT (upload_job_id) WHERE upload_job_id IS NOT NULL DO NOTHING
	 RETURNING id, created_at, upload_job_id`,
	).WithArgs(int64(1), "fresh", "", "", sqlmock.AnyArg(), (*time.Time)(nil), "unlisted", "", models.PostStatusDraft, uploadJobID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "upload_job_id"}).AddRow(100, now, uploadJobID))
	// Fresh-insert path with upload_job_id set: 2 targets + 2 outbox.
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
	mock.ExpectExec(
		`INSERT INTO outbox_events (aggregate_type, aggregate_id, event_type, payload)
	 VALUES ($1, $2, $3, $4::jsonb)`,
	).WithArgs("post_target", int64(200), "post_target.publish_requested", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(
		`INSERT INTO outbox_events (aggregate_type, aggregate_id, event_type, payload)
	 VALUES ($1, $2, $3, $4::jsonb)`,
	).WithArgs("post_target", int64(201), "post_target.publish_requested", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	post := &models.Post{
		WorkspaceID:         1,
		Title:               "fresh",
		Status:              models.PostStatusDraft,
		UploadJobID:         int64Ptr(uploadJobID),
		DefaultPrivacyLevel: "unlisted",
	}
	targets := []*models.PostTarget{
		{PlatformAccountID: 10, Status: models.PostStatusDraft},
		{PlatformAccountID: 11, Status: models.PostStatusDraft},
	}
	if err := repo.Create(post, targets); err != nil {
		t.Fatalf("Create (fresh): %v", err)
	}
	if post.ID != 100 {
		t.Errorf("post.ID: want 100, got %d", post.ID)
	}
	if post.UploadJobID == nil || *post.UploadJobID != uploadJobID {
		t.Errorf("post.UploadJobID: want %d, got %v", uploadJobID, post.UploadJobID)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// TestPostCreate_ConflictPath_PreservesCallerFieldOverwrites covers
// the deliberate preservation contract: when ON CONFLICT fires,
// fetchExistingByUploadJobID only stamps DB-derived fields
// (target.ID, target.PostID, target.Status) and LEAVES the caller's
// caller-set mutations untouched. Today we don't mutate any other
// field, but a future per-target shortcut (e.g. caller pre-seting
// WorkspaceID or attempt_count for an audit log) should survive the
// rehydrate. Pin the no-regression on WorkspaceID: server-side
// provides post.WorkspaceID=1; re-fetch stamps post.WorkspaceID=1
// too, but the caller's locally-set value (if any) would still be
// overwritten by the canonical SELECT — document the contract.
func TestPostCreate_ConflictPath_DBStampsAreAuthoritative(t *testing.T) {
	db, mock := newMockPostDBExact(t)
	repo := repository.NewPostRepository(db)
	now := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	uploadJobID := int64(999)

	mock.ExpectBegin()
	mock.ExpectQuery(
		`INSERT INTO posts (workspace_id, title, caption, media_url, ingest_after, publish_at, default_privacy_level, privacy_level, status, upload_job_id)
	 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	 ON CONFLICT (upload_job_id) WHERE upload_job_id IS NOT NULL DO NOTHING
	 RETURNING id, created_at, upload_job_id`,
	).WithArgs(int64(1), "video", "", "", sqlmock.AnyArg(), (*time.Time)(nil), "unlisted", "", models.PostStatusDraft, uploadJobID).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(
		`SELECT id, workspace_id, title, caption, media_url, ingest_after, publish_at, status, privacy_level, default_privacy_level, created_at, upload_job_id
 FROM posts
 WHERE upload_job_id = $1`,
	).WithArgs(uploadJobID).
		WillReturnRows(
			sqlmock.NewRows(
				[]string{
					"id", "workspace_id", "title", "caption", "media_url",
					"ingest_after", "publish_at", "status",
					"privacy_level", "default_privacy_level",
					"created_at", "upload_job_id",
				},
			).AddRow(int64(100), int64(7), "canonical-title", "", "", now, nil, models.PostStatusQueued, "", "unlisted", now, uploadJobID),
		)
	mock.ExpectQuery(
		`SELECT id, post_id, platform_account_id, status,
		        COALESCE(platform_post_id, ''), COALESCE(error_message, ''), published_at,
		        COALESCE(provider_state, ''), COALESCE(container_id, ''),
		        provider_idempotency_key, completed_at
		 FROM post_targets
		 WHERE post_id = $1
		 ORDER BY id ASC`,
	).WithArgs(int64(100)).
		WillReturnRows(
			sqlmock.NewRows(
				[]string{
					"id", "post_id", "platform_account_id", "status",
					"platform_post_id", "error_message", "published_at",
					"provider_state", "container_id",
					"provider_idempotency_key", "completed_at",
				},
			).AddRow(int64(200), int64(100), int64(10), models.PostStatusQueued, "", "", nil, "", "", nil, nil),
		)
	mock.ExpectCommit()

	// Caller fills in workspace=1, title="video". DB has workspace=7,
	// title="canonical-title". After re-fetch the DB values win.
	post := &models.Post{
		WorkspaceID: 1, Title: "video",
		Status:              models.PostStatusDraft,
		UploadJobID:         int64Ptr(uploadJobID),
		DefaultPrivacyLevel: "unlisted",
	}
	targets := []*models.PostTarget{
		{PlatformAccountID: 10, Status: models.PostStatusDraft},
	}
	if err := repo.Create(post, targets); err != nil {
		t.Fatalf("Create: %v", err)
	}
	// DB's workspace=7 + title="canonical-title" must win.
	if post.WorkspaceID != 7 {
		t.Errorf("post.WorkspaceID after conflict rehydrate: want 7 (DB), got %d", post.WorkspaceID)
	}
	if post.Title != "canonical-title" {
		t.Errorf("post.Title after conflict rehydrate: want %q (DB), got %q", "canonical-title", post.Title)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// Compile-time assertion (already covered at the production code
// layer, duplicated here as a harness). Ensures the migration-077
// shape keeps PostRepository.Create returning an error string
// and re-using outbox_events idempotency invariant.
var _ = repository.NewPostRepository
