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
//
// File layout:
//   - drive_batch_crawler_test.go            — package doc + unit tests
//     (driveId resolution/threading, fallbacks, resolve-once invariant)
//   - drive_batch_crawler_test_helpers_test.go — fakes + test harness
//   - drive_batch_crawler_e2e_test.go        — files.list URL e2e test
package worker

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestProcessBatch_FailurePersistsNormalizedProviderClass(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{name: "429", err: &services.ProviderError{Code: services.ErrorCodeRateLimited, Platform: "google_drive", StatusCode: 429}, want: "rate_limited"},
		{name: "503", err: &services.ProviderError{Code: services.ErrorCodeProviderUnavailable, Platform: "google_drive", StatusCode: 503}, want: "provider_unavailable"},
		{name: "auth", err: &services.ProviderError{Code: services.ErrorCodeAuthenticationError, Platform: "google_drive", StatusCode: 401}, want: "authentication_error"},
		{name: "permanent", err: &services.ProviderError{Code: services.ErrorCodeValidationError, Platform: "google_drive", StatusCode: 400}, want: "validation_error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			batchRepo := newFakeBatchStore()
			uploadRepo := &fakeUploadRepo{}
			vault := newFakeVault(testMyDriveToken)
			lister := &recordingLister{listErr: tc.err}
			inspector := &recordingInspector{}
			router := services.NewCapabilityRouter()
			router.Register("google_drive", &fakeProvider{Lister: lister, Inspector: inspector})
			batch := makeBatch(t)
			batchRepo.seedBatch(batch)

			c := newCrawlerForSharedDriveTests(batchRepo, uploadRepo, vault, router)
			c.processBatch(context.Background(), batch, testWorkerID)

			if len(batchRepo.markFailedCalls) != 1 {
				t.Fatalf("MarkFailed calls=%d, want 1", len(batchRepo.markFailedCalls))
			}
			if !strings.Contains(batchRepo.markFailedCalls[0], tc.want) {
				t.Fatalf("normalized failure=%q, want %q", batchRepo.markFailedCalls[0], tc.want)
			}
		})
	}
}

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
