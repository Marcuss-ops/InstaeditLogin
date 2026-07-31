package services

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

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
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, fmt.Errorf("youtube channel details returned %d", resp.StatusCode)
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
		"country":                 ch.Snippet.Country,
		"uploads_playlist_id":     ch.ContentDetails.RelatedPlaylists.Uploads,
		"hidden_subscriber_count": ch.Statistics.HiddenSubscriberCount,
	}

	return details, nil
}

// ListAccountContent returns recent videos from a YouTube channel by
// reading the channel's uploads playlist and then fetching video
// details. Pagination is supported via the cursor (nextPageToken).
func (s *YouTubeOAuthService) ListAccountContent(ctx context.Context, accessToken, platformUserID string, cursor string, limit int, privacyFilter string) (*models.AccountContentPage, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}

	// Step 1: Get the uploads playlist ID for this channel.
	uploadsPlaylist, err := s.getUploadsPlaylistID(ctx, accessToken, platformUserID)
	if err != nil {
		return nil, fmt.Errorf("get uploads playlist: %w", err)
	}

	// No privacy filter: preserve the original single-page behaviour.
	if privacyFilter == "" {
		return s.listUnfilteredAccountContent(ctx, accessToken, uploadsPlaylist, cursor, limit)
	}

	// Filtered path: walk playlist pages until we collect enough private
	// videos, run out of pages, or hit a safety cap. We use maxResults=50
	// per page to minimise round-trips and pass the cursor through so
	// load-more continues from the next YouTube page.
	const maxPages = 10
	var (
		items     []models.AccountContentItem
		pageToken = cursor
		pages     int
	)
	for pages < maxPages {
		videoIDs, nextPage, err := s.listPlaylistItems(ctx, accessToken, uploadsPlaylist, pageToken, 50)
		if err != nil {
			return nil, fmt.Errorf("list playlist items: %w", err)
		}
		if len(videoIDs) == 0 {
			return &models.AccountContentPage{Items: items}, nil
		}

		details, err := s.getVideoDetails(ctx, accessToken, videoIDs)
		if err != nil {
			return nil, fmt.Errorf("get video details: %w", err)
		}

		for _, item := range details {
			if item.Privacy == privacyFilter {
				items = append(items, item)
			}
		}

		if len(items) >= limit {
			// We have enough items to satisfy the request. Return what we
			// collected plus the next YouTube page token so the client can
			// load more. We intentionally do NOT slice to exactly `limit`
			// because that would drop already-fetched private videos from
			// the current chunk, causing data loss on the next page.
			return &models.AccountContentPage{Items: items, NextCursor: nextPage}, nil
		}

		if nextPage == "" {
			return &models.AccountContentPage{Items: items}, nil
		}

		pageToken = nextPage
		pages++
	}

	return &models.AccountContentPage{Items: items, NextCursor: pageToken}, nil
}

func (s *YouTubeOAuthService) listUnfilteredAccountContent(ctx context.Context, accessToken, uploadsPlaylist, cursor string, limit int) (*models.AccountContentPage, error) {
	// Step 1: List recent items from the uploads playlist.
	videoIDs, nextPageToken, err := s.listPlaylistItems(ctx, accessToken, uploadsPlaylist, cursor, limit)
	if err != nil {
		return nil, fmt.Errorf("list playlist items: %w", err)
	}

	if len(videoIDs) == 0 {
		return &models.AccountContentPage{Items: []models.AccountContentItem{}}, nil
	}

	// Step 2: Fetch video details (snippet, statistics, contentDetails, status).
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
		_, _ = io.Copy(io.Discard, resp.Body)
		return "", fmt.Errorf("channels.list returned %d", resp.StatusCode)
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
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, "", fmt.Errorf("playlistItems.list returned %d", resp.StatusCode)
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
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, fmt.Errorf("videos.list returned %d", resp.StatusCode)
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
			Duration:     v.ContentDetails.Duration,
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

// SetThumbnail uploads a JPEG/PNG image to YouTube and applies it as the
// custom thumbnail for the given video. The caller must supply a valid
// access token (retrieved from the vault) and the image must meet the
// YouTube Data API constraints (JPEG or PNG, max 2 MB).
//
// Error handling:
//   - 200 OK → success, nil error.
//   - 401 Unauthorized → the access token is invalid or expired.
//   - 403 Forbidden → the grant lacks permission or the channel is
//     ineligible for custom thumbnails.
//   - 404 Not Found → the video does not exist.
//   - 429 Too Many Requests → rate limited; the caller should retry.
//   - 5xx → transient server error; the caller should retry.
//
// The access token is never included in any returned error.
func (s *YouTubeOAuthService) GetYouTubeVideo(ctx context.Context, accessToken, videoID string) (*models.YouTubeVideoDetails, error) {
	if videoID == "" {
		return nil, fmt.Errorf("youtube video details: empty video id")
	}

	params := url.Values{}
	params.Set("part", "snippet,status,contentDetails")
	params.Set("id", videoID)

	reqURL := "https://www.googleapis.com/youtube/v3/videos?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("youtube video details: create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("youtube video details: request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, fmt.Errorf("youtube video details: videos.list returned %d", resp.StatusCode)
	}

	var result youtubeVideosResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("youtube video details: decode: %w", err)
	}
	if len(result.Items) == 0 {
		return nil, fmt.Errorf("youtube video details: video %s not found", videoID)
	}

	v := result.Items[0]
	return &models.YouTubeVideoDetails{
		ID:           v.ID,
		Title:        v.Snippet.Title,
		ChannelID:    v.Snippet.ChannelID,
		ThumbnailURL: youtubeBestThumbnail(v.Snippet.Thumbnails),
		Privacy:      v.Status.PrivacyStatus,
		UploadStatus: v.Status.UploadStatus,
	}, nil
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

// YouTube's Data API encodes statistics counters as JSON strings (for
// example, "viewCount": "123"), while fixtures and some compatible API
// implementations may emit JSON numbers. Accept both wire formats so a
// valid OAuth callback cannot fail while discovering the user's channel.
func decodeYouTubeCount(raw json.RawMessage) (int64, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, nil
	}
	if raw[0] == '"' {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return 0, err
		}
		return strconv.ParseInt(value, 10, 64)
	}
	var value int64
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, err
	}
	return value, nil
}
