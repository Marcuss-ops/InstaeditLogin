package models

import "time"

type YouTubeThumbnailBatch struct {
	ID             string     `json:"batch_id"`
	WorkspaceID    int64      `json:"workspace_id"`
	GroupID        int64      `json:"group_id"`
	IdempotencyKey string     `json:"-"`
	RequestHash    []byte     `json:"-"`
	Status         string     `json:"status"`
	Total          int        `json:"total"`
	Completed      int        `json:"completed"`
	Failed         int        `json:"failed"`
	LastError      string     `json:"last_error,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	StartedAt      *time.Time `json:"started_at,omitempty"`
	FinishedAt     *time.Time `json:"finished_at,omitempty"`
}

type YouTubeThumbnailBatchItem struct {
	ID                int64    `json:"id"`
	BatchID           string   `json:"batch_id"`
	PlatformAccountID int64    `json:"platform_account_id"`
	YouTubeVideoID    string   `json:"youtube_video_id"`
	VariantID         string   `json:"variant_id"`
	ThumbnailMediaID  string   `json:"-"`
	Title             string   `json:"title,omitempty"`
	Description       string   `json:"description,omitempty"`
	Tags              []string `json:"tags,omitempty"`
	Status            string   `json:"status"`
	EditorSessionID   string   `json:"editor_session_id,omitempty"`
	PublicURL         string   `json:"public_url,omitempty"`
	LastError         string   `json:"last_error,omitempty"`
}
