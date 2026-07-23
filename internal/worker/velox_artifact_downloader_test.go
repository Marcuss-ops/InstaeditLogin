// Tests for VeloxArtifactDownloader — polls external_deliveries
// for accepted Velox deliveries and registers each claimed row as
// an upload_jobs row. The tests use in-package fakes (no DB, no
// httptest) and exercise the new polling model: there is no
// channel; the worker claims rows from the database queue.
package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// --- Fakes ---------------------------------------------------------

type fakeClaimStore struct {
	mu          sync.Mutex
	queue       []*models.ExternalDelivery
	claimCalls  int32
	markCalls   map[string][]string // id -> list of method names
	markErr     error
	claimErr    error
	alwaysEmpty bool
}

func newFakeClaimStore() *fakeClaimStore {
	return &fakeClaimStore{markCalls: map[string][]string{}}
}

func (f *fakeClaimStore) seed(d ...*models.ExternalDelivery) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queue = append(f.queue, d...)
}

func (f *fakeClaimStore) ClaimDelivery(_ context.Context, _ string, _ time.Duration, _ int) (*models.ExternalDelivery, error) {
	atomic.AddInt32(&f.claimCalls, 1)
	if f.claimErr != nil {
		return nil, f.claimErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.queue) == 0 || f.alwaysEmpty {
		return nil, repository.ErrExternalDeliveryNotFound
	}
	d := f.queue[0]
	f.queue = f.queue[1:]
	return d, nil
}

func (f *fakeClaimStore) record(id, method string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.markCalls[id] = append(f.markCalls[id], method)
}

func (f *fakeClaimStore) MarkRetry(_ context.Context, id string, _ time.Time, _, _ string) error {
	f.record(id, "MarkRetry")
	return f.markErr
}

func (f *fakeClaimStore) MarkDeadLetter(_ context.Context, id string, _, _ string) error {
	f.record(id, "MarkDeadLetter")
	return f.markErr
}

func (f *fakeClaimStore) MarkFailed(_ context.Context, id string, _, _ string) error {
	f.record(id, "MarkFailed")
	return f.markErr
}

func (f *fakeClaimStore) MarkBlockedAuth(_ context.Context, id string, _, _ string) error {
	f.record(id, "MarkBlockedAuth")
	return f.markErr
}

type fakeDestinationLookup struct {
	mu     sync.Mutex
	rows   map[string]*models.ExternalDestination
	getErr error
}

func newFakeDestinationLookup() *fakeDestinationLookup {
	return &fakeDestinationLookup{rows: map[string]*models.ExternalDestination{}}
}

func (f *fakeDestinationLookup) seed(d *models.ExternalDestination) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows[d.ID] = d
}

func (f *fakeDestinationLookup) GetByID(_ context.Context, id string) (*models.ExternalDestination, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.rows[id], nil
}

type fakeWorkspaceLookup struct {
	mu     sync.Mutex
	rows   map[int64]*models.Workspace
	getErr error
}

func newFakeWorkspaceLookup() *fakeWorkspaceLookup {
	return &fakeWorkspaceLookup{rows: map[int64]*models.Workspace{}}
}

func (f *fakeWorkspaceLookup) seed(ws *models.Workspace) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows[ws.ID] = ws
}

func (f *fakeWorkspaceLookup) FindByID(id int64) (*models.Workspace, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.rows[id], nil
}

type fakeUploadCreator struct {
	mu          sync.Mutex
	jobs        []*models.UploadJob
	createErr   error
	nextID      int64
	createCalls int32
}

func newFakeUploadCreator() *fakeUploadCreator {
	return &fakeUploadCreator{nextID: 1000}
}

func (f *fakeUploadCreator) CreateUploadJobAndLink(_ context.Context, job *models.UploadJob, _ string, _ string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	atomic.AddInt32(&f.createCalls, 1)
	if f.createErr != nil {
		return 0, f.createErr
	}
	f.nextID++
	job.ID = f.nextID
	cp := *job
	f.jobs = append(f.jobs, &cp)
	return job.ID, nil
}

