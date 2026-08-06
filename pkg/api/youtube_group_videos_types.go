package api

import "time"

// groupYouTubeVideosFanoutConcurrency bounds the number of concurrent
// YouTube `channels.list → playlistItems.list → videos.list` chains
// performed per request. Empirically YouTube tolerates ~10
// simultaneous requests per access token, but the dark-editor dashboard
// renders this endpoint on every click — keeping the fan-out at 4
// keeps the upstream pressure at a fraction of the quota while still
// aggregating a group with ~16 channels well under the 5s SLA.
const groupYouTubeVideosFanoutConcurrency = 4

// groupYouTubeVideosPerAccountTimeout caps each per-account YouTube
// fetch so a single slow/stuck channel does not stall the whole
// response (the handler is on the dashboard refresh path).
const groupYouTubeVideosPerAccountTimeout = 15 * time.Second

// groupYouTubeVideosMaxAccounts caps the number of distinct accounts
// a single group request can fan-out against. Defense-in-depth: a
// hostile / misconfigured caller cannot trigger 500+ YouTube calls
// in one click. Adjustable via future config; sized to the largest
// observed customer group (≈80 channels).
const groupYouTubeVideosMaxAccounts = 200

// YouTubeGroupVideosConfig controls the group-video read projection.
// Zero values use the safe defaults below, which keeps test routers and
// older callers compatible while production wiring can tune the limits
// from environment-backed configuration.
type YouTubeGroupVideosConfig struct {
	MaxAccounts     int
	MaxVideos       int
	CacheTTL        time.Duration
	DefaultPageSize int
}

func (c YouTubeGroupVideosConfig) normalized() YouTubeGroupVideosConfig {
	if c.MaxAccounts <= 0 {
		c.MaxAccounts = groupYouTubeVideosMaxAccounts
	}
	if c.MaxVideos <= 0 {
		c.MaxVideos = groupYouTubeVideosMaxTotalVideos
	}
	if c.CacheTTL < 0 {
		c.CacheTTL = 0
	}
	if c.CacheTTL == 0 {
		// Keep private-video reads cached long enough that opening or
		// revisiting a group does not spend YouTube quota repeatedly.
		// Deployments may still override this with the env-backed value.
		c.CacheTTL = 5 * time.Minute
	}
	if c.DefaultPageSize <= 0 {
		c.DefaultPageSize = 50
	}
	if c.DefaultPageSize > c.MaxVideos {
		c.DefaultPageSize = c.MaxVideos
	}
	return c
}

// groupYouTubeVideosMaxTotalVideos caps the aggregated response size
// across all channels. The SPA renders the result as a card grid;
// more than 500 cards exceeds the first-paint budget so the cap
// truncates the response with the most-recent per-channel order
// preserved (YouTube's ListEditableVideos returns uploads-playlist
// order = newest first per channel).
const groupYouTubeVideosMaxTotalVideos = 500

// groupYouTubeVideosPhantomMaxAge bounds how far back the handler
// emits "phantom" entries for published sessions whose YouTube row
// was filtered out (see handleListGroupYouTubeVideos step 7.5).
// Without this cap a long-history channel would saturate the
// response with year-old publishes and push out the current
// editable videos. 90 days covers the typical "I just published
// this week / month" operator workflow without forcing a hard
// expiry for occasional re-edits of older videos.
const groupYouTubeVideosPhantomMaxAge = 90 * 24 * time.Hour

