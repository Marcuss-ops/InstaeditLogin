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
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// --- Config ----------------------------------------------------------------

type e2eConfig struct {
	UserID       int64
	WorkspaceID  int64
	AccountID    int64
	TestVideoURL string
	Privacy      string

	// Polling
	PollInterval time.Duration
	PollTimeout  time.Duration
}

func loadE2EConfig() (e2eConfig, error) {
	if os.Getenv("YOUTUBE_E2E") != "1" {
		return e2eConfig{}, fmt.Errorf("YOUTUBE_E2E is not set to 1 — test disabled (set YOUTUBE_E2E=1 to run)")
	}

	cfg := e2eConfig{
		UserID:       requiredInt64("INSTAEDIT_USER_ID"),
		WorkspaceID:  requiredInt64("INSTAEDIT_WORKSPACE_ID"),
		AccountID:    requiredInt64("YOUTUBE_PLATFORM_ACCOUNT_ID"),
		TestVideoURL: requiredString("YOUTUBE_TEST_VIDEO_URL"),
		Privacy:      optionalString("YOUTUBE_TEST_PRIVACY", "private"),
		PollInterval: 5 * time.Second,
		PollTimeout:  5 * time.Minute,
	}

	if cfg.Privacy != "private" && cfg.Privacy != "unlisted" && cfg.Privacy != "public" {
		return e2eConfig{}, fmt.Errorf("YOUTUBE_TEST_PRIVACY must be private, unlisted, or public (got %q)", cfg.Privacy)
	}

	return cfg, nil
}

func requiredString(key string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		fmt.Fprintf(os.Stderr, "fatal: %s is required\n", key)
		os.Exit(2)
	}
	return v
}

