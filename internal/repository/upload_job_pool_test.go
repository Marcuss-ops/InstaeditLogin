// Package repository — P1 worker-pool tests for upload_jobs.
//
// Covers the new worker-pool API surface:
//   - ClaimBatch (FOR-UPDATE-SKIP-LOCKED CTE) — happy / empty /
//     attempt_count increment / retry_wait visibility.
//   - Heartbeat — CAS against lease_owner keeps slow uploads alive.
//   - MarkRetry — transient failure routes through retry_wait.
//   - MarkDeadLetter — retry-budget-exhausted failure routes through
//     dead_letter (terminal).
//   - ReclaimExpiredLeases — stuck workers' leases get recovered.
//
// All tests use sqlmock + the canonical Postgres syntax. Each
// scenario mirrors a real branch the worker exercises in production
// so the contracts stay in lockstep with the SQL. The publish-pool
// surface (ClaimBatchForPublish / MarkIngested / AggregateByFolder /
// MarkCompleted / MarkFailed) lives in upload_job_publish_pool_test.go.
package repository

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

// =============================================================================
// P1 — ClaimBatch (CTE + FOR UPDATE SKIP LOCKED)
// =============================================================================

func TestClaimBatch_SQLContract(t *testing.T) {
	for _, want := range []string{"FOR UPDATE SKIP LOCKED", "status IN ('pending', 'retry_wait')", "COALESCE(next_attempt_at, NOW())", "LIMIT $1", "UPDATE upload_jobs"} {
		if !strings.Contains(SQLClaimBatch, want) {
			t.Errorf("SQLClaimBatch missing %q", want)
		}
	}
}

