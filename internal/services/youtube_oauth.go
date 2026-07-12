package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/config"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// YouTubeOAuthService implements the YouTube provider. Taglio 2.1:
// each provider only carries the methods it actually supports — no more
// composition onto a single monolithic PlatformService.
//
// Capabilities exposed:
//   - OAuthProvider (Google OAuth 2.0 with offline access)
//   - ContentValidator (video_url required)
//   - Publisher (resumable upload protocol)
//   - AccountManager (Validate / Revoke)
type YouTubeOAuthService struct {
	cfg        *config.Config
	httpClient *http.Client
}

// NewYouTubeOAuthService creates a new YouTubeOAuthService. Taglio 2.1:
// the constructor no longer takes a tokenRepo.
func NewYouTubeOAuthService(cfg *config.Config) (*YouTubeOAuthService, error) {
	if cfg.YouTubeClientID == "" {
		return nil, nil // provider disabled
	}
	return &YouTubeOAuthService{
		cfg:        cfg,
		httpClient: NewHTTPClient(),
	}, nil
}

func (s *YouTubeOAuthService) Name() string { return models.PlatformYouTube }

func (s *YouTubeOAuthService) GetLoginURL(state string) string {
	params := url.Values{}
	params.Set("client_id", s.cfg.YouTubeClientID)
	params.Set("redirect_uri", s.cfg.YouTubeRedirectURI)
	params.Set("state", state)
	params.Set("scope", "https://www.googleapis.com/auth/youtube.upload https://www.googleapis.com/auth/userinfo.profile")
	params.Set("response_type", "code")
	params.Set("access_type", "offline")

	return "https://accounts.google.com/o/oauth2/v2/auth?" + params.Encode()
}

func (s *YouTubeOAuthService) HandleCallback(ctx context.Context, state, code string) (*models.PlatformProfile, *models.TokenData, error) {
	slog.Info("YouTube: exchanging code for token")

	tokenResp, err := s.exchangeCodeForToken(ctx, code)
	if err != nil {
		return nil, nil, fmt.Errorf("youtube token exchange: %w", err)
	}

	slog.Info("YouTube: fetching user info")
	profile, err := s.getUserInfo(ctx, tokenResp.AccessToken)
	if err != nil {
		return nil, nil, fmt.Errorf("youtube user info: %w", err)
	}

	tokenData := &models.TokenData{
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		TokenType:    models.TokenTypeBearer,
		ExpiresIn:    tokenResp.ExpiresIn,
		Scopes:       strings.Split(tokenResp.Scope, " "),
	}

	return profile, tokenData, nil
}

// ValidateContent enforces the YouTube video-required rule.
func (s *YouTubeOAuthService) ValidateContent(payload models.PublishPayload) error {
	if payload.VideoURL == "" {
		return fmt.Errorf("youtube requires video_url for publishing")
	}
	return nil
}

