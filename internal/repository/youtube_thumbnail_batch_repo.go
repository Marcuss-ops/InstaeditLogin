package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/lib/pq"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

var ErrYouTubeThumbnailBatchKeyCollision = errors.New("youtube thumbnail batch idempotency key already exists")

type YouTubeThumbnailBatchStore interface {
	Create(ctx context.Context, batch *models.YouTubeThumbnailBatch, items []models.YouTubeThumbnailBatchItem) error
	FindByID(ctx context.Context, id string) (*models.YouTubeThumbnailBatch, error)
	FindByKey(ctx context.Context, workspaceID int64, key string) (*models.YouTubeThumbnailBatch, error)
	ListItems(ctx context.Context, batchID string) ([]models.YouTubeThumbnailBatchItem, error)
	ClaimBatch(ctx context.Context, batchID string, staleBefore time.Time) (bool, error)
	ClaimItem(ctx context.Context, itemID int64, staleBefore time.Time) (bool, error)
	UpdateItem(ctx context.Context, item *models.YouTubeThumbnailBatchItem) error
	Recompute(ctx context.Context, batchID string) (*models.YouTubeThumbnailBatch, error)
}

type YouTubeThumbnailBatchRepository struct{ db *sql.DB }

func NewYouTubeThumbnailBatchRepository(db *sql.DB) *YouTubeThumbnailBatchRepository {
	return &YouTubeThumbnailBatchRepository{db: db}
}

func (r *YouTubeThumbnailBatchRepository) Create(ctx context.Context, batch *models.YouTubeThumbnailBatch, items []models.YouTubeThumbnailBatchItem) error {
	if batch == nil || batch.ID == "" || batch.WorkspaceID <= 0 || batch.GroupID <= 0 || batch.IdempotencyKey == "" || len(batch.RequestHash) != 32 || len(items) == 0 {
		return errors.New("invalid YouTube thumbnail batch")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin thumbnail batch: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO youtube_thumbnail_batches
			(id, workspace_id, group_id, idempotency_key, request_hash, status, total)
		VALUES ($1, $2, $3, $4, $5, 'queued', $6)`,
		batch.ID, batch.WorkspaceID, batch.GroupID, batch.IdempotencyKey, batch.RequestHash, len(items))
	if err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == "23505" {
			return ErrYouTubeThumbnailBatchKeyCollision
		}
		return fmt.Errorf("insert thumbnail batch: %w", err)
	}

	for _, item := range items {
		tags, marshalErr := json.Marshal(item.Tags)
		if marshalErr != nil {
			return fmt.Errorf("marshal thumbnail batch tags: %w", marshalErr)
		}
		if _, err = tx.ExecContext(ctx, `
			INSERT INTO youtube_thumbnail_batch_items
				(batch_id, platform_account_id, youtube_video_id, variant_id,
				 thumbnail_media_id, title, description, tags)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			batch.ID, item.PlatformAccountID, item.YouTubeVideoID, item.VariantID,
			item.ThumbnailMediaID, item.Title, item.Description, tags); err != nil {
			return fmt.Errorf("insert thumbnail batch item: %w", err)
		}
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit thumbnail batch: %w", err)
	}
	return nil
}

const youtubeThumbnailBatchColumns = `id, workspace_id, group_id, idempotency_key,
request_hash, status, total, completed, failed, last_error, created_at,
updated_at, started_at, finished_at`

func scanYouTubeThumbnailBatch(row interface{ Scan(...any) error }) (*models.YouTubeThumbnailBatch, error) {
	b := &models.YouTubeThumbnailBatch{}
	if err := row.Scan(&b.ID, &b.WorkspaceID, &b.GroupID, &b.IdempotencyKey, &b.RequestHash,
		&b.Status, &b.Total, &b.Completed, &b.Failed, &b.LastError, &b.CreatedAt,
		&b.UpdatedAt, &b.StartedAt, &b.FinishedAt); err != nil {
		return nil, err
	}
	return b, nil
}

