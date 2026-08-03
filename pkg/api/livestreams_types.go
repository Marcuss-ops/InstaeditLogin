package api

import (
	"context"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// LivestreamStore is the persistence contract the livestream CRUD
// handlers depend on. The API keeps this contract local so handler
// tests can use an in-memory fake while production wires the SQL
// repository (internal/repository.LivestreamRepository declares an
// identical interface for its own consumers).
type LivestreamStore interface {
	Create(ctx context.Context, ls *models.Livestream) error
	FindByID(ctx context.Context, id string) (*models.Livestream, error)
	ListByWorkspace(ctx context.Context, workspaceID int64) ([]models.Livestream, error)
	Update(ctx context.Context, ls *models.Livestream) error
	Delete(ctx context.Context, id string) error
}

// createLivestreamRequest is the body accepted by
// POST /api/v1/livestreams. It creates a live CONFIGURATION row in
// state draft — it does not start a broadcast (that is the prepare /
// start control surface).
type createLivestreamRequest struct {
	WorkspaceID       int64   `json:"workspace_id"`
	PlatformAccountID int64   `json:"platform_account_id"`
	Title             string  `json:"title"`
	Description       string  `json:"description,omitempty"`
	PrivacyStatus     string  `json:"privacy_status"`
	PlaybackMode      string  `json:"playback_mode"`
	ScheduleType      string  `json:"schedule_type"`
	ScheduledStartAt  *string `json:"scheduled_start_at,omitempty"` // RFC3339
	Resolution        string  `json:"resolution,omitempty"`
	FrameRate         int     `json:"frame_rate,omitempty"`
	AutoRestart       *bool   `json:"auto_restart,omitempty"`
	// YouTube broadcast metadata (wizard step 2). All optional with
	// documented defaults; the worker maps them onto the liveBroadcast
	// resource at prepare time.
	Category          string  `json:"category,omitempty"`
	MadeForKids       *bool   `json:"made_for_kids,omitempty"`
	Language          string  `json:"language,omitempty"`
	ThumbnailMediaID  *string `json:"thumbnail_media_id,omitempty"`
	DVREnabled        *bool   `json:"dvr_enabled,omitempty"`
	AutoStart         *bool   `json:"auto_start,omitempty"`
	AutoStop          *bool   `json:"auto_stop,omitempty"`
	LatencyPreference string  `json:"latency_preference,omitempty"`
}

// patchLivestreamRequest is the body accepted by
// PATCH /api/v1/livestreams/{id}. Every field is a pointer so absent
// fields stay untouched (read-modify-write partial update; the write
// is last-writer-wins per column).
//
// scheduled_start_at semantics: omit to leave it unchanged, send an
// EMPTY string ("") to clear it, or a valid RFC3339 timestamp to set
// it. JSON null is treated as "not provided" (same as omitting it).
type patchLivestreamRequest struct {
	Title            *string `json:"title,omitempty"`
	Description      *string `json:"description,omitempty"`
	PrivacyStatus    *string `json:"privacy_status,omitempty"`
	PlaybackMode     *string `json:"playback_mode,omitempty"`
	ScheduleType     *string `json:"schedule_type,omitempty"`
	ScheduledStartAt *string `json:"scheduled_start_at,omitempty"` // RFC3339; "" clears
	Resolution       *string `json:"resolution,omitempty"`
	FrameRate        *int    `json:"frame_rate,omitempty"`
	AutoRestart      *bool   `json:"auto_restart,omitempty"`
	// YouTube broadcast metadata (wizard step 2). thumbnail_media_id
	// semantics mirror scheduled_start_at: omit to leave unchanged,
	// send "" to clear, or a media asset id to set.
	Category          *string `json:"category,omitempty"`
	MadeForKids       *bool   `json:"made_for_kids,omitempty"`
	Language          *string `json:"language,omitempty"`
	ThumbnailMediaID  *string `json:"thumbnail_media_id,omitempty"` // "" clears
	DVREnabled        *bool   `json:"dvr_enabled,omitempty"`
	AutoStart         *bool   `json:"auto_start,omitempty"`
	AutoStop          *bool   `json:"auto_stop,omitempty"`
	LatencyPreference *string `json:"latency_preference,omitempty"`
	// desired_state / actual_state are worker-owned; a request that
	// attempts to set them is rejected by the handler.
	DesiredState *string `json:"desired_state,omitempty"`
	ActualState  *string `json:"actual_state,omitempty"`
}

// livestreamResponse is the public shape of a livestream row. The
// YouTube resource references (broadcast/stream ids) and the stream
// name/key are deliberately excluded — the stream name is a technical
// credential that must never reach the frontend. ChannelName is the
// display name of the bound YouTube channel, resolved by the handlers
// from the platform account (empty when the account cannot be loaded).
type livestreamResponse struct {
	ID                string     `json:"id"`
	WorkspaceID       int64      `json:"workspace_id"`
	PlatformAccountID int64      `json:"platform_account_id"`
	ChannelName       string     `json:"channel_name"`
	Title             string     `json:"title"`
	Description       string     `json:"description"`
	PrivacyStatus     string     `json:"privacy_status"`
	PlaybackMode      string     `json:"playback_mode"`
	ScheduleType      string     `json:"schedule_type"`
	ScheduledStartAt  *time.Time `json:"scheduled_start_at,omitempty"`
	DesiredState      string     `json:"desired_state"`
	ActualState       string     `json:"actual_state"`
	Resolution        string     `json:"resolution"`
	FrameRate         int        `json:"frame_rate"`
	AutoRestart       bool       `json:"auto_restart"`
	Category          string     `json:"category"`
	MadeForKids       bool       `json:"made_for_kids"`
	Language          string     `json:"language"`
	ThumbnailMediaID  *string    `json:"thumbnail_media_id,omitempty"`
	DVREnabled        bool       `json:"dvr_enabled"`
	AutoStart         bool       `json:"auto_start"`
	AutoStop          bool       `json:"auto_stop"`
	LatencyPreference string     `json:"latency_preference"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

type listLivestreamsResponse struct {
	Items []livestreamResponse `json:"items"`
}

// livestreamChannelResponse is the per-channel preflight row for the
// creation wizard (GET /api/v1/livestreams/channels). OAuthReady and
// LiveEnabled are derived from the persisted grant: LiveEnabled means
// the grant carries a YouTube live scope (youtube or youtube.force-ssl),
// which is the necessary condition for the Live Streaming API — the
// authoritative liveStreamingNotEnabled check still happens on
// broadcast creation. LastVerifiedAt is the account's last_validated_at.
type livestreamChannelResponse struct {
	PlatformAccountID int64        `json:"platform_account_id"`
	Username          string       `json:"username"`
	PlatformUserID    string       `json:"platform_user_id"`
	AccountState      AccountState `json:"account_state"`
	OAuthReady        bool         `json:"oauth_ready"`
	LiveEnabled       bool         `json:"live_enabled"`
	LastVerifiedAt    *time.Time   `json:"last_verified_at,omitempty"`
	ActiveLives       int          `json:"active_lives"`
}

type listLivestreamChannelsResponse struct {
	Channels []livestreamChannelResponse `json:"channels"`
}

func toLivestreamResponse(ls *models.Livestream) livestreamResponse {
	if ls == nil {
		return livestreamResponse{}
	}
	return livestreamResponse{
		ID:                ls.ID,
		WorkspaceID:       ls.WorkspaceID,
		PlatformAccountID: ls.PlatformAccountID,
		Title:             ls.Title,
		Description:       ls.Description,
		PrivacyStatus:     ls.PrivacyStatus,
		PlaybackMode:      ls.PlaybackMode,
		ScheduleType:      ls.ScheduleType,
		ScheduledStartAt:  ls.ScheduledStartAt,
		DesiredState:      ls.DesiredState,
		ActualState:       ls.ActualState,
		Resolution:        ls.Resolution,
		FrameRate:         ls.FrameRate,
		AutoRestart:       ls.AutoRestart,
		Category:          ls.Category,
		MadeForKids:       ls.MadeForKids,
		Language:          ls.Language,
		ThumbnailMediaID:  ls.ThumbnailMediaID,
		DVREnabled:        ls.DVREnabled,
		AutoStart:         ls.AutoStart,
		AutoStop:          ls.AutoStop,
		LatencyPreference: ls.LatencyPreference,
		CreatedAt:         ls.CreatedAt,
		UpdatedAt:         ls.UpdatedAt,
	}
}