// Validate calls the Google userinfo endpoint to verify the access token.
func (s *YouTubeOAuthService) Validate(ctx context.Context, accessToken, platformUserID string) error {
	req, err := http.NewRequestWithContext(ctx, "GET",
		"https://www.googleapis.com/oauth2/v2/userinfo", nil)
	if err != nil {
		return fmt.Errorf("youtube validate request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("youtube validate failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("youtube validate returned status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// Revoke calls Google's OAuth 2.0 token revocation endpoint.
func (s *YouTubeOAuthService) Revoke(ctx context.Context, accessToken string) error {
	body := url.Values{}
	body.Set("token", accessToken)

	req, err := http.NewRequestWithContext(ctx, "POST",
		"https://oauth2.googleapis.com/revoke",
		strings.NewReader(body.Encode()))
	if err != nil {
		return fmt.Errorf("youtube revoke request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("youtube revoke failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("youtube revoke returned status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// RefreshOAuthToken exchanges a YouTube refresh token for a new access token.
func (s *YouTubeOAuthService) RefreshOAuthToken(ctx context.Context, refreshToken string) (result *models.TokenData, err error) {
	defer RecordTokenRefreshMetrics(models.PlatformYouTube, &err)
	if refreshToken == "" {
		return nil, fmt.Errorf("youtube RefreshOAuthToken: empty refresh token")
	}
	slog.Info("YouTube: refreshing access token")
	body := url.Values{}
	body.Set("client_id", s.cfg.YouTubeClientID)
	body.Set("client_secret", s.cfg.YouTubeClientSecret)
	body.Set("refresh_token", refreshToken)
	body.Set("grant_type", "refresh_token")

	req, err := http.NewRequestWithContext(ctx, "POST", "https://oauth2.googleapis.com/token",
		strings.NewReader(body.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("youtube refresh request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("youtube refresh failed (status %d): %s", resp.StatusCode, string(respBody))
	}

	var tr youtubeTokenResponse
	if err := json.Unmarshal(respBody, &tr); err != nil {
		return nil, fmt.Errorf("youtube refresh parse: %w", err)
	}
	refresh := tr.RefreshToken
	if refresh == "" {
		refresh = refreshToken
	}
	return &models.TokenData{
		AccessToken:  tr.AccessToken,
		RefreshToken: refresh,
		TokenType:    models.TokenTypeBearer,
		ExpiresIn:    tr.ExpiresIn,
		Scopes:       strings.Split(tr.Scope, " "),
	}, nil
}

const youtubeUploadChunkSize = 256 * 1024

// Publish uploads a video to YouTube using the resumable upload protocol.
func (s *YouTubeOAuthService) Publish(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (result *models.PublishResult, err error) {
	defer RecordPublishMetrics(models.PlatformYouTube, time.Now(), &err)
	if err := s.ValidateContent(payload); err != nil {
		return nil, err
	}

	slog.Info("YouTube: starting resumable video upload", "source", payload.VideoURL)

	fileSize, contentType, err := s.headVideo(ctx, payload.VideoURL)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect source video: %w", err)
	}
	if contentType == "" {
		contentType = "video/mp4"
	}
	slog.Info("YouTube: source video info", "size", fileSize, "content_type", contentType)

	metadata := map[string]interface{}{
		"snippet": map[string]string{
			"title":       defaultVideoTitle(payload),
			"description": payload.Text,
		},
		"status": map[string]string{
			"privacyStatus": "public",
		},
	}

	uploadURL, err := s.initiateResumableSession(ctx, accessToken, metadata, fileSize, contentType)
	if err != nil {
		return nil, fmt.Errorf("failed to initiate resumable session: %w", err)
	}
	slog.Debug("YouTube: resumable session initiated", "upload_url", uploadURL)

	videoID, err := s.uploadVideoChunks(ctx, uploadURL, payload.VideoURL, fileSize)
	if err != nil {
		return nil, fmt.Errorf("failed to stream video: %w", err)
	}

	slog.Info("YouTube: video uploaded successfully", "video_id", videoID)

	return &models.PublishResult{
		PlatformMediaID: videoID,
		PlatformURL:     "https://www.youtube.com/watch?v=" + videoID,
	}, nil
}

// --- Upload helpers ---

func (s *YouTubeOAuthService) headVideo(ctx context.Context, videoURL string) (size int64, contentType string, err error) {
	req, err := http.NewRequestWithContext(ctx, "HEAD", videoURL, nil)
	if err != nil {
		return 0, "", fmt.Errorf("HEAD request creation failed: %w", err)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return 0, "", fmt.Errorf("HEAD request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return s.headViaRange(ctx, videoURL)
	}

	return resp.ContentLength, resp.Header.Get("Content-Type"), nil
}

func (s *YouTubeOAuthService) headViaRange(ctx context.Context, videoURL string) (int64, string, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", videoURL, nil)
	req.Header.Set("Range", "bytes=0-0")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusPartialContent {
		return 0, "", fmt.Errorf("unable to determine video size (status %d)", resp.StatusCode)
	}

	contentRange := resp.Header.Get("Content-Range")
	parts := strings.Split(contentRange, "/")
	if len(parts) != 2 {
		return 0, resp.Header.Get("Content-Type"), fmt.Errorf("unexpected Content-Range: %s", contentRange)
	}

	var total int64
	if _, err := fmt.Sscanf(strings.TrimSpace(parts[1]), "%d", &total); err != nil {
		return 0, "", fmt.Errorf("failed to parse total size: %w", err)
	}

	return total, resp.Header.Get("Content-Type"), nil
}

func (s *YouTubeOAuthService) initiateResumableSession(ctx context.Context, accessToken string, metadata map[string]interface{}, fileSize int64, contentType string) (string, error) {
	jsonMeta, err := json.Marshal(metadata)
	if err != nil {
		return "", fmt.Errorf("failed to marshal metadata: %w", err)
	}

	reqURL := "https://www.googleapis.com/upload/youtube/v3/videos?uploadType=resumable&part=snippet,status"
	req, err := http.NewRequestWithContext(ctx, "POST", reqURL, strings.NewReader(string(jsonMeta)))
	if err != nil {
		return "", err
	}

	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Upload-Content-Length", fmt.Sprintf("%d", fileSize))
	req.Header.Set("X-Upload-Content-Type", contentType)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("init request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("init session failed (status %d): %s", resp.StatusCode, string(body))
	}

	uploadURL := resp.Header.Get("Location")
	if uploadURL == "" {
		return "", fmt.Errorf("no Location header in init response")
	}

	return uploadURL, nil
}

func (s *YouTubeOAuthService) uploadVideoChunks(ctx context.Context, uploadURL, sourceURL string, fileSize int64) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", sourceURL, nil)
	if err != nil {
		return "", err
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to download source video: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("source video returned status %d", resp.StatusCode)
	}

	if fileSize <= 0 {
		fileSize = resp.ContentLength
	}
	if fileSize <= 0 {
		resp.Body.Close()
		return "", fmt.Errorf("unable to determine video size (got %d)", fileSize)
	}

	var uploaded int64
	var retries int
	const maxRetries = 3
	buf := make([]byte, youtubeUploadChunkSize)

	for {
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("upload cancelled: %w", ctx.Err())
		default:
		}

		n, readErr := io.ReadFull(resp.Body, buf)
		if readErr != nil && readErr != io.ErrUnexpectedEOF && readErr != io.EOF {
			return "", fmt.Errorf("failed to read video chunk: %w", readErr)
		}

		if n == 0 {
			break
		}

		contentRange := fmt.Sprintf("bytes %d-%d/%d", uploaded, uploaded+int64(n)-1, fileSize)

		videoID, uploadErr := s.putChunk(ctx, uploadURL, buf[:n], contentRange, int64(n))
		if uploadErr != nil {
			retries++
			if retries > maxRetries {
				resp.Body.Close()
				return "", fmt.Errorf("upload failed at byte %d after %d retries: %w", uploaded, maxRetries, uploadErr)
			}

			slog.Warn("YouTube: chunk upload failed, attempting recovery", "byte", uploaded, "retry", retries, "error", uploadErr)

			resumedAt, qErr := s.queryUploadStatus(ctx, uploadURL, fileSize)
			if qErr != nil {
				resp.Body.Close()
				return "", fmt.Errorf("upload failed at byte %d (status query also failed): %w", uploaded, uploadErr)
			}
			slog.Info("YouTube: resuming upload from byte", "resumed_at", resumedAt)

			resp.Body.Close()
			req2, _ := http.NewRequestWithContext(ctx, "GET", sourceURL, nil)
			req2.Header.Set("Range", fmt.Sprintf("bytes=%d-", resumedAt))
			resp2, err2 := s.httpClient.Do(req2)
			if err2 != nil {
				return "", fmt.Errorf("failed to re-download from byte %d: %w", resumedAt, err2)
			}
			resp = resp2
			uploaded = resumedAt
			continue
		}

		if videoID != "" {
			resp.Body.Close()
			return videoID, nil
		}

		uploaded += int64(n)

		if readErr == io.EOF || readErr == io.ErrUnexpectedEOF {
			break
		}
	}

	resp.Body.Close()
	return "", fmt.Errorf("upload completed but no video ID returned")
}

func (s *YouTubeOAuthService) putChunk(ctx context.Context, uploadURL string, data []byte, contentRange string, expectedLen int64) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "PUT", uploadURL, bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Range", contentRange)
	req.ContentLength = expectedLen

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("PUT chunk failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated:
		var result struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(body, &result); err != nil {
			return "", fmt.Errorf("failed to parse upload completion response: %w", err)
		}
		return result.ID, nil

	case 308:
		return "", nil

	default:
		return "", fmt.Errorf("unexpected PUT response (status %d): %s", resp.StatusCode, string(body))
	}
}

func (s *YouTubeOAuthService) queryUploadStatus(ctx context.Context, uploadURL string, fileSize int64) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, "PUT", uploadURL, http.NoBody)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Range", fmt.Sprintf("bytes */%d", fileSize))
	req.ContentLength = 0

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 308 {
		return 0, fmt.Errorf("unexpected status query response: %d", resp.StatusCode)
	}

	rangeHeader := resp.Header.Get("Range")
	if rangeHeader == "" {
		return 0, nil
	}

	parts := strings.SplitN(rangeHeader, "=", 2)
	if len(parts) != 2 {
		return 0, fmt.Errorf("malformed Range header: %s", rangeHeader)
	}
	rangeParts := strings.SplitN(parts[1], "-", 2)
	if len(rangeParts) != 2 {
		return 0, fmt.Errorf("malformed Range value: %s", parts[1])
	}

	var lastByte int64
	if _, err := fmt.Sscanf(rangeParts[1], "%d", &lastByte); err != nil {
		return 0, fmt.Errorf("failed to parse Range end byte: %w", err)
	}

	return lastByte + 1, nil
}

func defaultVideoTitle(payload models.PublishPayload) string {
	if payload.Title != "" {
		return payload.Title
	}
	if payload.Text != "" {
		if len(payload.Text) > 100 {
			return payload.Text[:97] + "..."
		}
		return payload.Text
	}
	return "Uploaded via InstaEdit"
}

// --- Private ---

type youtubeTokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	Scope        string `json:"scope"`
	RefreshToken string `json:"refresh_token"`
}

func (s *YouTubeOAuthService) exchangeCodeForToken(ctx context.Context, code string) (*youtubeTokenResponse, error) {
	body := url.Values{}
	body.Set("client_id", s.cfg.YouTubeClientID)
	body.Set("client_secret", s.cfg.YouTubeClientSecret)
	body.Set("code", code)
	body.Set("grant_type", "authorization_code")
	body.Set("redirect_uri", s.cfg.YouTubeRedirectURI)

	req, err := http.NewRequestWithContext(ctx, "POST", "https://oauth2.googleapis.com/token",
		strings.NewReader(body.Encode()))
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

	var tr youtubeTokenResponse
	if err := json.Unmarshal(respBody, &tr); err != nil {
		return nil, fmt.Errorf("token parse: %w", err)
	}
	return &tr, nil
}

func (s *YouTubeOAuthService) getUserInfo(ctx context.Context, accessToken string) (*models.PlatformProfile, error) {
	req, err := http.NewRequestWithContext(ctx, "GET",
		"https://www.googleapis.com/oauth2/v2/userinfo", nil)
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