// --- Helpers -------------------------------------------------------

func shaTest() string {
	h := sha256.Sum256([]byte("test"))
	return hex.EncodeToString(h[:])
}

func dummyDelivery(id, downloadURL string) *models.ExternalDelivery {
	d := &models.ExternalDelivery{
		ID:                    id,
		SourceSystem:          "velox",
		ExternalDeliveryID:    "extdel_" + id,
		IdempotencyKey:        "idem_" + id,
		ExternalDestinationID: "extdst_01J",
		SourceArtifactID:      "artifact_" + id,
		ExpectedSHA256:        shaTest(),
		ExpectedMimeType:      "video/mp4",
		ExpectedSizeBytes:     1024 * 1024,
		Metadata:              []byte(`{"title":"hello","description":"d"}`),
		Status:                models.ExternalDeliveryStatusAccepted,
		CreatedAt:             time.Now().UTC(),
		UpdatedAt:             time.Now().UTC(),
	}
	if downloadURL != "" {
		dl := downloadURL
		d.DownloadURL = &dl
	}
	return d
}

func newTestDownloader(claimStore *fakeClaimStore, dests *fakeDestinationLookup, workspaces *fakeWorkspaceLookup, uploads *fakeUploadCreator) *VeloxArtifactDownloader {
	return NewVeloxArtifactDownloader(
		claimStore,
		uploads,
		NewIngestFSM(nil, slog.Default()),
		dests,
		workspaces,
		"test-worker",
		slog.Default(),
		VeloxArtifactDownloaderOptions{PollInterval: 10 * time.Millisecond, Lease: time.Minute, MaxAttempts: 3},
	)
}

// --- Tests ---------------------------------------------------------

// TestVeloxArtifactDownloader_HappyPath covers the full claim→process
// flow: a delivery is claimed, destination/workspace/metadata resolve,
// and an upload_job is created.
func TestVeloxArtifactDownloader_HappyPath(t *testing.T) {
	claimStore := newFakeClaimStore()
	claimStore.seed(dummyDelivery("sdel_happy", "https://velox.example/artifact_happy"))

	destStore := newFakeDestinationLookup()
	destStore.seed(&models.ExternalDestination{ID: "extdst_01J", WorkspaceID: 7, SourceSystem: "velox", Enabled: true})

	wsStore := newFakeWorkspaceLookup()
	wsStore.seed(&models.Workspace{ID: 7, OwnerID: 42})

	uploads := newFakeUploadCreator()
	d := newTestDownloader(claimStore, destStore, wsStore, uploads)

	d.claimAndProcess(context.Background())

	if got := atomic.LoadInt32(&uploads.createCalls); got != 1 {
		t.Errorf("CreateUploadJobAndLink calls = %d; want 1", got)
	}
	if len(uploads.jobs) != 1 {
		t.Fatalf("upload jobs = %d; want 1", len(uploads.jobs))
	}
	uj := uploads.jobs[0]
	if uj.SourceType != models.UploadJobSourceVeloxArtifact {
		t.Errorf("SourceType = %q; want %q", uj.SourceType, models.UploadJobSourceVeloxArtifact)
	}
	if uj.UserID != 42 || uj.WorkspaceID != 7 {
		t.Errorf("UserID=%d WorkspaceID=%d; want 42/7", uj.UserID, uj.WorkspaceID)
	}
	if uj.Title != "hello" {
		t.Errorf("Title = %q; want hello", uj.Title)
	}
	if uj.DefaultPrivacyLevel != "" {
		// metadata doesn't set privacy_status in the default dummy payload
		t.Errorf("DefaultPrivacyLevel = %q; want empty", uj.DefaultPrivacyLevel)
	}
}

