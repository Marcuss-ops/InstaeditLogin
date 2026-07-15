package repository

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/lib/pq"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// PostRepository handles CRUD operations for posts and post_targets — the
// scheduled-publishing fan-out domain introduced by migration 003.
//
// A Post belongs to a Workspace and represents one piece of content
// (idea → edit → publish pipeline). Each Post fans out to 1..N PostTargets
// (one row per platform_account), each tracking its own per-platform
// lifecycle. This lets the publishing worker record partial success (some
// platforms OK, others failed) without losing per-platform error context.
//
// Method style intentionally mirrors UserRepository / TokenRepository:
// no context.Context, not-found returns (nil, nil), errors wrapped with
// fmt.Errorf("%w", err). Transactions follow the named-err + defer-rollback
// pattern established by TokenRepository.SaveToken.
type PostRepository struct {
	db *sql.DB
}

// NewPostRepository creates a new PostRepository.
func NewPostRepository(db *sql.DB) *PostRepository {
	return &PostRepository{db: db}
}

// --- Posts ---

// Create inserts a new Post and, when targets is non-empty, its initial
// PostTargets inside a single explicit transaction. The auto-generated
// post.id and post.created_at are assigned back to post; the post_id of
// each target is filled in (silently overwriting any value the caller
// supplied — the relationship is owned by the parent insert).
//
// Empty targets is valid (e.g. a draft that will get targets later via
// Save). The transaction guarantees no orphan post is ever visible
// without its initial fan-out, and that a partial failure rolls back
// cleanly.
//
// Taglio 4.7 LEVEL 2: a duplicate target row at INSERT time (violating
// UNIQUE(post_id, platform_account_id) added by migration 022) aborts
// the transaction with ErrPostTargetDuplicate wrapped — the API layer
// maps to 409. The whole post insert also rolls back so the caller
// doesn't see an orphan post without its fan-out.
//
// Taglio 5.0 STEP 1: every target also gets a corresponding
// outbox_events row inserted in the SAME transaction. The outbox is
// the dispatcher's pickup queue; without same-tx atomicity, a process
// crash between the post insert and the outbox INSERT would leave a
// post with no publish intent — the canonical dual-write problem that
// the transactional outbox pattern eliminates.
//
//	post_target row → outbox_events row (one-to-one)
//	aggregate_type  = "post_target"
//	event_type      = "post_target.publish_requested"
//	aggregate_id    = target.ID (returned by RETURNING above)
//	payload         = JSON snapshot the dispatcher needs to materialise
//	                  a publish_job: post_id, target_id, workspace_id,
//	                  platform_account_id, scheduled_at, title, caption,
//	                  media_url. Caching these avoids a re-fetch of the
//	                  parent post in the dispatcher hot path.
//
// Empty posts (drafts target = []) emit ZERO outbox rows — there is no
// publish intent to enqueue. Future SAVE of an extra target via
// PostRepository.Save should ALSO write an outbox row in the same
// pattern; that follows a separate migration-change known as STEP 2.
func (r *PostRepository) Create(post *models.Post, targets []*models.PostTarget) error {
	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin create-post tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	// Insert the parent Post; capture auto-assigned id + created_at.
	err = tx.QueryRow(
		`INSERT INTO posts (workspace_id, title, caption, media_url, scheduled_at, status)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING id, created_at`,
		post.WorkspaceID, post.Title, post.Caption, post.MediaURL, post.ScheduledAt, post.Status,
	).Scan(&post.ID, &post.CreatedAt)
	if err != nil {
		return fmt.Errorf("failed to create post: %w", err)
	}

	// Insert each PostTarget, filling in target.PostID from the new post id.
	for _, t := range targets {
		t.PostID = post.ID
		err = tx.QueryRow(
			`INSERT INTO post_targets (post_id, platform_account_id, status)
			 VALUES ($1, $2, $3)
			 RETURNING id`,
			t.PostID, t.PlatformAccountID, t.Status,
		).Scan(&t.ID)
		if err != nil {
			var pqErr *pq.Error
			if errors.As(err, &pqErr) && pqErr.Code == "23505" && pqErr.Constraint == "post_targets_post_id_platform_uniq" {
				return fmt.Errorf("%w: post=%d platform_account=%d",
					ErrPostTargetDuplicate, t.PostID, t.PlatformAccountID)
			}
			return fmt.Errorf("failed to create post_target: %w", err)
		}
	}

	// Taglio 5.0 STEP 1: write the outbox event for each target in the
	// SAME transaction. The dispatcher (separate goroutine) will READ
	// from outbox_events (FOR UPDATE SKIP LOCKED) and materialise a
	// publish_job row + notify the worker to pick the target up.
	//
	// The payload is a JSON snapshot of the dispatcher's inputs so the
	// dispatcher NEVER has to re-read the parent post. (Re-reading
	// would require the dispatcher to hold a peer repo handle, and a
	// parent-post mutation between Create and the dispatch pickup would
	// leave the dispatcher acting on stale data.)
	for _, t := range targets {
		payload, marshalErr := json.Marshal(map[string]any{
			"event_version":       "v1",
			"post_id":             post.ID,
			"target_id":           t.ID,
			"workspace_id":        post.WorkspaceID,
			"platform_account_id": t.PlatformAccountID,
			"scheduled_at":        post.ScheduledAt,
			"title":               post.Title,
			"caption":             post.Caption,
			"media_url":           post.MediaURL,
		})
		if marshalErr != nil {
			return fmt.Errorf("failed to marshal outbox payload for target %d: %w", t.ID, marshalErr)
		}
		_, err = tx.Exec(
			`INSERT INTO outbox_events (aggregate_type, aggregate_id, event_type, payload)
			 VALUES ($1, $2, $3, $4::jsonb)`,
			"post_target", t.ID, "post_target.publish_requested", string(payload),
		)
		if err != nil {
			return fmt.Errorf("failed to insert outbox event for target %d: %w", t.ID, err)
		}
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit create-post tx: %w", err)
	}
	return nil
}