// TestClaimBatch_Happy verifies the canonical happy path: ClaimBatch
// returns up to `limit` rows transitioned from pending to 'leased'
// with the lease columns + attempt_count stamp. Mock returns two
// rows under UPDATE...FROM candidates...RETURNING (the CTE form).
func TestClaimBatch_Happy(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	repo := NewUploadJobRepository(db)
	ctx := context.Background()
	workerID := "test-worker-1"
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
		AddRow(101, 1, 1, "public_drive", "drive-file-1",
			nil, nil, "t1", "c1", []byte("[1,2]"), "leased", nil, nil, nil,
			time.Now(), nil, time.Now(), time.Now(),
			1, 8, nil, workerID, leaseUntil, time.Now(),
			0, nil, nil, 100, time.Now(), nil,
			nil, nil, nil, nil, nil, "", []byte(`{}`),
		).
		AddRow(102, 1, 1, "public_drive", "drive-file-2",
			nil, nil, "t2", "c2", []byte("[3,4]"), "leased", nil, nil, nil,
			time.Now(), nil, time.Now(), time.Now(),
			1, 8, nil, workerID, leaseUntil, time.Now(),
			0, nil, nil, 100, time.Now(), nil,
			nil, nil, nil, nil, nil, "", []byte(`{}`),
		)

	// Regex matches the distinguishing 'pending'|'retry_wait' filter so a
	// regression that swaps ClaimBatch's CTE filter to something else
	// (e.g. ClaimBatchForPublish's status='ready_to_publish') is caught.
	mock.ExpectQuery(regexp.QuoteMeta(`status IN ('pending', 'retry_wait')`)).
		WithArgs(2, workerID, sqlmock.AnyArg()).
		WillReturnRows(rows)

	jobs, err := repo.ClaimBatch(ctx, workerID, 2, lease)
	if err != nil {
		t.Fatalf("ClaimBatch: %v", err)
	}
	if len(jobs) != 2 {
		t.Fatalf("want 2 claimed jobs, got %d", len(jobs))
	}
	if jobs[0].Status != "leased" {
		t.Errorf("status: want leased, got %q", jobs[0].Status)
	}
	if jobs[0].AttemptCount != 1 {
		t.Errorf("attempt_count: want 1 after first claim, got %d", jobs[0].AttemptCount)
	}
	if jobs[0].LeaseOwner == nil || *jobs[0].LeaseOwner != workerID {
		t.Errorf("lease_owner: want %q, got %v", workerID, jobs[0].LeaseOwner)
	}
	if jobs[0].MaxAttempts != 8 {
		t.Errorf("max_attempts: want 8 (server default), got %d", jobs[0].MaxAttempts)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// TestClaimBatch_Empty verifies the queue-empty path. The CTE returns
// zero rows; ClaimBatch must surface ([]*UploadJob{}, nil) — NOT an
// error — because this is a normal "no work" case the worker treats
// as "sleep until next tick".
func TestClaimBatch_Empty(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	repo := NewUploadJobRepository(db)
	ctx := context.Background()

	// Regex matches the distinguishing 'pending'|'retry_wait' filter so a
	// regression that swaps ClaimBatch's CTE filter is caught.
	mock.ExpectQuery(regexp.QuoteMeta(`status IN ('pending', 'retry_wait')`)).
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

	jobs, err := repo.ClaimBatch(ctx, "test-worker-2", 4, 60*time.Second)
	if err != nil {
		t.Fatalf("ClaimBatch (empty): want nil err, got %v", err)
	}
	if len(jobs) != 0 {
		t.Fatalf("want 0 jobs, got %d", len(jobs))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// TestClaimBatch_RejectsEmptyWorkerID catches the foot-gun where a
// new worker pool forgets to set its workerID and ends up stamping
// rows with an empty lease_owner. The repo refuses to issue such a
// query so the bug fails loudly at the boundary.
func TestClaimBatch_RejectsEmptyWorkerID(t *testing.T) {
	db, _, _ := sqlmock.New()
	defer db.Close()

	repo := NewUploadJobRepository(db)
	_, err := repo.ClaimBatch(context.Background(), "", 4, 60*time.Second)
	if err == nil {
		t.Fatal("want error for empty workerID, got nil")
	}
}

// TestClaimBatch_RejectsNonPositiveLease ensures the lease duration
// is sane; a zero or negative lease would silently re-release every
// claim on the next tick.
func TestClaimBatch_RejectsNonPositiveLease(t *testing.T) {
	db, _, _ := sqlmock.New()
	defer db.Close()

	repo := NewUploadJobRepository(db)
	for _, lease := range []time.Duration{0, -1 * time.Second, -1 * time.Hour} {
		_, err := repo.ClaimBatch(context.Background(), "w", 4, lease)
		if err == nil {
			t.Errorf("lease=%s: want error, got nil", lease)
		}
	}
}

// TestClaimBatch_LimitZero returns nothing without hitting the DB.
// The worker tick loop checks limit > 0 itself; the repo is the
// safety net.
func TestClaimBatch_LimitZero(t *testing.T) {
	db, _, _ := sqlmock.New()
	defer db.Close()

	repo := NewUploadJobRepository(db)
	jobs, err := repo.ClaimBatch(context.Background(), "w", 0, 60*time.Second)
	if err != nil {
		t.Fatalf("ClaimBatch (limit=0): want nil err, got %v", err)
	}
	if jobs != nil {
		t.Fatalf("want nil jobs, got %v", jobs)
	}
}

// =============================================================================
// P1 — Heartbeat (CAS against lease_owner)
// =============================================================================

// TestHeartbeat_Happy verifies the canonical CAS: lease_owner matches
// + status='leased' → row updates lease_expires_at + heartbeat_at.
func TestHeartbeat_Happy(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	repo := NewUploadJobRepository(db)
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE upload_jobs
         SET lease_expires_at = $1,
             heartbeat_at     = NOW()`)).
		WithArgs(sqlmock.AnyArg(), int64(101), "test-worker-1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := repo.Heartbeat(context.Background(), 101, "test-worker-1", 60*time.Second); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// TestHeartbeat_LeaseLost covers the late-delivery race: the worker's
// lease expired and the reaper already released the row, so the
// CAS WHERE clause matches 0 rows and the repo surfaces
// ErrUploadJobLeaseLost (wrapped with row context). The worker drops
// the in-flight work and lets ClaimBatch re-queue if budget remains.
func TestHeartbeat_LeaseLost(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	repo := NewUploadJobRepository(db)
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE upload_jobs`)).
		WillReturnResult(sqlmock.NewResult(0, 0))

	err = repo.Heartbeat(context.Background(), 101, "wrong-worker", 60*time.Second)
	if !errors.Is(err, ErrUploadJobLeaseLost) {
		t.Fatalf("want ErrUploadJobLeaseLost, got %v", err)
	}
}

// =============================================================================
// P1 — MarkRetry (transient failure routing)
// =============================================================================

// TestMarkRetry_Happy transitions leased → retry_wait with the error
// taxonomy + next_attempt_at; CAS against lease_owner keeps a late
// delivery from overwriting a peer's writes.
func TestMarkRetry_Happy(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	repo := NewUploadJobRepository(db)
	nextAttempt := time.Now().Add(30 * time.Second)

	mock.ExpectExec(regexp.QuoteMeta(`UPDATE upload_jobs
         SET status           = 'retry_wait'`)).
		WithArgs(int64(101), "transient s3 error", "s3_error", nextAttempt, "test-worker-1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = repo.MarkRetry(context.Background(), 101, "test-worker-1", "s3_error", "transient s3 error", nextAttempt)
	if err != nil {
		t.Fatalf("MarkRetry: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// TestMarkRetry_EmptyErrorCode stores SQL NULL via the Go-side nil
// wrapping; the column is not constrained but the convention is
// "taxonomy present iff classified".
func TestMarkRetry_EmptyErrorCode(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	repo := NewUploadJobRepository(db)
	nextAttempt := time.Now().Add(30 * time.Second)

	mock.ExpectExec(regexp.QuoteMeta(`UPDATE upload_jobs`)).
		WithArgs(int64(101), "unclassified error", nil, nextAttempt, "test-worker-1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := repo.MarkRetry(context.Background(), 101, "test-worker-1", "", "unclassified error", nextAttempt); err != nil {
		t.Fatalf("MarkRetry (empty code): %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// TestMarkRetry_LeaseLost covers CAS mismatch (peer stole the row).
func TestMarkRetry_LeaseLost(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	repo := NewUploadJobRepository(db)
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE upload_jobs`)).
		WillReturnResult(sqlmock.NewResult(0, 0))

	err = repo.MarkRetry(context.Background(), 101, "wrong-worker", "x", "y", time.Now())
	if !errors.Is(err, ErrUploadJobLeaseLost) {
		t.Fatalf("want ErrUploadJobLeaseLost, got %v", err)
	}
}

// =============================================================================
// P1 — MarkDeadLetter (retry-budget-exhausted terminal)
// =============================================================================

// TestMarkDeadLetter_Happy transitions leased → dead_letter with
// completed_at = NOW(). The CTA + surface area mirrors MarkFailed,
// so the same CAS guarantees apply.
func TestMarkDeadLetter_Happy(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	repo := NewUploadJobRepository(db)
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE upload_jobs
         SET status           = 'dead_letter'`)).
		WithArgs(int64(101), "youtube_quotaExceeded", "youtube_quotaExceeded", "test-worker-1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := repo.MarkDeadLetter(context.Background(), 101, "test-worker-1", "youtube_quotaExceeded", "youtube_quotaExceeded"); err != nil {
		t.Fatalf("MarkDeadLetter: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// TestMarkDeadLetter_LeaseLost covers CAS mismatch.
func TestMarkDeadLetter_LeaseLost(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	repo := NewUploadJobRepository(db)
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE upload_jobs`)).
		WillReturnResult(sqlmock.NewResult(0, 0))

	err = repo.MarkDeadLetter(context.Background(), 101, "wrong-worker", "x", "y")
	if !errors.Is(err, ErrUploadJobLeaseLost) {
		t.Fatalf("want ErrUploadJobLeaseLost, got %v", err)
	}
}

// =============================================================================
// P1 — ReclaimExpiredLeases (crashed-worker recovery)
// =============================================================================

// TestReclaimExpiredLeases_Happy verifies the recoverer's happy path:
// lease_expires_at < NOW() AND heartbeat_at older than the grace
// window AND status='leased' → flipped back to 'pending' with the
// lease columns cleared + error_code marked 'lease_expired' (when
// no prior taxonomy was set).
func TestReclaimExpiredLeases_Happy(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	repo := NewUploadJobRepository(db)
	mock.ExpectExec(regexp.QuoteMeta(`WITH expired AS`)).
		WithArgs(100).
		WillReturnResult(sqlmock.NewResult(0, 3))

	n, err := repo.ReclaimExpiredLeases(context.Background(), 100)
	if err != nil {
		t.Fatalf("ReclaimExpiredLeases: %v", err)
	}
	if n != 3 {
		t.Errorf("reclaimed: want 3, got %d", n)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// TestReclaimExpiredLeases_DefaultLimit verifies the repo applies a
// sane default when the caller passes 0 / negative (the worker tick
// loop forgets to set it).
func TestReclaimExpiredLeases_DefaultLimit(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	repo := NewUploadJobRepository(db)
	mock.ExpectExec(regexp.QuoteMeta(`WITH expired AS`)).
		WithArgs(100). // default
		WillReturnResult(sqlmock.NewResult(0, 0))

	if _, err := repo.ReclaimExpiredLeases(context.Background(), 0); err != nil {
		t.Fatalf("ReclaimExpiredLeases (default): %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
