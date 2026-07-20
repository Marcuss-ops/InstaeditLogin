package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// mockDriveFolderLister only satisfies the narrow interface the
// batch handler actually type-asserts (DriveFolderLister). The same
// mock also implements Provider (Name) so it can be registered in
// the CapabilityRouter.
type mockDriveFolderLister struct {
	files         []services.GoogleDriveFile
	listErr       error
	nextPageToken string
	gotFolderID   string
	gotToken      string
	gotPageToken  string
	gotDriveID    string
	listCallCount int
	// pagesFn enables multi-page simulation. When set, the mock
	// routes ListFolder through this callback so test cases can
	// return different files per pageToken across sequential
	// calls. Nil → mock falls back to static files + static
	// nextPageToken (preserves every pre-existing test).
	pagesFn func(pageToken string) (files []services.GoogleDriveFile, next string, err error)
	// folderMetadataFn lets Task 6/10 acceptance tests drive the
	// Shared-Drive auto-resolve path without an httptest server.
	// GetFileMetadata call routes through this callback so tests
	// can return either a Shared-Drive bucket (driveId="shared-…")
	// or a My-Drive bucket (driveId=""). When nil, GetFileMetadata
	// returns ErrDriveFolderMetadataFetchFailed wrapped so the
	// resolver falls back to "" (matches pre-T6/10 behaviour for
	// every existing test that doesn't override this field).
	folderMetadataFn func(fileID string) (*services.GoogleDriveFile, error)
	metadataCalls    int // captured for the Shared Drive propagation test
}

func (m *mockDriveFolderLister) Name() string { return "google-drive" }
func (m *mockDriveFolderLister) ListFolder(_ context.Context, folderID, driveID, accessToken, pageToken string) ([]services.GoogleDriveFile, string, error) {
	m.gotFolderID = folderID
	m.gotDriveID = driveID
	m.gotToken = accessToken
	m.gotPageToken = pageToken
	m.listCallCount++
	if m.pagesFn != nil {
		return m.pagesFn(pageToken)
	}
	if m.listErr != nil {
		return nil, "", m.listErr
	}
	return m.files, m.nextPageToken, nil
}

// GetFileMetadata satisfies the Task 6/10 DriveFolderInspector
// narrowing so the resolver can run against this mock. When
// folderMetadataFn is set, routes through it (acceptance tests);
// otherwise returns a typed ErrDriveFolderMetadataFetchFailed so
// the resolver falls back to "" — preserves every pre-existing
// test that doesn't override the field.
func (m *mockDriveFolderLister) GetFileMetadata(_ context.Context, _, fileID string) (*services.GoogleDriveFile, error) {
	m.metadataCalls++
	if m.folderMetadataFn != nil {
		return m.folderMetadataFn(fileID)
	}
	return nil, fmt.Errorf("%w: test mock defaults to no-metadata (set folderMetadataFn for Shared-Drive routing tests)", services.ErrDriveFolderMetadataFetchFailed)
}

// RefreshOAuthToken + DownloadFile satisfy services.DriveImporter so
// the handler's `lister.(services.DriveImporter)` type assertion
// succeeds — WITHOUT these the handler returns 503 "google-drive
// provider does not implement drive import" before reaching any
// branch the 5 (now 4) failing tests actually exercise.
//
// The mock values are intentionally minimal: the batch-import flow
// only type-asserts (no live Drive call), so these methods don't
// need realistic Drive responses. RefreshOAuthToken returns a
// canned bearer so any future test that exercises the
// driveAccessToken(vault, importer, accountID) path through this
// mock has a non-nil access token to forward; DownloadFile returns
// nil so any future call would force the caller to short-circuit
// (less surprising than returning a fake response.Body the caller
// has to close).
func (m *mockDriveFolderLister) RefreshOAuthToken(_ context.Context, _ string) (*models.TokenData, error) {
	return &models.TokenData{
		AccessToken: "fake-mock-refreshed-bearer",
		TokenType:   "Bearer",
		ExpiresIn:   3600,
	}, nil
}

func (m *mockDriveFolderLister) DownloadFile(_ context.Context, _, _ string) (*http.Response, error) {
	return nil, nil
}

// Compose-time conformance to the three narrow interfaces the
// handler + Task 6/10 resolver cast to. Compile errors here mean
// the resolver would also fail to type-assert at runtime, so the
// test fails BEFORE runtime.
var (
	_ services.DriveFolderLister    = (*mockDriveFolderLister)(nil)
	_ services.DriveFolderInspector = (*mockDriveFolderLister)(nil)
	// DriveImporter assertion (Task P2 — drive import path). The
	// handler in pkg/api/drive_batch.go + pkg/api/uploads_batch.go
	// type-asserts `lister.(services.DriveImporter)` and returns
	// 503 "google-drive provider does not implement drive import"
	// when missing. The 5 (previously-)failing tests below all
	// exercise this path; the compile-time assertion here catches
	// future interface drift at `go vet` time, not at test time.
	_ services.DriveImporter = (*mockDriveFolderLister)(nil)
)

// mockUploadJobStore appends every Create'd job for inspection. We use
// a slice + sync.Mutex in real code, but for tests we own the goroutine
// (httptest serves sequentially) so a plain slice is fine.
//
// AggregateByFolder returns an explicit summary so tests can assert
// counts + min/max scheduled_at without depending on account-by-account
// insertion in the Create path.
type mockUploadJobStore struct {
	jobs []models.UploadJob
	err  error
	// aggregateFn lets each test pre-script the AggregateByFolder
	// response without going through Create. The default returns
	// a zero-value BatchStatusSummary when nil.
	aggregateFn func(folderID string, userID int64) (models.BatchStatusSummary, error)
	// pendingCountsFn is the scripted override for PendingCountsByAccount.
	// When nil, the mock computes the aggregate from its in-memory jobs
	// slice — the same code path that would be exercised by a real
	// integration test where the SQL aggregator has been run.
	pendingCountsFn func(userID int64) ([]repository.UploadJobPendingCount, error)
	distinctCountFn func(userID int64) (int64, error)
}

func (m *mockUploadJobStore) Create(job *models.UploadJob) error {
	if m.err != nil {
		return m.err
	}
	job.ID = int64(1000 + len(m.jobs))
	job.CreatedAt = time.Now()
	job.UpdatedAt = time.Now()
	m.jobs = append(m.jobs, *job)
	return nil
}

// AggregateByFolder implements the new interface method. Tests that
// care about the response pre-script the function via mock.aggregateFn;
// default returns the zero summary so a test that doesn't set it
// silently exercises the "no rows for folder" path.
func (m *mockUploadJobStore) AggregateByFolder(folderID string, userID int64) (models.BatchStatusSummary, error) {
	if m.aggregateFn != nil {
		return m.aggregateFn(folderID, userID)
	}
	return models.BatchStatusSummary{}, nil
}