// groupYouTubeVideoEntry is the per-row JSON shape returned by GET
// /api/v1/groups/{group_id}/youtube/videos. The shape mirrors
// models.YouTubeVideoDetails (from YouTube list) joined with the
// existing per-video editor_session row (if any). Fields are
// optional/omitempty so the same DTO can carry:
//   - "freshly discovered" videos (no editor_session yet) — all
//     editor_* fields omitted;
//   - "in editing" videos (session.status='editing' or 'failed');
//   - "published" videos (session.status='published').
//
// youtube_sync_status is the YouTube-side read projection the SPA
// uses to colour the privacy badge — for now it's always
// "unconfirmed" until the reconciler-side sync lands. The DTO
// already carries the field so the SPA implements the badge logic
// once and the reconciler can flip the value remotely.
//
// LIVE PROJECTION (P0#7): actual_privacy and youtube_sync_status are
// now stamped by the publish orchestrator's read-back
// (MarkPublishedWithActualPrivacy) the moment a publish completes,
// and refreshed by the drift_reconciler on every periodic sweep.
// The SPA's privacy badge + "Syncing with YouTube…" copy is wired
// against these fields. The DTO comment block above describes the
// field semantics; the mapping block (below "sessionMap lookup")
// projects the live fields straight onto the response.
type groupYouTubeVideoEntry struct {
	YouTubeVideoID    string     `json:"youtube_video_id"`
	Title             string     `json:"title"`
	Description       string     `json:"description,omitempty"`
	ThumbnailURL      string     `json:"thumbnail_url"`
	PrivacyStatus     string     `json:"privacy_status"`
	ProcessingStatus  string     `json:"processing_status"`
	PublishedAt       *time.Time `json:"published_at,omitempty"`
	PlatformAccountID int64      `json:"platform_account_id"`
	ChannelName       string     `json:"channel_name"`
	Language          string     `json:"language,omitempty"`
	// Editor session fields. All three are omitted when no
	// youtube_video_edits row exists yet for this (account, video)
	// tuple — that means the user hasn't opened the editor yet and
	// the SPA will route the click to POST /editor-sessions.
	EditorSessionID *string `json:"editor_session_id,omitempty"`
	VeloxProjectID  *string `json:"velox_project_id,omitempty"`
	EditorURL       *string `json:"editor_url,omitempty"`
	// EditorStatus: "editing" | "failed" | "publishing" | "published"
	// when a session exists, else "ready" (no session yet).
	EditorStatus     string  `json:"editor_status"`
	DraftDescription *string `json:"draft_description,omitempty"`
	// DesiredPrivacy: what the operator chose on the editor's "Pubblica"
	// panel (publish flow will use it). Empty when no session exists.
	DesiredPrivacy string `json:"desired_privacy,omitempty"`
	// PublishAt: scheduled time selected by the operator. Keeping it
	// in the group projection lets the UI distinguish an intentionally
	// private scheduled video from a private video that was never
	// published.
	PublishAt *time.Time `json:"publish_at,omitempty"`
	// ActualPrivacy: what YouTube's videos.list confirmed right after
	// our publish call, projected by the P0#7 read-back
	// (MarkPublishedWithActualPrivacy). Pointer-to-string so the
	// SPA can distinguish "we did read back and got X" from "we
	// haven't read back yet" (nil → null in JSON). The drift_reconciler
	// refreshes both fields on its periodic sweep.
	ActualPrivacy *string `json:"actual_privacy,omitempty"`
	// YouTubeSyncStatus: lifecycle marker stamped by the publish
	// orchestrator (confirmed/drift/pending/failed) and refreshed
	// by the drift_reconciler. Same pointer-to-string rationale as
	// ActualPrivacy. Valid values are constrained at the DB layer
	// by the CHECK constraint on youtube_video_edits.youtube_sync_status
	// (migration 072).
	YouTubeSyncStatus *string `json:"youtube_sync_status,omitempty"`
	// Phantom: true when this entry was synthesized from a session
	// row that no longer matches a YouTube row in the per-account
	// fan-out (ListEditableVideos filters out privacy=public).
	// The thumbnail URL points to YouTube's public CDN so the
	// operator gets a visual signal even though we did not query
	// the video's snippet. A deleted video surfaces a grey
	// placeholder thumbnail; that's an acceptable edge case.
	Phantom bool `json:"phantom,omitempty"`
}

// groupYouTubeVideosResponse is the envelope. `videos: []` is
// returned (NOT 404) when no videos match — the SPA's card grid
// renders an empty-state banner rather than treating "nothing to do"
// as an error. `warnings: []` surfaces per-account fetch failures
// so the operator can debug stale-token issues from the UI without
// inspecting server logs.
type groupYouTubeVideosSummary struct {
	// TotalVideos is the number of entries in the bounded aggregate
	// window. Truncated is true when MaxVideos clipped the complete
	// fan-out result; callers must not interpret TotalVideos as an
	// unbounded database/provider total in that case.
	TotalVideos          int     `json:"total_videos"`
	Truncated            bool    `json:"truncated"`
	Accounts             int     `json:"accounts"`
	AccountsWithVideos   int     `json:"accounts_with_videos"`
	FailedAccounts       int     `json:"failed_accounts"`
	InvalidTokenAccounts []int64 `json:"invalid_token_accounts,omitempty"`
}

type groupYouTubeVideosResponse struct {
	Videos     []groupYouTubeVideoEntry  `json:"videos"`
	Summary    groupYouTubeVideosSummary `json:"summary"`
	Warnings   []string                  `json:"warnings,omitempty"`
	Error      string                    `json:"error,omitempty"`
	HasMore    bool                      `json:"has_more,omitempty"`
	NextOffset int                       `json:"next_offset,omitempty"`
	NextCursor string                    `json:"next_cursor,omitempty"`
}
