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

// --- Posts ---

// Create inserts a new Post and, when targets is non-empty, its initial
// PostTargets inside a single explicit transaction. The auto-generated
// post.id and post.created_at are assigned back to post; the post_id of
// each target is filled in (silently overwriting any value the caller
// supplied — the relationship is owned by the parent insert).
//
// IngestAfter is auto-stamped to time.Now().UTC() when the caller passes
// the Go zero value `time.Time{}` (the common API-layer construction
// pattern of `&models.Post{}.fillInFields()`). Caller-supplied non-zero
// values are honoured verbatim as per-post overrides. The SQL column's
// NOT NULL DEFAULT NOW() remains as the safety-net for direct-SQL writers
// (psql scripts, admin tooling) that bypass this chokepoint. The dedicated
// regression test (zero-auto-stamp + override-verbatim branches) pins
// both sides of this contract.
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

	// P1#4 — ingest_after + publish_at replace the old scheduled_at
	// column (migration 049b). The contract for IngestAfter binding:
	//   * Caller-supplied non-zero value  → honour verbatim (per-post override).
	//   * Caller leaves time.Time{}       → fill with Go-side time.Now().UTC()
	//                                       so we never write the Go zero
	//                                       value '0001-01-01 00:00:00 UTC'
	//                                       into a NOT NULL column. The
	//                                       SQL column's DEFAULT NOW() remains
	//                                       as the safety net for direct-SQL
	//                                       writers (psql scripts, admin
	//                                       tools) that bypass this path.
	// The bound value is computed ONCE here so a clock-skew between Go's
	// time.Now() and Postgres' NOW() (NTP drift, typically <100ms) is the
	// SAME drift that qInsertPost already tolerates today. The gate mirrors
	// the prior docstring's intent ("we pass an explicit NOW() here…") that
	// the unconditional-time.Time binding silently violated for zero-init
	// callers.
	if post.IngestAfter.IsZero() {
		post.IngestAfter = time.Now().UTC()
	}

	// Insert the parent Post; capture auto-assigned id + created_at.
	// P1 (migration 053) — bind the inherited batch default + the explicit
	// per-post override. Order MUST match qInsertPost's column list; the
	// schema-side VALIDATE() of qInsertPost (via go vet) doesn't run on
	// raw SQL strings so order is a manual invariant here. A future
	// taglio can swap to a small struct-bound builder to compile-enforce
	// this.
	// Insert the parent Post; capture auto-assigned id + created_at.
	// P1 (migration 053) — bind the inherited batch default + the explicit
	// per-post override.
	// P1 (migration 077) — bind upload_job_id as $10 + Scan returns 3 cols
	// (id, created_at, upload_job_id). The partial unique index
	// uq_posts_upload_job_id enforces "one post per upload_job", so qInsertPost's
	// `ON CONFLICT ... DO NOTHING` clause surfaces sql.ErrNoRows via RETURNING
	// when a worker retry re-lands the same upload_job_id.
	// Order MUST match qInsertPost's column list; the schema-side VALIDATE()
	// of qInsertPost (via go vet) doesn't run on raw SQL strings so order
	// is a manual invariant here.
	err = tx.QueryRow(
		qInsertPost,
		post.WorkspaceID, post.Title, post.Caption, post.MediaURL,
		post.IngestAfter, post.PublishAt,
		post.DefaultPrivacyLevel, post.PrivacyLevel,
		post.Status,
		post.UploadJobID,
		// migration 080: 3 nullable canonical-source-of-truth columns.
		ns(post.MediaAssetID), ns(post.StorageObjectKey), ns(post.Bucket),
	).Scan(&post.ID, &post.CreatedAt, &post.UploadJobID)
	if err == sql.ErrNoRows {
		// P1 (migration 077) — ON CONFLICT (upload_job_id) WHERE
		// upload_job_id IS NOT NULL DO NOTHING fired: the canonical
		// row already exists (worker retry path). Rehydrate the
		// caller's struct + targets slice from the DB and commit.
		// Skipping the target/outbox INSERTs on the conflict path is
		// essential: those would 23505 on UNIQUE(post_id,
		// platform_account_id).
		//
		// post.UploadJobID is the caller's input arg (Scan didn't
		// execute so the pointer-to-nil-receive-via-Scan didn't
		// happen); the partial index WHERE upload_job_id IS NOT NULL
		// predicate guarantees we only get here with a non-nil
		// UploadJobID. The defensive nil-check is paranoia — if a future
		// SQL refactor removes the partial-index predicate, this branch
		// would surface the regression loudly rather than nil-deref.
		if post.UploadJobID == nil {
			return fmt.Errorf("post create: ON CONFLICT fired for nil upload_job_id (impossible per partial index predicate, but defensive): %w", err)
		}
		if err = r.fetchExistingByUploadJobID(tx, *post.UploadJobID, post, targets); err != nil {
			return err
		}
		if err = tx.Commit(); err != nil {
			return fmt.Errorf("failed to commit create-post tx (conflict path): %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to create post: %w", err)
	}
	if len(post.Metadata) > 0 {
		if _, err = tx.Exec(`UPDATE posts SET metadata = $1::jsonb WHERE id = $2`, post.Metadata, post.ID); err != nil {
			return fmt.Errorf("failed to persist post metadata: %w", err)
		}
	}

	// Insert each PostTarget, filling in target.PostID from the new post id.
	for _, t := range targets {
		t.PostID = post.ID
		err = tx.QueryRow(
			qInsertPostTarget,
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
		// P1#4 — the outbox payload now carries publish_at instead
		// of scheduled_at to keep the dispatcher contract consistent
		// with the post.publish_at column the worker reads in the
		// reconciler tick. ingest_after is computed at Create-time
		// server-side (DEFAULT NOW()) and is not part of the
		// publish-time payload — the worker doesn't need it; only
		// the snapshot "what time should this fire" cursor flows
		// downstream.
		payload, marshalErr := json.Marshal(map[string]any{
			"event_version":       "v1",
			"post_id":             post.ID,
			"target_id":           t.ID,
			"workspace_id":        post.WorkspaceID,
			"platform_account_id": t.PlatformAccountID,
			"publish_at":          post.PublishAt,
			"title":               post.Title,
			"caption":             post.Caption,
			"media_url":           post.MediaURL,
		})
		if marshalErr != nil {
			return fmt.Errorf("failed to marshal outbox payload for target %d: %w", t.ID, marshalErr)
		}
		_, err = tx.Exec(
			qInsertOutboxEvent,
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

// fetchExistingByUploadJobID (Blocco #1 P1 followup — migration 077) is
// the ON CONFLICT path handler for PostRepository.Create. When
// qInsertPost's `ON CONFLICT (upload_job_id) ... DO NOTHING` clause
// fires for a duplicate upload_job_id, Postgres returns 0 rows via
// RETURNING → QueryRow().Scan() surfaces sql.ErrNoRows. We treat
// ErrNoRows as "row already exists, rehydrate the caller's struct from
// the canonical DB row".
//
// Returns nil on success (caller's `post` + `targets` slices rehydrated
// from DB). Caller is responsible for committing the tx on success —
// deferred rollback in Create covers the error paths.
//
// Returns error if:
//   - The post SELECT returns 0 rows (race: row was deleted between
//     ON CONFLICT firing and the re-fetch). Extremely rare; surface
//     without silent retry, defer-rollback handles the cleanup.
//   - The fanout (scan count from post_targets) doesn't match the
//     caller's `targets` slice. A worker retry always builds the same
//     fanout so a mismatch is a real bug (operator SQL edit, a buggy
//     backfill, etc.). Escalate via error + (deferred) rollback so
//     the worker can surface the inconsistency to the operator.
//
// Stamps DB-derived fields onto caller's `targets` slice:
//   - targets[i].ID
//   - targets[i].PostID
//   - targets[i].Status (DB wins over caller's optimistic 'draft' —
//     typically 'queued' after upload_worker stamping).
//
// Other caller's `targets[i]` field mutations are preserved verbatim.
// The DB-stamps-are-authoritative contract applies to the parent `post`
// struct only (DB wins for all read-back fields).
//
// Run inside the same tx as the qInsertPost call so the re-fetch sees
// its own uncommitted state (and so the deferred rollback handles
// cleanup on any error path).
func (r *PostRepository) fetchExistingByUploadJobID(tx *sql.Tx, uploadJobID int64, post *models.Post, targets []*models.PostTarget) error {
	// SELECT the existing post row — same column order as the qSelectPost*
	// family (migration 053 + 077 shared canonical column list).
	err := tx.QueryRow(qSelectPostByUploadJobID, uploadJobID).Scan(
		&post.ID, &post.WorkspaceID, &post.Title, &post.Caption, &post.MediaURL,
		&post.IngestAfter, &post.PublishAt, &post.Status,
		&post.PrivacyLevel, &post.DefaultPrivacyLevel,
		&post.CreatedAt, &post.UploadJobID,
		&post.MediaAssetID, &post.StorageObjectKey, &post.Bucket,
	)
	if err == sql.ErrNoRows {
		return fmt.Errorf("post create: ON CONFLICT fired but no post row found for upload_job_id=%d (race detected — row deleted between CONFLICT and re-fetch): %w", uploadJobID, err)
	}
	if err != nil {
		return fmt.Errorf("post create: failed to re-fetch existing post by upload_job_id=%d: %w", uploadJobID, err)
	}

	// SELECT the existing target fan-out. Backed by the FK index with WHERE
	// post_id = $1 — O(log N) on the existing post's target count.
	rows, err := tx.Query(qSelectTargetsByPost, post.ID)
	if err != nil {
		return fmt.Errorf("post create: failed to re-fetch existing post_targets by post_id=%d: %w", post.ID, err)
	}
	defer rows.Close()

	var existing []models.PostTarget
	for rows.Next() {
		var t models.PostTarget
		if err := rows.Scan(&t.ID, &t.PostID, &t.PlatformAccountID, &t.Status,
			&t.PlatformPostID, &t.ErrorMessage, &t.PublishedAt,
			&t.ProviderState, &t.ContainerID, &t.ProviderIdempotencyKey, &t.CompletedAt); err != nil {
			return fmt.Errorf("post create: failed to scan re-fetched post_targets: %w", err)
		}
		existing = append(existing, t)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("post create: rows.Err() on re-fetch post_targets: %w", err)
	}

	// Safety guard: a mismatch is a real bug — escalate via error +
	// (deferred) rollback so the worker can surface the inconsistency.
	if len(existing) != len(targets) {
		return fmt.Errorf("post create: ON CONFLICT fanout mismatch (existing=%d vs caller=%d for upload_job_id=%d) — escalate: worker must rebuild post from canonical source",
			len(existing), len(targets), uploadJobID)
	}

	// Stamp DB-derived fields onto caller's targets. Order matters:
	// qSelectTargetsByPost orders by id ASC, so existing[i] is the canonical
	// pair for the i-th caller's target slot. The caller built its slice in
	// the same fan-out order the upload_job declared (the worker's
	// single-shot construction) so positional pairing is safe.
	for i := range targets {
		if targets[i] == nil {
			continue
		}
		targets[i].ID = existing[i].ID
		targets[i].PostID = existing[i].PostID
		targets[i].Status = existing[i].Status
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
	// P1#4 — qUpdatePost's UPDATE list now writes publish_at instead
	// of scheduled_at (queries.go). ingest_after is server-side DEFAULT
	// NOW() — caller can set post.IngestAfter explicitly to override,
	// otherwise the row keeps its prior value (the SQL UPDATE does not
	// touch ingest_after by design).
	// P1 (migration 053) — qUpdatePost now writes the privacy columns; arg
	// order MUST match the SET clause in queries.go.
	result, err := r.db.Exec(
		qUpdatePost,
		post.Title, post.Caption, post.MediaURL, post.PublishAt,
		post.PrivacyLevel, post.DefaultPrivacyLevel,
		post.Status,
		// migration 080: bind the 3 canonical-source-of-truth columns too.
		ns(post.MediaAssetID), ns(post.StorageObjectKey), ns(post.Bucket),
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
	return r.FindByIDForWorkspace(0, id)
}

// FindByIDForWorkspace (Blocco Carosello content-pipeline endpoint):
// returns the post with the given id SCOPED to the given workspace.
// (nil, nil) when no row matches EITHER id or workspace. The SQL
// predicate is `id = $2 AND workspace_id = $1` — the workspace is
// the FIRST predicate so the index-only lookup short-circuits on
// (workspace_id, id). Use this when the route is workspace-scoped
// (e.g. GET /api/v1/content/{id}/pipeline) so the tenant isolation
// check happens at the SQL layer rather than Go-side after a wider
// FindByID call. The legacy FindByID remains for the historical
// callers (the OAuth-attached flow, internal worker reads); it
// delegates to this method with workspace_id=0 (disabled predicate),
// preserving the existing behaviour.
func (r *PostRepository) FindByIDForWorkspace(workspaceID, postID int64) (*models.Post, error) {
	p := &models.Post{}
	err := r.db.QueryRow(
		`SELECT `+contentPipelineSelectColumns+`
		 FROM posts
		 WHERE ($1::bigint = 0 OR workspace_id = $1)
		   AND id = $2`,
		workspaceID, postID,
	).Scan(&p.ID, &p.WorkspaceID, &p.Title, &p.Caption, &p.MediaURL,
		&p.IngestAfter, &p.PublishAt, &p.Status,
		&p.PrivacyLevel, &p.DefaultPrivacyLevel,
		&p.CreatedAt, &p.UploadJobID,
		&p.MediaAssetID, &p.StorageObjectKey, &p.Bucket)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to find post by id (workspace-scoped): %w", err)
	}
	return p, nil
}

// ListByWorkspace returns every post in the given workspace, ordered by
// created_at DESC (most-recent first). Targets are NOT loaded — use
// ListByPost separately to fetch the fan-out set.
func (r *PostRepository) ListByWorkspace(workspaceID int64) ([]models.Post, error) {
	rows, err := r.db.Query(
		qSelectPostsByWorkspace,
		workspaceID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list posts by workspace: %w", err)
	}
	defer rows.Close()

	var posts []models.Post
	for rows.Next() {
		p := models.Post{}
		// P1 (migration 053) — also read the two new privacy columns; order
		// matches qSelectPostsByWorkspace's column list.
		if err := rows.Scan(&p.ID, &p.WorkspaceID, &p.Title, &p.Caption, &p.MediaURL,
			&p.IngestAfter, &p.PublishAt, &p.Status,
			&p.PrivacyLevel, &p.DefaultPrivacyLevel,
			&p.CreatedAt, &p.UploadJobID,
			&p.MediaAssetID, &p.StorageObjectKey, &p.Bucket); err != nil {
			return nil, fmt.Errorf("failed to scan post: %w", err)
		}
		posts = append(posts, p)
	}
	return posts, nil
}

// ListQueued returns posts whose status='queued' AND (publish_at IS NULL
// OR publish_at <= before). P1#4 — renamed from scheduled_at to
// publish_at. `before` is the cutoff time (typically time.Now()); passing
// it from Go (instead of using SQL NOW()) decouples the DB clock from
// the application clock, making the worker loop and tests fully deterministic.
func (r *PostRepository) ListQueued(before time.Time) ([]models.Post, error) {
	rows, err := r.db.Query(
		qSelectQueuedPosts,
		before,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list scheduled posts: %w", err)
	}
	defer rows.Close()

	var posts []models.Post
	for rows.Next() {
		p := models.Post{}
		// P1 (migration 053) — also read the two new privacy columns; order
		// matches qSelectQueuedPosts's column list.
		if err := rows.Scan(&p.ID, &p.WorkspaceID, &p.Title, &p.Caption, &p.MediaURL,
			&p.IngestAfter, &p.PublishAt, &p.Status,
			&p.PrivacyLevel, &p.DefaultPrivacyLevel,
			&p.CreatedAt, &p.UploadJobID,
			&p.MediaAssetID, &p.StorageObjectKey, &p.Bucket); err != nil {
			return nil, fmt.Errorf("failed to scan post: %w", err)
		}
		posts = append(posts, p)
	}
	return posts, nil
}

// Delete deletes a post by ID.
func (r *PostRepository) Delete(id int64) error {
	_, err := r.db.Exec(qDeletePost, id)
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
	if err = resetPostTargetsAndAggregateTx(tx, id, qPublishPostTargetsReset); err != nil {
		return err
	}
	return tx.Commit()
}

// CancelPost resets non-terminal targets to draft and derives the parent
// status from the complete target set in the same transaction. Terminal
// targets are preserved so cancellation cannot erase a published or failed
// destination, nor can it bypass the aggregate resolver.
func (r *PostRepository) CancelPost(id int64) error {
	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin cancel-post tx: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if err = resetPostTargetsAndAggregateTx(tx, id, qCancelPostTargetsReset); err != nil {
		return fmt.Errorf("failed to cancel post: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit cancel-post tx: %w", err)
	}
	return nil
}
