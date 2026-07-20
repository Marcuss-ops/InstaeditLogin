package services

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/config"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

func youtubeTestCfg() *config.Config {
	return &config.Config{
		YouTubeClientID:     "test-youtube-client-id",
		YouTubeClientSecret: "test-youtube-client-secret-min32ch",
		YouTubeRedirectURI:  "http://localhost:8080/api/v1/auth/youtube/callback",
	}
}

func newTestYouTubeService(srv *httptest.Server) *YouTubeOAuthService {
	cfg := youtubeTestCfg()
	svc, _ := NewYouTubeOAuthService(cfg, ProviderDependencies{HTTPClient: testClient(srv)})
	return svc
}

// TestYouTubeLoginURL_IncludesRequiredScopes verifies that GetLoginURL
// requests the YouTube scopes required by the publish pipeline (upload,
// readonly) along with the operator-identity scopes (openid, email,
// profile). `yt-analytics.readonly` is intentionally absent from the
// requested scope set (least-privilege; docs/OAUTH-PRODUCTION.md Step 3
// "Code-side guard"): `videos.insert` accepts `youtube.upload` alone,
// and re-introducing the analytics scope would re-open Google's brand
// verification queue with zero functional gain. A negative assertion
// in the test body confirms the analytics scope is NOT requested.
func TestYouTubeLoginURL_IncludesRequiredScopes(t *testing.T) {
	srv := httptest.NewServer(http.NewServeMux())
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	authURL := svc.GetLoginURL("yt-state")

	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("GetLoginURL returned unparseable URL: %v\nurl: %s", err, authURL)
	}

	params := parsed.Query()
	scopes := params.Get("scope")

	for _, want := range []string{
		"https://www.googleapis.com/auth/youtube.upload",
		"https://www.googleapis.com/auth/youtube.readonly",
		"openid",
		"email",
		"profile",
	} {
		if !containsScope(scopes, want) {
			t.Errorf("scope missing %q, got: %s", want, scopes)
		}
	}

	// Negative assertion on the analytics scope: least-privilege
	// policy (docs/OAUTH-PRODUCTION.md Step 3 "Code-side guard").
	// Re-introducing the analytics scope would re-open Google's
	// brand verification queue without delivering any functional
	// gain to the publish pipeline (`videos.insert` accepts
	// `youtube.upload` alone).
	const forbiddenAnalyticsScope = "https://www.googleapis.com/auth/yt-analytics.readonly"
	if containsScope(scopes, forbiddenAnalyticsScope) {
		t.Errorf("scope list MUST NOT contain %q (least-privilege + brand-verification cost); got: %s",
			forbiddenAnalyticsScope, scopes)
	}

	if params.Get("access_type") != "offline" {
		t.Errorf("access_type: want offline, got %q", params.Get("access_type"))
	}
	if params.Get("include_granted_scopes") != "true" {
		t.Errorf("include_granted_scopes: want true, got %q", params.Get("include_granted_scopes"))
	}
}

// TestYouTubeLoginURL_AddModeForcesConsent verifies that the add mode
// forces consent and account selection prompts.
func TestYouTubeLoginURL_AddModeForcesConsent(t *testing.T) {
	srv := httptest.NewServer(http.NewServeMux())
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	authURL := svc.GetLoginURLWithOptions("state", OAuthLoginOptions{
		ForceConsent:  true,
		SelectAccount: true,
	})

	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("unparseable URL: %v", err)
	}

	prompt := parsed.Query().Get("prompt")
	if !containsPrompt(prompt, "consent") {
		t.Errorf("prompt missing consent, got: %s", prompt)
	}
	if !containsPrompt(prompt, "select_account") {
		t.Errorf("prompt missing select_account, got: %s", prompt)
	}
}

// TestYouTubeLoginURL_ReconnectModeForcesConsent verifies that the
// reconnect mode forces consent but does not select_account.
func TestYouTubeLoginURL_ReconnectModeForcesConsent(t *testing.T) {
	srv := httptest.NewServer(http.NewServeMux())
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	authURL := svc.GetLoginURLWithOptions("state", OAuthLoginOptions{
		ForceConsent: true,
	})

	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("unparseable URL: %v", err)
	}

	prompt := parsed.Query().Get("prompt")
	if !containsPrompt(prompt, "consent") {
		t.Errorf("prompt missing consent, got: %s", prompt)
	}
	if containsPrompt(prompt, "select_account") {
		t.Errorf("prompt should NOT contain select_account in reconnect mode, got: %s", prompt)
	}
}

// TestYouTubePreferredTokenTypes verifies that YouTube declares its
// canonical token types for account validation.
func TestYouTubePreferredTokenTypes(t *testing.T) {
	srv := httptest.NewServer(http.NewServeMux())
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	types := svc.PreferredTokenTypes()
	if len(types) == 0 {
		t.Fatal("expected at least one preferred token type")
	}
	if types[0] != models.TokenTypeBearer {
		t.Errorf("first token type: want %q, got %q", models.TokenTypeBearer, types[0])
	}
}

// TestYouTubeLoginURL_LoginHint verifies that login_hint is set when provided.
func TestYouTubeLoginURL_LoginHint(t *testing.T) {
	srv := httptest.NewServer(http.NewServeMux())
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	authURL := svc.GetLoginURLWithOptions("state", OAuthLoginOptions{
		LoginHint: "user@example.com",
	})

	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("unparseable URL: %v", err)
	}

	if got := parsed.Query().Get("login_hint"); got != "user@example.com" {
		t.Errorf("login_hint: want user@example.com, got %q", got)
	}
}

// TestYouTubeRefresh_PreservesOldRefreshToken verifies that when Google
// does not return a new refresh token, the old one is preserved.
func TestYouTubeRefresh_PreservesOldRefreshToken(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		// Google sometimes omits refresh_token on refresh.
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "new-access-token",
			"token_type":   "bearer",
			"expires_in":   3600,
			"scope":        "youtube.upload youtube.readonly",
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	result, err := svc.RefreshOAuthToken(context.Background(), "old-refresh-token")
	if err != nil {
		t.Fatalf("RefreshOAuthToken failed: %v", err)
	}

	if result.RefreshToken != "old-refresh-token" {
		t.Errorf("refresh token: want old-refresh-token preserved, got %q", result.RefreshToken)
	}
	if result.AccessToken != "new-access-token" {
		t.Errorf("access token: want new-access-token, got %q", result.AccessToken)
	}
}