// ListByUser mirrors the real repository's filter semantics so the
// dashboard calendar tests can exercise the by-account bucketing and
// the Reschedule/Cancel tests can prepare the world without going
// through Create. matches() is filter-only — the by-account JSON
// grouping stays in the handler (where it has the user identity to
// short-circuit before reaching this method).
func (m *mockUploadJobStore) ListByUser(userID int64, filter repository.UploadJobListFilter) ([]models.UploadJob, error) {
	out := make([]models.UploadJob, 0, len(m.jobs))
	for _, j := range m.jobs {
		if j.UserID != userID {
			continue
		}
		if filter.AccountID != nil {
			ok := false
			for _, t := range j.Targets {
				if t == *filter.AccountID {
					ok = true
					break
				}
			}
			if !ok {
				continue
			}
		}
		if filter.Status != nil && j.Status != *filter.Status {
			continue
		}
		if filter.From != nil {
			if j.PublishAt == nil || j.PublishAt.Before(*filter.From) {
				continue
			}
		}
		if filter.To != nil {
			if j.PublishAt == nil || j.PublishAt.After(*filter.To) {
				continue
			}
		}
		out = append(out, j)
	}
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

// Reschedule mirrors the real repository's contract: only pending
// rows update; rows that already left "pending" return
// repository.ErrUploadJobNotFound so tests can assert the handler's
// 404 mapping.
func (m *mockUploadJobStore) Reschedule(jobID, userID int64, newScheduledAt time.Time) (models.UploadJob, error) {
	for i := range m.jobs {
		if m.jobs[i].ID != jobID || m.jobs[i].UserID != userID {
			continue
		}
		if m.jobs[i].Status != models.UploadJobStatusPending {
			return models.UploadJob{}, repository.ErrUploadJobNotFound
		}
		t := newScheduledAt
		m.jobs[i].PublishAt = &t
		m.jobs[i].UpdatedAt = time.Now()
		return m.jobs[i], nil
	}
	return models.UploadJob{}, repository.ErrUploadJobNotFound
}

// Cancel mirrors Reschedule's state-machine + authz contract.
func (m *mockUploadJobStore) Cancel(jobID, userID int64) error {
	for i := range m.jobs {
		if m.jobs[i].ID != jobID || m.jobs[i].UserID != userID {
			continue
		}
		if m.jobs[i].Status != models.UploadJobStatusPending {
			return repository.ErrUploadJobNotFound
		}
		m.jobs = append(m.jobs[:i], m.jobs[i+1:]...)
		return nil
	}
	return repository.ErrUploadJobNotFound
}

// PendingCountsByAccount mirrors the real repository's GROUP BY
// aggregate: one row per target account that has at least one pending
// upload_job, with the count + earliest scheduled_at. The mock walks
// the stored slice instead of running SQL so test expectations stay
// driven by Create invocations. Tests that need a deterministic
// response (e.g. to assert the exact JSON shape on /uploads/counts)
// pre-script mock.pendingCountsFn; the default falls through to the
// in-memory aggregate below so per-job Create()'d data still drives
// the dashboard widget in flight tests.
func (m *mockUploadJobStore) PendingCountsByAccount(userID int64) ([]repository.UploadJobPendingCount, error) {
	if m.pendingCountsFn != nil {
		return m.pendingCountsFn(userID)
	}
	type acc struct {
		count        int
		earliestUnix int64 // ms since epoch; 0 means "no scheduled_at yet"
	}
	byAcc := map[int64]*acc{}
	for _, j := range m.jobs {
		if j.UserID != userID || j.Status != models.UploadJobStatusPending {
			continue
		}
		var earliest int64
		if j.PublishAt != nil {
			earliest = j.PublishAt.Unix()
		}
		for _, t := range j.Targets {
			a, ok := byAcc[t]
			if !ok {
				a = &acc{}
				byAcc[t] = a
			}
			a.count++
			if earliest != 0 {
				if a.earliestUnix == 0 || earliest < a.earliestUnix {
					a.earliestUnix = earliest
				}
			}
		}
	}
	out := make([]repository.UploadJobPendingCount, 0, len(byAcc))
	for id, a := range byAcc {
		c := repository.UploadJobPendingCount{
			AccountID: id,
			Count:     a.count,
		}
		if a.earliestUnix != 0 {
			t := time.Unix(a.earliestUnix, 0).UTC()
			c.NextPublishAt = &t
		}
		out = append(out, c)
	}
	return out, nil
}

// PendingDistinctCount mirrors the real repository's SELECT
// COUNT(*) FROM upload_jobs WHERE user_id=$1 AND status='pending'.
// Defaults to the in-memory count of pending rows for the user when
// no override is set, so the dashboard's "Pending uploads" stat stays
// correct as long as Create()'d data drives it.
func (m *mockUploadJobStore) PendingDistinctCount(userID int64) (int64, error) {
	if m.distinctCountFn != nil {
		return m.distinctCountFn(userID)
	}
	var n int64
	for _, j := range m.jobs {
		if j.UserID == userID && j.Status == models.UploadJobStatusPending {
			n++
		}
	}
	return n, nil
}

// =====================================================================
// /api/v1/uploads/batch/by-folder tests (handleUploadsBatchByFolder)
//
// The new endpoint auto-paginates the single-page handleDriveBatchImport
// equivalent. Each test below uses mockDriveFolderLister.pagesFn to
// drive a deterministic sequence of ListFolder responses keyed on
// the incoming page_token.
// =====================================================================

// runUploadsBatchByFolderPost issues a POST with a fixed JWT (user 1,
// ws 1) and an optional Idempotency-Key against the new endpoint.
// Encapsulates the boilerplate every test repeats.
func runUploadsBatchByFolderPost(
	t *testing.T,
	r *Router,
	body, idempotencyKey string,
) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/uploads/batch/by-folder", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	return w
}

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
	if !resp.NeedsDriveAccount {
		t.Errorf("NeedsDriveAccount must be true when no drive_account_id was passed")
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
		WithUploadJobStore(store),
	)

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

// validFacebookAccountIDs enumerates the IDs the test userStore
// recognises as a valid Facebook Page platform_account. Anything else
// returns (nil, nil) so tests can validate the 404 path.
var validFacebookAccountIDs = map[int64]bool{50: true, 51: true}

// newBatchImportTestRouter builds a Router wired with the bare
// minimum needed for handleDriveBatchImport: workspace store, user
// store (facebook + drive account lookup), upload_job store, vault
// stub, capabilities with a DriveFolderLister.
//
// The idempotency store is NOT wired by default — most pre-existing
// tests don't care about Idempotency-Key and would silently regress
// if they suddenly saw a different cache layer. The 5 dedicated
// idempotency tests below build their router via
// newBatchImportTestRouterWithIdem.
func newBatchImportTestRouter(lister *mockDriveFolderLister, uploadStore *mockUploadJobStore) *Router {
	return newBatchImportTestRouterWithIdem(lister, uploadStore, nil)
}

// newBatchImportTestRouterWithIdem is the same builder but exposes
// an optional idempotency store. Passing nil omits WithIdempotencyStore
// (matching the production behaviour pre-migration 039: the header is
// silently ignored). Passing a real mockIdempotencyStore activates
// the Idempotency-Key code path in handleDriveBatchImport.
func newBatchImportTestRouterWithIdem(
	lister *mockDriveFolderLister,
	uploadStore *mockUploadJobStore,
	idemStore *mockIdempotencyStore,
) *Router {
	capRouter := services.NewCapabilityRouter()
	capRouter.Register("google-drive", lister)

	wsStore := &mockWorkspaceStore{
		findByIDFn: func(id int64) (*models.Workspace, error) {
			return &models.Workspace{ID: id, Name: "Mine", OwnerID: 1}, nil
		},
	}
	userStore := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			if id == 99 {
				return &models.PlatformAccount{ID: 99, UserID: 1, Platform: "google-drive"}, nil
			}
			if validFacebookAccountIDs[id] {
				return &models.PlatformAccount{ID: id, UserID: 1, Platform: models.PlatformFacebook}, nil
			}
			return nil, nil
		},
		listFn: func(userID int64, _ string) ([]*models.PlatformAccount, error) {
			return nil, nil
		},
	}

	opts := []RouterOption{
		WithWorkspaceStore(wsStore),
		WithUploadJobStore(uploadStore),
	}
	if idemStore != nil {
		opts = append(opts, WithIdempotencyStore(idemStore))
	}

	// Vault mock for batch-import handlers. Without this, 5 tests
	// (TestUploadsBatchByFolder_HappyPath_ThreePages_FlattenedEntryList,
	// TestUploadsBatchByFolder_ConfigGap_Returns200WithGuidance,
	// TestDriveBatchImport_Happy_CreatesJobsWithStaggeredSchedule,
	// TestDriveBatchImport_NoAPIKey_Returns200WithGuidance,
	// TestDriveBatchImport_InvalidFolderID_RejectedByLister) hit
	// `if r.vault == nil` and return 501. The fakeVault in
	// pkg/api/fakevault_test.go satisfies credentials.VaultAPI; the
	// 38+ other tests in this file never reach the vault check (they
	// short-circuit elsewhere) so wiring this in by default is safe.
	opts = append(opts, WithCredentialVault(&fakeVault{}))

	return NewRouter(
		capRouter,
		userStore,
		auth.NewManager(testJWTSecret, 24),
		"",
		nil,
		opts...,
	)
}

