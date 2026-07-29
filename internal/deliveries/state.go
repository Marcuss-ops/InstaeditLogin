// Package deliveries — Canonical state machine for the Velox → InstaEdit
// publishing handoff.
//
// Implements the 21-state enum from `docs/velox-instaedit-contract.md`
// §10. The pipeline has 14 happy-path states and 7 error exits:
//
//	HAPPY PATH (forward-only):
//	  DELIVERY_QUEUED → TARGET_VALIDATING → TARGET_VALIDATED →
//	  MEDIA_DOWNLOADING → MEDIA_VERIFIED → PRIVATE_UPLOAD_QUEUED →
//	  PRIVATE_UPLOADING → PRIVATE_UPLOADED → THUMBNAIL_PENDING →
//	  THUMBNAIL_UPLOADING → THUMBNAIL_APPLIED → READY_TO_PUBLISH →
//	  PUBLISHING → PUBLISHED
//
//	ERROR EXITS (terminal):
//	  BLOCKED_TARGET       workspace/channel absent or disabled
//	  BLOCKED_AUTH         OAuth grant revoked (operator must reconnect)
//	  MEDIA_INVALID        sha/size/mime/duration mismatch
//	  PRIVATE_UPLOAD_FAILED  YouTube insert rejected pre-PRIVATE_UPLOADED
//	  THUMBNAIL_FAILED     thumbnails.set rejected (post-PRIVATE_UPLOADED)
//	  PUBLISH_FAILED       videos.update rejected (post-PRIVATE_UPLOADED)
//	  CANCELLED            explicit cancel by producer (pre-PRIVATE_UPLOADED only)
//
// Coexists with `internal/models/external_delivery.go::ExternalDeliveryStatus`
// (an 11-state SQL-persisted enum backing `external_deliveries.status`).
// Models.Coexistence:
//   - This file (DeliveryState) is the AUTHORITATIVE runtime state model
//     used by the worker pipeline (download → private upload → thumbnail
//     → publish) for in-memory bookkeeping.
//   - The 11-state SQL enum is a lower-fidelity PERSISTENCE representation.
//     A future migration (056_external_delivery_states.sql) widens the
//     SQL CHECK constraint to match the 21 canonical states; until then,
//     MapToExternalDeliveryStatus helpers project down to the 11 states
//     the SQL CHECK allows.
//
// SAFETY INVARIANT — central to the spec:
//	From PRIVATE_UPLOADED onward ALL subsequent errors MUST leave the
//	video's YouTube-side privacy at "private". This invariant is
//	enforced by the PrivacyFloor + EnforcePrivacyInvariant helpers
//	below; callers MUST consult them before issuing videos.update or
//	any transition to PUBLISHED.
//
// References:
//   - docs/velox-instaedit-contract.md §10 (state machine matrix)
//   - docs/velox-instaedit-contract.md §10.3 (privacy-on-error rule)
//   - internal/models/external_delivery.go (SQL-persistence enum)
package deliveries

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// UnmarshalJSON decodes the state from a JSON string literal.
// Because DeliveryState is `type DeliveryState string`, encoding/
// json already marshals it correctly (no MarshalJSON override
// needed — TestUnmarshalJSONRoundtrip exercises that default).
// The custom UnmarshalJSON adds value by validating the value
// against the 21-canonical-state set so a producer typo cannot
// sneak past the JSON boundary into the worker pipeline
// (TestUnmarshalJSONRejectsInvalid pins the rejection surface).
//
// Any non-string JSON shape, or any string that does not match a
// canonical DeliveryState, returns an error so that bad
// payloads (numbers, booleans, or typos) never silently land
// in a pipeline's status column. Mirrors the ExternalDeliveryStatus
// pattern.
func (s *DeliveryState) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("DeliveryState unmarshal: expected JSON string, got: %w", err)
	}
	candidate := DeliveryState(raw)
	known := false
	for _, allowed := range AllDeliveryStates() {
		if candidate == allowed {
			known = true
			break
		}
	}
	if !known {
		return fmt.Errorf("DeliveryState unmarshal: %q is not one of the 21 canonical states", raw)
	}
	*s = candidate
	return nil
}
// DeliveryState is the named state in the publishing pipeline.
type DeliveryState string

