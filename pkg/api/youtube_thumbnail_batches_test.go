package api

// Thumbnail-batch security tests (P0):
//  1. Token renewal — the batch's private-video check
//     (ensurePrivateYouTubeBatchVideo) and the editor-session publish
//     (executePublishYouTubeEditorSession) MUST renew the access token
//     via vault.Renew when the stored bearer is expired — previously
//     they called vault.Get and a stale access token failed the whole
//     batch, forcing the operator to reconnect the channel for no
//     reason.
//  2. Cross-channel ownership — with shared Google grants (084/085) a
//     payload can name platform_account A but a youtube_video_id owned
//     by sibling channel B on the same grant. The batch MUST verify
//     video.channel_id == platform_account.platform_user_id (after
//     ValidateChannelBinding) and block before any modification.
import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// TestEnsurePrivateYouTubeBatchVideo_RenewsExpiredAccessToken is the P0
// regression proof for the "access token scaduto → refresh automatico"
// contract: the stored bearer token is expired (vault.Get would return
// "token expired"), but vault.Renew succeeds with a FRESH access token.
// The batch's private-video check must complete and call YouTube with the
// fresh token — never fall back to the expired Get path and never fail.
func TestEnsurePrivateYouTubeBatchVideo_RenewsExpiredAccessToken(t *testing.T) {
	var renewCalled, getCalled bool
	var passedRefresher credentials.TokenRefresher
	vault := &mockCredentialVault{
		renewFn: func(ctx context.Context, accountID int64, tokenType string, refresh credentials.TokenRefresher) (*models.OAuthToken, error) {
			renewCalled = true
			passedRefresher = refresh
			// Simulate the real vault outcome: refresh grant exchanged
			// for a brand-new access token.
			return &models.OAuthToken{AccessToken: "fresh-access-token", TokenType: tokenType}, nil
		},
		getFn: func(ctx context.Context, platformAccountID int64, tokenType string) (*models.OAuthToken, error) {
			getCalled = true
			return nil, errors.New("token expired") // the pre-fix failure mode
		},
	}

	var youTubeGotToken string
	youTubeSvc := &mockYouTubeOAuthServiceForEditor{
		validateChannelBindingFn: func(ctx context.Context, accessToken, expectedChannelID string) error {
			return nil // the grant is bound to UC123
		},
		getVideoFn: func(ctx context.Context, accessToken, videoID string) (*models.YouTubeVideoDetails, error) {
			youTubeGotToken = accessToken
			return &models.YouTubeVideoDetails{ID: videoID, ChannelID: "UC123", Privacy: "private", UploadStatus: "processed"}, nil
		},
	}

	r := mustNewRouterWithDefaults(
		services.NewCapabilityRouter(),
		&mockUserStore{
			findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
				return &models.PlatformAccount{ID: 42, Platform: models.PlatformYouTube, PlatformUserID: "UC123", Status: models.AccountStatusActive}, nil
			},
		},
		auth.NewManager(testJWTSecret, 24),
		"",
		nil,
		WithYouTubeService(youTubeSvc),
		WithCredentialVault(vault),
	)

	edit := &models.YouTubeVideoEdit{PlatformAccountID: 42, YouTubeVideoID: "ytvideo123"}
	if err := r.ensurePrivateYouTubeBatchVideo(context.Background(), edit); err != nil {
		t.Fatalf("ensurePrivateYouTubeBatchVideo: want nil error (auto-refresh), got %v", err)
	}
	if !renewCalled {
		t.Fatal("vault.Renew was NOT called — the expired access token was not refreshed (P0 regression)")
	}
	if passedRefresher == nil {
		t.Error("vault.Renew must receive the YouTube refresh adapter (r.youTubeSvc.RefreshOAuthToken)")
	}
	if getCalled {
		t.Error("vault.Get MUST NOT be the token source when Renew succeeds (the expired Get path must not run)")
	}
	if youTubeGotToken != "fresh-access-token" {
		t.Errorf("YouTube privacy check got access token %q, want %q (the RENEWED token must be used)", youTubeGotToken, "fresh-access-token")
	}
}

