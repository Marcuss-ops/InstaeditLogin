package repository

import (
	"fmt"
	"time"

	"github.com/lib/pq"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// ListByWorkspacePage returns a lightweight, keyset-paginated post page.
// Ordering is deterministic on (created_at, id), newest first. The extra
// row is fetched only to compute hasMore; callers never receive it.
func (r *PostRepository) ListByWorkspacePage(workspaceID int64, afterTime *time.Time, afterID int64, limit int) ([]models.Post, bool, error) {
	return r.listByWorkspaceIDsPage([]int64{workspaceID}, afterTime, afterID, limit)
}

// ListByWorkspacesPage returns one keyset-paginated page across an already
// ownership-checked workspace set. It keeps the all-workspaces API path at
// one SQL query instead of issuing one unbounded query per workspace.
func (r *PostRepository) ListByWorkspacesPage(workspaceIDs []int64, afterTime *time.Time, afterID int64, limit int) ([]models.Post, bool, error) {
	return r.listByWorkspaceIDsPage(workspaceIDs, afterTime, afterID, limit)
}

func (r *PostRepository) listByWorkspaceIDsPage(workspaceIDs []int64, afterTime *time.Time, afterID int64, limit int) ([]models.Post, bool, error) {
	if len(workspaceIDs) == 0 {
		return []models.Post{}, false, nil
	}
	for _, workspaceID := range workspaceIDs {
		if workspaceID <= 0 {
			return nil, false, fmt.Errorf("list posts: invalid workspace id %d", workspaceID)
		}
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	var after interface{}
	if afterTime != nil {
		after = *afterTime
	}
	query := `SELECT id, workspace_id, title, caption, media_url, media_asset_id, storage_object_key, bucket,
        privacy_level, default_privacy_level, ingest_after, publish_at, status,
	        upload_job_id, created_at
	 FROM posts
	 WHERE workspace_id = ANY($1::bigint[])
	   AND ($2::timestamptz IS NULL OR (created_at, id) < ($2, $3))
	 ORDER BY created_at DESC, id DESC
	 LIMIT $4`
	args := []any{pq.Array(workspaceIDs), after, afterID, limit + 1}
	if len(workspaceIDs) == 1 {
		query = `SELECT id, workspace_id, title, caption, media_url, media_asset_id, storage_object_key, bucket,
        privacy_level, default_privacy_level, ingest_after, publish_at, status,
	        upload_job_id, created_at
	 FROM posts
	 WHERE workspace_id = $1
	   AND ($2::timestamptz IS NULL OR (created_at, id) < ($2, $3))
	 ORDER BY created_at DESC, id DESC
	 LIMIT $4`
		args = []any{workspaceIDs[0], after, afterID, limit + 1}
	}
	rows, err := r.db.Query(query, args...)
	if err != nil {
		return nil, false, fmt.Errorf("list paginated posts: %w", err)
	}
	defer rows.Close()
	posts := make([]models.Post, 0, limit+1)
	for rows.Next() {
		var post models.Post
		if err := rows.Scan(
			&post.ID, &post.WorkspaceID, &post.Title, &post.Caption, &post.MediaURL,
			&post.MediaAssetID, &post.StorageObjectKey, &post.Bucket,
			&post.PrivacyLevel, &post.DefaultPrivacyLevel, &post.IngestAfter,
			&post.PublishAt, &post.Status, &post.UploadJobID, &post.CreatedAt,
		); err != nil {
			return nil, false, fmt.Errorf("scan paginated post: %w", err)
		}
		posts = append(posts, post)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("iterate paginated posts: %w", err)
	}
	hasMore := len(posts) > limit
	if hasMore {
		posts = posts[:limit]
	}
	return posts, hasMore, nil
}
