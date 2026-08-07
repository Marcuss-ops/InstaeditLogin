package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/lib/pq"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// --- PostTargets ---

// Save inserts a new post_target (a single fan-out row added to an existing
// post). Use this to add a platform_account to an already-existing post.
// For the initial create-of-post-with-N-targets use PostRepository.Create
// which wraps both inserts in one transaction.
//
// provider_idempotency_key is intentionally NOT set here — it's a
// worker-side concern stamped AFTER the atomic claim (see
// SetProviderIdempotencyKey). Stamping at Save time would require the
// API handler to know the determinism rule, which would leak the
// worker contract into HTTP-body parsing.
//
// A duplicate (post_id, platform_account_id) surfaces as
// ErrPostTargetDuplicate (mapped to 409 in the API layer).
func (r *PostRepository) Save(target *models.PostTarget) error {
	err := r.db.QueryRow(
		qInsertPostTarget,
		target.PostID, target.PlatformAccountID, target.Status,
	).Scan(&target.ID)
	if err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == "23505" && pqErr.Constraint == "post_targets_post_id_platform_uniq" {
			return fmt.Errorf("%w: post=%d platform_account=%d",
				ErrPostTargetDuplicate, target.PostID, target.PlatformAccountID)
		}
		return fmt.Errorf("failed to save post_target: %w", err)
	}
	return nil
}

// SetProviderIdempotencyKey (Taglio 4.7 LEVEL 2, migration 022) writes
// the per-target provider-side idempotency key onto the post_target
// row. The worker calls this AFTER the atomic lease-aware claim
// (ClaimQueuedTargetWithLease) and BEFORE the publish call, so the key
// is stamped on the same row across retries (same input → same key via
// deterministic SHA-256 prefix).
//
// Behaviour:
//   - 23505 with constraint `post_targets_platform_provider_uniq` →
//     ErrProviderIdempotencyConflict (this account already has another
//     target with the same key; degenerate but exported so the caller
//     can log + skip rather than silently re-keying). In normal flow
//     this should not fire — the worker stamps a fresh key only when
//     the existing one is nil — but the typed dispatch is the safety net.
//   - 0 rows affected → ErrPostTargetNotFound.
//   - Anything else → wrapped generic error.
//
// On conflict, the WORKER treats it as a recoverable race: re-reads the
// target's existing key from ListByPost/ListPublishing and reuses it.
// The DB constraint is the authoritative safety net; the worker's
// resolve-on-conflict handling is the runtime mitigation.
func (r *PostRepository) SetProviderIdempotencyKey(id int64, key string) error {
	if key == "" {
		return fmt.Errorf("SetProviderIdempotencyKey: key is empty for post_target id=%d", id)
	}
	result, err := r.db.Exec(
		qUpdateTargetProviderIdempotencyKey,
		key, id,
	)
	if err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == "23505" && pqErr.Constraint == "post_targets_platform_provider_uniq" {
			return fmt.Errorf("%w: id=%d", ErrProviderIdempotencyConflict, id)
		}
		return fmt.Errorf("failed to set provider_idempotency_key: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to read rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: id=%d", ErrPostTargetNotFound, id)
	}
	return nil
}

// UpdateStatus mutates the lifecycle fields of a single post_target row.
// The worker's per-attempt flow is:
//
//	target status transition  scheduled → publishing → (published | failed)
//	on success:  PlatformPostID set, PublishedAt set
//	on failure:  ErrorMessage set
//
// target.ID identifies the row; every other field supplies the new values.
// Persists status, platform_post_id, error_message, published_at atomically
// (single UPDATE).
func (r *PostRepository) UpdateStatus(target *models.PostTarget) error {
	return r.updateStatusWithAggregate(target)
}

// ListByPost returns the full fan-out set for a given post, ordered by id
// ASC (insertion order). Returns (nil, nil) if the post has no targets
// (the empty slice path through Scan-loop). Includes the
// provider_idempotency_key column added by migration 022 — pre-022
// rows expose NULL. Includes the completed_at column added by migration
// 035 (SPRINT 5.2) so DLQ-triage queries can filter on terminal
// timestamps.