// TestEnsurePrivateYouTubeBatchVideo_RenewFailure_FallsBackToLegacyTokens
// pins the preserved historical path: when Renew fails (no refresh grant,
// legacy migration rows), the handler MUST fall back to the Get
// bearer → long_lived → short_lived chain so pre-043 tokens keep
// working instead of failing the batch.
func TestEnsurePrivateYouTubeBatchVideo_RenewFailure_FallsBackToLegacyTokens(t *testing.T) {
	var renewCalled, bearerGetCalled, longLivedGetCalled bool
	vault := &mockCredentialVault{
		renewFn: func(ctx context.Context, accountID int64, tokenType string, refresh credentials.TokenRefresher) (*models.OAuthToken, error) {
			renewCalled = true
			return nil, errors.New("no refresh grant stored")
		},
		getFn: func(ctx context.Context, platformAccountID int64, tokenType string) (*models.OAuthToken, error) {
			switch tokenType {
			case models.TokenTypeBearer:
				bearerGetCalled = true
				return nil, errors.New("token expired")
			case models.TokenTypeLongLived:
				longLivedGetCalled = true
				return &models.OAuthToken{AccessToken: "legacy-long-lived-token"}, nil
			default:
				return nil, errors.New("no token")
			}
		},
	}

	var youTubeGotToken string
	youTubeSvc := &mockYouTubeOAuthServiceForEditor{
		validateChannelBindingFn: func(ctx context.Context, accessToken, expectedChannelID string) error {
			return nil // the grant is bound to UC123
		},
		getVideoFn: func(ctx context.Context, accessToken, videoID string) (*models.YouTubeVideoDetails, error) {
			youTubeGotToken = accessToken
			return &models.YouTubeVideoDetails{ID: videoID, ChannelID: "UC123", Privacy: "private", UploadStatus: "processed"}, nil
		},
	}

	r := mustNewRouterWithDefaults(
		services.NewCapabilityRouter(),
		&mockUserStore{
			findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
				return &models.PlatformAccount{ID: 42, Platform: models.PlatformYouTube, PlatformUserID: "UC123", Status: models.AccountStatusActive}, nil
			},
		},
		auth.NewManager(testJWTSecret, 24),
		"",
		nil,
		WithYouTubeService(youTubeSvc),
		WithCredentialVault(vault),
	)

	edit := &models.YouTubeVideoEdit{PlatformAccountID: 42, YouTubeVideoID: "ytvideo123"}
	if err := r.ensurePrivateYouTubeBatchVideo(context.Background(), edit); err != nil {
		t.Fatalf("ensurePrivateYouTubeBatchVideo: want nil error via legacy fallback, got %v", err)
	}
	if !renewCalled {
		t.Fatal("vault.Renew must be attempted first even when it will fail")
	}
	if !bearerGetCalled || !longLivedGetCalled {
		t.Errorf("legacy fallback order violated: bearerGet=%v longLivedGet=%v (want both true)", bearerGetCalled, longLivedGetCalled)
	}
	if youTubeGotToken != "legacy-long-lived-token" {
		t.Errorf("YouTube got access token %q, want %q (the legacy long_lived token must be used)", youTubeGotToken, "legacy-long-lived-token")
	}
}

