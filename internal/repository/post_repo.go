package repository

import (
	"database/sql"
	"fmt"
	"time"

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
			return fmt.Errorf("failed to create post_target: %w", err)
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

// ListScheduled returns posts whose status='queued' AND scheduled_at <=
// before. `before` is the cutoff time (typically time.Now()); passing it
// from Go (instead of using SQL NOW()) decouples the DB clock from the
// application clock, making the worker loop and tests fully deterministic.
func (r *PostRepository) ListScheduled(before time.Time) ([]models.Post, error) {
	rows, err := r.db.Query(
		`SELECT id, workspace_id, title, caption, media_url, scheduled_at, status, created_at
		 FROM posts
		 WHERE status = 'scheduled' AND scheduled_at <= $1
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
func (r *PostRepository) Save(target *models.PostTarget) error {
	err := r.db.QueryRow(
		`INSERT INTO post_targets (post_id, platform_account_id, status)
		 VALUES ($1, $2, $3)
		 RETURNING id`,
		target.PostID, target.PlatformAccountID, target.Status,
	).Scan(&target.ID)
	if err != nil {
		return fmt.Errorf("failed to save post_target: %w", err)
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
// status='queued' to status='publishing', returning true on claim
// success and false if the target was already claimed by another
// worker (or the id is invalid).
//
// Verdict §10: this is the atomic-claim primitive that unblocks
// running 2+ worker replicas without double-publishes. The single
// UPDATE statement uses WHERE status='queued' as a logical
// lock — the database's row-level locking guarantees that exactly
// one worker's UPDATE returns RowsAffected==1 and the rest return
// RowsAffected==0 (the row is no longer in 'scheduled' state from
// the loser's perspective). The 'publishing' row is then invisible
// to the next ListPending sweep (which filters status='queued'),
// so the losing worker never re-picks it.
func (r *PostRepository) ClaimQueuedTarget(id int64) (bool, error) {
	result, err := r.db.Exec(
		`UPDATE post_targets
		 SET status = 'publishing'
		 WHERE id = $1 AND status = 'scheduled'`,
		id,
	)
	if err != nil {
		return false, fmt.Errorf("failed to claim post_target: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("failed to read rows affected: %w", err)
	}
	return n == 1, nil
}

// ClaimWaitingProviderTarget atomically transitions a post_target from
// status='waiting_provider' to status='publishing'.
func (r *PostRepository) ClaimWaitingProviderTarget(id int64) (bool, error) {
	result, err := r.db.Exec(
		`UPDATE post_targets
		 SET status = 'publishing'
		 WHERE id = $1 AND status = 'waiting_provider'`,
		id,
	)
	if err != nil {
		return false, fmt.Errorf("failed to claim waiting target: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("failed to read rows affected: %w", err)
	}
	return n == 1, nil
}

// ListByPost returns the full fan-out set for a given post, ordered by id
// ASC (insertion order). Returns (nil, nil) if the post has no targets
// (the empty slice path through Scan-loop).
func (r *PostRepository) ListByPost(postID int64) ([]models.PostTarget, error) {
	rows, err := r.db.Query(
		`SELECT id, post_id, platform_account_id, status, platform_post_id, error_message, published_at,
		       provider_state, container_id
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
			&t.ProviderState, &t.ContainerID); err != nil {
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
// check the same target on every tick without flapping.
func (r *PostRepository) ListPublishing() ([]models.PostTarget, error) {
	rows, err := r.db.Query(
		`SELECT id, post_id, platform_account_id, status, platform_post_id, error_message, published_at,
		        provider_state, container_id
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
			&t.ProviderState, &t.ContainerID); err != nil {
			return nil, fmt.Errorf("failed to scan post_target: %w", err)
		}
		targets = append(targets, t)
	}
	return targets, nil
}

// UpdatePublishState (Taglio 4.2) updates only the provider_state column
// on a post_target. Used by the reconciler to record the current
// platform-specific state (PROCESSING_UPLOAD / PENDING_PUBLISH /
// IN_REVIEW) on every CheckPublishStatus call without triggering a
// full status transition. Idempotent.
func (r *PostRepository) UpdatePublishState(id int64, providerState string) error {
	result, err := r.db.Exec(
		`UPDATE post_targets SET provider_state = $1 WHERE id = $2`,
		providerState, id,
	)
	if err != nil {
		return fmt.Errorf("failed to update provider_state: %w", err)
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

// ListPending returns post_targets whose status='queued' AND whose parent
// post is due (scheduled_at <= before). This is the worker's main pickup
// query, called periodically (e.g. every 30s) by the publishing worker.
//
// The JOIN with posts is essential: a target whose parent post is scheduled
// for tomorrow is NOT pending today. Without the JOIN we'd waste cycles
// re-checking and would still race on scheduled_at boundaries.
func (r *PostRepository) ListPending(before time.Time) ([]models.PostTarget, error) {
	rows, err := r.db.Query(
		`SELECT pt.id, pt.post_id, pt.platform_account_id, pt.status,
		        pt.platform_post_id, pt.error_message, pt.published_at,
		        pt.provider_state, pt.container_id
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
			&t.ProviderState, &t.ContainerID); err != nil {
			return nil, fmt.Errorf("failed to scan post_target: %w", err)
		}
		targets = append(targets, t)
	}
	return targets, nil
}
