package models

import "time"

type YouTubeCopyrightStatus string

const (
	YouTubeCopyrightPending    YouTubeCopyrightStatus = "pending"
	YouTubeCopyrightProcessing YouTubeCopyrightStatus = "processing"
	YouTubeCopyrightClear      YouTubeCopyrightStatus = "clear"
	YouTubeCopyrightClaim      YouTubeCopyrightStatus = "claim"
	YouTubeCopyrightBlocked    YouTubeCopyrightStatus = "blocked"
	YouTubeCopyrightError      YouTubeCopyrightStatus = "error"
)

type YouTubeCopyrightCandidate struct {
	ID                int64
	PlatformAccountID int64
	VideoID           string
}

type YouTubeCopyrightResult struct {
	Status           YouTubeCopyrightStatus
	Message          string
	ProcessingStatus string
	RejectionReason  string
	FailureReason    string
	LicensedContent  bool
	BlockedRegions   []string
	AllowedRegions   []string
}

type YouTubeCopyrightAlert struct {
	ID                int64                  `json:"id"`
	PostID            int64                  `json:"post_id"`
	UploadJobID       int64                  `json:"upload_job_id"`
	PostTargetID      int64                  `json:"post_target_id"`
	PlatformAccountID int64                  `json:"platform_account_id"`
	YouTubeVideoID    string                 `json:"youtube_video_id"`
	Status            YouTubeCopyrightStatus `json:"status"`
	Message           string                 `json:"message"`
	RejectionReason   string                 `json:"rejection_reason,omitempty"`
	FailureReason     string                 `json:"failure_reason,omitempty"`
	ProcessingStatus  string                 `json:"processing_status,omitempty"`
	LicensedContent   bool                   `json:"licensed_content"`
	BlockedRegions    []string               `json:"blocked_regions,omitempty"`
	AllowedRegions    []string               `json:"allowed_regions,omitempty"`
	CheckedAt         *time.Time             `json:"checked_at,omitempty"`
}
