package services

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// YouTubeVideoPage is the paginated read model used by the InstaEdit
// YouTube Studio. Items only contains processed videos that are still
// private or unlisted, because public videos are not eligible for the
// pre-publish thumbnail workflow.
type YouTubeVideoPage struct {
	Items         []models.YouTubeVideoDetails `json:"items"`
	NextPageToken string                       `json:"next_page_token,omitempty"`
}

type youtubeLibraryChannelsResponse struct {
	Items []struct {
		ID             string `json:"id"`
		ContentDetails struct {
			RelatedPlaylists struct {
				Uploads string `json:"uploads"`
			} `json:"relatedPlaylists"`
		} `json:"contentDetails"`
	} `json:"items"`
}

type youtubeLibraryPlaylistResponse struct {
	NextPageToken string `json:"nextPageToken"`
	Items         []struct {
		ContentDetails struct {
			VideoID string `json:"videoId"`
		} `json:"contentDetails"`
	} `json:"items"`
}

type youtubeLibraryVideosResponse struct {
	Items []youtubeLibraryVideo `json:"items"`
}

type youtubeLibraryVideo struct {
	ID      string `json:"id"`
	Snippet struct {
		Title     string `json:"title"`
		ChannelID string `json:"channelId"`
		Thumbnails struct {
			Default youtubeLibraryThumbnail `json:"default"`
			Medium  youtubeLibraryThumbnail `json:"medium"`
			High    youtubeLibraryThumbnail `json:"high"`
			Maxres  youtubeLibraryThumbnail `json:"maxres"`
		} `json:"thumbnails"`
	} `json:"snippet"`
	Status struct {
		PrivacyStatus string `json:"privacyStatus"`
		UploadStatus  string `json:"uploadStatus"`
	} `json:"status"`
}

type youtubeLibraryThumbnail struct {
	URL string `json:"url"`
}

// ListEditableVideos returns one page of processed private/unlisted videos
// belonging to channelID. The uploads playlist preserves channel upload order;
// videos.list enriches the playlist ids with privacy, processing and thumbnail
// metadata without using the expensive search.list endpoint.
func (s *YouTubeOAuthService) ListEditableVideos(ctx context.Context, accessToken, channelID, pageToken string) (*YouTubeVideoPage, error) {
	if s == nil || s.httpClient == nil {
		return nil, fmt.Errorf("youtube video library: service not configured")
	}
	accessToken = strings.TrimSpace(accessToken)
	channelID = strings.TrimSpace(channelID)
	if accessToken == "" {
		return nil, fmt.Errorf("youtube video library: access token is required")
	}
	if channelID == "" {
		return nil, fmt.Errorf("youtube video library: channel id is required")
	}

	channelValues := url.Values{}
	channelValues.Set("part", "contentDetails")
	channelValues.Set("id", channelID)
	channelValues.Set("maxResults", "1")
	var channels youtubeLibraryChannelsResponse
	if err := s.getYouTubeLibraryJSON(ctx, accessToken, "https://www.googleapis.com/youtube/v3/channels?"+channelValues.Encode(), &channels); err != nil {
		return nil, fmt.Errorf("youtube video library channels.list: %w", err)
	}
	if len(channels.Items) == 0 || channels.Items[0].ID != channelID {
		return nil, fmt.Errorf("youtube video library: connected channel not found")
	}
	uploadsPlaylistID := strings.TrimSpace(channels.Items[0].ContentDetails.RelatedPlaylists.Uploads)
	if uploadsPlaylistID == "" {
		return nil, fmt.Errorf("youtube video library: uploads playlist is missing")
	}

	playlistValues := url.Values{}
	playlistValues.Set("part", "contentDetails")
	playlistValues.Set("playlistId", uploadsPlaylistID)
	playlistValues.Set("maxResults", "25")
	if strings.TrimSpace(pageToken) != "" {
		playlistValues.Set("pageToken", strings.TrimSpace(pageToken))
	}
	var playlist youtubeLibraryPlaylistResponse
	if err := s.getYouTubeLibraryJSON(ctx, accessToken, "https://www.googleapis.com/youtube/v3/playlistItems?"+playlistValues.Encode(), &playlist); err != nil {
		return nil, fmt.Errorf("youtube video library playlistItems.list: %w", err)
	}

	orderedIDs := make([]string, 0, len(playlist.Items))
	for _, item := range playlist.Items {
		if id := strings.TrimSpace(item.ContentDetails.VideoID); id != "" {
			orderedIDs = append(orderedIDs, id)
		}
	}
	page := &YouTubeVideoPage{Items: []models.YouTubeVideoDetails{}, NextPageToken: playlist.NextPageToken}
	if len(orderedIDs) == 0 {
		return page, nil
	}

	videoValues := url.Values{}
	videoValues.Set("part", "snippet,status")
	videoValues.Set("id", strings.Join(orderedIDs, ","))
	videoValues.Set("maxResults", "25")
	var videos youtubeLibraryVideosResponse
	if err := s.getYouTubeLibraryJSON(ctx, accessToken, "https://www.googleapis.com/youtube/v3/videos?"+videoValues.Encode(), &videos); err != nil {
		return nil, fmt.Errorf("youtube video library videos.list: %w", err)
	}

	byID := make(map[string]youtubeLibraryVideo, len(videos.Items))
	for _, video := range videos.Items {
		byID[video.ID] = video
	}
	for _, id := range orderedIDs {
		video, ok := byID[id]
		if !ok || video.Snippet.ChannelID != channelID {
			continue
		}
		privacy := strings.ToLower(strings.TrimSpace(video.Status.PrivacyStatus))
		uploadStatus := strings.ToLower(strings.TrimSpace(video.Status.UploadStatus))
		if privacy == "public" || (privacy != "private" && privacy != "unlisted") || uploadStatus != "processed" {
			continue
		}
		thumbnailURL := video.Snippet.Thumbnails.Maxres.URL
		if thumbnailURL == "" {
			thumbnailURL = video.Snippet.Thumbnails.High.URL
		}
		if thumbnailURL == "" {
			thumbnailURL = video.Snippet.Thumbnails.Medium.URL
		}
		if thumbnailURL == "" {
			thumbnailURL = video.Snippet.Thumbnails.Default.URL
		}
		page.Items = append(page.Items, models.YouTubeVideoDetails{
			ID:           video.ID,
			Title:        video.Snippet.Title,
			ChannelID:    video.Snippet.ChannelID,
			ThumbnailURL: thumbnailURL,
			Privacy:      privacy,
			UploadStatus: uploadStatus,
		})
	}
	return page, nil
}

func (s *YouTubeOAuthService) getYouTubeLibraryJSON(ctx context.Context, accessToken, endpoint string, destination interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if err := json.Unmarshal(body, destination); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}
