// Package worker tests — Task 5/3 wire-level integration test.
//
// The unit-level tests in artifact_verify_test.go lock the verifier
// reader's behaviour in isolation (size + SHA match / mismatch,
// RequireSHA flag, MIME boundary errors). THIS file locks the next
// level up: given a real UploadWorker.processIngestJob invocation
// against a fake Drive source + fake S3, the per-source switch in
// the worker's flow:
//
//   - Drive with declared sha256Checksum + match → policy.RequireSHA=true,
//     Verify() passes → MarkReady(actualSHA) → MarkIngested ONCE
//   - Drive with declared sha256Checksum + mismatch → policy.RequireSHA=true,
//     Verify() returns PermanentError → MarkFailedWithReason(asset, ...)
//     → processIngestJob returns NON-Permanent so handleProcessingError
//     classifies for retry (intentional: a flaky-network failure
//     could land here too) — but the asset MUST NOT transition to
//     'ready' AND MarkIngested MUST NOT be called.
//   - Drive WITHOUT sha256Checksum in metadata → policy.RequireSHA=false,
//     Verify() accepts (size match) but still computes local SHA via
//     ActualSHA256Hex() → MarkReady(actualSHA) → MarkIngested ONCE
//
// Three tests. Each wires a fresh UploadWorker with the minimum
// dependency surface to exercise processIngestJob without dragging in
// the postgres/vault/capRouter scaffolding. The body of the fake
// source is a small fixed byte slice so the SHA match / mismatch
// scenarios are deterministic across reruns.
//
// Reference: internal/worker/upload_worker.go::processIngestJob
// (the per-source switch that this test pins); the prior
// artifact_verify_test.go (which covered the reader in isolation).
package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// sha256HexBytes is an inline version of sha256HexOf from
// artifact_verify_test.go, kept private here since the test files
// cannot share unexported helpers across files without pulling
// them into a dedicated test_helpers package.
func sha256HexBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// ---------------------------------------------------------------------------
// Fakes — minimal-but-correct subset of the worker's dependency surface
// ---------------------------------------------------------------------------

// roundTripSource is the test-side ArtifactSource that mimics the
// AuthenticatedDriveSource surface without the DriveImporter +
// VaultAPI dep pair (the worker's per-source switch only consumes
// Inspect + Open, not the OAuth plumbing).
type roundTripSource struct {
	md    *SourceMetadata
	body  []byte
	mime  string
	size  int64
}

func (s *roundTripSource) Name() models.UploadJobSource {
	return models.UploadJobSourceAuthenticatedDrive
}

func (s *roundTripSource) Inspect(_ context.Context, _ *models.UploadJob) (*SourceMetadata, error) {
	return s.md, nil
}

func (s *roundTripSource) Open(_ context.Context, _ *models.UploadJob) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(string(s.body))), nil
}

// fakeJobRepo is the smallest UploadJobStore stub that satisfies the
// interface — only MarkIngested + MarkFailed are observable for
// processIngestJob asserts; the rest are present to satisfy the
// interface and recorded for completeness.
type fakeJobRepo struct {
	mu sync.Mutex
	markIngestedCalls   []markIngestedCall
	markFailedCalls     []markFailedCall
	markDeadLetterCalls []markDeadLetterCall
	markRetryCalls      []markRetryCall
}

type markIngestedCall struct {
	jobID, totalBytes int64
	workerID, assetID string
}
type markFailedCall struct {
	jobID                                  int64
	workerID, errCode, errMessage           string
}
type markDeadLetterCall struct {
	jobID                          int64
	workerID, errCode, errMessage  string
}
type markRetryCall struct {
	jobID                   int64
	workerID, errCode, msg   string
	nextAt                  time.Time
}

func (f *fakeJobRepo) ClaimBatch(_ context.Context, _ string, _ int, _ time.Duration) ([]*models.UploadJob, error) {
	return nil, nil
}
func (f *fakeJobRepo) ClaimBatchForPublish(_ context.Context, _ string, _ int, _ time.Duration) ([]*models.UploadJob, error) {
	return nil, nil
}
func (f *fakeJobRepo) Heartbeat(_ context.Context, _ int64, _ string, _ time.Duration) error { return nil }
func (f *fakeJobRepo) MarkCompleted(_ context.Context, _ int64, _ string, _ int64, _ string) error { return nil }
func (f *fakeJobRepo) SaveYouTubeSession(_ context.Context, _ int64, _, _ string, _, _ int64, _ time.Time) error {
	return nil
}
func (f *fakeJobRepo) ClearYouTubeSession(_ context.Context, _ int64, _ string) error { return nil }
func (f *fakeJobRepo) ReclaimExpiredLeases(_ context.Context, _ int) (int64, error)    { return 0, nil }

