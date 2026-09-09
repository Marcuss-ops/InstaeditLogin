// Package repository — publish-pool tests for upload_jobs.
//
// Covers the publish-side API surface (P1 step 2):
//   - ClaimBatchForPublish (ready_to_publish → leased).
//   - MarkIngested (leased → ingest_completed stamps).
//   - MarkCompleted / MarkFailed terminal transitions.
//   - AggregateByFolder status counters.
//
// The ingest-pool lifecycle (ClaimBatch / Heartbeat / MarkRetry /
// MarkDeadLetter / ReclaimExpiredLeases) lives in
// upload_job_pool_test.go.
package repository

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

// =============================================================================
// P1 — MarkCompleted/MarkFailed happy + lease-loss paths
// =============================================================================

// TestMarkCompleted_Happy verifies the canonical happy terminal:
// post_id + asset_id stamped, status = completed, lease cleared,
// completed_at = NOW(), CAS against lease_owner.
func TestMarkCompleted_Happy(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	repo := NewUploadJobRepository(db)
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE upload_jobs`)).
		WithArgs(int64(101), int64(42), "asset-abc", "test-worker-1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := repo.MarkCompleted(context.Background(), 101, "test-worker-1", 42, "asset-abc"); err != nil {
		t.Fatalf("MarkCompleted: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// TestMarkCompleted_LeaseLost covers the late-delivery race.
func TestMarkCompleted_LeaseLost(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	repo := NewUploadJobRepository(db)
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE upload_jobs`)).
		WillReturnResult(sqlmock.NewResult(0, 0))

	err = repo.MarkCompleted(context.Background(), 101, "wrong-worker", 42, "asset-abc")
	if !errors.Is(err, ErrUploadJobLeaseLost) {
		t.Fatalf("want ErrUploadJobLeaseLost, got %v", err)
	}
}

