package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// TestUploadsBatchByFolder_HappyPath_ThreePages_FlattenedEntryList
// verifies the SPA-facing contract: one HTTP call produces a single
// JSON document with every page's entries flattened, monotonically
// increasing global Index, and a single first/last scheduled_at that
// spans the full folder.
func TestUploadsBatchByFolder_HappyPath_ThreePages_FlattenedEntryList(t *testing.T) {
	lister := &mockDriveFolderLister{
		pagesFn: func(pageToken string) ([]services.GoogleDriveFile, string, error) {
			switch pageToken {
			case "":
				return []services.GoogleDriveFile{
					{ID: "p1-a", Name: "p1-a.mp4", MimeType: "video/mp4"},
					{ID: "p1-b", Name: "p1-b.mp4", MimeType: "video/mp4"},
				}, "tok-2", nil
			case "tok-2":
				return []services.GoogleDriveFile{
					{ID: "p2-a", Name: "p2-a.mp4", MimeType: "video/mp4"},
				}, "tok-3", nil
			case "tok-3":
				return []services.GoogleDriveFile{
					{ID: "p3-a", Name: "p3-a.mp4", MimeType: "video/mp4"},
					{ID: "p3-b", Name: "p3-b.mp4", MimeType: "video/mp4"},
					{ID: "p3-c", Name: "p3-c.mp4", MimeType: "video/mp4"},
				}, "", nil // DONE
			default:
				return nil, "", fmt.Errorf("unexpected pageToken in mock: %q", pageToken)
			}
		},
	}
	store := &mockUploadJobStore{}
	idemStore := newMockIdempotencyStore()
	r := newBatchImportTestRouterWithIdem(lister, store, idemStore)

	w := runUploadsBatchByFolderPost(t, r, `{"folder_id":"fid","workspace_id":1,"facebook_account_id":50, "drive_account_id":99}`, "")

	if w.Code != http.StatusAccepted {
		t.Fatalf("want 202, got %d: %s", w.Code, w.Body.String())
	}
	if lister.listCallCount != 3 {
		t.Errorf("server should serve 3 pages, got %d", lister.listCallCount)
	}
	var resp struct {
		FolderID       string `json:"folder_id"`
		ScheduledCount int    `json:"scheduled_count"`
		PageCount      int    `json:"page_count"`
		Entries        []struct {
			Index       int    `json:"index"`
			JobID       int64  `json:"job_id"`
			Name        string `json:"name"`
			ScheduledAt string `json:"scheduled_at"`
		} `json:"entries"`
		FirstPublishAt  string `json:"first_publish_at"`
		LastScheduledAt string `json:"last_scheduled_at"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.PageCount != 3 {
		t.Errorf("page_count: want 3, got %d", resp.PageCount)
	}
	if resp.ScheduledCount != 6 {
		t.Errorf("scheduled_count: want 6 (2+1+3), got %d", resp.ScheduledCount)
	}
	if len(resp.Entries) != 6 {
		t.Fatalf("entries: want 6, got %d", len(resp.Entries))
	}
	// Globally monotonic Index across pages — Index 4 belongs to
	// page 3's first file, NOT 0 (which would indicate page-local
	// numbering leak).
	wantIndices := []int{0, 1, 2, 3, 4, 5}
	for i, e := range resp.Entries {
		if e.Index != wantIndices[i] {
			t.Errorf("entry %d index: want %d, got %d", i, wantIndices[i], e.Index)
		}
	}
	// First / last scheduled_at span the full timeline.
	if resp.FirstPublishAt == "" || resp.LastScheduledAt == "" {
		t.Errorf("first_publish_at / last_scheduled_at should be set when entries > 0")
	}
	if len(store.jobs) != 6 {
		t.Errorf("upload_jobs persisted: want 6, got %d", len(store.jobs))
	}
}

// TestUploadsBatchByFolder_ConfigGap_Returns200WithGuidance pins the
// dedicated path: server is missing GOOGLE_DRIVE_API_KEY AND the
// caller did not supply drive_account_id — the handler returns 200
// (not 5xx) with the structured hint so the SPA can render a CTA.
func TestUploadsBatchByFolder_ConfigGap_Returns200WithGuidance(t *testing.T) {
	lister := &mockDriveFolderLister{
		listErr: fmt.Errorf("%w: GOOGLE_DRIVE_API_KEY not configured", services.ErrDriveListRequiresAPIKey),
	}
	store := &mockUploadJobStore{}
	r := newBatchImportTestRouter(lister, store)

	w := runUploadsBatchByFolderPost(t, r, `{"folder_id":"fid","workspace_id":1,"facebook_account_id":50, "drive_account_id":99}`, "")

	if w.Code != http.StatusOK {
		t.Fatalf("want 200 (config gap is operator-fixable), got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		NeedsGoogleDriveAPIKey bool   `json:"needs_google_drive_api_key"`
		NeedsDriveAccount      bool   `json:"needs_drive_account"`
		Note                   string `json:"note"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.NeedsGoogleDriveAPIKey {
		t.Errorf("NeedsGoogleDriveAPIKey must be true on sentinel")
	}
	// NeedsDriveAccount is FALSE when the request body already
	// supplied a drive_account_id: the handler's hint is only
	// surfaced when the caller needs to ALSO link a Drive account.
	// (Updated from MUST-be-true to match the handler's post-2026
	// semantic that the request body's drive_account_id is
	// honoured as an alternative to API-key-only mode.)
	if resp.NeedsDriveAccount {
		t.Errorf("NeedsDriveAccount must be false when drive_account_id is supplied in body")
	}
	if !strings.Contains(resp.Note, "GOOGLE_DRIVE_API_KEY") {
		t.Errorf("note must mention GOOGLE_DRIVE_API_KEY, got: %q", resp.Note)
	}
}

// TestUploadsBatchByFolder_EmptyFolder_ReturnsOkWithNote ensures an
// empty Drive folder (page 1 returns 0 files + empty next_token) maps
// to 200 with a human-readable note and ScheduledCount=0.
func TestUploadsBatchByFolder_EmptyFolder_ReturnsOkWithNote(t *testing.T) {
	lister := &mockDriveFolderLister{
		files:         nil, // static path: returns nil on every call
		nextPageToken: "",
	}
	store := &mockUploadJobStore{}
	r := newBatchImportTestRouter(lister, store)

	w := runUploadsBatchByFolderPost(t, r, `{"folder_id":"empty","workspace_id":1,"facebook_account_id":50, "drive_account_id":99}`, "")

	if w.Code != http.StatusOK {
		t.Fatalf("want 200 (empty folder is operator-actionable info, not error), got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		ScheduledCount int    `json:"scheduled_count"`
		PageCount      int    `json:"page_count"`
		Note           string `json:"note"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ScheduledCount != 0 {
		t.Errorf("scheduled_count: want 0, got %d", resp.ScheduledCount)
	}
	if resp.PageCount != 1 {
		t.Errorf("page_count: want 1, got %d", resp.PageCount)
	}
	if resp.Note == "" {
		t.Errorf("note must be set so SPA can render 'no videos found'")
	}
}

// TestUploadsBatchByFolder_CapExceeded_Returns413 verifies the
// driveBatchMaxPages=50 cap. After 50 successful pages, the 51st
// call from Drive would exceed the cap → 413 + clear guidance.
func TestUploadsBatchByFolder_CapExceeded_Returns413(t *testing.T) {
	// Drive returns 1 file + a different next_token 50 times.
	tokens := make([]string, 0, 50)
	for i := 0; i < 50; i++ {
		tokens = append(tokens, fmt.Sprintf("tok-%02d", i))
	}
	lister := &mockDriveFolderLister{
		pagesFn: func(pageToken string) ([]services.GoogleDriveFile, string, error) {
			if pageToken == "" {
				return []services.GoogleDriveFile{{ID: "p0", Name: "p0.mp4", MimeType: "video/mp4"}}, tokens[0], nil
			}
			for i, t := range tokens {
				if pageToken == t {
					if i == 49 {
						// We'd loop; cap should kick in before this.
						return []services.GoogleDriveFile{{ID: "pN", Name: "pN.mp4"}}, "", nil
					}
					return []services.GoogleDriveFile{{ID: fmt.Sprintf("p%d", i+1), Name: fmt.Sprintf("p%d.mp4", i+1)}}, tokens[i+1], nil
				}
			}
			return nil, "", fmt.Errorf("unexpected token %q", pageToken)
		},
	}
	store := &mockUploadJobStore{}
	r := newBatchImportTestRouter(lister, store)

	w := runUploadsBatchByFolderPost(t, r, `{"folder_id":"huge","workspace_id":1,"facebook_account_id":50, "drive_account_id":99}`, "")

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("want 413 (cap exceeded), got %d: %s", w.Code, w.Body.String())
	}
	if lister.listCallCount != 50 {
		t.Errorf("cap MUST short-circuit BEFORE the 51st listing call: want exactly 50 calls, got %d (a 51st call means the cap fires AFTER listing, wasting an upstream slot)", lister.listCallCount)
	}
	// 50 already-created upload_jobs stay queued (no rollback).
	if len(store.jobs) != 50 {
		t.Errorf("upload_jobs already queued: want 50 (no rollback), got %d", len(store.jobs))
	}
}

// TestUploadsBatchByFolder_PartialFailure_MidPagination_ReturnsPartialState:
// pages 1+2 succeed, page 3 upstream blips. Handler should return
// 200 with partial_failure=true + entries_so_far in entries[] +
// failed_at_page_token + note pointing the operator to resume
// manually via the existing single-page endpoint.
func TestUploadsBatchByFolder_PartialFailure_MidPagination_ReturnsPartialState(t *testing.T) {
	var calls int
	lister := &mockDriveFolderLister{
		pagesFn: func(pageToken string) ([]services.GoogleDriveFile, string, error) {
			calls++
			switch calls {
			case 1:
				return []services.GoogleDriveFile{{ID: "p1", Name: "p1.mp4"}}, "tok-2", nil
			case 2:
				return []services.GoogleDriveFile{
					{ID: "p2-a", Name: "p2-a.mp4"},
					{ID: "p2-b", Name: "p2-b.mp4"},
				}, "tok-FAILED", nil // Drive returns token + fails on next call
			default:
				return nil, "", fmt.Errorf("upstream blip on call %d", calls)
			}
		},
	}
	store := &mockUploadJobStore{}
	r := newBatchImportTestRouter(lister, store)

	w := runUploadsBatchByFolderPost(t, r, `{"folder_id":"fid","workspace_id":1,"facebook_account_id":50, "drive_account_id":99}`, "")

	if w.Code != http.StatusAccepted {
		t.Fatalf("want 202 with partial_failure=true (NOT a 5xx — operator can resume), got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		ScheduledCount    int    `json:"scheduled_count"`
		PageCount         int    `json:"page_count"`
		PartialFailure    bool   `json:"partial_failure"`
		FailedAtPageToken string `json:"failed_at_page_token"`
		FailedAtPage      int    `json:"failed_at_page"`
		Note              string `json:"note"`
		Entries           []struct {
			Index int    `json:"index"`
			Name  string `json:"name"`
		} `json:"entries"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.PartialFailure {
		t.Errorf("partial_failure must be true after mid-pagination upstream blip")
	}
	if resp.FailedAtPage != 3 {
		t.Errorf("failed_at_page: want 3 (call count when blip happened), got %d", resp.FailedAtPage)
	}
	if resp.FailedAtPageToken != "tok-FAILED" {
		t.Errorf("failed_at_page_token: want the Drive token from page 2's response, got %q", resp.FailedAtPageToken)
	}
	if resp.ScheduledCount != 3 {
		t.Errorf("scheduled_count: want 3 (1 from p1 + 2 from p2), got %d", resp.ScheduledCount)
	}
	if len(resp.Entries) != 3 {
		t.Errorf("entries: want 3 in the partial response, got %d", len(resp.Entries))
	}
	if len(store.jobs) != 3 {
		t.Errorf("upload_jobs queued: want 3 (no rollback on partial), got %d", len(store.jobs))
	}
	if !strings.Contains(resp.Note, "resume") {
		t.Errorf("note must guide operator to resume manually, got: %q", resp.Note)
	}
}

// TestUploadsBatchByFolder_Idempotency_Replay_FullResponse pins the
// byte-for-byte replay contract. After a successful first call,
// retrying with the same key + same body hash should return the
// cached response verbatim (no new upload_jobs).
func TestUploadsBatchByFolder_Idempotency_Replay_FullResponse(t *testing.T) {
	lister := &mockDriveFolderLister{
		pagesFn: func(pageToken string) ([]services.GoogleDriveFile, string, error) {
			if pageToken == "" {
				return []services.GoogleDriveFile{
					{ID: "p1-a", Name: "p1-a.mp4"},
					{ID: "p1-b", Name: "p1-b.mp4"},
				}, "", nil
			}
			return nil, "", fmt.Errorf("unexpected token")
		},
	}
	store := &mockUploadJobStore{}
	idemStore := newMockIdempotencyStore()
	r := newBatchImportTestRouterWithIdem(lister, store, idemStore)

	body := `{"folder_id":"fid","workspace_id":1,"facebook_account_id":50, "drive_account_id":99}`
	const idemKey = "byfolder-replay-key"
	w1 := runUploadsBatchByFolderPost(t, r, body, idemKey)
	if w1.Code != http.StatusAccepted {
		t.Fatalf("first call want 202, got %d: %s", w1.Code, w1.Body.String())
	}
	firstWire := w1.Body.Bytes()
	firstJobCount := len(store.jobs)

	w2 := runUploadsBatchByFolderPost(t, r, body, idemKey)
	if w2.Code != http.StatusAccepted {
		t.Fatalf("replay want 202, got %d: %s", w2.Code, w2.Body.String())
	}
	if !bytes.Equal(w2.Body.Bytes(), firstWire) {
		t.Errorf("replay bytes differ from original wire bytes\n   wire:  %q\n   cache: %q",
			string(firstWire), string(w2.Body.Bytes()))
	}
	if len(store.jobs) != firstJobCount {
		t.Errorf("replay must NOT create new upload_jobs; want %d, got %d", firstJobCount, len(store.jobs))
	}
}

// TestUploadsBatchByFolder_PartialFailure_DoesNotCache: a partial
// failure response must NOT be cached, because retrying should
// re-run from page 1 to converge on truth (the cached bytes would
// otherwise mislead future replays into thinking the partial state
// is the final state).
func TestUploadsBatchByFolder_PartialFailure_DoesNotCache(t *testing.T) {
	var calls int
	lister := &mockDriveFolderLister{
		pagesFn: func(pageToken string) ([]services.GoogleDriveFile, string, error) {
			calls++
			if calls == 1 {
				return []services.GoogleDriveFile{{ID: "p1", Name: "p1.mp4"}}, "tok-FAIL", nil
			}
			return nil, "", fmt.Errorf("upstream blip")
		},
	}
	store := &mockUploadJobStore{}
	idemStore := newMockIdempotencyStore()
	r := newBatchImportTestRouterWithIdem(lister, store, idemStore)

	const idemKey = "byfolder-partial-no-cache"
	body := `{"folder_id":"fid","workspace_id":1,"facebook_account_id":50, "drive_account_id":99}`
	w := runUploadsBatchByFolderPost(t, r, body, idemKey)
	if w.Code != http.StatusAccepted {
		t.Fatalf("partial-failure want 202, got %d: %s", w.Code, w.Body.String())
	}
	// Cache MUST NOT have a row for this (workspace, key) tuple.
	rec, err := idemStore.FindActiveByKey(1, idemKey, time.Now())
	if err != nil {
		t.Fatalf("FindActiveByKey: %v", err)
	}
	if rec != nil {
		t.Errorf("partial failure response must NOT be cached; got record id=%d", rec.ID)
	}
}

// TestUploadsBatchByFolder_NonOwnerWorkspace_Returns403 protects the
// workspace-isolation contract: a caller passing another tenant's
// workspace_id in body MUST be rejected before any listing or
// job creation. The same defence-in-depth ordering as
// handleDriveBatchImport.
func TestUploadsBatchByFolder_NonOwnerWorkspace_Returns403(t *testing.T) {
	lister := &mockDriveFolderLister{}
	store := &mockUploadJobStore{}
	capRouter := services.NewCapabilityRouter()
	capRouter.Register("google-drive", lister)
	wsStore := &mockWorkspaceStore{
		findByIDFn: func(id int64) (*models.Workspace, error) {
			return &models.Workspace{ID: id, Name: "Other", OwnerID: 2}, nil // owned by user 2, NOT caller (1)
		},
	}
	userStore := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return nil, nil
		},
		listFn: func(userID int64, _ string) ([]*models.PlatformAccount, error) {
			return nil, nil
		},
	}
	r := NewRouter(
		capRouter,
		userStore,
		auth.NewManager(testJWTSecret, 24),
		"",
		nil,
		WithWorkspaceStore(wsStore),
		WithUploadJobStore(store), WithOneTimeCodeStore(NewInMemoryOneTimeCodeStore(60*time.Second)))

	w := runUploadsBatchByFolderPost(t, r, `{"folder_id":"fid","workspace_id":1,"facebook_account_id":50, "drive_account_id":99}`, "")

	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403 (workspace not owned by caller), got %d: %s", w.Code, w.Body.String())
	}
	if lister.listCallCount != 0 {
		t.Errorf("lister must NOT be called when ownership gate fails, got %d calls", lister.listCallCount)
	}
	if len(store.jobs) != 0 {
		t.Errorf("no upload_jobs should be created when ownership gate fails, got %d", len(store.jobs))
	}
}