func TestDriveBatchImport_Happy_CreatesJobsWithStaggeredSchedule(t *testing.T) {
	files := []services.GoogleDriveFile{
		{ID: "f-1", Name: "intro.mp4", MimeType: "video/mp4", Size: "1024"},
		{ID: "f-2", Name: "demo.mp4", MimeType: "video/mp4", Size: "2048"},
		{ID: "f-3", Name: "outro.mp4", MimeType: "video/mp4", Size: "4096"},
	}
	lister := &mockDriveFolderLister{files: files}
	store := &mockUploadJobStore{}
	r := newBatchImportTestRouter(lister, store)

	body := `{"folder_id":"abc-folder","workspace_id":1,"facebook_account_id":50, "drive_account_id":99}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/media/import/drive/folder", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Force-200", "n/a") // placeholder for future debugging
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("want 202, got %d: %s", w.Code, w.Body.String())
	}
	var resp DriveBatchImportResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if resp.ScheduledCount != 3 {
		t.Errorf("ScheduledCount: want 3, got %d", resp.ScheduledCount)
	}
	if len(resp.Entries) != 3 {
		t.Fatalf("entries: want 3, got %d", len(resp.Entries))
	}
	if len(store.jobs) != 3 {
		t.Fatalf("uploadJobStore.Create call count: want 3, got %d", len(store.jobs))
	}

	// First entry publishes NOW (scheduled_at <= now + 5s tolerance).
	now := time.Now()
	first := store.jobs[0].PublishAt
	if first == nil {
		t.Fatalf("first job scheduled_at is nil — should be approximately now")
	}
	if (*first).After(now.Add(5 * time.Second)) {
		t.Errorf("first job scheduled_at not within 5s of now: %v", *first)
	}

	// The intermittent entries must be in the future and ORDER EVERY job
	// in the chronological order. We don't check exact gaps (randomness
	// would break the test), only that each next entry is strictly
	// later than the previous.
	for i := 1; i < len(store.jobs); i++ {
		cur := store.jobs[i].PublishAt
		prev := store.jobs[i-1].PublishAt
		if cur == nil || prev == nil {
			t.Fatalf("entry %d: scheduled_at is nil", i)
		}
		if !cur.After(*prev) {
			t.Errorf("entry %d scheduled_at = %v is not after entry %d scheduled_at = %v",
				i, *cur, i-1, *prev)
		}
	}

	// Defaults applied: every job targets the requested facebook_account_id
	// and uses source_type=authenticated_drive (the public_drive path was
	// removed in the Blocco #2.1 hardening refactor; producer-side
	// handlers now require drive_account_id and would 422 otherwise).
	for i, j := range store.jobs {
		if j.SourceType != models.UploadJobSourceAuthenticatedDrive {
			t.Errorf("job %d source_type: want authenticated_drive, got %s", i, j.SourceType)
		}
		if len(j.Targets) != 1 || j.Targets[0] != 50 {
			t.Errorf("job %d targets: want [50], got %v", i, j.Targets)
		}
	}

	// Duplicate env var note check.
	if resp.Note != "" {
		t.Errorf("note on small batch: want empty, got %q", resp.Note)
	}
}

func TestDriveBatchImport_EmptyFolder_ReturnsOkWithEmptyEntries(t *testing.T) {
	lister := &mockDriveFolderLister{files: nil}
	store := &mockUploadJobStore{}
	r := newBatchImportTestRouter(lister, store)

	body := `{"folder_id":"empty","workspace_id":1,"facebook_account_id":50, "drive_account_id":99}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/media/import/drive/folder", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp DriveBatchImportResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ScheduledCount != 0 {
		t.Errorf("ScheduledCount: want 0, got %d", resp.ScheduledCount)
	}
	if len(store.jobs) != 0 {
		t.Errorf("no upload jobs should have been created")
	}
	if resp.Note == "" {
		t.Error("note: want a hint about empty folder, got empty")
	}
}

func TestDriveBatchImport_NoAPIKey_Returns200WithGuidance(t *testing.T) {
	// Use the typed sentinel to assert the handler maps it to 200
	// (operator-fixable config gap, not a transient outage) + the
	// NeedsGoogleDriveAPIKey + NeedsDriveAccount flags so the SPA
	// can render an actionable CTA.
	lister := &mockDriveFolderLister{
		listErr: fmt.Errorf("%w: GOOGLE_DRIVE_API_KEY not configured and no user-specific drive access token supplied", services.ErrDriveListRequiresAPIKey),
	}
	store := &mockUploadJobStore{}
	r := newBatchImportTestRouter(lister, store)

	body := `{"folder_id":"public","workspace_id":1,"facebook_account_id":50, "drive_account_id":99}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/media/import/drive/folder", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200 (operator-fixable config gap), got %d: %s", w.Code, w.Body.String())
	}
	var resp DriveBatchImportResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.NeedsGoogleDriveAPIKey {
		t.Errorf("NeedsGoogleDriveAPIKey must be true on sentinel, got false (response: %+v)", resp)
	}
	if !resp.NeedsDriveAccount {
		t.Errorf("NeedsDriveAccount must be true (public mode), got false (response: %+v)", resp)
	}
	if resp.ScheduledCount != 0 {
		t.Errorf("ScheduledCount must be 0 on sentinel, got %d", resp.ScheduledCount)
	}
	if !strings.Contains(resp.Note, "GOOGLE_DRIVE_API_KEY") {
		t.Errorf("Note must mention GOOGLE_DRIVE_API_KEY, got: %q", resp.Note)
	}
}

func TestDriveBatchImport_UpstreamErrorReturns502_NoLeak(t *testing.T) {
	// Generic upstream failure: 502 with generic body (no raw error).
	// The full err.Error() from the upstream goes to server logs only.
	lister := &mockDriveFolderLister{
		listErr: errors.New("google drive list failed (status 500): <some upstream html with sensitive path>"),
	}
	store := &mockUploadJobStore{}
	r := newBatchImportTestRouter(lister, store)

	body := `{"folder_id":"any","workspace_id":1,"facebook_account_id":50, "drive_account_id":99}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/media/import/drive/folder", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("want 502 on generic upstream failure, got %d: %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "upstream html") {
		t.Errorf("response must not leak upstream error details, got: %s", w.Body.String())
	}
}