// TestMarkFailed_Happy verifies the terminal-fail path used for
// non-retryable errors (4xx-classified, schema mismatch, etc).
func TestMarkFailed_Happy(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	repo := NewUploadJobRepository(db)
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE upload_jobs`)).
		WithArgs(int64(101), "drive 404", "drive_404", "test-worker-1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := repo.MarkFailed(context.Background(), 101, "test-worker-1", "drive_404", "drive 404"); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// TestMarkFailed_EmptyErrorCode verifies the NULLIF maps an empty
// error_code to SQL NULL on disk (taxonomy present iff classified).
func TestMarkFailed_EmptyErrorCode(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	repo := NewUploadJobRepository(db)
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE upload_jobs`)).
		WithArgs(int64(101), "unknown", "", "test-worker-1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := repo.MarkFailed(context.Background(), 101, "test-worker-1", "", "unknown"); err != nil {
		t.Fatalf("MarkFailed (empty code): %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// TestMarkFailed_LeaseLost covers CAS mismatch.
func TestMarkFailed_LeaseLost(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	repo := NewUploadJobRepository(db)
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE upload_jobs`)).
		WillReturnResult(sqlmock.NewResult(0, 0))

	err = repo.MarkFailed(context.Background(), 101, "wrong-worker", "x", "y")
	if !errors.Is(err, ErrUploadJobLeaseLost) {
		t.Fatalf("want ErrUploadJobLeaseLost, got %v", err)
	}
}

// =============================================================================
// P1 — AggregateByFolder now reports the 4 new states
// =============================================================================

// TestAggregateByFolder_NewStates verifies the COUNT FILTER list now
// includes leased / retry_wait / dead_letter / cancelled alongside
// the original pending / processing / completed / failed. Without
// these terms, the dashboard's new status badges (P1 UX) would
// silently read 0 even when rows exist in those states.
func TestAggregateByFolder_NewStates(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	repo := NewUploadJobRepository(db)
	mock.ExpectQuery(regexp.QuoteMeta(`COUNT(*) FILTER (WHERE status = 'retry_wait')`)).
		WithArgs("fid", int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{
			"pending_count", "retry_wait_count", "leased_count",
			"processing_count", "ready_to_publish_count", "completed_count", "failed_count",
			"dead_letter_count", "cancelled_count",
			"first_publish_at", "last_publish_at",
		}).AddRow(2, 1, 0, 0, 0, 5, 1, 0, 0, nil, nil))

	summary, err := repo.AggregateByFolder("fid", 42)
	if err != nil {
		t.Fatalf("AggregateByFolder: %v", err)
	}
	if summary.PendingCount != 2 || summary.RetryWaitCount != 1 ||
		summary.CompletedCount != 5 || summary.FailedCount != 1 {
		t.Errorf("counts mismatch: %+v", summary)
	}
	if summary.TotalCount != 9 {
		t.Errorf("total: want 9 (all 8 states + 0 leased); got %d", summary.TotalCount)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// =============================================================================
// P1 step 2 — ClaimBatchForPublish (ready_to_publish → leased) + MarkIngested
// =============================================================================

// TestClaimBatchForPublish_Happy verifies the upload-pool counterpart
// to ClaimBatch: claims up to `limit` rows whose status =
// 'ready_to_publish' (the ingest pool's MarkIngested output).
// Mock the canonical CTE+UPDATE-FROM+RETURNING form scoped to the
// ready_to_publish filter; lease_owner is the pool's "upload-..."
// workerID.
func TestClaimBatchForPublish_Happy(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	repo := NewUploadJobRepository(db)
	ctx := context.Background()
	workerID := "upload-test-1"
	lease := 60 * time.Second
	leaseUntil := time.Now().Add(lease)

	rows := sqlmock.NewRows([]string{
		"id", "user_id", "workspace_id", "source_type", "source_id",
		"drive_account_id", "folder_id", "title", "caption",
		"targets", "status", "error_message", "post_id", "asset_id",
		"ingest_after", "publish_at", "created_at", "updated_at",
		"attempt_count", "max_attempts", "next_attempt_at",
		"lease_owner", "lease_expires_at", "heartbeat_at",
		"progress_bytes", "total_bytes", "error_code", "priority",
		"started_at", "completed_at",
		"youtube_session_uri", "youtube_session_offset", "youtube_session_expires_at", "youtube_chunk_size", "youtube_last_chunk_at",
		"default_privacy_level", "metadata",
	}).
		AddRow(201, 1, 1, "public_drive", "drive-file-201",
			nil, nil, "t201", "c201", []byte("[1,2]"), "leased", nil, nil, "asset-201",
			time.Now(), nil, time.Now(), time.Now(),
			3, 8, nil, workerID, leaseUntil, time.Now(),
			100, 100, nil, 100, time.Now(), nil,
			nil, nil, nil, nil, nil, "", []byte(`{}`),
		)

	// Regex matches the distinguishing ready_to_publish filter so a
	// regression that swaps ClaimBatchForPublish's CTE filter to
	// ClaimBatch's 'pending'|'retry_wait' is caught.
	mock.ExpectQuery(regexp.QuoteMeta(`status = 'ingest_completed'`)).
		WithArgs(1, workerID, sqlmock.AnyArg()).
		WillReturnRows(rows)

	jobs, err := repo.ClaimBatchForPublish(ctx, workerID, 1, lease)
	if err != nil {
		t.Fatalf("ClaimBatchForPublish: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("want 1 job, got %d", len(jobs))
	}
	if jobs[0].Status != "leased" {
		t.Errorf("status: want leased, got %q", jobs[0].Status)
	}
	if jobs[0].LeaseOwner == nil || *jobs[0].LeaseOwner != workerID {
		t.Errorf("lease_owner: want %q, got %v", workerID, jobs[0].LeaseOwner)
	}
	if jobs[0].AssetID == nil || *jobs[0].AssetID != "asset-201" {
		t.Errorf("asset_id: want asset-201, got %v", jobs[0].AssetID)
	}
	if jobs[0].AttemptCount != 3 {
		t.Errorf("attempt_count: want 3 (post-claim increment from prior 2), got %d", jobs[0].AttemptCount)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// TestClaimBatchForPublish_Empty verifies the queue-empty path for
// the upload pool.
func TestClaimBatchForPublish_Empty(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	repo := NewUploadJobRepository(db)
	// Regex matches the distinguishing ready_to_publish filter.
	mock.ExpectQuery(regexp.QuoteMeta(`status = 'ingest_completed'`)).
		WithArgs(4, sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "user_id", "workspace_id", "source_type", "source_id",
			"drive_account_id", "folder_id", "title", "caption",
			"targets", "status", "error_message", "post_id", "asset_id",
			"ingest_after", "publish_at", "created_at", "updated_at",
			"attempt_count", "max_attempts", "next_attempt_at",
			"lease_owner", "lease_expires_at", "heartbeat_at",
			"progress_bytes", "total_bytes", "error_code", "priority",
			"started_at", "completed_at",
			"youtube_session_uri", "youtube_session_offset", "youtube_session_expires_at", "youtube_chunk_size", "youtube_last_chunk_at",
			"default_privacy_level",
		}))

	jobs, err := repo.ClaimBatchForPublish(context.Background(), "upload-test", 4, 60*time.Second)
	if err != nil {
		t.Fatalf("ClaimBatchForPublish (empty): want nil err, got %v", err)
	}
	if len(jobs) != 0 {
		t.Fatalf("want 0 jobs, got %d", len(jobs))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// TestClaimBatchForPublish_ArgValidation enforces the same arg
// invariants ClaimBatch requires.
func TestClaimBatchForPublish_ArgValidation(t *testing.T) {
	db, _, _ := sqlmock.New()
	defer db.Close()

	repo := NewUploadJobRepository(db)
	if _, err := repo.ClaimBatchForPublish(context.Background(), "", 4, 60*time.Second); err == nil {
		t.Error("empty workerID: want error, got nil")
	}
	if _, err := repo.ClaimBatchForPublish(context.Background(), "w", 0, 60*time.Second); err != nil {
		t.Errorf("limit=0: want nil err (returns empty), got %v", err)
	}
	if _, err := repo.ClaimBatchForPublish(context.Background(), "w", 4, 0); err == nil {
		t.Error("non-positive lease: want error, got nil")
	}
}

// TestMarkIngested_Happy transitions leased → ready_to_publish with
// asset_id + total_bytes + progress_bytes stamps. The CAS against
// lease_owner keeps the late-delivery race safe.
func TestMarkIngested_Happy(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	repo := NewUploadJobRepository(db)
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE upload_jobs
         SET status           = 'ingest_completed'`)).
		WithArgs(int64(101), "asset-abc", int64(12345), "ingest-test-1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := repo.MarkIngested(context.Background(), 101, "ingest-test-1", "asset-abc", 12345); err != nil {
		t.Fatalf("MarkIngested: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// TestMarkIngested_RejectsEmptyArgs catches the foot-guns:
//
//   - empty assetID: the row would have nothing to transition on.
//   - empty workerID: a blank lease_owner CAS would match no rows.
func TestMarkIngested_RejectsEmptyArgs(t *testing.T) {
	db, _, _ := sqlmock.New()
	defer db.Close()

	repo := NewUploadJobRepository(db)
	if err := repo.MarkIngested(context.Background(), 101, "w", "", 1234); err == nil {
		t.Error("empty assetID: want error, got nil")
	}
	if err := repo.MarkIngested(context.Background(), 101, "", "asset-abc", 1234); err == nil {
		t.Error("empty workerID: want error, got nil")
	}
}

// TestMarkIngested_LeaseLost covers the late-delivery race for the
// ingest pool's transition: peer reaper already recovered the row.
func TestMarkIngested_LeaseLost(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	repo := NewUploadJobRepository(db)
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE upload_jobs`)).
		WillReturnResult(sqlmock.NewResult(0, 0))

	err = repo.MarkIngested(context.Background(), 101, "wrong-worker", "asset-abc", 1234)
	if !errors.Is(err, ErrUploadJobLeaseLost) {
		t.Fatalf("want ErrUploadJobLeaseLost, got %v", err)
	}
}

// Ensure unused import 'sql' is referenced via the helper; future
// maintenance hooks (BeginTx, Stmt, etc.) typically appear here.
var _ sql.IsolationLevel
