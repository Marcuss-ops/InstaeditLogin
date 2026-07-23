package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

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
	// NeedsDriveAccount is FALSE when the request body already
	// supplied drive_account_id:99. Handler's 2026 semantic
	// treats a supplied drive_account_id as an alternative
	// to API-key-only mode, so the only thing the caller needs
	// to ALSO do is configure GOOGLE_DRIVE_API_KEY.
	if resp.NeedsDriveAccount {
		t.Errorf("NeedsDriveAccount must be false when drive_account_id is supplied in body, got true (response: %+v)", resp)
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
		// Single file so the handler returns 202 ("accepts N jobs")
		// instead of 200 "no videos found"; the test's real concern
		// is the resolver + ListFolder wiring (asserted below) and
		// those assertions are unchanged when ListFolder returns
		// at least one entry. The current drive_batch.go handler
		// short-circuits on `len(files) == 0` to 200 OK + a
		// "no videos found" note — by design.
		files: []services.GoogleDriveFile{
			{ID: "f-shared", Name: "shared-video.mp4", MimeType: "video/mp4"},
		},
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
		// Same rationale as the SharedDrive test: a non-empty
		// files slice drives the handler down the 202 path. The
		// My-Drive back-compat assertion (driveID stays empty
		// after GetFileMetadata returns "") is independent of
		// the file count.
		files: []services.GoogleDriveFile{
			{ID: "f-personal", Name: "personal-video.mp4", MimeType: "video/mp4"},
		},
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
		// Body uses drive_account_id=99 + facebook_account_id=1.
		// Drive lookup must resolve to a google-drive account
		// owned by the JWT caller (user 1 on the first call); the
		// previous generic lookup returned Platform=Facebook
		// for id=99, which made the handler short-circuit on the
		// platform check with 404 "google drive account not
		// found". User 2's cross-tenant retry never reaches
		// this lookup — the workspace-ownership gate fires
		// first (ws 1 owner=1, JWT user 2 → 403).
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			if id == 99 {
				return &models.PlatformAccount{ID: 99, UserID: 1, Platform: "google-drive"}, nil
			}
			return &models.PlatformAccount{ID: id, UserID: id, Platform: models.PlatformFacebook}, nil
		},
		listFn: func(userID int64, _ string) ([]*models.PlatformAccount, error) {
			return nil, nil
		},
	}
	// WithCredentialVault is REQUIRED: after fix #1 (userStore
	// resolving drive_account_id=99 to google-drive + user 1),
	// the next failure point along the drive-batch import flow
	// is the vault check — handleDriveBatchImport returns 501
	// "credential vault not configured" when r.vault == nil.
	// fakeVault in fakevault_test.go implements
	// credentials.VaultAPI without hitting Postgres so the
	// existing driveAccessToken path returns a canned bearer
	// for the (idempotent, fully-cached) replay assertion below.
	r := NewRouter(
		capRouter,
		userStore,
		auth.NewManager(testJWTSecret, 24),
		"",
		nil,
		WithWorkspaceStore(wsStore),
		WithUploadJobStore(store),
		WithIdempotencyStore(idemStore),
		WithCredentialVault(&fakeVault{}),
		WithOneTimeCodeStore(NewInMemoryOneTimeCodeStore(60*time.Second)),
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

// =====================================================================
// TestDriveBatchImport_EndToEndAuth_VaultRefreshChainDrivesFolderListing
//
// Verifies the full auth+token+listing integration chain through
// the single-page POST /api/v1/media/import/drive/folder endpoint —
// the chain that the other ~30 batch-import tests don't exercise
// because they use the canned fakeVault whose Renew bypasses the
// closure:
//
//  1. Client → POST with valid workspace_id (owned by JWT caller)
//     + drive_account_id (a google-drive platform_account owned
//     by the JWT caller).
//  2. Handler → workspace ownership check passes.
//  3. Handler → userRepo.FindPlatformAccountByID(drive_account_id)
//     resolves the google-drive account.
//  4. Handler → vault.Renew(accountID, TokenTypeBearer, refreshFn).
//     Here the production-relevant chain runs end-to-end: a
//     recording-style renewFn override invokes the closure with
//     a hardcoded refresh string ("vault-decrypted-refresh-XYZ")
//     so we can verify it flowed through. The closure is the
//     lister's RefreshOAuthToken which returns a canned TokenData
//     with AccessToken="fake-mock-refreshed-bearer"; the renewFn
//     echoes the TokenData's AccessToken into the OAuthToken so
//     the handler sees the SAME bearer the refresher produced
//     (not a decoupled sentinel that would mask a swap).
//  5. Handler → ListFolder(ctx, folderID, driveID, AccessToken, pageToken).
//     gotToken captures the access_token the handler forwarded.
//  6. Handler → 202 + DriveBatchImportResponse with N=filesCount
//     upload_jobs queued.
//
// Pins the production-relevant integration invariant:
//   - vault.Renew invoked exactly once per request
//   - lister.RefreshOAuthToken invoked exactly once, with the
//     refresh token the vault resolved from encrypted storage
//   - ListFolder invoked exactly once, with the access_token the
//     vault produced (echoed from RefreshOAuthToken's TokenData)
//
// A regression that breaks any link — replacing the real Vault
// with a no-op, swapping the refresh closure, or feeding the
// wrong token to ListFolder — surfaces here rather than in
// production. Mirrors the assertVaultRenewedOnce /
// driveBatchFakeVault pattern in
// internal/worker/drive_batch_crawler_test.go.
// =====================================================================
func TestDriveBatchImport_EndToEndAuth_VaultRefreshChainDrivesFolderListing(t *testing.T) {
	const (
		specificRefresh   = "vault-decrypted-refresh-XYZ"
		echoedAccessToken = "fake-mock-refreshed-bearer" // value returned by lister.RefreshOAuthToken
	)
	files := []services.GoogleDriveFile{
		{ID: "e2e-1", Name: "e2e-1.mp4", MimeType: "video/mp4"},
		{ID: "e2e-2", Name: "e2e-2.mp4", MimeType: "video/mp4"},
		{ID: "e2e-3", Name: "e2e-3.mp4", MimeType: "video/mp4"},
	}
	lister := &mockDriveFolderLister{files: files}

	// Recording-style override: actually invoke the refresher
	// closure with the hardcoded refresh token (mirrors what the
	// production CredentialVault does — looks up the encrypted
	// refresh from Postgres, invokes the closure, encrypts +
	// persists the returned TokenData). The TokenData returned
	// by RefreshOAuthToken is echoed into the OAuthToken so the
	// handler sees the SAME AccessToken the refresher produced,
	// not a decoupled sentinel that would mask a swap.
	vault := &fakeVault{
		renewFn: func(ctx context.Context, _ int64, _ string, ref credentials.TokenRefresher) (*models.OAuthToken, error) {
			td, refreshErr := ref(ctx, specificRefresh)
			if refreshErr != nil {
				return nil, refreshErr
			}
			return &models.OAuthToken{
				TokenType:   models.TokenTypeBearer,
				AccessToken: td.AccessToken,
			}, nil
		},
	}

	// Build the router inline (not via newBatchImportTestRouter
	// because the helper hardwires a vanilla fakeVault and we
	// need the recording-style override).
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
	store := &mockUploadJobStore{}
	r := NewRouter(
		capRouter,
		userStore,
		auth.NewManager(testJWTSecret, 24),
		"",
		nil,
		WithWorkspaceStore(wsStore),
		WithUploadJobStore(store),
		WithCredentialVault(vault), WithOneTimeCodeStore(NewInMemoryOneTimeCodeStore(60 * time.Second)))

	body := `{"folder_id":"e2e-folder","workspace_id":1,"facebook_account_id":50,"drive_account_id":99}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/media/import/drive/folder", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("want 202 (full chain success), got %d: %s", w.Code, w.Body.String())
	}

	// ─── Chain assertions ───
	if vault.renewCalls != 1 {
		t.Errorf("vault.Renew calls: want 1, got %d", vault.renewCalls)
	}
	if lister.refreshCallCount != 1 {
		t.Errorf("lister.RefreshOAuthToken calls: want 1, got %d", lister.refreshCallCount)
	}
	if lister.lastRefreshInput != specificRefresh {
		t.Errorf("lister.RefreshOAuthToken input: want %q (the refresh vault resolved from encrypted storage), got %q",
			specificRefresh, lister.lastRefreshInput)
	}
	// Note: the exact-match check above also catches an empty
	// lastRefreshInput (specificRefresh is non-empty, so any
	// "" against it triggers the t.Errorf). No separate empty
	// guard required — keeps the assertion set minimal.
	if lister.gotToken != echoedAccessToken {
		t.Errorf("lister.ListFolder access_token: want %q (the AccessToken from RefreshOAuthToken's TokenData), got %q",
			echoedAccessToken, lister.gotToken)
	}
	if lister.gotFolderID != "e2e-folder" {
		t.Errorf("lister.ListFolder folderID: want e2e-folder, got %q", lister.gotFolderID)
	}
	if lister.listCallCount != 1 {
		t.Errorf("lister.ListFolder calls: want 1, got %d", lister.listCallCount)
	}
	if len(store.jobs) != len(files) {
		t.Errorf("upload_jobs queued: want %d, got %d", len(files), len(store.jobs))
	}

	var resp DriveBatchImportResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ScheduledCount != len(files) {
		t.Errorf("response ScheduledCount: want %d, got %d", len(files), resp.ScheduledCount)
	}
}
