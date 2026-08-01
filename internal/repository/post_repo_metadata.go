package repository

import (
	"database/sql"
	"encoding/json"
	"fmt"
)

// GetMetadata (Task 7/10) returns the post's metadata JSON column
// (or nil if the row has no metadata). Used by the publish_worker
// canary pre-flight to read post.Metadata.canary_upload without
// forcing FindByID to load the JSONB blob on every call.
func (r *PostRepository) GetMetadata(id int64) (json.RawMessage, error) {
	var meta sql.NullString
	err := r.db.QueryRow(
		`SELECT metadata FROM posts WHERE id = $1`,
		id,
	).Scan(&meta)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to load post metadata: %w", err)
	}
	if !meta.Valid {
		return nil, nil
	}
	return json.RawMessage(meta.String), nil
}

// SetTargetCanaryVideoID (Task 7/10) stamps the canary upload's
// video_id onto the post_target row so /admin/youtube/fleet_readiness
// can report per-channel canary coverage (Definition of Done step 10).
// Idempotent: empty videoID is a no-op.
func (r *PostRepository) SetTargetCanaryVideoID(targetID int64, videoID string) error {
	if videoID == "" {
		return nil
	}
	_, err := r.db.Exec(
		`UPDATE post_targets SET canary_video_id = $1, updated_at = NOW() WHERE id = $2`,
		videoID, targetID,
	)
	if err != nil {
		return fmt.Errorf("failed to set target canary video id: %w", err)
	}
	return nil
}
