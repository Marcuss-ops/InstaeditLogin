package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// Error-path tests (4xx / 5xx + operator-guidance) for the Drive batch
// import endpoint POST /api/v1/media/import/drive/folder.

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
