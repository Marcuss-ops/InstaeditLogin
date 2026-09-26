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

// CreateWorkerCalendarEvent is idempotent on (workspace_id, event_key). The
// partial unique index arbitrates concurrent retries; a replay with a changed
// request is rejected instead of silently replacing the existing video.
func (r *PostRepository) CreateWorkerCalendarEvent(post *models.Post) (*models.Post, bool, error) {
	if post == nil || post.WorkspaceID <= 0 || len(post.Metadata) == 0 || !json.Valid(post.Metadata) {
		return nil, false, errors.New("worker calendar event requires workspace and valid metadata")
	}
	key, requestHash, err := workerCalendarIdentity(post.Metadata)
	if err != nil {
		return nil, false, err
	}
	existing, err := r.FindWorkerCalendarEvent(context.Background(), post.WorkspaceID, key)
	if err != nil {
		return nil, false, err
	}
	if existing != nil {
		if workerCalendarHash(existing.Metadata) != requestHash {
			return nil, false, ErrIdempotencyConflict
		}
		return existing, false, nil
	}
	if err = r.Create(post, nil); err == nil {
		return post, true, nil
	}
	// A concurrent identical request may have won the unique-index race.
	existing, lookupErr := r.FindWorkerCalendarEvent(context.Background(), post.WorkspaceID, key)
	if lookupErr == nil && existing != nil {
		if workerCalendarHash(existing.Metadata) != requestHash {
			return nil, false, ErrIdempotencyConflict
		}
		return existing, false, nil
	}
	if lookupErr != nil {
		return nil, false, fmt.Errorf("read worker calendar event after create failure: %w", lookupErr)
	}
	return nil, false, fmt.Errorf("create worker calendar event: %w", err)
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

func (r *PostRepository) UpdateWorkerCalendarEventProgress(ctx context.Context, workspaceID int64, key, kind, status, phase string, progress *int, snapshot json.RawMessage) error {
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
		  || CASE WHEN $4::text = 'RUNNING' THEN jsonb_build_object('worker_heartbeat_at',NOW()) ELSE '{}'::jsonb END
		WHERE workspace_id=$1 AND metadata->>'worker_event_key'=$2 AND metadata->>'worker_calendar_event'='true'`,
		workspaceID, key, kind, status, phase, overall, string(snapshot), string(perKindJSON))
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
		  AND COALESCE((metadata->>'worker_heartbeat_at')::timestamptz,updated_at) < NOW() - $1::interval`,
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
