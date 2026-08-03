package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// LivestreamEventRecorder is the dependency used by reconciler/state-machine
// code to persist audit events without knowing SQL details.
type LivestreamEventRecorder interface {
	Record(ctx context.Context, event *models.LivestreamEvent) error
}

type LivestreamEventRepository struct {
	db *sql.DB
}

func NewLivestreamEventRepository(db *sql.DB) *LivestreamEventRepository {
	return &LivestreamEventRepository{db: db}
}

const SQLRecordLivestreamEvent = `INSERT INTO livestream_events
    (livestream_id, run_id, event_type, severity, payload)
VALUES ($1, $2, $3, $4, $5::jsonb)
RETURNING id, created_at`

func (r *LivestreamEventRepository) Record(ctx context.Context, event *models.LivestreamEvent) error {
	if err := models.ValidateLivestreamEvent(event); err != nil {
		return err
	}
	if !isKnownLivestreamEventType(event.EventType) {
		return fmt.Errorf("invalid livestream event type %q", event.EventType)
	}
	if err := r.db.QueryRowContext(ctx, SQLRecordLivestreamEvent,
		event.LivestreamID, event.RunID, event.EventType, event.Severity, json.RawMessage(event.Payload)).
		Scan(&event.ID, &event.CreatedAt); err != nil {
		return fmt.Errorf("record livestream event: %w", err)
	}
	return nil
}

func (r *LivestreamEventRepository) ListByRun(ctx context.Context, runID string, limit int) ([]models.LivestreamEvent, error) {
	if runID == "" {
		return nil, errors.New("list livestream events: empty run ID")
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := r.db.QueryContext(ctx, `SELECT id, livestream_id, run_id, event_type, severity, payload, created_at
		FROM livestream_events WHERE run_id = $1 ORDER BY id ASC LIMIT $2`, runID, limit)
	if err != nil {
		return nil, fmt.Errorf("list livestream events: %w", err)
	}
	defer rows.Close()
	var result []models.LivestreamEvent
	for rows.Next() {
		var event models.LivestreamEvent
		if err := rows.Scan(&event.ID, &event.LivestreamID, &event.RunID, &event.EventType, &event.Severity, &event.Payload, &event.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan livestream event: %w", err)
		}
		result = append(result, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate livestream events: %w", err)
	}
	return result, nil
}

func isKnownLivestreamEventType(eventType string) bool {
	switch eventType {
	case models.LivestreamEventRunCreated,
		models.LivestreamEventRunLeased,
		models.LivestreamEventOAuthRefreshed,
		models.LivestreamEventStreamCreated,
		models.LivestreamEventYouTubeStreamCreated,
		models.LivestreamEventBroadcastCreated,
		models.LivestreamEventYouTubeBroadcastCreated,
		models.LivestreamEventBroadcastBound,
		models.LivestreamEventEncoderStarted,
		models.LivestreamEventIngestActive,
		models.LivestreamEventBroadcastLive,
		models.LivestreamEventHealthWarning,
		models.LivestreamEventHealthDegraded,
		models.LivestreamEventEncoderRestarted,
		models.LivestreamEventBroadcastCompleted,
		models.LivestreamEventRunFailed,
		models.LivestreamEventHeartbeatLost:
		return true
	default:
		return false
	}
}
