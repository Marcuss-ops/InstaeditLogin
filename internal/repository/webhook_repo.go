// Package repository — webhook runtime (SPRINT 4.2).
//
// Three tables, four concerns:
//
//   - Endpoints: CRUD for webhook_endpoints rows (workspace-scoped
//     subscriber configuration with URL + secret + event filter).
//   - Events:    INSERT ON CONFLICT for the dedup-anchored webhook_events
//     log. The event_id UNIQUE constraint is the canonical dedup;
//     two emits with the same event_id short-circuit at the DB level.
//   - Deliveries: claim due rows (status='pending' AND scheduled_at
//     <= NOW()) via SELECT FOR UPDATE SKIP LOCKED + UPDATE in a
//     single tx. Classify responses (2xx success, 4xx dead, 5xx/timeout
//     retry-or-dead). Mark success / dead. Replay resets attempt.
//   - Sweeper:   DeleteOlderThan (deferred follow-up) bounds the
//     webhook_deliveries table growth.
package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/lib/pq"
)

// -----------------------------------------------------------------------
// Endpoints
// -----------------------------------------------------------------------

// WebhookEndpoint mirrors a webhook_endpoints row.
type WebhookEndpoint struct {
	ID          int64
	WorkspaceID int64
	URL         string
	Secret      string
	Events      []string
	Status      string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// ErrWebhookEndpointNotFound is returned when a row does not exist.
var ErrWebhookEndpointNotFound = errors.New("webhook endpoint not found")

// CreateEndpoint inserts a new endpoint row and returns its id.
func (r *WebhookRepository) CreateEndpoint(ctx context.Context, e *WebhookEndpoint) error {
	err := r.db.QueryRowContext(ctx,
		`INSERT INTO webhook_endpoints (workspace_id, url, secret, events, status)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING id, created_at, updated_at`,
		e.WorkspaceID, e.URL, e.Secret, pq.Array(e.Events), e.Status,
	).Scan(&e.ID, &e.CreatedAt, &e.UpdatedAt)
	if err != nil {
		return fmt.Errorf("create webhook endpoint: %w", err)
	}
	return nil
}

// FindEndpointByID returns (nil, ErrWebhookEndpointNotFound) when no row matches.
func (r *WebhookRepository) FindEndpointByID(ctx context.Context, id int64) (*WebhookEndpoint, error) {
	e := &WebhookEndpoint{}
	err := r.db.QueryRowContext(ctx,
		`SELECT id, workspace_id, url, secret, events, status, created_at, updated_at
		 FROM webhook_endpoints WHERE id = $1`,
		id,
	).Scan(&e.ID, &e.WorkspaceID, &e.URL, &e.Secret, &e.Events, &e.Status, &e.CreatedAt, &e.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrWebhookEndpointNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find webhook endpoint: %w", err)
	}
	return e, nil
}

// ListEndpointsForWorkspace returns active endpoints (or all when
// includeDisabled) for the workspace, ordered by id ASC.
func (r *WebhookRepository) ListEndpointsForWorkspace(ctx context.Context, workspaceID int64, includeDisabled bool) ([]WebhookEndpoint, error) {
	q := `SELECT id, workspace_id, url, secret, events, status, created_at, updated_at
	      FROM webhook_endpoints WHERE workspace_id = $1`
	if !includeDisabled {
		q += ` AND status = 'active'`
	}
	q += ` ORDER BY id ASC`
	rows, err := r.db.QueryContext(ctx, q, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list webhook endpoints: %w", err)
	}
	defer rows.Close()
	var out []WebhookEndpoint
	for rows.Next() {
		var e WebhookEndpoint
		if err := rows.Scan(&e.ID, &e.WorkspaceID, &e.URL, &e.Secret, &e.Events, &e.Status, &e.CreatedAt, &e.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan webhook endpoint: %w", err)
		}
		out = append(out, e)
	}
	return out, nil
}

// DeleteEndpoint removes a webhook endpoint. Returns
// ErrWebhookEndpointNotFound if the row was already gone.
func (r *WebhookRepository) DeleteEndpoint(ctx context.Context, id int64) error {
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM webhook_endpoints WHERE id = $1`, id,
	)
	if err != nil {
		return fmt.Errorf("delete webhook endpoint: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete webhook endpoint rows affected: %w", err)
	}
	if n == 0 {
		return ErrWebhookEndpointNotFound
	}
	return nil
}

// ListActiveEndpointsForEvent returns active endpoints subscribed to
// the given event_type (events array contains the type). Used by the
// dispatcher's fan-out.
func (r *WebhookRepository) ListActiveEndpointsForEvent(ctx context.Context, workspaceID int64, eventType string) ([]WebhookEndpoint, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, workspace_id, url, secret, events, status, created_at, updated_at
		 FROM webhook_endpoints
		 WHERE workspace_id = $1 AND status = 'active' AND $2 = ANY(events)`,
		workspaceID, eventType,
	)
	if err != nil {
		return nil, fmt.Errorf("list active endpoints for event: %w", err)
	}
	defer rows.Close()
	var out []WebhookEndpoint
	for rows.Next() {
		var e WebhookEndpoint
		if err := rows.Scan(&e.ID, &e.WorkspaceID, &e.URL, &e.Secret, &e.Events, &e.Status, &e.CreatedAt, &e.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan endpoint: %w", err)
		}
		out = append(out, e)
	}
	return out, nil
}

