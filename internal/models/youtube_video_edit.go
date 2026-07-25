package models

import "time"

// YouTubeVideoEdit persists an InstaEdit-managed thumbnail editing
// session for a specific YouTube video. It links the InstaEdit
// workspace/account, the YouTube video, and the Velox dark-editor
// project used to produce the thumbnail.
type YouTubeVideoEdit struct {
	ID                 string     `json:"id"`
	WorkspaceID        int64      `json:"workspace_id"`
	PlatformAccountID  int64      `json:"platform_account_id"`
	YouTubeVideoID     string     `json:"youtube_video_id"`
	VeloxProjectID     string     `json:"velox_project_id"`
	SourceThumbnailURL string     `json:"source_thumbnail_url,omitempty"`
	ThumbnailMediaID   *int64     `json:"thumbnail_media_id,omitempty"`
	DesiredPrivacy     string     `json:"desired_privacy"`
	PublishAt          *time.Time `json:"publish_at,omitempty"`
	Status               string     `json:"status"`
	LastError          string     `json:"last_error,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

// YouTubeVideoDetails is the narrow view of a YouTube video returned
// by the YouTubeOAuthService when validating a video before an editor
// session is created.
type YouTubeVideoDetails struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	ChannelID    string `json:"channel_id"`
	ThumbnailURL string `json:"thumbnail_url,omitempty"`
	Privacy      string `json:"privacy"`
	UploadStatus string `json:"upload_status"`
}
