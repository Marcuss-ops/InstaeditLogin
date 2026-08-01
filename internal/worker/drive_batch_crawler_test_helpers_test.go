package worker

// Test helpers for the drive batch crawler tests — fakes, recording
// doubles, and the harness shared by the unit tests in
// drive_batch_crawler_test.go and the e2e test in
// drive_batch_crawler_e2e_test.go. Extracted from
// drive_batch_crawler_test.go (Task 7/10) so the test files stay
// focused on behavior rather than setup.

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// ---------------------------------------------------------------------------
// Fakes
// ---------------------------------------------------------------------------

// fakeBatchStore is the in-memory CrawlerBatchStore used by every test
// in this file. Single-row contract: seed one batch, expect the
// crawler to claim exactly it once. Records heartbeat, cursor,
// increment, completed, and failed calls so the test can assert the
// per-page lifecycle happened in the right order.
//
// FindByID is implemented but unreferenced by the crawler — kept only
// to satisfy the CrawlerBatchStore interface (which the Go type
// system requires of any fake). The error sentinel is inlined; the
// crawler calls ClaimNextBatch instead, so this branch is dead.
type fakeBatchStore struct {
	mu                  sync.Mutex
	batches             map[uuid.UUID]*models.ImportBatch
	heartbeatCalls      int
	updateCursorHistory []updateCursorCall
	incrementCalls      []int
	markCompletedCalls  int
	markFailedCalls     []string
}

type updateCursorCall struct {
	PageToken string
	Count     int
}

// Compile-time conformance check. NewDriveBatchCrawler takes a
// CrawlerBatchStore (the local interface defined up the file in
// drive_batch_crawler.go); the type system rejects our fake at the
// call site if methods drift, but the explicit assertion makes the
// same drift fail at this file's compile rather than at the caller
// — keeps the diff for a future breakage colocated with the fake.
var _ CrawlerBatchStore = (*fakeBatchStore)(nil)

func newFakeBatchStore() *fakeBatchStore {
	return &fakeBatchStore{batches: make(map[uuid.UUID]*models.ImportBatch)}
}

func (f *fakeBatchStore) seedBatch(b *models.ImportBatch) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.batches[b.ID] = b
}

func (f *fakeBatchStore) ClaimNextBatch(_ context.Context, _ string, _ time.Duration) (*models.ImportBatch, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, b := range f.batches {
		return b, nil
	}
	return nil, nil
}

func (f *fakeBatchStore) Heartbeat(_ context.Context, _ uuid.UUID, _ string, _ time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.heartbeatCalls++
	return nil
}

func (f *fakeBatchStore) UpdateCursor(_ context.Context, _ uuid.UUID, _ string, pageToken string, count int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updateCursorHistory = append(f.updateCursorHistory, updateCursorCall{PageToken: pageToken, Count: count})
	return nil
}

func (f *fakeBatchStore) IncrementCreatedCount(_ context.Context, _ uuid.UUID, _ string, delta int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.incrementCalls = append(f.incrementCalls, delta)
	return nil
}

func (f *fakeBatchStore) MarkCompleted(_ context.Context, _ uuid.UUID, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.markCompletedCalls++
	return nil
}

func (f *fakeBatchStore) MarkFailed(_ context.Context, _ uuid.UUID, _, msg string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.markFailedCalls = append(f.markFailedCalls, msg)
	return nil
}

func (f *fakeBatchStore) FindByID(id uuid.UUID) (*models.ImportBatch, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.batches[id]
	if !ok {
		// Inlined sentinel — the production repository doesn't
		// expose a typed "not found" error for ImportBatch (its
		// only typed sentinel is ErrImportBatchLeaseLost, used
		// for ownership conflicts, not for 404-style misses).
		// The crawler calls ClaimNextBatch rather than FindByID,
		// so this branch is dead in tests.
		return nil, errors.New("drive-id test: batch not found")
	}
	return b, nil
}

func (f *fakeBatchStore) ReclaimExpiredBatches(_ context.Context, _ int) (int64, error) {
	return 0, nil
}

// fakeUploadRepo records every upload_job creation the crawler asks
// for. Returning a non-nil createErr short-circuits the for-loop
// page iteration (not used by the 5 tests below, but exposed for
// future expansion inside this same package).
type fakeUploadRepo struct {
	mu        sync.Mutex
	created   []*models.UploadJob
	createErr error
}

