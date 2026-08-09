package services

import (
	"encoding/json"
	"fmt"
)

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
	ID               string                `json:"id"`
	Snippet          youtubeChannelSnippet `json:"snippet"`
	Statistics       youtubeStatistics     `json:"statistics"`
	ContentDetails   youtubeContentDetails `json:"contentDetails"`
	BrandingSettings youtubeBranding       `json:"brandingSettings"`
}

type youtubeChannelSnippet struct {
	Title       string             `json:"title"`
	Description string             `json:"description"`
	CustomURL   string             `json:"customUrl"`
	Country     string             `json:"country"`
	Thumbnails  *youtubeThumbnails `json:"thumbnails"`
}

type youtubeStatistics struct {
	SubscriberCount       int64 `json:"subscriberCount"`
	HiddenSubscriberCount bool  `json:"hiddenSubscriberCount"`
	ViewCount             int64 `json:"viewCount"`
	VideoCount            int64 `json:"videoCount"`
}

func (s *youtubeStatistics) UnmarshalJSON(data []byte) error {
	var wire struct {
		SubscriberCount       json.RawMessage `json:"subscriberCount"`
		HiddenSubscriberCount bool            `json:"hiddenSubscriberCount"`
		ViewCount             json.RawMessage `json:"viewCount"`
		VideoCount            json.RawMessage `json:"videoCount"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	var err error
	if s.SubscriberCount, err = decodeYouTubeCount(wire.SubscriberCount); err != nil {
		return fmt.Errorf("subscriberCount: %w", err)
	}
	if s.ViewCount, err = decodeYouTubeCount(wire.ViewCount); err != nil {
		return fmt.Errorf("viewCount: %w", err)
	}
	if s.VideoCount, err = decodeYouTubeCount(wire.VideoCount); err != nil {
		return fmt.Errorf("videoCount: %w", err)
	}
	s.HiddenSubscriberCount = wire.HiddenSubscriberCount
	return nil
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
	BannerMobileExtra string `json:"bannerMobileExtraDevicesImageUrl"`
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
	Items         []youtubePlaylistItem `json:"items"`
	NextPageToken string                `json:"nextPageToken"`
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
	Title       string             `json:"title"`
	Description string             `json:"description"`
	PublishedAt string             `json:"publishedAt"`
	ChannelID   string             `json:"channelId"`
	Thumbnails  *youtubeThumbnails `json:"thumbnails"`
}

type youtubeVideoStats struct {
	ViewCount    int64 `json:"viewCount"`
	LikeCount    int64 `json:"likeCount"`
	CommentCount int64 `json:"commentCount"`
}

func (s *youtubeVideoStats) UnmarshalJSON(data []byte) error {
	var wire struct {
		ViewCount    json.RawMessage `json:"viewCount"`
		LikeCount    json.RawMessage `json:"likeCount"`
		CommentCount json.RawMessage `json:"commentCount"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	var err error
	if s.ViewCount, err = decodeYouTubeCount(wire.ViewCount); err != nil {
		return fmt.Errorf("viewCount: %w", err)
	}
	if s.LikeCount, err = decodeYouTubeCount(wire.LikeCount); err != nil {
		return fmt.Errorf("likeCount: %w", err)
	}
	if s.CommentCount, err = decodeYouTubeCount(wire.CommentCount); err != nil {
		return fmt.Errorf("commentCount: %w", err)
	}
	return nil
}

type youtubeVideoContent struct {
	Duration         string                       `json:"duration"`
	LicensedContent  bool                         `json:"licensedContent"`
	RegionRestriction *youtubeRegionRestriction   `json:"regionRestriction,omitempty"`
}

type youtubeRegionRestriction struct {
	Allowed []string `json:"allowed"`
	Blocked []string `json:"blocked"`
}

type youtubeVideoStatus struct {
	PrivacyStatus  string `json:"privacyStatus"`
	UploadStatus   string `json:"uploadStatus"`
	RejectionReason string `json:"rejectionReason"`
	FailureReason   string `json:"failureReason"`
}

type youtubeVideoProcessingDetails struct {
	ProcessingStatus string `json:"processingStatus"`
}

// --- Private ---

type youtubeTokenResponse struct {
	AccessToken           string `json:"access_token"`
	TokenType             string `json:"token_type"`
	ExpiresIn             int64  `json:"expires_in"`
	Scope                 string `json:"scope"`
	RefreshToken          string `json:"refresh_token"`
	RefreshTokenExpiresIn int64  `json:"refresh_token_expires_in"`
}
