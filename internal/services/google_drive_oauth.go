package services

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
}

// Compile-time conformance to the central Platform Registry contract.
var (
	_ Provider      = (*GoogleDriveOAuthService)(nil)
	_ OAuthProvider = (*GoogleDriveOAuthService)(nil)
	_ DriveImporter = (*GoogleDriveOAuthService)(nil)
)