func (f *fakeUploadRepo) Create(job *models.UploadJob) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return f.createErr
	}
	f.created = append(f.created, job)
	return nil
}

// driveBatchFakeVault returns a canned bearer token from Renew.
//
// NOTE: name has "driveBatch" prefix to avoid colliding with the
// existing `fakeVault` struct in authenticated_drive_source_test.go
// — same package (worker), so Go's identifier namespace rules forbid
// redeclaration regardless of file boundary.
//
// Implements BOTH credentials.VaultAPI methods. Production
// signatures, copied verbatim from internal/credentials/vault.go:
//
//	Renew(ctx, accountID int64, tokenType string, refresher TokenRefresher) (*models.OAuthToken, error)
//	Get(ctx, accountID int64, tokenType string) (*models.OAuthToken, error)
//
// The Get signature is the (ctx, int64, string) shape — tokenType
// is part of the lookup key in the production vault; the test fake
// just ignores it and returns the canned token.
//
// Subtlety: the production vault stores encrypted OAuth tokens
// separately from the public TokenData shape exported by the
// platform OAuth callbacks — the return type is *models.OAuthToken
// (not *TokenData). Captures now() once at construction so multiple
// Renew / Get calls return the SAME time-equivalent expiry pointer
// (production caches the vault-minted timestamp; the fake mimics that
// to avoid asserting on a clock drift between successive calls).
type driveBatchFakeVault struct {
	mu          sync.Mutex
	accessToken string
	expiresAt   *time.Time
	renewCalls  int
}

func newFakeVault(token string) *driveBatchFakeVault {
	now := time.Now().Add(1 * time.Hour)
	return &driveBatchFakeVault{
		accessToken: token,
		expiresAt:   &now,
	}
}

func (f *driveBatchFakeVault) Renew(_ context.Context, _ int64, _ string, _ credentials.TokenRefresher) (*models.OAuthToken, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.renewCalls++
	return &models.OAuthToken{
		AccessToken: f.accessToken,
		ExpiresAt:   f.expiresAt,
	}, nil
}

// Get is the other half of credentials.VaultAPI. The crawler doesn't
// reach this path (publish_worker does), but the type system requires
// it. Returning the canned token is fine.
func (f *driveBatchFakeVault) Get(_ context.Context, _ int64, _ string) (*models.OAuthToken, error) {
	return &models.OAuthToken{
		AccessToken: f.accessToken,
		ExpiresAt:   f.expiresAt,
	}, nil
}

// Save / Rotate / Revoke are the remaining credentials.VaultAPI
// methods. The crawler calls ONLY Renew during processBatch — Save
// and Rotate happen during the OAuth callback (handlers package,
// never reached here), Revoke runs on disconnect flows (also
// outside the crawler). All three return nil + nil/sentinel so the
// interface is fully satisfied; the tests assert the crawler never
// reached them via the sentinel error.
var errFakeVaultNotImplemented = errors.New("driveBatchFakeVault: Save/Rotate/Revoke not exercised by the test — the crawler path doesn't reach them")

func (f *driveBatchFakeVault) Save(_ context.Context, _ int64, _ *models.TokenData) error {
	return errFakeVaultNotImplemented
}
func (f *driveBatchFakeVault) Rotate(_ context.Context, _ int64, _ *models.TokenData) error {
	return errFakeVaultNotImplemented
}
func (f *driveBatchFakeVault) Revoke(_ context.Context, _ int64) error {
	return errFakeVaultNotImplemented
}

// recordingLister is the in-memory DriveFolderLister used by tests
// 1-4. Records every call so the test can assert the driveId was
// threaded correctly across pages; returns a pre-programmed sequence
// of pages (the test author fills pages[] up front).
type recordingLister struct {
	mu      sync.Mutex
	calls   []listFolderCall
	pages   []listFolderPage
	pageIdx int
	listErr error // optional: force every ListFolder to error with this err
}

type listFolderCall struct {
	FolderID    string
	DriveID     string
	AccessToken string
	PageToken   string
}

type listFolderPage struct {
	Files         []services.GoogleDriveFile
	NextPageToken string
}

