package models

import "time"

// Livestream configuration + state machine constants.
//
// The state machine is split into desired_state (operator intent) and
// actual_state (observed truth). The livestream worker continuously
// reconciles actual toward desired; CRUD handlers only ever create
// rows in 'draft' and never write these columns directly.
const (
	LivestreamStateDraft            = "draft"
	LivestreamStatePreparing        = "preparing"
	LivestreamStateReady            = "ready"
	LivestreamStateScheduled        = "scheduled"
	LivestreamStateStarting         = "starting"
	LivestreamStateWaitingForIngest = "waiting_for_ingest"
	LivestreamStateTesting          = "testing"
	LivestreamStateLive             = "live"
	LivestreamStateDegraded         = "degraded"
	LivestreamStateReconnecting     = "reconnecting"
	LivestreamStateStopping         = "stopping"
	LivestreamStateCompleted        = "completed"
	LivestreamStateFailed           = "failed"
	LivestreamStateCancelled        = "cancelled"
)

// livestreamStates is the full allowed state-machine alphabet. Both
// desired_state and actual_state draw from the same set.
var livestreamStates = map[string]struct{}{
	LivestreamStateDraft:            {},
	LivestreamStatePreparing:        {},
	LivestreamStateReady:            {},
	LivestreamStateScheduled:        {},
	LivestreamStateStarting:         {},
	LivestreamStateWaitingForIngest: {},
	LivestreamStateTesting:          {},
	LivestreamStateLive:             {},
	LivestreamStateDegraded:         {},
	LivestreamStateReconnecting:     {},
	LivestreamStateStopping:         {},
	LivestreamStateCompleted:        {},
	LivestreamStateFailed:           {},
	LivestreamStateCancelled:        {},
}

// ValidLivestreamState reports whether s is a member of the livestream
// state-machine alphabet.
func ValidLivestreamState(s string) bool {
	_, ok := livestreamStates[s]
	return ok
}

// Livestream privacy / playback / scheduling / encoding enums.
const (
	LivestreamPrivacyPrivate  = "private"
	LivestreamPrivacyUnlisted = "unlisted"
	LivestreamPrivacyPublic   = "public"

	LivestreamPlaybackLoopContinuous = "loop_continuous"
	LivestreamPlaybackPlayOnce       = "play_once"

	LivestreamScheduleManual    = "manual"
	LivestreamScheduleNow       = "now"
	LivestreamScheduleScheduled = "scheduled"
	LivestreamScheduleRecurring = "recurring"

	LivestreamResolution720p  = "720p30"
	LivestreamResolution1080p = "1080p30"

	LivestreamFrameRate = 30

	// LivestreamTitleMaxRunes bounds the title (YouTube titles are
	// capped at 100 characters).
	LivestreamTitleMaxRunes = 100
	// LivestreamDescriptionMaxRunes bounds the description.
	LivestreamDescriptionMaxRunes = 5000
)

// Livestream is the persisted YouTube live configuration row. The
// worker-owned state columns are included so a single row is the full
// reconciliation record (desired vs actual).
type Livestream struct {
	ID                string
	WorkspaceID       int64
	PlatformAccountID int64
	CreatedBy         int64

	Title            string
	Description      string
	PrivacyStatus    string
	PlaybackMode     string
	ScheduleType     string
	ScheduledStartAt *time.Time

	DesiredState string
	ActualState  string

	// YouTube resource references; filled by the worker once the
	// broadcast/stream are created. streamName / stream key are NEVER
	// persisted here (security: retrievable from YouTube or encrypted,
	// redacted in logs).
	YouTubeBroadcastID string
	YouTubeStreamID    string

	Resolution  string
	FrameRate   int
	AutoRestart bool

	CreatedAt time.Time
	UpdatedAt time.Time
}
