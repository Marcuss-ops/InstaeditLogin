package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// UpdateAgentVideoProgress stores a compact execution-plane projection on the
// draft calendar event. The agent_run_step remains the durable status source.
func (r *PostRepository) UpdateAgentVideoProgress(ctx context.Context, workspaceID int64, postID int64, runID, stepID, remoteStatus string, remoteProgress *int, snapshot []byte) error {
	if !json.Valid(snapshot) {
		snapshot = []byte(`{}`)
	}
	var metadata json.RawMessage
	if err := r.db.QueryRowContext(ctx, `
		SELECT metadata FROM posts
		WHERE id=$1 AND workspace_id=$2
		  AND metadata->>'agent_run_id'=$3 AND metadata->>'agent_step_id'=$4`,
		postID, workspaceID, runID, stepID).Scan(&metadata); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrAgentRunNotFound
		}
		return fmt.Errorf("agent video event progress lookup: %w", err)
	}
	var projected map[string]json.RawMessage
	if json.Unmarshal(snapshot, &projected) != nil {
		projected = map[string]json.RawMessage{}
	}
	phase := remoteStatus
	for _, key := range []string{"current_stage", "current_step", "phase", "current_phase"} {
		var value string
		if json.Unmarshal(projected[key], &value) == nil && value != "" {
			phase = value
			break
		}
	}
	update := map[string]any{"generation_status": remoteStatus, "generation_phase": phase, "generation_snapshot": json.RawMessage(snapshot)}
	if remoteProgress != nil {
		update["generation_progress"] = *remoteProgress
	}
	encoded, err := json.Marshal(update)
	if err != nil {
		return fmt.Errorf("encode agent video event progress: %w", err)
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE posts SET metadata=COALESCE(metadata,'{}'::jsonb) || $1::jsonb
		WHERE id=$2 AND workspace_id=$3
		  AND metadata->>'agent_run_id'=$4 AND metadata->>'agent_step_id'=$5`,
		string(encoded), postID, workspaceID, runID, stepID)
	if err != nil {
		return fmt.Errorf("update agent video event progress: %w", err)
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return ErrAgentRunNotFound
	}
	return nil
}

// FinalizeAgentVideoEvent transforms the existing scheduled draft into a real
// queued post and writes target fan-out plus outbox events atomically.
func (r *PostRepository) FinalizeAgentVideoEvent(post *models.Post, targets []*models.PostTarget) error {
	if post == nil || post.ID <= 0 || post.WorkspaceID <= 0 || len(targets) == 0 || post.MediaAssetID == nil || post.StorageObjectKey == nil {
		return errors.New("generated video event requires an existing post, media asset and targets")
	}
	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("begin generated video event finalization: %w", err)
	}
	defer tx.Rollback()
	var status models.PostStatus
	err = tx.QueryRow(`SELECT status FROM posts WHERE id=$1 AND workspace_id=$2 FOR UPDATE`, post.ID, post.WorkspaceID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrAgentRunNotFound
	}
	if err != nil {
		return fmt.Errorf("lock generated video calendar event: %w", err)
	}
	if status == models.PostStatusQueued {
		return tx.Commit()
	}
	if status != models.PostStatusDraft {
		return fmt.Errorf("generated video calendar event cannot transition from %q", status)
	}
	if len(post.Metadata) == 0 || !json.Valid(post.Metadata) {
		return errors.New("generated video metadata must be valid JSON")
	}
	if _, err = tx.Exec(`
		UPDATE posts SET title=$1, caption=$2, media_url=$3, publish_at=$4,
			privacy_level=$5, default_privacy_level=$6, status=$7,
			media_asset_id=$8, storage_object_key=$9, bucket=$10, metadata=$11::jsonb
		WHERE id=$12 AND workspace_id=$13`,
		post.Title, post.Caption, post.MediaURL, post.PublishAt,
		post.PrivacyLevel, post.DefaultPrivacyLevel, models.PostStatusQueued,
		ns(post.MediaAssetID), ns(post.StorageObjectKey), ns(post.Bucket), string(post.Metadata), post.ID, post.WorkspaceID); err != nil {
		return fmt.Errorf("update generated video calendar event: %w", err)
	}
	for _, target := range targets {
		target.PostID = post.ID
		if err = tx.QueryRow(qInsertPostTarget, target.PostID, target.PlatformAccountID, models.PostStatusQueued).Scan(&target.ID); err != nil {
			return fmt.Errorf("insert generated video target: %w", err)
		}
		payload, marshalErr := json.Marshal(map[string]any{
			"event_version": "v1", "post_id": post.ID, "target_id": target.ID,
			"workspace_id": post.WorkspaceID, "platform_account_id": target.PlatformAccountID,
			"publish_at": post.PublishAt, "title": post.Title, "caption": post.Caption,
			"media_url": post.MediaURL,
		})
		if marshalErr != nil {
			return fmt.Errorf("marshal generated video outbox payload: %w", marshalErr)
		}
		if _, err = tx.Exec(qInsertOutboxEvent, "post_target", target.ID, "post_target.publish_requested", string(payload)); err != nil {
			return fmt.Errorf("insert generated video outbox event: %w", err)
		}
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit generated video event: %w", err)
	}
	post.Status = models.PostStatusQueued
	return nil
}
