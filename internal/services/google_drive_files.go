package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// driveDownloadMaxBytes caps every Drive download at 10 GiB. Files
// larger than this are rejected by limitReadCloser with a typed error
// rather than silently truncating. The 10 GiB ceiling matches the
// valutazione doc's "operator-side cap" — a 4-hour 4K clip is
// ~120 GiB, well above the cap, so operators splitting their ingest
// into smaller files is the expected workflow. We deliberately do
// NOT rely on the response's Content-Length header (Drive omits it
// on chunked transfer-encoding responses); the cap is enforced at
// the reader layer so the caller can't bypass it by ignoring
// Content-Length.
const driveDownloadMaxBytes = 10 * 1024 * 1024 * 1024

// driveFileFields is the canonical `fields=` projection for files.get
// and files.list. Extended in this refactor (vs the prior version
// which only had id,name,mimeType,size) to include:
//
//   - sha256Checksum        — optional hex digest Drive computes for
//     some files; absent for older / non-checksummed entries
//   - capabilities.canDownload — boolean; missing for some legacy
//     files, but when present-and-false we fail-fast instead of
//     surfacing a 403 mid-download
//   - driveId               — set for Shared Drive files; empty for
//     My Drive files
//   - parents               — used by future nested-folder traversal
//   - createdTime, modifiedTime — for batch-crawler ordering
const driveFileFields = "id,name,mimeType,size,sha256Checksum,capabilities,driveId,parents,createdTime,modifiedTime"

// driveListFields wraps driveFileFields in the `files(...)` envelope
// the files.list response uses, plus the nextPageToken pagination
// cursor. Kept as a constant so the two callsites (files.list + the
// custom query) can't drift.
const driveListFields = "files(" + driveFileFields + "),nextPageToken"

// ErrDriveDownloadTooLarge is the typed sentinel limitReadCloser
// returns when a Drive response body exceeds driveDownloadMaxBytes.
// Handlers use errors.Is to map this to HTTP 422 Unprocessable
// Entity (caller can show a clear "file too big" toast) instead of
// the generic 502 Bad Gateway they'd otherwise return on a body-read
// error.
var ErrDriveDownloadTooLarge = errors.New("ERR_DRIVE_DOWNLOAD_TOO_LARGE")

// ErrDriveNotDownloadable (Task 5/10) is the typed sentinel
// AuthenticatedDriveSource.Inspect + pkg/api/drive_import return
// when the Drive file's capabilities.canDownload is explicitly
// false. NO fallback — the import is permanently rejected so the
// operator-triage dashboard surfaces a clear remediation message
// (DLP rule on the Workspace, IRM stamped on the file, the
// "viewers and commenters can download" share-setting unchecked)
// rather than letting the row leak into 'pending' and burn the
// operator's quota when a future publish tick would 403 mid-download
// anyway. Handlers use errors.Is to map this to HTTP 422.
//
// ABSENT Capabilities field is NOT a rejection — the field is
// omitted for legacy Drive files where the API cannot determine
// the boolean — see godoc on Capabilities.
var ErrDriveNotDownloadable = errors.New("ERR_DRIVE_NOT_DOWNLOADABLE")

// limitReadCloser wraps an io.ReadCloser and rejects any read that
// would push the cumulative byte count past `cap`. Returns the typed
// ErrDriveDownloadTooLarge (wrapped with the actual cap) so callers
// can map the error to the correct HTTP status instead of seeing a
// generic "unexpected EOF" or partial-file behavior.
//
// We use a custom type instead of io.LimitReader because the latter
// silently returns io.EOF at the cap, which the caller can't
// distinguish from "the file is exactly N bytes". The custom type
// makes the cap explicit at the failure boundary.
type limitReadCloser struct {
	rc   io.ReadCloser
	cap  int64
	read int64
}

func (l *limitReadCloser) Read(p []byte) (n int, err error) {
	if l.read >= l.cap {
		return 0, fmt.Errorf("%w: read=%d cap=%d", ErrDriveDownloadTooLarge, l.read, l.cap)
	}
	remaining := l.cap - l.read
	if int64(len(p)) > remaining {
		p = p[:remaining]
	}
	n, err = l.rc.Read(p)
	l.read += int64(n)
	return n, err
}

func (l *limitReadCloser) Close() error {
	return l.rc.Close()
}