func (r *YouTubeThumbnailBatchRepository) FindByID(ctx context.Context, id string) (*models.YouTubeThumbnailBatch, error) {
	b, err := scanYouTubeThumbnailBatch(r.db.QueryRowContext(ctx, `SELECT `+youtubeThumbnailBatchColumns+` FROM youtube_thumbnail_batches WHERE id = $1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find thumbnail batch: %w", err)
	}
	return b, nil
}

func (r *YouTubeThumbnailBatchRepository) FindByKey(ctx context.Context, workspaceID int64, key string) (*models.YouTubeThumbnailBatch, error) {
	b, err := scanYouTubeThumbnailBatch(r.db.QueryRowContext(ctx, `SELECT `+youtubeThumbnailBatchColumns+` FROM youtube_thumbnail_batches WHERE workspace_id = $1 AND idempotency_key = $2`, workspaceID, key))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find thumbnail batch by key: %w", err)
	}
	return b, nil
}

func (r *YouTubeThumbnailBatchRepository) ListItems(ctx context.Context, batchID string) ([]models.YouTubeThumbnailBatchItem, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, batch_id, platform_account_id, youtube_video_id,
		variant_id, thumbnail_media_id, title, description, tags, status,
		editor_session_id, public_url, last_error
		FROM youtube_thumbnail_batch_items WHERE batch_id = $1 ORDER BY id`, batchID)
	if err != nil {
		return nil, fmt.Errorf("list thumbnail batch items: %w", err)
	}
	defer rows.Close()
	var result []models.YouTubeThumbnailBatchItem
	for rows.Next() {
		var item models.YouTubeThumbnailBatchItem
		var tags []byte
		if err := rows.Scan(&item.ID, &item.BatchID, &item.PlatformAccountID, &item.YouTubeVideoID,
			&item.VariantID, &item.ThumbnailMediaID, &item.Title, &item.Description, &tags,
			&item.Status, &item.EditorSessionID, &item.PublicURL, &item.LastError); err != nil {
			return nil, fmt.Errorf("scan thumbnail batch item: %w", err)
		}
		if len(tags) > 0 && string(tags) != "null" {
			if err := json.Unmarshal(tags, &item.Tags); err != nil {
				return nil, fmt.Errorf("decode thumbnail batch tags: %w", err)
			}
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate thumbnail batch items: %w", err)
	}
	return result, nil
}

func (r *YouTubeThumbnailBatchRepository) ClaimBatch(ctx context.Context, batchID string, staleBefore time.Time) (bool, error) {
	res, err := r.db.ExecContext(ctx, `UPDATE youtube_thumbnail_batches
		SET status = 'processing', started_at = COALESCE(started_at, NOW()), updated_at = NOW()
		WHERE id = $1 AND (status = 'queued' OR (status = 'processing' AND updated_at < $2))`, batchID, staleBefore)
	if err != nil {
		return false, fmt.Errorf("claim thumbnail batch: %w", err)
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

func (r *YouTubeThumbnailBatchRepository) ClaimItem(ctx context.Context, itemID int64, staleBefore time.Time) (bool, error) {
	res, err := r.db.ExecContext(ctx, `UPDATE youtube_thumbnail_batch_items
		SET status = 'processing', updated_at = NOW()
		WHERE id = $1 AND (status = 'queued' OR (status = 'processing' AND updated_at < $2))`, itemID, staleBefore)
	if err != nil {
		return false, fmt.Errorf("claim thumbnail batch item: %w", err)
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

func (r *YouTubeThumbnailBatchRepository) UpdateItem(ctx context.Context, item *models.YouTubeThumbnailBatchItem) error {
	if item == nil || item.ID <= 0 {
		return errors.New("invalid thumbnail batch item")
	}
	_, err := r.db.ExecContext(ctx, `UPDATE youtube_thumbnail_batch_items
		SET status = $2, editor_session_id = NULLIF($3, ''), public_url = $4,
			last_error = $5, updated_at = NOW() WHERE id = $1`,
		item.ID, item.Status, item.EditorSessionID, item.PublicURL, item.LastError)
	if err != nil {
		return fmt.Errorf("update thumbnail batch item: %w", err)
	}
	return nil
}

func (r *YouTubeThumbnailBatchRepository) Recompute(ctx context.Context, batchID string) (*models.YouTubeThumbnailBatch, error) {
	_, err := r.db.ExecContext(ctx, `UPDATE youtube_thumbnail_batches b SET
		completed = x.completed, failed = x.failed,
		status = CASE WHEN x.completed + x.failed = b.total AND x.failed = 0 THEN 'completed'
			WHEN x.completed + x.failed = b.total THEN 'partial' ELSE 'processing' END,
		finished_at = CASE WHEN x.completed + x.failed = b.total THEN NOW() ELSE b.finished_at END,
		updated_at = NOW()
		FROM (SELECT batch_id,
			COUNT(*) FILTER (WHERE status = 'completed')::int AS completed,
			COUNT(*) FILTER (WHERE status = 'failed')::int AS failed
			FROM youtube_thumbnail_batch_items WHERE batch_id = $1 GROUP BY batch_id) x
		WHERE b.id = x.batch_id`, batchID)
	if err != nil {
		return nil, fmt.Errorf("recompute thumbnail batch: %w", err)
	}
	return r.FindByID(ctx, batchID)
}
