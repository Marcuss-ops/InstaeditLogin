package deliveries

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

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

// DeliverySet is the aggregator row for N child deliveries expanded
// from a single group_id delivery. One row per accepted
// `VeloxDeliverContractRequest` with `destination.target_type=group`.
//
// Persisted in `delivery_sets` (pending migration
// `057_external_delivery_sets.sql`); the fields below are the
// canonical shape regardless of repository choice. A `Membership`
// row per child stores the (set, child_id, account_id) triple; the
// per-account deliverable row stays in `external_deliveries` so the
// existing single-delivery surface is unchanged.
type DeliverySet struct {
	ID               string            `json:"id"`
	WorkspaceID      int64             `json:"workspace_id"`
	Platform         string            `json:"platform"`
	GroupID          int64             `json:"group_id"`
	SourceSystem     string            `json:"source_system"`
	SourceJobID      string            `json:"source_job_id"`
	SourceTaskID     string            `json:"source_task_id"`
	SourceArtifactID string            `json:"source_artifact_id"`
	Status           DeliverySetStatus `json:"status"`
	ChildDeliveries  []string          `json:"child_deliveries"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
}

// DeliverySetMembership is the (set_id, child_delivery_id, account_id)
// triple that pins a child delivery to its parent set. Mirrors the
// `delivery_set_members` table shape (pending migration 057).
type DeliverySetMembership struct {
	SetID             string `json:"set_id"`
	DeliveryID        string `json:"delivery_id"`
	WorkspaceID       int64  `json:"workspace_id"`
	PlatformAccountID int64  `json:"platform_account_id"`
	Ordinal           int    `json:"ordinal"` // 0..len(children)-1; pinned for stable order
}

// ErrGroupEmpty is returned from ExpandForGroup when the input
// group has zero active+enabled accounts attached. The handler
// layer surfaces this as `GROUP_EMPTY` per
// `destinations_resolve_target.go::GROUP_EMPTY` wire convention
// (422 with `error_code`).
var ErrGroupEmpty = errors.New("velox deliveries: group has zero active accounts")

// ActiveGroupAccountLister is the minimal interface ExpandForGroup
// uses to enumerate the active+enabled accounts attached to a group.
// Today production wiring is `internal/repository.groupRepo` via
// `ListAccountsInGroup` filtered with status='active' + enabled=true
// + reauth_required_at IS NULL. Tests provide an in-memory fake.
//
// The workspaceID+groupID pair is a redundant safety guard so a
// caller that mistakenly looks up the group without scoping it to
// the workspace does not leak across tenants.
type ActiveGroupAccountLister interface {
	ListActiveAccountsInGroup(ctx context.Context, workspaceID, groupID int64) ([]int64, error)
}

// DeliveryInserter is the minimal interface for the per-child
// InsertExternalDelivery call.
//
// The Inserter MUST echo params.DeliveryID back as its return
// value — ExpandForGroup computes the canonical id deterministically
// from (set_id, account_id) BEFORE calling Insert, so the caller
// already knows the row id. The inserter's role is just to persist
// the row (under pg_advisory_xact_lock + on-conflict-dedup the
// existing row's id) and return what was passed. This pattern lets
// the production wiring use a Postgres sequence (RETURNING id) AND
// the test fakes a no-op without BOTH sides needing to re-derive.
//
// Production wiring: `external_delivery_repo.Insert` with
// `external_delivery_id` set explicitly from `params.DeliveryID`.
// Test wiring: a fake that just returns `params.DeliveryID`.
type DeliveryInserter interface {
	InsertChildDelivery(ctx context.Context, params ChildDeliveryParams) (string, error)
}

// ChildDeliveryParams is the per-child deliverable-record input.
// Mirrors the VeloxContractRequest shape post-group-expansion: only
// the `destination.platform_account_id` differs per child, all
// other fields are inherited from the parent group's request.
//
// DeliveryID is the canonical id (as derived from
// deriveChildID(set_id, account_id)) that ExpandForGroup stamps
// on the row. The inserter MUST persist the row under this id
// (NOT auto-mint a new one) so the set↔child join stays stable
// across replays. If the id collides on a previous row, the
// inserter returns the same id (idempotent upsert).
type ChildDeliveryParams struct {
	DeliveryID        string // canonical child id set by ExpandForGroup before calling Insert
	SetID             string
	System            string
	JobID             string
	TaskID            string
	ArtifactID        string
	WorkspaceID       int64
	Platform          string
	PlatformAccountID int64
	Ordinal           int
}

// DeliverySetRecorder is the minimal interface for the (set row +
// membership rows) persistence — today a future
// `delivery_set_repo.InsertBatch`. Tests use an in-memory fake.
type DeliverySetRecorder interface {
	RecordDeliverySet(ctx context.Context, set DeliverySet) error
	RecordMemberships(ctx context.Context, memberships []DeliverySetMembership) error
}

// ExpandForGroupParams is the canonical input shape for
// ExpandForGroup, derived from VeloxDeliverContractRequest but
// scoped to the group expansion call. Field names mirror the
// existing contract struct (snake_case JSON outside of this Go
// package).
type ExpandForGroupParams struct {
	System      string
	JobID       string
	TaskID      string
	ArtifactID  string
	WorkspaceID int64
	Platform    string
	GroupID     int64
}

// Validate enforces the structural rules before ExpandForGroup does
// any repo work. Mirror of the validateContractRequest gate in
// `pkg/api/deliveries_create.go::validateContractRequest` —
// callers SHOULD call this before invoking ExpandForGroup to fail
// fast on malformed input rather than after a list+insert round
// trip.
func (p *ExpandForGroupParams) Validate() error {
	if p == nil {
		return errors.New("expand: params nil")
	}
	if p.System != "velox" {
		return fmt.Errorf("expand: system must be 'velox', got %q", p.System)
	}
	if strings.TrimSpace(p.JobID) == "" {
		return errors.New("expand: job_id must be non-empty")
	}
	if strings.TrimSpace(p.TaskID) == "" {
		return errors.New("expand: task_id must be non-empty")
	}
	if strings.TrimSpace(p.ArtifactID) == "" {
		return errors.New("expand: artifact_id must be non-empty")
	}
	if p.WorkspaceID <= 0 {
		return fmt.Errorf("expand: workspace_id must be > 0, got %d", p.WorkspaceID)
	}
	if p.Platform != models.PlatformYouTube {
		return fmt.Errorf("expand: platform must be %q, got %q",
			models.PlatformYouTube, p.Platform)
	}
	if p.GroupID <= 0 {
		return fmt.Errorf("expand: group_id must be > 0, got %d", p.GroupID)
	}
	return nil
}

// ExpandForGroup takes a group_id-backed delivery request and:
//
//  1. Enumerates the group's active+enabled accounts via
//     ActiveGroupAccountLister.
//  2. Mints a deterministic DeliverySet ID + N child IDs
//     (stable across replays; see deriveSetID / deriveChildID).
//  3. Calls DeliveryInserter.InsertChildDelivery for each account
//     in deterministic ascending-order, stamping the canonical
//     child id on the params BEFORE the call. The inserter
//     persists the row AND echoes the same id back. This way the
//     production SQL wiring (which uses pg_advisory_xact_lock +
//     RETURNING id) and the test fake (a no-op) converge on the
//     same id without either side re-deriving.
//  4. Records the (set, memberships) tuple via DeliverySetRecorder.
//  5. Returns the populated DeliverySet + the membership slice + nil
//     on the happy path; ErrGroupEmpty when the group has zero
//     active accounts; wrapped DB errors otherwise.
//
// The function is the SPEC'D GROUP ENTRY POINT for
// `VeloxDeliverContractRequest` with
// `destination.target_type=group`. Once the per-child Insert has
// succeeded, each child is treated by the existing
// single-delivery worker pipeline (download → private upload →
// thumbnail → publish); the set's status is recomputed by
// AggregateStatus from the children's DeliveryState changes.
//
// Determinism contract: child IDs and the set ID are derived from
// (system, job, task, artifact, platform, group|account) so a
// replay of the SAME producer request reproduces the SAME set ID
// + SAME child IDs + Insert's pg_advisory_xact_lock fires the
// "duplicate" branch instead of inserting twice. This matches the
// spec §7.2 Idempotency-Key contract extended to sets.
func ExpandForGroup(
	ctx context.Context,
	params ExpandForGroupParams,
	lister ActiveGroupAccountLister,
	inserter DeliveryInserter,
	recorder DeliverySetRecorder,
) (*DeliverySet, []DeliverySetMembership, error) {
	if err := params.Validate(); err != nil {
		return nil, nil, err
	}
	if lister == nil {
		return nil, nil, errors.New("expand: ActiveGroupAccountLister is nil")
	}
	if inserter == nil {
		return nil, nil, errors.New("expand: DeliveryInserter is nil")
	}
	if recorder == nil {
		return nil, nil, errors.New("expand: DeliverySetRecorder is nil")
	}

	// Step 1 — enumerate. Active+enabled+reauth-clean per spec §2.2.
	accountIDs, err := lister.ListActiveAccountsInGroup(ctx, params.WorkspaceID, params.GroupID)
	if err != nil {
		return nil, nil, fmt.Errorf("expand: list active accounts failed: %w", err)
	}
	if len(accountIDs) == 0 {
		return nil, nil, ErrGroupEmpty
	}

	// Deterministic ascending order so replays produce the same
	// child_id / ordinal pair. Account 381 < 382 < 442 < 605.
	sort.Slice(accountIDs, func(i, j int) bool { return accountIDs[i] < accountIDs[j] })

	// Step 2 — derive set ID BEFORE child IDs (children hash the
	// set ID into their derived form so replays of the same set
	// produce the same child IDs even if the production code
	// switches from per-account sha to set-relative sha later).
	setID := deriveSetID(params)

	now := time.Now().UTC()
	set := &DeliverySet{
		ID:               setID,
		WorkspaceID:      params.WorkspaceID,
		Platform:         params.Platform,
		GroupID:          params.GroupID,
		SourceSystem:     params.System,
		SourceJobID:      params.JobID,
		SourceTaskID:     params.TaskID,
		SourceArtifactID: params.ArtifactID,
		Status:           DeliverySetStatusInProgress, // at least one queued child
		ChildDeliveries:  make([]string, 0, len(accountIDs)),
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	// Step 3 — per-child Insert + membership record. We pre-flight
	// the membership slice so even an inserter panic mid-loop
	// leaves a deterministic record of the partial run (recorded
	// at most once via the recorder's atomic batch below).
	memberships := make([]DeliverySetMembership, 0, len(accountIDs))
	for ordinal, accountID := range accountIDs {
		childID := deriveChildID(params, accountID)
		insertParams := ChildDeliveryParams{
			DeliveryID:        childID,
			SetID:             setID,
			System:            params.System,
			JobID:             params.JobID,
			TaskID:            params.TaskID,
			ArtifactID:        params.ArtifactID,
			WorkspaceID:       params.WorkspaceID,
			Platform:          params.Platform,
			PlatformAccountID: accountID,
			Ordinal:           ordinal,
		}
		deliveryID, err := inserter.InsertChildDelivery(ctx, insertParams)
		if err != nil {
			return nil, nil, fmt.Errorf("expand: insert child %d failed: %w", accountID, err)
		}
		// The inserter echoes the canonical id back. A mismatch is
		// a SERIOUS inserter bug (would break the set↔child join)
		// and is rejected loudly so test fakes + production stay
		// aligned.
		if deliveryID != childID {
			return nil, nil, fmt.Errorf(
				"expand: inserter returned %q for account %d, expected canonical child id %q; both inserter and deriveChildID must agree",
				deliveryID, accountID, childID,
			)
		}
		set.ChildDeliveries = append(set.ChildDeliveries, deliveryID)
		memberships = append(memberships, DeliverySetMembership{
			SetID:             setID,
			DeliveryID:        deliveryID,
			WorkspaceID:       params.WorkspaceID,
			PlatformAccountID: accountID,
			Ordinal:           ordinal,
		})
	}

	// Step 4 — persist. Order: set row first, then memberships.
	// A real implementation runs both under a single tx so a
	// partial-record crash doesn't leave the (set, members) tables
	// inconsistent. Tests use an in-memory batch fake that matches
	// the same XOR logic.
	if err := recorder.RecordDeliverySet(ctx, *set); err != nil {
		return nil, nil, fmt.Errorf("expand: record set failed: %w", err)
	}
	if err := recorder.RecordMemberships(ctx, memberships); err != nil {
		return nil, nil, fmt.Errorf("expand: record memberships failed: %w", err)
	}

	return set, memberships, nil
}

// deriveSetID produces the canonical DeliverySet ID from the parent
// request's identifying fields. Stable across process restarts so
// same producer request maps to same set_id across an InstaeditLogin
// restart, replica failover, or in-flight retry.
//
// Format: `set-<16 hex chars>`. The hash input concatenates with
// `|` separators so a different field ordering at the producer
// side cannot accidentally reshuffle the set ID.
func deriveSetID(p ExpandForGroupParams) string {
	h := sha256.Sum256([]byte(strings.Join([]string{
		"set",
		p.System,
		p.JobID,
		p.TaskID,
		p.ArtifactID,
		p.Platform,
		fmt.Sprintf("group=%d", p.GroupID),
	}, "|")))
	return "set-" + hex.EncodeToString(h[:8])
}

// deriveChildID produces the canonical per-child delivery ID. The
// hash input is keyed on (set_id, account_id) so a child ID is
// unique to its parent set yet still deterministic across
// replays of the producer's same key. deriveSetID is called with
// the same params (NOT the inserter's reconstructed params), so
// both sides must thread GroupID end-to-end — a parameter that a
// future binding change accidentally zeroes WILL surface as a
// deterministic-id mismatch at ExpandForGroup time.
//
// Format: `velox-<16 hex chars>` matches the existing
// `deliveries_create.go::synthesizeContractDeliveryID` contract.
func deriveChildID(p ExpandForGroupParams, accountID int64) string {
	h := sha256.Sum256([]byte(strings.Join([]string{
		"child",
		deriveSetID(p),
		fmt.Sprintf("account=%d", accountID),
	}, "|")))
	return "velox-" + hex.EncodeToString(h[:8])
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
