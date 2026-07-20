package worker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// fakeUploadJobStoreForRouting is a minimal UploadJobStore stub that
// records how many times MarkRetry vs MarkDeadLetter were called.
// All other methods return zero values — handleProcessingError does
// not need them.
type fakeUploadJobStoreForRouting struct {
	markDeadLetterCalls atomic.Int32
	markRetryCalls      atomic.Int32
	lastDeadLetterArgs  struct {
		id        int64
		workerID  string
		errorCode string
		errMsg    string
	}
	lastRetryArgs struct {
		id        int64
		workerID  string
		errorCode string
		errMsg    string
		nextAt    time.Time
	}
}

func (f *fakeUploadJobStoreForRouting) ClaimBatch(context.Context, string, int, time.Duration) ([]*models.UploadJob, error) {
	return nil, nil
}
func (f *fakeUploadJobStoreForRouting) ClaimBatchForPublish(context.Context, string, int, time.Duration) ([]*models.UploadJob, error) {
	return nil, nil
}
func (f *fakeUploadJobStoreForRouting) Heartbeat(context.Context, int64, string, time.Duration) error {
	return nil
}
func (f *fakeUploadJobStoreForRouting) MarkCompleted(context.Context, int64, string, int64, string) error {
	return nil
}
func (f *fakeUploadJobStoreForRouting) MarkFailed(context.Context, int64, string, string, string) error {
	return nil
}
func (f *fakeUploadJobStoreForRouting) MarkIngested(context.Context, int64, string, string, int64) error {
	return nil
}
func (f *fakeUploadJobStoreForRouting) ReclaimExpiredLeases(context.Context, int) (int64, error) {
	return 0, nil
}
func (f *fakeUploadJobStoreForRouting) SaveYouTubeSession(context.Context, int64, string, string, int64, int64, time.Time) error {
	return nil
}
func (f *fakeUploadJobStoreForRouting) ClearYouTubeSession(context.Context, int64, string) error {
	return nil
}
func (f *fakeUploadJobStoreForRouting) MarkDeadLetter(_ context.Context, id int64, workerID, errorCode, errMessage string) error {
	f.markDeadLetterCalls.Add(1)
	f.lastDeadLetterArgs.id = id
	f.lastDeadLetterArgs.workerID = workerID
	f.lastDeadLetterArgs.errorCode = errorCode
	f.lastDeadLetterArgs.errMsg = errMessage
	return nil
}
func (f *fakeUploadJobStoreForRouting) MarkRetry(_ context.Context, id int64, workerID, errorCode, errMessage string, nextAttemptAt time.Time) error {
	f.markRetryCalls.Add(1)
	f.lastRetryArgs.id = id
	f.lastRetryArgs.workerID = workerID
	f.lastRetryArgs.errorCode = errorCode
	f.lastRetryArgs.errMsg = errMessage
	f.lastRetryArgs.nextAt = nextAttemptAt
	return nil
}