// Update persists the editable state of an existing post (title, caption,
// media_url, scheduled_at, status). workspace_id and created_at are
// intentionally NOT updated (immutable from this entrypoint).
//
// The WHERE clause includes both id AND workspace_id: the workspace_id
// lookup must match the post's actual workspace, acting as a tenant-isolation
// guard against any caller passing a post.id from a workspace they don't
// own.
func (r *PostRepository) Update(post *models.Post) error {
	result, err := r.db.Exec(
		`UPDATE posts
		 SET title = $1, caption = $2, media_url = $3, scheduled_at = $4, status = $5
		 WHERE id = $6 AND workspace_id = $7`,
		post.Title, post.Caption, post.MediaURL, post.ScheduledAt, post.Status,
		post.ID, post.WorkspaceID,
	)
	if err != nil {
		return fmt.Errorf("failed to update post: %w", err)
	}
	// RowsAffected = 0 means either id doesn't exist OR workspace_id doesn't
	// match (tenant-isolation miss). Surface as a real error so the API
	// layer can map to 404 instead of silently leaving stale state.
	// Used as ErrPostUnauthorized (not ErrPostNotFound) because the two
	// cases are indistinguishable from a single UPDATE statement; mapping
	// both to 404 via the sentinel prevents leaking workspace existence
	// to cross-tenant probes.
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to read rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: id=%d", ErrPostUnauthorized, post.ID)
	}
	return nil
}

// FindByID returns the post with the given id (without its targets),
// or (nil, nil) when no row matches. Use ListByPost for the target fan-out.
func (r *PostRepository) FindByID(id int64) (*models.Post, error) {
	p := &models.Post{}
	err := r.db.QueryRow(
		`SELECT id, workspace_id, title, caption, media_url, scheduled_at, status, created_at
		 FROM posts
		 WHERE id = $1`,
		id,
	).Scan(&p.ID, &p.WorkspaceID, &p.Title, &p.Caption, &p.MediaURL,
		&p.ScheduledAt, &p.Status, &p.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to find post by id: %w", err)
	}
	return p, nil
}

// ListByWorkspace returns every post in the given workspace, ordered by
// created_at DESC (most-recent first). Targets are NOT loaded — use
// ListByPost separately to fetch the fan-out set.
func (r *PostRepository) ListByWorkspace(workspaceID int64) ([]models.Post, error) {
	rows, err := r.db.Query(
		`SELECT id, workspace_id, title, caption, media_url, scheduled_at, status, created_at
		 FROM posts
		 WHERE workspace_id = $1
		 ORDER BY created_at DESC`,
		workspaceID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list posts by workspace: %w", err)
	}
	defer rows.Close()

	var posts []models.Post
	for rows.Next() {
		p := models.Post{}
		if err := rows.Scan(&p.ID, &p.WorkspaceID, &p.Title, &p.Caption, &p.MediaURL,
			&p.ScheduledAt, &p.Status, &p.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan post: %w", err)
		}
		posts = append(posts, p)
	}
	return posts, nil
}