func TestDriveBatchImport_InvalidJitter_422(t *testing.T) {
	lister := &mockDriveFolderLister{}
	store := &mockUploadJobStore{}
	r := newBatchImportTestRouter(lister, store)

	body := `{"folder_id":"any","workspace_id":1,"facebook_account_id":50, "drive_account_id":99,"min_jitter_seconds":10000,"max_jitter_seconds":5000}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/media/import/drive/folder", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d: %s", w.Code, w.Body.String())
	}
}

func TestDriveBatchImport_MissingFields_422(t *testing.T) {
	lister := &mockDriveFolderLister{}
	store := &mockUploadJobStore{}
	r := newBatchImportTestRouter(lister, store)

	body := `{"folder_id":"x"}` // no workspace_id, no facebook_account_id
	req := httptest.NewRequest(http.MethodPost, "/api/v1/media/import/drive/folder", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d: %s", w.Code, w.Body.String())
	}
}

func TestDriveBatchImport_FacebookAccountNotFound_404(t *testing.T) {
	lister := &mockDriveFolderLister{}
	store := &mockUploadJobStore{}
	r := newBatchImportTestRouter(lister, store)

	// facebook_account_id=9999 is not in validFacebookAccountIDs so the
	// userStore mock returns (nil, nil) — closer to a real "account not
	// found" than the previous fallback default.
	body := `{"folder_id":"any","workspace_id":1,"facebook_account_id":9999, "drive_account_id":99}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/media/import/drive/folder", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestDriveBatchImport_CumulativeJitter_GrowsMonotonically(t *testing.T) {
	// Stress test: 10 videos must produce strict monotonic scheduled_at
	// regardless of the random jitter within [60,3600].
	files := make([]services.GoogleDriveFile, 10)
	for i := range files {
		files[i] = services.GoogleDriveFile{ID: "f-" + string(rune('a'+i)), Name: "v.mp4", MimeType: "video/mp4"}
	}
	lister := &mockDriveFolderLister{files: files}
	store := &mockUploadJobStore{}
	r := newBatchImportTestRouter(lister, store)

	body := `{"folder_id":"folder","workspace_id":1,"facebook_account_id":50, "drive_account_id":99,"min_jitter_seconds":60,"max_jitter_seconds":3600}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/media/import/drive/folder", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("want 202, got %d: %s", w.Code, w.Body.String())
	}
	var last time.Time
	for i, j := range store.jobs {
		if j.PublishAt == nil {
			t.Fatalf("job %d scheduled_at nil", i)
		}
		if i > 0 && !(*j.PublishAt).After(last) {
			t.Errorf("job %d not strictly after previous: %v (prev: %v)", i, *j.PublishAt, last)
		}
		last = *j.PublishAt
	}
}

func TestDriveBatchImport_PageToken_PassedToLister(t *testing.T) {
	// Caller is iterating: they supply the page_token from the previous
	// response. Verify the handler forwards it byte-for-byte to the
	// DriveFolderLister (no protocol translation; the value is opaque).
	files := []services.GoogleDriveFile{
		{ID: "p2-first", Name: "p2-1.mp4", MimeType: "video/mp4"},
		{ID: "p2-second", Name: "p2-2.mp4", MimeType: "video/mp4"},
	}
	lister := &mockDriveFolderLister{files: files}
	store := &mockUploadJobStore{}
	r := newBatchImportTestRouter(lister, store)

	body := `{"folder_id":"abc","workspace_id":1,"facebook_account_id":50, "drive_account_id":99,"page_token":"opaque-from-drive-abc123"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/media/import/drive/folder", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("want 202, got %d: %s", w.Code, w.Body.String())
	}
	if lister.gotPageToken != "opaque-from-drive-abc123" {
		t.Errorf("page_token not forwarded: want %q, got %q",
			"opaque-from-drive-abc123", lister.gotPageToken)
	}
}

func TestDriveBatchImport_NextPageTokenInResponseAndNote(t *testing.T) {
	// Mock returns a non-empty nextPageToken. The response MUST echo it
	// under next_page_token and the note MUST mention the required fields
	// for the follow-up call so the SPA can render a clear CTA.
	files := []services.GoogleDriveFile{
		{ID: "p1", Name: "p1.mp4", MimeType: "video/mp4"},
	}
	lister := &mockDriveFolderLister{
		files:         files,
		nextPageToken: "NEXT-PAGETOKEN-XYZ",
	}
	store := &mockUploadJobStore{}
	r := newBatchImportTestRouter(lister, store)

	body := `{"folder_id":"abc","workspace_id":1,"facebook_account_id":50, "drive_account_id":99}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/media/import/drive/folder", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("want 202, got %d: %s", w.Code, w.Body.String())
	}
	var resp DriveBatchImportResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.NextPageToken != "NEXT-PAGETOKEN-XYZ" {
		t.Errorf("NextPageToken: want NEXT-PAGETOKEN-XYZ, got %q", resp.NextPageToken)
	}
	if !strings.Contains(resp.Note, "page_token") {
		t.Errorf("Note must mention page_token for follow-up, got %q", resp.Note)
	}
	if !strings.Contains(resp.Note, "cursor_scheduled_at") {
		t.Errorf("Note must mention cursor_scheduled_at for follow-up, got %q", resp.Note)
	}
	if !strings.Contains(resp.Note, "last_scheduled_at") {
		t.Errorf("Note must mention last_scheduled_at as the cursor source, got %q", resp.Note)
	}
}

func TestDriveBatchImport_EmptyNextPageTokenAlwaysEmitted(t *testing.T) {
	// Reviewer feedback: omitempty on NextPageToken hid the
	// "exactly-one-page boundary" case. With omitempty removed, an EMPTY
	// next_page_token MUST always appear in the response so the caller
	// can distinguish "you got everything" from "you forgot to read it".
	files := []services.GoogleDriveFile{
		{ID: "last", Name: "last.mp4", MimeType: "video/mp4"},
	}
	lister := &mockDriveFolderLister{
		files:         files,
		nextPageToken: "", // Drive's signal for "no more pages"
	}
	store := &mockUploadJobStore{}
	r := newBatchImportTestRouter(lister, store)

	body := `{"folder_id":"abc","workspace_id":1,"facebook_account_id":50, "drive_account_id":99}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/media/import/drive/folder", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("want 202, got %d: %s", w.Code, w.Body.String())
	}
	// Field MUST exist; raw JSON must contain next_page_token.
	raw := w.Body.String()
	if !strings.Contains(raw, `"next_page_token":""`) {
		t.Errorf("next_page_token MUST be present even when empty; got body: %s", raw)
	}
	var resp DriveBatchImportResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.NextPageToken != "" {
		t.Errorf("NextPageToken: want empty, got %q", resp.NextPageToken)
	}
}

