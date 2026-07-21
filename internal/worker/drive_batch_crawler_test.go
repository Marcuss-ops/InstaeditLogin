// Package worker tests — Task 7/10.
//
// What this file locks end-to-end:
//
//  1. The crawler resolves a folder's driveId ONCE before the pagination
//     loop via ResolveFolderDriveID (Task 6/10) — NOT per-page (would
//     halve Drive quota for no gain).
//
//  2. The resolved driveId is threaded into EVERY ListFolder call,
//     including across the nextPageToken checkpoints — Shared Drive
//     folders must keep `corpora=drive&driveId=…` scoping on every
//     request or the second page will 404.
//
//  3. My-Drive folders (driveId absent from GetFileMetadata response)
//     fall back to driveID="" → default My Drive corpus (no corpora=
//     param, pre-T6/10 back-compat).
//
//  4. A metadata-fetch failure does NOT abort the crawl — the crawler
//     logs a warn-level remediation hint and proceeds with driveID=""
//     so the operator still gets their files imported (the alternative
//     — fail-loud on metadata — would brick imports on DLP-blocked
//     folder metadata reads).
//
//  5. End-to-end: when the real *GoogleDriveOAuthService handles
//     ListFolder + the crawler threads a resolved driveId, the actual
//     files.list URL contains BOTH corpora=drive AND driveId=X AND
//     pageSize=200 AND the Authorization Bearer header — the full
//     underlying contract the user spec called out.
//
// Reference: docs/ARCHITECTURE.md §Shared Drives; internal/services/
// google_drive_oauth.go::ResolveFolderDriveID + ListFolder.
package worker

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Marcuss-ops/InstaeditLogin/internal/config"
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
// interface is fully satisfied; tests assert via assertVaultUnused
// (helper below) that the crawler never reached them.
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