func (l *recordingLister) ListFolder(_ context.Context, folderID, driveID, accessToken, pageToken string) ([]services.GoogleDriveFile, string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, listFolderCall{
		FolderID:    folderID,
		DriveID:     driveID,
		AccessToken: accessToken,
		PageToken:   pageToken,
	})
	if l.listErr != nil {
		return nil, "", l.listErr
	}
	if l.pageIdx >= len(l.pages) {
		return nil, "", errors.New("recordingLister: pages exhausted — test misconfigured (call count > pages length)")
	}
	p := l.pages[l.pageIdx]
	l.pageIdx++
	return p.Files, p.NextPageToken, nil
}

// recordingInspector is the in-memory DriveFolderInspector. Records
// the call count + the FOLDER ID it was asked about + returns a
// pre-canned GoogleDriveFile (with the driveId the test wants the
// resolver to surface) OR a pre-canned error (the failure-fallback
// test).
//
// argFolderIDs is asserted in EVERY test (not just Test 1) because
// the contract — "the fileID passed to GetFileMetadata is the
// batch's SourceFolderID, not a child fileID" — is the exact thing
// a future refactor could most easily regress (especially when
// someone reorders the loop entry + the resolve call). Locking it
// globally here is cheap.
type recordingInspector struct {
	mu           sync.Mutex
	calls        int
	argFolderIDs []string // which folder_id was passed (asserted by every test below)
	driveID      string   // empty → My Drive fallback
	err          error    // non-nil → GetFileMetadata fails (warn-level recovery in crawler)
}

func (i *recordingInspector) GetFileMetadata(_ context.Context, _, fileID string) (*services.GoogleDriveFile, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.calls++
	i.argFolderIDs = append(i.argFolderIDs, fileID)
	if i.err != nil {
		return nil, i.err
	}
	return &services.GoogleDriveFile{
		ID:       fileID,
		Name:     "shared/",
		MimeType: "application/vnd.google-apps.folder",
		DriveID:  i.driveID,
	}, nil
}

// fakeProvider satisfies ALL THREE Drive-side interfaces the crawler
// type-asserts against from capRouter.Get(provider):
//
//   - services.DriveFolderLister    → ListFolder
//   - services.DriveFolderInspector → GetFileMetadata
//   - services.DriveImporter         → GetFileMetadata + RefreshOAuthToken
//   - DownloadFile
//
// DriveImporter is required because resolveFolderLister wraps the
// importer's RefreshOAuthToken into the closure passed to vault.Renew;
// without it the type assertion fails and processBatch aborts with
// `source_provider "google_drive" does not implement DriveImporter`.
//
// RefreshOAuthToken / DownloadFile bodies return the sentinel below
// because the test path doesn't actually fire them — the fake vault
// returns the canned token without invoking the refresher closure,
// and the crawler never reaches DownloadFile during a folder crawl.
type fakeProvider struct {
	Lister    *recordingLister
	Inspector *recordingInspector
}

func (p *fakeProvider) ListFolder(ctx context.Context, folderID, driveID, accessToken, pageToken string) ([]services.GoogleDriveFile, string, error) {
	return p.Lister.ListFolder(ctx, folderID, driveID, accessToken, pageToken)
}

func (p *fakeProvider) GetFileMetadata(ctx context.Context, accessToken, fileID string) (*services.GoogleDriveFile, error) {
	return p.Inspector.GetFileMetadata(ctx, accessToken, fileID)
}

func (p *fakeProvider) RefreshOAuthToken(_ context.Context, _ string) (*models.TokenData, error) {
	// Never reached on the crawler path — the fake vault returns
	// the canned token without invoking the refresher closure.
	// Returning a typed sentinel here means a future regression
	// that DOES reach this path fails the test loudly instead of
	// silently no-opping.
	return nil, errors.New("driveBatchFakeProvider: RefreshOAuthToken not exercised by the crawler test path (vault short-circuits with canned token)")
}

func (p *fakeProvider) DownloadFile(_ context.Context, _, _ string) (*http.Response, error) {
	// Never reached during folder crawling (DownloadFile is fired
	// by the upload_worker, not the crawler). Returning the sentinel
	// keeps a regression that accidentally routes through here
	// immediately visible.
	return nil, errors.New("driveBatchFakeProvider: DownloadFile not exercised by the crawler test path (only upload_worker fires this)")
}

