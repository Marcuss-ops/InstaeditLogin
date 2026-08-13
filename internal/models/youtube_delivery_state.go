package models

import "sort"

// YouTubeDeliveryState is the 17-value lifecycle enum for the atomic
// YouTube delivery — the canonical `state` column on
// youtube_target_publications added by migration 125. It is the single
// operational cursor for ONE video on ONE channel, while the existing
// youtube_upload_status / youtube_processing_status / thumbnail_status
// columns remain per-phase observations.
//
// Happy path:
//
//	preflight → ready_to_upload → uploading → youtube_uploaded
//	    → processing → thumbnail_pending → thumbnail_ready → scheduled
//	    → published → verified
//	(processing may skip the thumbnail branch straight to scheduled)
//
// Side states (parallel to the main flow, all carrying a resume_state
// so the worker knows where to return once the blocking condition clears):
//
//	retry_wait         — transient failure, exponential backoff
//	quota_wait         — real quota gate denied a call mid-run
//	blocked_auth       — OAuth grant invalid; re-auth required
//	copyright_review   — copyright restriction needs operator review
//	processing_stuck   — YouTube processing exceeded the stuck threshold
//	failed             — terminal non-recoverable (validation, media)
//	dead_letter        — retry budget exhausted; operator retry only
//
// ─── ON-CALL DBA CAVEAT (read this BEFORE issuing a direct-SQL UPDATE) ───
//
// The Go-layer guard (CanTransitionTo + youtubeDeliveryTransitionMap) is
// BYPASSED when you issue a raw `UPDATE youtube_target_publications SET
// state = '...'` via psql. The model layer never runs; there is no SQL
// CHECK on the value set (matching the table's TEXT-status convention).
// Direct-SQL repairs carry the operator's FULL responsibility: review the
// transition map comments below AND docs/OPERATIONS.md before any UPDATE.
type YouTubeDeliveryState string

const (
	// YouTubeDeliveryPreflight — materializer created the delivery; the
	// worker validates media/account/OAuth/channel/title/privacy/publish_at
	// and reserves capacity before any videos.insert.
	YouTubeDeliveryPreflight YouTubeDeliveryState = "preflight"
	// YouTubeDeliveryReadyToUpload — preflight passed; the worker claims
	// the row and may begin the upload.
	YouTubeDeliveryReadyToUpload YouTubeDeliveryState = "ready_to_upload"
	// YouTubeDeliveryUploading — resumable videos.insert in flight.
	YouTubeDeliveryUploading YouTubeDeliveryState = "uploading"
	// YouTubeDeliveryUploaded — videos.insert returned a youtube_video_id;
	// YouTube is now processing the video server-side.
	YouTubeDeliveryUploaded YouTubeDeliveryState = "youtube_uploaded"
	// YouTubeDeliveryProcessing — poll-driven processingStatus tracking.
	YouTubeDeliveryProcessing YouTubeDeliveryState = "processing"
	// YouTubeDeliveryThumbnailPending — a custom cover is required; the
	// thumbnail pipeline is producing it.
	YouTubeDeliveryThumbnailPending YouTubeDeliveryState = "thumbnail_pending"
	// YouTubeDeliveryThumbnailReady — the cover is linked and ready.
	YouTubeDeliveryThumbnailReady YouTubeDeliveryState = "thumbnail_ready"
	// YouTubeDeliveryScheduled — everything is in place; the delivery is
	// waiting for publish_at (or native YouTube schedule) to elapse.
	YouTubeDeliveryScheduled YouTubeDeliveryState = "scheduled"
	// YouTubeDeliveryPublished — YouTube reports the intended privacy /
	// publish state.
	YouTubeDeliveryPublished YouTubeDeliveryState = "published"
	// YouTubeDeliveryVerified — terminal success: the remote state fully
	// matches intent (video, channel, privacy, publishAt, thumbnail, title).
	YouTubeDeliveryVerified YouTubeDeliveryState = "verified"

	// YouTubeDeliveryRetryWait — transient failure, exponential backoff.
	// next_attempt_at gates re-claim; resume_state records where to return.
	YouTubeDeliveryRetryWait YouTubeDeliveryState = "retry_wait"
	// YouTubeDeliveryQuotaWait — the real quota gate denied a call despite
	// a reservation. resume_state records where to return once quota resets.
	YouTubeDeliveryQuotaWait YouTubeDeliveryState = "quota_wait"
	// YouTubeDeliveryBlockedAuth — OAuth grant invalid (invalid_grant,
	// channel_mismatch). Halts until re-auth; resume_state records where
	// to return.
	YouTubeDeliveryBlockedAuth YouTubeDeliveryState = "blocked_auth"
	// YouTubeDeliveryCopyrightReview — copyright restriction needs
	// operator review. resume_state records where to return once cleared.
	YouTubeDeliveryCopyrightReview YouTubeDeliveryState = "copyright_review"
	// YouTubeDeliveryProcessingStuck — YouTube processing exceeded the
	// stuck threshold. The reconciler re-polls and either advances to the
	// thumbnail/scheduled branch or stays stuck.
	YouTubeDeliveryProcessingStuck YouTubeDeliveryState = "processing_stuck"
	// YouTubeDeliveryFailed — terminal non-recoverable failure (validation,
	// invalid media). No automatic recovery.
	YouTubeDeliveryFailed YouTubeDeliveryState = "failed"
	// YouTubeDeliveryDeadLetter — retry budget exhausted. Operator retry
	// (dead_letter → retry_wait) is the only exit and lives in the
	// operator-transition map, not the worker map.
	YouTubeDeliveryDeadLetter YouTubeDeliveryState = "dead_letter"
)