// Happy-path forward states (14).
const (
	DeliveryStateDeliveryQueued      DeliveryState = "delivery_queued"
	DeliveryStateTargetValidating    DeliveryState = "target_validating"
	DeliveryStateTargetValidated     DeliveryState = "target_validated"
	DeliveryStateMediaDownloading    DeliveryState = "media_downloading"
	DeliveryStateMediaVerified       DeliveryState = "media_verified"
	DeliveryStatePrivateUploadQueued DeliveryState = "private_upload_queued"
	DeliveryStatePrivateUploading    DeliveryState = "private_uploading"
	DeliveryStatePrivateUploaded     DeliveryState = "private_uploaded"
	DeliveryStateThumbnailPending    DeliveryState = "thumbnail_pending"
	DeliveryStateThumbnailUploading  DeliveryState = "thumbnail_uploading"
	DeliveryStateThumbnailApplied    DeliveryState = "thumbnail_applied"
	DeliveryStateReadyToPublish      DeliveryState = "ready_to_publish"
	DeliveryStatePublishing          DeliveryState = "publishing"
	DeliveryStatePublished           DeliveryState = "published"
)

// Error exit states (7).
const (
	DeliveryStateBlockedTarget       DeliveryState = "blocked_target"
	DeliveryStateBlockedAuth         DeliveryState = "blocked_auth"
	DeliveryStateMediaInvalid        DeliveryState = "media_invalid"
	DeliveryStatePrivateUploadFailed DeliveryState = "private_upload_failed"
	DeliveryStateThumbnailFailed     DeliveryState = "thumbnail_failed"
	DeliveryStatePublishFailed       DeliveryState = "publish_failed"
	DeliveryStateCancelled           DeliveryState = "cancelled"
)

// IsPrivateFloorReached reports whether the pipeline has crossed the
// PRIVATE_UPLOADED boundary. From here on the SAFETY INVARIANT
// applies: any subsequent error MUST keep privacy="private".
//
// The boundary encompasses PRIVATE_UPLOADED itself plus every
// downstream state (thumbnail pipeline → publishing) including
// PUBLISHED. PUBLISHED IS one of the post-boundary states so callers
// that issue videos.update in PUBLISHED still double-check the
// invariant.
func (s DeliveryState) IsPrivateFloorReached() bool {
	switch s {
	case DeliveryStatePrivateUploaded,
		DeliveryStateThumbnailPending,
		DeliveryStateThumbnailUploading,
		DeliveryStateThumbnailApplied,
		DeliveryStateReadyToPublish,
		DeliveryStatePublishing,
		DeliveryStatePublished:
		return true
	}
	return false
}

// IsTerminal returns true when the state has no outgoing transitions.
// Terminal states are operator-final: PUBLISHED is the success
// terminus; the 7 error exits require explicit operator reconcile.
// `delivery_queued` and `target_validating` are NOT terminal: they
// have forward + error exits still available.
func (s DeliveryState) IsTerminal() bool {
	switch s {
	case DeliveryStatePublished,
		DeliveryStateBlockedTarget,
		DeliveryStateBlockedAuth,
		DeliveryStateMediaInvalid,
		DeliveryStatePrivateUploadFailed,
		DeliveryStateThumbnailFailed,
		DeliveryStatePublishFailed,
		DeliveryStateCancelled:
		return true
	}
	return false
}

// IsErrorExit reports whether s is one of the 7 error terminals.
func (s DeliveryState) IsErrorExit() bool {
	switch s {
	case DeliveryStateBlockedTarget,
		DeliveryStateBlockedAuth,
		DeliveryStateMediaInvalid,
		DeliveryStatePrivateUploadFailed,
		DeliveryStateThumbnailFailed,
		DeliveryStatePublishFailed,
		DeliveryStateCancelled:
		return true
	}
	return false
}

