package services

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// DriveImporter is the narrow interface the drive-import handler needs
// from the Google Drive provider. Keeping the interface in the
// services package lets pkg/api depend on the abstraction rather
// than the concrete *GoogleDriveOAuthService.
//
// Note: the prior version of this interface exposed DownloadPublicFile
// (a `drive.google.com/uc` + HTML-confirmation-scraping fallback).
// The P0 hardening refactor removed it — every call site now uses the
// authenticated DownloadFile path, and the bearer token is fetched
// from the vault via Renew. There is no longer a "public anonymous
// download" surface in this codebase.
type DriveImporter interface {
	RefreshOAuthToken(ctx context.Context, refreshToken string) (*models.TokenData, error)
	GetFileMetadata(ctx context.Context, accessToken, fileID string) (*GoogleDriveFile, error)
	DownloadFile(ctx context.Context, accessToken, fileID string) (*http.Response, error)
}

// DriveFolderInspector is the narrower interface the Shared Drive
// resolver (Task 6/10) needs from the Drive provider. Only the
// GetFileMetadata side of the contract — the resolver doesn't need
// the refresh + download surface. Tests can implement just this
// method, which keeps the Shared-Drive routing mock minimal.
//
// DriveImporter (above) is a SUPERSET interface; *GoogleDriveOAuthService
// satisfies both. The Resolver accepts the narrow interface so a
// future split (e.g. a FolderInspector service dedicated to folder
// metadata) can plug in without rebuilding the DriveImporter surface.
type DriveFolderInspector interface {
	GetFileMetadata(ctx context.Context, accessToken, fileID string) (*GoogleDriveFile, error)
}

// DriveFolderLister is the narrow interface the batch
// /api/v1/media/import/drive/folder handler needs from the Drive
// provider. Implemented by *GoogleDriveOAuthService.
//
// The folder must be accessible to *some* principal: either the user's
// own Drive OAuth grant (accessToken non-empty) or as a public folder
// via the Drive v3 API key configured at the deployment level
// (cfg.Storage.GoogleDriveAPIKey). Pass empty accessToken for the public flow.
//
// driveID is optional: when empty, the lister uses the default My
// Drive corpus; when non-empty, the lister scopes the listing to the
// specific Shared Drive via `corpora=drive&driveId=X`. Callers
// typically invoke ResolveFolderDriveID (Task 6/10) once BEFORE the
// pagination loop to derive the right corpus id from the folder's
// GetFileMetadata response.
type DriveFolderLister interface {
	// ListFolder returns one page (up to 200) of folderID's immediate
	// children in Drive's natural order (createdTime ASC). To iterate,
	// re-call with pageToken set to the previous response's nextPageToken
	// (empty pageToken means "first page"). When the folder has more
	// items beyond this page, nextPageToken is non-empty in the response.
	ListFolder(ctx context.Context, folderID, driveID, accessToken, pageToken string) (files []GoogleDriveFile, nextPageToken string, err error)
}

// ErrDriveListRequiresAPIKey is the typed sentinel ListFolder returns
// when the caller asks to list a public folder WITHOUT both a Drive
// OAuth accessToken AND cfg.Storage.GoogleDriveAPIKey. The handler uses
// errors.Is to map this to HTTP 503 Service Unavailable instead of the
// generic 502 the handler would otherwise return.
var ErrDriveListRequiresAPIKey = errors.New("ERR_DRIVE_LIST_REQUIRES_API_KEY")

// ErrDriveFolderMetadataFetchFailed is the typed sentinel
// ResolveFolderDriveID (Task 6/10) returns (wrapped) when
// GetFileMetadata for the supplied folder_id fails. Callers can
// errors.Is against it to render a structured "Shared Drive scoping
// could not be resolved, falling back to My Drive corpus" message.
// Returns are best-effort by design — the resolver never aborts
// the import; it just narrows down what kind of corpus it was
// able to determine. A failure here typically means:
//   - The folder_id is a shortcut to another Drive resource
//   - The principal has permission to list children but NOT to read
//     folder metadata (a known Drive quirk on Workspace DLP rules)
//   - A transient network blip during GetFileMetadata
//
// All three are recoverable: ListFolder still progresses with
// driveID="".
var ErrDriveFolderMetadataFetchFailed = errors.New("ERR_DRIVE_FOLDER_METADATA_FETCH_FAILED")

// ResolveFolderDriveID calls GetFileMetadata(folderID) and returns
// the file's DriveID — the corpus-scoped driveId to thread into
// ListFolder's driveID parameter.
//
// Return contract:
//   - ("<drive-id>", nil)  → folder is inside a Shared Drive; caller
//     threads this into ListFolder which adds corpora=drive&driveId=…
//   - ("", nil)            → folder is in My Drive (driveId empty in
//     the metadata response); caller threads "" into ListFolder which
//     uses the default My Drive corpus
//   - ("", wrapped Err…)    → metadata fetched failed; best-effort
//     caller logs a warn-level remediation hint and falls back to ""
//
// Folders don't move between corpora mid-crawl (a Shared Drive id is
// stable for the lifetime of the folder), so the resolver is called
// ONCE per import — not once per page — and the resolved value is
// reused across the entire pagination loop. This halves Drive quota
// usage vs. resolving per page.
//
// The resolver takes a narrow DriveFolderInspector interface (NOT
// the full DriveImporter) so test mocks can plug in with a single
// GetFileMetadata shim.
func ResolveFolderDriveID(ctx context.Context, svc DriveFolderInspector, folderID, accessToken string) (string, error) {
	if svc == nil {
		return "", fmt.Errorf("%w: nil DriveFolderInspector", ErrDriveFolderMetadataFetchFailed)
	}
	if folderID == "" {
		return "", fmt.Errorf("%w: empty folder id", ErrDriveFolderMetadataFetchFailed)
	}
	file, err := svc.GetFileMetadata(ctx, accessToken, folderID)
	if err != nil {
		return "", fmt.Errorf("%w: folder=%q: %w", ErrDriveFolderMetadataFetchFailed, folderID, err)
	}
	return file.DriveID, nil
}

// Compile-time conformance to the central Platform Registry contract.
var (
	_ OAuthProvider        = (*GoogleDriveOAuthService)(nil)
	_ DriveImporter        = (*GoogleDriveOAuthService)(nil)
	_ DriveFolderLister    = (*GoogleDriveOAuthService)(nil)
	_ DriveFolderInspector = (*GoogleDriveOAuthService)(nil)
)
