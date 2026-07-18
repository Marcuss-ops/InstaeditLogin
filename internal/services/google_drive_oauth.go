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

// GetLoginURL builds the Google OAuth authorization URL with the
// drive.readonly scope so the user can pick a video clip from Drive.
func (s *GoogleDriveOAuthService) GetLoginURL(state string) string {
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

// GetFileMetadata fetches Drive file metadata (name, mimeType, size).
// This is a Google-Drive-specific helper used by the import endpoint.
func (s *GoogleDriveOAuthService) GetFileMetadata(ctx context.Context, accessToken, fileID string) (*GoogleDriveFile, error) {
	urlStr := "https://www.googleapis.com/drive/v3/files/" + url.PathEscape(fileID) + "?fields=id,name,mimeType,size"
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

// DownloadFile streams a Drive file's bytes. The caller must close the
// response body. fileID is the Drive file id; accessToken is a valid
// Google access token with drive.readonly scope.
func (s *GoogleDriveOAuthService) DownloadFile(ctx context.Context, accessToken, fileID string) (*http.Response, error) {
	urlStr := "https://www.googleapis.com/drive/v3/files/" + url.PathEscape(fileID) + "?alt=media"
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
	return resp, nil
}

// DownloadPublicFile streams a publicly shared Drive file's bytes without
// authentication. It follows the standard Google Drive export redirect and
// handles the virus-scan confirmation page for large files.
func (s *GoogleDriveOAuthService) DownloadPublicFile(ctx context.Context, fileID string) (*http.Response, error) {
	exportURL := "https://drive.google.com/uc?export=download&id=" + url.QueryEscape(fileID)
	return s.downloadPublicFileWithConfirm(ctx, exportURL, 0)
}

func (s *GoogleDriveOAuthService) downloadPublicFileWithConfirm(ctx context.Context, reqURL string, depth int) (*http.Response, error) {
	if depth > 2 {
		return nil, fmt.Errorf("google drive public download: too many redirects/confirmations")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	// Google blocks non-browser user-agents for public downloads. Use a
	// realistic browser User-Agent to avoid 403s on the export endpoint.
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("google drive public download request: %w", err)
	}

	// Happy path: direct binary stream.
	if resp.StatusCode == http.StatusOK && isVideoContentType(resp.Header.Get("Content-Type")) {
		return resp, nil
	}

	// Drive may return an HTML confirmation page for large files. Read a
	// small prefix to extract the confirm token, then replay the request.
	if isHTMLContentType(resp.Header.Get("Content-Type")) {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		confirm := extractDriveConfirmToken(string(body))
		if confirm == "" {
			return nil, fmt.Errorf("google drive public download: unable to confirm large file download")
		}

		u, err := url.Parse(reqURL)
		if err != nil {
			return nil, fmt.Errorf("google drive public download: invalid url: %w", err)
		}
		q := u.Query()
		q.Set("confirm", confirm)
		u.RawQuery = q.Encode()
		return s.downloadPublicFileWithConfirm(ctx, u.String(), depth+1)
	}

	_ = resp.Body.Close()
	return nil, fmt.Errorf("google drive public download failed (status %d)", resp.StatusCode)
}

func isVideoContentType(ct string) bool {
	return strings.HasPrefix(ct, "video/") || ct == "application/octet-stream"
}

func isHTMLContentType(ct string) bool {
	return strings.HasPrefix(ct, "text/html") || ct == "application/xhtml+xml"
}

func extractDriveConfirmToken(html string) string {
	// The confirmation token is typically in a form or link as
	// confirm=XYZ. Scrape the first occurrence.
	idx := strings.Index(html, "confirm=")
	if idx == -1 {
		return ""
	}
	token := html[idx+len("confirm="):]
	for i, ch := range token {
		if ch == '&' || ch == '"' || ch == '\'' || ch == ' ' || ch == '>' {
			return token[:i]
		}
	}
	return token
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
// import endpoint needs.
type GoogleDriveFile struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	MimeType string `json:"mimeType"`
	Size     string `json:"size"`
}

// DriveImporter is the narrow interface the drive-import handler needs
// from the Google Drive provider. Keeping the interface in the
// services package lets pkg/api depend on the abstraction rather
// than the concrete *GoogleDriveOAuthService.
type DriveImporter interface {
	RefreshOAuthToken(ctx context.Context, refreshToken string) (*models.TokenData, error)
	GetFileMetadata(ctx context.Context, accessToken, fileID string) (*GoogleDriveFile, error)
	DownloadFile(ctx context.Context, accessToken, fileID string) (*http.Response, error)
	// DownloadPublicFile streams a publicly shared Drive file's bytes
	// without authentication. Used by the background upload worker for
	// public_drive jobs.
	DownloadPublicFile(ctx context.Context, fileID string) (*http.Response, error)
}

// DriveFolderLister is the narrow interface the batch
// /api/v1/media/import/drive/folder handler needs from the Drive
// provider. Implemented by *GoogleDriveOAuthService.
//
// The folder must be accessible to *some* principal: either the user's
// own Drive OAuth grant (accessToken non-empty) or as a public folder
// via the Drive v3 API key configured at the deployment level
// (cfg.GoogleDriveAPIKey). Pass empty accessToken for the public flow.
type DriveFolderLister interface {
	// ListFolder returns one page (up to 200) of folderID's immediate
	// children in Drive's natural order (createdTime ASC). To iterate,
	// re-call with pageToken set to the previous response's nextPageToken
	// (empty pageToken means "first page"). When the folder has more
	// items beyond this page, nextPageToken is non-empty in the response.
	ListFolder(ctx context.Context, folderID, accessToken, pageToken string) (files []GoogleDriveFile, nextPageToken string, err error)
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
// Paginated: returns one page (up to 200 entries). To walk a folder
// containing more than 200 items, re-call with pageToken set to the
// previous response's nextPageToken. When the folder has no more
// items, nextPageToken is empty.
//
// Folders are skipped (Drive returns a folder mimeType of
// `application/vnd.google-apps.folder`); we only want video files.
func (s *GoogleDriveOAuthService) ListFolder(ctx context.Context, folderID, accessToken, pageToken string) ([]GoogleDriveFile, string, error) {
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
	params.Set("fields", "files(id,name,mimeType,size),nextPageToken")
	params.Set("pageSize", strconv.Itoa(pageSize))
	params.Set("orderBy", "createdTime")
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