// IsRetryable reports whether the worker pool's ClaimBatchForPublish
// CTE should pick up the row from the retry pool on the next claim
// tick. CANCELLED + the 6 error-exit terminals are out: they require
// operator reconcile. PUBLISHED is also out (success terminus).
//
// Asymmetry with `internal/models/external_delivery.go::ExternalDeliveryStatus.IsRetryable`:
// PrivateUploaded and ThumbnailApplied are deliberately NOT retryable
// here. They are post-private-upload "waiting for external system"
// states — the thumbnail session creator (for PrivateUploaded) and
// the publish command (for ThumbnailApplied) drive them forward,
// not the worker pool. Once they emit an event the row moves on;
// until then, the worker's role is to do nothing, not to retry.
// If a future refactor unifies the two state machines this is
// the divergence to flag.
func (s DeliveryState) IsRetryable() bool {
	switch s {
	case DeliveryStateDeliveryQueued,
		DeliveryStateTargetValidating,
		DeliveryStateTargetValidated,
		DeliveryStateMediaDownloading,
		DeliveryStateMediaVerified,
		DeliveryStatePrivateUploadQueued,
		DeliveryStatePrivateUploading,
		DeliveryStateThumbnailPending,
		DeliveryStateThumbnailUploading,
		DeliveryStateReadyToPublish,
		DeliveryStatePublishing:
		return true
	}
	return false
}

// transitionMap encodes the legal successor set per non-terminal
// state. The Go-level guard is the source-of-truth for the topology;
// the SQL CHECK constraint enforces only the value set, not the
// graph (a future migration will widen the SQL-side CHECK).
//
// SAETY-PRESERVING EDGES:
//
//	Pre-PRIVATE_UPLOADED errors can abandon freely; anything that
//	failed before private_uploaded never reached YouTube.
//
//	Post-PRIVATE_UPLOADED errors (thumbnail_failed, publish_failed)
//	share the row with PRIVATE_UPLOADED; the row's privacy stays
//	"private" because we never issued videos.update to flip it.
var transitionMap = map[DeliveryState]map[DeliveryState]bool{
	DeliveryStateDeliveryQueued: {
		DeliveryStateTargetValidating: true,
		DeliveryStateBlockedTarget:   true,
		DeliveryStateCancelled:       true,
	},
	DeliveryStateTargetValidating: {
		DeliveryStateTargetValidated: true,
		DeliveryStateBlockedTarget:   true,
		DeliveryStateCancelled:       true,
	},
	DeliveryStateTargetValidated: {
		DeliveryStateMediaDownloading: true,
		DeliveryStateBlockedTarget:    true,
		DeliveryStateBlockedAuth:      true,
		DeliveryStateCancelled:        true,
	},
	DeliveryStateMediaDownloading: {
		DeliveryStateMediaVerified:  true,
		DeliveryStateMediaInvalid:   true,
		DeliveryStateBlockedAuth:    true,
		DeliveryStateCancelled:      true,
	},
	DeliveryStateMediaVerified: {
		DeliveryStatePrivateUploadQueued: true,
		DeliveryStateMediaInvalid:         true,
		DeliveryStateBlockedAuth:          true,
		DeliveryStateCancelled:            true,
	},
	// Pre-boundary: CANCELLED is legal here even though the
	// upload has been initiated — the YouTube-side row either
	// does not exist yet (PrivateUploadQueued) or has not yet
	// received an authoritative youtube_video_id acknowledgement
	// (PrivateUploading). Workers honouring CANCELLED from these
	// states are responsible for orphan-cleanup (DELETE on the
	// partially-uploaded resource if YouTube returns a 201 mid-
	// shutdown). Per spec §10.2, CANCELLED is FORBIDDEN from any
	// state at-or-after PrivateUploaded.
	DeliveryStatePrivateUploadQueued: {
		DeliveryStatePrivateUploading:    true,
		DeliveryStatePrivateUploadFailed: true,
		DeliveryStateCancelled:           true,
	},
	DeliveryStatePrivateUploading: {
		DeliveryStatePrivateUploaded:     true,
		DeliveryStatePrivateUploadFailed: true,
		DeliveryStateCancelled:           true,
	},
	DeliveryStatePrivateUploaded: {
		DeliveryStateThumbnailPending: true,
		DeliveryStateThumbnailFailed:  true,
	},
	DeliveryStateThumbnailPending: {
		DeliveryStateThumbnailUploading: true,
		DeliveryStateThumbnailFailed:    true,
	},
	DeliveryStateThumbnailUploading: {
		DeliveryStateThumbnailApplied: true,
		DeliveryStateThumbnailFailed:  true,
	},
	DeliveryStateThumbnailApplied: {
		DeliveryStateReadyToPublish: true,
		DeliveryStatePublishFailed:  true,
	},
	DeliveryStateReadyToPublish: {
		DeliveryStatePublishing:    true,
		DeliveryStatePublishFailed: true,
	},
	DeliveryStatePublishing: {
		DeliveryStatePublished:     true,
		DeliveryStatePublishFailed: true,
	},
	// Terminal happy-path: no outgoing edges.
	DeliveryStatePublished: {},
	// Terminal error exits: no outgoing edges (operator must reconcile).
	DeliveryStateBlockedTarget:       {},
	DeliveryStateBlockedAuth:         {},
	DeliveryStateMediaInvalid:        {},
	DeliveryStatePrivateUploadFailed: {},
	DeliveryStateThumbnailFailed:     {},
	DeliveryStatePublishFailed:       {},
	DeliveryStateCancelled:           {},
}