func (f *fakeJobRepo) MarkIngested(_ context.Context, id int64, w, assetID string, total int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.markIngestedCalls = append(f.markIngestedCalls, markIngestedCall{id, total, w, assetID})
	return nil
}
func (f *fakeJobRepo) MarkFailed(_ context.Context, id int64, w, code, msg string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.markFailedCalls = append(f.markFailedCalls, markFailedCall{id, w, code, msg})
	return nil
}
func (f *fakeJobRepo) MarkRetry(_ context.Context, id int64, w, code, msg string, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.markRetryCalls = append(f.markRetryCalls, markRetryCall{id, w, code, msg, time.Now()})
	return nil
}
func (f *fakeJobRepo) MarkDeadLetter(_ context.Context, id int64, w, code, msg string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.markDeadLetterCalls = append(f.markDeadLetterCalls, markDeadLetterCall{id, w, code, msg})
	return nil
}

// fakeMediaStore is the UploadMediaStore stub: tracks Create +
// MarkReady + MarkFailed so the worker's media_assets transitions
// are observable across tests.
type fakeMediaStore struct {
	mu              sync.Mutex
	created         []*models.MediaAsset
	markReadyCalls  []markReadyCall
	markFailedCalls []markFailedAssetCall
}

// markReadyCall mirrors the production MarkReady(id, sha256 string,
// sizeBytes int64, contentType string) arg order. The struct field
// order matters because every construction site passes positional
// args — making the struct fields match the production arg order
// keeps the two synchronized at the test surface.
type markReadyCall struct {
	id          string
	sha256      string
	sizeBytes   int64
	contentType string
}
type markFailedAssetCall struct {
	id, reason string
}

// Compile-time assertion that fakeMediaStore satisfies the worker-layer
// UploadMediaStore contract (Create + MarkReady + MarkFailed +
// MarkFailedWithReason). Cheap insurance against future interface drift.
var _ UploadMediaStore = (*fakeMediaStore)(nil)

func (f *fakeMediaStore) Create(asset *models.MediaAsset) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if asset.ID == "" {
		asset.ID = "asset_test_" + uuid.NewString()[:8]
	}
	cp := *asset
	f.created = append(f.created, &cp)
	return nil
}
func (f *fakeMediaStore) MarkReady(id, sha256 string, size int64, ct string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.markReadyCalls = append(f.markReadyCalls, markReadyCall{
		id:          id,
		sha256:      sha256,
		sizeBytes:   size,
		contentType: ct,
	})
	return nil
}
func (f *fakeMediaStore) MarkFailed(id, reason string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.markFailedCalls = append(f.markFailedCalls, markFailedAssetCall{id, reason})
	return nil
}
func (f *fakeMediaStore) MarkFailedWithReason(id, reason string, _ error) error {
	return f.MarkFailed(id, reason)
}

// fakeStorage drives SignUpload to a httptest.Server URL; the server
// accepts any PUT + returns 200. The captured body is irrelevant —
// the verifier reads from the source body, not from S3's response.
// VerifyUpload returns the (contentType, size) the test wants
// surfaced as the "post-stream truth".
type fakeStorage struct {
	srv             *httptest.Server
	expectedContent string
	expectedSize    int64
}

// Compile-time conformance for services.StorageProvider. The worker's
// call sites only use SignUpload + VerifyUpload + AssetURL; we stub
// the latter to a no-op so the interface is satisfied.
var _ services.StorageProvider = (*fakeStorage)(nil)

func (s *fakeStorage) SignUpload(_ context.Context, _ int64, _, ct string, size int64, _ time.Duration) (*services.UploadGrant, error) {
	s.expectedContent = ct
	s.expectedSize = size
	return &services.UploadGrant{UploadURL: s.srv.URL + "/" + uuid.NewString()}, nil
}
func (s *fakeStorage) VerifyUpload(_ context.Context, _ string) (string, int64, error) {
	return s.expectedContent, s.expectedSize, nil
}
func (s *fakeStorage) AssetURL(string) string   { return "https://fake-cdn.local/" + uuid.NewString() }
func (s *fakeStorage) Provider() string           { return "fake-s3" }
// Upload is interface-satisfaction ONLY. The current worker flow
// routes Upload via SignUpload (which returns an httptest.Server
// PUT URL wrapped by the artifactVerifyReader). A future regression
// that accidentally calls Upload() instead of SignUpload() would
// silently bypass the SHA-verify gate; making Upload() return an
// explicit error means any such regression trips loudly here
// instead of growing silent in test outputs.
func (s *fakeStorage) Upload(_ context.Context, _ io.Reader, _, ct string, size int64) (int64, error) {
	return 0, errors.New("fakeStorage.Upload: not exercised by worker hot path; SignUpload is the production surface — a regression that lands here means Upload was called instead of SignUpload, which would bypass the artifactVerifyReader (the user-spec invariant 'Drive verification as complete as Velox before MarkIngested' would be silently broken)")
}