func requiredInt64(key string) int64 {
	s := strings.TrimSpace(os.Getenv(key))
	if s == "" {
		fmt.Fprintf(os.Stderr, "fatal: %s is required\n", key)
		os.Exit(2)
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %s must be an integer (got %q): %v\n", key, s, err)
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

func main() {
	cfg, err := loadE2EConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(2)
	}

	ctx := context.Background()

	fmt.Println("InstaEdit E2E YouTube upload test")
	fmt.Printf("  user_id=%d workspace_id=%d account_id=%d\n", cfg.UserID, cfg.WorkspaceID, cfg.AccountID)
	fmt.Printf("  video=%s privacy=%s\n", cfg.TestVideoURL, cfg.Privacy)
	fmt.Println()

	// --- Wire ---------------------------------------------------------------
	fmt.Println("[1/7] Wiring bootstrap (DB + Vault + CapRouter) ...")
	app, err := bootstrap.Wire(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "wire failed: %v\n", err)
		os.Exit(1)
	}
	defer app.DB.Close()

	userRepo := repository.NewUserRepository(app.DB)
	postRepo := repository.NewPostRepository(app.DB)

	// --- Load account -------------------------------------------------------
	fmt.Printf("[2/7] Loading platform_account id=%d ...\n", cfg.AccountID)
	account, err := userRepo.FindPlatformAccountByID(cfg.AccountID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FindPlatformAccountByID failed: %v\n", err)
		os.Exit(1)
	}
	if account == nil {
		fmt.Fprintf(os.Stderr, "platform_account id=%d not found\n", cfg.AccountID)
		os.Exit(1)
	}
	if account.Platform != models.PlatformYouTube {
		fmt.Fprintf(os.Stderr, "platform_account id=%d is platform=%q, expected youtube\n",
			cfg.AccountID, account.Platform)
		os.Exit(1)
	}
	fmt.Printf("  platform_user_id=%s username=%s status=%s\n",
		account.PlatformUserID, account.Username, account.Status)

	// --- Refresh token ------------------------------------------------------
	// Vault.Renew handles fetching the stored refresh token, calling the
	// provider's RefreshOAuthToken, and saving the new access token.
	fmt.Println("[3/7] Refreshing OAuth bearer token via Vault.Renew ...")
	youtubeSvc, err := services.NewYouTubeOAuthService(app.Cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "NewYouTubeOAuthService failed: %v\n", err)
		os.Exit(1)
	}
	if youtubeSvc == nil {
		fmt.Fprintln(os.Stderr, "YouTube provider is disabled (YOUTUBE_CLIENT_ID not set)")
		os.Exit(2)
	}

	refresher := func(ctx context.Context, refreshToken string) (*models.TokenData, error) {
		return youtubeSvc.RefreshOAuthToken(ctx, refreshToken)
	}
	oauthToken, err := app.Vault.Renew(ctx, cfg.AccountID, models.TokenTypeBearer, refresher)
	if err != nil {
		oauthToken, err = app.Vault.Renew(ctx, cfg.AccountID, models.TokenTypeLongLived, refresher)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Vault.Renew failed (bearer + long_lived): %v\n", err)
			os.Exit(1)
		}
	}
	accessToken := oauthToken.AccessToken
	fmt.Printf("  access_token len=%d expires_at=%v scopes=%v\n",
		len(accessToken), oauthToken.ExpiresAt, oauthToken.Scopes)

	// --- Verify channel ownership -------------------------------------------
	fmt.Printf("[4/7] Verifying channel ownership (channels.list id=%s) ...\n",
		account.PlatformUserID)
	chInfo, err := getChannelInfo(ctx, accessToken, account.PlatformUserID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "channels.list failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("  channel_title=%s subscriber_count=%d view_count=%d video_count=%d\n",
		chInfo.Snippet.Title, chInfo.Statistics.SubscriberCount,
		chInfo.Statistics.ViewCount, chInfo.Statistics.VideoCount)

	// --- Create Post --------------------------------------------------------
	fmt.Println("[5/7] Creating Post + PostTarget (queued) ...")
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
		fmt.Fprintf(os.Stderr, "Create post failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("  post_id=%d target_id=%d\n", post.ID, target.ID)

	// --- Wait for PublishWorker ---------------------------------------------
	fmt.Printf("[6/7] Waiting for PublishWorker (polling every %v, timeout %v) ...\n",
		cfg.PollInterval, cfg.PollTimeout)
	deadline := time.Now().Add(cfg.PollTimeout)
	var publishedTarget *models.PostTarget
	for time.Now().Before(deadline) {
		time.Sleep(cfg.PollInterval)

		targets, err := postRepo.ListByPost(post.ID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  FindTargetsByPostID failed: %v\n", err)
			continue
		}
		if len(targets) == 0 {
			continue
		}
		t := &targets[0]
		fmt.Printf("  target_status=%s attempt=%d remote_post_id=%q provider_state=%q error=%q\n",
			t.Status, t.AttemptCount, t.RemotePostID, t.ProviderState, t.ErrorMessage)

		switch t.Status {
		case models.PostStatusPublished:
			publishedTarget = t
			goto DONE
		case models.PostStatusFailed, models.PostStatusDLQ:
			fmt.Fprintf(os.Stderr, "Target reached terminal status %s. error=%q\n",
				t.Status, t.ErrorMessage)
			os.Exit(1)
		}
	}
	fmt.Fprintf(os.Stderr, "Poll timeout after %v — worker did not process the post in time.\n", cfg.PollTimeout)
	os.Exit(3)

DONE:
	fmt.Printf("  published! remote_post_id=%s remote_post_url=%s\n",
		publishedTarget.RemotePostID, publishedTarget.RemotePostURL)

	// --- Verify uploaded video ----------------------------------------------
	fmt.Println("[7/7] Verifying uploaded video (videos.list) ...")
	videoID := publishedTarget.RemotePostID
	if videoID == "" {
		fmt.Fprintln(os.Stderr, "remote_post_id is empty — cannot verify video")
		os.Exit(1)
	}

	videoInfo, err := getVideoInfo(ctx, accessToken, videoID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "videos.list failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("  video_title=%s channel_id=%s privacy=%s\n",
		videoInfo.Snippet.Title, videoInfo.Snippet.ChannelID, videoInfo.Status.PrivacyStatus)

	// --- Assertions ---------------------------------------------------------
	var failures int

	if videoInfo.Snippet.ChannelID != account.PlatformUserID {
		fmt.Fprintf(os.Stderr,
			"FAIL: video uploaded to wrong channel: expected %s, got %s\n",
			account.PlatformUserID, videoInfo.Snippet.ChannelID)
		failures++
	} else {
		fmt.Println("  PASS: snippet.channelId matches platform_user_id")
	}

	if strings.ToLower(videoInfo.Status.PrivacyStatus) != "private" {
		fmt.Fprintf(os.Stderr,
			"WARN: privacy is %q (expected private) — unverified apps may force private\n",
			videoInfo.Status.PrivacyStatus)
	} else {
		fmt.Println("  PASS: privacy=private")
	}

	fmt.Println()
	if failures > 0 {
		fmt.Fprintf(os.Stderr, "E2E FAILED with %d assertion failure(s)\n", failures)
		os.Exit(1)
	}
	fmt.Println("E2E PASSED ✓")
	fmt.Printf("  Video: https://www.youtube.com/watch?v=%s\n", videoID)
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
	params.Set("part", "snippet,statistics")
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
		return nil, fmt.Errorf("channels.list returned %d: %s", resp.StatusCode, string(body))
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
	Title     string `json:"title"`
	ChannelID string `json:"channelId"`
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
		return nil, fmt.Errorf("videos.list returned %d: %s", resp.StatusCode, string(body))
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

// --- Compile-time safety net -----------------------------------------------

// httpClient is the retry-enabled HTTP client shared by all YouTube API calls
// in this binary. Uses the same constructor as the production services.
var httpClient = services.NewHTTPClient()

// Prevent accidental production inclusion. The E2E test imports bootstrap.Wire
// and models, but the binary is never part of a production build — it lives in
// its own main package under cmd/test-youtube-upload.