// FindTargetByID (Taglio 5.1 step 2 — POST /posts/{id}/targets
// polling endpoint contract) returns a single post_target by id.
// Returns (nil, nil) when no row matches the id. Reads the FULL
// retry-aware column set so the API layer can render attempt_count
// / next_retry_at / error_message without an N+1 fetch.
//
// The companion SQL qSelectTargetByID does NOT join posts on purpose:
// workspace isolation (workspace.OwnerID == userID) is the API
// layer's responsibility, called explicitly through r.postStore.FindByID.
// Two round-trips intentional — agrees with the existing handlerGetPost
// pattern in pkg/api/posts_handlers.go so we don't regress the IDOR
// guard with a "clever JOIN".
func (r *PostRepository) FindTargetByID(id int64) (*models.PostTarget, error) {
	t := &models.PostTarget{}
	err := r.db.QueryRow(qSelectTargetByID, id).Scan(
		&t.ID, &t.PostID, &t.PlatformAccountID, &t.Status,
		&t.PlatformPostID, &t.ErrorMessage, &t.PublishedAt,
		&t.ProviderState, &t.ContainerID, &t.ProviderIdempotencyKey, &t.CompletedAt,
		&t.AttemptCount, &t.NextAttemptAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to find post_target by id: %w", err)
	}
	return t, nil
}

func (r *PostRepository) ListByPost(postID int64) ([]models.PostTarget, error) {
	rows, err := r.db.Query(
		qSelectTargetsByPost,
		postID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list post_targets by post: %w", err)
	}
	defer rows.Close()

	var targets []models.PostTarget
	for rows.Next() {
		t := models.PostTarget{}
		if err := rows.Scan(&t.ID, &t.PostID, &t.PlatformAccountID, &t.Status,
			&t.PlatformPostID, &t.ErrorMessage, &t.PublishedAt,
			&t.ProviderState, &t.ContainerID, &t.ProviderIdempotencyKey, &t.CompletedAt); err != nil {
			return nil, fmt.Errorf("failed to scan post_target: %w", err)
		}
		targets = append(targets, t)
	}
	return targets, nil
}

// ListPublishing returns at most limit ready publishing targets whose
// next_reconcile_at is due. Targets without a non-empty provider publish ID
// and targets scheduled for the future remain invisible to the reconciler.
// These are the targets the reconciler
// goroutine needs to poll for async state transitions (TikTok's
// PROCESSING_UPLOAD → PUBLISH_COMPLETE flow).
//
// The non-null platform_post_id filter is essential: a target that
// transitions to 'publishing' but has not yet been assigned a
// publish_id (e.g. still in the synchronous Publish() call) must NOT
// be picked up by the reconciler — there's no publish_id to query
// status against.
//
// Ordered by id ASC for stable iteration; this lets the reconciler
// check the same target on every tick without flapping. Includes the
// provider_idempotency_key column added by migration 022 so retries
// from the reconciler reuse the same key already stamped at claim time.
// Includes the completed_at column added by migration 035 so the
// reconciler can detect rows that were DLQ'd while the reconciler
// held a stale read (defensive — ListPublishing filters on
// status='publishing' so DLQ'd rows are naturally excluded, but the
// field is included for consistency with ListByPost).
func (r *PostRepository) ListPublishing(limit int) ([]models.PostTarget, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("failed to list publishing post_targets: limit must be positive (got %d)", limit)
	}
	rows, err := r.db.Query(qSelectPublishingTargets, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to list publishing post_targets: %w", err)
	}
	defer rows.Close()

	var targets []models.PostTarget
	for rows.Next() {
		t := models.PostTarget{}
		if err := rows.Scan(
			&t.ID, &t.PostID, &t.PlatformAccountID, &t.Status,
			&t.PlatformPostID, &t.ErrorMessage, &t.PublishedAt,
			&t.ProviderState, &t.ContainerID, &t.ProviderIdempotencyKey,
			&t.CompletedAt, &t.ReconcileAttempt, &t.NextReconcileAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan post_target: %w", err)
		}
		targets = append(targets, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read publishing post_targets: %w", err)
	}
	return targets, nil
}