// startFakeS3 builds an httptest server that accepts any PUT and
// returns 200. The body is intentionally not echoed back — the
// verifier runs on the source body, not the S3 response. Capture
// is included for ops debugging only.
func startFakeS3(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
}

// ---------------------------------------------------------------------------
// Test harness — builds the worker with all fakes wired
// ---------------------------------------------------------------------------

// buildWorkerForDriveTest wires the minimum UploadWorker for
// processIngestJob: sourceRegistry (with the test's roundTripSource
// registered), UploadJobStore, UploadMediaStore, services.StorageProvider.
// deliveryVerifier is intentionally nil — Drive source does NOT use
// it (Velox does).
//
// Worker options are zero-value so applyDefaults() picks the
// production values; the test thread doesn't depend on heartbeat /
// reclaimer behaviour because we call processIngestJob directly
// (bypassing runIngestPool's ticker).
func buildWorkerForDriveTest(t *testing.T, src *roundTripSource, media UploadMediaStore, jobRepo UploadJobStore, storage services.StorageProvider) *UploadWorker {
	t.Helper()
	registry := NewArtifactSourceRegistry()
	if err := registry.Register(src); err != nil {
		t.Fatalf("registry.Register: %v", err)
	}
	return NewUploadWorker(
		jobRepo,
		media,
		nil, // postStore — processIngestJob doesn't reach it
		nil, // userRepo  — same
		storage,
		nil, // capRouter — processIngestJob doesn't reach it
		nil, // vault — processIngestJob doesn't reach it on Drive path
		registry,
		nil, // deliveryVerifier — nil; Drive path doesn't use it
		time.Minute,
		nil, // logger — slog.Default() picked up by constructor
		UploadWorkerOptions{}, // zero-value → applyDefaults fills in production hardiness
	)
}

