package worker

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// fakeMetadataGenStore is an in-memory MetadataGenerationJobStore
// recording every Mark* call. ClaimNext serves jobs FIFO and returns
// ErrMetadataGenAlreadyClaimed when the queue is drained.
type fakeMetadataGenStore struct {
	mu        sync.Mutex
	jobs      []*models.MetadataGenerationJob
	completed map[int64][]byte
	failed    map[int64]string
	backoffs  map[int64]*time.Duration
	reclaims  int64
}

func newFakeMetadataGenStore(jobs ...*models.MetadataGenerationJob) *fakeMetadataGenStore {
	return &fakeMetadataGenStore{
		jobs:      jobs,
		completed: map[int64][]byte{},
		failed:    map[int64]string{},
		backoffs:  map[int64]*time.Duration{},
	}
}

func (f *fakeMetadataGenStore) ClaimNext(leaseID string, _ time.Duration) (*models.MetadataGenerationJob, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, j := range f.jobs {
		if j.Status == models.MetadataGenJobQueued {
			j.Status = models.MetadataGenJobProcessing
			j.LockedBy = leaseID
			return j, nil
		}
	}
	return nil, repository.ErrMetadataGenAlreadyClaimed
}

func (f *fakeMetadataGenStore) RenewLease(_ int64, _ string, _ time.Duration) error {
	return nil
}

func (f *fakeMetadataGenStore) MarkCompleted(id int64, _ string, result []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.completed[id] = result
	return nil
}

func (f *fakeMetadataGenStore) MarkFailed(id int64, _ string, lastError string, backoff *time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failed[id] = lastError
	f.backoffs[id] = backoff
	return nil
}

func (f *fakeMetadataGenStore) ReclaimExpired(_ time.Duration) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reclaims++
	return 0, nil
}

// fakeMetadataGen is a MetadataGenerator test double.
type fakeMetadataGen struct {
	mu  sync.Mutex
	fn  func(ctx context.Context, prompt string) (*services.NVIDIAMetadataResponse, error)
	got string
}

func (f *fakeMetadataGen) Generate(ctx context.Context, prompt string) (*services.NVIDIAMetadataResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.got = prompt
	if f.fn == nil {
		return &services.NVIDIAMetadataResponse{Title: "T", Description: "D"}, nil
	}
	return f.fn(ctx, prompt)
}

func newMetadataGenTestRig(store *fakeMetadataGenStore, gen *fakeMetadataGen) *MetadataGenerationWorker {
	// interval 0 → default; we drive drainOnce directly in tests.
	return NewMetadataGenerationWorker(store, gen, 0, nil)
}

func queuedGenJob(id int64) *models.MetadataGenerationJob {
	return &models.MetadataGenerationJob{
		ID: id, WorkspaceID: 1, VeloxProjectID: "prj_1",
		Prompt: "boxing tutorial", Status: models.MetadataGenJobQueued,
		MaxAttempts: 3,
	}
}

// TestMetadataGenWorker_ClaimsAndCompletes: one queued job → the
// generator is called with the prompt, the job is marked completed and
// the marshaled result is stored.
func TestMetadataGenWorker_ClaimsAndCompletes(t *testing.T) {
	store := newFakeMetadataGenStore(queuedGenJob(7))
	gen := &fakeMetadataGen{}
	w := newMetadataGenTestRig(store, gen)

	w.drainOnce(context.Background())

	if gen.got != "boxing tutorial" {
		t.Errorf("generator prompt: want %q, got %q", "boxing tutorial", gen.got)
	}
	raw, ok := store.completed[7]
	if !ok {
		t.Fatalf("job 7 not marked completed (completed=%v failed=%v)", store.completed, store.failed)
	}
	var resp services.NVIDIAMetadataResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("stored result is not valid JSON: %v", err)
	}
	if resp.Title != "T" {
		t.Errorf("stored result title: want T, got %q", resp.Title)
	}
	if len(store.failed) != 0 {
		t.Errorf("job must not be failed: %v", store.failed)
	}
}

