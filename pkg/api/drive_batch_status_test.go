package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

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
