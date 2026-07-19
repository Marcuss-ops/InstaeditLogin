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
//
// Capabilities exposed:
//   - OAuthProvider (Google OAuth 2.0 with offline access)
//   - ContentValidator (video required)
//   - Publisher (resumable upload protocol)
//   - AccountManager (Validate / Revoke)
type YouTubeOAuthService struct {
	cfg        *config.Config
	httpClient *http.Client
	clock      func() time.Time
}

// NewYouTubeOAuthService creates a new YouTubeOAuthService. Accepts optional
// ProviderDependencies for HTTP client injection.
func NewYouTubeOAuthService(cfg *config.Config, deps ...ProviderDependencies) (*YouTubeOAuthService, error) {
	if cfg.YouTubeClientID == "" {
		return nil, nil // provider disabled
	}
	var dep ProviderDependencies
	if len(deps) > 0 {
		dep = deps[0]
	}
	return &YouTubeOAuthService{
		cfg:        cfg,
		httpClient: dep.resolveHTTPClient(),
		clock:      dep.resolveClock(),
	}, nil
}

// now returns the current time via the injected clock, or time.Now as default.
func (s *YouTubeOAuthService) now() time.Time {
	if s.clock != nil {
		return s.clock()
	}
	return time.Now()
}

func (s *YouTubeOAuthService) Name() string { return models.PlatformYouTube }

// PreferredTokenTypes declares that YouTube stores the OAuth grant as a
// bearer token. Validation checks bearer first, then falls back to the
// other common token types for backwards compatibility.
func (s *YouTubeOAuthService) PreferredTokenTypes() []string {
	return []string{
		models.TokenTypeBearer,
		models.TokenTypeShortLived,
		models.TokenTypeLongLived,
	}
}

func (s *YouTubeOAuthService) GetLoginURL(state string) string {
	return s.GetLoginURLWithOptions(state, OAuthLoginOptions{})
}