// GetFileMetadata fetches Drive file metadata. Returns the expanded
// GoogleDriveFile struct including sha256Checksum, capabilities, and
// driveId — the caller is expected to inspect Capabilities.CanDownload
// (when non-nil) and fail-fast on a false value, but the absence of
// the Capabilities field is NOT treated as a rejection (some legacy
// files omit it).
//
// Note: Drive returns `size` as a JSON STRING (a quirk of the v3 API;
// the underlying value is bytes). We keep it as a string here and
// let the caller ParseInt when needed so the parser can stay in one
// place. The Sha256Checksum / DriveID / CreatedTime fields are
// pointers-to-string in the underlying JSON shape; we surface them
// as plain strings with `omitempty` so callers see "" when absent.
func (s *GoogleDriveOAuthService) GetFileMetadata(ctx context.Context, accessToken, fileID string) (*GoogleDriveFile, error) {
	urlStr := "https://www.googleapis.com/drive/v3/files/" + url.PathEscape(fileID) + "?fields=" + url.QueryEscape(driveFileFields) + "&supportsAllDrives=true"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("google drive file metadata request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("google drive file metadata failed (status %d)", resp.StatusCode)
	}
	var file GoogleDriveFile
	if err := json.Unmarshal(body, &file); err != nil {
		return nil, fmt.Errorf("google drive file metadata parse: %w", err)
	}
	return &file, nil
}

// DownloadFile streams a Drive file's bytes via the authenticated
// files.get?alt=media endpoint. The caller MUST close the response
// body (the returned *http.Response has Body wrapped by
// limitReadCloser, which closes the underlying conn on Close).
//
// The response body is wrapped by limitReadCloser(driveDownloadMaxBytes)
// so any read past 10 GiB returns the typed ErrDriveDownloadTooLarge.
// This is enforced at the reader layer — we deliberately do NOT rely
// on the response's Content-Length header because Drive omits it on
// chunked transfer-encoding responses. A missing Content-Length MUST
// NOT cause a rejection (per the user's invariant); the limit applies
// either way.
func (s *GoogleDriveOAuthService) DownloadFile(ctx context.Context, accessToken, fileID string) (*http.Response, error) {
	urlStr := "https://www.googleapis.com/drive/v3/files/" + url.PathEscape(fileID) + "?alt=media&supportsAllDrives=true"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("google drive download request: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("google drive download failed (status %d)", resp.StatusCode)
	}
	// Enforce the cap at the reader layer (defence-in-depth — the
	// caller might forget to check Content-Length on the upload side).
	resp.Body = &limitReadCloser{rc: resp.Body, cap: driveDownloadMaxBytes}
	return resp, nil
}

// GoogleDriveFile is the subset of the Drive v3 file resource the
// import endpoint + folder crawler need. Extended from the prior
// (id/name/mimeType/size) shape to include the metadata fields the
// P0 hardening refactor surfaces: SHA256Checksum for end-to-end
// integrity verification, Capabilities.CanDownload for fail-fast on
// read-only ACLs, DriveID for Shared Drive scoping, and Parents for
// future nested-folder traversal.
//
// `size` remains a string because the Drive v3 API returns it as a
// JSON STRING (not a number — the underlying protobuf uses string for
// int64). Callers ParseInt when they need the numeric value.
type GoogleDriveFile struct {
	ID             string        `json:"id"`
	Name           string        `json:"name"`
	MimeType       string        `json:"mimeType"`
	Size           string        `json:"size"`
	SHA256Checksum string        `json:"sha256Checksum,omitempty"`
	DriveID        string        `json:"driveId,omitempty"`
	Parents        []string      `json:"parents,omitempty"`
	CreatedTime    string        `json:"createdTime,omitempty"`
	ModifiedTime   string        `json:"modifiedTime,omitempty"`
	Capabilities   *Capabilities `json:"capabilities,omitempty"`
}

// Capabilities is the subset of the Drive file resource's capabilities
// map we act on. Drive returns many capability flags (canEdit,
// canComment, canShare, etc.); we only surface CanDownload because
// that's the one the import endpoint needs to fail-fast on a
// read-only ACL.
//
// `CanDownload == false` is treated as a hard reject. A nil
// Capabilities pointer (the field is absent from the response) is
// NOT treated as a reject — some legacy files omit the capabilities
// field entirely, and we don't want to break those imports.
type Capabilities struct {
	CanDownload bool `json:"canDownload"`
}