// TestVeloxArtifactDownloader_CtxCancelDrainPromptly asserts Run
// returns promptly when the context is cancelled.
func TestVeloxArtifactDownloader_CtxCancelDrainPromptly(t *testing.T) {
	claimStore := newFakeClaimStore()
	claimStore.alwaysEmpty = true
	d := newTestDownloader(claimStore, newFakeDestinationLookup(), newFakeWorkspaceLookup(), newFakeUploadCreator())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	err := d.Run(ctx)
	elapsed := time.Since(start)
	if err != nil && err != context.Canceled {
		t.Errorf("Run err = %v; want nil or context.Canceled", err)
	}
	if elapsed > 100*time.Millisecond {
		t.Errorf("drain elapsed = %v; want <100ms (no hang)", elapsed)
	}
}

// TestVeloxArtifactDownloader_NilDownloadURL_MetadataOnly asserts
// metadata-only deliveries (no download_url) transition to
// blocked_auth without creating an upload_job.
func TestVeloxArtifactDownloader_NilDownloadURL_MetadataOnly(t *testing.T) {
	claimStore := newFakeClaimStore()
	claimStore.seed(dummyDelivery("sdel_metadata_only", ""))

	destStore := newFakeDestinationLookup()
	destStore.seed(&models.ExternalDestination{ID: "extdst_01J", WorkspaceID: 7, SourceSystem: "velox", Enabled: true})

	wsStore := newFakeWorkspaceLookup()
	wsStore.seed(&models.Workspace{ID: 7, OwnerID: 42})

	uploads := newFakeUploadCreator()
	d := newTestDownloader(claimStore, destStore, wsStore, uploads)
	d.claimAndProcess(context.Background())

	if got := atomic.LoadInt32(&uploads.createCalls); got != 0 {
		t.Errorf("CreateUploadJobAndLink calls = %d; want 0", got)
	}
	if m := claimStore.markCalls["sdel_metadata_only"]; len(m) != 1 || m[0] != "MarkBlockedAuth" {
		t.Errorf("mark calls = %v; want [MarkBlockedAuth]", m)
	}
}

// TestVeloxArtifactDownloader_DestinationMissing_Failed asserts a
// missing destination row is treated as a terminal error and the
// delivery is marked failed.
func TestVeloxArtifactDownloader_DestinationMissing_Failed(t *testing.T) {
	claimStore := newFakeClaimStore()
	claimStore.seed(dummyDelivery("sdel_no_dest", "https://x/"))

	uploads := newFakeUploadCreator()
	d := newTestDownloader(claimStore, newFakeDestinationLookup(), newFakeWorkspaceLookup(), uploads)
	d.claimAndProcess(context.Background())

	if got := atomic.LoadInt32(&uploads.createCalls); got != 0 {
		t.Errorf("CreateUploadJobAndLink calls = %d; want 0", got)
	}
	if m := claimStore.markCalls["sdel_no_dest"]; len(m) != 1 || m[0] != "MarkFailed" {
		t.Errorf("mark calls = %v; want [MarkFailed]", m)
	}
}

// TestVeloxArtifactDownloader_WorkspaceMissing_Failed asserts a
// missing workspace row is treated as a terminal error.
func TestVeloxArtifactDownloader_WorkspaceMissing_Failed(t *testing.T) {
	claimStore := newFakeClaimStore()
	claimStore.seed(dummyDelivery("sdel_no_ws", "https://x/"))

	destStore := newFakeDestinationLookup()
	destStore.seed(&models.ExternalDestination{ID: "extdst_01J", WorkspaceID: 7, SourceSystem: "velox", Enabled: true})

	uploads := newFakeUploadCreator()
	d := newTestDownloader(claimStore, destStore, newFakeWorkspaceLookup(), uploads)
	d.claimAndProcess(context.Background())

	if got := atomic.LoadInt32(&uploads.createCalls); got != 0 {
		t.Errorf("CreateUploadJobAndLink calls = %d; want 0", got)
	}
	if m := claimStore.markCalls["sdel_no_ws"]; len(m) != 1 || m[0] != "MarkFailed" {
		t.Errorf("mark calls = %v; want [MarkFailed]", m)
	}
}