// -----------------------------------------------------------------------
// Events
// -----------------------------------------------------------------------

// WebhookEvent mirrors a webhook_events row.
type WebhookEvent struct {
	ID          int64
	EventID     string
	EventType   string
	WorkspaceID int64
	Payload     []byte // JSONB
	CreatedAt   time.Time
}

// InsertEvent inserts a new event row. If event_id already exists,
// returns the existing row (dedup). The dedup is at the DB level via
// the UNIQUE constraint on event_id — the canonical "exactly one
// fan-out per event" guarantee.
func (r *WebhookRepository) InsertEvent(ctx context.Context, ev *WebhookEvent) error {
	err := r.db.QueryRowContext(ctx,
		`INSERT INTO webhook_events (event_id, event_type, workspace_id, payload)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (event_id) DO UPDATE SET event_id = EXCLUDED.event_id
		 RETURNING id, created_at`,
		ev.EventID, ev.EventType, ev.WorkspaceID, ev.Payload,
	).Scan(&ev.ID, &ev.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert webhook event: %w", err)
	}
	return nil
}

// FindEventByID returns (nil, ErrWebhookEventNotFound) when missing.
func (r *WebhookRepository) FindEventByID(ctx context.Context, id int64) (*WebhookEvent, error) {
	ev := &WebhookEvent{}
	err := r.db.QueryRowContext(ctx,
		`SELECT id, event_id, event_type, workspace_id, payload, created_at
		 FROM webhook_events WHERE id = $1`,
		id,
	).Scan(&ev.ID, &ev.EventID, &ev.EventType, &ev.WorkspaceID, &ev.Payload, &ev.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrWebhookEventNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find webhook event: %w", err)
	}
	return ev, nil
}

// ErrWebhookEventNotFound is the event lookup sentinel.
var ErrWebhookEventNotFound = errors.New("webhook event not found")

// -----------------------------------------------------------------------
// Deliveries
// -----------------------------------------------------------------------

// WebhookDelivery mirrors a webhook_deliveries row.
type WebhookDelivery struct {
	ID           int64
	EventID      int64
	EndpointID   int64
	Attempt      int
	Status       string // 'pending' | 'success' | 'dead'
	RequestLog   string
	ResponseLog  string
	ScheduledAt  time.Time
	CompletedAt  *time.Time
	LastError    string
}

// ErrWebhookDeliveryNotFound is the delivery lookup sentinel.
var ErrWebhookDeliveryNotFound = errors.New("webhook delivery not found")

// CreateDelivery inserts a delivery row (fan-out). Returns the new id.
func (r *WebhookRepository) CreateDelivery(ctx context.Context, d *WebhookDelivery) error {
	err := r.db.QueryRowContext(ctx,
		`INSERT INTO webhook_deliveries (event_id, endpoint_id, scheduled_at)
		 VALUES ($1, $2, NOW())
		 RETURNING id, attempt, status, scheduled_at`,
		d.EventID, d.EndpointID,
	).Scan(&d.ID, &d.Attempt, &d.Status, &d.ScheduledAt)
	if err != nil {
		return fmt.Errorf("create webhook delivery: %w", err)
	}
	return nil
}

// ClaimDueDeliveries atomically claims up to `limit` due deliveries
// (status='pending' AND scheduled_at <= NOW()) using SELECT FOR UPDATE
// SKIP LOCKED + UPDATE inside a transaction. Returns the claimed rows
// with their full state. Multi-replica safe: each replica only sees
// rows no other replica is currently processing.
func (r *WebhookRepository) ClaimDueDeliveries(ctx context.Context, limit int) ([]WebhookDelivery, error) {
	if limit <= 0 {
		limit = 25
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin claim tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := tx.QueryContext(ctx,
		`SELECT id, event_id, endpoint_id, attempt, status, COALESCE(request_log, ''),
		        COALESCE(response_log, ''), scheduled_at, completed_at, COALESCE(last_error, '')
		 FROM webhook_deliveries
		 WHERE status = 'pending' AND scheduled_at <= NOW()
		 ORDER BY scheduled_at ASC
		 LIMIT $1
		 FOR UPDATE SKIP LOCKED`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("claim due deliveries: %w", err)
	}
	var ids []int64
	var out []WebhookDelivery
	for rows.Next() {
		var d WebhookDelivery
		if err := rows.Scan(&d.ID, &d.EventID, &d.EndpointID, &d.Attempt, &d.Status,
			&d.RequestLog, &d.ResponseLog, &d.ScheduledAt, &d.CompletedAt, &d.LastError); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan delivery: %w", err)
		}
		ids = append(ids, d.ID)
		out = append(out, d)
	}
	rows.Close()
	if len(ids) == 0 {
		return nil, nil
	}
	// Bump attempt + reschedule by a small lease window (5s) so a
	// mid-flight crash doesn't leave the row in 'pending' for the
	// next tick to immediately re-pick. The next tick will see
	// scheduled_at > NOW() and skip; the lease expires after 5s.
	const leaseSeconds = 5
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx,
			`UPDATE webhook_deliveries
			 SET attempt = attempt + 1,
			     scheduled_at = NOW() + ($2 || ' seconds')::INTERVAL
			 WHERE id = $1`,
			id, fmt.Sprintf("%d", leaseSeconds),
		); err != nil {
			return nil, fmt.Errorf("bump attempt: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit claim tx: %w", err)
	}
	// The in-memory structs reflect the pre-update attempt; the
	// caller uses them to know which attempt THIS invocation is on
	// (the attempt value was already incremented, so the dispatcher's
	// `attempt` field IS the post-update value).
	return out, nil
}

