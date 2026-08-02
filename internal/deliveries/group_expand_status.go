package deliveries

// DeliverySetStatus is the 6-value aggregate-status enum for a
// DeliverySet — the parent rollup of N child deliveries expanded
// from a single group_id delivery per
// `docs/velox-instaedit-contract.md` §12.
//
// Coexists with the per-child `DeliveryState` (21 values). A child
// in `published` contributes to `set_succeeded`; children in
// `cancelled` are EXCLUDED from success/error counting because
// operator cancel is neither success nor failure.
//
//	SET_PENDING     — initial state before any child progresses.
//	SET_IN_PROGRESS — at least one child is non-terminal.
//	SET_SUCCEEDED   — every child PUBLISHED (CANCELLED children ignored).
//	SET_PARTIAL     — at least one child PUBLISHED + at least one
//	                  child in terminal-error (NOT CANCELLED).
//	SET_FAILED      — at least one terminal-error child + zero
//	                  PUBLISHED children (CANCELLED children ignored).
//	SET_CANCELLED   — every child CANCELLED (or set-level operator
//	                  cancel that propagated downward).
//
// Conforms to the spec §12 "PARTIAL semantics":
//
//	"At least one child delivery reached PUBLISHED and at least one
//	reached a terminal error. SUCCEEDED requires all children
//	PUBLISHED."
type DeliverySetStatus string

const (
	DeliverySetStatusPending    DeliverySetStatus = "set_pending"
	DeliverySetStatusInProgress DeliverySetStatus = "set_in_progress"
	DeliverySetStatusSucceeded  DeliverySetStatus = "set_succeeded"
	DeliverySetStatusPartial    DeliverySetStatus = "set_partial"
	DeliverySetStatusFailed     DeliverySetStatus = "set_failed"
	DeliverySetStatusCancelled  DeliverySetStatus = "set_cancelled"
)

// AllDeliverySetStatuses returns the canonical 6-value set as a slice,
// ordered happy → error (matches the convention in
// `AllDeliveryStates`). Used by ops dashboards and tests that enumerate
// the full status space.
func AllDeliverySetStatuses() []DeliverySetStatus {
	return []DeliverySetStatus{
		DeliverySetStatusPending,
		DeliverySetStatusInProgress,
		DeliverySetStatusSucceeded,
		DeliverySetStatusPartial,
		DeliverySetStatusFailed,
		DeliverySetStatusCancelled,
	}
}

// AggregateStatus computes the DeliverySet status from N
// per-child DeliveryState values per spec §12. Pure function —
// given the children states, the rolled-up status is
// deterministic. The function is the source-of-truth for dashboards
// and tests; a persisted `delivery_sets.status` column is
// recomputed lazily from the live child states to avoid stale
// reads after a partial-failure restart.
//
// CANCELLED reasoning: operator cancel is treated as a
// "neither-success-nor-failure" signal. Implication:
//
//   - All children PUBLISHED → SUCCEEDED, regardless of intermediate
//     CANCELLED children (those were operator-driven skips that
//     didn't fail).
//   - Mixed CANCELLED + PUBLISHED → SUCCEEDED (the surviving
//     children all succeeded organically).
//   - Mixed CANCELLED + error (no PUBLISHED) → FAILED (the failing
//     children dominated; the cancelled ones are neutral).
//   - All children CANCELLED → CANCELLED (nothing failed AND
//     nothing succeeded).
//
// Ordering of inspection (highest-priority first):
//
//  1. Every child CANCELLED → CANCELLED.
//  2. Any child non-terminal → IN_PROGRESS.
//  3. All non-cancelled children PUBLISHED → SUCCEEDED.
//  4. ≥ 1 PUBLISHED + ≥ 1 error (excluding CANCELLED) → PARTIAL.
//  5. ≥ 1 error (excluding CANCELLED) + zero PUBLISHED → FAILED.
//  6. Otherwise → PENDING (degenerate).
//
// Empty children slice → PENDING (degenerate; ExpandForGroup
// refuses to create a set when len(children)==0, returning
// ErrGroupEmpty).
func AggregateStatus(childStates []DeliveryState) DeliverySetStatus {
	if len(childStates) == 0 {
		return DeliverySetStatusPending
	}

	var (
		published    int
		errorExits   int // terminal errors EXCLUDING cancelled
		cancelled    int
		inProgress   int // non-terminal states
		allCancelled = true
	)
	for _, s := range childStates {
		// Defensive: zero-value DeliveryState indicates a caller
		// bug (no child delivery should ever aggregate ""). Count
		// as in-progress rather than as a silent CANCELLED so the
		// status highlights the mismatch.
		if s == "" {
			inProgress++
			allCancelled = false
			continue
		}
		// CANCELLED is treated as a separate class — operator
		// cancel is neither success nor failure, so it does NOT
		// contribute to errorExits or published counting. The
		// `IsTerminal()` helper returns true for CANCELLED AND
		// PUBLISHED, so we MUST branch before it.
		if s == DeliveryStateCancelled {
			cancelled++
			continue
		}
		// PUBLISHED is the success terminus — counts as published,
		// NOT as an error exit (even though IsTerminal() is true).
		// Branch before IsTerminal() for the same reason as above.
		if s == DeliveryStatePublished {
			published++
			allCancelled = false
			continue
		}
		// Terminal errors — the 6 error-exit states
		// (BLOCKED_TARGET / BLOCKED_AUTH / MEDIA_INVALID /
		// PRIVATE_UPLOAD_FAILED / THUMBNAIL_FAILED / PUBLISH_FAILED).
		// CANCELLED + PUBLISHED already filtered above.
		if s.IsTerminal() {
			errorExits++
			allCancelled = false
			continue
		}
		// Non-terminal: in progress (includes DeliveryQueued and
		// any of the 13 happy-path forward states).
		inProgress++
		allCancelled = false
	}

	// Rule 1: every child cancelled → set cancelled.
	if allCancelled {
		return DeliverySetStatusCancelled
	}

	// Rule 2: any child non-terminal → set still in progress.
	if inProgress > 0 {
		return DeliverySetStatusInProgress
	}

	// From here on every non-cancelled child is terminal.
	// Inspect the success / error counts (CANCELLED excluded).
	switch {
	case published == len(childStates)-cancelled:
		// All non-cancelled children are PUBLISHED.
		return DeliverySetStatusSucceeded
	case published > 0 && errorExits > 0:
		return DeliverySetStatusPartial
	case errorExits > 0:
		return DeliverySetStatusFailed
	default:
		// Degenerate: zero published, zero errors, zero
		// in-progress; all cancelled already short-circuited at
		// rule 1. Should not reach.
		return DeliverySetStatusPending
	}
}

// IsTerminal classifies the operator-final DeliverySet states.
// Mirrors DeliveryState.IsTerminal but for the set layer. Used by
// the dashboard "what's done" surface and tests.
func (s DeliverySetStatus) IsTerminal() bool {
	switch s {
	case DeliverySetStatusSucceeded,
		DeliverySetStatusPartial,
		DeliverySetStatusFailed,
		DeliverySetStatusCancelled:
		return true
	}
	return false
}
