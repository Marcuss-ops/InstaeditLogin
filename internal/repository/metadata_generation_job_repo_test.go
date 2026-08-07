package repository

import (
	"encoding/json"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// newMetadataGenRepoMockDB wires a sqlmock-backed *sql.DB into the
// MetadataGenerationJobRepository.
func newMetadataGenRepoMockDB(t *testing.T) (*MetadataGenerationJobRepository, sqlmock.Sqlmock, func()) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	return NewMetadataGenerationJobRepository(db), mock, func() { _ = db.Close() }
}

// metadataGenJobRow returns a complete column fixture in the order the
// repo scans. completed_at uses a real time.Time so the NullTime scan
// path (the fix for the NullString+RFC3339 parse bug) is exercised.
func metadataGenJobRow(id int64, status string, result string) []driverValue {
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	locked := time.Date(2026, 8, 7, 12, 1, 0, 0, time.UTC)
	return []driverValue{
		id, int64(12), "ve_test", "boxing tutorial", status,
		mgjNullableString(result), "last error", 1, 3,
		mgjNullableTime(now.Add(time.Minute)), "lease-1", mgjNullableTime(locked),
		now, now, mgjNullableTime(now.Add(2 * time.Minute)),
	}
}

// driverValue is any value sqlmock can hand to Scan.
type driverValue = interface{}