// MarkSuccess transitions a delivery to status='success' with
// response_log + completed_at set.
func (r *WebhookRepository) MarkSuccess(ctx context.Context, id int64, responseLog string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE webhook_deliveries
		 SET status = 'success', response_log = $2, completed_at = NOW()
		 WHERE id = $1`,
		id, responseLog,
	)
	if err != nil {
		return fmt.Errorf("mark success: %w", err)
	}
	return nil
}

// MarkRetry reschedules a delivery with attempt increment + last_error
// set. Used for transient failures (5xx, timeout) below max_attempts.
func (r *WebhookRepository) MarkRetry(ctx context.Context, id int64, lastError, requestLog, responseLog string, nextAttemptAt time.Time) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE webhook_deliveries
		 SET scheduled_at = $2, last_error = $3,
		     request_log = $4, response_log = $5
		 WHERE id = $1`,
		id, nextAttemptAt, lastError, requestLog, responseLog,
	)
	if err != nil {
		return fmt.Errorf("mark retry: %w", err)
	}
	return nil
}

// MarkDead transitions a delivery to status='dead' (DLQ). Used for
// 4xx terminal errors OR when attempt >= max_attempts.
func (r *WebhookRepository) MarkDead(ctx context.Context, id int64, lastError, requestLog, responseLog string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE webhook_deliveries
		 SET status = 'dead', completed_at = NOW(),
		     last_error = $2, request_log = $3, response_log = $4
		 WHERE id = $1`,
		id, lastError, requestLog, responseLog,
	)
	if err != nil {
		return fmt.Errorf("mark dead: %w", err)
	}
	return nil
}

// FindDeliveryByID returns (nil, ErrWebhookDeliveryNotFound) when missing.
func (r *WebhookRepository) FindDeliveryByID(ctx context.Context, id int64) (*WebhookDelivery, error) {
	d := &WebhookDelivery{}
	err := r.db.QueryRowContext(ctx,
		`SELECT id, event_id, endpoint_id, attempt, status, COALESCE(request_log, ''),
		        COALESCE(response_log, ''), scheduled_at, completed_at, COALESCE(last_error, '')
		 FROM webhook_deliveries WHERE id = $1`,
		id,
	).Scan(&d.ID, &d.EventID, &d.EndpointID, &d.Attempt, &d.Status,
		&d.RequestLog, &d.ResponseLog, &d.ScheduledAt, &d.CompletedAt, &d.LastError)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrWebhookDeliveryNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find delivery: %w", err)
	}
	return d, nil
}

// MarkReplay resets a 'dead' delivery for manual replay: status='pending',
// attempt=0, scheduled_at=NOW(), clears response_log + last_error.
// Returns ErrWebhookDeliveryNotFound when the row is missing OR not in
// 'dead' state (the operator UI surfaces 404 for both).
func (r *WebhookRepository) MarkReplay(ctx context.Context, id int64) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE webhook_deliveries
		 SET status = 'pending', attempt = 0, scheduled_at = NOW(),
		     completed_at = NULL, last_error = NULL, response_log = NULL
		 WHERE id = $1 AND status = 'dead'`,
		id,
	)
	if err != nil {
		return fmt.Errorf("mark replay: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("mark replay rows affected: %w", err)
	}
	if n == 0 {
		return ErrWebhookDeliveryNotFound
	}
	return nil
}

// DeleteOlderThan removes completed deliveries (success|dead) older
// than cutoff. Used by the cron sweeper (deferred follow-up) to
// bound table growth. Returns the number of rows deleted.
func (r *WebhookRepository) DeleteOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM webhook_deliveries
		 WHERE status IN ('success', 'dead') AND completed_at < $1`,
		cutoff,
	)
	if err != nil {
		return 0, fmt.Errorf("delete older than: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("delete older than rows affected: %w", err)
	}
	return n, nil
}

// -----------------------------------------------------------------------
// Repository type
// -----------------------------------------------------------------------

// WebhookRepository is the Postgres-backed webhook runtime. Construct
// via NewWebhookRepository. The interface is local to the repository
// package — the service layer (internal/services/webhook_dispatcher.go)
// is the only caller.
type WebhookRepository struct {
	db *sql.DB
}

// NewWebhookRepository wires the repository.
func NewWebhookRepository(db *sql.DB) *WebhookRepository {
	return &WebhookRepository{db: db}
}