// TestVeloxArtifactDownloader_CreateError_Retries asserts a
// transient CreateUploadJobAndLink error is retried (MarkRetry)
// when attempts remain.
func TestVeloxArtifactDownloader_CreateError_Retries(t *testing.T) {
	claimStore := newFakeClaimStore()
	claimStore.seed(&models.ExternalDelivery{
		ID:                    "sdel_create_err",
		SourceSystem:          "velox",
		ExternalDeliveryID:    "extdel_sdel_create_err",
		ExternalDestinationID: "extdst_01J",
		Metadata:              []byte(`{"title":"hello","description":"d"}`),
		DownloadURL:           strPtr("https://x/"),
		Status:                models.ExternalDeliveryStatusAccepted,
		CreatedAt:             time.Now().UTC(),
		UpdatedAt:             time.Now().UTC(),
		AttemptCount:          1,
	})

	destStore := newFakeDestinationLookup()
	destStore.seed(&models.ExternalDestination{ID: "extdst_01J", WorkspaceID: 7, SourceSystem: "velox", Enabled: true})

	wsStore := newFakeWorkspaceLookup()
	wsStore.seed(&models.Workspace{ID: 7, OwnerID: 42})

	uploads := newFakeUploadCreator()
	uploads.createErr = errors.New("boom")

	d := newTestDownloader(claimStore, destStore, wsStore, uploads)
	d.claimAndProcess(context.Background())

	if got := atomic.LoadInt32(&uploads.createCalls); got != 1 {
		t.Errorf("CreateUploadJobAndLink calls = %d; want 1", got)
	}
	if m := claimStore.markCalls["sdel_create_err"]; len(m) != 1 || m[0] != "MarkRetry" {
		t.Errorf("mark calls = %v; want [MarkRetry]", m)
	}
}

// TestVeloxArtifactDownloader_MaxAttempts_DeadLetter asserts that
// once the retry budget is exhausted the delivery is dead-lettered.
func TestVeloxArtifactDownloader_MaxAttempts_DeadLetter(t *testing.T) {
	claimStore := newFakeClaimStore()
	claimStore.seed(&models.ExternalDelivery{
		ID:                    "sdel_dead",
		SourceSystem:          "velox",
		ExternalDeliveryID:    "extdel_sdel_dead",
		ExternalDestinationID: "extdst_01J",
		Metadata:              []byte(`{"title":"hello","description":"d"}`),
		DownloadURL:           strPtr("https://x/"),
		Status:                models.ExternalDeliveryStatusAccepted,
		CreatedAt:             time.Now().UTC(),
		UpdatedAt:             time.Now().UTC(),
		AttemptCount:          3, // equals MaxAttempts
	})

	destStore := newFakeDestinationLookup()
	destStore.seed(&models.ExternalDestination{ID: "extdst_01J", WorkspaceID: 7, SourceSystem: "velox", Enabled: true})

	wsStore := newFakeWorkspaceLookup()
	wsStore.seed(&models.Workspace{ID: 7, OwnerID: 42})

	uploads := newFakeUploadCreator()
	uploads.createErr = errors.New("boom")

	d := newTestDownloader(claimStore, destStore, wsStore, uploads)
	d.claimAndProcess(context.Background())

	if m := claimStore.markCalls["sdel_dead"]; len(m) != 1 || m[0] != "MarkDeadLetter" {
		t.Errorf("mark calls = %v; want [MarkDeadLetter]", m)
	}
}

// TestVeloxArtifactDownloader_ClaimEmpty_Loops asserts Run polls
// repeatedly and only exits on context cancellation.
func TestVeloxArtifactDownloader_ClaimEmpty_Loops(t *testing.T) {
	claimStore := newFakeClaimStore()
	claimStore.alwaysEmpty = true
	d := newTestDownloader(claimStore, newFakeDestinationLookup(), newFakeWorkspaceLookup(), newFakeUploadCreator())

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	err := d.Run(ctx)
	if err != nil && err != context.Canceled {
		t.Errorf("Run err = %v; want nil or context.Canceled", err)
	}
	if got := atomic.LoadInt32(&claimStore.claimCalls); got < 2 {
		t.Errorf("ClaimDelivery calls = %d; want >= 2", got)
	}
}
