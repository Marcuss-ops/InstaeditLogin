package services

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

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

func TestYouTubeStatisticsAcceptStringCounts(t *testing.T) {
	var stats youtubeStatistics
	if err := json.Unmarshal([]byte(`{
		"subscriberCount":"125000",
		"hiddenSubscriberCount":false,
		"viewCount":"18000000",
		"videoCount":"942"
	}`), &stats); err != nil {
		t.Fatalf("unmarshal YouTube string statistics: %v", err)
	}
	if stats.SubscriberCount != 125000 || stats.ViewCount != 18000000 || stats.VideoCount != 942 {
		t.Fatalf("unexpected statistics: %+v", stats)
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
