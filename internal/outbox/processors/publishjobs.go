// Package processors hosts the ProcessFunc implementations the
// outbox dispatcher plugs in. Each file is one consumer of the
// outbox-stream (publish_jobs materialiser today, future
// workspace.member.invited / api_key.rotated / media.uploaded later).
//
// Taglio 5.x — STEP 3 (publish_jobs materialiser). STEP 1 wrote
// outbox_events atomically alongside posts + post_targets
// (internal/repository/post_repo.go::Create, lines 130–160). STEP 2
// (internal/outbox/dispatcher.go) reads them back via a claim-with-
// lease pattern. STEP 3, THIS FILE, materialises the publish_jobs
// audit row from the payload.
//
// Lifecycle for one post_target publish intent:
//
//	CreatePost          (one tx): posts + post_targets + outbox_events
//	                                       │
//	                                       ▼
//	Dispatcher (async)  (separate goroutine, separate tx):
//	    ClaimNext → Materialize → MarkProcessed
//	                                       │
//	                                       ▼
//	publish_jobs row created  (audit-only — post_targets.status
//	                           remains the source of truth)
//
// Idempotency: a partial UNIQUE index on publish_jobs.outbox_event_id
// (WHERE NOT NULL) guarantees at-most-one publish_jobs row per
// outbox_event. A dispatcher retry of an already-processed event
// surfaces SQLSTATE 23505 → we map it to success (nil). This is the
// correct behaviour because MarkProcessed is the dispatcher's
// durable commit; the dispatcher has already decided "this event is
// done", and re-running the materialiser should NOT re-decide it.
//
// Tx shape: this ProcessFunc runs in autocommit mode (one INSERT).
// No explicit BEGIN/COMMIT. The dispatcher calls it inside its own
// claim-with-lease OUTBOX tx — adding a second tx here would
// unnecessarily nest transaction-within-transaction. The DB-level
// UNIQUE index provides the atomicity we need.
package processors

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/lib/pq"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/outbox"
)

// Defer the package import of outbox only for the ProcessFunc type
// (avoids the import alias dance in callers — outbox.ProcessFunc is
// the type the dispatcher expects).

// PublishJobsMaterialiser is a ProcessFunc that turns a claimed
// outbox_event row into a publish_jobs audit row. Construct via
// NewPublishJobsMaterialiser.
//
// Behaviour summary per call (with an event ev):
//
//  1. Validate ev.AggregateType / EventType / AggregateID —
//     wrong-shape events are terminal (DLQ).
//  2. Decode ev.Payload into publishPayload — malformed JSON or
//     missing required fields are terminal (DLQ).
//  3. INSERT INTO publish_jobs (status='pending', version=1, …).
//     SQLSTATE 23505 (UNIQUE on outbox_event_id) is success (a
//     previous attempt already materialised this row).
//  4. Any other SQL error is transient (the dispatcher retries up
//     to MaxAttempts, then DLQs).
//
// Context: passed by the dispatcher. We respect ctx.Done so a
// graceful shutdown mid-INSERT surfaces as a transient error
// rather than a terminal one — partial commits don't get to DLQ.
type PublishJobsMaterialiser struct {
	db *sql.DB
}

// NewPublishJobsMaterialiser packs a closure around *sql.DB that
// satisfies outbox.ProcessFunc. The returned closure is safe for
// concurrent use across dispatches (database/sql.DB pool is).
func NewPublishJobsMaterialiser(db *sql.DB) outbox.ProcessFunc {
	m := &PublishJobsMaterialiser{db: db}
	return m.Process
}

// publishPayload mirrors the JSON shape PostRepository.Create writes
// into outbox_events.payload (see internal/repository/post_repo.go
// lines 130–160). event_version is the schema contract — bumps force
// the materialiser to evolve without breaking older rows.
//
// Fields NOT consumed by the materialiser (scheduled_at, title,
// caption, media_url) are intentionally absent. publish_jobs is a
// thin audit row keyed by post_target_id + outbox_event_id; the
// publisher reads post_targets + posts directly to render the actual
// platform API call. Re-storing those fields here doubles the
// per-row footprint and risks drift.
type publishPayload struct {
	EventVersion   string `json:"event_version"`
	PostID         int64  `json:"post_id"`
	TargetID       int64  `json:"target_id"`
	WorkspaceID    int64  `json:"workspace_id"`
	PlatformAcctID int64  `json:"platform_account_id"`
}

// supported event_version values. The schema is currently v1; a
// future major-version bump (e.g. materialise publish_jobs across
// multiple workspaces) would add v2 and surface a "version 2 not
// yet supported" terminal error so the row goes to DLQ.
const (
	publishPayloadV1 = "v1"
	payloadEmpty     = ""
)