// IsTerminal classifies the "no further automatic worker transitions"
// set. Terminal states are excluded from the delivery worker's claim CTE.
func (s YouTubeDeliveryState) IsTerminal() bool {
	switch s {
	case YouTubeDeliveryVerified,
		YouTubeDeliveryFailed,
		YouTubeDeliveryDeadLetter:
		return true
	}
	return false
}

// IsSideState reports whether s is one of the blocking side states that
// carry a resume_state and return to the main flow once the blocking
// condition clears. Side states are NOT claimable by the normal worker
// (they resume via a reconciler, an OAuth callback, or the operator
// retry endpoint), but they are also NOT terminal.
func (s YouTubeDeliveryState) IsSideState() bool {
	switch s {
	case YouTubeDeliveryRetryWait,
		YouTubeDeliveryQuotaWait,
		YouTubeDeliveryBlockedAuth,
		YouTubeDeliveryCopyrightReview,
		YouTubeDeliveryProcessingStuck:
		return true
	}
	return false
}

// youtubeDeliveryTransitionMap enumerates the LEGAL worker- and
// reconciler-driven state-to-state transitions. It is the single source
// of truth — CanTransitionTo + LegalTransitions both derive from here.
//
// Every enum value MUST have an entry (possibly empty for terminal
// states); the TestYouTubeDeliveryTransitionMapEnumCoverage test guards
// against accidentally omitting a new enum value.
//
//	from → targets (legal successors only)
//
// Happy-path forward edges are always present; error exits (→ retry_wait /
// blocked_auth / quota_wait / failed / dead_letter) are present from the
// states where they can occur; side states resume via the resume_state
// column — every resume target below is a legal return destination, and
// the actual target is read from resume_state at transition time.
//
// Terminal states (verified / failed / dead_letter) have EMPTY successor
// maps here: no worker transition may leave them. Operator-only
// transitions (dead_letter → retry_wait) live in
// youtubeDeliveryOperatorTransitionMap and are rejected by CanTransitionTo.
var youtubeDeliveryTransitionMap = map[YouTubeDeliveryState]map[YouTubeDeliveryState]bool{
	YouTubeDeliveryPreflight: {
		YouTubeDeliveryReadyToUpload: true, // preflight passed
		YouTubeDeliveryQuotaWait:      true, // capacity unavailable (resume → ready_to_upload)
		YouTubeDeliveryBlockedAuth:    true, // OAuth invalid (resume → preflight)
		YouTubeDeliveryFailed:         true, // validation / permanent preflight failure
	},
	YouTubeDeliveryReadyToUpload: {
		YouTubeDeliveryUploading:   true, // claim before the network call
		YouTubeDeliveryQuotaWait:   true, // runtime quota gate denied
		YouTubeDeliveryBlockedAuth: true, // auth refresh failed
	},
	YouTubeDeliveryUploading: {
		YouTubeDeliveryUploaded:    true, // videos.insert returned video_id
		YouTubeDeliveryRetryWait:   true, // transient upload failure
		YouTubeDeliveryBlockedAuth: true, // auth failure mid-upload
		YouTubeDeliveryDeadLetter:  true, // retry budget exhausted
	},
	YouTubeDeliveryUploaded: {
		YouTubeDeliveryProcessing:      true, // YouTube accepted; poll processingStatus
		YouTubeDeliveryCopyrightReview: true, // copyright restriction needs review
	},
	YouTubeDeliveryProcessing: {
		YouTubeDeliveryThumbnailPending: true, // custom cover required
		YouTubeDeliveryScheduled:        true, // no cover → straight to schedule
		YouTubeDeliveryProcessingStuck:  true, // exceeded the stuck threshold
		YouTubeDeliveryCopyrightReview:  true, // copyright restriction needs review
	},
	YouTubeDeliveryThumbnailPending: {
		YouTubeDeliveryThumbnailReady: true, // thumbnail pipeline produced the cover
		YouTubeDeliveryRetryWait:      true, // thumbnail generation transient failure
	},
	YouTubeDeliveryThumbnailReady: {
		YouTubeDeliveryScheduled: true,
	},
	YouTubeDeliveryScheduled: {
		YouTubeDeliveryPublished: true, // YouTube reports the intended privacy
	},
	YouTubeDeliveryPublished: {
		YouTubeDeliveryVerified: true, // remote state fully matches intent
	},
	// Verified → terminal success — no outgoing worker transitions.
	YouTubeDeliveryVerified: {},

	// Side states — each carries a resume_state; every listed target is a
	// legal return destination (see the resume_state column, migration 125).
	YouTubeDeliveryRetryWait: {
		YouTubeDeliveryReadyToUpload:    true, // retry the pre-upload gate
		YouTubeDeliveryUploading:        true, // resume the resumable upload
		YouTubeDeliveryThumbnailPending: true, // resume thumbnail generation
		YouTubeDeliveryDeadLetter:       true, // retry budget exhausted
	},
	YouTubeDeliveryQuotaWait: {
		YouTubeDeliveryReadyToUpload:    true, // quota reset → retry the gate
		YouTubeDeliveryThumbnailPending: true, // quota reset → resume thumbnail
	},
	YouTubeDeliveryBlockedAuth: {
		YouTubeDeliveryPreflight:     true, // re-auth → re-run preflight
		YouTubeDeliveryReadyToUpload: true, // re-auth → resume before upload
		YouTubeDeliveryUploading:     true, // re-auth → resume the upload
	},
	YouTubeDeliveryCopyrightReview: {
		YouTubeDeliveryProcessing: true, // operator cleared → resume processing
		YouTubeDeliveryScheduled:  true, // operator cleared → resume schedule
	},
	YouTubeDeliveryProcessingStuck: {
		YouTubeDeliveryThumbnailPending: true, // reconciler saw processed → cover path
		YouTubeDeliveryScheduled:        true, // reconciler saw processed → schedule path
	},

	// Failed → terminal — no outgoing worker transitions.
	YouTubeDeliveryFailed: {},
	// DeadLetter → terminal for the worker; the only exit is the operator
	// retry in youtubeDeliveryOperatorTransitionMap.
	YouTubeDeliveryDeadLetter: {},
}