func TestDriveBatchImport_CursorScheduledAt_AnchorsStagger(t *testing.T) {
	// Caller is on page 2 and supplies the cursor from page 1's
	// last_scheduled_at. Verify the FIRST job on this page is anchored
	// to the cursor (not to now()) so the cumulative jitter is
	// uninterrupted across pages.
	files := []services.GoogleDriveFile{
		{ID: "p2-a", Name: "p2-a.mp4", MimeType: "video/mp4"},
		{ID: "p2-b", Name: "p2-b.mp4", MimeType: "video/mp4"},
	}
	lister := &mockDriveFolderLister{files: files}
	store := &mockUploadJobStore{}
	r := newBatchImportTestRouter(lister, store)

	// Cursor = 1h in the future (the page-1 last_scheduled_at).
	cursor := time.Now().Add(time.Hour).Format(time.RFC3339Nano)
	body := `{"folder_id":"abc","workspace_id":1,"facebook_account_id":50, "drive_account_id":99,"cursor_scheduled_at":"` + cursor + `","min_jitter_seconds":60,"max_jitter_seconds":60}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/media/import/drive/folder", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("want 202, got %d: %s", w.Code, w.Body.String())
	}

	expectedCursor, err := time.Parse(time.RFC3339Nano, cursor)
	if err != nil {
		t.Fatalf("parse cursor: %v", err)
	}

	first := store.jobs[0].PublishAt
	if first == nil {
		t.Fatal("first job scheduled_at nil")
	}
	// First job on this page should be AT the cursor (no jitter
	// before it). Tolerance: jitter doesn't apply to the index-0
	// entry on a page; only inter-page anchors via the cursor.
	if first.Sub(expectedCursor).Abs() > 2*time.Second {
		t.Errorf("first job on this page should match cursor: cursor=%v, first=%v, diff=%v",
			expectedCursor, *first, first.Sub(expectedCursor))
	}

	// Second job should be ~1 minute after the first (jitter [60,60]).
	second := store.jobs[1].PublishAt
	if second == nil {
		t.Fatal("second job scheduled_at nil")
	}
	if second.Sub(*first) != 60*time.Second {
		t.Errorf("second job expected ~60s after first: first=%v, second=%v, diff=%v",
			*first, *second, second.Sub(*first))
	}
}

func TestDriveBatchImport_CursorInPast_ClampsToNow(t *testing.T) {
	// If a buggy caller (or a fresh restart of a partially-scheduled
	// pagination) sends a cursor_scheduled_at in the past, we MUST NOT
	// start publishing backdated posts (which would fire immediately).
	// Smoke-check: scheduled_at is not before now() AND the response
	// surfaces cursor_clamped_to_now: true so the SPA can show a warning.
	files := []services.GoogleDriveFile{
		{ID: "x", Name: "x.mp4", MimeType: "video/mp4"},
	}
	lister := &mockDriveFolderLister{files: files}
	store := &mockUploadJobStore{}
	r := newBatchImportTestRouter(lister, store)

	// Cursor = 2h in the PAST — handler should ignore it.
	pastCursor := time.Now().Add(-2 * time.Hour).Format(time.RFC3339Nano)
	body := `{"folder_id":"abc","workspace_id":1,"facebook_account_id":50, "drive_account_id":99,"cursor_scheduled_at":"` + pastCursor + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/media/import/drive/folder", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("want 202, got %d: %s", w.Code, w.Body.String())
	}
	now := time.Now()
	first := *store.jobs[0].PublishAt
	if first.Before(now.Add(-1 * time.Second)) {
		t.Errorf("past cursor should be clamped to now: first=%v, now=%v", first, now)
	}
	var resp DriveBatchImportResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.CursorClampedToNow {
		t.Errorf("CursorClampedToNow must be true when cursor was too far in the past, got false (response: %+v)", resp)
	}
}

func TestDriveBatchImport_CursorInFuture_FlagNotSet(t *testing.T) {
	// Symmetric: when the cursor is in the future (the well-behaved
	// pagination case), the flag MUST be omitted. omitempty + bool means
	// it's absent in JSON and Go zero-value (false) when decoded.
	files := []services.GoogleDriveFile{
		{ID: "y", Name: "y.mp4", MimeType: "video/mp4"},
	}
	lister := &mockDriveFolderLister{files: files}
	store := &mockUploadJobStore{}
	r := newBatchImportTestRouter(lister, store)

	futureCursor := time.Now().Add(1 * time.Hour).Format(time.RFC3339Nano)
	body := `{"folder_id":"abc","workspace_id":1,"facebook_account_id":50, "drive_account_id":99,"cursor_scheduled_at":"` + futureCursor + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/media/import/drive/folder", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("want 202, got %d: %s", w.Code, w.Body.String())
	}
	// JSON body MUST NOT contain cursor_clamped_to_now at all.
	if strings.Contains(w.Body.String(), "cursor_clamped_to_now") {
		t.Errorf("cursor_clamped_to_now must be omitted for valid forward cursor, got body: %s", w.Body.String())
	}
}

// DriveBatchStatus tests -----------------------------------------------------------------

// =====================================================================
// Shared Drive auto-resolve tests (Task 6/10)
// =====================================================================

// TestDriveBatchImport_SharedDrive_ResolvesAndPropagatesDriveID verifies
// acceptance: when a folder's GetFileMetadata returns a non-empty
// driveId (Shared Drive), the handler threads that driveId into the
// ListFolder call so Drive's v3 API gets `corpora=drive&driveId=…`.
// The mock bridge: folderMetadataFn returns a Shared-Drive-style
// resource; the handler then calls ListFolder with that driveId.
func TestDriveBatchImport_SharedDrive_ResolvesAndPropagatesDriveID(t *testing.T) {
	const sharedDriveID = "0ABC-shared-drive-folder-x"
	lister := &mockDriveFolderLister{
		folderMetadataFn: func(fileID string) (*services.GoogleDriveFile, error) {
			if fileID != "shared-folder" {
				t.Errorf("resolver called with wrong fileID: want %q, got %q", "shared-folder", fileID)
			}
			return &services.GoogleDriveFile{
				ID:      fileID,
				Name:    "shared/",
				DriveID: sharedDriveID,
			}, nil
		},
	}
	store := &mockUploadJobStore{}
	r := newBatchImportTestRouter(lister, store)

	body := `{"folder_id":"shared-folder","workspace_id":1,"facebook_account_id":50, "drive_account_id":99}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/media/import/drive/folder", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("want 202, got %d: %s", w.Code, w.Body.String())
	}
	if lister.metadataCalls != 1 {
		t.Errorf("resolver should be called exactly once per import (not per page); got %d calls", lister.metadataCalls)
	}
	if lister.gotDriveID != sharedDriveID {
		t.Errorf("ListFolder driveID: want %q (the Shared Drive id from metadata), got %q", sharedDriveID, lister.gotDriveID)
	}
	if lister.gotFolderID != "shared-folder" {
		t.Errorf("ListFolder folderID: want shared-folder, got %q", lister.gotFolderID)
	}
}

// TestDriveBatchImport_PrivateFolder_DriveIDRemainsEmpty verifies the
// My-Drive corpus path: when a folder's GetFileMetadata returns
// driveId="" (the default for personal-Drive folders), the resolver
// returns "" and the handler threads "" into ListFolder, which uses
// the default My-Drive corpus. This is the back-compat case — every
// operator using personal Drive still works unchanged.
func TestDriveBatchImport_PrivateFolder_DriveIDRemainsEmpty(t *testing.T) {
	lister := &mockDriveFolderLister{
		folderMetadataFn: func(fileID string) (*services.GoogleDriveFile, error) {
			return &services.GoogleDriveFile{
				ID:      fileID,
				Name:    "personal/",
				DriveID: "", // explicit empty = My Drive
			}, nil
		},
	}
	store := &mockUploadJobStore{}
	r := newBatchImportTestRouter(lister, store)

	body := `{"folder_id":"personal-folder","workspace_id":1,"facebook_account_id":50, "drive_account_id":99}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/media/import/drive/folder", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("want 202 (My Drive is the back-compat path), got %d: %s", w.Code, w.Body.String())
	}
	if lister.metadataCalls != 1 {
		t.Errorf("resolver call count: want 1, got %d", lister.metadataCalls)
	}
	if lister.gotDriveID != "" {
		t.Errorf("ListFolder driveID: want empty (My Drive corpus), got %q", lister.gotDriveID)
	}
}