// Process satisfies outbox.ProcessFunc. Recipient of ev.Payload from
// ClaimNext; reads + writes a single publish_jobs row.
//
// Error classification:
//
//   - nil → MarkProcessed (terminal success).
//   - wraps outbox.ErrTerminal → MarkDeadLetter (no retry).
//   - other → MarkFailed with backoff (transient retry; eventually DLQ
//     on MaxAttempts).
func (m *PublishJobsMaterialiser) Process(ctx context.Context, ev *models.OutboxEvent) error {
	if err := validateEventMetadata(ev); err != nil {
		return err // already wraps ErrTerminal when terminal
	}

	var p publishPayload
	if err := json.Unmarshal(ev.Payload, &p); err != nil {
		// Malformed JSON is unrecoverable — the row will never decode
		// correctly. Terminal prevents wasted retry attempts.
		return fmt.Errorf("%w: outbox event %d has malformed payload JSON: %v",
			outbox.ErrTerminal, ev.ID, err)
	}
	if err := validatePayload(ev.ID, &p); err != nil {
		return err
	}

	// post_target_id is nullable after migration 026. TargetID <= 0 is
	// the "no target" signal (future use case — reuses same outbox
	// pattern for non-post_target events). We pass sql.NullInt64 so the
	// column stores NULL without tripping the FK on post_targets(id).
	var targetIDArg sql.NullInt64
	if p.TargetID > 0 {
		targetIDArg = sql.NullInt64{Int64: p.TargetID, Valid: true}
	}

	_, err := m.db.ExecContext(ctx,
		`INSERT INTO publish_jobs
		    (post_target_id, outbox_event_id, status, attempt_number, version)
		 VALUES ($1, $2, 'pending', 0, 1)`,
		targetIDArg, ev.ID,
	)
	if err != nil {
		// SQLSTATE 23505 on the partial UNIQUE index
		// (uniq_publish_jobs_outbox_event) means the dispatcher
		// re-claimed an event whose publish_jobs row already
		// exists from a previous attempt. Treat as idempotent
		// success so MarkProcessed fires — the dispatcher already
		// decided "this event is done" and we don't want to
		// re-decide it via the retry path.
		var pqErr *pq.Error
		if errors.As(err, &pqErr) {
			// SQLSTATE 23505 on the partial UNIQUE index
			// (uniq_publish_jobs_outbox_event) means the dispatcher
			// re-claimed an event whose publish_jobs row already
			// exists from a previous attempt. Treat as idempotent
			// success so MarkProcessed fires.
			if pqErr.Code == "23505" {
				return nil
			}
			// SQLSTATE 23503 on the post_target_id FK means the
			// referenced post_target was deleted between the outbox
			// event being written and the dispatcher picking it up.
			// There is no publish job to materialise anymore; drop
			// the obsolete intent as terminal so it doesn't loop
			// through MaxAttempts.
			if pqErr.Code == "23503" {
				return fmt.Errorf("%w: post_target %d (outbox event %d) deleted before materialisation: %v",
					outbox.ErrTerminal, p.TargetID, ev.ID, err)
			}
		}
		// Context cancellation during INSERT is transient (a
		// graceful shutdown mid-flight should NOT send to DLQ).
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("publish_jobs materialise cancelled: %w", err)
		}
		// Any other SQL error is a transient infrastructure blip —
		// surface as-is so the dispatcher does its backoff +
		// MaxAttempts → eventually DLQ.
		return fmt.Errorf("publish_jobs materialise: %w", err)
	}
	return nil
}

// validateEventMetadata returns a terminal error when the event
// fields the dispatcher hands us don't match what post_repo.go wrote
// (a malformed row is the dispatcher's job to refuse — silent
// acceptance would silently lose audit rows).
func validateEventMetadata(ev *models.OutboxEvent) error {
	if ev == nil {
		return fmt.Errorf("%w: nil event passed to materialiser", outbox.ErrTerminal)
	}
	if ev.AggregateType != "post_target" {
		return fmt.Errorf("%w: outbox event %d has aggregate_type=%q (want %q)",
			outbox.ErrTerminal, ev.ID, ev.AggregateType, "post_target")
	}
	if ev.EventType != "post_target.publish_requested" {
		return fmt.Errorf("%w: outbox event %d has event_type=%q (want %q)",
			outbox.ErrTerminal, ev.ID, ev.EventType, "post_target.publish_requested")
	}
	// AggregateID is the post_target id for post_target events. A
	// zero or negative id can never be a real BIGSERIAL id —
	// refuse loudly rather than store NULL silently.
	if ev.AggregateID <= 0 {
		return fmt.Errorf("%w: outbox event %d has aggregate_id=%d (must be > 0)",
			outbox.ErrTerminal, ev.ID, ev.AggregateID)
	}
	return nil
}

// validatePayload checks that the decoded payload carries the
// minimum the publisher relies on. target_id is allowed to be
// zero (the nullable-FK path); post_id and workspace_id are
// required because the publisher uses them to render the call
// downstream.
func validatePayload(eventID int64, p *publishPayload) error {
	if p.EventVersion == payloadEmpty {
		return fmt.Errorf("%w: outbox event %d has empty event_version",
			outbox.ErrTerminal, eventID)
	}
	if p.EventVersion != publishPayloadV1 {
		return fmt.Errorf("%w: outbox event %d has event_version=%q (only %q supported)",
			outbox.ErrTerminal, eventID, p.EventVersion, publishPayloadV1)
	}
	if p.PostID <= 0 {
		return fmt.Errorf("%w: outbox event %d has post_id=%d (must be > 0)",
			outbox.ErrTerminal, eventID, p.PostID)
	}
	if p.WorkspaceID <= 0 {
		return fmt.Errorf("%w: outbox event %d has workspace_id=%d (must be > 0)",
			outbox.ErrTerminal, eventID, p.WorkspaceID)
	}
	// TargetID is allowed to be 0 (nullable FK path) — publisher
	// keys on post_target_id after the dispatcher materialises
	// the row, and NULL is a defensible state.
	return nil
}
