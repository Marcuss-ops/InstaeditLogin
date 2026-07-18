package services

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/config"
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

// TestYouTubeLoginURL_IncludesReadonlyAndUploadScopes verifies that
// GetLoginURL requests both youtube.upload and youtube.readonly scopes
// along with openid, email, and profile.
func TestYouTubeLoginURL_IncludesReadonlyAndUploadScopes(t *testing.T) {
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
