package api

import (
	"context"
	"errors"
	"fmt"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestFetchAccountEditableVideos_PaginatesUntilConfiguredLimit(t *testing.T) {
	account := &models.PlatformAccount{ID: 42, Platform: models.PlatformYouTube, PlatformUserID: "UC123"}
	var pageTokens []string
	ytSvc := &mockYouTubeOAuthServiceForEditor{
		listEditableVideosFn: func(_ context.Context, _, _ string, pageToken string) (*services.YouTubeVideoPage, error) {
			pageTokens = append(pageTokens, pageToken)
			switch pageToken {
			case "":
				return &services.YouTubeVideoPage{
					Items: []models.YouTubeVideoDetails{{ID: "v1"}, {ID: "v2"}}, NextPageToken: "page-2",
				}, nil
			case "page-2":
				return &services.YouTubeVideoPage{Items: []models.YouTubeVideoDetails{{ID: "v3"}, {ID: "v4"}}}, nil
			default:
				return nil, fmt.Errorf("unexpected page token %q", pageToken)
			}
		},
	}
	r := &Router{
		vault: &mockCredentialVault{getFn: func(context.Context, int64, string) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "access-token"}, nil
		}},
		youTubeSvc: ytSvc,
	}

	items, err := r.fetchAccountEditableVideos(context.Background(), account, 3)
	if err != nil {
		t.Fatalf("fetchAccountEditableVideos: %v", err)
	}
	if got, want := len(items), 3; got != want {
		t.Fatalf("items: got %d, want %d", got, want)
	}
	if got := []string{items[0].ID, items[1].ID, items[2].ID}; fmt.Sprint(got) != "[v1 v2 v3]" {
		t.Errorf("item order: got %v, want [v1 v2 v3]", got)
	}
	if got, want := fmt.Sprint(pageTokens), "[ page-2]"; got != want {
		t.Errorf("page tokens: got %s, want %s", got, want)
	}
}

func TestFetchCachedAccountEditableVideos_UsesShortLivedCache(t *testing.T) {
	account := &models.PlatformAccount{ID: 42, Platform: models.PlatformYouTube, PlatformUserID: "UC123"}
	var listCalls int
	ytSvc := &mockYouTubeOAuthServiceForEditor{
		listEditableVideosFn: func(context.Context, string, string, string) (*services.YouTubeVideoPage, error) {
			listCalls++
			return &services.YouTubeVideoPage{Items: []models.YouTubeVideoDetails{{ID: "cached-video"}}}, nil
		},
	}
	r := &Router{
		vault: &mockCredentialVault{getFn: func(context.Context, int64, string) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "access-token"}, nil
		}},
		youTubeSvc: ytSvc,
	}
	cfg := YouTubeGroupVideosConfig{MaxVideos: 10, CacheTTL: time.Minute}.normalized()

	first, err := r.fetchCachedAccountEditableVideos(context.Background(), account, cfg, false)
	if err != nil {
		t.Fatalf("first fetch: %v", err)
	}
	second, err := r.fetchCachedAccountEditableVideos(context.Background(), account, cfg, false)
	if err != nil {
		t.Fatalf("second fetch: %v", err)
	}
	if listCalls != 1 {
		t.Fatalf("YouTube list calls: got %d, want 1", listCalls)
	}
	if len(first) != 1 || len(second) != 1 || first[0].ID != second[0].ID {
		t.Fatalf("cached results differ: first=%+v second=%+v", first, second)
	}
}