// assertVaultNotImplementedPathLocked asserts the crawler did NOT
// reach Save / Rotate / Revoke — those belong to OAuth-callback and
// disconnect flows (handlers package), not the crawl loop. If a
// future regression reroutes through one of them, the test catches
// it.

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

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestProcessBatch_SharedDrive_ThreadsDriveIDAcrossPages — happy
// path lock for the user's spec line "propagare folder.driveId come
// parametro driveId a tutte le pagine successive":
//
//   - The crawler resolves a Shared Drive folder's driveId ONCE
//     (inspector.calls == 1) AND the fileID passed =
//     batch.SourceFolderID (proving the resolver is the folder, not
//     a page's child file).
//   - The driveId is threaded into ListFolder for EVERY page,
//     INCLUDING after UpdateCursor writes a non-empty nextPageToken.
//   - The vault was invoked once (proving the resolveFolderLister
//     path actually ran).
//   - Upload jobs are created for every video-shaped item; the cursor
//     reflects the page's video count + the accumulator from prior
//     pages.
func TestProcessBatch_SharedDrive_ThreadsDriveIDAcrossPages(t *testing.T) {
	batchRepo := newFakeBatchStore()
	uploadRepo := &fakeUploadRepo{}
	vault := newFakeVault(testSharedVaultTok)

	lister := &recordingLister{
		pages: []listFolderPage{
			{
				Files: []services.GoogleDriveFile{
					{ID: "f1", Name: "video1.mp4", MimeType: "video/mp4"},
					{ID: "f2", Name: "video2.mp4", MimeType: "video/mp4"},
				},
				NextPageToken: "p2",
			},
			{
				Files: []services.GoogleDriveFile{
					{ID: "f3", Name: "video3.mp4", MimeType: "video/mp4"},
				},
				NextPageToken: "",
			},
		},
	}
	inspector := &recordingInspector{driveID: testSharedDriveID}
	provider := &fakeProvider{Lister: lister, Inspector: inspector}

	router := services.NewCapabilityRouter()
	router.Register("google_drive", provider)

	batch := makeBatch(t)
	batchRepo.seedBatch(batch)

	c := newCrawlerForSharedDriveTests(batchRepo, uploadRepo, vault, router)
	c.processBatch(context.Background(), batch, testWorkerID)

	assertInspectorCalledOnceWith(t, inspector, batch.SourceFolderID)
	assertVaultRenewedOnce(t, vault)

	// ListFolder called for every page with the resolved driveID.
	if got := len(lister.calls); got != 2 {
		t.Fatalf("ListFolder call count: want 2 pages, got %d", got)
	}
	for i, call := range lister.calls {
		if call.FolderID != batch.SourceFolderID {
			t.Errorf("page %d FolderID: want %q, got %q", i, batch.SourceFolderID, call.FolderID)
		}
		if call.DriveID != testSharedDriveID {
			t.Errorf("page %d DriveID: want %q (Shared Drive scoping), got %q", i, testSharedDriveID, call.DriveID)
		}
		if call.AccessToken != testSharedVaultTok {
			t.Errorf("page %d AccessToken: want %q, got %q", i, testSharedVaultTok, call.AccessToken)
		}
	}
	// 1st page is pageToken=""; 2nd page is pageToken="p2" (the
	// nextPageToken checkpoint from the first page).
	if lister.calls[0].PageToken != "" {
		t.Errorf("page 0 PageToken: want empty (initial), got %q", lister.calls[0].PageToken)
	}
	if lister.calls[1].PageToken != "p2" {
		t.Errorf("page 1 PageToken: want %q (loop-carried), got %q", "p2", lister.calls[1].PageToken)
	}

	// UpdateCursor called per page: 1st with "p2" + 2 indexed,
	// 2nd with "" + 3 indexed (accumulator from both pages).
	if got := len(batchRepo.updateCursorHistory); got != 2 {
		t.Fatalf("UpdateCursor call count: want 2, got %d", got)
	}
	if batchRepo.updateCursorHistory[0].PageToken != "p2" || batchRepo.updateCursorHistory[0].Count != 2 {
		t.Errorf("UpdateCursor[0]: want (p2,2), got (%q,%d)",
			batchRepo.updateCursorHistory[0].PageToken, batchRepo.updateCursorHistory[0].Count)
	}
	if batchRepo.updateCursorHistory[1].PageToken != "" || batchRepo.updateCursorHistory[1].Count != 3 {
		t.Errorf("UpdateCursor[1]: want (\"\",3), got (%q,%d)",
			batchRepo.updateCursorHistory[1].PageToken, batchRepo.updateCursorHistory[1].Count)
	}

	// IncrementCreatedCount called per page with the page's video
	// delta — total 2 + 1 = 3 should not appear anywhere as a single
	// call (it's the per-page delta).
	if got := len(batchRepo.incrementCalls); got != 2 {
		t.Fatalf("IncrementCreatedCount call count: want 2 (one per page), got %d", got)
	}
	if batchRepo.incrementCalls[0] != 2 {
		t.Errorf("IncrementCreatedCount[0]: want 2 (2 videos on page 1), got %d", batchRepo.incrementCalls[0])
	}
	if batchRepo.incrementCalls[1] != 1 {
		t.Errorf("IncrementCreatedCount[1]: want 1 (1 video on page 2), got %d", batchRepo.incrementCalls[1])
	}

	// MarkCompleted called once, MarkFailed never called (success).
	if batchRepo.markCompletedCalls != 1 {
		t.Errorf("MarkCompleted: want 1 (terminal success), got %d", batchRepo.markCompletedCalls)
	}
	if len(batchRepo.markFailedCalls) != 0 {
		t.Errorf("MarkFailed: want 0 (success path), got %d: %v",
			len(batchRepo.markFailedCalls), batchRepo.markFailedCalls)
	}

	// All 3 video files turned into upload_jobs.
	if got := len(uploadRepo.created); got != 3 {
		t.Errorf("upload_jobs created: want 3 (every video file), got %d", got)
	}
}

