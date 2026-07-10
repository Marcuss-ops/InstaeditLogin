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
	// layer can map to 404/403 instead of silently leaving stale state.
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to read rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("post not found or unauthorized (id=%d, workspace_id=%d)", post.ID, post.WorkspaceID)
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

// ListScheduled returns posts whose status='scheduled' AND scheduled_at <=
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
		 SET status = $1, platform_post_id = $2, error_message = $3, published_at = $4
		 WHERE id = $5`,
		target.Status, target.PlatformPostID, target.ErrorMessage,
		target.PublishedAt, target.ID,
	)
	if err != nil {
		return fmt.Errorf("failed to update post_target status: %w", err)
	}
	// RowsAffected = 0 means the post_target id is stale or invalid. The
	// worker would otherwise see nil error and assume the transition
	// happened, leaving a ghost target in the pending queue.
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to read rows affected: %w", err)
	}
	if n == 0 {
		return fmt.Errorf("post_target not found (id=%d)", target.ID)
	}
	return nil
}

// ListByPost returns the full fan-out set for a given post, ordered by id
// ASC (insertion order). Returns (nil, nil) if the post has no targets
// (the empty slice path through Scan-loop).
func (r *PostRepository) ListByPost(postID int64) ([]models.PostTarget, error) {
	rows, err := r.db.Query(
		`SELECT id, post_id, platform_account_id, status, platform_post_id, error_message, published_at
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
			&t.PlatformPostID, &t.ErrorMessage, &t.PublishedAt); err != nil {
			return nil, fmt.Errorf("failed to scan post_target: %w", err)
		}
		targets = append(targets, t)
	}
	return targets, nil
}

// ListPending returns post_targets whose status='scheduled' AND whose parent
// post is due (scheduled_at <= before). This is the worker's main pickup
// query, called periodically (e.g. every 30s) by the publishing worker.
//
// The JOIN with posts is essential: a target whose parent post is scheduled
// for tomorrow is NOT pending today. Without the JOIN we'd waste cycles
// re-checking and would still race on scheduled_at boundaries.
func (r *PostRepository) ListPending(before time.Time) ([]models.PostTarget, error) {
	rows, err := r.db.Query(
		`SELECT pt.id, pt.post_id, pt.platform_account_id, pt.status,
		        pt.platform_post_id, pt.error_message, pt.published_at
		 FROM post_targets pt
		 JOIN posts p ON p.id = pt.post_id
		 WHERE pt.status = 'scheduled' AND p.scheduled_at <= $1
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
			&t.PlatformPostID, &t.ErrorMessage, &t.PublishedAt); err != nil {
			return nil, fmt.Errorf("failed to scan post_target: %w", err)
		}
		targets = append(targets, t)
	}
	return targets, nil
}