// CanTransitionTo returns whether the transition from s to target is
// in the canonical graph. Returns false for empty source OR empty
// target (defensive; SQL CHECK never holds the empty string, but
// callers can leak zero values).
func (s DeliveryState) CanTransitionTo(target DeliveryState) bool {
	if target == "" {
		return false
	}
	successors, ok := transitionMap[s]
	if !ok {
		return false
	}
	return successors[target]
}

// PrivacyFloor determines the YouTube-side privacy that any
// transition must enforce from the current state.
//
//   Pre-PRIVATE_UPLOADED:  "" (no in-band privacy gate)
//   Post-PRIVATE_UPLOADED: "private"  (mandatory from here on)
//
// Callers MUST consult this helper BEFORE issuing videos.update or
// transitioning toward PUBLISHED; refuse the operation if the floor
// is "private" but the producer's final_privacy is something else.
func (s DeliveryState) PrivacyFloor() string {
	if s.IsPrivateFloorReached() {
		return "private"
	}
	return ""
}

// EnforcePrivacyInvariant is the hard assertion callers MUST run
// before issuing any YouTube-side privacy transition
// (videos.update). It refuses any non-"private" final_privacy once
// the state machine has crossed PRIVATE_UPLOADED.
//
// Pre-boundary transitions are unconstrained here: the producer
// picked final_privacy but it has not yet been "committed" to
// YouTube (the row never reached YouTube). Once the row reaches
// PRIVATE_UPLOADED, the producer's intent has become an active
// YouTube-side concern and the SAFETY INVARIANT applies.
func (s DeliveryState) EnforcePrivacyInvariant(finalPrivacy string) error {
	if !s.IsPrivateFloorReached() {
		return nil
	}
	if strings.EqualFold(finalPrivacy, "private") {
		return nil
	}
	return fmt.Errorf(
		"privacy invariant violated: state=%s has crossed private-uploaded boundary; final_privacy=%q refused, YouTube-side privacy must remain \"private\" until operator-side public transition",
		s, finalPrivacy,
	)
}

// happyPathSuccessor is the explicit canonical happy-path
// successor for every non-terminal state (and the zero value ""
// for every terminal state).
//
// Defined as a separate lookup table (rather than scanning
// transitionMap[] in random map-iteration order) because every
// non-terminal state has EXACTLY ONE happy-path successor; pinning
// it explicitly makes Next() trivially deterministic and stable
// across process restarts. See TestHappyPathSuccessorComplete for
// the consistency pin against transitionMap.
//
// SAETY-PRESERVING RULE: a happy-path successor MUST NOT point at
// an error exit (otherwise `Next` would silently bridge into a
// terminal branch). The table is constructed only from the 14
// forward-state row of the spec §10 matrix.
var happyPathSuccessor = map[DeliveryState]DeliveryState{
	// Happy-path forward (14).
	DeliveryStateDeliveryQueued:       DeliveryStateTargetValidating,
	DeliveryStateTargetValidating:     DeliveryStateTargetValidated,
	DeliveryStateTargetValidated:      DeliveryStateMediaDownloading,
	DeliveryStateMediaDownloading:     DeliveryStateMediaVerified,
	DeliveryStateMediaVerified:        DeliveryStatePrivateUploadQueued,
	DeliveryStatePrivateUploadQueued:  DeliveryStatePrivateUploading,
	DeliveryStatePrivateUploading:     DeliveryStatePrivateUploaded,
	DeliveryStatePrivateUploaded:      DeliveryStateThumbnailPending,
	DeliveryStateThumbnailPending:     DeliveryStateThumbnailUploading,
	DeliveryStateThumbnailUploading:   DeliveryStateThumbnailApplied,
	DeliveryStateThumbnailApplied:     DeliveryStateReadyToPublish,
	DeliveryStateReadyToPublish:       DeliveryStatePublishing,
	DeliveryStatePublishing:           DeliveryStatePublished,
	// Terminals: empty-string successor (preserves Next()=="" semantics).
	DeliveryStatePublished:           "",
	DeliveryStateBlockedTarget:       "",
	DeliveryStateBlockedAuth:         "",
	DeliveryStateMediaInvalid:        "",
	DeliveryStatePrivateUploadFailed: "",
	DeliveryStateThumbnailFailed:     "",
	DeliveryStatePublishFailed:       "",
	DeliveryStateCancelled:           "",
}