// ListPending returns post_targets whose status='queued' AND whose parent
// post is due (publish_at <= before). P1#4 — renamed from scheduled_at
// to publish_at. This is the worker's main pickup query, called
// periodically (e.g. every 30s) by the publishing worker.
//
// The JOIN with posts is essential: a target whose parent post is scheduled
// for tomorrow is NOT pending today. Without the JOIN we'd waste cycles
// re-checking and would still race on publish_at boundaries. Includes
// the provider_idempotency_key column added by migration 022 so the
// worker can read the existing key (preserved across retries) without
// an extra round-trip. Includes the completed_at column added by
// migration 035 (SPRINT 5.2) for consistency with ListByPost/ListPublishing.
func (r *PostRepository) ListPending(before time.Time) ([]models.PostTarget, error) {
	rows, err := r.db.Query(
		qSelectPendingTargetsFair,
		before,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list pending post_targets: %w", err)
	}
	defer rows.Close()

	var targets []models.PostTarget
	for rows.Next() {
		t := models.PostTarget{}
		if err := rows.Scan(&t.ID, &t.PostID, &t.PlatformAccountID, &t.Status,
			&t.PlatformPostID, &t.ErrorMessage, &t.PublishedAt,
			&t.ProviderState, &t.ContainerID, &t.ProviderIdempotencyKey, &t.CompletedAt); err != nil {
			return nil, fmt.Errorf("failed to scan post_target: %w", err)
		}
		targets = append(targets, t)
	}
	return targets, nil
}

// ScheduleNextReconcile advances the target's adaptive polling schedule.
// The status/readiness predicates make stale worker calls harmless after a
// terminal transition or provider ID loss.
func (r *PostRepository) ScheduleNextReconcile(id int64, ownerID string, expectedAttempt int, next time.Time) error {
	if id <= 0 {
		return fmt.Errorf("schedule next reconcile: target ID must be positive (got %d)", id)
	}
	if ownerID == "" {
		return fmt.Errorf("schedule next reconcile: ownerID must not be empty")
	}
	if expectedAttempt < 0 {
		return fmt.Errorf("schedule next reconcile: attempt must be non-negative (got %d)", expectedAttempt)
	}
	if next.IsZero() {
		return fmt.Errorf("schedule next reconcile: next time must be non-zero")
	}
	result, err := r.db.Exec(qScheduleNextReconcile, id, next, expectedAttempt, ownerID, true, "", "")
	if err != nil {
		return fmt.Errorf("schedule next reconcile for target %d: %w", id, err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("schedule next reconcile rows affected for target %d: %w", id, err)
	} else if affected == 0 {
		return fmt.Errorf("schedule next reconcile lost CAS for target %d", id)
	}
	return nil
}

// SaveTarget saves a post target.
func (r *PostRepository) SaveTarget(target *models.PostTarget) error {
	return r.Save(target)
}

// SetTargetStatus is a narrow atomic status-flip for a single
// post_target row, used by the upload worker's
// uploadVideoAsPrivateForTarget helper when channels.list(mine=true)
// returns a channel id other than platform_account.platform_user_id
// (the P0#3 channel-binding guard).
//
// Differs from UpdateStatus(target) in two ways:
//  1. Status-only update — does NOT touch platform_post_id /
//     error_message / provider_state / container_id. The full row
//     stamp is UpdateStatus's job; SetTargetStatus's job is the
//     narrow "flip to blocked_auth + stamp error_message" used by the
//     per-target upload phase.
//  2. Returns ErrPostTargetNotFound (NOT ErrPostUnauthorized) so the
//     upload worker's caller can distinguish a missing row from a
//     tenant boundary violation (UpdateStatus uses the latter because
//     its WHERE includes the workspace_id scope).
//
// errorMessage is COALESCE'd via NULLIF so an empty string preserves
// any existing error_message column (e.g. a prior failed attempt's
// prose). status itself is NOT NULL DB-side so unset status would
// fail at the SQL layer — the IsValid guard catches this at Go-side.
//
// CAS via version+1 mirrors the optimistic-concurrency contract
// introduced by migration 012 (post_targets.version). Bumping on
// every status flip keeps the row's revision counter monotonic for
// reconciler queries.
func (r *PostRepository) SetTargetStatus(ctx context.Context, targetID int64, status models.PostStatus, errorMessage string) error {
	if targetID <= 0 {
		return fmt.Errorf("post target SetTargetStatus: targetID must be positive (got %d)", targetID)
	}
	if !status.IsValid() {
		return fmt.Errorf("post target SetTargetStatus: status %q is not a valid PostStatus", status)
	}
	return r.setTargetStatusWithAggregate(ctx, targetID, status, errorMessage)
}
