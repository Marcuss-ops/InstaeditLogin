package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// Pagination + cursor tests for the Drive batch import endpoint
// POST /api/v1/media/import/drive/folder.

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
