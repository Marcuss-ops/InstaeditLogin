package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

type WorkerCalendarEventRef struct {
	WorkspaceID int64
	EventKey    string
	JobID       string
	Kind        string
}

func (r *PostRepository) ListActiveWorkerCalendarEvents(ctx context.Context, limit int) ([]WorkerCalendarEventRef, error) {
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	rows, err := r.db.QueryContext(ctx, `SELECT workspace_id, metadata->>'worker_event_key', metadata->>'worker_remote_job_id', metadata->>'generation_kind'
		FROM posts WHERE metadata->>'worker_calendar_event'='true'
		AND COALESCE(metadata->>'generation_status','QUEUED') NOT IN ('SUCCEEDED','COMPLETED','FAILED','CANCELLED')
		AND COALESCE(metadata->>'worker_remote_job_id','')<>'' ORDER BY updated_at ASC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list active worker calendar events: %w", err)
	}
	defer rows.Close()
	items := make([]WorkerCalendarEventRef, 0)
	for rows.Next() {
		var item WorkerCalendarEventRef
		if err := rows.Scan(&item.WorkspaceID, &item.EventKey, &item.JobID, &item.Kind); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// CreateWorkerCalendarEvent uses the event-key unique index as a native UPSERT.
// Replays return the existing post ID; changing the linked remote job resets
// the initial progress snapshot for that new execution.
func (r *PostRepository) CreateWorkerCalendarEvent(post *models.Post) (*models.Post, bool, error) {
	if post == nil || post.WorkspaceID <= 0 || len(post.Metadata) == 0 || !json.Valid(post.Metadata) {
		return nil, false, errors.New("worker calendar event requires workspace and valid metadata")
	}
	if _, _, err := workerCalendarIdentity(post.Metadata); err != nil {
		return nil, false, err
	}
	if post.IngestAfter.IsZero() {
		post.IngestAfter = time.Now().UTC()
	}
	var wasCreated bool
	err := r.db.QueryRowContext(context.Background(), `
		INSERT INTO posts (workspace_id,title,caption,media_url,ingest_after,publish_at,default_privacy_level,privacy_level,status,upload_job_id,media_asset_id,storage_object_key,bucket,metadata)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14::jsonb)
		ON CONFLICT (workspace_id,(metadata->>'worker_event_key'))
		WHERE metadata->>'worker_calendar_event'='true' AND metadata->>'worker_event_key' IS NOT NULL
		DO UPDATE SET
			title=EXCLUDED.title,publish_at=EXCLUDED.publish_at,updated_at=NOW(),
			metadata=CASE WHEN posts.metadata->>'worker_remote_job_id' IS DISTINCT FROM EXCLUDED.metadata->>'worker_remote_job_id'
				THEN EXCLUDED.metadata
				ELSE posts.metadata || jsonb_build_object('worker_event_hash',EXCLUDED.metadata->>'worker_event_hash') END
		RETURNING id,created_at,upload_job_id,(xmax=0)`,
		post.WorkspaceID, post.Title, post.Caption, post.MediaURL, post.IngestAfter, post.PublishAt,
		post.DefaultPrivacyLevel, post.PrivacyLevel, post.Status, post.UploadJobID,
		ns(post.MediaAssetID), ns(post.StorageObjectKey), ns(post.Bucket), string(post.Metadata),
	).Scan(&post.ID, &post.CreatedAt, &post.UploadJobID, &wasCreated)
	if err != nil {
		return nil, false, fmt.Errorf("upsert worker calendar event: %w", err)
	}
	saved, err := r.FindByID(post.ID)
	if err != nil {
		return nil, false, fmt.Errorf("load upserted worker calendar event: %w", err)
	}
	return saved, wasCreated, nil
}

func (r *PostRepository) FindWorkerCalendarEvent(ctx context.Context, workspaceID int64, key string) (*models.Post, error) {
	var id int64
	err := r.db.QueryRowContext(ctx, `SELECT id FROM posts WHERE workspace_id=$1 AND metadata->>'worker_event_key'=$2 AND metadata->>'worker_calendar_event'='true'`, workspaceID, key).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find worker calendar event: %w", err)
	}
	post, err := r.FindByID(id)
	if err != nil {
		return nil, fmt.Errorf("load worker calendar event: %w", err)
	}
	if post == nil || post.WorkspaceID != workspaceID {
		return nil, nil
	}
	return post, nil
}

func (r *PostRepository) FindWorkerCalendarEventByJobID(ctx context.Context, workspaceID int64, jobID string) (*models.Post, error) {
	var id int64
	err := r.db.QueryRowContext(ctx, `SELECT id FROM posts WHERE workspace_id=$1 AND metadata->>'worker_remote_job_id'=$2 AND metadata->>'worker_calendar_event'='true' ORDER BY id DESC LIMIT 1`, workspaceID, jobID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find worker calendar event by job id: %w", err)
	}
	post, err := r.FindByID(id)
	if err != nil {
		return nil, fmt.Errorf("load worker calendar event by job id: %w", err)
	}
	if post == nil || post.WorkspaceID != workspaceID {
		return nil, nil
	}
	return post, nil
}

func (r *PostRepository) CancelWorkerCalendarEvent(ctx context.Context, workspaceID int64, key string) error {
	result, err := r.db.ExecContext(ctx, `UPDATE posts
		SET metadata=COALESCE(metadata,'{}'::jsonb) || jsonb_build_object(
			'generation_status','CANCELLED','generation_phase','CANCELLED','worker_cancelled_at',NOW()),
			updated_at=NOW()
		WHERE workspace_id=$1 AND metadata->>'worker_event_key'=$2
		  AND metadata->>'worker_calendar_event'='true'
		  AND metadata->>'generation_status' NOT IN ('SUCCEEDED','COMPLETED','FAILED','CANCELLED')`, workspaceID, key)
	if err != nil {
		return fmt.Errorf("cancel worker calendar event: %w", err)
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return ErrAgentRunNotFound
	}
	return nil
}

// UpdateWorkerCalendarEvent changes the editable Calendar fields only. It is
// scoped to the owning workspace and the worker-event marker, and applies only
// while the card remains a draft.
func (r *PostRepository) UpdateWorkerCalendarEvent(ctx context.Context, workspaceID int64, key string, title *string, scheduledAt *time.Time) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE posts SET
			title = COALESCE($3, title),
			publish_at = COALESCE($4, publish_at),
			updated_at = NOW()
		WHERE workspace_id=$1 AND metadata->>'worker_event_key'=$2
		  AND metadata->>'worker_calendar_event'='true' AND status='draft'`,
		workspaceID, key, title, scheduledAt)
	if err != nil {
		return fmt.Errorf("update worker calendar event: %w", err)
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return ErrAgentRunNotFound
	}
	return nil
}

