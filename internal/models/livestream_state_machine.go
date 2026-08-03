package models

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// LivestreamDesiredState is operator intent. It deliberately contains no
// worker observations; the worker reconciles this intent into a run's actual
// state.
type LivestreamDesiredState string

const (
	LivestreamDesiredDraft     LivestreamDesiredState = "draft"
	LivestreamDesiredPrepared  LivestreamDesiredState = "prepared"
	LivestreamDesiredRunning   LivestreamDesiredState = "running"
	LivestreamDesiredStopped   LivestreamDesiredState = "stopped"
	LivestreamDesiredCancelled LivestreamDesiredState = "cancelled"
)

func (s LivestreamDesiredState) Valid() bool {
	switch s {
	case LivestreamDesiredDraft, LivestreamDesiredPrepared,
		LivestreamDesiredRunning, LivestreamDesiredStopped,
		LivestreamDesiredCancelled:
		return true
	default:
		return false
	}
}

// LivestreamActualState is worker-observed truth for a run. It is separate
// from LivestreamDesiredState so, for example, running/reconnecting is a
// valid and visible intermediate condition.
type LivestreamActualState string

const (
	LivestreamActualDraft            LivestreamActualState = "draft"
	LivestreamActualPreflighting     LivestreamActualState = "preflighting"
	LivestreamActualPreparing        LivestreamActualState = "preparing"
	LivestreamActualReady            LivestreamActualState = "ready"
	LivestreamActualScheduled        LivestreamActualState = "scheduled"
	LivestreamActualStarting         LivestreamActualState = "starting"
	LivestreamActualWaitingForIngest LivestreamActualState = "waiting_for_ingest"
	LivestreamActualTesting          LivestreamActualState = "testing"
	LivestreamActualLive             LivestreamActualState = "live"
	LivestreamActualDegraded         LivestreamActualState = "degraded"
	LivestreamActualReconnecting     LivestreamActualState = "reconnecting"
	LivestreamActualStopping         LivestreamActualState = "stopping"
	LivestreamActualCompleted        LivestreamActualState = "completed"
	LivestreamActualFailed           LivestreamActualState = "failed"
	LivestreamActualCancelled        LivestreamActualState = "cancelled"
)

func (s LivestreamActualState) Valid() bool {
	switch s {
	case LivestreamActualDraft, LivestreamActualPreflighting,
		LivestreamActualPreparing, LivestreamActualReady, LivestreamActualScheduled,
		LivestreamActualStarting, LivestreamActualWaitingForIngest, LivestreamActualTesting,
		LivestreamActualLive, LivestreamActualDegraded, LivestreamActualReconnecting,
		LivestreamActualStopping, LivestreamActualCompleted, LivestreamActualFailed,
		LivestreamActualCancelled:
		return true
	default:
		return false
	}
}

var desiredTransitions = map[LivestreamDesiredState]map[LivestreamDesiredState]struct{}{
	LivestreamDesiredDraft: {
		LivestreamDesiredPrepared: {}, LivestreamDesiredRunning: {},
		LivestreamDesiredStopped: {}, LivestreamDesiredCancelled: {},
	},
	LivestreamDesiredPrepared: {
		LivestreamDesiredDraft: {}, LivestreamDesiredRunning: {},
		LivestreamDesiredStopped: {}, LivestreamDesiredCancelled: {},
	},
	LivestreamDesiredRunning: {
		LivestreamDesiredStopped: {}, LivestreamDesiredCancelled: {},
	},
	LivestreamDesiredStopped: {
		LivestreamDesiredRunning: {}, LivestreamDesiredDraft: {},
		LivestreamDesiredCancelled: {},
	},
	LivestreamDesiredCancelled: {},
}

