// Package-level note: group expansion is split per concern
// (split-by-concern, 2026-08):
//
//	group_expand.go        — this file: delivery-set data types + wiring
//	                         interfaces + ExpandForGroup + deriveSetID /
//	                         deriveChildID (deterministic id derivation)
//	group_expand_status.go — DeliverySetStatus enum + AllDeliverySetStatuses
//	                         + AggregateStatus + DeliverySetStatus.IsTerminal
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

// DeliverySet is the aggregator row for N child deliveries expanded
// from a single group_id delivery. One row per accepted
// the canonical Velox delivery request with `destination.target_type=group`.
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
// Mirrors the canonical Velox delivery shape post-group-expansion: only
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
// ExpandForGroup, derived from the flat Velox delivery request but
// scoped to the group expansion call. Field names mirror the
// canonical delivery request (snake_case JSON outside of this Go
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
// any repo work. Mirror of the delivery validation gate in
// `pkg/api/deliveries_handler.go` —
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
// a canonical Velox delivery request with
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
// Format: `velox-<16 hex chars>` is the stable child-delivery ID
// format used by the canonical delivery expansion path.
func deriveChildID(p ExpandForGroupParams, accountID int64) string {
	h := sha256.Sum256([]byte(strings.Join([]string{
		"child",
		deriveSetID(p),
		fmt.Sprintf("account=%d", accountID),
	}, "|")))
	return "velox-" + hex.EncodeToString(h[:8])
}
