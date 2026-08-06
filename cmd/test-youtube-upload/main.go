// Command test-youtube-upload is an E2E smoke test that exercises the full
// InstaEdit YouTube publish pipeline end-to-end:
//
//  1. Load the platform_account row
//  2. Refresh the OAuth bearer token
//  3. Call channels.list to verify the token controls the expected channel
//  4. Create a Post + PostTarget (queued)
//  5. Wait for the PublishWorker to process it (poll target status)
//  6. Call videos.list on the resulting video ID
//  7. Assert privacy=private and snippet.channelId == account.platform_user_id
//
// The test is gated behind YOUTUBE_E2E=1 so it never runs during CI.
// It uses the production bootstrap.Wire path (same DB + Vault + CapRouter
// as the real workers), NOT direct HTTP calls to the API.
//
// Configuration is environment-only so it composes cleanly with `make`
// targets and shell scripts:
//
//	YOUTUBE_E2E=1                     (required gate)
//	INSTAEDIT_USER_ID=3               (user who owns the account)
//	INSTAEDIT_WORKSPACE_ID=3          (workspace for the post)
//	YOUTUBE_PLATFORM_ACCOUNT_ID=127   (platform_accounts.id)
//	YOUTUBE_TEST_VIDEO_URL=https://... (publicly-accessible mp4)
//	YOUTUBE_TEST_PRIVACY=private      (always private for testing)
//
// The decisive assertion is:
//
//	uploadedVideo.Snippet.ChannelID == account.PlatformUserID
//
// This proves InstaEdit published to the selected channel, not just any
// channel the OAuth grant has access to.
//
// Exit codes: 0 = success, 1 = test failure, 2 = config/usage error,
// 3 = poll timeout (worker didn't process the post in time).
package main

import (
	"bytes"
	"context"
	"encoding/json"

	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/bootstrap"
	"github.com/Marcuss-ops/InstaeditLogin/internal/database"
	appLogging "github.com/Marcuss-ops/InstaeditLogin/internal/logging"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// --- Config ----------------------------------------------------------------

type e2eConfig struct {
	UserID        int64
	WorkspaceID   int64
	AccountID     int64
	TestVideoURL  string
	VerifyVideoID string
	Privacy       string

	// Polling
	PollInterval time.Duration
	PollTimeout  time.Duration
}

func loadE2EConfig() (e2eConfig, error) {
	if os.Getenv("YOUTUBE_E2E") != "1" {
		return e2eConfig{}, fmt.Errorf("YOUTUBE_E2E is not set to 1 — test disabled (set YOUTUBE_E2E=1 to run)")
	}

	cfg := e2eConfig{
		UserID:        requiredInt64("INSTAEDIT_USER_ID"),
		WorkspaceID:   requiredInt64("INSTAEDIT_WORKSPACE_ID"),
		AccountID:     requiredInt64("YOUTUBE_PLATFORM_ACCOUNT_ID"),
		TestVideoURL:  optionalString("YOUTUBE_TEST_VIDEO_URL", ""),
		VerifyVideoID: optionalString("YOUTUBE_VERIFY_VIDEO_ID", ""),
		Privacy:       optionalString("YOUTUBE_TEST_PRIVACY", "private"),
		PollInterval:  5 * time.Second,
		PollTimeout:   5 * time.Minute,
	}

	if cfg.Privacy != "private" && cfg.Privacy != "unlisted" && cfg.Privacy != "public" {
		return e2eConfig{}, fmt.Errorf("YOUTUBE_TEST_PRIVACY must be private, unlisted, or public (got %q)", cfg.Privacy)
	}
	if cfg.TestVideoURL == "" && cfg.VerifyVideoID == "" {
		return e2eConfig{}, fmt.Errorf("set YOUTUBE_TEST_VIDEO_URL for upload or YOUTUBE_VERIFY_VIDEO_ID for verification")
	}

	return cfg, nil
}

func requiredString(key string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		safeFprintf("fatal: %s is required\n", key)
		os.Exit(2)
	}
	return v
}