// Compile-time interface conformance so a future signature drift in
// the service interfaces fails the build at this file rather than at
// runtime.
var (
	_ services.DriveFolderLister    = (*fakeProvider)(nil)
	_ services.DriveFolderInspector = (*fakeProvider)(nil)
	_ services.DriveImporter        = (*fakeProvider)(nil)
)

// ---------------------------------------------------------------------------
// Test harness
// ---------------------------------------------------------------------------

const (
	testWorkerID       = "test-worker-7-10"
	testFolderID       = "shared-folder-1"
	testSharedDriveID  = "0ABCshared-folder-XYZ"
	testMyDriveToken   = "ya29.fake-my-drive-fallback"
	testSharedVaultTok = "ya29.fake-shared-drive-token"
)

// assertInspectorCalledOnceWith asserts the recordingInspector was
// called exactly once with the batch's SourceFolderID. Centralised
// so the 5 tests don't drift on the exact phrasing / counter check.
func assertInspectorCalledOnceWith(t *testing.T, in *recordingInspector, expectedFolderID string) {
	t.Helper()
	if in.calls != 1 {
		t.Errorf("inspector.calls: want 1 (single resolve before loop), got %d", in.calls)
	}
	if len(in.argFolderIDs) != 1 || in.argFolderIDs[0] != expectedFolderID {
		t.Errorf("inspector.argFolderIDs: want [%q] (the batch's folder, not a child file), got %v",
			expectedFolderID, in.argFolderIDs)
	}
}

// assertVaultRenewedOnce asserts vault.Renew was called exactly once
// (proves the resolveFolderLister path executed — without this, a
// future refactor that bypasses the vault on some code path would
// silently pass the driveId threading tests).
func assertVaultRenewedOnce(t *testing.T, v *driveBatchFakeVault) {
	t.Helper()
	if v.renewCalls != 1 {
		t.Errorf("vault.Renew calls: want 1, got %d", v.renewCalls)
	}
}

// makeBatch returns a fresh ImportBatch in the right state for
// processBatch to do work: SourceDriveAccountID set (the P0 hardening
// guard short-circuits on nil), PublishScheduleStartAt in the future
// (the schedule-stagger guard pins to NOW() otherwise), full target
// list populated (publish_worker reads this but the test doesn't
// follow that deep).
func makeBatch(t *testing.T) *models.ImportBatch {
	t.Helper()
	driveAcct := int64(42)
	startAt := time.Now().Add(1 * time.Hour)
	return &models.ImportBatch{
		ID:                     uuid.New(),
		UserID:                 100,
		WorkspaceID:            200,
		SourceProvider:         "google_drive",
		SourceDriveAccountID:   &driveAcct,
		SourceFolderID:         testFolderID,
		TargetAccountIDs:       []int64{11, 12},
		PublishScheduleStartAt: startAt,
		PublishScheduleMinGap:  60,
		PublishScheduleMaxGap:  120,
		DefaultPrivacyLevel:    "unlisted",
		Status:                 models.ImportBatchStatusProcessing,
		CursorIndexedCount:     0,
	}
}

// newCrawlerForSharedDriveTests wires a DriveBatchCrawler with the
// fakes above.
//
// HeartbeatInterval is set to 5 * time.Minute (the production
// default) instead of the 50ms used in earlier iterations. The
// synchronous test thread completes processBatch in milliseconds,
// so a 5-minute heartbeat is unreachable in a single test run —
// the goroutine never ticks before `defer cancelHB()` fires. This
// eliminates a race where the heartbeat could increment
// heartbeatCalls once and surface as a spurious >0 count in any
// future assertion that locks the count.
func newCrawlerForSharedDriveTests(
	batchRepo *fakeBatchStore,
	uploadRepo *fakeUploadRepo,
	vault *driveBatchFakeVault,
	router *services.CapabilityRouter,
) *DriveBatchCrawler {
	return NewDriveBatchCrawler(
		batchRepo, uploadRepo, vault, router,
		"test-prefix",
		DriveBatchCrawlerOptions{
			LeaseTTL:          5 * time.Minute,
			HeartbeatInterval: 5 * time.Minute, // production default; unreachable in fast tests
			ReclaimOnStart:    false,
		},
		nil,
	)
}