var actualTransitions = map[LivestreamActualState]map[LivestreamActualState]struct{}{
	LivestreamActualDraft: {
		LivestreamActualPreflighting: {}, LivestreamActualPreparing: {},
		LivestreamActualCancelled: {}, LivestreamActualFailed: {},
	},
	LivestreamActualPreflighting: {
		LivestreamActualPreparing: {}, LivestreamActualFailed: {}, LivestreamActualCancelled: {},
	},
	LivestreamActualPreparing: {
		LivestreamActualReady: {}, LivestreamActualFailed: {}, LivestreamActualCancelled: {},
	},
	LivestreamActualReady: {
		LivestreamActualStarting: {}, LivestreamActualStopping: {},
		LivestreamActualFailed: {}, LivestreamActualCancelled: {},
	},
	LivestreamActualScheduled: {
		LivestreamActualStarting: {}, LivestreamActualStopping: {}, LivestreamActualCancelled: {},
	},
	LivestreamActualStarting: {
		LivestreamActualWaitingForIngest: {}, LivestreamActualReconnecting: {},
		LivestreamActualStopping: {}, LivestreamActualFailed: {},
	},
	LivestreamActualWaitingForIngest: {
		LivestreamActualTesting: {}, LivestreamActualReconnecting: {},
		LivestreamActualStopping: {}, LivestreamActualFailed: {},
	},
	LivestreamActualTesting: {
		LivestreamActualLive: {}, LivestreamActualDegraded: {},
		LivestreamActualReconnecting: {}, LivestreamActualStopping: {}, LivestreamActualFailed: {},
	},
	LivestreamActualLive: {
		LivestreamActualDegraded: {}, LivestreamActualReconnecting: {},
		LivestreamActualStopping: {}, LivestreamActualCompleted: {}, LivestreamActualFailed: {},
	},
	LivestreamActualDegraded: {
		LivestreamActualLive: {}, LivestreamActualReconnecting: {},
		LivestreamActualStopping: {}, LivestreamActualCompleted: {}, LivestreamActualFailed: {},
	},
	LivestreamActualReconnecting: {
		LivestreamActualStarting: {}, LivestreamActualWaitingForIngest: {},
		LivestreamActualTesting: {}, LivestreamActualLive: {}, LivestreamActualDegraded: {},
		LivestreamActualStopping: {}, LivestreamActualFailed: {},
	},
	LivestreamActualStopping: {
		LivestreamActualCompleted: {}, LivestreamActualFailed: {}, LivestreamActualCancelled: {},
	},
	LivestreamActualFailed: {
		LivestreamActualPreflighting: {}, LivestreamActualPreparing: {},
		LivestreamActualCancelled: {},
	},
	LivestreamActualCompleted: {},
	LivestreamActualCancelled: {},
}

// CanTransitionDesiredState reports whether an operator command is valid.
func CanTransitionDesiredState(from, to LivestreamDesiredState) bool {
	if !from.Valid() || !to.Valid() || from == to {
		return false
	}
	_, ok := desiredTransitions[from][to]
	return ok
}

// CanTransitionActualState reports whether a worker observation is a valid
// state-machine edge. Recovery edges are explicit rather than an unrestricted
// "any state to any state" escape hatch.
func CanTransitionActualState(from, to LivestreamActualState) bool {
	if !from.Valid() || !to.Valid() || from == to {
		return false
	}
	_, ok := actualTransitions[from][to]
	return ok
}

var (
	ErrInvalidLivestreamDesiredTransition = errors.New("invalid livestream desired-state transition")
	ErrInvalidLivestreamActualTransition  = errors.New("invalid livestream actual-state transition")
	ErrLivestreamConfigurationStale       = errors.New("stale livestream configuration version")
	ErrLivestreamDesiredGenerationStale   = errors.New("stale livestream desired generation")
)

// LivestreamEvent is the append-only audit record for state/reconciler
// actions. Payload is JSON object data and must never contain OAuth tokens or
// ingest secrets.
type LivestreamEvent struct {
	ID           int64           `json:"id"`
	LivestreamID string          `json:"livestream_id"`
	RunID        *string         `json:"run_id,omitempty"`
	EventType    string          `json:"event_type"`
	Severity     string          `json:"severity"`
	Payload      json.RawMessage `json:"payload"`
	CreatedAt    time.Time       `json:"created_at"`
}

const (
	LivestreamEventRunCreated              = "run_created"
	LivestreamEventRunLeased               = "run_leased"
	LivestreamEventOAuthRefreshed          = "oauth_refreshed"
	LivestreamEventStreamCreated           = "stream_created"
	LivestreamEventYouTubeStreamCreated    = "youtube_stream_created"
	LivestreamEventBroadcastCreated        = "broadcast_created"
	LivestreamEventYouTubeBroadcastCreated = "youtube_broadcast_created"
	LivestreamEventBroadcastBound          = "broadcast_bound"
	LivestreamEventEncoderStarted          = "encoder_started"
	LivestreamEventIngestActive            = "ingest_active"
	LivestreamEventBroadcastLive           = "broadcast_live"
	LivestreamEventHealthWarning           = "health_warning"
	LivestreamEventHealthDegraded          = "health_degraded"
	LivestreamEventEncoderRestarted        = "encoder_restarted"
	LivestreamEventBroadcastCompleted      = "broadcast_completed"
	LivestreamEventRunFailed               = "run_failed"
	LivestreamEventHeartbeatLost           = "heartbeat_lost"
)

var validLivestreamEventSeverities = map[string]struct{}{
	"info": {}, "warning": {}, "error": {}, "critical": {},
}

func ValidateLivestreamEvent(event *LivestreamEvent) error {
	if event == nil || event.LivestreamID == "" || event.EventType == "" {
		return errors.New("livestream event requires livestream ID and event type")
	}
	if _, ok := validLivestreamEventSeverities[event.Severity]; !ok {
		return fmt.Errorf("invalid livestream event severity %q", event.Severity)
	}
	if len(event.Payload) == 0 {
		event.Payload = json.RawMessage(`{}`)
	}
	var object map[string]any
	if err := json.Unmarshal(event.Payload, &object); err != nil || object == nil {
		return errors.New("livestream event payload must be a JSON object")
	}
	return nil
}