// TestEnsurePrivateYouTubeBatchVideo_OtherChannelVideo_BlockedBeforeUpload
// is the P0 cross-channel guard: with shared Google grants (084/085) a
// payload can name platform_account A but a youtube_video_id owned by a
// sibling channel B on the same grant. The batch MUST verify the video
// belongs to the exact selected channel (video.channel_id ==
// platform_account.platform_user_id) and block BEFORE any thumbnail
// work — even though the token has full access to both channels.
func TestEnsurePrivateYouTubeBatchVideo_OtherChannelVideo_BlockedBeforeUpload(t *testing.T) {
	var bindingCheckCalled bool
	vault := &mockCredentialVault{
		renewFn: func(ctx context.Context, accountID int64, tokenType string, refresh credentials.TokenRefresher) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "fresh-access-token", TokenType: tokenType}, nil
		},
	}
	// The grant really IS bound to the selected channel (channels.list
	// succeeds) — that is not enough. The video still belongs to a
	// DIFFERENT channel sharing the same grant.
	youTubeSvc := &mockYouTubeOAuthServiceForEditor{
		validateChannelBindingFn: func(ctx context.Context, accessToken, expectedChannelID string) error {
			bindingCheckCalled = true
			if expectedChannelID != "UC123" {
				t.Errorf("ValidateChannelBinding: want expectedChannelID UC123 (the SELECTED channel), got %q", expectedChannelID)
			}
			return nil
		},
		getVideoFn: func(ctx context.Context, accessToken, videoID string) (*models.YouTubeVideoDetails, error) {
			// The video belongs to WWE Insider France (sibling), not the
			// selected WWE Insider Italia channel.
			return &models.YouTubeVideoDetails{ID: videoID, ChannelID: "UCSIBLING99", Privacy: "private", UploadStatus: "processed"}, nil
		},
	}

	r := mustNewRouterWithDefaults(
		services.NewCapabilityRouter(),
		&mockUserStore{
			findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
				return &models.PlatformAccount{ID: 42, Platform: models.PlatformYouTube, PlatformUserID: "UC123", Status: models.AccountStatusActive}, nil
			},
		},
		auth.NewManager(testJWTSecret, 24),
		"",
		nil,
		WithYouTubeService(youTubeSvc),
		WithCredentialVault(vault),
	)

	edit := &models.YouTubeVideoEdit{PlatformAccountID: 42, YouTubeVideoID: "ytvideo-france-123"}
	err := r.ensurePrivateYouTubeBatchVideo(context.Background(), edit)
	if err == nil {
		t.Fatal("ensurePrivateYouTubeBatchVideo: want error when the video belongs to a sibling channel, got nil")
	}
	if !strings.Contains(err.Error(), "does not belong to the selected channel") {
		t.Errorf("want cross-channel ownership error, got: %v", err)
	}
	if !bindingCheckCalled {
		t.Error("ValidateChannelBinding must run BEFORE the ownership check (binding passes but ownership fails)")
	}
}

// TestEnsurePrivateYouTubeBatchVideo_ChannelBindingFailure_Blocked pins
// the binding leg of the same guard: when the grant is not bound to the
// expected channel at all (ValidateChannelBinding fails), the batch must
// stop immediately — the token grants access to sibling channels only.
func TestEnsurePrivateYouTubeBatchVideo_ChannelBindingFailure_Blocked(t *testing.T) {
	vault := &mockCredentialVault{
		renewFn: func(ctx context.Context, accountID int64, tokenType string, refresh credentials.TokenRefresher) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "fresh-access-token", TokenType: tokenType}, nil
		},
	}
	var videoFetched bool
	youTubeSvc := &mockYouTubeOAuthServiceForEditor{
		validateChannelBindingFn: func(ctx context.Context, accessToken, expectedChannelID string) error {
			return errors.New("channel binding mismatch: expected UC123, grant bound to UCSIBLING99")
		},
		getVideoFn: func(ctx context.Context, accessToken, videoID string) (*models.YouTubeVideoDetails, error) {
			videoFetched = true
			return &models.YouTubeVideoDetails{ID: videoID, ChannelID: "UC123", Privacy: "private", UploadStatus: "processed"}, nil
		},
	}

	r := mustNewRouterWithDefaults(
		services.NewCapabilityRouter(),
		&mockUserStore{
			findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
				return &models.PlatformAccount{ID: 42, Platform: models.PlatformYouTube, PlatformUserID: "UC123", Status: models.AccountStatusActive}, nil
			},
		},
		auth.NewManager(testJWTSecret, 24),
		"",
		nil,
		WithYouTubeService(youTubeSvc),
		WithCredentialVault(vault),
	)

	edit := &models.YouTubeVideoEdit{PlatformAccountID: 42, YouTubeVideoID: "ytvideo123"}
	err := r.ensurePrivateYouTubeBatchVideo(context.Background(), edit)
	if err == nil {
		t.Fatal("ensurePrivateYouTubeBatchVideo: want error on channel binding failure, got nil")
	}
	if !strings.Contains(err.Error(), "channel binding") {
		t.Errorf("want channel binding error, got: %v", err)
	}
	if videoFetched {
		t.Error("GetYouTubeVideo MUST NOT run after a binding failure — no video read on a misbound grant")
	}
}