// youtubeDeliveryOperatorTransitionMap enumerates the transitions that may
// ONLY be driven by an operator/endpoint (never by the worker or a
// reconciler). Kept separate so CanTransitionTo (the worker guard) rejects
// them while the retry endpoint can still express them explicitly.
var youtubeDeliveryOperatorTransitionMap = map[YouTubeDeliveryState]map[YouTubeDeliveryState]bool{
	// Operator retry resets the retry budget and re-enters the flow.
	YouTubeDeliveryDeadLetter: {
		YouTubeDeliveryRetryWait: true,
	},
}

// CanTransitionTo returns whether the proposed worker/reconciler
// transition is legal per youtubeDeliveryTransitionMap. It returns false
// for terminal states, for operator-only transitions (dead_letter →
// retry_wait), for unknown enum values, and for empty source/target.
//
// Worker / reconciler / handler code MUST call this BEFORE writing the
// row: the state column carries no SQL CHECK on the transition graph, so
// an absent guard would let a buggy transition (e.g. processing → verified,
// skipping scheduled → published) regress the delivery journal invisibly.
func (s YouTubeDeliveryState) CanTransitionTo(target YouTubeDeliveryState) bool {
	if s == "" || target == "" {
		return false
	}
	successors, ok := youtubeDeliveryTransitionMap[s]
	if !ok {
		return false
	}
	return successors[target]
}

// CanOperatorTransitionTo returns whether the proposed operator-driven
// transition is legal (e.g. the retry endpoint moving dead_letter →
// retry_wait). It is intentionally a SEPARATE gate from CanTransitionTo:
// the worker must never take an operator-only transition, and the retry
// endpoint must never take a worker transition without re-validation.
func (s YouTubeDeliveryState) CanOperatorTransitionTo(target YouTubeDeliveryState) bool {
	if s == "" || target == "" {
		return false
	}
	successors, ok := youtubeDeliveryOperatorTransitionMap[s]
	if !ok {
		return false
	}
	return successors[target]
}

// LegalTransitions returns the deterministically-ordered set of states
// that may legally follow s via a worker/reconciler transition. Returns
// nil for terminal states. Order is by string-sort of the state values —
// stable across process restarts and platform-independent.
func (s YouTubeDeliveryState) LegalTransitions() []YouTubeDeliveryState {
	successors, ok := youtubeDeliveryTransitionMap[s]
	if !ok || len(successors) == 0 {
		return nil
	}
	out := make([]YouTubeDeliveryState, 0, len(successors))
	for tgt, allowed := range successors {
		if allowed {
			out = append(out, tgt)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