// driveFolderIDPattern restricts folder_id to characters valid in a
// Drive file id, eliminating the q= query injection vector. Drive ids
// are alphanumeric, dashes, and underscores, typically 25-44 chars
// (we allow up to 100 to leave headroom for current/future formats).
var driveFolderIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,100}$`)

// ListFolder enumerates the immediate children of a Drive folder, filtering
// to recognised video MIME types + extensions. The result is in the
// natural Drive order (typically created_at ascending).
//
// Two modes:
//   - accessToken != "" → authenticated mode. Uses the user's Drive
//     OAuth grant to call /drive/v3/files. Works for ANY folder the
//     user has access to (private, shared, public).
//   - accessToken == ""  → public mode. Requires cfg.Storage.GoogleDriveAPIKey
//     (a Google Cloud API key configured at the deployment level) to
//     hit /drive/v3/files anonymously on a publicly-shared folder.
//     Returns ErrDriveListRequiresAPIKey (wrapped) when the key is
//     missing — handlers use errors.Is to map this to HTTP 503.
//
// driveID selects the corpus: empty → "user" (default My Drive);
// non-empty → "drive" + the specific Shared Drive. The latter is the
// recommended pattern for operators who keep their content on a
// Shared Drive; the current crawler doesn't yet populate driveID, so
// we always include `supportsAllDrives=true` + `includeItemsFromAllDrives=true`
// to ensure Shared Drive folders don't 404 out when the caller
// forgets to scope. (The flags are safe no-ops when the folder is in
// My Drive.)
//
// Paginated: returns one page (up to 200 entries). To walk a folder
// containing more than 200 items, re-call with pageToken set to the
// previous response's nextPageToken. When the folder has no more
// items, nextPageToken is empty.
//
// Folders are skipped (Drive returns a folder mimeType of
// `application/vnd.google-apps.folder`); we only want video files.
func (s *GoogleDriveOAuthService) ListFolder(ctx context.Context, folderID, driveID, accessToken, pageToken string) ([]GoogleDriveFile, string, error) {
	if folderID == "" {
		return nil, "", fmt.Errorf("google drive ListFolder: empty folder id")
	}
	if !driveFolderIDPattern.MatchString(folderID) {
		// Drive ids never contain a quote, so any character outside the
		// allow-list is almost certainly an injection attempt. Reject
		// before concatenating into the q= query (see the regex comment).
		return nil, "", fmt.Errorf("google drive ListFolder: invalid folder id (only A-Za-z0-9_- allowed, max 100 chars)")
	}
	if s.cfg.Storage.GoogleDriveAPIKey == "" && accessToken == "" {
		return nil, "", fmt.Errorf("%w: GOOGLE_DRIVE_API_KEY not configured and no user-specific drive access token supplied", ErrDriveListRequiresAPIKey)
	}

	const pageSize = 200
	q := "'" + folderID + "' in parents and trashed = false and mimeType != 'application/vnd.google-apps.folder'"
	params := url.Values{}
	params.Set("q", q)
	params.Set("fields", driveListFields)
	params.Set("pageSize", strconv.Itoa(pageSize))
	params.Set("orderBy", "createdTime")
	// Shared Drive support — these two flags are the v3 API contract
	// for accessing Shared Drive content; they're safe no-ops when
	// the folder is in My Drive. The Crawler currently lists without
	// driveID scoping, so this is the floor; Shared Drive scoping
	// (corpora=drive) is layered on top when the caller passes a
	// non-empty driveID.
	params.Set("supportsAllDrives", "true")
	params.Set("includeItemsFromAllDrives", "true")
	if driveID != "" {
		params.Set("corpora", "drive")
		params.Set("driveId", driveID)
	}
	if pageToken != "" {
		// Round-tripping an opaque string from a previous response; url.Values
		// encodes it so any chars Drive returns are safely carried through.
		params.Set("pageToken", pageToken)
	}
	if accessToken != "" {
		params.Set("access_token", accessToken)
	} else {
		params.Set("key", s.cfg.Storage.GoogleDriveAPIKey)
	}

	reqURL := "https://www.googleapis.com/drive/v3/files?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("google drive list request: %w", err)
	}
	if accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("google drive list request failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("google drive list failed (status %d)", resp.StatusCode)
	}

	var parsed struct {
		Files         []GoogleDriveFile `json:"files"`
		NextPageToken string            `json:"nextPageToken"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, "", fmt.Errorf("google drive list parse: %w", err)
	}

	files := make([]GoogleDriveFile, 0, len(parsed.Files))
	for _, f := range parsed.Files {
		if !isDriveListableVideo(f.MimeType, f.Name) {
			continue
		}
		files = append(files, f)
	}
	return files, parsed.NextPageToken, nil
}

// isDriveListableVideo extends isDriveVideoMimeType from drive_import.go
// to also recognise Drive's common video MIME types returned by list.
// Same allow-list as the per-file upload path so the two stay consistent.
func isDriveListableVideo(mime, filename string) bool {
	switch mime {
	case "video/mp4", "video/quicktime", "video/webm", "video/x-msvideo",
		"video/mpeg", "video/x-matroska", "video/3gpp":
		return true
	}
	if mime == "application/octet-stream" || mime == "" {
		lower := strings.ToLower(filename)
		for _, ext := range []string{".mp4", ".mov", ".webm", ".avi", ".mpeg", ".mkv", ".3gp"} {
			if strings.HasSuffix(lower, ext) {
				return true
			}
		}
	}
	return false
}