// TestPublishYouTubeEditorSession_ExpiredToken_RenewsAutomatically pins
// the same contract on the publish leg of the thumbnail batch ("copertina
// applicata"): the stored bearer is expired, vault.Renew returns a fresh
// token, and the thumbnail publish must complete 200 with the fresh token
// reaching YouTube — the batch item finishes instead of failing.
func TestPublishYouTubeEditorSession_ExpiredToken_RenewsAutomatically(t *testing.T) {
	account := &models.PlatformAccount{
		ID:             42,
		UserID:         1,
		Platform:       models.PlatformYouTube,
		PlatformUserID: "UC123",
		Username:       "testchannel",
		Status:         models.AccountStatusActive,
	}
	workspace := &models.Workspace{ID: 7, OwnerID: 1, Name: "Test Workspace"}
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			if id == account.ID {
				return account, nil
			}
			return nil, nil
		},
	}
	workspaceStore := &mockWorkspaceStore{
		findByIDFn: func(id int64) (*models.Workspace, error) {
			if id == workspace.ID {
				return workspace, nil
			}
			return nil, nil
		},
	}

	mediaStore := newMockMediaStore()
	mediaStore.assets["asset-uuid-123"] = &models.MediaAsset{
		ID:          "asset-uuid-123",
		UserID:      1,
		UploadKey:   "uploads/1/thumb.jpg",
		ContentType: "image/jpeg",
		SizeBytes:   1024,
		Status:      models.MediaAssetStatusReady,
	}

	editStore := &mockYouTubeVideoEditStore{
		findFn: func(ctx context.Context, id string) (*models.YouTubeVideoEdit, error) {
			return &models.YouTubeVideoEdit{
				ID:                "session-123",
				WorkspaceID:       workspace.ID,
				PlatformAccountID: account.ID,
				YouTubeVideoID:    "ytvideo123",
				VeloxProjectID:    "ve-project-123",
				ThumbnailMediaID:  strPtr("asset-uuid-123"),
				DesiredPrivacy:    "private",
				Status:            "editing",
			}, nil
		},
	}

	thumbnailBytes := []byte("fake-thumbnail-bytes")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(thumbnailBytes)
	}))
	defer server.Close()

	storage := newMockStorageProvider()
	storage.assetURLFn = func(key string) string { return server.URL + "/" + key }

	var renewCalled bool
	var publishGotToken string
	youTubeSvc := &mockYouTubeOAuthServiceForEditor{
		publishThumbnailFn: func(ctx context.Context, accessToken, videoID string, data []byte, mimeType, privacyStatus string, publishAt *time.Time, opts models.YouTubePublishOptions) (string, error) {
			publishGotToken = accessToken
			if privacyStatus != "private" {
				t.Errorf("expected privacyStatus private, got %s", privacyStatus)
			}
			return "https://www.youtube.com/watch?v=" + videoID, nil
		},
	}

	r := mustNewRouterWithDefaults(
		services.NewCapabilityRouter(),
		store,
		auth.NewManager(testJWTSecret, 24),
		"https://app.instaedit.org",
		nil,
		WithWorkspaceStore(workspaceStore),
		WithYouTubeVideoEditStore(editStore),
		WithMediaStore(mediaStore),
		WithStorageProvider(storage),
		WithYouTubeService(youTubeSvc),
		WithCredentialVault(&mockCredentialVault{
			renewFn: func(ctx context.Context, accountID int64, tokenType string, refresh credentials.TokenRefresher) (*models.OAuthToken, error) {
				renewCalled = true
				return &models.OAuthToken{AccessToken: "fresh-access-token", TokenType: tokenType}, nil
			},
			getFn: func(ctx context.Context, id int64, tt string) (*models.OAuthToken, error) {
				return nil, errors.New("token expired") // the pre-fix failure mode
			},
		}),
	)

	payload := map[string]any{"privacy_status": "private"}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/session-123/publish", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("publish with expired token: want 200 after auto-refresh, got %d: %s", w.Code, w.Body.String())
	}
	if !renewCalled {
		t.Fatal("vault.Renew was NOT called on the publish path — the expired access token was not refreshed (P0 regression)")
	}
	if publishGotToken != "fresh-access-token" {
		t.Errorf("PublishThumbnail got access token %q, want %q (the RENEWED token must be used)", publishGotToken, "fresh-access-token")
	}
}