// ListQueued returns posts whose status='queued' AND scheduled_at <=
// before. `before` is the cutoff time (typically time.Now()); passing it
// from Go (instead of using SQL NOW()) decouples the DB clock from the
// application clock, making the worker loop and tests fully deterministic.
func (r *PostRepository) ListQueued(before time.Time) ([]models.Post, error) {
	rows, err := r.db.Query(
		`SELECT id, workspace_id, title, caption, media_url, scheduled_at, status, created_at
		 FROM posts
		 WHERE status = 'queued' AND scheduled_at <= $1
		 ORDER BY scheduled_at ASC`,
		before,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list scheduled posts: %w", err)
	}
	defer rows.Close()

	var posts []models.Post
	for rows.Next() {
		p := models.Post{}
		if err := rows.Scan(&p.ID, &p.WorkspaceID, &p.Title, &p.Caption, &p.MediaURL,
			&p.ScheduledAt, &p.Status, &p.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan post: %w", err)
		}
		posts = append(posts, p)
	}
	return posts, nil
}

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
		`INSERT INTO post_targets (post_id, platform_account_id, status)
		 VALUES ($1, $2, $3)
		 RETURNING id`,
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
// row. The worker calls this AFTER the atomic ClaimQueuedTarget and
// BEFORE the publish call, so the key is stamped on the same row across
// retries (same input → same key via deterministic SHA-256 prefix).
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
		`UPDATE post_targets
		 SET provider_idempotency_key = $1
		 WHERE id = $2`,
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
	result, err := r.db.Exec(
		`UPDATE post_targets
		 SET status = $1, platform_post_id = $2, error_message = $3, published_at = $4,
		     provider_state = $6, container_id = $7
		 WHERE id = $5`,
		target.Status, target.PlatformPostID, target.ErrorMessage,
		target.PublishedAt, target.ID, target.ProviderState, target.ContainerID,
	)
	if err != nil {
		return fmt.Errorf("failed to update post_target status: %w", err)
	}
	// RowsAffected = 0 means the post_target id is stale or invalid. The
	// worker would otherwise see nil error and assume the transition
	// happened, leaving a ghost target in the pending queue. Sentinel
	// (ErrPostTargetNotFound) lets the worker drop the phantom attempt.
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to read rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("%w: id=%d", ErrPostTargetNotFound, target.ID)
	}
	return nil
}