// DeleteWorkerCalendarEvent deletes only a worker-created draft with no
// publication targets. It never removes a normal post or a scheduled upload.
func (r *PostRepository) DeleteWorkerCalendarEvent(ctx context.Context, workspaceID int64, key string) error {
	result, err := r.db.ExecContext(ctx, `
		DELETE FROM posts p
		WHERE p.workspace_id=$1 AND p.metadata->>'worker_event_key'=$2
		  AND p.metadata->>'worker_calendar_event'='true' AND p.status='draft'
		  AND NOT EXISTS (SELECT 1 FROM post_targets pt WHERE pt.post_id=p.id)`, workspaceID, key)
	if err != nil {
		return fmt.Errorf("delete worker calendar event: %w", err)
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return ErrAgentRunNotFound
	}
	return nil
}

func (r *PostRepository) UpdateWorkerCalendarEventProgress(ctx context.Context, workspaceID int64, key, kind, status, phase string, progress *int, snapshot json.RawMessage, heartbeat bool) error {
	if !json.Valid(snapshot) {
		snapshot = json.RawMessage(`{}`)
	}
	perKind := map[string]any{"status": status, "phase": phase, "snapshot": json.RawMessage(snapshot)}
	if progress != nil {
		perKind["progress"] = *progress
	}
	perKindJSON, err := json.Marshal(perKind)
	if err != nil {
		return fmt.Errorf("encode worker calendar kind progress: %w", err)
	}
	var overall any
	if progress != nil {
		overall = *progress
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE posts
		SET metadata = COALESCE(metadata,'{}'::jsonb) || jsonb_build_object(
			'generation_kind',$3,
			'generation_status',$4,
			'generation_phase',$5,
			'generation_snapshot', jsonb_set(
				COALESCE(metadata->'generation_snapshot','{}'::jsonb) || $7::jsonb,
				'{worker_kinds}',
				COALESCE(metadata->'generation_snapshot'->'worker_kinds','{}'::jsonb)
					|| jsonb_build_object($3::text, COALESCE((metadata->'generation_snapshot'->'worker_kinds')->($3::text),'{}'::jsonb) || $8::jsonb),
				true
			)
		) || CASE WHEN $6::integer IS NOT NULL THEN jsonb_build_object('generation_progress',$6::integer) ELSE '{}'::jsonb END
		  || CASE WHEN $4::text = 'RUNNING' AND $9::boolean THEN jsonb_build_object('worker_heartbeat_at',NOW()) ELSE '{}'::jsonb END
		WHERE workspace_id=$1 AND metadata->>'worker_event_key'=$2 AND metadata->>'worker_calendar_event'='true'`,
		workspaceID, key, kind, status, phase, overall, string(snapshot), string(perKindJSON), heartbeat)
	if err != nil {
		return fmt.Errorf("update worker calendar event progress: %w", err)
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return ErrAgentRunNotFound
	}
	return nil
}

// MarkStaleWorkerCalendarEvents fails running cards whose last worker heartbeat
// is older than maxAge. Terminal transitions are preserved and the update is
// safe when several API worker replicas run the sweep concurrently.
func (r *PostRepository) MarkStaleWorkerCalendarEvents(ctx context.Context, maxAge time.Duration) (int64, error) {
	if maxAge <= 0 {
		maxAge = 15 * time.Minute
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE posts
		SET metadata = COALESCE(metadata,'{}'::jsonb) || jsonb_build_object(
			'generation_status','FAILED', 'generation_phase','TIMEOUT',
			'worker_timeout_at',NOW(),
			'generation_snapshot',COALESCE(metadata->'generation_snapshot','{}'::jsonb) ||
				jsonb_build_object('phase','TIMEOUT','error',jsonb_build_object(
					'error_code','WORKER_HEARTBEAT_TIMEOUT',
					'reason','worker heartbeat expired before the job completed')))
		WHERE metadata->>'worker_calendar_event'='true'
		  AND metadata->>'generation_status'='RUNNING'
		  AND COALESCE((metadata->>'worker_heartbeat_at')::timestamptz,created_at) < NOW() - $1::interval`,
		fmt.Sprintf("%f seconds", maxAge.Seconds()))
	if err != nil {
		return 0, fmt.Errorf("mark stale worker calendar events: %w", err)
	}
	return result.RowsAffected()
}

func workerCalendarIdentity(raw json.RawMessage) (key, requestHash string, err error) {
	var metadata map[string]json.RawMessage
	if err = json.Unmarshal(raw, &metadata); err != nil {
		return "", "", err
	}
	_ = json.Unmarshal(metadata["worker_event_key"], &key)
	_ = json.Unmarshal(metadata["worker_event_hash"], &requestHash)
	if key == "" || requestHash == "" {
		return "", "", errors.New("worker calendar event metadata is missing idempotency identity")
	}
	return key, requestHash, nil
}

func workerCalendarHash(raw json.RawMessage) string {
	var metadata map[string]json.RawMessage
	_ = json.Unmarshal(raw, &metadata)
	var hash string
	_ = json.Unmarshal(metadata["worker_event_hash"], &hash)
	return hash
}