func (s *YouTubeOAuthService) GetLoginURLWithOptions(state string, options OAuthLoginOptions) string {
	params := url.Values{}
	params.Set("client_id", s.cfg.YouTubeClientID)
	params.Set("redirect_uri", s.cfg.YouTubeRedirectURI)
	params.Set("state", state)
	params.Set("scope", "https://www.googleapis.com/auth/youtube.upload https://www.googleapis.com/auth/youtube.readonly https://www.googleapis.com/auth/yt-analytics.readonly openid email profile")
	params.Set("response_type", "code")
	params.Set("access_type", "offline")
	params.Set("include_granted_scopes", "true")

	if options.ForceConsent || options.SelectAccount {
		var prompts []string
		if options.SelectAccount {
			prompts = append(prompts, "select_account")
		}
		if options.ForceConsent {
			prompts = append(prompts, "consent")
		}
		params.Set("prompt", strings.Join(prompts, " "))
	}

	if options.LoginHint != "" {
		params.Set("login_hint", options.LoginHint)
	}

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

// ValidateContent enforces the YouTube video-required rule
// and a mandatory privacy_level.
// Taglio 4b: privacy_level is now required — one of public, unlisted, private.
func (s *YouTubeOAuthService) ValidateContent(payload models.PublishPayload) error {
	if payload.VideoURL == "" {
		return fmt.Errorf("youtube requires a video for publishing")
	}
	if payload.PrivacyLevel == "" {
		return fmt.Errorf("youtube requires a privacy_level: one of public, unlisted, private")
	}
	if err := validateYouTubePrivacyLevel(payload.PrivacyLevel); err != nil {
		return err
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
// For YouTube this is the async entrypoint: the upload completes synchronously
// and returns a composite publishID (channelID:videoID). The reconciler will
// then poll videos.list processingDetails until the video is fully processed.
func (s *YouTubeOAuthService) Publish(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (result *models.PublishResult, err error) {
	defer RecordPublishMetrics(models.PlatformYouTube, s.now(), &err)
	publishID, _, err := s.StartPublish(ctx, accessToken, platformUserID, payload)
	if err != nil {
		return nil, err
	}
	_, videoID, err := decodeYouTubePublishID(publishID)
	if err != nil {
		return nil, err
	}
	slog.Info("YouTube: async publish initiated, reconciler will poll processing status",
		"publish_id", publishID, "video_id", videoID)
	return &models.PublishResult{
		PlatformMediaID: publishID,
		PlatformURL:     "https://www.youtube.com/watch?v=" + videoID,
	}, nil
}

// StartPublish performs the resumable upload and returns a composite
// publishID (channelID:videoID) plus the initial "processing" state.
func (s *YouTubeOAuthService) StartPublish(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (publishID string, state string, err error) {
	if err := s.ValidateContent(payload); err != nil {
		return "", "", err
	}

	slog.Info("YouTube: starting resumable video upload", "source", payload.VideoURL)

	fileSize, contentType, err := s.headVideo(ctx, payload.VideoURL)
	if err != nil {
		return "", "", fmt.Errorf("failed to inspect source video: %w", err)
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
			"privacyStatus": normalizeYouTubePrivacyLevel(payload.PrivacyLevel),
		},
	}

	uploadURL, err := s.initiateResumableSession(ctx, accessToken, metadata, fileSize, contentType)
	if err != nil {
		return "", "", fmt.Errorf("failed to initiate resumable session: %w", err)
	}
	slog.Debug("YouTube: resumable session initiated", "upload_url", uploadURL)

	videoID, err := s.uploadVideoChunks(ctx, uploadURL, payload.VideoURL, fileSize)
	if err != nil {
		return "", "", fmt.Errorf("failed to stream video: %w", err)
	}

	slog.Info("YouTube: video uploaded successfully", "video_id", videoID)

	return encodeYouTubePublishID(platformUserID, videoID), "processing", nil
}

// CheckPublishStatus returns the processing status of a YouTube video by
// calling videos.list with part=processingDetails.
func (s *YouTubeOAuthService) CheckPublishStatus(ctx context.Context, accessToken, publishID string) (state string, err error) {
	_, videoID, err := decodeYouTubePublishID(publishID)
	if err != nil {
		return "", err
	}

	video, err := s.fetchVideoStatus(ctx, accessToken, videoID)
	if err != nil {
		return "", err
	}

	if video.ProcessingDetails == nil {
		// No processing details yet; assume still processing.
		return "processing", nil
	}
	return video.ProcessingDetails.ProcessingStatus, nil
}

// ContinuePublish is a no-op for YouTube. The full resumable upload is
// performed inside StartPublish.
func (s *YouTubeOAuthService) ContinuePublish(ctx context.Context, accessToken, publishID string) error {
	return nil
}

// Reconcile polls the YouTube video status and drives the async state machine.
// It verifies the video belongs to the expected channel (snippet.channelId)
// and maps processingDetails.processingStatus to terminal or in-flight.
//
//   processing  → (nil, nil)   // still in flight
//   succeeded   → (*PublishResult, nil)
//   failed      → (nil, error)  // terminal failure
//   terminated  → (nil, error)  // terminal failure
func (s *YouTubeOAuthService) Reconcile(ctx context.Context, accessToken, publishID string) (*models.PublishResult, error) {
	platformUserID, videoID, err := decodeYouTubePublishID(publishID)
	if err != nil {
		return nil, err
	}

	video, err := s.fetchVideoStatus(ctx, accessToken, videoID)
	if err != nil {
		return nil, err
	}

	// The upload was performed with the account's token, but verify the
	// video landed on the expected channel. A missing channelId is treated
	// as a failure because we cannot confirm ownership.
	if video.Snippet.ChannelID != platformUserID {
		return nil, fmt.Errorf("youtube channel mismatch: expected %s, got %s", platformUserID, video.Snippet.ChannelID)
	}

	processingStatus := ""
	if video.ProcessingDetails != nil {
		processingStatus = video.ProcessingDetails.ProcessingStatus
	}

	switch processingStatus {
	case "", "processing":
		// Still processing or no processing details yet.
		return nil, nil
	case "succeeded":
		return &models.PublishResult{
			PlatformMediaID: videoID,
			PlatformURL:     "https://www.youtube.com/watch?v=" + videoID,
		}, nil
	case "failed":
		return nil, fmt.Errorf("youtube processing failed for video %s", videoID)
	case "terminated":
		return nil, fmt.Errorf("youtube processing terminated for video %s", videoID)
	default:
		// Unknown status; treat as in-flight to avoid premature failure.
		slog.Warn("YouTube: unknown processing status, treating as in-flight",
			"video_id", videoID, "status", processingStatus)
		return nil, nil
	}
}

// fetchVideoStatus calls videos.list with part=snippet,status,processingDetails
// for a single video ID and returns the first (and only) item.
func (s *YouTubeOAuthService) fetchVideoStatus(ctx context.Context, accessToken, videoID string) (*youtubeVideo, error) {
	reqURL := "https://www.googleapis.com/youtube/v3/videos" +
		"?part=snippet,status,processingDetails" +
		"&id=" + url.QueryEscape(videoID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create youtube video status request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("youtube video status request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		return nil, fmt.Errorf("youtube video status returned %d: %s", resp.StatusCode, string(body))
	}

	var result youtubeVideosResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode youtube video status: %w", err)
	}

	if len(result.Items) == 0 {
		return nil, fmt.Errorf("youtube video %s not found", videoID)
	}

	return &result.Items[0], nil
}

// encodeYouTubePublishID encodes the channel ID and video ID into a single
// opaque publish ID used during the async publishing lifecycle.
//
// The composite is stored temporarily in post_target.platform_post_id while
// the target is in 'publishing' status. On a successful Reconcile, the final
// stored value is overwritten with the plain video ID.
func encodeYouTubePublishID(channelID, videoID string) string {
	return channelID + ":" + videoID
}

// decodeYouTubePublishID splits an encoded publish ID back into channel ID
// and video ID.
func decodeYouTubePublishID(publishID string) (channelID, videoID string, err error) {
	parts := strings.SplitN(publishID, ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid youtube publish id: %s", publishID)
	}
	return parts[0], parts[1], nil
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

// validateYouTubePrivacyLevel returns an error if level is not one of the
// three YouTube-recognized privacy values. Used by ValidateContent.
// Taglio 4b: no default — empty/unrecognized causes validation_error.
func validateYouTubePrivacyLevel(level string) error {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "public", "unlisted", "private":
		return nil
	default:
		return fmt.Errorf("youtube privacy_level must be one of public, unlisted, private (got %q)", level)
	}
}

// normalizeYouTubePrivacyLevel canonicalizes the privacy value for the
// YouTube API (lowercase). ValidateContent already guarantees the value
// is valid.
func normalizeYouTubePrivacyLevel(level string) string {
	return strings.ToLower(strings.TrimSpace(level))
}

// DiscoverAccounts returns the YouTube channels owned by the authenticated
// Google account. Uses channels.list with mine=true to retrieve all channels
// linked to the OAuth grant. Each channel becomes a distinct PlatformAccount
// with the real YouTube channel ID (UC...) as PlatformUserID.
func (s *YouTubeOAuthService) DiscoverAccounts(ctx context.Context, accessToken, _ string) ([]*DiscoveredAccount, error) {
	const maxChannels = 500

	params := url.Values{}
	params.Set("part", "snippet,statistics,contentDetails,status,brandingSettings")
	params.Set("mine", "true")
	params.Set("maxResults", "50")

	var allAccounts []*DiscoveredAccount
	var pageToken string

	for {
		if pageToken != "" {
			params.Set("pageToken", pageToken)
		} else {
			params.Del("pageToken")
		}

		reqURL := "https://www.googleapis.com/youtube/v3/channels?" + params.Encode()

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		if err != nil {
			return nil, fmt.Errorf("create youtube channel request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+accessToken)

		resp, err := s.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("youtube channel discovery: %w", err)
		}

		var result youtubeChannelsResponse
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
			resp.Body.Close()
			return nil, fmt.Errorf("youtube channel discovery returned %d: %s", resp.StatusCode, string(body))
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("decode youtube channels: %w", err)
		}
		resp.Body.Close()

		for _, ch := range result.Items {
			allAccounts = append(allAccounts, &DiscoveredAccount{
				Profile: models.PlatformProfile{
					PlatformUserID: ch.ID,
					Username:       ch.Snippet.Title,
				},
				Metadata: models.Metadata{
					"description":               ch.Snippet.Description,
					"handle":                    ch.Snippet.CustomURL,
					"avatar_url":                youtubeBestThumbnail(ch.Snippet.Thumbnails),
					"uploads_playlist_id":       ch.ContentDetails.RelatedPlaylists.Uploads,
					"country":                   ch.Snippet.Country,
					"subscriber_count":          ch.Statistics.SubscriberCount,
					"hidden_subscriber_count":   ch.Statistics.HiddenSubscriberCount,
					"video_count":               ch.Statistics.VideoCount,
					"view_count":                ch.Statistics.ViewCount,
				},
			})
		}

		if len(allAccounts) >= maxChannels {
			break
		}

		if result.NextPageToken == "" {
			break
		}
		pageToken = result.NextPageToken
	}

	if len(allAccounts) == 0 {
		return nil, fmt.Errorf("the authenticated Google account has no YouTube channel")
	}

	return allAccounts, nil
}

// youtubeBestThumbnail selects the highest-resolution thumbnail from a
// YouTube thumbnail set, falling back to default → medium → high.
func youtubeBestThumbnail(thumbs *youtubeThumbnails) string {
	if thumbs == nil {
		return ""
	}
	if thumbs.Maxres != nil && thumbs.Maxres.URL != "" {
		return thumbs.Maxres.URL
	}
	if thumbs.Standard != nil && thumbs.Standard.URL != "" {
		return thumbs.Standard.URL
	}
	if thumbs.High != nil && thumbs.High.URL != "" {
		return thumbs.High.URL
	}
	if thumbs.Medium != nil && thumbs.Medium.URL != "" {
		return thumbs.Medium.URL
	}
	if thumbs.Default != nil && thumbs.Default.URL != "" {
		return thumbs.Default.URL
	}
	return ""
}

// GetAccountDetails fetches the current state of a YouTube channel via
// channels.list with id=<platformUserID>. Returns rich account details
// including statistics, branding, and upload playlist ID.
func (s *YouTubeOAuthService) GetAccountDetails(ctx context.Context, accessToken, platformUserID string) (*models.AccountDetails, error) {
	reqURL := "https://www.googleapis.com/youtube/v3/channels" +
		"?part=snippet,statistics,contentDetails,status,brandingSettings" +
		"&id=" + url.QueryEscape(platformUserID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create youtube channel details request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("youtube channel details: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		return nil, fmt.Errorf("youtube channel details returned %d: %s", resp.StatusCode, string(body))
	}

	var result youtubeChannelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode youtube channel details: %w", err)
	}

	if len(result.Items) == 0 {
		return nil, fmt.Errorf("youtube channel %s not found", platformUserID)
	}

	ch := result.Items[0]
	now := s.now()

	details := &models.AccountDetails{
		ResourceType: "channel",
		ExternalID:   ch.ID,
		DisplayName:  ch.Snippet.Title,
		Description:  ch.Snippet.Description,
		Handle:       ch.Snippet.CustomURL,
		AvatarURL:    youtubeBestThumbnail(ch.Snippet.Thumbnails),
		PublicURL:    "https://www.youtube.com/channel/" + ch.ID,
		FetchedAt:    now,
		Metrics: []models.AccountMetric{
			{
				Key:          "subscribers",
				Label:        "Subscribers",
				Value:        ch.Statistics.SubscriberCount,
				DisplayValue: formatCount(ch.Statistics.SubscriberCount),
			},
			{
				Key:          "views",
				Label:        "Views",
				Value:        ch.Statistics.ViewCount,
				DisplayValue: formatCount(ch.Statistics.ViewCount),
			},
			{
				Key:          "videos",
				Label:        "Videos",
				Value:        ch.Statistics.VideoCount,
				DisplayValue: formatCount(ch.Statistics.VideoCount),
			},
		},
	}

	// Banner URL from branding settings.
	if ch.BrandingSettings.Image != nil {
		details.BannerURL = ch.BrandingSettings.Image.BannerImageUrl
	}

	// Platform-specific properties.
	details.Properties = map[string]any{
		"country":                  ch.Snippet.Country,
		"uploads_playlist_id":      ch.ContentDetails.RelatedPlaylists.Uploads,
		"hidden_subscriber_count":  ch.Statistics.HiddenSubscriberCount,
	}

	return details, nil
}

// ListAccountContent returns recent videos from a YouTube channel by
// reading the channel's uploads playlist and then fetching video
// details. Pagination is supported via the cursor (nextPageToken).
func (s *YouTubeOAuthService) ListAccountContent(ctx context.Context, accessToken, platformUserID string, cursor string, limit int) (*models.AccountContentPage, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}

	// Step 1: Get the uploads playlist ID for this channel.
	uploadsPlaylist, err := s.getUploadsPlaylistID(ctx, accessToken, platformUserID)
	if err != nil {
		return nil, fmt.Errorf("get uploads playlist: %w", err)
	}

	// Step 2: List recent items from the uploads playlist.
	videoIDs, nextPageToken, err := s.listPlaylistItems(ctx, accessToken, uploadsPlaylist, cursor, limit)
	if err != nil {
		return nil, fmt.Errorf("list playlist items: %w", err)
	}

	if len(videoIDs) == 0 {
		return &models.AccountContentPage{Items: []models.AccountContentItem{}}, nil
	}

	// Step 3: Fetch video details (snippet, statistics, contentDetails, status).
	items, err := s.getVideoDetails(ctx, accessToken, videoIDs)
	if err != nil {
		return nil, fmt.Errorf("get video details: %w", err)
	}

	return &models.AccountContentPage{
		Items:      items,
		NextCursor: nextPageToken,
	}, nil
}

func (s *YouTubeOAuthService) getUploadsPlaylistID(ctx context.Context, accessToken, channelID string) (string, error) {
	reqURL := "https://www.googleapis.com/youtube/v3/channels" +
		"?part=contentDetails" +
		"&id=" + url.QueryEscape(channelID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		return "", fmt.Errorf("channels.list returned %d: %s", resp.StatusCode, string(body))
	}

	var result youtubeChannelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	if len(result.Items) == 0 {
		return "", fmt.Errorf("channel %s not found", channelID)
	}

	return result.Items[0].ContentDetails.RelatedPlaylists.Uploads, nil
}

func (s *YouTubeOAuthService) listPlaylistItems(ctx context.Context, accessToken, playlistID, pageToken string, maxResults int) (videoIDs []string, nextPage string, err error) {
	params := url.Values{}
	params.Set("part", "snippet,contentDetails")
	params.Set("playlistId", playlistID)
	params.Set("maxResults", fmt.Sprintf("%d", maxResults))
	if pageToken != "" {
		params.Set("pageToken", pageToken)
	}

	reqURL := "https://www.googleapis.com/youtube/v3/playlistItems?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		return nil, "", fmt.Errorf("playlistItems.list returned %d: %s", resp.StatusCode, string(body))
	}

	var result youtubePlaylistItemsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, "", err
	}

	ids := make([]string, 0, len(result.Items))
	for _, item := range result.Items {
		if item.ContentDetails.VideoID != "" {
			ids = append(ids, item.ContentDetails.VideoID)
		}
	}

	return ids, result.NextPageToken, nil
}

func (s *YouTubeOAuthService) getVideoDetails(ctx context.Context, accessToken string, videoIDs []string) ([]models.AccountContentItem, error) {
	params := url.Values{}
	params.Set("part", "snippet,statistics,contentDetails,status")
	params.Set("id", strings.Join(videoIDs, ","))

	reqURL := "https://www.googleapis.com/youtube/v3/videos?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		return nil, fmt.Errorf("videos.list returned %d: %s", resp.StatusCode, string(body))
	}

	var result youtubeVideosResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	items := make([]models.AccountContentItem, 0, len(result.Items))
	for _, v := range result.Items {
		item := models.AccountContentItem{
			ExternalID:   v.ID,
			Title:        v.Snippet.Title,
			Description:  v.Snippet.Description,
			ThumbnailURL: youtubeBestThumbnail(v.Snippet.Thumbnails),
			PublicURL:    "https://www.youtube.com/watch?v=" + v.ID,
			Privacy:      v.Status.PrivacyStatus,
			Status:       v.Status.UploadStatus,
			Metrics: []models.AccountMetric{
				{
					Key:          "views",
					Label:        "Views",
					Value:        v.Statistics.ViewCount,
					DisplayValue: formatCount(v.Statistics.ViewCount),
				},
				{
					Key:          "likes",
					Label:        "Likes",
					Value:        v.Statistics.LikeCount,
					DisplayValue: formatCount(v.Statistics.LikeCount),
				},
				{
					Key:          "comments",
					Label:        "Comments",
					Value:        v.Statistics.CommentCount,
					DisplayValue: formatCount(v.Statistics.CommentCount),
				},
			},
		}

		if v.Snippet.PublishedAt != "" {
			if t, err := time.Parse(time.RFC3339, v.Snippet.PublishedAt); err == nil {
				item.PublishedAt = &t
			}
		}

		item.Properties = map[string]any{
			"duration": v.ContentDetails.Duration,
		}

		items = append(items, item)
	}

	return items, nil
}