func TestFetchCachedAccountEditableVideos_ForceRefreshBypassesCache(t *testing.T) {
	account := &models.PlatformAccount{ID: 44, Platform: models.PlatformYouTube, PlatformUserID: "UC-force"}
	var listCalls int
	ytSvc := &mockYouTubeOAuthServiceForEditor{
		listEditableVideosFn: func(context.Context, string, string, string) (*services.YouTubeVideoPage, error) {
			listCalls++
			return &services.YouTubeVideoPage{Items: []models.YouTubeVideoDetails{{ID: fmt.Sprintf("video-%d", listCalls)}}}, nil
		},
	}
	r := &Router{
		vault: &mockCredentialVault{getFn: func(context.Context, int64, string) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "access-token"}, nil
		}},
		youTubeSvc: ytSvc,
	}
	cfg := YouTubeGroupVideosConfig{MaxVideos: 10, CacheTTL: time.Minute}.normalized()
	if _, err := r.fetchCachedAccountEditableVideos(context.Background(), account, cfg, false); err != nil {
		t.Fatalf("initial fetch: %v", err)
	}
	items, err := r.fetchCachedAccountEditableVideos(context.Background(), account, cfg, true)
	if err != nil {
		t.Fatalf("forced fetch: %v", err)
	}
	if listCalls != 2 {
		t.Fatalf("YouTube list calls: got %d, want 2", listCalls)
	}
	if got := items[0].ID; got != "video-2" {
		t.Fatalf("forced fetch returned %q, want video-2", got)
	}
}

func TestFetchCachedAccountEditableVideos_SharesConcurrentMiss(t *testing.T) {
	account := &models.PlatformAccount{ID: 43, Platform: models.PlatformYouTube, PlatformUserID: "UC-concurrent"}
	var listCalls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	ytSvc := &mockYouTubeOAuthServiceForEditor{
		listEditableVideosFn: func(context.Context, string, string, string) (*services.YouTubeVideoPage, error) {
			listCalls.Add(1)
			close(started)
			<-release
			return &services.YouTubeVideoPage{Items: []models.YouTubeVideoDetails{{ID: "single-flight"}}}, nil
		},
	}
	r := &Router{
		vault: &mockCredentialVault{getFn: func(context.Context, int64, string) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "access-token"}, nil
		}},
		youTubeSvc: ytSvc,
	}
	cfg := YouTubeGroupVideosConfig{MaxVideos: 10, CacheTTL: time.Minute}.normalized()
	results := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := r.fetchCachedAccountEditableVideos(context.Background(), account, cfg, false)
			results <- err
		}()
	}
	<-started
	close(release)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatalf("concurrent fetch: %v", err)
		}
	}
	if got := listCalls.Load(); got != 1 {
		t.Fatalf("YouTube list calls: got %d, want 1", got)
	}
}

func TestIsInvalidYouTubeTokenError_DoesNotClassifyVaultOutage(t *testing.T) {
	if isInvalidYouTubeTokenError(errors.New("vault: database unavailable")) {
		t.Fatal("database outage must not mark an account for reauthentication")
	}
	if !isInvalidYouTubeTokenError(errors.New("oauth2: invalid_grant (token revoked)")) {
		t.Fatal("invalid_grant must mark an account for reauthentication")
	}
}

func TestParseGroupVideosPagination(t *testing.T) {
	cfg := YouTubeGroupVideosConfig{MaxVideos: 100, DefaultPageSize: 25}.normalized()
	tests := []struct {
		name       string
		query      string
		wantOffset int
		wantLimit  int
		wantErr    bool
	}{
		{name: "defaults", query: "", wantLimit: 25},
		{name: "offset and limit", query: "offset=10&limit=7", wantOffset: 10, wantLimit: 7},
		{name: "limit capped", query: "limit=1000", wantLimit: 100},
		{name: "negative offset", query: "offset=-1", wantErr: true},
		{name: "zero limit", query: "limit=0", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/?"+tt.query, nil)
			offset, limit, err := parseGroupVideosPagination(req, cfg)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error: got %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if offset != tt.wantOffset || limit != tt.wantLimit {
				t.Errorf("pagination: got offset=%d limit=%d, want offset=%d limit=%d", offset, limit, tt.wantOffset, tt.wantLimit)
			}
		})
	}
}

func TestGroupYouTubeVideos_InvalidTokenClassification(t *testing.T) {
	for _, test := range []struct {
		message string
		want    bool
	}{
		{message: "oauth: invalid_grant", want: true},
		{message: "youtube list: status 401", want: true},
		{message: "youtube list: status 500", want: false},
		{message: "context deadline exceeded", want: false},
	} {
		t.Run(test.message, func(t *testing.T) {
			if got := isInvalidYouTubeTokenError(errors.New(test.message)); got != test.want {
				t.Errorf("isInvalidYouTubeTokenError(%q) = %v, want %v", test.message, got, test.want)
			}
		})
	}
}
