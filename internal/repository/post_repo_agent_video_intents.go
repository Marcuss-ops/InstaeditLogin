package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// CreateAgentVideoIntent persists the Calendar draft that represents a
// future video generation. Migration 136 gives its schedule key a unique
// workspace-scoped constraint.
func (r *PostRepository) CreateAgentVideoIntent(post *models.Post) error {
	return r.Create(post, nil)
}

func (r *PostRepository) FindAgentVideoIntentByKey(ctx context.Context, workspaceID int64, key string) (*models.Post, error) {
	return r.scanAgentVideoIntent(r.db.QueryRowContext(ctx, agentVideoIntentSelect+`
		WHERE workspace_id=$1 AND metadata->>'agent_schedule_key'=$2`, workspaceID, key))
}

func (r *PostRepository) FindAgentVideoIntentByID(ctx context.Context, workspaceID, postID int64) (*models.Post, error) {
	return r.scanAgentVideoIntent(r.db.QueryRowContext(ctx, agentVideoIntentSelect+` WHERE workspace_id=$1 AND id=$2 AND metadata->>'agent_video_intent'='true'`, workspaceID, postID))
}

func (r *PostRepository) ListDueAgentVideoIntents(ctx context.Context, now time.Time, limit int) ([]models.Post, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := r.db.QueryContext(ctx, agentVideoIntentSelect+`
		WHERE metadata->>'agent_video_intent'='true'
		  AND metadata->>'generation_at' IS NOT NULL
		  AND (metadata->>'generation_at')::timestamptz <= $1
		  AND metadata->>'generation_status' IN ('SCHEDULED','DISPATCHING')
		  AND (metadata->>'generation_status'='SCHEDULED'
		       OR COALESCE((metadata->>'generation_dispatch_lease_at')::timestamptz, 'epoch'::timestamptz) < $1 - interval '2 minutes')
		ORDER BY (metadata->>'generation_at')::timestamptz, id LIMIT $2`, now, limit)
	if err != nil {
		return nil, fmt.Errorf("list due agent video intents: %w", err)
	}
	defer rows.Close()
	items := make([]models.Post, 0)
	for rows.Next() {
		var post models.Post
		var metadata []byte
		if err := rows.Scan(&post.ID, &post.WorkspaceID, &post.Title, &post.Caption, &post.MediaURL, &post.IngestAfter, &post.PublishAt, &post.Status, &post.PrivacyLevel, &post.DefaultPrivacyLevel, &post.CreatedAt, &post.UploadJobID, &post.MediaAssetID, &post.StorageObjectKey, &post.Bucket, &metadata); err != nil {
			return nil, fmt.Errorf("scan due agent video intent: %w", err)
		}
		post.Metadata = metadata
		items = append(items, post)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate due agent video intents: %w", err)
	}
	return items, nil
}

// ClaimAgentVideoIntent leases a due event. Expired DISPATCHING leases can
// be reclaimed after a process restart; stable run and step keys make replay
// safe if submission succeeded before the process stopped.
func (r *PostRepository) ClaimAgentVideoIntent(ctx context.Context, workspaceID, postID int64, now time.Time) (bool, error) {
	result, err := r.db.ExecContext(ctx, `
		UPDATE posts SET metadata=COALESCE(metadata,'{}'::jsonb) || jsonb_build_object(
			'generation_status','DISPATCHING','generation_phase','DISPATCHING',
			'generation_dispatch_lease_at',$3::timestamptz)
		WHERE id=$1 AND workspace_id=$2 AND status='draft'
		  AND metadata->>'agent_video_intent'='true'
		  AND (metadata->>'generation_at')::timestamptz <= $3
		  AND (metadata->>'generation_status'='SCHEDULED'
		       OR (metadata->>'generation_status'='DISPATCHING'
		           AND COALESCE((metadata->>'generation_dispatch_lease_at')::timestamptz,'epoch'::timestamptz) < $3 - interval '2 minutes'))`, postID, workspaceID, now)
	if err != nil {
		return false, fmt.Errorf("claim agent video intent: %w", err)
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

func (r *PostRepository) LinkAgentVideoIntent(ctx context.Context, workspaceID, postID int64, runID, stepID string) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE posts SET metadata=COALESCE(metadata,'{}'::jsonb) || jsonb_build_object(
			'agent_run_id',$3,'agent_step_id',$4,'generation_status','QUEUED','generation_phase','QUEUED')
		WHERE id=$1 AND workspace_id=$2 AND metadata->>'agent_video_intent'='true'
		  AND (metadata->>'generation_status' IN ('DISPATCHING','SCHEDULED')
		       OR (metadata->>'generation_status'='QUEUED'
		           AND metadata->>'agent_run_id'=$3 AND metadata->>'agent_step_id'=$4))`, postID, workspaceID, runID, stepID)
	if err != nil {
		return fmt.Errorf("link scheduled video intent to run: %w", err)
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return ErrAgentRunNotFound
	}
	return nil
}

func (r *PostRepository) UpdateAgentVideoIntentSchedule(ctx context.Context, workspaceID, postID int64, publishAt, generationAt time.Time, payload []byte) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE posts SET publish_at=$3, metadata=COALESCE(metadata,'{}'::jsonb) || jsonb_build_object('generation_at',$4::timestamptz,'generation_payload',$5::jsonb)
		WHERE id=$1 AND workspace_id=$2 AND status='draft' AND metadata->>'agent_video_intent'='true' AND metadata->>'generation_status'='SCHEDULED'`, postID, workspaceID, publishAt, generationAt, string(payload))
	if err != nil {
		return fmt.Errorf("reschedule agent video intent: %w", err)
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return ErrAgentRunNotFound
	}
	return nil
}

