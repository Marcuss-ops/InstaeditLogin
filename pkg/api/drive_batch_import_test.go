package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// Happy-path and stagger-scheduling tests for the Drive batch import
// endpoint POST /api/v1/media/import/drive/folder.

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
