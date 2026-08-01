package api

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// Shared Drive auto-resolve tests (Task 6/10) for the Drive batch
// import endpoint POST /api/v1/media/import/drive/folder.

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