// TestProcessBatch_MyDrive_FallsBackToEmptyDriveIDPages — verifies
// the My Drive corpus fallback: GetFileMetadata returns driveId=""
// (the standard My Drive response shape) → the crawler threads ""
// into ListFolder for every page. Pre-T6/10 back-compat preserved.
func TestProcessBatch_MyDrive_FallsBackToEmptyDriveIDPages(t *testing.T) {
	batchRepo := newFakeBatchStore()
	uploadRepo := &fakeUploadRepo{}
	vault := newFakeVault(testMyDriveToken)

	lister := &recordingLister{
		pages: []listFolderPage{
			{
				Files: []services.GoogleDriveFile{
					{ID: "f1", Name: "v1.mp4", MimeType: "video/mp4"},
				},
				NextPageToken: "p2",
			},
			{
				Files:         []services.GoogleDriveFile{},
				NextPageToken: "",
			},
		},
	}
	inspector := &recordingInspector{driveID: ""} // My Drive: no driveId
	provider := &fakeProvider{Lister: lister, Inspector: inspector}
	router := services.NewCapabilityRouter()
	router.Register("google_drive", provider)

	batch := makeBatch(t)
	batchRepo.seedBatch(batch)

	c := newCrawlerForSharedDriveTests(batchRepo, uploadRepo, vault, router)
	c.processBatch(context.Background(), batch, testWorkerID)

	assertInspectorCalledOnceWith(t, inspector, batch.SourceFolderID)
	assertVaultRenewedOnce(t, vault)

	if got := len(lister.calls); got != 2 {
		t.Fatalf("ListFolder call count: want 2, got %d", got)
	}
	for i, call := range lister.calls {
		if call.DriveID != "" {
			t.Errorf("page %d DriveID: want empty string (My Drive fallback), got %q", i, call.DriveID)
		}
	}
	if batchRepo.markCompletedCalls != 1 {
		t.Errorf("MarkCompleted: want 1, got %d", batchRepo.markCompletedCalls)
	}
	if len(batchRepo.markFailedCalls) != 0 {
		t.Errorf("MarkFailed: want 0, got: %v", batchRepo.markFailedCalls)
	}
}

// TestProcessBatch_MetadataFetchFails_BestEffortEmptyDriveID —
// verifies the failure-fallback path: a typed network/5xx on
// GetFileMetadata → the crawler logs a warn-level remediation hint
// AND proceeds with driveID="" (My Drive fallback). The crawl does
// NOT abort; the operator still gets the import to succeed with the
// caveat that Shared Drive scoping wasn't applied (the warn-level
// log line is the operator-side signal to retry the import).
func TestProcessBatch_MetadataFetchFails_BestEffortEmptyDriveID(t *testing.T) {
	batchRepo := newFakeBatchStore()
	uploadRepo := &fakeUploadRepo{}
	vault := newFakeVault(testMyDriveToken)

	lister := &recordingLister{
		pages: []listFolderPage{
			{
				Files: []services.GoogleDriveFile{
					{ID: "f1", Name: "v.mp4", MimeType: "video/mp4"},
				},
				NextPageToken: "",
			},
		},
	}
	inspector := &recordingInspector{
		err: errors.New("simulated 503 on folder metadata (DLP-blocked metadata read)"),
	}
	provider := &fakeProvider{Lister: lister, Inspector: inspector}
	router := services.NewCapabilityRouter()
	router.Register("google_drive", provider)

	batch := makeBatch(t)
	batchRepo.seedBatch(batch)

	c := newCrawlerForSharedDriveTests(batchRepo, uploadRepo, vault, router)
	c.processBatch(context.Background(), batch, testWorkerID)

	assertInspectorCalledOnceWith(t, inspector, batch.SourceFolderID)
	// vault.Renew ran BEFORE the resolver fired (resolveFolderLister
	// always hydrates the bearer first). Locks the invariant that
	// the resolve path is unconditional — a future regression that
	// bypasses the vault on the error path would not surface here
	// without this assertion.
	assertVaultRenewedOnce(t, vault)

	// ListFolder STILL called with the My Drive fallback driveID=""
	// (the processBatch loop is not aborted by the metadata failure).
	if got := len(lister.calls); got != 1 {
		t.Fatalf("ListFolder call count: want 1 (proceeds despite metadata fail), got %d", got)
	}
	if lister.calls[0].DriveID != "" {
		t.Errorf("ListFolder DriveID: want empty (My Drive fallback), got %q", lister.calls[0].DriveID)
	}
	// Best-effort continued: MarkCompleted called once, MarkFailed never.
	if batchRepo.markCompletedCalls != 1 {
		t.Errorf("MarkCompleted: want 1 (best-effort continues despite metadata fail), got %d", batchRepo.markCompletedCalls)
	}
	if len(batchRepo.markFailedCalls) != 0 {
		t.Errorf("MarkFailed: want 0 (best-effort continues), got: %v", batchRepo.markFailedCalls)
	}
}