// TestYouTubeDiscoverAccounts_OneChannel verifies that DiscoverAccounts
// returns a single channel with the real YouTube channel ID.
func TestYouTubeDiscoverAccounts_OneChannel(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/youtube/v3/channels", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("mine") != "true" {
			t.Errorf("channels.list: mine=true expected, got mine=%q", r.URL.Query().Get("mine"))
		}
		json.NewEncoder(w).Encode(youtubeChannelsResponse{
			Items: []youtubeChannel{
				{
					ID: "UCtest123channelID",
					Snippet: youtubeChannelSnippet{
						Title:       "Test Channel",
						Description: "A test channel",
						CustomURL:   "@testchannel",
						Country:     "US",
					},
					Statistics: youtubeStatistics{
						SubscriberCount: 125000,
						ViewCount:       18000000,
						VideoCount:      942,
					},
					ContentDetails: youtubeContentDetails{
						RelatedPlaylists: youtubeRelatedPlaylists{
							Uploads: "UUtest123channelID",
						},
					},
				},
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	accounts, err := svc.DiscoverAccounts(context.Background(), "fake-token", "")
	if err != nil {
		t.Fatalf("DiscoverAccounts failed: %v", err)
	}

	if len(accounts) != 1 {
		t.Fatalf("expected 1 account, got %d", len(accounts))
	}

	acc := accounts[0]
	if acc.Profile.PlatformUserID != "UCtest123channelID" {
		t.Errorf("PlatformUserID: want UCtest123channelID, got %q", acc.Profile.PlatformUserID)
	}
	if acc.Profile.Username != "Test Channel" {
		t.Errorf("Username: want Test Channel, got %q", acc.Profile.Username)
	}
	if acc.Metadata["handle"] != "@testchannel" {
		t.Errorf("metadata handle: want @testchannel, got %v", acc.Metadata["handle"])
	}
	if acc.Metadata["uploads_playlist_id"] != "UUtest123channelID" {
		t.Errorf("metadata uploads_playlist_id: want UUtest123channelID, got %v", acc.Metadata["uploads_playlist_id"])
	}
}

// TestYouTubeDiscoverAccounts_MultipleChannels verifies that DiscoverAccounts
// returns all channels when the authenticated user manages more than one.
func TestYouTubeDiscoverAccounts_MultipleChannels(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/youtube/v3/channels", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(youtubeChannelsResponse{
			Items: []youtubeChannel{
				{
					ID: "UCchannelOne",
					Snippet: youtubeChannelSnippet{
						Title:     "First Channel",
						CustomURL: "@firstchannel",
					},
					Statistics: youtubeStatistics{
						SubscriberCount: 1000,
						ViewCount:       50000,
						VideoCount:      25,
					},
					ContentDetails: youtubeContentDetails{
						RelatedPlaylists: youtubeRelatedPlaylists{Uploads: "UUchannelOne"},
					},
				},
				{
					ID: "UCchannelTwo",
					Snippet: youtubeChannelSnippet{
						Title:     "Second Channel",
						CustomURL: "@secondchannel",
					},
					Statistics: youtubeStatistics{
						SubscriberCount: 50000,
						ViewCount:       2000000,
						VideoCount:      150,
					},
					ContentDetails: youtubeContentDetails{
						RelatedPlaylists: youtubeRelatedPlaylists{Uploads: "UUchannelTwo"},
					},
				},
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	accounts, err := svc.DiscoverAccounts(context.Background(), "fake-token", "")
	if err != nil {
		t.Fatalf("DiscoverAccounts failed: %v", err)
	}

	if len(accounts) != 2 {
		t.Fatalf("expected 2 accounts, got %d", len(accounts))
	}

	if accounts[0].Profile.PlatformUserID != "UCchannelOne" {
		t.Errorf("first account ID: want UCchannelOne, got %q", accounts[0].Profile.PlatformUserID)
	}
	if accounts[0].Profile.Username != "First Channel" {
		t.Errorf("first account username: want First Channel, got %q", accounts[0].Profile.Username)
	}
	if accounts[0].Metadata["uploads_playlist_id"] != "UUchannelOne" {
		t.Errorf("first uploads playlist: want UUchannelOne, got %v", accounts[0].Metadata["uploads_playlist_id"])
	}

	if accounts[1].Profile.PlatformUserID != "UCchannelTwo" {
		t.Errorf("second account ID: want UCchannelTwo, got %q", accounts[1].Profile.PlatformUserID)
	}
	if accounts[1].Profile.Username != "Second Channel" {
		t.Errorf("second account username: want Second Channel, got %q", accounts[1].Profile.Username)
	}
	if accounts[1].Metadata["uploads_playlist_id"] != "UUchannelTwo" {
		t.Errorf("second uploads playlist: want UUchannelTwo, got %v", accounts[1].Metadata["uploads_playlist_id"])
	}
}

// TestYouTubeDiscoverAccounts_NoChannel verifies that DiscoverAccounts
// returns an error when the Google account has no YouTube channel.
func TestYouTubeDiscoverAccounts_NoChannel(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/youtube/v3/channels", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(youtubeChannelsResponse{Items: []youtubeChannel{}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	_, err := svc.DiscoverAccounts(context.Background(), "fake-token", "")
	if err == nil {
		t.Fatal("expected error for no channel, got nil")
	}
}

// TestYouTubeDiscoverAccounts_HiddenSubscribers verifies that hidden
// subscriber count is stored in metadata.
func TestYouTubeDiscoverAccounts_HiddenSubscribers(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/youtube/v3/channels", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(youtubeChannelsResponse{
			Items: []youtubeChannel{
				{
					ID: "UChidden",
					Snippet: youtubeChannelSnippet{
						Title: "Hidden Subs Channel",
					},
					Statistics: youtubeStatistics{
						HiddenSubscriberCount: true,
						SubscriberCount:       0,
						ViewCount:             5000,
						VideoCount:            10,
					},
					ContentDetails: youtubeContentDetails{
						RelatedPlaylists: youtubeRelatedPlaylists{Uploads: "UUhidden"},
					},
				},
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	accounts, err := svc.DiscoverAccounts(context.Background(), "fake-token", "")
	if err != nil {
		t.Fatalf("DiscoverAccounts failed: %v", err)
	}
	if len(accounts) != 1 {
		t.Fatalf("expected 1 account, got %d", len(accounts))
	}
	if accounts[0].Metadata["hidden_subscriber_count"] != true {
		t.Errorf("hidden_subscriber_count: want true, got %v", accounts[0].Metadata["hidden_subscriber_count"])
	}
}

// TestYouTubeGetAccountDetails verifies that GetAccountDetails returns
// structured account details from channels.list.
func TestYouTubeGetAccountDetails(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/youtube/v3/channels", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(youtubeChannelsResponse{
			Items: []youtubeChannel{
				{
					ID: "UCabc123",
					Snippet: youtubeChannelSnippet{
						Title:       "My Channel",
						Description: "Channel description",
						CustomURL:   "@mychannel",
						Country:     "IT",
					},
					Statistics: youtubeStatistics{
						SubscriberCount: 125000,
						ViewCount:       18000000,
						VideoCount:      942,
					},
					ContentDetails: youtubeContentDetails{
						RelatedPlaylists: youtubeRelatedPlaylists{Uploads: "UUabc123"},
					},
					BrandingSettings: youtubeBranding{
						Image: &youtubeBrandingImage{
							BannerImageUrl: "https://yt3.ggpht.com/banner.jpg",
						},
					},
				},
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	details, err := svc.GetAccountDetails(context.Background(), "token", "UCabc123")
	if err != nil {
		t.Fatalf("GetAccountDetails failed: %v", err)
	}

	if details.ResourceType != "channel" {
		t.Errorf("ResourceType: want channel, got %q", details.ResourceType)
	}
	if details.ExternalID != "UCabc123" {
		t.Errorf("ExternalID: want UCabc123, got %q", details.ExternalID)
	}
	if details.DisplayName != "My Channel" {
		t.Errorf("DisplayName: want My Channel, got %q", details.DisplayName)
	}
	if details.Handle != "@mychannel" {
		t.Errorf("Handle: want @mychannel, got %q", details.Handle)
	}
	if details.BannerURL != "https://yt3.ggpht.com/banner.jpg" {
		t.Errorf("BannerURL: want banner URL, got %q", details.BannerURL)
	}
	if len(details.Metrics) != 3 {
		t.Fatalf("Metrics: want 3, got %d", len(details.Metrics))
	}

	// Check subscribers metric.
	for _, m := range details.Metrics {
		if m.Key == "subscribers" && m.Value != 125000 {
			t.Errorf("subscribers: want 125000, got %d", m.Value)
		}
		if m.Key == "views" && m.Value != 18000000 {
			t.Errorf("views: want 18000000, got %d", m.Value)
		}
		if m.Key == "videos" && m.Value != 942 {
			t.Errorf("videos: want 942, got %d", m.Value)
		}
	}
}

// TestYouTubeGetAccountDetails_NotFound verifies error when channel not found.
func TestYouTubeGetAccountDetails_NotFound(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/youtube/v3/channels", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(youtubeChannelsResponse{Items: []youtubeChannel{}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	_, err := svc.GetAccountDetails(context.Background(), "token", "UCnotfound")
	if err == nil {
		t.Fatal("expected error for not found, got nil")
	}
}

// TestYouTubeAsyncPublish_EncodeDecodePublishID verifies the composite
// publishID encoding and decoding used to carry the channel ID through
// the async publishing lifecycle.
func TestYouTubeAsyncPublish_EncodeDecodePublishID(t *testing.T) {
	publishID := encodeYouTubePublishID("UCexpected", "yt-async-video-id")
	if publishID != "UCexpected:yt-async-video-id" {
		t.Errorf("publishID: want UCexpected:yt-async-video-id, got %q", publishID)
	}

	channelID, videoID, err := decodeYouTubePublishID(publishID)
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if channelID != "UCexpected" {
		t.Errorf("channelID: want UCexpected, got %q", channelID)
	}
	if videoID != "yt-async-video-id" {
		t.Errorf("videoID: want yt-async-video-id, got %q", videoID)
	}

	// Invalid inputs.
	for _, invalid := range []string{"", "nocolon", ":", "channel:", ":video"} {
		if _, _, err := decodeYouTubePublishID(invalid); err == nil {
			t.Errorf("expected error for %q", invalid)
		}
	}
}

// TestYouTubeAsyncPublish_Reconcile_Processing_ReturnsNil verifies that
// Reconcile returns (nil, nil) while the video is still processing.
func TestYouTubeAsyncPublish_Reconcile_Processing_ReturnsNil(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/youtube/v3/videos", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(youtubeVideosResponse{
			Items: []youtubeVideo{
				{
					ID:                "yt-async-video-id",
					Snippet:           youtubeVideoSnippet{ChannelID: "UCexpected"},
					ProcessingDetails: &youtubeVideoProcessingDetails{ProcessingStatus: "processing"},
				},
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	publishID := encodeYouTubePublishID("UCexpected", "yt-async-video-id")
	res, err := svc.Reconcile(context.Background(), "token", publishID)
	if err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}
	if res != nil {
		t.Fatalf("expected in-flight (nil result), got %+v", res)
	}
}

// TestYouTubeAsyncPublish_Reconcile_Succeeded_ReturnsResult verifies that
// Reconcile returns the public video URL once processing succeeds.
func TestYouTubeAsyncPublish_Reconcile_Succeeded_ReturnsResult(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/youtube/v3/videos", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(youtubeVideosResponse{
			Items: []youtubeVideo{
				{
					ID:                "yt-async-video-id",
					Snippet:           youtubeVideoSnippet{ChannelID: "UCexpected"},
					ProcessingDetails: &youtubeVideoProcessingDetails{ProcessingStatus: "succeeded"},
				},
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	publishID := encodeYouTubePublishID("UCexpected", "yt-async-video-id")
	res, err := svc.Reconcile(context.Background(), "token", publishID)
	if err != nil {
		t.Fatalf("Reconcile returned error: %v", err)
	}
	if res == nil {
		t.Fatal("expected success result, got nil")
	}
	if res.PlatformMediaID != "yt-async-video-id" {
		t.Errorf("PlatformMediaID: want yt-async-video-id, got %q", res.PlatformMediaID)
	}
	wantURL := "https://www.youtube.com/watch?v=yt-async-video-id"
	if res.PlatformURL != wantURL {
		t.Errorf("PlatformURL: want %q, got %q", wantURL, res.PlatformURL)
	}
}

// TestYouTubeAsyncPublish_Reconcile_Failed_ReturnsError verifies that a
// failed processing status is treated as a terminal failure.
func TestYouTubeAsyncPublish_Reconcile_Failed_ReturnsError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/youtube/v3/videos", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(youtubeVideosResponse{
			Items: []youtubeVideo{
				{
					ID:                "yt-async-video-id",
					Snippet:           youtubeVideoSnippet{ChannelID: "UCexpected"},
					ProcessingDetails: &youtubeVideoProcessingDetails{ProcessingStatus: "failed"},
				},
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	publishID := encodeYouTubePublishID("UCexpected", "yt-async-video-id")
	res, err := svc.Reconcile(context.Background(), "token", publishID)
	if err == nil {
		t.Fatal("expected terminal error for failed status, got nil")
	}
	if res != nil {
		t.Fatalf("expected nil result on failure, got %+v", res)
	}
}

// TestYouTubeAsyncPublish_Reconcile_ChannelMismatch_ReturnsError verifies
// that Reconcile fails when the video landed on a different channel.
func TestYouTubeAsyncPublish_Reconcile_ChannelMismatch_ReturnsError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/youtube/v3/videos", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(youtubeVideosResponse{
			Items: []youtubeVideo{
				{
					ID:                "yt-async-video-id",
					Snippet:           youtubeVideoSnippet{ChannelID: "UCother"},
					ProcessingDetails: &youtubeVideoProcessingDetails{ProcessingStatus: "succeeded"},
				},
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	publishID := encodeYouTubePublishID("UCexpected", "yt-async-video-id")
	res, err := svc.Reconcile(context.Background(), "token", publishID)
	if err == nil {
		t.Fatal("expected channel mismatch error, got nil")
	}
	if res != nil {
		t.Fatalf("expected nil result on mismatch, got %+v", res)
	}
}

// TestYouTubeAsyncPublish_Reconcile_Terminated_ReturnsError verifies that a
// terminated processing status is treated as a terminal failure.
func TestYouTubeAsyncPublish_Reconcile_Terminated_ReturnsError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/youtube/v3/videos", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(youtubeVideosResponse{
			Items: []youtubeVideo{
				{
					ID:                "yt-async-video-id",
					Snippet:           youtubeVideoSnippet{ChannelID: "UCexpected"},
					ProcessingDetails: &youtubeVideoProcessingDetails{ProcessingStatus: "terminated"},
				},
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	publishID := encodeYouTubePublishID("UCexpected", "yt-async-video-id")
	res, err := svc.Reconcile(context.Background(), "token", publishID)
	if err == nil {
		t.Fatal("expected terminal error for terminated status, got nil")
	}
	if res != nil {
		t.Fatalf("expected nil result on termination, got %+v", res)
	}
}

// TestYouTubeAsyncPublish_ContinuePublish_NoOp verifies that ContinuePublish
// is a no-op for YouTube.
func TestYouTubeAsyncPublish_ContinuePublish_NoOp(t *testing.T) {
	srv := httptest.NewServer(http.NewServeMux())
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	if err := svc.ContinuePublish(context.Background(), "token", "UCexpected:yt-async-video-id"); err != nil {
		t.Fatalf("ContinuePublish should be a no-op, got error: %v", err)
	}
}

// TestYouTubeAsyncPublish_Reconcile_MissingChannelID_ReturnsError verifies
// that Reconcile fails when the API response omits the channel ID.
func TestYouTubeAsyncPublish_Reconcile_MissingChannelID_ReturnsError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/youtube/v3/videos", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(youtubeVideosResponse{
			Items: []youtubeVideo{
				{
					ID:                "yt-async-video-id",
					Snippet:           youtubeVideoSnippet{ChannelID: ""},
					ProcessingDetails: &youtubeVideoProcessingDetails{ProcessingStatus: "succeeded"},
				},
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	publishID := encodeYouTubePublishID("UCexpected", "yt-async-video-id")
	res, err := svc.Reconcile(context.Background(), "token", publishID)
	if err == nil {
		t.Fatal("expected channel mismatch error for missing channelId, got nil")
	}
	if res != nil {
		t.Fatalf("expected nil result on missing channelId, got %+v", res)
	}
}

// TestYouTubeAsyncPublish_CheckPublishStatus_ReturnsStatus verifies that
// CheckPublishStatus returns the processing status string.
func TestYouTubeAsyncPublish_CheckPublishStatus_ReturnsStatus(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/youtube/v3/videos", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(youtubeVideosResponse{
			Items: []youtubeVideo{
				{
					ID:                "yt-async-video-id",
					ProcessingDetails: &youtubeVideoProcessingDetails{ProcessingStatus: "succeeded"},
				},
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	publishID := encodeYouTubePublishID("UCexpected", "yt-async-video-id")
	state, err := svc.CheckPublishStatus(context.Background(), "token", publishID)
	if err != nil {
		t.Fatalf("CheckPublishStatus returned error: %v", err)
	}
	if state != "succeeded" {
		t.Errorf("state: want succeeded, got %q", state)
	}
}

// TestYouTubeBuildUploadMetadata_ScheduledPublish sets privacy to private
// and includes publishAt when PublishAt is in the future.
func TestYouTubeBuildUploadMetadata_ScheduledPublish(t *testing.T) {
	srv := httptest.NewServer(http.NewServeMux())
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	future := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Second)
	payload := models.PublishPayload{
		Title:        "Scheduled Video",
		Text:         "A scheduled video",
		PrivacyLevel: "public",
		PublishAt:    &future,
	}

	meta := svc.buildUploadMetadata(payload)

	status, ok := meta["status"].(map[string]string)
	if !ok {
		t.Fatalf("status metadata type: want map[string]string, got %T", meta["status"])
	}
	if status["privacyStatus"] != "private" {
		t.Errorf("privacyStatus: want private, got %q", status["privacyStatus"])
	}
	if status["publishAt"] != future.UTC().Format(time.RFC3339) {
		t.Errorf("publishAt: want %q, got %q", future.UTC().Format(time.RFC3339), status["publishAt"])
	}
}

// TestYouTubeBuildUploadMetadata_ImmediatePublish uses the requested
// privacy level when no future PublishAt is set.
func TestYouTubeBuildUploadMetadata_ImmediatePublish(t *testing.T) {
	srv := httptest.NewServer(http.NewServeMux())
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	payload := models.PublishPayload{
		Title:        "Immediate Video",
		Text:         "An immediate video",
		PrivacyLevel: "unlisted",
	}

	meta := svc.buildUploadMetadata(payload)

	status, ok := meta["status"].(map[string]string)
	if !ok {
		t.Fatalf("status metadata type: want map[string]string, got %T", meta["status"])
	}
	if status["privacyStatus"] != "unlisted" {
		t.Errorf("privacyStatus: want unlisted, got %q", status["privacyStatus"])
	}
	if _, exists := status["publishAt"]; exists {
		t.Errorf("publishAt should not be set for immediate publishes")
	}
}

// TestYouTubeBuildUploadMetadata_PastPublishAt_Ignored verifies that a
// PublishAt in the past is ignored and the requested privacy is used.
func TestYouTubeBuildUploadMetadata_PastPublishAt_Ignored(t *testing.T) {
	srv := httptest.NewServer(http.NewServeMux())
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	past := time.Now().UTC().Add(-2 * time.Hour)
	payload := models.PublishPayload{
		Title:        "Past Video",
		Text:         "A past video",
		PrivacyLevel: "public",
		PublishAt:    &past,
	}

	meta := svc.buildUploadMetadata(payload)

	status, ok := meta["status"].(map[string]string)
	if !ok {
		t.Fatalf("status metadata type: want map[string]string, got %T", meta["status"])
	}
	if status["privacyStatus"] != "public" {
		t.Errorf("privacyStatus: want public, got %q", status["privacyStatus"])
	}
	if _, exists := status["publishAt"]; exists {
		t.Errorf("publishAt should not be set for past timestamps")
	}
}

// TestYouTubeValidateChannelBinding_Match verifies that a single-
// channel grant returns nil when the channel id matches the expected
// id. exercises the happy path of services.YouTubeChannelBinder.
func TestYouTubeValidateChannelBinding_Match(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/youtube/v3/channels", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(youtubeChannelsResponse{
			Items: []youtubeChannel{
				{ID: "UCexpectedChanID", Snippet: youtubeChannelSnippet{Title: "My Channel"}},
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	if err := svc.ValidateChannelBinding(context.Background(), "fresh-access-token", "UCexpectedChanID"); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

// TestYouTubeValidateChannelBinding_MultiChannelIncludesExpected
// verifies the multi-channel case (a single grant can manage up to
// 100 channels). The expected id is present in the set, so the check
// succeeds even though N=3 channels are returned.
func TestYouTubeValidateChannelBinding_MultiChannelIncludesExpected(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/youtube/v3/channels", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(youtubeChannelsResponse{
			Items: []youtubeChannel{
				{ID: "UCmanagedByA", Snippet: youtubeChannelSnippet{Title: "First"}},
				{ID: "UCexpectedChanID", Snippet: youtubeChannelSnippet{Title: "Second"}},
				{ID: "UCmanagedByB", Snippet: youtubeChannelSnippet{Title: "Third"}},
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	if err := svc.ValidateChannelBinding(context.Background(), "fresh-access-token", "UCexpectedChanID"); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

// TestYouTubeValidateChannelBinding_Mismatch verifies that a grant
// reporting only DIFFERENT channel ids returns ErrYouTubeChannelMismatch
// (detectable via errors.Is). The expected id is NOT in the returned set.
func TestYouTubeValidateChannelBinding_Mismatch(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/youtube/v3/channels", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(youtubeChannelsResponse{
			Items: []youtubeChannel{
				{ID: "UCdifferentChanID", Snippet: youtubeChannelSnippet{Title: "Some Other Channel"}},
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	err := svc.ValidateChannelBinding(context.Background(), "fresh-access-token", "UCexpectedChanID")
	if err == nil {
		t.Fatalf("expected ErrYouTubeChannelMismatch, got nil")
	}
	if !errors.Is(err, ErrYouTubeChannelMismatch) {
		t.Errorf("expected errors.Is to match ErrYouTubeChannelMismatch, got error: %v", err)
	}
	// Sanity: the diagnostic message includes BOTH the expected id and
	// the actual channel set so the operator sees what drifted.
	if !strings.Contains(err.Error(), "UCexpectedChanID") {
		t.Errorf("expected error message to include expected channel id, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "UCdifferentChanID") {
		t.Errorf("expected error message to include actual channel id, got %q", err.Error())
	}
}

// TestYouTubeValidateChannelBinding_ZeroChannels verifies that an
// empty channels.list response is treated as a structural mismatch
// (the grant has lost all bindings). Returns ErrYouTubeChannelMismatch.
func TestYouTubeValidateChannelBinding_ZeroChannels(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/youtube/v3/channels", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(youtubeChannelsResponse{Items: []youtubeChannel{}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	err := svc.ValidateChannelBinding(context.Background(), "fresh-access-token", "UCexpectedChanID")
	if !errors.Is(err, ErrYouTubeChannelMismatch) {
		t.Fatalf("expected ErrYouTubeChannelMismatch, got %v", err)
	}
	if !strings.Contains(err.Error(), "0 channels") {
		t.Errorf("expected message to mention 0 channels, got %q", err.Error())
	}
}

// TestYouTubeValidateChannelBinding_Transient5xx verifies that a
// non-200 response from channels.list returns a plain error WITHOUT
// wrapping ErrYouTubeChannelMismatch. The worker must treat this as
// transient and NOT flag the platform_account reauth_required.
func TestYouTubeValidateChannelBinding_Transient5xx(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/youtube/v3/channels", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "google internal error", http.StatusServiceUnavailable)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	err := svc.ValidateChannelBinding(context.Background(), "fresh-access-token", "UCexpectedChanID")
	if err == nil {
		t.Fatalf("expected error on 5xx, got nil")
	}
	if errors.Is(err, ErrYouTubeChannelMismatch) {
		t.Errorf("5xx must NOT wrap ErrYouTubeChannelMismatch (worker would flag reauth on transient); got %v", err)
	}
	if !strings.Contains(err.Error(), "503") {
		t.Errorf("expected message to include status code 503, got %q", err.Error())
	}
}

// TestYouTubeValidateChannelBinding_PaginationAcrossThreePages
// verifies the pagination behaviour introduced by the rewrite of
// ValidateChannelBinding: a grant whose expected channel id lives
// ONLY on the third page of channels.list?mine=true (i.e. position
// >50 in the manager's channel set) is correctly recognized. The
// stateful httptest handler returns three pages:
//   page 1 (no pageToken)        : 50 channels (no expected)
//   page 2 (pageToken=page2t)    : 50 channels (no expected)
//   page 3 (pageToken=page3t)    : 10 channels, expected at index 5
// Without pagination the old single-GET path would have invisibly
// truncated at page 1 → ErrYouTubeChannelMismatch. With pagination,
// the loop follows nextPageToken through all three, finds the
// expected, and returns nil.
//
// Side-checks:
//   - handler.calls == 3 confirms the loop actually paged (and not
//     a single-shot GET that happened to find the expected by luck).
//   - The handler's last query string must carry pageToken=page3t —
//     guards against an off-by-one where pagination stops at page 2.
func TestYouTubeValidateChannelBinding_PaginationAcrossThreePages(t *testing.T) {
	const expected = "UCexpectedChanIDp3" // unique to page 3, index 5

	var handlerCalls int
	var lastPageToken string
	mux := http.NewServeMux()
	mux.HandleFunc("/youtube/v3/channels", func(w http.ResponseWriter, r *http.Request) {
		handlerCalls++
		lastPageToken = r.URL.Query().Get("pageToken")

		var (
			items         []youtubeChannel
			nextPageToken string
		)
		switch lastPageToken {
		case "":
			items = make([]youtubeChannel, 50)
			for i := range items {
				items[i] = youtubeChannel{ID: fmt.Sprintf("UCp1-%03d", i)}
			}
			nextPageToken = "page2t"
		case "page2t":
			items = make([]youtubeChannel, 50)
			for i := range items {
				items[i] = youtubeChannel{ID: fmt.Sprintf("UCp2-%03d", i)}
			}
			nextPageToken = "page3t"
		default: // page3t — final page
			items = make([]youtubeChannel, 10)
			for i := range items {
				if i == 5 {
					items[i] = youtubeChannel{ID: expected}
				} else {
					items[i] = youtubeChannel{ID: fmt.Sprintf("UCp3-%03d", i)}
				}
			}
			// nextPageToken deliberately empty — final page
		}

		_ = json.NewEncoder(w).Encode(youtubeChannelsResponse{
			Items:         items,
			NextPageToken: nextPageToken,
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	if err := svc.ValidateChannelBinding(context.Background(), "fresh-access-token", expected); err != nil {
		t.Fatalf("expected nil (expected channel is on page 3), got: %v", err)
	}
	if handlerCalls != 3 {
		t.Errorf("expected 3 server hits (page1 + page2 + page3), got %d", handlerCalls)
	}
	if lastPageToken != "page3t" {
		t.Errorf("expected last request to carry pageToken=page3t, got %q", lastPageToken)
	}
}

// TestYouTubeValidateChannelBinding_FailureMidPagination verifies the
// transient contract is preserved across pagination boundaries: page 1
// succeeds with a non-matching set + nextPageToken, page 2 returns a
// transient 5xx. The function MUST return a plain wrapped error NOT
// wrapping ErrYouTubeChannelMismatch so the worker treats it as
// transient and does NOT flip the platform_account to reauth_required.
// The page-1 5xx case is already covered by
// TestYouTubeValidateChannelBinding_Transient5xx; this test covers the
// post-pagination failure path that did not exist before the
// pagination rewrite — a regression there would silently expand the
// failure surface into reauth territory.
//
// Also asserts handler.calls == 2 so a future off-by-one that issues
// a page-3 GET after page-2's 503 would be caught.
func TestYouTubeValidateChannelBinding_FailureMidPagination(t *testing.T) {
	var handlerCalls int
	mux := http.NewServeMux()
	mux.HandleFunc("/youtube/v3/channels", func(w http.ResponseWriter, r *http.Request) {
		handlerCalls++
		switch r.URL.Query().Get("pageToken") {
		case "": // page 1 — succeeds with 50 non-matching channels
			items := make([]youtubeChannel, 50)
			for i := range items {
				items[i] = youtubeChannel{ID: fmt.Sprintf("UCnomatch-%03d", i)}
			}
			_ = json.NewEncoder(w).Encode(youtubeChannelsResponse{
				Items:         items,
				NextPageToken: "page2t",
			})
		default: // page 2 — transient 5xx
			http.Error(w, "google internal error", http.StatusServiceUnavailable)
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	err := svc.ValidateChannelBinding(context.Background(), "fresh-access-token", "UCexpectedChanID")
	if err == nil {
		t.Fatalf("expected error on mid-loop 5xx, got nil")
	}
	if errors.Is(err, ErrYouTubeChannelMismatch) {
		t.Errorf("mid-loop 5xx MUST NOT wrap ErrYouTubeChannelMismatch (worker would flag reauth on transient); got %v", err)
	}
	if !strings.Contains(err.Error(), "503") {
		t.Errorf("expected error message to include status code 503, got %q", err.Error())
	}
	if handlerCalls != 2 {
		t.Errorf("expected exactly 2 server hits (page1 + aborted page2), got %d", handlerCalls)
	}
}

// TestYouTubeValidateChannelBinding_EmptyExpectedChannelID verifies
// the empty-input guard: an empty expectedChannelID returns an error
// without any HTTP round-trip.
func TestYouTubeValidateChannelBinding_EmptyExpectedChannelID(t *testing.T) {
	srv := httptest.NewServer(http.NewServeMux())
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	if err := svc.ValidateChannelBinding(context.Background(), "token", ""); err == nil {
		t.Fatal("expected error on empty expectedChannelID, got nil")
	}
}

// TestFormatCount verifies the human-readable count formatting.
func TestFormatCount(t *testing.T) {
	tests := []struct {
		input int64
		want  string
	}{
		{0, "0"},
		{42, "42"},
		{999, "999"},
		{1000, "1.0K"},
		{1500, "1.5K"},
		{999999, "1000.0K"},
		{1000000, "1.0M"},
		{18000000, "18.0M"},
		{1000000000, "1.0B"},
	}
	for _, tc := range tests {
		got := formatCount(tc.input)
		if got != tc.want {
			t.Errorf("formatCount(%d): want %q, got %q", tc.input, tc.want, got)
		}
	}
}

// --- helpers ---

func containsScope(scopes, target string) bool {
	for _, s := range splitScopes(scopes) {
		if s == target {
			return true
		}
	}
	return false
}

func splitScopes(scopes string) []string {
	var result []string
	for _, s := range splitBySpace(scopes) {
		if s != "" {
			result = append(result, s)
		}
	}
	return result
}

func splitBySpace(s string) []string {
	var result []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ' ' {
			if i > start {
				result = append(result, s[start:i])
			}
			start = i + 1
		}
	}
	return result
}

func containsPrompt(prompt, target string) bool {
	for _, p := range splitBySpace(prompt) {
		if p == target {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// BindGrantToChannel — provider-level tests for the P0 1-grant-1-channel rule
// (Taglio P0#1 — see github issue / repo discussion for context).
//
// What this proves at the provider layer: a YouTubeOAuthService wired
// with a fake channels.list endpoint enforces the same rule the HTTP
// handler already enforces end-to-end (see pkg/api/routes_test.go's
// TestHandleCallback_YouTube_*), so any future consumer of
// BindGrantToChannel (e.g. a per-channel reconnect endpoint, an
// admin re-bind tool) inherits the same safety guarantees without
// re-implementing the filter.
//
// The integration tests in pkg/api/routes_test.go already count the
// vault.Save calls; these unit tests count the *DiscoveredAccount
// returned and the sentinel surfaced. Together they prove the rule
// end-to-end at both layers.
// ---------------------------------------------------------------------------

// TestYouTubeBindGrantToChannel_OneChannel_NoExpected returns the
// single channel without any sentinel (canonical happy path for the
// most common operator: one Google account, one channel).
func TestYouTubeBindGrantToChannel_OneChannel_NoExpected(t *testing.T) {
	const channelID = "UCaaaaaaaaaaaaaaaaaaaaa1"
	mux := http.NewServeMux()
	mux.HandleFunc("/youtube/v3/channels", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("mine") != "true" {
			t.Errorf("DiscoverAccounts must call channels.list with mine=true, got query=%q", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"items": []map[string]interface{}{
				{"id": channelID, "snippet": map[string]string{"title": "Solo Channel"}},
			},
			"pageInfo": map[string]int{"totalResults": 1, "resultsPerPage": 1},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	acc, err := svc.BindGrantToChannel(context.Background(), "fake-bearer", "")
	if err != nil {
		t.Fatalf("BindGrantToChannel: %v", err)
	}
	if acc == nil {
		t.Fatal("expected a single *DiscoveredAccount, got nil")
	}
	if acc.Profile.PlatformUserID != channelID {
		t.Errorf("channelID: want %q, got %q", channelID, acc.Profile.PlatformUserID)
	}
}

// TestYouTubeBindGrantToChannel_MultipleChannels_NoExpected_Ambiguous
// returns ErrYouTubeAmbiguousAuthorization — the bearer token must
// NEVER be cloned across N>1 channels. The error message must
// surface the observed channel count so the operator's runbook can
// say "your Google account owns X channels, please re-authorize with
// expected_channel_id".
func TestYouTubeBindGrantToChannel_MultipleChannels_NoExpected_Ambiguous(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/youtube/v3/channels", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"items": []map[string]interface{}{
				{"id": "UCaaaaaaaaaaaaaaaaaaaaa1", "snippet": map[string]string{"title": "Channel A"}},
				{"id": "UCaaaaaaaaaaaaaaaaaaaaa2", "snippet": map[string]string{"title": "Channel B"}},
			},
			"pageInfo": map[string]int{"totalResults": 2, "resultsPerPage": 2},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	acc, err := svc.BindGrantToChannel(context.Background(), "fake-bearer", "")
	if err == nil {
		t.Fatalf("expected ErrYouTubeAmbiguousAuthorization, got nil and account=%+v", acc)
	}
	if acc != nil {
		t.Errorf("ambiguous: account must be nil, got %+v", acc)
	}
	if !errors.Is(err, ErrYouTubeAmbiguousAuthorization) {
		t.Errorf("errors.Is(err, ErrYouTubeAmbiguousAuthorization) = false; err=%v", err)
	}
	if !strings.Contains(err.Error(), "got 2 channels") {
		t.Errorf("error should report the observed channel count, got %q", err.Error())
	}
}

// TestYouTubeBindGrantToChannel_MultipleChannels_ExpectedMatches
// returns the matching channel from a multi-channel grant (canonical
// expected_channel_id happy path).
func TestYouTubeBindGrantToChannel_MultipleChannels_ExpectedMatches(t *testing.T) {
	const expectedID = "UCaaaaaaaaaaaaaaaaaaaaa2"
	mux := http.NewServeMux()
	mux.HandleFunc("/youtube/v3/channels", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"items": []map[string]interface{}{
				{"id": "UCaaaaaaaaaaaaaaaaaaaaa1", "snippet": map[string]string{"title": "Channel A"}},
				{"id": expectedID, "snippet": map[string]string{"title": "Channel B"}},
				{"id": "UCaaaaaaaaaaaaaaaaaaaaa3", "snippet": map[string]string{"title": "Channel C"}},
			},
			"pageInfo": map[string]int{"totalResults": 3, "resultsPerPage": 3},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	acc, err := svc.BindGrantToChannel(context.Background(), "fake-bearer", expectedID)
	if err != nil {
		t.Fatalf("BindGrantToChannel: %v", err)
	}
	if acc == nil {
		t.Fatal("expected the matching channel, got nil")
	}
	if acc.Profile.PlatformUserID != expectedID {
		t.Errorf("channelID: want %q, got %q", expectedID, acc.Profile.PlatformUserID)
	}
}

// TestYouTubeBindGrantToChannel_OneChannel_ExpectedNoMatch_Mismatch
// returns a wrapped ErrYouTubeChannelMismatch — the operator
// authenticated the wrong Google account (or imported a Brand
// Account id that no longer exists).
func TestYouTubeBindGrantToChannel_OneChannel_ExpectedNoMatch_Mismatch(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/youtube/v3/channels", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"items": []map[string]interface{}{
				{"id": "UCaaaaaaaaaaaaaaaaaaaaa1", "snippet": map[string]string{"title": "Channel A"}},
			},
			"pageInfo": map[string]int{"totalResults": 1, "resultsPerPage": 1},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	acc, err := svc.BindGrantToChannel(context.Background(), "fake-bearer", "UCaaaaaaaaaaaaaaaaaaaaaZ")
	if err == nil {
		t.Fatalf("expected ErrYouTubeChannelMismatch, got nil and account=%+v", acc)
	}
	if acc != nil {
		t.Errorf("mismatch: account must be nil, got %+v", acc)
	}
	if !errors.Is(err, ErrYouTubeChannelMismatch) {
		t.Errorf("errors.Is(err, ErrYouTubeChannelMismatch) = false; err=%v", err)
	}
	if !strings.Contains(err.Error(), "UCaaaaaaaaaaaaaaaaaaaaaZ") {
		t.Errorf("error should quote the expected channel id, got %q", err.Error())
	}
}

// TestYouTubeBindGrantToChannel_NoChannels_PreservesZeroChannelError
// preserves the existing 0-channel behaviour: DiscoverAccounts
// returns a typed error (the grant is bound to zero channels),
// BindGrantToChannel wraps but does NOT reclassify it as
// ErrYouTubeAmbiguousAuthorization. A zero-channel grant is a
// structurally distinct class of failure (likely a revoked Brand
// Account, not an ambiguous multi-channel one) and must not be
// misrouted by the caller — the handler would otherwise map it to
// 409 Conflict with the wrong operator runbook.
func TestYouTubeBindGrantToChannel_NoChannels_PreservesZeroChannelError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/youtube/v3/channels", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Mimics the Google-side response when the OAuth grant is
		// bound to zero channels (revoked Brand Account, etc.).
		json.NewEncoder(w).Encode(map[string]interface{}{
			"items":    []map[string]interface{}{},
			"pageInfo": map[string]int{"totalResults": 0, "resultsPerPage": 0},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	acc, err := svc.BindGrantToChannel(context.Background(), "fake-bearer", "")
	if err == nil {
		t.Fatalf("expected error on 0 channels, got nil and account=%+v", acc)
	}
	if acc != nil {
		t.Errorf("0 channels: account must be nil, got %+v", acc)
	}
	if errors.Is(err, ErrYouTubeAmbiguousAuthorization) {
		t.Errorf("0 channels must not be misclassified as ErrYouTubeAmbiguousAuthorization (would misroute to reauth path): %v", err)
	}
	if errors.Is(err, ErrYouTubeChannelMismatch) {
		t.Errorf("0 channels must not be misclassified as ErrYouTubeChannelMismatch either: %v", err)
	}
	if !strings.Contains(err.Error(), "no YouTube channel") {
		t.Errorf("error should preserve the upstream 'no YouTube channel' message for operator diagnostics, got %q", err.Error())
	}
}

// TestYouTubeBindGrantToChannel_ExpectedAndZeroChannels_PreservesUpstreamError
// covers the structural edge case: the operator supplied an
// expected_channel_id at /auth/youtube/login but the OAuth grant
// bound to ZERO channels (Brand Account revoked, account
// re-rotated, etc.). BindGrantToChannel must NOT wrap this as
// ErrYouTubeChannelMismatch — a 0-channel grant and a non-matching
// grant are structurally distinct failures with different operator
// runbooks ("re-authorize" vs "the channel id is wrong"), and the
// upstream error already communicates the right diagnostic.
func TestYouTubeBindGrantToChannel_ExpectedAndZeroChannels_PreservesUpstreamError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/youtube/v3/channels", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"items":    []map[string]interface{}{},
			"pageInfo": map[string]int{"totalResults": 0, "resultsPerPage": 0},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	acc, err := svc.BindGrantToChannel(context.Background(), "fake-bearer", "UCaaaaaaaaaaaaaaaaaaaaaX")
	if err == nil {
		t.Fatalf("expected error on 0 channels with expected set, got nil and account=%+v", acc)
	}
	if acc != nil {
		t.Errorf("0 channels: account must be nil, got %+v", acc)
	}
	if errors.Is(err, ErrYouTubeChannelMismatch) {
		t.Errorf("0 channels must NOT be misclassified as ErrYouTubeChannelMismatch — different runbook (re-authorize vs id mismatch): %v", err)
	}
	if errors.Is(err, ErrYouTubeAmbiguousAuthorization) {
		t.Errorf("0 channels must NOT be misclassified as ErrYouTubeAmbiguousAuthorization either: %v", err)
	}
	if !strings.Contains(err.Error(), "no YouTube channel") {
		t.Errorf("error should preserve the upstream 'no YouTube channel' message, got %q", err.Error())
	}
}

// TestYouTubeBindGrantToChannel_Transient5xx_NoSentinelMisclassification
// proves that transient (5xx) errors from channels.list do NOT
// carry either sentinel — the worker / future reconnect callers
// must retry rather than misclassify a 502 as a reauth-required
// state. This is the reverse of the existing TestYouTubeValidateChannelBinding_5xx_NoSentinel
// pattern in this file: it asserts the same property on the
// BindGrantToChannel path.
func TestYouTubeBindGrantToChannel_Transient5xx_NoSentinelMisclassification(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/youtube/v3/channels", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":{"code":502,"message":"Upstream Google error"}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	acc, err := svc.BindGrantToChannel(context.Background(), "fake-bearer", "")
	if err == nil {
		t.Fatalf("expected error on 5xx, got nil and account=%+v", acc)
	}
	if acc != nil {
		t.Errorf("5xx: account must be nil, got %+v", acc)
	}
	if errors.Is(err, ErrYouTubeAmbiguousAuthorization) {
		t.Errorf("transient 5xx must NOT wrap ErrYouTubeAmbiguousAuthorization (worker would misroute to reauth): %v", err)
	}
	if errors.Is(err, ErrYouTubeChannelMismatch) {
		t.Errorf("transient 5xx must NOT wrap ErrYouTubeChannelMismatch (worker would misroute to reauth): %v", err)
	}
	if !strings.Contains(err.Error(), "502") {
		t.Errorf("error should surface the upstream status for the worker's logged breadcrumb, got %q", err.Error())
	}
}

// TestYouTubeDiscoverAccounts_Pagination verifies that DiscoverAccounts
// follows nextPageToken to collect all channels across multiple pages.
func TestYouTubeDiscoverAccounts_Pagination(t *testing.T) {
	callCount := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/youtube/v3/channels", func(w http.ResponseWriter, r *http.Request) {
		callCount++
		pageToken := r.URL.Query().Get("pageToken")

		switch callCount {
		case 1:
			json.NewEncoder(w).Encode(youtubeChannelsResponse{
				Items: []youtubeChannel{
					{ID: "UCpage1ch1", Snippet: youtubeChannelSnippet{Title: "Channel 1"}},
					{ID: "UCpage1ch2", Snippet: youtubeChannelSnippet{Title: "Channel 2"}},
				},
				NextPageToken: "page2token",
			})
		case 2:
			if pageToken != "page2token" {
				t.Errorf("page 2: want pageToken=page2token, got %q", pageToken)
			}
			json.NewEncoder(w).Encode(youtubeChannelsResponse{
				Items: []youtubeChannel{
					{ID: "UCpage2ch1", Snippet: youtubeChannelSnippet{Title: "Channel 3"}},
				},
				NextPageToken: "",
			})
		default:
			t.Fatalf("unexpected call %d", callCount)
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	accounts, err := svc.DiscoverAccounts(context.Background(), "fake-token", "")
	if err != nil {
		t.Fatalf("DiscoverAccounts failed: %v", err)
	}

	if len(accounts) != 3 {
		t.Fatalf("expected 3 accounts across 2 pages, got %d", len(accounts))
	}
	if accounts[0].Profile.PlatformUserID != "UCpage1ch1" {
		t.Errorf("first account: want UCpage1ch1, got %q", accounts[0].Profile.PlatformUserID)
	}
	if accounts[2].Profile.PlatformUserID != "UCpage2ch1" {
		t.Errorf("last account: want UCpage2ch1, got %q", accounts[2].Profile.PlatformUserID)
	}
	if callCount != 2 {
		t.Errorf("expected 2 API calls, got %d", callCount)
	}
}

// =============================================================================
// P1#6 — Resumable upload chunk size + Retry-After-aware backoff
// =============================================================================

// youtubeTestSvcUpload wires a tiny chunk size + tiny backoff for the
// upload-loop tests so 200-byte files exercise multiple PUTs and the
// suite stays fast (real production defaults: 16 MB chunks, 1s/5m
// backoff — far too slow for unit tests). The cfg-driven load path
// is identical to production; only the values differ.
func youtubeTestSvcUpload(srv *httptest.Server) *YouTubeOAuthService {
	cfg := youtubeTestCfg()
	cfg.YouTubeUploadChunkBytes = 64
	cfg.YouTubeUploadMaxRetries = 3
	cfg.YouTubeUploadBackoffBaseMs = 1
	cfg.YouTubeUploadBackoffCapMs = 5
	svc, _ := NewYouTubeOAuthService(cfg, ProviderDependencies{HTTPClient: testClient(srv)})
	return svc
}

// TestYouTubeUpload_ChunkBoundary computes the chunk span correctly:
// a file of 200 bytes with a 64-byte chunkSize yields 4 PUTs in
// strict Content-Range order (64+64+64+8). Asserts each chunk's
// Content-Range header reflects the running byte offset so the
// resumable-upload contract is preserved.
func TestYouTubeUpload_ChunkBoundary(t *testing.T) {
	var gotRanges []string
	mux := http.NewServeMux()
	mux.HandleFunc("/upload", func(w http.ResponseWriter, r *http.Request) {
		gotRanges = append(gotRanges, r.Header.Get("Content-Range"))
		// Always 308 — server expects more bytes, no final 200.
		w.WriteHeader(http.StatusPermanentRedirect)
	})
	mux.HandleFunc("/source", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("Content-Length", "200")
		_, _ = w.Write(bytes.Repeat([]byte{0xAB}, 200))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc := youtubeTestSvcUpload(srv)

	_, err := svc.uploadVideoChunks(context.Background(), srv.URL+"/upload", srv.URL+"/source", 200)
	if err == nil {
		t.Fatalf("expected end-of-stream error, got nil")
	}
	if !strings.Contains(err.Error(), "no video ID") {
		t.Errorf("expected end-of-stream error, got: %v", err)
	}
	want := []string{
		"bytes 0-63/200",
		"bytes 64-127/200",
		"bytes 128-191/200",
		"bytes 192-199/200",
	}
	if len(gotRanges) != len(want) {
		t.Fatalf("chunk count: want %d PUTs, got %d (%v)", len(want), len(gotRanges), gotRanges)
	}
	for i, w := range want {
		if gotRanges[i] != w {
			t.Errorf("chunk %d Content-Range: want %q, got %q", i, w, gotRanges[i])
		}
	}
}

// TestYouTubeUpload_PutChunk_5xx_Retryable_RetryAfter verifies the
// 5xx-with-Retry-After path: putChunk returns retryable=true and
// retryAfter=parsed_value so the uploadVideoChunks loop sleeps for
// the server's hint rather than the calculated fallback.
func TestYouTubeUpload_PutChunk_5xx_Retryable_RetryAfter(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/upload", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "7")
		http.Error(w, "transient", http.StatusServiceUnavailable)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc := youtubeTestSvcUpload(srv)

	_, retryAfter, retryable, err := svc.putChunk(context.Background(), srv.URL+"/upload", []byte{0xAB}, "bytes 0-0/1", 1)
	if err == nil {
		t.Fatal("expected error on 503, got nil")
	}
	if !retryable {
		t.Errorf("503 should be retryable")
	}
	if retryAfter != 7*time.Second {
		t.Errorf("retryAfter: want 7s (parsed from Retry-After), got %s", retryAfter)
	}
}

// TestYouTubeUpload_PutChunk_429_RetryAfter verifies that 429 is
// treated as retryable with the parsed Retry-After honored as the
// retry hint. RFC 6585 / Google API conventions.
func TestYouTubeUpload_PutChunk_429_RetryAfter(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/upload", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "3")
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc := youtubeTestSvcUpload(srv)

	_, retryAfter, retryable, err := svc.putChunk(context.Background(), srv.URL+"/upload", []byte{0xAB}, "bytes 0-0/1", 1)
	if err == nil {
		t.Fatal("expected error on 429, got nil")
	}
	if !retryable {
		t.Errorf("429 should be retryable")
	}
	if retryAfter != 3*time.Second {
		t.Errorf("retryAfter: want 3s, got %s", retryAfter)
	}
}

// TestYouTubeUpload_PutChunk_4xx_NotRetried verifies the design
// decision that 4xx (other than 429) is permanent: bubble up
// immediately so the upload-job worker MarkDeadLetter on attempt 1
// instead of wasting the chunk budget on a row YouTube will reject
// forever. Google's docs are clear: bad metadata, body validation
// errors, etc. will not fix themselves on retry.
func TestYouTubeUpload_PutChunk_4xx_NotRetried(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/upload", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad metadata", http.StatusBadRequest)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc := youtubeTestSvcUpload(srv)

	_, _, retryable, err := svc.putChunk(context.Background(), srv.URL+"/upload", []byte{0xAB}, "bytes 0-0/1", 1)
	if err == nil {
		t.Fatal("expected error on 400, got nil")
	}
	if retryable {
		t.Errorf("400 must NOT be retryable (bubble up to MarkDeadLetter)")
	}
}

// TestYouTubeUpload_PutChunk_NetworkError_Retryable verifies that a
// transport-level error (DNS, TCP reset) is treated as retryable so
// the uploadVideoChunks loop can resume from queryUploadStatus.
func TestYouTubeUpload_PutChunk_NetworkError_Retryable(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/upload", func(w http.ResponseWriter, r *http.Request) {
		// Hijack + close to force a transport-level RST.
		conn, _, _ := w.(http.Hijacker).Hijack()
		_ = conn.Close()
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc := youtubeTestSvcUpload(srv)

	_, _, retryable, err := svc.putChunk(context.Background(), srv.URL+"/upload", []byte{0xAB}, "bytes 0-0/1", 1)
	if err == nil {
		t.Fatal("expected error on connection reset, got nil")
	}
	if !retryable {
		t.Errorf("transport error should be retryable, got retryable=false")
	}
}

// TestYouTubeUpload_MaxRetriesExceeded verifies the chunk-retry
// budget: with MaxRetries=3, uploadVideoChunks issues 4 chunk PUTs
// (initial + 3 retries) before bubbling up the error. Without this
// cap, an outage would loop forever.
//
// Discrimination: the mux path /upload handles both chunk PUTs
// (Content-Range "bytes X-Y/TOTAL") AND the recovery phase's
// queryUploadStatusWithRetry PUTs (Content-Range "bytes */TOTAL").
// We inspect the header so we only count chunk PUTs — keeping the
// assertion crisp even though the recovery phase issues its own PUTs
// to the same path. The query path returns 308 with no Range header
// so the resume point is byte 0 (recovery re-GETs the full source,
// simulating "no bytes streamed yet"); chunk PUTs always return 503
// to drive the retry path.
func TestYouTubeUpload_MaxRetriesExceeded(t *testing.T) {
	var chunkPutCount int
	mux := http.NewServeMux()
	mux.HandleFunc("/upload", func(w http.ResponseWriter, r *http.Request) {
		cr := r.Header.Get("Content-Range")
		if strings.HasPrefix(cr, "bytes *") {
			// queryUploadStatusWithRetry: succeed with 308 + no
			// Range header so the resume point is byte 0. This
			// isolates the chunk-retry-counting assertion from
			// any errors in the recovery helper itself.
			w.WriteHeader(http.StatusPermanentRedirect)
			return
		}
		chunkPutCount++
		http.Error(w, "still down", http.StatusServiceUnavailable)
	})
	mux.HandleFunc("/source", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("Content-Length", "10")
		_, _ = w.Write(bytes.Repeat([]byte{0}, 10))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc := youtubeTestSvcUpload(srv)

	_, err := svc.uploadVideoChunks(context.Background(), srv.URL+"/upload", srv.URL+"/source", 10)
	if err == nil {
		t.Fatal("expected MaxRetries error, got nil")
	}
	if chunkPutCount != 4 {
		t.Errorf("chunk PUT count: want 4 (initial + 3 retries), got %d", chunkPutCount)
	}
}

// TestYouTubeUpload_SleepCtxInterruptible verifies the canonical
// shutdown-safe sleep shape: a 1-hour pending sleep returns within
// milliseconds when the ctx is cancelled mid-flight. time.Sleep would
// block past cancellation and break the worker's drain-then-stop
// contract — this is precisely why defaultYouTubeSleep uses
// time.NewTimer + select on ctx.Done().
func TestYouTubeUpload_SleepCtxInterruptible(t *testing.T) {
	svc := youtubeTestSvcUpload(httptest.NewServer(http.NewServeMux()))
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	err := svc.uploadDeps.sleep(ctx, time.Hour)
	if err == nil {
		t.Fatal("expected ctx error after cancel, got nil")
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Errorf("sleep did not interrupt cleanly (elapsed: %s)", elapsed)
	}
}

// TestParseRetryAfterHeader covers the RFC 7231 §7.1.3 parsing
// matrix: delta-seconds (the common case), HTTP-date (deprecated
// but seen in the wild), empty, garbage, and the past-date clamp.
func TestParseRetryAfterHeader(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		if got := parseRetryAfterHeader(""); got != 0 {
			t.Errorf("got %s, want 0", got)
		}
	})
	t.Run("zero-seconds", func(t *testing.T) {
		if got := parseRetryAfterHeader("0"); got != 0 {
			t.Errorf("got %s, want 0", got)
		}
	})
	t.Run("delta-seconds", func(t *testing.T) {
		if got := parseRetryAfterHeader("120"); got != 120*time.Second {
			t.Errorf("got %s, want 120s", got)
		}
	})
	t.Run("negative-clamped", func(t *testing.T) {
		if got := parseRetryAfterHeader("-5"); got != 0 {
			t.Errorf("got %s, want 0 (clamped from -5)", got)
		}
	})
	t.Run("unparseable", func(t *testing.T) {
		if got := parseRetryAfterHeader("not-a-number"); got != 0 {
			t.Errorf("got %s, want 0 (unparseable)", got)
		}
	})
	t.Run("http-date-future", func(t *testing.T) {
		// 60 s in the future → expected ~60 s band (with clock drift).
		hd := time.Now().Add(60 * time.Second).UTC().Format(http.TimeFormat)
		got := parseRetryAfterHeader(hd)
		if got < 50*time.Second || got > 61*time.Second {
			t.Errorf("got %s, want ~60s window", got)
		}
	})
	t.Run("http-date-past-clamped", func(t *testing.T) {
		// Past date → clamped to 0 (never wait a negative amount).
		hd := time.Now().Add(-60 * time.Second).UTC().Format(http.TimeFormat)
		if got := parseRetryAfterHeader(hd); got != 0 {
			t.Errorf("got %s, want 0 (clamped from past)", got)
		}
	})
}

// TestComputeYouTubeBackoff_Bounds verifies the full-jitter curve
// stays within [base, cap] for every attempt number — important
// because computeYouTubeBackoff is what uploadVideoChunks falls
// back to when the server didn't send a Retry-After.
func TestComputeYouTubeBackoff_Bounds(t *testing.T) {
	base := 100 * time.Millisecond
	capd := 1 * time.Second
	fn := computeYouTubeBackoff(base, capd)
	for attempt := 1; attempt <= 12; attempt++ {
		got := fn(attempt)
		if got < base {
			t.Errorf("attempt %d: %s below base %s", attempt, got, base)
		}
		if got > capd {
			t.Errorf("attempt %d: %s above cap %s", attempt, got, capd)
		}
	}
}

// TestYouTubeGetTokenInfo_HappyPath exercises the canonical path:
// mock Google's /oauth2/v3/tokeninfo to reply 200 with a fully-shaped
// JSON body, then assert every field on YouTubeTokenInfo is hydrated
// correctly AND the HasUpload / HasReadonly derived flags flip to true.
// This is the contract pkg/api/handlers.go::handleValidateAccount
// (step 2 of the 4-step YouTube OAuth readiness pipeline) relies on.
func TestYouTubeGetTokenInfo_HappyPath(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth2/v3/tokeninfo", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("access_token"); got != "fresh-access-token" {
			t.Errorf("tokeninfo endpoint got access_token=%q, want fresh-access-token", got)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"aud":"%s","azp":"%s","scope":"%s %s openid email profile","expires_in":3599,"access_type":"offline","email":"operator@example.com"}`,
			"test-youtube-client-id",
			"test-youtube-client-id",
			"https://www.googleapis.com/auth/youtube.upload",
			"https://www.googleapis.com/auth/youtube.readonly",
		)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	info, err := svc.GetTokenInfo(context.Background(), "fresh-access-token")
	if err != nil {
		t.Fatalf("GetTokenInfo happy-path: unexpected error: %v", err)
	}
	if info.Aud != "test-youtube-client-id" {
		t.Errorf("Aud: got %q, want test-youtube-client-id", info.Aud)
	}
	if info.Azp != "test-youtube-client-id" {
		t.Errorf("Azp: got %q, want test-youtube-client-id", info.Azp)
	}
	if !info.HasUpload {
		t.Errorf("HasUpload: want true, got false (Scope=%q)", info.Scope)
	}
	if !info.HasReadonly {
		t.Errorf("HasReadonly: want true, got false (Scope=%q)", info.Scope)
	}
	if info.ExpiresIn != 3599*time.Second {
		t.Errorf("ExpiresIn: got %s, want 3599s", info.ExpiresIn)
	}
	if info.Email != "operator@example.com" {
		t.Errorf("Email: got %q, want operator@example.com", info.Email)
	}
}

// TestYouTubeGetTokenInfo_MissingUploadScope asserts the SHAPE of the
// error the handler maps to 422 + reauth_required. The Hard-fail
// contract is: a token that scopes youtube.readonly but NOT
// youtube.upload MUST surface as a google-returned 400 (the endpoint
// does run; the scope-shape check is the handler's job). Both shapes
// are exercised here as separate subtests so a regression on the
// Google-response path doesn't accidentally over-trigger the
// HasUpload-false code path.
//
// This is the step-2 failure matrix handleValidateAccount depends on.
func TestYouTubeGetTokenInfo_SurfaceAllContractShapes(t *testing.T) {
	t.Run("empty_access_token_rejected", func(t *testing.T) {
		srv := httptest.NewServer(http.NewServeMux())
		defer srv.Close()
		svc := newTestYouTubeService(srv)
		if _, err := svc.GetTokenInfo(context.Background(), ""); err == nil {
			t.Fatal("expected error on empty access token, got nil")
		}
	})

	t.Run("google_400_invalid_token_surfaces_wrapped_error", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/oauth2/v3/tokeninfo", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, `{"error":"invalid_token","error_description":"Token expired or revoked"}`)
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()
		svc := newTestYouTubeService(srv)

		_, err := svc.GetTokenInfo(context.Background(), "stale-token")
		if err == nil {
			t.Fatal("expected error on Google 400 response, got nil")
		}
		if !strings.Contains(err.Error(), "400") {
			t.Errorf("error message must contain the literal status code 400 (handler reads it for 422 mapping); got %v", err)
		}
		if !strings.Contains(err.Error(), "invalid_token") {
			t.Errorf("error message must contain Google's invalid_token payload for the audit log; got %v", err)
		}
	})

	t.Run("garbage_body_decode_error_is_plain_wrapped", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/oauth2/v3/tokeninfo", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `[this is definitely not valid json`)
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()
		svc := newTestYouTubeService(srv)

		_, err := svc.GetTokenInfo(context.Background(), "good-shape-bad-body")
		if err == nil {
			t.Fatal("expected decode error on garbage body, got nil")
		}
		if !strings.Contains(err.Error(), "decode") {
			t.Errorf("non-token error must mention decode so handler classifies it transient (NOT reauth); got %v", err)
		}
	})

	t.Run("missing_upload_scope_keeps_HasUpload_false", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/oauth2/v3/tokeninfo", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, `{"aud":"test-youtube-client-id","azp":"test-youtube-client-id","scope":"https://www.googleapis.com/auth/youtube.readonly openid email profile","expires_in":300}`)
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()
		svc := newTestYouTubeService(srv)

		info, err := svc.GetTokenInfo(context.Background(), "readonly-only-token")
		if err != nil {
			t.Fatalf("scope-missing: unexpected error: %v", err)
		}
		if info.HasUpload {
			t.Errorf("HasUpload on readonly-only token: want false, got true (handler would let it through step 2 incorrectly)")
		}
		if !info.HasReadonly {
			t.Errorf("HasReadonly on readonly-only token: want true, got false (would force-fail step 2 incorrectly)")
		}
	})

	t.Run("missing_readonly_scope_keeps_HasReadonly_false", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/oauth2/v3/tokeninfo", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, `{"aud":"test-youtube-client-id","scope":"https://www.googleapis.com/auth/youtube.upload openid email profile","expires_in":300}`)
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()
		svc := newTestYouTubeService(srv)

		info, err := svc.GetTokenInfo(context.Background(), "upload-only-token")
		if err != nil {
			t.Fatalf("scope-missing: unexpected error: %v", err)
		}
		if !info.HasUpload {
			t.Errorf("HasUpload on upload-only token: want true, got false")
		}
		if info.HasReadonly {
			t.Errorf("HasReadonly on upload-only token: want false, got true (the handler needs readonly for channels.list)")
		}
	})
}

// TestYouTubeCanaryUpload_HappyPath mocks the full canary pipeline on
// a single httptest server (testClient re-routes every Google URL
// through the same mux):
//
//   1. POST /upload/youtube/v3/videos?uploadType=resumable returns a
//      Location: /canary-session header. The mock also asserts the
//      metadata body contains the INSTAEDIT-OAUTH-CANARY-{channel}-{ts}
//      title prefix, the expected channel id embedded, and the
//      status.privacyStatus="private" guard.
//   2. PUT /canary-session returns 200 + a synthesized
//      {"id":"<videoID>"} terminal body so putChunk reports the
//      video id.
//   3. GET /youtube/v3/videos?id=<videoID>&part=snippet,status,processingDetails
//      returns the same video id with snippet.channelId equal to
//      the expected channel — the post-upload reconcile that
//      step-4 uses as the source of truth.
//
// Asserts: err == nil, result.VideoID == <videoID>,
// result.UploadedChannelID == "UCexpectedChannelID".
func TestYouTubeCanaryUpload_HappyPath(t *testing.T) {
	const (
		expectedChannel = "UCexpectedChannelID"
		canaryVideoID   = "viabc123def4"
	)
	mux := http.NewServeMux()
	mux.HandleFunc("/upload/youtube/v3/videos", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("uploadType") != "resumable" {
			t.Errorf("canary init: uploadType got %q, want resumable", r.URL.Query().Get("uploadType"))
		}
		body, _ := io.ReadAll(r.Body)
		var meta map[string]interface{}
		if jerr := json.Unmarshal(body, &meta); jerr != nil {
			t.Errorf("canary init: metadata is not JSON: %v / body=%s", jerr, string(body))
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		snippet, _ := meta["snippet"].(map[string]interface{})
		if snippet == nil {
			t.Errorf("canary init: missing snippet block in metadata; got %+v", meta)
		} else {
			title, _ := snippet["title"].(string)
			if !strings.HasPrefix(title, "INSTAEDIT-OAUTH-CANARY-") {
				t.Errorf("canary init: title %q does not start with INSTAEDIT-OAUTH-CANARY-", title)
			}
			if !strings.Contains(title, expectedChannel) {
				t.Errorf("canary init: title %q must embed the expected channel id %s", title, expectedChannel)
			}
		}
		status, _ := meta["status"].(map[string]interface{})
		if status == nil {
			t.Errorf("canary init: missing status block in metadata")
		} else if ps, _ := status["privacyStatus"].(string); ps != "private" {
			t.Errorf("canary init: privacyStatus got %q, want private (the canary must NOT land on the operator's public tab)", ps)
		}
		w.Header().Set("Location", "/canary-session")
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/canary-session", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("canary session: expected PUT, got %s", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if r.Header.Get("Content-Range") == "" {
			t.Errorf("canary session: missing Content-Range header on final PUT")
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"id":"%s"}`, canaryVideoID)
	})
	mux.HandleFunc("/youtube/v3/videos", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("id") != canaryVideoID {
			t.Errorf("videos.list: id got %q, want %s", r.URL.Query().Get("id"), canaryVideoID)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"items":[{"id":"%s","snippet":{"channelId":"%s","title":"canary"}}]}`, canaryVideoID, expectedChannel)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	svc := newTestYouTubeService(srv)

	res, err := svc.CanaryUpload(context.Background(), "fresh-access-token", expectedChannel)
	if err != nil {
		t.Fatalf("canary happy-path: unexpected error: %v", err)
	}
	if res == nil {
		t.Fatal("canary happy-path: result is nil")
		return
	}
	if res.VideoID != canaryVideoID {
		t.Errorf("canary VideoID: got %q, want %s", res.VideoID, canaryVideoID)
	}
	if res.UploadedChannelID != expectedChannel {
		t.Errorf("canary UploadedChannelID: got %q, want %q", res.UploadedChannelID, expectedChannel)
	}
}

// TestYouTubeCanaryUpload_AllContractShapes exercises the four
// failure surfaces the handler maps to its 422 / transient
// classification:
//
//   - bind-mismatch (snippet.channelId != expected) → wrap
//     ErrYouTubeChannelMismatch → handler → 422 + reauth_required.
//   - initiate 4xx (POST /upload returns 400) → wrap
//     ErrYouTubeCanaryRejected (the "canary upload rejected by
//     videos.insert" sentinel) → handler → 422 + reauth_required.
//   - PUT 5xx transient → plain wrapped (NOT a sentinel) → handler
//     treats as transient, next-sync retry.
//   - empty input fields → fast-fail with no network round trip.
//
// The bind-mismatch case is the canonical P2 hardening scenario:
// the OAuth grant produced a video on the WRONG channel (you can
// reach videos.insert successfully but the grant was silently
// re-bound to a Brand Account the operator didn't intend). The
// worker MUST escalate to reauth_required so the operator picks up
// the drift before the next upload-attempt lands their content
// somewhere unauthorised.
func TestYouTubeCanaryUpload_AllContractShapes(t *testing.T) {
	t.Run("bind_mismatch_returns_typed_sentinel", func(t *testing.T) {
		const (
			expectedChannel = "UCexpectedChannelID"
			canaryVideoID   = "vibindmismatch01"
			actualChannel   = "UCdifferentChannelID"
		)
		mux := http.NewServeMux()
		mux.HandleFunc("/upload/youtube/v3/videos", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", "/canary-session")
		})
		mux.HandleFunc("/canary-session", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"id":"%s"}`, canaryVideoID)
		})
		mux.HandleFunc("/youtube/v3/videos", func(w http.ResponseWriter, r *http.Request) {
			// snippet.channelId intentionally ≠ expected — bind-mismatch.
			fmt.Fprintf(w, `{"items":[{"id":"%s","snippet":{"channelId":"%s","title":"canary"}}]}`, canaryVideoID, actualChannel)
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()
		svc := newTestYouTubeService(srv)

		_, err := svc.CanaryUpload(context.Background(), "fresh-access-token", expectedChannel)
		if err == nil {
			t.Fatal("canary bind-mismatch: expected error, got nil")
		}
		if !errors.Is(err, ErrYouTubeChannelMismatch) {
			t.Errorf("bind-mismatch MUST wrap services.ErrYouTubeChannelMismatch so the handler routes it to 422 + reauth; got %v", err)
		}
		if !strings.Contains(err.Error(), actualChannel) {
			t.Errorf("bind-mismatch error must surface the actually-uploaded channel for the operator log; got %v", err)
		}
	})

	t.Run("initiate_4xx_returns_canary_rejected_sentinel", func(t *testing.T) {
		const expectedChannel = "UCexpectedChannelID"
		mux := http.NewServeMux()
		mux.HandleFunc("/upload/youtube/v3/videos", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":{"message":"invalid video metadata"}}`)
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()
		svc := newTestYouTubeService(srv)

		_, err := svc.CanaryUpload(context.Background(), "fresh-access-token", expectedChannel)
		if err == nil {
			t.Fatal("canary init 4xx: expected error, got nil")
		}
		if !errors.Is(err, ErrYouTubeCanaryRejected) {
			t.Errorf("init 4xx MUST wrap ErrYouTubeCanaryRejected so handler routes to 422; got %v", err)
		}
	})

	t.Run("put_5xx_returns_plain_wrapped_transient", func(t *testing.T) {
		const expectedChannel = "UCexpectedChannelID"
		mux := http.NewServeMux()
		mux.HandleFunc("/upload/youtube/v3/videos", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", "/canary-session")
		})
		mux.HandleFunc("/canary-session", func(w http.ResponseWriter, r *http.Request) {
			// 503 — transient. putChunk formats this as
			// 'server error (status 503)' which the canary
			// helper explicitly treats as transient.
			http.Error(w, "google internal error", http.StatusServiceUnavailable)
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()
		svc := newTestYouTubeService(srv)

		_, err := svc.CanaryUpload(context.Background(), "fresh-access-token", expectedChannel)
		if err == nil {
			t.Fatal("canary PUT 5xx: expected error, got nil")
		}
		if errors.Is(err, ErrYouTubeCanaryRejected) {
			t.Errorf("put 5xx MUST NOT wrap ErrYouTubeCanaryRejected (next-sync retry should handle it); got %v", err)
		}
		if errors.Is(err, ErrYouTubeChannelMismatch) {
			t.Errorf("put 5xx MUST NOT wrap ErrYouTubeChannelMismatch (channel drift is unrelated to a 503); got %v", err)
		}
		if !strings.Contains(err.Error(), "503") && !strings.Contains(err.Error(), "server error") {
			t.Errorf("put 5xx error must preserve the underlying transient signal for the handler log; got %v", err)
		}
	})

	t.Run("empty_inputs_fast_fail_without_network", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/upload/youtube/v3/videos", func(w http.ResponseWriter, r *http.Request) {
			t.Error("empty-input canary MUST NOT issue any outbound request")
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()
		svc := newTestYouTubeService(srv)

		if _, err := svc.CanaryUpload(context.Background(), "valid-token", ""); err == nil {
			t.Errorf("empty expectedChannelID: expected error, got nil")
		}
		if _, err := svc.CanaryUpload(context.Background(), "", "UCexpected"); err == nil {
			t.Errorf("empty accessToken: expected error, got nil")
		}
	})
}