func requiredInt64(key string) int64 {
	s := strings.TrimSpace(os.Getenv(key))
	if s == "" {
		safeFprintf("fatal: %s is required\n", key)
		os.Exit(2)
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		safeFprintf("fatal: %s must be an integer (got %q): %v\n", key, s, err)
		os.Exit(2)
	}
	return v
}

func optionalString(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

// --- Main ------------------------------------------------------------------

var (
	safeStdout io.Writer = appLogging.NewRedactingWriter(os.Stdout)
	safeStderr io.Writer = appLogging.NewRedactingWriter(os.Stderr)
)

func safePrintf(format string, args ...any)  { _, _ = fmt.Fprintf(safeStdout, format, args...) }
func safePrintln(args ...any)                { _, _ = fmt.Fprintln(safeStdout, args...) }
func safeFprintf(format string, args ...any) { _, _ = fmt.Fprintf(safeStderr, format, args...) }
func safeFprintln(args ...any)               { _, _ = fmt.Fprintln(safeStderr, args...) }

func main() {
	cfg, err := loadE2EConfig()
	if err != nil {
		safeFprintf("config error: %v\n", err)
		os.Exit(2)
	}

	ctx := context.Background()

	safePrintln("InstaEdit E2E YouTube upload test")
	safePrintf("  user_id=%d workspace_id=%d account_id=%d\n", cfg.UserID, cfg.WorkspaceID, cfg.AccountID)
	safePrintf("  video_configured=%t privacy=%s\n", cfg.TestVideoURL != "", cfg.Privacy)
	safePrintln()

	// --- Wire ---------------------------------------------------------------
	safePrintln("[1/7] Wiring bootstrap (DB + Vault + CapRouter) ...")
	app, err := bootstrap.Wire(ctx)
	if err != nil {
		safeFprintln("wire failed (details withheld from logs)")
		os.Exit(1)
	}
	if err := database.VerifyInstallationIdentity(ctx, app.DB, app.Cfg.Database.ExpectedInstallationUUID); err != nil {
		safeFprintln("database identity verification failed: DATABASE_IDENTITY_MISMATCH")
		os.Exit(1)
	}
	defer app.DB.Close()

	userRepo := repository.NewUserRepository(app.DB)
	postRepo := repository.NewPostRepository(app.DB)

	// --- Load account -------------------------------------------------------
	safePrintf("[2/7] Loading platform_account id=%d ...\n", cfg.AccountID)
	account, err := userRepo.FindPlatformAccountByID(cfg.AccountID)
	if err != nil {
		safeFprintln("FindPlatformAccountByID failed (details withheld from logs)")
		os.Exit(1)
	}
	if account == nil {
		safeFprintf("platform_account id=%d not found\n", cfg.AccountID)
		os.Exit(1)
	}
	if account.Platform != models.PlatformYouTube {
		safeFprintf("platform_account id=%d is platform=%q, expected youtube\n",
			cfg.AccountID, account.Platform)
		os.Exit(1)
	}
	safePrintf("  platform_user_id=%s username=%s status=%s\n",
		account.PlatformUserID, account.Username, account.Status)

	// --- Refresh token ------------------------------------------------------
	// Vault.Renew handles fetching the stored refresh token, calling the
	// provider's RefreshOAuthToken, and saving the new access token.
	safePrintln("[3/7] Refreshing OAuth bearer token via Vault.Renew ...")
	youtubeSvc, err := services.NewYouTubeOAuthService(app.Cfg)
	if err != nil {
		safeFprintln("NewYouTubeOAuthService failed (details withheld from logs)")
		os.Exit(1)
	}
	if youtubeSvc == nil {
		safeFprintln("YouTube provider is disabled (YOUTUBE_CLIENT_ID not set)")
		os.Exit(2)
	}

	refresher := func(ctx context.Context, refreshToken string) (*models.TokenData, error) {
		return youtubeSvc.RefreshOAuthToken(ctx, refreshToken)
	}
	oauthToken, err := app.Vault.Renew(ctx, cfg.AccountID, models.TokenTypeBearer, refresher)
	if err != nil {
		oauthToken, err = app.Vault.Renew(ctx, cfg.AccountID, models.TokenTypeLongLived, refresher)
		if err != nil {
			safeFprintln("Vault.Renew failed (details withheld from logs)")
			os.Exit(1)
		}
	}
	accessToken := oauthToken.AccessToken
	safePrintf("  oauth_token_refreshed=true expires_at=%v scope_count=%d\n",
		oauthToken.ExpiresAt, len(oauthToken.Scopes))

	// --- Verify channel ownership -------------------------------------------
	safePrintf("[4/7] Verifying channel ownership (channels.list id=%s) ...\n",
		account.PlatformUserID)
	chInfo, err := getChannelInfo(ctx, accessToken, account.PlatformUserID)
	if err != nil {
		safeFprintln("channels.list failed (details withheld from logs)")
		os.Exit(1)
	}
	safePrintf("  channel_title=%s subscriber_count=%d view_count=%d video_count=%d\n",
		chInfo.Snippet.Title, chInfo.Statistics.SubscriberCount,
		chInfo.Statistics.ViewCount, chInfo.Statistics.VideoCount)

	if cfg.VerifyVideoID != "" {
		safePrintf("[5/5] Verifying existing video (videos.list id=%s) ...\n", cfg.VerifyVideoID)
		videoInfo, err := getVideoInfo(ctx, accessToken, cfg.VerifyVideoID)
		if err != nil {
			safeFprintln("videos.list failed (details withheld from logs)")
			os.Exit(1)
		}
		safePrintf("  video_title=%s channel_id=%s privacy=%s upload_status=%s\n",
			videoInfo.Snippet.Title, videoInfo.Snippet.ChannelID,
			videoInfo.Status.PrivacyStatus, videoInfo.Status.UploadStatus)
		safePrintf("  thumbnail_available=%t\n", videoInfo.Snippet.Thumbnails.Default.URL != "")
		if videoInfo.Snippet.ChannelID != account.PlatformUserID {
			safeFprintf("FAIL: video channel mismatch: expected %s, got %s\n", account.PlatformUserID, videoInfo.Snippet.ChannelID)
			os.Exit(1)
		}
		if videoInfo.Snippet.Thumbnails.Default.URL == "" {
			safeFprintln("FAIL: YouTube returned no thumbnail")
			os.Exit(1)
		}
		safePrintln("VERIFY PASSED: channel binding and thumbnail are present ✓")
		return
	}

	if os.Getenv("YOUTUBE_LIST_LIBRARY") == "1" {
		safePrintln("[5/5] Listing existing editable YouTube videos ...")
		page, err := youtubeSvc.ListEditableVideos(ctx, accessToken, account.PlatformUserID, "")
		if err != nil {
			safeFprintln("youtube library failed (details withheld from logs)")
			os.Exit(1)
		}
		safePrintf("  returned=%d\n", len(page.Items))
		for _, item := range page.Items {
			published := "unknown"
			if item.PublishedAt != nil {
				published = item.PublishedAt.Format(time.RFC3339)
			}
			safePrintf("  %s privacy=%s published_at=%s title=%q\n", item.ID, item.Privacy, published, item.Title)
		}
		return
	}

	if videoID := strings.TrimSpace(os.Getenv("YOUTUBE_SET_PRIVATE_VIDEO_ID")); videoID != "" {
		safePrintf("[5/5] Setting existing video private (id=%s) ...\n", videoID)
		if err := setVideoPrivate(ctx, accessToken, videoID); err != nil {
			safeFprintln("videos.update failed (details withheld from logs)")
			os.Exit(1)
		}
		safePrintln("  privacy=private")
		return
	}

	// --- Create Post --------------------------------------------------------
	safePrintln("[5/7] Creating Post + PostTarget (queued) ...")
	now := time.Now()
	post := &models.Post{
		WorkspaceID: cfg.WorkspaceID,
		Title:       fmt.Sprintf("InstaEdit E2E %s", now.UTC().Format(time.RFC3339)),
		Caption:     "End-to-end upload test via InstaEdit PublishWorker",
		MediaURL:    cfg.TestVideoURL,
		Status:      models.PostStatusQueued,
	}
	target := &models.PostTarget{
		PlatformAccountID: cfg.AccountID,
		Status:            models.PostStatusQueued,
	}

	if err := postRepo.Create(post, []*models.PostTarget{target}); err != nil {
		safeFprintln("Create post failed (details withheld from logs)")
		os.Exit(1)
	}
	safePrintf("  post_id=%d target_id=%d\n", post.ID, target.ID)

	// --- Wait for PublishWorker ---------------------------------------------
	safePrintf("[6/7] Waiting for PublishWorker (polling every %v, timeout %v) ...\n",
		cfg.PollInterval, cfg.PollTimeout)
	deadline := time.Now().Add(cfg.PollTimeout)
	var publishedTarget *models.PostTarget
	for time.Now().Before(deadline) {
		time.Sleep(cfg.PollInterval)

		targets, err := postRepo.ListByPost(post.ID)
		if err != nil {
			safeFprintln("  FindTargetsByPostID failed (details withheld from logs)")
			continue
		}
		if len(targets) == 0 {
			continue
		}
		t := &targets[0]
		safePrintf("  target_status=%s attempt=%d remote_post_id_present=%t provider_state=%s error_present=%t\n",
			t.Status, t.AttemptCount, t.RemotePostID != "", t.ProviderState, t.ErrorMessage != "")

		switch t.Status {
		case models.PostStatusPublished:
			publishedTarget = t
			goto DONE
		case models.PostStatusFailed, models.PostStatusDLQ:
			safeFprintf("Target reached terminal status %s (error details withheld)\n", t.Status)
			os.Exit(1)
		}
	}
	safeFprintf("Poll timeout after %v — worker did not process the post in time.\n", cfg.PollTimeout)
	os.Exit(3)

DONE:
	safePrintf("  published=true remote_post_id_present=%t remote_post_url_present=%t\n",
		publishedTarget.RemotePostID != "", publishedTarget.RemotePostURL != "")

	// --- Verify uploaded video ----------------------------------------------
	safePrintln("[7/7] Verifying uploaded video (videos.list) ...")
	videoID := publishedTarget.RemotePostID
	if videoID == "" {
		safeFprintln("remote_post_id is empty — cannot verify video")
		os.Exit(1)
	}

	videoInfo, err := getVideoInfo(ctx, accessToken, videoID)
	if err != nil {
		safeFprintln("videos.list failed (details withheld from logs)")
		os.Exit(1)
	}

	safePrintf("  video_title=%s channel_id=%s privacy=%s\n",
		videoInfo.Snippet.Title, videoInfo.Snippet.ChannelID, videoInfo.Status.PrivacyStatus)

	// --- Assertions ---------------------------------------------------------
	var failures int

	if videoInfo.Snippet.ChannelID != account.PlatformUserID {
		safeFprintf(
			"FAIL: video uploaded to wrong channel: expected %s, got %s\n",
			account.PlatformUserID, videoInfo.Snippet.ChannelID)
		failures++
	} else {
		safePrintln("  PASS: snippet.channelId matches platform_user_id")
	}

	if strings.ToLower(videoInfo.Status.PrivacyStatus) != "private" {
		safeFprintf(
			"WARN: privacy is %q (expected private) — unverified apps may force private\n",
			videoInfo.Status.PrivacyStatus)
	} else {
		safePrintln("  PASS: privacy=private")
	}

	safePrintln()
	if failures > 0 {
		safeFprintf("E2E FAILED with %d assertion failure(s)\n", failures)
		os.Exit(1)
	}
	safePrintln("E2E PASSED ✓")
	safePrintln("  Video published successfully (URL withheld from logs)")
}

// --- YouTube API helpers ---------------------------------------------------

// channelSnippet mirrors a subset of the channels.list JSON response used by
// the E2E test. The full response types live in internal/services but are
// unexported, so we duplicate the minimal subset here to keep the binary
// self-contained.
type channelInfo struct {
	ID         string            `json:"id"`
	Snippet    channelSnippet    `json:"snippet"`
	Statistics channelStatistics `json:"statistics"`
}

type channelSnippet struct {
	Title string `json:"title"`
}

type channelStatistics struct {
	SubscriberCount int64 `json:"subscriberCount"`
	ViewCount       int64 `json:"viewCount"`
	VideoCount      int64 `json:"videoCount"`
}

func getChannelInfo(ctx context.Context, accessToken, channelID string) (*channelInfo, error) {
	params := url.Values{}
	// Statistics values are returned by YouTube as JSON strings and are not
	// needed for this ownership/upload smoke test. Request only the channel
	// snippet so a formatting change in count fields cannot block the upload.
	params.Set("part", "snippet")
	params.Set("id", channelID)

	reqURL := "https://www.googleapis.com/youtube/v3/channels?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("channels.list request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("channels.list returned %d", resp.StatusCode)
	}

	var result struct {
		Items []channelInfo `json:"items"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode channels.list: %w", err)
	}

	if len(result.Items) == 0 {
		return nil, fmt.Errorf("channel %s not found", channelID)
	}

	return &result.Items[0], nil
}

// videoInfo mirrors the subset of videos.list JSON needed by assertions.
type videoInfo struct {
	Snippet videoSnippet `json:"snippet"`
	Status  videoStatus  `json:"status"`
}

type videoSnippet struct {
	Title      string          `json:"title"`
	ChannelID  string          `json:"channelId"`
	Thumbnails videoThumbnails `json:"thumbnails"`
}

type videoThumbnails struct {
	Default  videoThumbnail `json:"default"`
	Medium   videoThumbnail `json:"medium"`
	High     videoThumbnail `json:"high"`
	Standard videoThumbnail `json:"standard"`
	Maxres   videoThumbnail `json:"maxres"`
}

type videoThumbnail struct {
	URL string `json:"url"`
}

type videoStatus struct {
	PrivacyStatus string `json:"privacyStatus"`
	UploadStatus  string `json:"uploadStatus"`
}

func getVideoInfo(ctx context.Context, accessToken, videoID string) (*videoInfo, error) {
	params := url.Values{}
	params.Set("part", "snippet,status")
	params.Set("id", videoID)

	reqURL := "https://www.googleapis.com/youtube/v3/videos?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("videos.list request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("videos.list returned %d", resp.StatusCode)
	}

	var result struct {
		Items []videoInfo `json:"items"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode videos.list: %w", err)
	}

	if len(result.Items) == 0 {
		return nil, fmt.Errorf("video %s not found", videoID)
	}

	return &result.Items[0], nil
}

func setVideoPrivate(ctx context.Context, accessToken, videoID string) error {
	body, err := json.Marshal(map[string]any{
		"id":     videoID,
		"status": map[string]string{"privacyStatus": "private"},
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut,
		"https://www.googleapis.com/youtube/v3/videos?part=status",
		bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("videos.update returned %d", resp.StatusCode)
	}
	return nil
}

// --- Compile-time safety net -----------------------------------------------

// httpClient is the retry-enabled HTTP client shared by all YouTube API calls
// in this binary. Uses the same constructor as the production services.
var httpClient = services.NewHTTPClient()

// Prevent accidental production inclusion. The E2E test imports bootstrap.Wire
// and models, but the binary is never part of a production build — it lives in
// its own main package under cmd/test-youtube-upload.