// TestDriveBatchImport_FolderMetadataFetchFails_DriveIDEmpty verifies
// the best-effort swallow path: when GetFileMetadata fails (transient
// network blip, 404, parse), the resolver returns ErrDriveFolder-
// MetadataFetchFailed which the handler logs at warn level and
// converts to driveID="" (= pre-T6/10 behaviour, full back-compat).
// This is the contract that protects against the Shared-Drive resolver
// regressing into a hard import failure.
func TestDriveBatchImport_FolderMetadataFetchFails_DriveIDEmpty(t *testing.T) {
	lister := &mockDriveFolderLister{
		folderMetadataFn: func(fileID string) (*services.GoogleDriveFile, error) {
			return nil, fmt.Errorf("%w: 404 not found", services.ErrDriveFolderMetadataFetchFailed)
		},
	}
	store := &mockUploadJobStore{}
	r := newBatchImportTestRouter(lister, store)

	body := `{"folder_id":"unreachable-folder","workspace_id":1,"facebook_account_id":50, "drive_account_id":99}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/media/import/drive/folder", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	// Handler must NOT surface the resolver error to the client.
	// With a resolver failure + no static files, ListFolder returns
	// 0 files → 200 OK with empty entries + a note (the existing
	// empty-folder path; preserves the user's existing UX).
	if w.Code != http.StatusOK {
		t.Fatalf("want 200 (resolver failure must NOT 5xx), got %d: %s", w.Code, w.Body.String())
	}
	if lister.metadataCalls != 1 {
		t.Errorf("resolver call count: want 1, got %d", lister.metadataCalls)
	}
	if lister.gotDriveID != "" {
		t.Errorf("ListFolder driveID: want empty after resolver failure, got %q", lister.gotDriveID)
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

// DriveBatchImport idempotency tests ----------------------------------------------------

// runBatchImportPost issues a POST with a fixed JWT (user 1, ws 1)
// and a pre-supplied Idempotency-Key. Returns the recorded response.
// Encapsulates the boilerplate every idempotency test repeats.
func runBatchImportPost(
	t *testing.T,
	r *Router,
	body, idempotencyKey string,
) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/media/import/drive/folder", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	return w
}

func TestDriveBatchImport_IdempotencyKey_HappyPath_InsertsCache(t *testing.T) {
	// 3-video batch + Idempotency-Key=batch-key-v1. Verifies:
	//   - 202 returned with the scheduled entries
	//   - parent idempotency_records row created with resource_type=
	//     "drive_batch" and resource_id=first job's id
	//   - side row in idempotency_batch_replays created with the
	//     same JSON bytes that were written to the wire (byte-for-byte)
	files := []services.GoogleDriveFile{
		{ID: "f-1", Name: "intro.mp4", MimeType: "video/mp4"},
		{ID: "f-2", Name: "demo.mp4", MimeType: "video/mp4"},
		{ID: "f-3", Name: "outro.mp4", MimeType: "video/mp4"},
	}
	lister := &mockDriveFolderLister{files: files}
	store := &mockUploadJobStore{}
	idemStore := newMockIdempotencyStore()
	r := newBatchImportTestRouterWithIdem(lister, store, idemStore)

	const idemKey = "batch-key-v1"
	body := `{"folder_id":"abc-folder","workspace_id":1,"facebook_account_id":50, "drive_account_id":99}`
	w := runBatchImportPost(t, r, body, idemKey)

	if w.Code != http.StatusAccepted {
		t.Fatalf("want 202, got %d: %s", w.Code, w.Body.String())
	}
	respBytes := w.Body.Bytes()

	// parent record exists.
	parent, err := idemStore.FindActiveByKey(1, idemKey, time.Now())
	if err != nil {
		t.Fatalf("FindActiveByKey: %v", err)
	}
	if parent == nil {
		t.Fatal("expected parent idempotency record to be persisted on first-call success")
	}
	if parent.ResourceType != "drive_batch" {
		t.Errorf("parent.ResourceType: want drive_batch, got %q", parent.ResourceType)
	}
	if parent.ResponseStatus != http.StatusAccepted {
		t.Errorf("parent.ResponseStatus: want 202, got %d", parent.ResponseStatus)
	}
	if parent.ResourceID <= 0 {
		t.Errorf("parent.ResourceID should be the first job id (>0), got %d", parent.ResourceID)
	}
	// Tighten: resource_id must be the FIRST scheduled job's id, not
	// just any positive number. Catches the regression where a future
	// refactor accidentally points at a different entry (e.g. the LAST
	// scheduled job, or 0-as-sentinel).
	if len(store.jobs) == 0 {
		t.Fatal("no upload jobs created; cannot verify resource_id contract")
	}
	if parent.ResourceID != store.jobs[0].ID {
		t.Errorf("parent.ResourceID should equal first job id (=%d), got %d (regression: caching wrong entry?)",
			store.jobs[0].ID, parent.ResourceID)
	}
	wantReqHash := idempotencyHash([]byte(body))
	if !bytes.Equal(parent.RequestHash, wantReqHash) {
		t.Errorf("parent.RequestHash mismatch (sha256 of body)")
	}

	// side row exists with byte-identical payload.
	side, err := idemStore.FindBatchReplay(parent.ID)
	if err != nil {
		t.Fatalf("FindBatchReplay: %v", err)
	}
	if side == nil {
		t.Fatal("expected batch_replay side row to be persisted alongside the parent")
	}
	if !bytes.Equal(side.ResponsePayload, respBytes) {
		t.Errorf("side.ResponsePayload should equal wire bytes byte-for-byte\n   wire:  %q\n   cache: %q",
			string(respBytes), string(side.ResponsePayload))
	}
}

func TestDriveBatchImport_IdempotencyKey_ReplaySameHash_ReturnsCachedEntries(t *testing.T) {
	// First call populates the cache; second call (same key + same
	// hash) replays byte-identical JSON. The mock upload job store
	// records ANY Create call, so we also assert the replay did NOT
	// create new upload_jobs (otherwise we'd end up with 4+4=8 jobs
	// instead of 4).
	files := []services.GoogleDriveFile{
		{ID: "f-1", Name: "a.mp4", MimeType: "video/mp4"},
		{ID: "f-2", Name: "b.mp4", MimeType: "video/mp4"},
	}
	lister := &mockDriveFolderLister{files: files}
	store := &mockUploadJobStore{}
	idemStore := newMockIdempotencyStore()
	r := newBatchImportTestRouterWithIdem(lister, store, idemStore)

	const idemKey = "batch-replay-key"
	body := `{"folder_id":"replay-folder","workspace_id":1,"facebook_account_id":50, "drive_account_id":99}`

	// First call writes to cache.
	w1 := runBatchImportPost(t, r, body, idemKey)
	if w1.Code != http.StatusAccepted {
		t.Fatalf("first call want 202, got %d: %s", w1.Code, w1.Body.String())
	}
	if len(store.jobs) != 2 {
		t.Fatalf("first call: want 2 jobs created, got %d", len(store.jobs))
	}
	firstWire := w1.Body.Bytes()

	// Second call (same key + same body hash) REPLAYS byte-for-byte.
	w2 := runBatchImportPost(t, r, body, idemKey)
	if w2.Code != http.StatusAccepted {
		t.Fatalf("replay want 202, got %d: %s", w2.Code, w2.Body.String())
	}
	if !bytes.Equal(w2.Body.Bytes(), firstWire) {
		t.Errorf("replay bytes differ from original wire bytes\n   wire:  %q\n   cache: %q",
			string(firstWire), string(w2.Body.Bytes()))
	}
	// Critical: replay must NOT have created new upload jobs.
	if len(store.jobs) != 2 {
		t.Errorf("replay must not create new jobs; want 2 total, got %d", len(store.jobs))
	}
}

func TestDriveBatchImport_IdempotencyKey_DifferentHash_Returns409(t *testing.T) {
	// First call with body A populates the cache. Second call with
	// body B but the same Idempotency-Key MUST fail with 409 — the
	// client sent a different request body under the same key, which
	// is the Stripe-documented conflict semantics.
	files := []services.GoogleDriveFile{
		{ID: "f-1", Name: "a.mp4", MimeType: "video/mp4"},
	}
	lister := &mockDriveFolderLister{files: files}
	store := &mockUploadJobStore{}
	idemStore := newMockIdempotencyStore()
	r := newBatchImportTestRouterWithIdem(lister, store, idemStore)

	const idemKey = "conflict-key"
	bodyA := `{"folder_id":"folder-A","workspace_id":1,"facebook_account_id":50, "drive_account_id":99}`
	bodyB := `{"folder_id":"folder-B","workspace_id":1,"facebook_account_id":50, "drive_account_id":99}`

	w1 := runBatchImportPost(t, r, bodyA, idemKey)
	if w1.Code != http.StatusAccepted {
		t.Fatalf("first call want 202, got %d: %s", w1.Code, w1.Body.String())
	}

	w2 := runBatchImportPost(t, r, bodyB, idemKey)
	if w2.Code != http.StatusConflict {
		t.Fatalf("hash mismatch want 409, got %d: %s", w2.Code, w2.Body.String())
	}
	// Critical: the conflict must NOT create new upload jobs.
	if len(store.jobs) != 1 {
		t.Errorf("conflict path must not create new jobs; want 1 from first call, got %d", len(store.jobs))
	}
}

func TestDriveBatchImport_IdempotencyKey_NoHeader_DoesNotCache(t *testing.T) {
	// Pure positive control: a request without Idempotency-Key runs
	// the handler normally and writes NO cache row. We assert the
	// store is empty after a single call so future contributors can't
	// silently flip the default to "cache everything".
	files := []services.GoogleDriveFile{
		{ID: "f-1", Name: "no-cache.mp4", MimeType: "video/mp4"},
	}
	lister := &mockDriveFolderLister{files: files}
	store := &mockUploadJobStore{}
	idemStore := newMockIdempotencyStore()
	r := newBatchImportTestRouterWithIdem(lister, store, idemStore)

	body := `{"folder_id":"no-key","workspace_id":1,"facebook_account_id":50, "drive_account_id":99}`
	w := runBatchImportPost(t, r, body, "")
	if w.Code != http.StatusAccepted {
		t.Fatalf("no-header want 202, got %d: %s", w.Code, w.Body.String())
	}

	got, err := idemStore.FindActiveByKey(1, "", time.Now())
	if err != nil {
		t.Fatalf("FindActiveByKey: %v", err)
	}
	if got != nil {
		t.Errorf("no-header should not cache; got %+v", got)
	}
	if len(idemStore.records) != 0 {
		t.Errorf("no-header should leave store empty; got %d records", len(idemStore.records))
	}
	if len(store.jobs) != 1 {
		t.Errorf("handler still ran (1 job expected), got %d", len(store.jobs))
	}
}

func TestDriveBatchImport_IdempotencyKey_TooLong_Returns400(t *testing.T) {
	// Stripe-mandated limit: 255 chars. A 256-char key MUST 400.
	files := []services.GoogleDriveFile{
		{ID: "f-1", Name: "x.mp4", MimeType: "video/mp4"},
	}
	lister := &mockDriveFolderLister{files: files}
	store := &mockUploadJobStore{}
	idemStore := newMockIdempotencyStore()
	r := newBatchImportTestRouterWithIdem(lister, store, idemStore)

	longKey := strings.Repeat("k", 256) // 256 > 255 (Stripe limit)
	body := `{"folder_id":"long-key","workspace_id":1,"facebook_account_id":50, "drive_account_id":99}`
	w := runBatchImportPost(t, r, body, longKey)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400 on 256-char key, got %d: %s", w.Code, w.Body.String())
	}
	// The lookup short-circuited, so no cache row + no upload_jobs.
	if len(store.jobs) != 0 {
		t.Errorf("long-key path must not create jobs; got %d", len(store.jobs))
	}
	if len(idemStore.records) != 0 {
		t.Errorf("long-key path must not cache; got %d records", len(idemStore.records))
	}
}

func TestDriveBatchImport_IdempotencyKey_EmptyBatchNotCached(t *testing.T) {
	// Defence-in-depth: a successful first call that returned 200
	// (empty folder / needs_google_drive_api_key) MUST NOT cache —
	// re-trying after fixing the underlying issue should re-run the
	// handler to get a fresh response.
	lister := &mockDriveFolderLister{files: nil}
	store := &mockUploadJobStore{}
	idemStore := newMockIdempotencyStore()
	r := newBatchImportTestRouterWithIdem(lister, store, idemStore)

	const idemKey = "empty-folder-key"
	body := `{"folder_id":"empty","workspace_id":1,"facebook_account_id":50, "drive_account_id":99}`
	w := runBatchImportPost(t, r, body, idemKey)

	if w.Code != http.StatusOK {
		t.Fatalf("empty folder want 200, got %d: %s", w.Code, w.Body.String())
	}
	got, _ := idemStore.FindActiveByKey(1, idemKey, time.Now())
	if got != nil {
		t.Errorf("empty-folder response must not be cached; got %+v", got)
	}
}

func TestDriveBatchImport_IdempotencyKey_CrossTenant_DoesNotReplay(t *testing.T) {
	// SECURITY: attacker (JWT user 2) targets user 1's workspace
	// (workspace_id=1 in the body) while reusing user 1's
	// Idempotency-Key. Workspace ownership check fires FIRST and
	// blocks the request with 403 BEFORE the cache lookup runs.
	// If the handler skipped the ownership check, the cache lookup
	// would hit user 1's row and replay their entries — a
	// cross-tenant data leak.
	wsStore := &mockWorkspaceStore{
		findByIDFn: func(id int64) (*models.Workspace, error) {
			// ws 1 owned by user 1, ws 2 owned by user 2.
			return &models.Workspace{ID: id, Name: "Mine", OwnerID: id}, nil
		},
	}
	files := []services.GoogleDriveFile{
		{ID: "f-1", Name: "a.mp4", MimeType: "video/mp4"},
	}
	lister := &mockDriveFolderLister{files: files}
	capRouter := services.NewCapabilityRouter()
	capRouter.Register("google-drive", lister) // not strictly needed (cross-tenant test 403s before the lister), but registered for completeness
	store := &mockUploadJobStore{}
	idemStore := newMockIdempotencyStore()
	userStore := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return &models.PlatformAccount{ID: id, UserID: id, Platform: models.PlatformFacebook}, nil
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
		WithUploadJobStore(store),
		WithIdempotencyStore(idemStore),
	)

	const idemKey = "cross-tenant-key"
	body := `{"folder_id":"x","workspace_id":1,"facebook_account_id":1, "drive_account_id":99}`

	// User 1 (JWT) targets workspace 1 (their own). Cache populates
	// under (workspace_id=1, idempotency_key=cross-tenant-key).
	w1 := mustServe(t, r, body, idemKey, 1)
	if w1.Code != http.StatusAccepted {
		t.Fatalf("user-1 first call want 202, got %d: %s", w1.Code, w1.Body.String())
	}
	// Sanity: cache exists for user 1.
	parent, _ := idemStore.FindActiveByKey(1, idemKey, time.Now())
	if parent == nil {
		t.Fatal("cache should exist for (1, cross-tenant-key) after user-1 first call")
	}

	// Attacker (JWT user 2) sends the SAME body + SAME key but their
	// own JWT. Workspace ownership check: ws 1 owner is user 1, JWT
	// caller is user 2 → 403. Cache lookup NEVER happens for user 2.
	w2 := mustServe(t, r, body, idemKey, 2)
	if w2.Code != http.StatusForbidden {
		t.Fatalf("user-2 retry want 403 (workspace ownership gate before cache lookup), got %d: %s",
			w2.Code, w2.Body.String())
	}
	// Also: no cross-tenant cache row under user 2's scope.
	if got, _ := idemStore.FindActiveByKey(2, idemKey, time.Now()); got != nil {
		t.Errorf("user 2 must not have a cache row for that key (would indicate cache leak)")
	}
}

// mustReq and mustServe are local helpers that build httptest
// requests and serve them through the Router without leaking the
// boilerplate into every test. HTTP-only; tests that need a
// different JWT user override via the `userID` parameter.
func mustReq(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/media/import/drive/folder", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func mustServe(t *testing.T, r *Router, body, idemKey string, userID int64) *httptest.ResponseRecorder {
	t.Helper()
	req := mustReq(body)
	if idemKey != "" {
		req.Header.Set("Idempotency-Key", idemKey)
	}
	withBearerJWT(t, req, userID)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	return w
}

// DriveBatchStatus tests -----------------------------------------------------------------

func TestDriveBatchStatus_HappyPath_CountsAndFirstLastPublish(t *testing.T) {
	// AggregateByFolder returns a curated summary. The handler must
	// pass it through byte-for-byte (status counts + first/last +
	// total) and echo the folder_id + user_id. We also assert the
	// handler does not invent data when the summary is empty.
	folderID := "abc-folder-monitor"
	first := time.Date(2026, 7, 17, 16, 0, 0, 0, time.UTC)
	last := first.Add(7 * 24 * time.Hour)
	store := &mockUploadJobStore{
		aggregateFn: func(_ string, _ int64) (models.BatchStatusSummary, error) {
			return models.BatchStatusSummary{
				PendingCount:    3,
				ProcessingCount: 1,
				CompletedCount:  10,
				FailedCount:     0,
				TotalCount:      14,
				FirstPublishAt:  &first,
				LastPublishAt:   &last,
			}, nil
		},
	}
	r := newBatchImportTestRouter(&mockDriveFolderLister{}, store)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/media/import/drive/batch/status?folder_id="+folderID, nil)
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp DriveBatchStatusResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.FolderID != folderID {
		t.Errorf("folder_id echo: want %q, got %q", folderID, resp.FolderID)
	}
	if resp.UserID != 1 {
		t.Errorf("user_id: want 1, got %d", resp.UserID)
	}
	if resp.PendingCount != 3 || resp.ProcessingCount != 1 || resp.CompletedCount != 10 || resp.FailedCount != 0 {
		t.Errorf("status counts wrong: pending=%d processing=%d completed=%d failed=%d",
			resp.PendingCount, resp.ProcessingCount, resp.CompletedCount, resp.FailedCount)
	}
	if resp.TotalCount != 14 {
		t.Errorf("total_count: want 14, got %d", resp.TotalCount)
	}
	if resp.FirstPublishAt == nil || !resp.FirstPublishAt.Equal(first) {
		t.Errorf("first_publish_at: want %v, got %v", first, resp.FirstPublishAt)
	}
	if resp.LastPublishAt == nil || !resp.LastPublishAt.Equal(last) {
		t.Errorf("last_publish_at: want %v, got %v", last, resp.LastPublishAt)
	}
	if resp.Note != "" {
		t.Errorf("note should be empty when batch has rows, got %q", resp.Note)
	}
}

func TestDriveBatchStatus_ZeroRows_Returns200WithNote(t *testing.T) {
	// Authenticated caller queries a folder_id that has no upload_jobs
	// for them. The handler must NOT 404 (the dashboard polls aggressively;
	// a 404 would surface as a red banner between batches). Instead 200
	// with all-zero counts + a hint note explaining why.
	store := &mockUploadJobStore{
		aggregateFn: func(_ string, _ int64) (models.BatchStatusSummary, error) {
			return models.BatchStatusSummary{}, nil
		},
	}
	r := newBatchImportTestRouter(&mockDriveFolderLister{}, store)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/media/import/drive/batch/status?folder_id=ghost", nil)
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp DriveBatchStatusResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.TotalCount != 0 || resp.PendingCount != 0 {
		t.Errorf("counts should be zero, got total=%d pending=%d", resp.TotalCount, resp.PendingCount)
	}
	if resp.FolderID != "ghost" {
		t.Errorf("folder_id must still echo back, got %q", resp.FolderID)
	}
	if resp.Note == "" {
		t.Errorf("note should explain why counts are zero")
	}
}

func TestDriveBatchStatus_CrossTenant_ReturnsZeroRows(t *testing.T) {
	// Simulates user A created a folder batch, user B queries that
	// folder_id. The repo MUST scope by user_id so user B never sees
	// user A's counts. The mock models the real SQL: rows for this
	// folder belong to userID=1 (user A), so any other caller — including
	// the JWT caller in this test (user 2) — receives zero. The handler
	// has no way to know the rows existed; it must surface zeros to
	// the SPA so the dashboard shows an empty queue (and the user can
	// re-import if they wish).
	const callerJWTUserID = 2 // user B in the test
	const rowsOwnerUserID = 1 // user A owns the underlying rows
	folderID := "another-tens-folder"
	calls := 0
	store := &mockUploadJobStore{
		aggregateFn: func(_ string, userID int64) (models.BatchStatusSummary, error) {
			calls++
			if userID != rowsOwnerUserID {
				// Simulate the real WHERE user_id=$2 returning zero
				// rows for non-owners: no SQL aggregates are computed
				// at all.
				return models.BatchStatusSummary{}, nil
			}
			// 5 rows live under this folder, owned by user 1.
			return models.BatchStatusSummary{
				PendingCount: 5,
				TotalCount:   5,
			}, nil
		},
	}
	r := newBatchImportTestRouter(&mockDriveFolderLister{}, store)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/media/import/drive/batch/status?folder_id="+folderID, nil)
	withBearerJWT(t, req, callerJWTUserID)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp DriveBatchStatusResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if calls != 1 {
		t.Errorf("expected 1 AggregateByFolder call, got %d", calls)
	}
	if resp.TotalCount != 0 {
		t.Errorf("cross-tenant lookup must return zero counts (handler lets repo filter), got total=%d", resp.TotalCount)
	}
	// The handler always echoes user_id from the JWT, regardless of the
	// repo's response — the SPA uses this for client-side debugging,
	// never as an authz decision.
	if resp.UserID != callerJWTUserID {
		t.Errorf("user_id echo: want %d, got %d", callerJWTUserID, resp.UserID)
	}
	// When zero rows match, the handler sets a note so the SPA can show
	// a hint instead of a generic "0/0" state on the dashboard.
	if resp.Note == "" {
		t.Errorf("note should be set when zero rows match, got empty")
	}
}

func TestDriveBatchStatus_MissingFolderID_422(t *testing.T) {
	store := &mockUploadJobStore{}
	r := newBatchImportTestRouter(&mockDriveFolderLister{}, store)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/media/import/drive/batch/status", nil)
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422 for missing folder_id, got %d: %s", w.Code, w.Body.String())
	}
}

func TestDriveBatchStatus_InvalidFolderID_422(t *testing.T) {
	// server-side regex `^[A-Za-z0-9_\-]{1,100}$` rejects chars
	// outside the URL-safe set (spaces, slashes, unicode, etc.) at
	// the API boundary, before any Postgres hit.
	invalid := []string{
		"bad id with spaces",
		"abc/def",
		"abc+xyz",
		strings.Repeat("a", 101),
	}
	for _, id := range invalid {
		t.Run(id, func(t *testing.T) {
			store := &mockUploadJobStore{}
			r := newBatchImportTestRouter(&mockDriveFolderLister{}, store)

			req := httptest.NewRequest(http.MethodGet, "/api/v1/media/import/drive/batch/status?folder_id="+url.QueryEscape(id), nil)
			withBearerJWT(t, req, 1)
			w := httptest.NewRecorder()
			r.Setup().ServeHTTP(w, req)

			if w.Code != http.StatusUnprocessableEntity {
				t.Fatalf("want 422 for invalid folder_id %q, got %d: %s", id, w.Code, w.Body.String())
			}
		})
	}
}