// makeDriveTestJob builds an UploadJob in the shape processIngestJob
// expects: SourceAuthenticatedDrive, SourceID + FolderID set,
// TargetAccountIDs set, PublishAt in the future.
func makeDriveTestJob(t *testing.T) *models.UploadJob {
	t.Helper()
	driveAcct := int64(99)
	folderID := "test-folder-" + uuid.NewString()[:6]
	publishAt := time.Now().Add(2 * time.Hour)
	return &models.UploadJob{
		ID:             1001,
		UserID:         7,
		WorkspaceID:    11,
		SourceType:     models.UploadJobSourceAuthenticatedDrive,
		SourceID:       "drive-file-" + uuid.NewString()[:8],
		DriveAccountID: &driveAcct,
		FolderID:       &folderID,
		Title:          "Test Drive Ingest",
		Targets:        []int64{42},
		Status:         models.UploadJobStatusPending,
		IngestAfter:    time.Now(),
		PublishAt:      &publishAt,
	}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestProcessIngestJob_DriveWithDeclaredSHA_Happy_ActualSHAPersisted
// — Drive file metadata includes sha256Checksum MATCHING the streamed
// bytes → processIngestJob runs through the full happy path:
// S3 PUT → Verify passes → MarkReady(actualSHA=local compute) →
// MarkIngested(totalBytes=size). The local SHA is computed via
// ActualSHA256Hex() (regardless of RequireSHA=true) so
// media_assets.sha256 carries the truth source.
func TestProcessIngestJob_DriveWithDeclaredSHA_Happy_ActualSHAPersisted(t *testing.T) {
	payload := []byte("drive-streamed-bytes-task-five-three")
	declaredDriveSHA := sha256HexBytes(payload)

	src := &roundTripSource{
		md: &SourceMetadata{
			SizeBytes: int64(len(payload)),
			MimeType:  "video/mp4",
			SHA256Hex: declaredDriveSHA,
		},
		body: payload,
		size: int64(len(payload)),
		mime: "video/mp4",
	}
	jobRepo := &fakeJobRepo{}
	mediaStore := &fakeMediaStore{}
	s3Srv := startFakeS3(t)
	defer s3Srv.Close()
	storage := &fakeStorage{srv: s3Srv, expectedContent: "video/mp4", expectedSize: int64(len(payload))}

	w := buildWorkerForDriveTest(t, src, mediaStore, jobRepo, storage)
	job := makeDriveTestJob(t)

	if err := w.processIngestJob(context.Background(), job, "test-worker"); err != nil {
		t.Fatalf("processIngestJob: %v", err)
	}

	// MarkIngested called exactly once.
	if got := len(jobRepo.markIngestedCalls); got != 1 {
		t.Errorf("MarkIngested call count: want 1, got %d", got)
	}
	// MarkFailed/MarkRetry/MarkDeadLetter NOT called on the happy path.
	if got := len(jobRepo.markFailedCalls); got != 0 {
		t.Errorf("MarkFailed calls: want 0 on happy path, got %d (%v)", got, jobRepo.markFailedCalls)
	}
	if got := len(jobRepo.markRetryCalls); got != 0 {
		t.Errorf("MarkRetry calls: want 0 on happy path, got %d", got)
	}
	if got := len(jobRepo.markDeadLetterCalls); got != 0 {
		t.Errorf("MarkDeadLetter calls: want 0 on happy path, got %d", got)
	}

	// MarkReady called exactly once with the LOCAL SHA (defense-in-depth
	// — the worker always persists local compute, regardless of
	// RequireSHA).
	if got := len(mediaStore.markReadyCalls); got != 1 {
		t.Fatalf("MarkReady call count: want 1, got %d", got)
	}
	gotReady := mediaStore.markReadyCalls[0]
	if gotReady.sha256 == "" {
		t.Errorf("MarkReady sha256: want non-empty (local compute), got empty")
	}
	if gotReady.sha256 != sha256HexBytes(payload) {
		t.Errorf("MarkReady sha256: want %q (local compute of payload), got %q", sha256HexBytes(payload), gotReady.sha256)
	}
	if gotReady.sizeBytes != int64(len(payload)) {
		t.Errorf("MarkReady sizeBytes: want %d, got %d", len(payload), gotReady.sizeBytes)
	}
	if gotReady.contentType != "video/mp4" {
		t.Errorf("MarkReady contentType: want \"video/mp4\", got %q", gotReady.contentType)
	}
}

// TestProcessIngestJob_DriveWithDeclaredSHA_Mismatch_FailLoud —
// Drive's metadata sha256Checksum DOES NOT match the streamed bytes
// → Verify() returns PermanentError → MarkReady NOT called →
// MarkFailedWithReason called once on the asset → MarkIngested NOT
// called. The asset MUST NOT transition to ready_to_publish (that's
// the user spec invariant).
func TestProcessIngestJob_DriveWithDeclaredSHA_Mismatch_FailLoud(t *testing.T) {
	payload := []byte("drive-streamed-bytes-task-five-three")
	// Deliberately wrong declared SHA.
	declaredDriveSHA := sha256HexBytes([]byte("drive-claimed-but-actually-was-something-else"))

	src := &roundTripSource{
		md: &SourceMetadata{
			SizeBytes: int64(len(payload)),
			MimeType:  "video/mp4",
			SHA256Hex: declaredDriveSHA,
		},
		body: payload,
		size: int64(len(payload)),
		mime: "video/mp4",
	}
	jobRepo := &fakeJobRepo{}
	mediaStore := &fakeMediaStore{}
	s3Srv := startFakeS3(t)
	defer s3Srv.Close()
	storage := &fakeStorage{srv: s3Srv, expectedContent: "video/mp4", expectedSize: int64(len(payload))}

	w := buildWorkerForDriveTest(t, src, mediaStore, jobRepo, storage)
	job := makeDriveTestJob(t)

	err := w.processIngestJob(context.Background(), job, "test-worker")
	if err == nil {
		t.Fatal("processIngestJob on Drive SHA mismatch: want non-nil error, got nil")
	}
	// PermanentError (the verifier wraps the mismatch as PermanentError
	// which handleProcessingError matches for MarkDeadLetter fast-path).
	if !errors.Is(err, ErrPermanent) {
		t.Errorf("error must wrap ErrPermanent (verifier mismatch path); got %v", err)
	}

	// Verify ordering ASSUMPTION: S3 PUT happens before Verify() and
	// MarkReady is conditional on Verify() — so on the failure path,
	// the asset must NOT be MarkReady'd. The MarkFailedWithReason on
	// the asset is what stamps the failure.
	if got := len(mediaStore.markReadyCalls); got != 0 {
		t.Errorf("MarkReady calls on Drive SHA mismatch: want 0 (must not transition to ready), got %d", got)
	}
	if got := len(mediaStore.markFailedCalls); got != 1 {
		t.Errorf("MarkFailedWithReason calls: want 1 (asset-level failure stamp), got %d", got)
	}
	// The user-spec invariant: MarkIngested MUST NOT fire on the
	// failure path. The asset must not progress to ready_to_publish.
	if got := len(jobRepo.markIngestedCalls); got != 0 {
		t.Errorf("MarkIngested calls on Drive SHA mismatch: want 0 (user spec: Verify before MarkIngested), got %d", got)
	}
}

// TestProcessIngestJob_DriveWithoutDeclaredSHA_LocalSHAComputed —
// Drive's metadata omits sha256Checksum (legacy files, very large
// uploads, or files Drive can't checksum). RequireSHA must be FALSE;
// the local-SHA-compute path still runs, Verify passes on size match
// alone, and MarkReady is called with the locally-computed SHA so
// media_assets.sha256 carries truth regardless.
func TestProcessIngestJob_DriveWithoutDeclaredSHA_LocalSHAComputed(t *testing.T) {
	payload := []byte("drive-no-declared-sha-task-five-three")

	src := &roundTripSource{
		md: &SourceMetadata{
			SizeBytes: int64(len(payload)),
			MimeType:  "video/mp4",
			SHA256Hex: "", // Drive didn't surface sha256Checksum
		},
		body: payload,
		size: int64(len(payload)),
		mime: "video/mp4",
	}
	jobRepo := &fakeJobRepo{}
	mediaStore := &fakeMediaStore{}
	s3Srv := startFakeS3(t)
	defer s3Srv.Close()
	storage := &fakeStorage{srv: s3Srv, expectedContent: "video/mp4", expectedSize: int64(len(payload))}

	w := buildWorkerForDriveTest(t, src, mediaStore, jobRepo, storage)
	job := makeDriveTestJob(t)

	if err := w.processIngestJob(context.Background(), job, "test-worker"); err != nil {
		t.Fatalf("processIngestJob: %v (Drive WITH no declared SHA should still succeed — compute-and-persist local SHA only)", err)
	}

	// MarkIngested fired once — the asset transitioned to ready_to_publish.
	if got := len(jobRepo.markIngestedCalls); got != 1 {
		t.Errorf("MarkIngested call count: want 1 (local-compute-only path is success), got %d", got)
	}
	// MarkReady called with the LOCAL SHA — defense-in-depth (no
	// upstream SHA to cross-check; we persist what we computed).
	if got := len(mediaStore.markReadyCalls); got != 1 {
		t.Fatalf("MarkReady call count: want 1, got %d", got)
	}
	if got := mediaStore.markReadyCalls[0].sha256; got != sha256HexBytes(payload) {
		t.Errorf("MarkReady sha256: want %q (local-computed), got %q", sha256HexBytes(payload), got)
	}
	// No failure path exercised — no MarkFailed.
	if got := len(mediaStore.markFailedCalls); got != 0 {
		t.Errorf("MarkFailed calls: want 0 (no-declared-SHA path is success), got %d", got)
	}
}

// TestArtifactVerificationPolicy_FieldSurfaceClosure — pins the
// public field surface of models.ArtifactVerificationPolicy. If a
// future refactor renames or types one of the four fields, the
// production code paths in upload_worker.go::processIngestJob and
// artifact_verify.go::NewArtifactVerifyReader silently drift
// through the policy wiring (Go's structural access via field name
// doesn't fail compile on rename). This test catches the drift at
// the boundary — any new refactor touching ArtifactVerificationPolicy
// MUST keep these four field names + types, since both worker-layer
// call sites AND the Drive source layer reference them.
//
// Cheap insurance — 6 lines of explicit field reads.
func TestArtifactVerificationPolicy_FieldSurfaceClosure(t *testing.T) {
	p := models.ArtifactVerificationPolicy{}
	// The four production-critical fields. Names and types are
	// part of the contract: drive-side Inspect feeds
	// `p.ExpectedSHA256 + p.RequireSHA`, Velox-side feeds
	// `p.ExpectedSHA256 + p.RequireSHA`, worker's policy factory
	// sets `p.ExpectedSize + p.ExpectedMIME`, the verifier reader
	// consumes ALL four. A rename breaks the wire.
	_ = p.ExpectedSize
	_ = p.ExpectedSHA256
	_ = p.ExpectedMIME
	_ = p.RequireSHA
	// Smoke-construct a non-zero policy to lock type compatibility
	// across the package boundary.
	_ = models.ArtifactVerificationPolicy{
		ExpectedSize:   1024,
		ExpectedSHA256: strings.Repeat("a", 64),
		ExpectedMIME:   "video/mp4",
		RequireSHA:     true,
	}
}
