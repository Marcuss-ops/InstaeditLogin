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

	// Latency preference (liveBroadcast.contentDetails.latencyPreference).
	LivestreamLatencyNormal   = "normal"
	LivestreamLatencyLow      = "low"
	LivestreamLatencyUltraLow = "ultraLow"

	// LivestreamTitleMaxRunes bounds the title (YouTube titles are
	// capped at 100 characters).
	LivestreamTitleMaxRunes = 100
	// LivestreamDescriptionMaxRunes bounds the description.
	LivestreamDescriptionMaxRunes = 5000
	// LivestreamLanguageMaxRunes bounds the language code (ISO 639-1 /
	// BCP-47 sanity gate; well-formed codes are < 12 chars).
	LivestreamLanguageMaxRunes = 35
)

// livestreamLatencies is the allowed latency_preference alphabet.
var livestreamLatencies = map[string]struct{}{
	LivestreamLatencyNormal:   {},
	LivestreamLatencyLow:      {},
	LivestreamLatencyUltraLow: {},
}

// ValidLivestreamLatency reports whether s is a known latency
// preference.
func ValidLivestreamLatency(s string) bool {
	_, ok := livestreamLatencies[s]
	return ok
}

// livestreamCategories is the known YouTube video category id set
// (videoCategories.list, default global region). The empty string
// ("no category") is always allowed; YouTube remains the authority
// at broadcast creation time, so unknown ids are only caught by the
// API rather than by this local gate.
var livestreamCategories = map[string]struct{}{
	"1": {}, "2": {}, "10": {}, "15": {}, "17": {}, "18": {}, "19": {}, "20": {},
	"21": {}, "22": {}, "23": {}, "24": {}, "25": {}, "26": {}, "27": {}, "28": {},
	"29": {}, "30": {}, "31": {}, "32": {}, "33": {}, "34": {}, "35": {}, "36": {},
	"37": {}, "38": {}, "39": {}, "40": {}, "41": {}, "42": {}, "43": {}, "44": {},
}

// ValidLivestreamCategory reports whether s is a known YouTube video
// category id.
func ValidLivestreamCategory(s string) bool {
	_, ok := livestreamCategories[s]
	return ok
}

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

	DesiredState         string
	ActualState          string
	DesiredGeneration    int64
	ConfigurationVersion int64

	// YouTube resource references; filled by the worker once the
	// broadcast/stream are created. streamName / stream key are NEVER
	// persisted here (security: retrievable from YouTube or encrypted,
	// redacted in logs).
	YouTubeBroadcastID string
	YouTubeStreamID    string

	Resolution  string
	FrameRate   int
	AutoRestart bool

	// YouTube broadcast metadata (wizard step 2, migration 091).
	// Category is the YouTube numeric category id ("" = none);
	// Language is an ISO 639-1 code ("" = none); ThumbnailMediaID is
	// the media_assets.id of the uploaded cover (nil = none). These
	// map onto liveBroadcast snippet/status/contentDetails at prepare
	// time — the worker is the only writer of the state columns, but
	// these metadata columns are operator-owned.
	Category          string
	MadeForKids       bool
	Language          string
	ThumbnailMediaID  *string
	DVREnabled        bool
	AutoStart         bool
	AutoStop          bool
	LatencyPreference string

	CreatedAt time.Time
	UpdatedAt time.Time
}