func mgjNullableString(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

func mgjNullableTime(t time.Time) interface{} {
	if t.IsZero() {
		return nil
	}
	return t
}

// TestMetadataGenRepo_Create assigns id + created_at from RETURNING and
// defaults status/attempts.
func TestMetadataGenRepo_Create(t *testing.T) {
	repo, mock, cleanup := newMetadataGenRepoMockDB(t)
	defer cleanup()

	mock.ExpectQuery(`INSERT INTO metadata_generation_jobs`).
		WithArgs(int64(12), "ve_test", "my prompt", 3).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).
			AddRow(int64(42), time.Now()))

	job := &models.MetadataGenerationJob{WorkspaceID: 12, VeloxProjectID: "ve_test", Prompt: "my prompt"}
	if err := repo.Create(job); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if job.ID != 42 {
		t.Errorf("assigned id: want 42, got %d", job.ID)
	}
	if job.Status != models.MetadataGenJobQueued || job.MaxAttempts != 3 {
		t.Errorf("defaults: status=%q max_attempts=%d", job.Status, job.MaxAttempts)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// TestMetadataGenRepo_FindByID_ScansTimestamps exercises the full
// column scan with non-NULL TIMESTAMPTZ values — the regression lock
// for the NullString+RFC3339Nano parse bug (a zero locked_at would
// have made ReclaimExpired reclaim an active row instantly).
func TestMetadataGenRepo_FindByID_ScansTimestamps(t *testing.T) {
	repo, mock, cleanup := newMetadataGenRepoMockDB(t)
	defer cleanup()

	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	mock.ExpectQuery(`SELECT id, workspace_id`).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "workspace_id", "velox_project_id", "prompt", "status", "result",
			"error_message", "attempt_count", "max_attempts", "next_attempt_at",
			"locked_by", "locked_at", "created_at", "updated_at", "completed_at",
		}).AddRow(
			7, 12, "ve_test", "prompt", "completed", `{"title":"T"}`,
			"", 1, 3, now.Add(time.Minute),
			"lease-1", now, now, now, now.Add(2*time.Minute),
		))

	job, err := repo.FindByID(7)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if job == nil {
		t.Fatal("FindByID returned nil job")
	}
	if job.Status != "completed" {
		t.Errorf("status: want completed, got %q", job.Status)
	}
	if string(job.Result) != `{"title":"T"}` {
		t.Errorf("result: want %s, got %s", `{"title":"T"}`, job.Result)
	}
	if job.CompletedAt == nil || !job.CompletedAt.Equal(now.Add(2*time.Minute)) {
		t.Errorf("completed_at: want %v, got %v (zero time = the NullString scan bug)", now.Add(2*time.Minute), job.CompletedAt)
	}
	if job.LockedAt == nil || !job.LockedAt.Equal(now) {
		t.Errorf("locked_at: want %v, got %v", now, job.LockedAt)
	}
	if job.NextAttemptAt == nil || !job.NextAttemptAt.Equal(now.Add(time.Minute)) {
		t.Errorf("next_attempt_at: want %v, got %v", now.Add(time.Minute), job.NextAttemptAt)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// TestMetadataGenRepo_FindByID_NotFound returns (nil, nil).
func TestMetadataGenRepo_FindByID_NotFound(t *testing.T) {
	repo, mock, cleanup := newMetadataGenRepoMockDB(t)
	defer cleanup()

	mock.ExpectQuery(`SELECT id, workspace_id`).
		WithArgs(int64(999)).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "workspace_id", "velox_project_id", "prompt", "status", "result",
			"error_message", "attempt_count", "max_attempts", "next_attempt_at",
			"locked_by", "locked_at", "created_at", "updated_at", "completed_at",
		}))

	job, err := repo.FindByID(999)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if job != nil {
		t.Errorf("want (nil, nil), got %+v", job)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// TestMetadataGenRepo_ClaimNext_HappyPath: claims a queued row via
// SKIP LOCKED and flips it to processing with the lease.
func TestMetadataGenRepo_ClaimNext_HappyPath(t *testing.T) {
	repo, mock, cleanup := newMetadataGenRepoMockDB(t)
	defer cleanup()

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT id, workspace_id`).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "workspace_id", "velox_project_id", "prompt", "status", "result",
			"error_message", "attempt_count", "max_attempts", "next_attempt_at",
			"locked_by", "locked_at", "created_at", "updated_at", "completed_at",
		}).AddRow(
			3, 12, "ve_test", "prompt", "queued", nil,
			"", 0, 3, nil,
			"", nil, time.Now(), time.Now(), nil,
		))
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE metadata_generation_jobs
		 SET status = 'processing', locked_by = $1, locked_at = $2, updated_at = $2
		 WHERE id = $3 AND status = 'queued'`)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	job, err := repo.ClaimNext("lease-abc", 5*time.Minute)
	if err != nil {
		t.Fatalf("ClaimNext: %v", err)
	}
	if job.ID != 3 {
		t.Errorf("id: want 3, got %d", job.ID)
	}
	if job.Status != models.MetadataGenJobProcessing {
		t.Errorf("status: want processing, got %q", job.Status)
	}
	if job.LockedBy != "lease-abc" {
		t.Errorf("locked_by: want lease-abc, got %q", job.LockedBy)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// TestMetadataGenRepo_ClaimNext_EmptyQueue: no pending row →
// ErrMetadataGenAlreadyClaimed (the worker sleeps until the next tick).
func TestMetadataGenRepo_ClaimNext_EmptyQueue(t *testing.T) {
	repo, mock, cleanup := newMetadataGenRepoMockDB(t)
	defer cleanup()

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT id, workspace_id`).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "workspace_id", "velox_project_id", "prompt", "status", "result",
			"error_message", "attempt_count", "max_attempts", "next_attempt_at",
			"locked_by", "locked_at", "created_at", "updated_at", "completed_at",
		}))
	mock.ExpectRollback()

	job, err := repo.ClaimNext("lease-abc", 5*time.Minute)
	if !errors.Is(err, ErrMetadataGenAlreadyClaimed) {
		t.Fatalf("want ErrMetadataGenAlreadyClaimed, got %v (job=%v)", err, job)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// TestMetadataGenRepo_MarkFailed_Terminal: attempt_count reaches
// max_attempts → status 'failed', no requeue.
func TestMetadataGenRepo_MarkFailed_Terminal(t *testing.T) {
	repo, mock, cleanup := newMetadataGenRepoMockDB(t)
	defer cleanup()

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT attempt_count, max_attempts`).
		WithArgs(int64(9), "lease-1").
		WillReturnRows(sqlmock.NewRows([]string{"attempt_count", "max_attempts"}).AddRow(2, 3))
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE metadata_generation_jobs
		 SET status = 'failed', error_message = $1, attempt_count = $2,
		     locked_by = '', locked_at = NULL, completed_at = NOW(), updated_at = NOW()
		 WHERE id = $3 AND locked_by = $4`)).
		WithArgs("boom", 3, int64(9), "lease-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if err := repo.MarkFailed(9, "lease-1", "boom", nil); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// TestMetadataGenRepo_MarkFailed_RequeuesWithBackoff: attempts left →
// back to 'queued' with next_attempt_at = now + backoff.
func TestMetadataGenRepo_MarkFailed_RequeuesWithBackoff(t *testing.T) {
	repo, mock, cleanup := newMetadataGenRepoMockDB(t)
	defer cleanup()

	backoff := 10 * time.Second
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT attempt_count, max_attempts`).
		WithArgs(int64(5), "lease-1").
		WillReturnRows(sqlmock.NewRows([]string{"attempt_count", "max_attempts"}).AddRow(0, 3))
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE metadata_generation_jobs
		 SET status = 'queued', error_message = $1, attempt_count = $2,
		     next_attempt_at = $3, locked_by = '', locked_at = NULL, updated_at = NOW()
		 WHERE id = $4 AND locked_by = $5`)).
		WithArgs("transient", 1, sqlmock.AnyArg(), int64(5), "lease-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if err := repo.MarkFailed(5, "lease-1", "transient", &backoff); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// TestMetadataGenRepo_MarkFailed_Gone: the row is not owned by this
// lease → ErrMetadataGenGone.
func TestMetadataGenRepo_MarkFailed_Gone(t *testing.T) {
	repo, mock, cleanup := newMetadataGenRepoMockDB(t)
	defer cleanup()

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT attempt_count, max_attempts`).
		WithArgs(int64(5), "lease-other").
		WillReturnRows(sqlmock.NewRows([]string{"attempt_count", "max_attempts"}))
	mock.ExpectRollback()

	err := repo.MarkFailed(5, "lease-other", "x", nil)
	if !errors.Is(err, ErrMetadataGenGone) {
		t.Fatalf("want ErrMetadataGenGone, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// TestMetadataGenRepo_ReclaimExpired: stale processing rows are reset
// to queued (interval-typed WHERE clause).
func TestMetadataGenRepo_ReclaimExpired(t *testing.T) {
	repo, mock, cleanup := newMetadataGenRepoMockDB(t)
	defer cleanup()

	mock.ExpectExec(regexp.QuoteMeta(`UPDATE metadata_generation_jobs
		 SET status = 'queued', locked_by = '', locked_at = NULL,
		     next_attempt_at = NOW(), updated_at = NOW()
		 WHERE status = 'processing'
		   AND locked_at < NOW() - $1::interval`)).
		WithArgs("300.000000 seconds").
		WillReturnResult(sqlmock.NewResult(0, 2))

	n, err := repo.ReclaimExpired(5 * time.Minute)
	if err != nil {
		t.Fatalf("ReclaimExpired: %v", err)
	}
	if n != 2 {
		t.Errorf("reclaimed: want 2, got %d", n)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// TestMetadataGenRepo_ResultJSONRoundtrip guards the json.RawMessage
// column: a completed job's result must survive DB → model unaltered.
func TestMetadataGenRepo_ResultJSONRoundtrip(t *testing.T) {
	raw := json.RawMessage(`{"title":"T","tags":["a","b"]}`)
	job := &models.MetadataGenerationJob{Result: raw}
	if string(job.Result) != `{"title":"T","tags":["a","b"]}` {
		t.Errorf("rawmessage mismatch: %s", job.Result)
	}
}