// TestHandleProcessingError_PermanentError_RoutesToDeadLetter — Task
// 5/10 acceptance test. The struct-typed PermError from
// AuthenticatedDriveSource.Inspect's canDownload=false reject path
// (or any PermanentError source) MUST bypass the attempt_count-based
// retry gate and route directly to MarkDeadLetter on the FIRST call.
//
// Without this routing, a single canDownload=false row would burn
// ~5 min × 8 attempts of wall-clock + DB log noise before
// dead-letter naturally kicks in via max_attempts exhaustion.
// Defense-in-depth: the test must be regression-tight — a future
// change that reorders handleProcessingError to put the
// attempt-count gate BEFORE the errors.Is(err, ErrPermanent) check
// trips this test on the FIRST failed tick.
func TestHandleProcessingError_PermanentError_RoutesToDeadLetter(t *testing.T) {
	store := &fakeUploadJobStoreForRouting{}
	w := &UploadWorker{
		jobRepo: store,
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	// Simulate the canDownload=false rejection produced by
	// AuthenticatedDriveSource.Inspect (errors.Join of the
	// ErrDriveNotDownloadable wrapper + PermanentError{Code:
	// CodeDriveNotDownloadable}). The exact same construction shape
	// the production code emits.
	processErr := errors.Join(
		fmt.Errorf("%w: file drive-file-123 (Drive reported capabilities.canDownload=false; check DLP rules / IRM / share-settings)",
			services.ErrDriveNotDownloadable),
		PermanentError{
			Code:    CodeDriveNotDownloadable,
			Message: "drive file drive-file-123 reported capabilities.canDownload=false; cannot be ingested",
		},
	)

	// attempt_count=1, max_attempts=8: the retry-gate branch of
	// handleProcessingError would normally MarkRetry. The
	// permanent-error branch MUST short-circuit to MarkDeadLetter
	// instead.
	job := &models.UploadJob{
		ID:           9001,
		AttemptCount: 1,
		MaxAttempts:  8,
	}
	w.handleProcessingError(context.Background(), "ingest", "ingest-test-worker", job, processErr)

	if got := store.markDeadLetterCalls.Load(); got != 1 {
		t.Fatalf("MarkDeadLetter was called %d times; want exactly 1 (permanent-error fast-path must short-circuit the retry budget)", got)
	}
	if got := store.markRetryCalls.Load(); got != 0 {
		t.Fatalf("MarkRetry was called %d times; want 0 (permanent error must not consume retry budget — reordering handleProcessingError to put attempt-count gate before errors.Is(err, ErrPermanent) trips this assertion)", got)
	}
	if store.lastDeadLetterArgs.id != job.ID {
		t.Errorf("MarkDeadLetter id = %d; want %d", store.lastDeadLetterArgs.id, job.ID)
	}
	if store.lastDeadLetterArgs.workerID != "ingest-test-worker" {
		t.Errorf("MarkDeadLetter workerID = %q; want %q", store.lastDeadLetterArgs.workerID, "ingest-test-worker")
	}
	// Code classification: the canDownload=false rejection's
	// message starts with the wrapped "%w: file %s (Drive
	// reported" pattern that classifyUploadError greps for the
	// "drive" needle → returns "drive_error". Locked here so a
	// regression in classifyUploadError (or the wrapper message
	// shape) surfaces in the test, not in the operator dashboard.
	if store.lastDeadLetterArgs.errorCode != "drive_error" {
		t.Errorf("MarkDeadLetter errorCode = %q; want %q (classifyUploadError must tag the canDownload=false rejection with the drive_error code so the dashboard filter key stays stable)",
			store.lastDeadLetterArgs.errorCode, "drive_error")
	}
	// The full Inspect error texture (including the PermanentError
	// Code prefix "DRIVE_NOT_DOWNLOADABLE:") MUST be persisted on
	// upload_jobs.error_message for the runbook reader
	// (docs/OPERATIONS.md §3.2) to grep on the literal Code.
	if !strings.Contains(store.lastDeadLetterArgs.errMsg, "DRIVE_NOT_DOWNLOADABLE") {
		t.Errorf("MarkDeadLetter errMsg = %q; must contain the canonical Code prefix %q for runbook grep compatibility",
			store.lastDeadLetterArgs.errMsg, "DRIVE_NOT_DOWNLOADABLE")
	}
}

// TestHandleProcessingError_TransientError_HonorsRetryBudget —
// negative control: a NON-permanent error (not wrapping ErrPermanent)
// MUST NOT be routed to MarkDeadLetter on the first tick when
// attempt_count < max_attempts. The retry gate stays active for
// genuinely transient failures (network blip, 5xx, etc.) so the row
// can retry instead of being dead-lettered prematurely.
//
// Without this negative control, a regression that flip-flops the
// permanent-error fast-path to ALSO match non-permanent errors would
// land transient failures in dead_letter and break the worker's
// retry budget semantics.
func TestHandleProcessingError_TransientError_HonorsRetryBudget(t *testing.T) {
	store := &fakeUploadJobStoreForRouting{}
	w := &UploadWorker{
		jobRepo: store,
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	// Plain error — does NOT wrap ErrPermanent. Mirrors the network
	// or S3 transient failure shape the worker sees on the happy
	// retry path.
	processErr := errors.New("transient s3 502 bad gateway")

	job := &models.UploadJob{
		ID:           9002,
		AttemptCount: 2,
		MaxAttempts:  8,
	}
	w.handleProcessingError(context.Background(), "ingest", "ingest-test-worker", job, processErr)

	if got := store.markDeadLetterCalls.Load(); got != 0 {
		t.Fatalf("MarkDeadLetter was called %d times for a transient error; want 0 (transient errors MUST use the retry gate, not the permanent fast-path)", got)
	}
	if got := store.markRetryCalls.Load(); got != 1 {
		t.Fatalf("MarkRetry was called %d times; want exactly 1 (transient error at attempt=2/max=8 must schedule a backoff retry)", got)
	}
	if store.lastRetryArgs.id != job.ID {
		t.Errorf("MarkRetry id = %d; want %d", store.lastRetryArgs.id, job.ID)
	}
	// nextAttemptAt must be in the future (not Now() — the worker
	// adds computeUploadBackoff).
	if !store.lastRetryArgs.nextAt.After(time.Now().Add(-1 * time.Second)) {
		t.Errorf("MarkRetry nextAttemptAt = %v; must be ahead of now (computeUploadBackoff must add jitter)", store.lastRetryArgs.nextAt)
	}
}

// TestHandleProcessingError_UploadLeaseLost_DropsSilently — the
// lease-lost path is unchanged by the Task 5/10 edit. Pinning it
// here keeps the regression-tight guard for the unchanged branch
// (errors.Is(err, repository.ErrUploadJobLeaseLost) → return, no
// Mark* call) and serves as a tripwire if a future change accidentally
// routes lease-loss into the dead-letter or retry branch.
func TestHandleProcessingError_UploadLeaseLost_DropsSilently(t *testing.T) {
	store := &fakeUploadJobStoreForRouting{}
	w := &UploadWorker{
		jobRepo: store,
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	processErr := repository.ErrUploadJobLeaseLost
	job := &models.UploadJob{ID: 9003, SourceType: models.UploadJobSourceAuthenticatedDrive}
	w.handleProcessingError(context.Background(), "ingest", "ingest-test-worker", job, processErr)

	if got := store.markDeadLetterCalls.Load(); got != 0 {
		t.Errorf("MarkDeadLetter was called %d times for lease-lost; want 0 (lease-lost must drop silently — the peer owns the row)", got)
	}
	if got := store.markRetryCalls.Load(); got != 0 {
		t.Errorf("MarkRetry was called %d times for lease-lost; want 0 (lease-lost must drop silently — the peer owns the row)", got)
	}
}

// contains helper REMOVED — using strings.Contains from the stdlib instead
// (smaller surface, less likely to bisect in a regression).