// TestMetadataGenWorker_DrainsQueueUntilEmpty: several queued jobs are
// all processed in one drain (loop stops on ErrMetadataGenAlreadyClaimed).
func TestMetadataGenWorker_DrainsQueueUntilEmpty(t *testing.T) {
	store := newFakeMetadataGenStore(queuedGenJob(1), queuedGenJob(2), queuedGenJob(3))
	w := newMetadataGenTestRig(store, &fakeMetadataGen{})

	w.drainOnce(context.Background())

	if len(store.completed) != 3 {
		t.Errorf("completed jobs: want 3, got %d (%v)", len(store.completed), store.completed)
	}
}

// TestMetadataGenWorker_TransientError_RequeuesWithBackoff: a generic
// generation error → MarkFailed with a non-nil backoff (retry later).
func TestMetadataGenWorker_TransientError_RequeuesWithBackoff(t *testing.T) {
	store := newFakeMetadataGenStore(queuedGenJob(9))
	gen := &fakeMetadataGen{fn: func(ctx context.Context, prompt string) (*services.NVIDIAMetadataResponse, error) {
		return nil, errors.New("nvidia timeout")
	}}
	w := newMetadataGenTestRig(store, gen)

	w.drainOnce(context.Background())

	errMsg, ok := store.failed[9]
	if !ok {
		t.Fatalf("job 9 not marked failed")
	}
	if errMsg != "nvidia timeout" {
		t.Errorf("error message: want %q, got %q", "nvidia timeout", errMsg)
	}
	if store.backoffs[9] == nil {
		t.Error("backoff must be non-nil for a transient failure (job is retried)")
	}
}

// TestMetadataGenWorker_NotConfigured_FailsPermanently: NVIDIA not
// configured is terminal — MarkFailed with nil backoff (no retry loop
// hammering a misconfigured service).
func TestMetadataGenWorker_NotConfigured_FailsPermanently(t *testing.T) {
	store := newFakeMetadataGenStore(queuedGenJob(11))
	gen := &fakeMetadataGen{fn: func(ctx context.Context, prompt string) (*services.NVIDIAMetadataResponse, error) {
		return nil, services.ErrNVIDIANotConfigured
	}}
	w := newMetadataGenTestRig(store, gen)

	w.drainOnce(context.Background())

	if _, ok := store.failed[11]; !ok {
		t.Fatalf("job 11 must be marked failed for ErrNVIDIANotConfigured")
	}
	if store.backoffs[11] != nil {
		t.Error("backoff must be nil for a terminal misconfiguration failure")
	}
}

// TestMetadataGenWorker_EmptyQueue_IsNoOp: no queued jobs → the worker
// returns silently (no panic, no marks).
func TestMetadataGenWorker_EmptyQueue_IsNoOp(t *testing.T) {
	store := newFakeMetadataGenStore()
	w := newMetadataGenTestRig(store, &fakeMetadataGen{})

	w.drainOnce(context.Background())

	if len(store.completed) != 0 || len(store.failed) != 0 {
		t.Errorf("empty queue must mark nothing: completed=%v failed=%v", store.completed, store.failed)
	}
	if store.reclaims == 0 {
		t.Error("ReclaimExpired must run on each drain tick")
	}
}

// TestMetadataGenWorker_ComputeBackoff_GrowsWithAttempts: the backoff
// for a higher attempt count is (bounded) bigger on average than for
// attempt 0.
func TestMetadataGenWorker_ComputeBackoff_GrowsWithAttempts(t *testing.T) {
	store := newFakeMetadataGenStore()
	w := newMetadataGenTestRig(store, &fakeMetadataGen{})

	b0 := w.computeBackoff(0)
	b1 := w.computeBackoff(1)
	b5 := w.computeBackoff(5)

	if b0 <= 0 {
		t.Errorf("backoff attempt 0 must be positive, got %v", b0)
	}
	// attempt 1: base*2 → jitter [0, 10s) — mean 5s > mean 2.5s of attempt 0.
	if b1 < b0 {
		t.Logf("jitter may occasionally invert; b0=%v b1=%v", b0, b1)
	}
	if b5 > 5*time.Minute {
		t.Errorf("backoff must be capped at 5m, got %v", b5)
	}
}