// TestProcessBatch_ResolveMetadataExactlyOnce_NotPerPage — the
// efficiency invariant: across N pages, GetFileMetadata is called
// exactly ONCE (not per-page). Per-page resolve would halve the
// Drive API quota available for content listing — folders don't
// move between corpora mid-crawl so re-resolving is wasted work.
func TestProcessBatch_ResolveMetadataExactlyOnce_NotPerPage(t *testing.T) {
	batchRepo := newFakeBatchStore()
	uploadRepo := &fakeUploadRepo{}
	vault := newFakeVault(testSharedVaultTok)

	lister := &recordingLister{
		pages: []listFolderPage{
			{Files: []services.GoogleDriveFile{{ID: "a", Name: "a.mp4", MimeType: "video/mp4"}}, NextPageToken: "p2"},
			{Files: []services.GoogleDriveFile{{ID: "b", Name: "b.mp4", MimeType: "video/mp4"}}, NextPageToken: "p3"},
			{Files: []services.GoogleDriveFile{{ID: "c", Name: "c.mp4", MimeType: "video/mp4"}}, NextPageToken: ""},
		},
	}
	inspector := &recordingInspector{driveID: testSharedDriveID}
	provider := &fakeProvider{Lister: lister, Inspector: inspector}
	router := services.NewCapabilityRouter()
	router.Register("google_drive", provider)

	batch := makeBatch(t)
	batchRepo.seedBatch(batch)

	c := newCrawlerForSharedDriveTests(batchRepo, uploadRepo, vault, router)
	c.processBatch(context.Background(), batch, testWorkerID)

	assertInspectorCalledOnceWith(t, inspector, batch.SourceFolderID)
	assertVaultRenewedOnce(t, vault)
	if got := len(lister.calls); got != 3 {
		t.Fatalf("ListFolder call count: want 3 pages, got %d", got)
	}
	// All 3 pages threaded the same driveID (the resolved value
	// captured before the loop entry).
	for i, call := range lister.calls {
		if call.DriveID != testSharedDriveID {
			t.Errorf("page %d DriveID want %q, got %q", i, testSharedDriveID, call.DriveID)
		}
	}
	if batchRepo.markCompletedCalls != 1 {
		t.Errorf("MarkCompleted: want 1, got %d", batchRepo.markCompletedCalls)
	}
}