// Next returns the canonical one-step HAPPY-PATH successor for
// the current state. Returns the zero value DeliveryState ("")
// when the state is terminal. Callers needing the algebraically
// complete successor set (forward + error) should use
// LegalTransitions; callers needing just the next happy step
// should use this method.
//
// Determinism: the result is keyed directly into the explicit
// `happyPathSuccessor` table rather than iterated out of the
// randomised `transitionMap`. The two structures must remain in
// sync — TestHappyPathSuccessorComplete guards the invariant.
func (s DeliveryState) Next() DeliveryState {
	return happyPathSuccessor[s]
}

// LegalTransitions returns the deterministically-ordered successor
// set for any state. Returns nil for terminal / empty maps. Used by
// the dashboard's "what's next" hint UI and the audit log's
// allowed-action surface. Order is alphabetical — stable across
// process restarts and platform-independent.
func (s DeliveryState) LegalTransitions() []DeliveryState {
	successors, ok := transitionMap[s]
	if !ok || len(successors) == 0 {
		return nil
	}
	out := make([]DeliveryState, 0, len(successors))
	for tgt, allowed := range successors {
		if allowed {
			out = append(out, tgt)
		}
	}
	sortDeliveryStates(out)
	return out
}

// sortDeliveryStates sorts a []DeliveryState alphabetically.
// Delegates to sort.Slice for consistency with
// `internal/models/external_delivery.go::ExternalDeliveryStatus.LegalTransitions`,
// which uses the same SortFn shape. Stable across process restarts
// because the comparator is a pure string compare.
func sortDeliveryStates(in []DeliveryState) {
	sort.Slice(in, func(i, j int) bool { return in[i] < in[j] })
}

// AllDeliveryStates returns the canonical 21-state set as a stable
// slice. Order matches the happy-path-then-error pattern used by the
// spec §10 matrix. Used by ops dashboards and test fixtures to
// enumerate the full state space.
//
// TODO(velox-state-machine#mig056): when migration
// `056_external_delivery_states.sql` lands and widens the
// `external_deliveries.status` CHECK constraint from the current
// 11-value set to the 21 canonical ones, this enum becomes
// eligible to round-trip through `external_delivery_repo.UpdateStatus`.
// Until that migration lands, callers MUST project down to
// `models.ExternalDeliveryStatus` via a mapper helper rather than
// write a DeliveryState value directly into the SQL column — the
// CHECK constraint would reject it at runtime.
func AllDeliveryStates() []DeliveryState {
	return []DeliveryState{
		// Happy path (14).
		DeliveryStateDeliveryQueued,
		DeliveryStateTargetValidating,
		DeliveryStateTargetValidated,
		DeliveryStateMediaDownloading,
		DeliveryStateMediaVerified,
		DeliveryStatePrivateUploadQueued,
		DeliveryStatePrivateUploading,
		DeliveryStatePrivateUploaded,
		DeliveryStateThumbnailPending,
		DeliveryStateThumbnailUploading,
		DeliveryStateThumbnailApplied,
		DeliveryStateReadyToPublish,
		DeliveryStatePublishing,
		DeliveryStatePublished,
		// Error exits (7) — grouped by classification need.
		DeliveryStateBlockedTarget,
		DeliveryStateBlockedAuth,
		DeliveryStateMediaInvalid,
		DeliveryStatePrivateUploadFailed,
		DeliveryStateThumbnailFailed,
		DeliveryStatePublishFailed,
		DeliveryStateCancelled,
	}
}