// ClaimQueuedTarget atomically transitions a post_target from
// status='queued' to status='publishing' using SELECT FOR UPDATE
// SKIP LOCKED inside an explicit transaction. Returns true on claim
// success and false if the target was already claimed by another
// worker (row locked → SKIP LOCKED returns no rows) or the id is
// invalid (no row matches).
//
// Verdict §10 (FASE 1.1 — SKIP LOCKED): the SELECT FOR UPDATE SKIP
// LOCKED + UPDATE pattern inside a single explicit transaction
// guarantees that 2+ worker replicas racing the same row NEVER
// block on each other. The first worker to SELECT locks the row;
// the loser's SELECT returns immediately with no rows (SKIP
// LOCKED), and the function returns (false, nil) — no row-level
// wait, no deadlock risk, no connection-pool exhaustion under
// multi-replica contention.
//
// The explicit transaction is REQUIRED for FOR UPDATE to acquire
// a row lock (PostgreSQL only honours FOR UPDATE inside a
// transaction block). The tx is scoped to the claim operation
// only — BEGIN → SELECT FOR UPDATE SKIP LOCKED → UPDATE → COMMIT.
func (r *PostRepository) ClaimQueuedTarget(id int64) (bool, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return false, fmt.Errorf("failed to begin claim tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	// SELECT ... FOR UPDATE SKIP LOCKED: if another tx already holds
	// a row lock on this row, SKIP LOCKED returns immediately with
	// zero rows instead of blocking. The caller sees (false, nil)
	// and moves to the next target without stalling.
	var foundID int64
	err = tx.QueryRow(
		`SELECT id FROM post_targets
		 WHERE id = $1 AND status = 'queued'
		 FOR UPDATE SKIP LOCKED`,
		id,
	).Scan(&foundID)
	if err == sql.ErrNoRows {
		// Row either doesn't exist, isn't in 'queued' status, or is
		// locked by another tx — in all cases we didn't win the claim.
		_ = tx.Rollback()
		err = nil // prevent deferred double-rollback
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to select for update: %w", err)
	}

	// Row locked — we own it. Transition status.
	_, err = tx.Exec(
		`UPDATE post_targets SET status = 'publishing' WHERE id = $1`,
		id,
	)
	if err != nil {
		return false, fmt.Errorf("failed to update claimed target: %w", err)
	}

	if err = tx.Commit(); err != nil {
		return false, fmt.Errorf("failed to commit claim: %w", err)
	}
	return true, nil
}

// ClaimQueuedTargetWithLease (SPRINT 5.2, P1#10) extends ClaimQueuedTarget
// with a per-replica lease stamp so a crashed worker doesn't leak the
// row forever. The lease is a (lease_owner_id, leased_until) tuple;
// the heartbeat goroutine (UpdatePublishProgress) extends leased_until
// every heartbeat tick; ReclaimExpiredLeases (called by the
// reconciler) takes over rows whose leased_until <= NOW() and whose
// lease_owner_id is not the calling replica.
//
// The atomic UPDATE is the SAME shape as ClaimQueuedTarget — single
// SQL statement that flips status AND stamps the lease fields. The
// lease TTL is supplied as a duration; the SQL converts it to an
// INTERVAL via NOW() + $N * INTERVAL '1 second'.
//
// Returns true on claim success and false if:
//   - The row is locked by another tx (SKIP LOCKED).
//   - The row's status is not 'queued' (someone else already claimed).
//   - The id is invalid (no row matches).
//
// On success the caller is the SOLE owner of the row for at least
// `leaseTTL`. The heartbeat goroutine must extend the lease before
// `leaseTTL` elapses; failure to do so lets the reconciler
// reclaim the row on its next tick.
func (r *PostRepository) ClaimQueuedTargetWithLease(id int64, ownerID string, leaseTTL time.Duration) (bool, error) {
	if ownerID == "" {
		return false, fmt.Errorf("ClaimQueuedTargetWithLease: ownerID is empty")
	}
	if leaseTTL <= 0 {
		return false, fmt.Errorf("ClaimQueuedTargetWithLease: leaseTTL must be positive (got %v)", leaseTTL)
	}
	leaseSeconds := int(leaseTTL.Seconds())
	if leaseSeconds < 1 {
		leaseSeconds = 1
	}
	tx, err := r.db.Begin()
	if err != nil {
		return false, fmt.Errorf("failed to begin claim-with-lease tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	var foundID int64
	err = tx.QueryRow(
		`SELECT id FROM post_targets
		 WHERE id = $1 AND status = 'queued'
		 FOR UPDATE SKIP LOCKED`,
		id,
	).Scan(&foundID)
	if err == sql.ErrNoRows {
		_ = tx.Rollback()
		err = nil
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to select for update (lease): %w", err)
	}

	// Atomic claim + lease stamp. leased_until = NOW() + leaseTTL
	// (computed in seconds; works for TTLs from 1s to ~68 years).
	_, err = tx.Exec(
		`UPDATE post_targets
		 SET status = 'publishing',
		     lease_owner_id = $2,
		     leased_until = NOW() + ($3 || ' seconds')::INTERVAL,
		     heartbeat_at = NOW()
		 WHERE id = $1`,
		id, ownerID, fmt.Sprintf("%d", leaseSeconds),
	)
	if err != nil {
		return false, fmt.Errorf("failed to update claimed target with lease: %w", err)
	}

	if err = tx.Commit(); err != nil {
		return false, fmt.Errorf("failed to commit claim-with-lease: %w", err)
	}
	return true, nil
}

// UpdatePublishProgress (SPRINT 5.2) is the heartbeat goroutine's
// per-tick writer. CAS on lease_owner_id: only the row's current
// owner can stamp progress. Updates:
//   - upload_offset (bytes uploaded so far) — for chunked-upload
//     resume after a crash.
//   - provider_state (opaque platform state string) — for
//     observability of where the upload is on the platform side.
//   - heartbeat_at (now) — observability for the lease monitoring.
//   - leased_until (now + leaseTTL) — extends the lease for another
//     heartbeat cycle.
//
// Returns nil on success. If the CAS fails (lease_owner_id has
// changed → another replica took over via reclaim), the heartbeat
// goroutine exits silently — the next Mark* will also see the new
// owner and the heartbeat's stale writes would fail. This is the
// canonical "ownership transfer" race the user spec called out:
// the heartbeating replica stops writing when the lease flips.
func (r *PostRepository) UpdatePublishProgress(id int64, ownerID string, uploadOffset int64, providerState string, leaseTTL time.Duration) error {
	if ownerID == "" {
		return fmt.Errorf("UpdatePublishProgress: ownerID is empty")
	}
	if leaseTTL <= 0 {
		return fmt.Errorf("UpdatePublishProgress: leaseTTL must be positive (got %v)", leaseTTL)
	}
	leaseSeconds := int(leaseTTL.Seconds())
	if leaseSeconds < 1 {
		leaseSeconds = 1
	}
	_, err := r.db.Exec(
		`UPDATE post_targets
		 SET upload_offset = $3,
		     provider_state = $4,
		     heartbeat_at = NOW(),
		     leased_until = NOW() + ($5 || ' seconds')::INTERVAL
		 WHERE id = $1 AND lease_owner_id = $2`,
		id, ownerID, uploadOffset, providerState, fmt.Sprintf("%d", leaseSeconds),
	)
	if err != nil {
		return fmt.Errorf("failed to update publish progress: %w", err)
	}
	return nil
}

// ReleaseLease (SPRINT 5.2) clears the lease fields on a terminal
// transition (published|failed|dlq). CAS on lease_owner_id so only
// the current owner can release — a reclaimed row's new owner
// can't be clobbered by a stale release from the crashed original.
//
// Idempotent: returns nil on RowsAffected = 0 (the row is already
// lease-cleared, e.g. on a prior terminal write).
func (r *PostRepository) ReleaseLease(id int64, ownerID string) error {
	if ownerID == "" {
		return fmt.Errorf("ReleaseLease: ownerID is empty")
	}
	_, err := r.db.Exec(
		`UPDATE post_targets
		 SET lease_owner_id = NULL,
		     leased_until = NULL,
		     heartbeat_at = NULL
		 WHERE id = $1 AND lease_owner_id = $2`,
		id, ownerID,
	)
	if err != nil {
		return fmt.Errorf("failed to release lease: %w", err)
	}
	return nil
}

// MarkDeadLetter (SPRINT 5.2) transitions a target to status='dlq'
// when max_attempts is exhausted on a transient error, OR when a
// terminal-class error (4xx non-429) is classified. CAS on
// lease_owner_id + clear lease in the same UPDATE. The row is
// terminal: no further transitions, the publish driver and
// reconciler both filter status IN ('queued', 'waiting_provider',
// 'publishing') and therefore skip it.
//
// lastError is persisted to error_message for operator visibility.
// last_error_code is set to 'DLQ' for consistency with
// MarkRateLimited ('RATE_LIMITED') — dashboards can filter by
// stable code without parsing the human prose of error_message.
// completed_at is set to NOW() so the DLQ-triage query
// (WHERE status='dlq' AND completed_at > now() - interval '7d')
// can find recent rows. The webhook runtime (SPRINT 4.2) emits a
// post.failed event on this transition so the workspace owner's
// webhook endpoint gets notified.
func (r *PostRepository) MarkDeadLetter(id int64, ownerID string, lastError string) error {
	if ownerID == "" {
		return fmt.Errorf("MarkDeadLetter: ownerID is empty")
	}
	res, err := r.db.Exec(
		`UPDATE post_targets
		 SET status = 'dlq',
		     lease_owner_id = NULL,
		     leased_until = NULL,
		     heartbeat_at = NULL,
		     error_message = $3,
		     last_error_code = 'DLQ',
		     completed_at = NOW()
		 WHERE id = $1 AND lease_owner_id = $2`,
		id, ownerID, lastError,
	)
	if err != nil {
		return fmt.Errorf("failed to mark dead letter: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to read rows affected: %w", err)
	}
	if n == 0 {
		// Either id is stale, lease_owner_id changed, or the row
		// is already DLQ'd. Idempotent: not an error.
		return nil
	}
	return nil
}

// MarkRetrying (SPRINT 5.2) increments attempt_count + stamps
// next_retry_at + clears the lease. CAS on lease_owner_id. The
// publish driver re-picks the row on its next tick when
// next_retry_at <= NOW() (the existing ListPending filter is
// extended to include next_retry_at in commit 2's publish_worker
// rewrite).
//
// backoff is the AWS-decorrelated-jitter delay computed by the
// worker. The supplied time.Time is the next-attempt absolute
// timestamp (now + backoff).
func (r *PostRepository) MarkRetrying(id int64, ownerID string, lastError string, nextAttemptAt time.Time) error {
	if ownerID == "" {
		return fmt.Errorf("MarkRetrying: ownerID is empty")
	}
	res, err := r.db.Exec(
		`UPDATE post_targets
		 SET attempt_count = attempt_count + 1,
		     next_retry_at = $3,
		     lease_owner_id = NULL,
		     leased_until = NULL,
		     heartbeat_at = NULL,
		     error_message = $4
		 WHERE id = $1 AND lease_owner_id = $2`,
		id, ownerID, nextAttemptAt, lastError,
	)
	if err != nil {
		return fmt.Errorf("failed to mark retrying: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to read rows affected: %w", err)
	}
	if n == 0 {
		return nil
	}
	return nil
}

// MarkRateLimited (SPRINT 5.2) handles the platform's 429/Retry-After
// response. Stamps next_retry_at and rate_limit_reset_at to the
// platform's hint, clears the lease, and (critically) does NOT
// increment attempt_count. Rate-limit is not a fault — the
// platform explicitly told us when to come back, so retrying
// sooner is the right behavior. attempt_count stays bounded by
// actual transient failures (5xx, network), not by platform
// throttling.
//
// The publish driver re-picks the row when next_retry_at <= NOW()
// (the existing ListPending filter, when extended in commit 2,
// handles this). status stays 'queued' so the next claim is
// permitted by ClaimQueuedTargetWithLease's WHERE clause.
func (r *PostRepository) MarkRateLimited(id int64, ownerID string, retryAfter time.Time) error {
	if ownerID == "" {
		return fmt.Errorf("MarkRateLimited: ownerID is empty")
	}
	res, err := r.db.Exec(
		`UPDATE post_targets
		 SET next_retry_at = $3,
		     rate_limit_reset_at = $3,
		     lease_owner_id = NULL,
		     leased_until = NULL,
		     heartbeat_at = NULL,
		     last_error_code = 'RATE_LIMITED'
		 WHERE id = $1 AND lease_owner_id = $2`,
		id, ownerID, retryAfter,
	)
	if err != nil {
		return fmt.Errorf("failed to mark rate limited: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to read rows affected: %w", err)
	}
	if n == 0 {
		return nil
	}
	return nil
}

// ReclaimExpiredLeases (SPRINT 5.2) takes over rows whose lease
// expired. The reconciler calls this on every tick as the first
// step of its work loop. A row is reclaimable if:
//   - leased_until <= NOW() (the lease is past its TTL)
//   - lease_owner_id != $myWorkerID (I'm not the one holding the
//     expired lease; reclaiming my own would be a no-op)
//   - status IN ('publishing', 'queued') (DLQ and published are
//     terminal; the publish driver only picks up queued/waiting_provider)
//
// On reclaim the row is reset to status='queued' (a crashed
// mid-publish row becomes pending so the driver re-picks it),
// lease fields are cleared, and next_retry_at = NOW() so the
// driver picks it up immediately on the next tick (no
// next_retry_at wait for crash-recovery).
//
// NOTE: attempt_count is INTENTIONALLY NOT bumped on reclaim. A
// crash is not a "real" attempt — the platform never saw a publish
// call (or saw one that returned mid-flight). The next
// MarkRetrying on this row is the one that increments. This keeps
// attempt_count bounded by actual transient failures (5xx,
// network) and not by replica crash/restart cycles.
//
// Returns the number of rows reclaimed. A replica running
// ReclaimExpiredLeases with a unique myWorkerID can safely share
// the table with peers — the WHERE lease_owner_id != $myWorkerID
// filter ensures two replicas don't fight over the same row
// (the second replica's reclaim finds lease_owner_id = NULL
// already and is a no-op).
func (r *PostRepository) ReclaimExpiredLeases(myWorkerID string) (int64, error) {
	res, err := r.db.Exec(
		`UPDATE post_targets
		 SET status = 'queued',
		     lease_owner_id = NULL,
		     leased_until = NULL,
		     heartbeat_at = NULL,
		     next_retry_at = NOW()
		 WHERE leased_until IS NOT NULL
		   AND leased_until <= NOW()
		   AND lease_owner_id IS NOT NULL
		   AND lease_owner_id <> $1
		   AND status IN ('publishing', 'queued')`,
		myWorkerID,
	)
	if err != nil {
		return 0, fmt.Errorf("failed to reclaim expired leases: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to read rows affected: %w", err)
	}
	return n, nil
}

// ClaimWaitingProviderTarget atomically transitions a post_target from
// status='waiting_provider' to status='publishing' using SELECT FOR
// UPDATE SKIP LOCKED (same pattern as ClaimQueuedTarget — see that
// method's docstring for the FASE 1.1 rationale).
func (r *PostRepository) ClaimWaitingProviderTarget(id int64) (bool, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return false, fmt.Errorf("failed to begin claim-waiting tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	var foundID int64
	err = tx.QueryRow(
		`SELECT id FROM post_targets
		 WHERE id = $1 AND status = 'waiting_provider'
		 FOR UPDATE SKIP LOCKED`,
		id,
	).Scan(&foundID)
	if err == sql.ErrNoRows {
		_ = tx.Rollback()
		err = nil // prevent deferred double-rollback
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to select for update (waiting): %w", err)
	}

	_, err = tx.Exec(
		`UPDATE post_targets SET status = 'publishing' WHERE id = $1`,
		id,
	)
	if err != nil {
		return false, fmt.Errorf("failed to update claimed waiting target: %w", err)
	}

	if err = tx.Commit(); err != nil {
		return false, fmt.Errorf("failed to commit claim-waiting: %w", err)
	}
	return true, nil
}

// ClaimPublishingTarget (FASE 1.1 — SKIP LOCKED for ReconcileWorker)
// atomically claims a post_target that is in status='publishing' with
// a non-null platform_post_id. Uses the same SELECT FOR UPDATE SKIP
// LOCKED + UPDATE pattern as ClaimQueuedTarget.
//
// This is the reconciler's claim primitive: before calling
// AsyncPublisher.Reconcile, the reconciler claims the row so two
// reconciler replicas racing the same publishing target don't both
// spend an API call on the same publish_id. The first reconciler wins
// the claim; the loser sees (false, nil) and skips.
//
// Note: unlike ClaimQueuedTarget, this does NOT transition the status
// — the row stays in 'publishing'. The claim is a pure row-lock
// ownership check ("I'm working on this row, nobody else touch it")
// scoped to the duration of the transaction. The status transition
// (publishing → published|failed) is still done by UpdateStatus after
// Reconcile returns.
func (r *PostRepository) ClaimPublishingTarget(id int64) (bool, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return false, fmt.Errorf("failed to begin claim-publishing tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	var foundID int64
	err = tx.QueryRow(
		`SELECT id FROM post_targets
		 WHERE id = $1 AND status = 'publishing' AND platform_post_id IS NOT NULL AND platform_post_id <> ''
		 FOR UPDATE SKIP LOCKED`,
		id,
	).Scan(&foundID)
	if err == sql.ErrNoRows {
		_ = tx.Rollback()
		err = nil // prevent deferred double-rollback
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to select for update (publishing): %w", err)
	}

	// Claim acquired — commit the tx to release the row lock. The
	// reconciler proceeds with Reconcile OUTSIDE the tx because
	// holding a row lock across an HTTP call to a platform API
	// would be a connection-leak antipattern.
	//
	// IMPORTANT (FASE 1.1 design note): this is a BEST-EFFORT claim.
	// Between this COMMIT and the Reconcile API call, another
	// reconciler replica COULD claim the same row (the lock is
	// released). The claim reduces wasted API calls but does NOT
	// serialise Reconcile — two reconcilers racing the same row
	// may both call the platform. Terminal-state updates are
	// idempotent (same status, same UPDATE is a no-op), so this
	// is safe. The real protection against double-publish is the
	// post_targets.status column, not the claim.
	if err = tx.Commit(); err != nil {
		return false, fmt.Errorf("failed to commit claim-publishing: %w", err)
	}
	return true, nil
}

// ListByPost returns the full fan-out set for a given post, ordered by id
// ASC (insertion order). Returns (nil, nil) if the post has no targets
// (the empty slice path through Scan-loop). Includes the
// provider_idempotency_key column added by migration 022 — pre-022
// rows expose NULL. Includes the completed_at column added by migration
// 035 (SPRINT 5.2) so DLQ-triage queries can filter on terminal
// timestamps.
func (r *PostRepository) ListByPost(postID int64) ([]models.PostTarget, error) {
	rows, err := r.db.Query(
		`SELECT id, post_id, platform_account_id, status,
		        COALESCE(platform_post_id, ''), COALESCE(error_message, ''), published_at,
		        COALESCE(provider_state, ''), COALESCE(container_id, ''),
		        provider_idempotency_key, completed_at
		 FROM post_targets
		 WHERE post_id = $1
		 ORDER BY id ASC`,
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

// ListPublishing (Taglio 4.2) returns post_targets whose status='publishing'
// AND platform_post_id IS NOT NULL. These are the targets the reconciler
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
func (r *PostRepository) ListPublishing() ([]models.PostTarget, error) {
	rows, err := r.db.Query(
		`SELECT id, post_id, platform_account_id, status,
			        COALESCE(platform_post_id, ''), COALESCE(error_message, ''), published_at,
			        COALESCE(provider_state, ''), COALESCE(container_id, ''),
			        provider_idempotency_key, completed_at
			 FROM post_targets
			 WHERE status = 'publishing' AND platform_post_id IS NOT NULL AND platform_post_id <> ''
			 ORDER BY id ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list publishing post_targets: %w", err)
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

// ListPending returns post_targets whose status='queued' AND whose parent
// post is due (scheduled_at <= before). This is the worker's main pickup
// query, called periodically (e.g. every 30s) by the publishing worker.
//
// The JOIN with posts is essential: a target whose parent post is scheduled
// for tomorrow is NOT pending today. Without the JOIN we'd waste cycles
// re-checking and would still race on scheduled_at boundaries. Includes
// the provider_idempotency_key column added by migration 022 so the
// worker can read the existing key (preserved across retries) without
// an extra round-trip. Includes the completed_at column added by
// migration 035 (SPRINT 5.2) for consistency with ListByPost/ListPublishing.
func (r *PostRepository) ListPending(before time.Time) ([]models.PostTarget, error) {
	rows, err := r.db.Query(
		`SELECT pt.id, pt.post_id, pt.platform_account_id, pt.status,
		        COALESCE(pt.platform_post_id, ''), COALESCE(pt.error_message, ''), pt.published_at,
		        COALESCE(pt.provider_state, ''), COALESCE(pt.container_id, ''),
		        pt.provider_idempotency_key, pt.completed_at
		 FROM post_targets pt
		 JOIN posts p ON p.id = pt.post_id
		 WHERE (pt.status = 'queued' OR pt.status = 'waiting_provider') AND p.scheduled_at <= $1
		 ORDER BY p.scheduled_at ASC`,
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

// SaveTarget saves a post target.
func (r *PostRepository) SaveTarget(target *models.PostTarget) error {
	return r.Save(target)
}

// Delete deletes a post by ID.
func (r *PostRepository) Delete(id int64) error {
	_, err := r.db.Exec("DELETE FROM posts WHERE id = $1", id)
	if err != nil {
		return fmt.Errorf("failed to delete post: %w", err)
	}
	return nil
}

// PublishPost updates status to queued.
func (r *PostRepository) PublishPost(id int64) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.Exec("UPDATE posts SET status = 'queued' WHERE id = $1", id)
	if err != nil {
		return err
	}

	_, err = tx.Exec("UPDATE post_targets SET status = 'queued', error_message = '' WHERE post_id = $1", id)
	if err != nil {
		return err
	}

	return tx.Commit()
}

// CancelPost updates status to draft.
func (r *PostRepository) CancelPost(id int64) error {
	_, err := r.db.Exec("UPDATE posts SET status = 'draft' WHERE id = $1", id)
	if err != nil {
		return fmt.Errorf("failed to cancel post: %w", err)
	}
	return nil
}

// RetryPost transitions failed post back to queued.
func (r *PostRepository) RetryPost(id int64) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.Exec("UPDATE posts SET status = 'queued' WHERE id = $1", id)
	if err != nil {
		return err
	}

	_, err = tx.Exec("UPDATE post_targets SET status = 'queued', error_message = '' WHERE post_id = $1 AND status = 'failed'", id)
	if err != nil {
		return err
	}

	return tx.Commit()
}

// RetryTarget transitions failed target back to queued.
func (r *PostRepository) RetryTarget(id int64) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.Exec("UPDATE post_targets SET status = 'queued', error_message = '' WHERE id = $1", id)
	if err != nil {
		return err
	}

	_, err = tx.Exec("UPDATE posts SET status = 'queued' WHERE id = (SELECT post_id FROM post_targets WHERE id = $1)", id)
	if err != nil {
		return err
	}

	return tx.Commit()
}
