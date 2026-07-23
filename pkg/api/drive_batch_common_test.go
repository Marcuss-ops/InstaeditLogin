package api

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
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
	// refreshCallCount + lastRefreshInput capture the refresh chain
	// exercised by handleDriveBatchImport → driveAccessToken →
	// vault.Renew → importer.RefreshOAuthToken. The capture reflects
	// the production chain contract: the vault invokes the refresher
	// closure with the encrypted-stored refresh string, and the
	// returned TokenData's AccessToken is what ends up forwarded to
	// ListFolder. Default 0 + "" — tests that don't exercise this
	// path (every pre-existing batch-import test) are unaffected.
	refreshCallCount int
	lastRefreshInput string
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
func (m *mockDriveFolderLister) RefreshOAuthToken(_ context.Context, refresh string) (*models.TokenData, error) {
	m.refreshCallCount++
	m.lastRefreshInput = refresh
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
	opts = append(opts, WithOneTimeCodeStore(NewInMemoryOneTimeCodeStore(60*time.Second)))

	opts = append(opts, WithOneTimeCodeStore(NewInMemoryOneTimeCodeStore(60*time.Second)))

	return NewRouter(
		capRouter,
		userStore,
		auth.NewManager(testJWTSecret, 24),
		"",
		nil,
		opts...,
	)
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
