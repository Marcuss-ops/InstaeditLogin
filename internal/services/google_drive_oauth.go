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
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/config"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// GoogleDriveOAuthService implements the OAuth flow for Google Drive.
// It is used only to read (import) video files from a user's Drive;
// it does not publish content, so it implements only OAuthProvider.
type GoogleDriveOAuthService struct {
	cfg        *config.Config
	httpClient *http.Client
	clock      func() time.Time
}

// NewGoogleDriveOAuthService creates a new GoogleDriveOAuthService.
// Returns (nil, nil) when the provider is disabled (no client id).
func NewGoogleDriveOAuthService(cfg *config.Config, deps ...ProviderDependencies) (*GoogleDriveOAuthService, error) {
	if cfg.GoogleDriveClientID == "" {
		return nil, nil
	}
	var dep ProviderDependencies
	if len(deps) > 0 {
		dep = deps[0]
	}
	return &GoogleDriveOAuthService{
		cfg:        cfg,
		httpClient: dep.resolveHTTPClient(),
		clock:      dep.resolveClock(),
	}, nil
}

func (s *GoogleDriveOAuthService) now() time.Time {
	if s.clock != nil {
		return s.clock()
	}
	return time.Now()
}

// Name returns the platform identifier.
func (s *GoogleDriveOAuthService) Name() string { return "google-drive" }

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

// GetLoginURL builds the Google OAuth authorization URL with the
// drive.file scope so the user can pick a video clip from Drive via
// the Google Picker (per-file access; non-sensitive).
func (s *GoogleDriveOAuthService) GetLoginURL(state string) string {
	return s.GetLoginURLWithOptions(state, OAuthLoginOptions{})
}

// GetLoginURLWithOptions builds the Google OAuth authorization URL.
// Google Drive does not use OAuthLoginOptions; options are ignored.
//
// Scope choice (UNCHANGED from prior versions — do not regress):
//   - drive.readonly — required for folder-level listing (the
//     crawler walks arbitrary folders). We deliberately do NOT use
//     `drive.file` because that scope only grants access to files
//     the user explicitly opens via the Google Picker API; it
//     cannot list arbitrary folders and would break the
//     folder-batch import feature entirely.
//   - userinfo.profile — operator display name in the dashboard.
func (s *GoogleDriveOAuthService) GetLoginURLWithOptions(state string, _ OAuthLoginOptions) string {
	params := url.Values{}
	params.Set("client_id", s.cfg.GoogleDriveClientID)
	params.Set("redirect_uri", s.cfg.GoogleDriveRedirectURI)
	params.Set("state", state)
	params.Set("scope", "https://www.googleapis.com/auth/drive.readonly https://www.googleapis.com/auth/userinfo.profile")
	params.Set("response_type", "code")
	params.Set("access_type", "offline")
	params.Set("prompt", "consent")
	return "https://accounts.google.com/o/oauth2/v2/auth?" + params.Encode()
}

// HandleCallback exchanges the authorization code for an access token
// and fetches the user's Google profile.
func (s *GoogleDriveOAuthService) HandleCallback(ctx context.Context, state, code string) (*models.PlatformProfile, *models.TokenData, error) {
	tokenResp, err := s.exchangeCodeForToken(ctx, code)
	if err != nil {
		return nil, nil, fmt.Errorf("google drive token exchange: %w", err)
	}
	profile, err := s.getUserInfo(ctx, tokenResp.AccessToken)
	if err != nil {
		return nil, nil, fmt.Errorf("google drive user info: %w", err)
	}
	return profile, &models.TokenData{
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		TokenType:    models.TokenTypeBearer,
		ExpiresIn:    tokenResp.ExpiresIn,
		Scopes:       strings.Split(tokenResp.Scope, " "),
	}, nil
}