// TestProcessBatch_FilesListIntegration_CorporaAndDriveIdInQuery —
// the END-TO-END verification of the user's spec line "verifica del
// parametro passato a files.list":
//
//   - The capRouter is wired with a REAL *GoogleDriveOAuthService
//     (not a recordingLister).
//   - The service is hydrated via NewGoogleDriveOAuthService +
//     a redirectingRoundTripper that re-points every URL to an
//     httptest.Server that pretends to be Drive's v3 API.
//   - The httptest.Server captures every request's URL + Authorization
//     header + replies with the share-drive JSON (folder metadata) →
//     2 pages of files.list (1 video on each page, then
//     nextPageToken="").
//
// After processBatch returns, the captured URLs are the ACTUAL
// files.list URLs the service sent. We assert:
//
//  1. files.get was called once for the folder (Task 6/10 resolve).
//  2. files.list was called twice (per-page iteration).
//  3. BOTH files.list URLs contain corpora=drive AND driveId=<resolved>
//     AND pageSize=200 (the underlying contracts the user spec is
//     locking down).
//  4. BOTH files.list requests carried the Authorization Bearer header
//     with the vault-supplied token.
//  5. The folder metadata response was honored end-to-end (the
//     driveId resolved from that response is what flows into the URL).
//
// This is the highest-fidelity test in this file — it doesn't fake
// the real lister's URL-building path, so a future regression that
// drops driveId plumbing inside ListFolder would surface here as a
// missing parameter in the recorded URL.
func TestProcessBatch_FilesListIntegration_CorporaAndDriveIdInQuery(t *testing.T) {
	const (
		folderID      = "shared-folder-e2e"
		sharedDriveID = "0ABCshared-e2e-folder"
		accessToken   = "ya29.crawler-e2e-fake-token"
	)

	var filesListURLs []string
	var filesListAuthHeaders []string
	var folderGetURL string
	var folderGetAuthHeader string
	var folderGetCalls int
	var urlMu sync.Mutex

	driveSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		// files.get for the folder — fired by GetFileMetadata during resolve.
		case req.Method == http.MethodGet && strings.HasSuffix(req.URL.Path, "/"+folderID):
			urlMu.Lock()
			folderGetURL = req.URL.String()
			folderGetAuthHeader = req.Header.Get("Authorization")
			folderGetCalls++
			urlMu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{
				"id": %q,
				"name": "shared",
				"mimeType": "application/vnd.google-apps.folder",
				"driveId": %q
			}`, folderID, sharedDriveID)
		// files.list endpoint — fired by ListFolder on every page.
		case req.Method == http.MethodGet && strings.Contains(req.URL.Path, "/drive/v3/files"):
			urlMu.Lock()
			filesListURLs = append(filesListURLs, req.URL.String())
			filesListAuthHeaders = append(filesListAuthHeaders, req.Header.Get("Authorization"))
			urlMu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			if req.URL.Query().Get("pageToken") == "" {
				_, _ = w.Write([]byte(`{
					"files": [{"id": "v1", "name": "v1.mp4", "mimeType": "video/mp4"}],
					"nextPageToken": "p2"
				}`))
			} else {
				_, _ = w.Write([]byte(`{
					"files": [{"id": "v2", "name": "v2.mp4", "mimeType": "video/mp4"}],
					"nextPageToken": ""
				}`))
			}
		default:
			t.Logf("unexpected request: %s %s", req.Method, req.URL.String())
			http.Error(w, "unexpected request", http.StatusBadRequest)
		}
	}))
	defer driveSrv.Close()

	driveSrvURL, err := url.Parse(driveSrv.URL)
	if err != nil {
		t.Fatalf("parse driveSrv URL: %v", err)
	}

	// Real *services.GoogleDriveOAuthService with a redirecting
	// RoundTripper that re-points every URL to the httptest.Server.
	// We construct via the public constructor + ProviderDependencies,
	// so this test compiles despite living in package worker (the
	// service's internal fields stay unexported on our side).
	realSvc, err := services.NewGoogleDriveOAuthService(
		&config.Config{
			GoogleDriveClientID:     "test-client",
			GoogleDriveClientSecret: "test-secret",
		},
		services.ProviderDependencies{
			HTTPClient: &http.Client{
				Transport: &driveBatchE2ERoundTripper{target: driveSrvURL},
			},
		},
	)
	if err != nil || realSvc == nil {
		t.Fatalf("NewGoogleDriveOAuthService returned nil: err=%v", err)
	}

	// Verify the service satisfies both interfaces the crawler needs.
	if _, ok := any(realSvc).(services.DriveFolderLister); !ok {
		t.Fatal("realSvc does not implement DriveFolderLister")
	}
	if _, ok := any(realSvc).(services.DriveFolderInspector); !ok {
		t.Fatal("realSvc does not implement DriveFolderInspector")
	}

	batchRepo := newFakeBatchStore()
	uploadRepo := &fakeUploadRepo{}
	vault := newFakeVault(accessToken)

	router := services.NewCapabilityRouter()
	router.Register("google_drive", realSvc)

	batch := makeBatch(t)
	batch.ID = uuid.MustParse("00000000-0000-0000-0000-000000000001")
	batch.SourceFolderID = folderID
	batchRepo.seedBatch(batch)

	c := newCrawlerForSharedDriveTests(batchRepo, uploadRepo, vault, router)
	c.processBatch(context.Background(), batch, testWorkerID)

	urlMu.Lock()
	defer urlMu.Unlock()

	// files.get fired exactly once for the folder.
	if folderGetCalls != 1 {
		t.Errorf("folder files.get call count: want 1 (resolver fires once), got %d", folderGetCalls)
	}
	if folderGetURL == "" {
		t.Fatalf("folder files.get URL: want non-empty (resolver must call GetFileMetadata once), got empty")
	}
	if !strings.Contains(folderGetURL, "supportsAllDrives=true") {
		t.Errorf("folder files.get URL: want supportsAllDrives=true, got %q", folderGetURL)
	}
	if folderGetAuthHeader != "Bearer "+accessToken {
		t.Errorf("folder files.get Authorization header: want %q, got %q", "Bearer "+accessToken, folderGetAuthHeader)
	}

	// files.list fired TWICE (per-page iteration), with the resolved
	// driveId threaded in BOTH calls — the user's spec line expressed
	// as a URL assertion.
	if len(filesListURLs) != 2 {
		t.Fatalf("files.list URL captures: want 2 (pages), got %d: %v", len(filesListURLs), filesListURLs)
	}
	for i, rawURL := range filesListURLs {
		parsed, err := url.Parse(rawURL)
		if err != nil {
			t.Errorf("page %d URL parse: %v (raw=%s)", i, err, rawURL)
			continue
		}
		q := parsed.Query()
		// 1. supportsAllDrives + includeItemsFromAllDrives are the
		//    base Shared-Drive compatibility flags (always-on).
		if q.Get("supportsAllDrives") != "true" {
			t.Errorf("page %d: supportsAllDrives: want true, got %q", i, q.Get("supportsAllDrives"))
		}
		if q.Get("includeItemsFromAllDrives") != "true" {
			t.Errorf("page %d: includeItemsFromAllDrives: want true, got %q", i, q.Get("includeItemsFromAllDrives"))
		}
		// 2. corpora=drive is the corpus-scoped LIST contract — the
		//    user's "corpora=Shared Drive" requirement made literal.
		if q.Get("corpora") != "drive" {
			t.Errorf("page %d: corpora: want \"drive\", got %q (this means the resolved driveId didn't flow into ListFolder)",
				i, q.Get("corpora"))
		}
		// 3. driveId=<resolved> is the Shared Drive scoping.
		if q.Get("driveId") != sharedDriveID {
			t.Errorf("page %d: driveId: want %q (resolved from folder metadata), got %q",
				i, sharedDriveID, q.Get("driveId"))
		}
		// 4. pageSize is the production page-cap invariant (200).
		if q.Get("pageSize") != "200" {
			t.Errorf("page %d: pageSize: want \"200\" (production page cap), got %q", i, q.Get("pageSize"))
		}
		// 5. access_token (the vault-supplied bearer) is present in
		//    the URL query (production also adds Authorization header
		//    — see assertion 6).
		if q.Get("access_token") != accessToken {
			t.Errorf("page %d: access_token: want %q, got %q", i, accessToken, q.Get("access_token"))
		}
		// 6. Authorization Bearer header carries the same token —
		//    locks the dual-channel (URL + header) contract.
		if filesListAuthHeaders[i] != "Bearer "+accessToken {
			t.Errorf("page %d: Authorization header: want %q, got %q",
				i, "Bearer "+accessToken, filesListAuthHeaders[i])
		}
		// 7. pageToken flows correctly across pages.
		if i == 0 && q.Get("pageToken") != "" {
			t.Errorf("page 0: pageToken: want empty (initial), got %q", q.Get("pageToken"))
		}
		if i == 1 && q.Get("pageToken") != "p2" {
			t.Errorf("page 1: pageToken: want \"p2\" (loop-carried), got %q", q.Get("pageToken"))
		}
	}

	// Successful terminal transition.
	if batchRepo.markCompletedCalls != 1 {
		t.Errorf("MarkCompleted: want 1, got %d", batchRepo.markCompletedCalls)
	}
	if len(batchRepo.markFailedCalls) != 0 {
		t.Errorf("MarkFailed: want 0 (success), got: %v", batchRepo.markFailedCalls)
	}
	if got := len(uploadRepo.created); got != 2 {
		t.Errorf("upload_jobs created: want 2 (1 file per page), got %d", got)
	}
}

// driveBatchE2ERoundTripper is the worker's test-side replacement for
// the services package's `redirectingRoundTripper` (which lives in
// internal/services/google_drive_oauth_resolve_test.go and isn't
// exported). Rewrites scheme + host to the test server while keeping
// path + query intact so production code's URL-building runs
// unchanged.
type driveBatchE2ERoundTripper struct {
	target *url.URL
}

func (r *driveBatchE2ERoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = r.target.Scheme
	req.URL.Host = r.target.Host
	return http.DefaultTransport.RoundTrip(req)
}