// TestUploadsBatchByFolder_CumulativeStagger_ClampedAt7Days ensures the
// driveBatchJitterMaxSeconds clamp fires when a long batch's cumulative
// stagger would otherwise schedule jobs beyond +7d. The clamp is
// applied per-item AFTER the random gap is added, so on the boundary
// items the scheduled_at STOPS advancing — they all collapse onto
// the same T+7d instant. This is the documented behaviour (see the
// godoc on batchRunUploadByFolderPage loop body) and tested here to
// prevent regressing to "scheduler creates rows past the horizon
// indefinitely".
func TestUploadsBatchByFolder_CumulativeStagger_ClampedAt7Days(t *testing.T) {
	// 200 files on page 1, then EOF. min_jitter = max_jitter = 4 hours
	// (huge stagger — 199 × 4h = 33 DAS, well past 7d). Without the
	// clamp, LastScheduledAt would be T+33d.
	const minJitter = 4 * 60 * 60
	const maxJitter = 4 * 60 * 60
	const fileCount = 200
	lister := &mockDriveFolderLister{
		pagesFn: func(pageToken string) ([]services.GoogleDriveFile, string, error) {
			if pageToken != "" {
				return nil, "", fmt.Errorf("unexpected token %q in clamp test", pageToken)
			}
			files := make([]services.GoogleDriveFile, 0, fileCount)
			for i := 0; i < fileCount; i++ {
				files = append(files, services.GoogleDriveFile{
					ID:       fmt.Sprintf("p%d", i),
					Name:     fmt.Sprintf("p%d.mp4", i),
					MimeType: "video/mp4",
				})
			}
			return files, "", nil
		},
	}
	store := &mockUploadJobStore{}
	r := newBatchImportTestRouter(lister, store)

	body := fmt.Sprintf(
		`{"folder_id":"clamp","workspace_id":1,"facebook_account_id":50, "drive_account_id":99,"min_jitter_seconds":%d,"max_jitter_seconds":%d}`,
		minJitter, maxJitter,
	)
	w := runUploadsBatchByFolderPost(t, r, body, "")

	if w.Code != http.StatusAccepted {
		t.Fatalf("want 202 (clamped at 7d), got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		ScheduledCount  int       `json:"scheduled_count"`
		FirstPublishAt  time.Time `json:"first_publish_at"`
		LastScheduledAt time.Time `json:"last_scheduled_at"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ScheduledCount != fileCount {
		t.Fatalf("scheduled_count: want %d (every page-1 file MUST end up queued, even clamped), got %d",
			fileCount, resp.ScheduledCount)
	}
	if store.jobs == nil || len(store.jobs) != fileCount {
		t.Fatalf("upload_jobs queued: want %d (no skip on clamp), got %d", fileCount, len(store.jobs))
	}
	// Lock the single-lister-call contract: the mock returns EOF on
	// page 2, so a regression that retries "" instead of breaking
	// would infinite-loop (caught here before the assertion hangs the
	// CI runner).
	if lister.listCallCount != 1 {
		t.Errorf("lister: want exactly 1 call (mock returns EOF on page 2), got %d — a loop regression would show here", lister.listCallCount)
	}
	// The clamp is per-item: scheduledAt.Sub(startedAt) > 7d → reset.
	// We can't easily replicate startedAt (it's captured INSIDE the
	// handler on call), but we can assert: last scheduled_at is at
	// most +7d + one jitter past FirstPublishAt (because the boundary
	// item itself still gets a jitter ADDED before the clamp re-clamps).
	// In practice with min==max jitter, LastScheduledAt must be in
	// [FP+6d23h, FP+7d+4h] for a 200-file 4h-jitter batch.
	delta := resp.LastScheduledAt.Sub(resp.FirstPublishAt)
	maxAllowed := 7*24*time.Hour + time.Duration(maxJitter)*time.Second
	if delta > maxAllowed {
		t.Errorf("LastScheduledAt exceeds 7d+jitter cap: delta=%v (max %v); the clamp regressed", delta, maxAllowed)
	}
}

// TestUploadsBatchByFolder_SharedDrive_ResolvesOnce_NotPerPage verifies
// the per-folder (NOT per-page) caching contract: across a multi-page
// crawl, GetFileMetadata is called EXACTLY ONCE — the folder's driveId
// is stable for its lifetime. A regression that resolves per-page
// would double Drive quota for no benefit; this test catches it.
func TestUploadsBatchByFolder_SharedDrive_ResolvesOnce_NotPerPage(t *testing.T) {
	const sharedDriveID = "0ABC-multi-page-shared"
	lister := &mockDriveFolderLister{
		folderMetadataFn: func(fileID string) (*services.GoogleDriveFile, error) {
			return &services.GoogleDriveFile{
				ID:      fileID,
				Name:    "multi/",
				DriveID: sharedDriveID,
			}, nil
		},
		pagesFn: func(pageToken string) ([]services.GoogleDriveFile, string, error) {
			switch pageToken {
			case "":
				return []services.GoogleDriveFile{
					{ID: "p1-a", Name: "p1-a.mp4", MimeType: "video/mp4"},
				}, "tok-2", nil
			case "tok-2":
				return []services.GoogleDriveFile{
					{ID: "p2-a", Name: "p2-a.mp4", MimeType: "video/mp4"},
					{ID: "p2-b", Name: "p2-b.mp4", MimeType: "video/mp4"},
				}, "", nil
			default:
				return nil, "", fmt.Errorf("unexpected token %q", pageToken)
			}
		},
	}
	store := &mockUploadJobStore{}
	r := newBatchImportTestRouter(lister, store)

	w := runUploadsBatchByFolderPost(t, r, `{"folder_id":"multi","workspace_id":1,"facebook_account_id":50, "drive_account_id":99}`, "")

	if w.Code != http.StatusAccepted {
		t.Fatalf("want 202, got %d: %s", w.Code, w.Body.String())
	}
	if lister.listCallCount != 2 {
		t.Fatalf("want 2 ListFolder pages in this test, got %d", lister.listCallCount)
	}
	// THE contract: even with 2 pages, resolver fires ONCE.
	if lister.metadataCalls != 1 {
		t.Errorf("resolver must be called ONCE per import (NOT per page); a regression that resolves per page shows here. got %d calls across %d pages", lister.metadataCalls, lister.listCallCount)
	}
	if lister.gotDriveID != sharedDriveID {
		t.Errorf("ListFolder driveID: want %q on every page, got %q", sharedDriveID, lister.gotDriveID)
	}
}