// RefreshOAuthToken refreshes a Google Drive access token.
func (s *GoogleDriveOAuthService) RefreshOAuthToken(ctx context.Context, refreshToken string) (*models.TokenData, error) {
	if refreshToken == "" {
		return nil, fmt.Errorf("google drive refresh: empty refresh token")
	}
	body := url.Values{}
	body.Set("client_id", s.cfg.GoogleDriveClientID)
	body.Set("client_secret", s.cfg.GoogleDriveClientSecret)
	body.Set("refresh_token", refreshToken)
	body.Set("grant_type", "refresh_token")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://oauth2.googleapis.com/token", strings.NewReader(body.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("google drive refresh request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("google drive refresh failed (status %d): %s", resp.StatusCode, string(respBody))
	}
	var tr googleDriveTokenResponse
	if err := json.Unmarshal(respBody, &tr); err != nil {
		return nil, fmt.Errorf("google drive refresh parse: %w", err)
	}
	if tr.RefreshToken == "" {
		tr.RefreshToken = refreshToken
	}
	return &models.TokenData{
		AccessToken:  tr.AccessToken,
		RefreshToken: tr.RefreshToken,
		TokenType:    models.TokenTypeBearer,
		ExpiresIn:    tr.ExpiresIn,
		Scopes:       strings.Split(tr.Scope, " "),
	}, nil
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
		return nil, fmt.Errorf("google drive file metadata failed (status %d): %s", resp.StatusCode, string(body))
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

func (s *GoogleDriveOAuthService) exchangeCodeForToken(ctx context.Context, code string) (*googleDriveTokenResponse, error) {
	body := url.Values{}
	body.Set("client_id", s.cfg.GoogleDriveClientID)
	body.Set("client_secret", s.cfg.GoogleDriveClientSecret)
	body.Set("code", code)
	body.Set("grant_type", "authorization_code")
	body.Set("redirect_uri", s.cfg.GoogleDriveRedirectURI)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://oauth2.googleapis.com/token", strings.NewReader(body.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token exchange failed (status %d): %s", resp.StatusCode, string(respBody))
	}
	var tr googleDriveTokenResponse
	if err := json.Unmarshal(respBody, &tr); err != nil {
		return nil, fmt.Errorf("token parse: %w", err)
	}
	return &tr, nil
}

func (s *GoogleDriveOAuthService) getUserInfo(ctx context.Context, accessToken string) (*models.PlatformProfile, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.googleapis.com/oauth2/v2/userinfo", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("user info failed (status %d): %s", resp.StatusCode, string(body))
	}
	var result struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Email   string `json:"email"`
		Picture string `json:"picture"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	return &models.PlatformProfile{
		PlatformUserID: result.ID,
		Username:       result.Name,
		Name:           result.Name,
		Email:          result.Email,
	}, nil
}

type googleDriveTokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	Scope        string `json:"scope"`
	RefreshToken string `json:"refresh_token"`
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

// DriveFolderLister is the narrow interface the batch
// /api/v1/media/import/drive/folder handler needs from the Drive
// provider. Implemented by *GoogleDriveOAuthService.
//
// The folder must be accessible to *some* principal: either the user's
// own Drive OAuth grant (accessToken non-empty) or as a public folder
// via the Drive v3 API key configured at the deployment level
// (cfg.GoogleDriveAPIKey). Pass empty accessToken for the public flow.
//
// driveID is optional: when empty, the lister uses the default My
// Drive corpus; when non-empty, the lister scopes the listing to the
// specific Shared Drive via `corpora=drive&driveId=X`. The Crawler
// currently passes empty (the Shared Drive scoping refinement is a
// follow-up once the crawler fetches folder metadata to learn the
// driveId before the listing).
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
// OAuth accessToken AND cfg.GoogleDriveAPIKey. The handler uses
// errors.Is to map this to HTTP 503 Service Unavailable instead of the
// generic 502 the handler would otherwise return.
var ErrDriveListRequiresAPIKey = errors.New("ERR_DRIVE_LIST_REQUIRES_API_KEY")

// Compile-time conformance to the central Platform Registry contract.
var (
	_ OAuthProvider     = (*GoogleDriveOAuthService)(nil)
	_ DriveImporter     = (*GoogleDriveOAuthService)(nil)
	_ DriveFolderLister = (*GoogleDriveOAuthService)(nil)
)

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
//   - accessToken == ""  → public mode. Requires cfg.GoogleDriveAPIKey
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
	if s.cfg.GoogleDriveAPIKey == "" && accessToken == "" {
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
		params.Set("key", s.cfg.GoogleDriveAPIKey)
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
		return nil, "", fmt.Errorf("google drive list failed (status %d): %s", resp.StatusCode, truncateForLog(string(body), 300))
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