func (r *PostRepository) RunAgentVideoIntentNow(ctx context.Context, workspaceID, postID int64, now time.Time) error {
	result, err := r.db.ExecContext(ctx, `UPDATE posts SET metadata=COALESCE(metadata,'{}'::jsonb) || jsonb_build_object('generation_at',$3::timestamptz) WHERE id=$1 AND workspace_id=$2 AND status='draft' AND metadata->>'agent_video_intent'='true' AND metadata->>'generation_status'='SCHEDULED'`, postID, workspaceID, now)
	if err != nil {
		return fmt.Errorf("run agent video intent now: %w", err)
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return ErrAgentRunNotFound
	}
	return nil
}

func (r *PostRepository) CancelAgentVideoIntent(ctx context.Context, workspaceID, postID int64) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE posts SET metadata=COALESCE(metadata,'{}'::jsonb) || jsonb_build_object('generation_status','CANCELLED','generation_phase','CANCELLED')
		WHERE id=$1 AND workspace_id=$2 AND status='draft' AND metadata->>'agent_video_intent'='true' AND metadata->>'generation_status'='SCHEDULED'`, postID, workspaceID)
	if err != nil {
		return fmt.Errorf("cancel agent video intent: %w", err)
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return ErrAgentRunNotFound
	}
	return nil
}

func (r *PostRepository) FailAgentVideoIntent(ctx context.Context, workspaceID, postID int64, message string) error {
	result, err := r.db.ExecContext(ctx, `UPDATE posts SET metadata=COALESCE(metadata,'{}'::jsonb) || jsonb_build_object('generation_status','FAILED','generation_phase','FAILED','generation_snapshot',jsonb_build_object('phase','FAILED','error',$3)) WHERE id=$1 AND workspace_id=$2 AND metadata->>'agent_video_intent'='true' AND metadata->>'generation_status' IN ('DISPATCHING','SCHEDULED')`, postID, workspaceID, message)
	if err != nil {
		return fmt.Errorf("fail agent video intent: %w", err)
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return ErrAgentRunNotFound
	}
	return nil
}

const agentVideoIntentSelect = `SELECT id, workspace_id, title, caption, media_url, ingest_after, publish_at, status, privacy_level, default_privacy_level, created_at, upload_job_id, media_asset_id, storage_object_key, bucket, COALESCE(metadata,'{}'::jsonb) FROM posts `

func (r *PostRepository) scanAgentVideoIntent(row *sql.Row) (*models.Post, error) {
	var post models.Post
	var metadata []byte
	err := row.Scan(&post.ID, &post.WorkspaceID, &post.Title, &post.Caption, &post.MediaURL, &post.IngestAfter, &post.PublishAt, &post.Status, &post.PrivacyLevel, &post.DefaultPrivacyLevel, &post.CreatedAt, &post.UploadJobID, &post.MediaAssetID, &post.StorageObjectKey, &post.Bucket, &metadata)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan agent video intent: %w", err)
	}
	post.Metadata = json.RawMessage(metadata)
	return &post, nil
}