// formatCount returns a human-readable count string (e.g. "125K", "1.2M").
func formatCount(n int64) string {
	switch {
	case n >= 1_000_000_000:
		return fmt.Sprintf("%.1fB", float64(n)/1_000_000_000)
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// --- YouTube Data API v3 response types ---

type youtubeChannelsResponse struct {
	Items         []youtubeChannel `json:"items"`
	NextPageToken string           `json:"nextPageToken"`
	PageInfo      youtubePageInfo  `json:"pageInfo"`
}

type youtubePageInfo struct {
	TotalResults   int `json:"totalResults"`
	ResultsPerPage int `json:"resultsPerPage"`
}

type youtubeChannel struct {
	ID              string                `json:"id"`
	Snippet         youtubeChannelSnippet `json:"snippet"`
	Statistics      youtubeStatistics     `json:"statistics"`
	ContentDetails  youtubeContentDetails `json:"contentDetails"`
	BrandingSettings youtubeBranding      `json:"brandingSettings"`
}

type youtubeChannelSnippet struct {
	Title       string            `json:"title"`
	Description string            `json:"description"`
	CustomURL   string            `json:"customUrl"`
	Country     string            `json:"country"`
	Thumbnails  *youtubeThumbnails `json:"thumbnails"`
}

type youtubeStatistics struct {
	SubscriberCount      int64 `json:"subscriberCount"`
	HiddenSubscriberCount bool  `json:"hiddenSubscriberCount"`
	ViewCount            int64 `json:"viewCount"`
	VideoCount           int64 `json:"videoCount"`
}

type youtubeContentDetails struct {
	RelatedPlaylists youtubeRelatedPlaylists `json:"relatedPlaylists"`
}

type youtubeRelatedPlaylists struct {
	Uploads string `json:"uploads"`
}

type youtubeBranding struct {
	Image *youtubeBrandingImage `json:"image"`
}

type youtubeBrandingImage struct {
	BannerExternalURL string `json:"bannerExternalUrl"`
	BannerImageUrl    string `json:"bannerImageUrl"`
	BannerMobileExtra  string `json:"bannerMobileExtraDevicesImageUrl"`
}

type youtubeThumbnails struct {
	Default  *youtubeThumbnail `json:"default"`
	Medium   *youtubeThumbnail `json:"medium"`
	High     *youtubeThumbnail `json:"high"`
	Standard *youtubeThumbnail `json:"standard"`
	Maxres   *youtubeThumbnail `json:"maxres"`
}

type youtubeThumbnail struct {
	URL    string `json:"url"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

type youtubePlaylistItemsResponse struct {
	Items          []youtubePlaylistItem `json:"items"`
	NextPageToken  string                `json:"nextPageToken"`
}

type youtubePlaylistItem struct {
	ContentDetails youtubePlaylistItemContentDetails `json:"contentDetails"`
}

type youtubePlaylistItemContentDetails struct {
	VideoID string `json:"videoId"`
}

type youtubeVideosResponse struct {
	Items []youtubeVideo `json:"items"`
}

type youtubeVideo struct {
	ID                string                         `json:"id"`
	Snippet           youtubeVideoSnippet            `json:"snippet"`
	Statistics        youtubeVideoStats              `json:"statistics"`
	ContentDetails    youtubeVideoContent            `json:"contentDetails"`
	Status            youtubeVideoStatus             `json:"status"`
	ProcessingDetails *youtubeVideoProcessingDetails `json:"processingDetails,omitempty"`
}

type youtubeVideoSnippet struct {
	Title       string            `json:"title"`
	Description string            `json:"description"`
	PublishedAt string            `json:"publishedAt"`
	ChannelID   string            `json:"channelId"`
	Thumbnails  *youtubeThumbnails `json:"thumbnails"`
}

type youtubeVideoStats struct {
	ViewCount    int64 `json:"viewCount"`
	LikeCount    int64 `json:"likeCount"`
	CommentCount int64 `json:"commentCount"`
}

type youtubeVideoContent struct {
	Duration string `json:"duration"`
}

type youtubeVideoStatus struct {
	PrivacyStatus string `json:"privacyStatus"`
	UploadStatus  string `json:"uploadStatus"`
}

type youtubeVideoProcessingDetails struct {
	ProcessingStatus string `json:"processingStatus"`
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

// -----------------------------------------------------------------------------
// Compile-time conformance to the central Platform Registry contract.
// Taglio 4.3.
// -----------------------------------------------------------------------------
var (
	_ OAuthProvider         = (*YouTubeOAuthService)(nil)
	_ ContentValidator      = (*YouTubeOAuthService)(nil)
	_ Publisher             = (*YouTubeOAuthService)(nil)
	_ AsyncPublisher        = (*YouTubeOAuthService)(nil)
	_ AccountDiscoverer     = (*YouTubeOAuthService)(nil)
	_ AccountDetailsProvider = (*YouTubeOAuthService)(nil)
	_ AccountContentProvider = (*YouTubeOAuthService)(nil)
)
