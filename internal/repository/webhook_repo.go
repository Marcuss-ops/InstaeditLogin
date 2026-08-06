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

	"github.com/google/uuid"
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
	ID          int64
	EventID     int64
	EndpointID  int64
	Attempt     int
	Status      string // 'pending' | 'success' | 'dead'
	RequestLog  string
	ResponseLog string
	ScheduledAt time.Time
	CompletedAt *time.Time
	LastError   string
	// LeaseID fences every in-flight owner. It is returned by ClaimDueDeliveries
	// and must be supplied to heartbeat and terminal state transitions.
	LeaseID     string
	LeaseUntil  *time.Time
	HeartbeatAt *time.Time
}

// ErrWebhookDeliveryNotFound is the delivery lookup sentinel.
var ErrWebhookDeliveryNotFound = errors.New("webhook delivery not found")

// ErrWebhookLeaseLost means the caller no longer owns the delivery lease.
// This is deliberately returned for both stale owners and expired leases so
// callers cannot accidentally persist a result after takeover.
var ErrWebhookLeaseLost = errors.New("webhook delivery lease lost")

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

// ClaimDueDeliveries atomically claims up to limit due deliveries and stamps
// each row with an independent UUID lease. The lease remains on the pending
// row while the HTTP request runs; scheduled_at is not moved as a pseudo-lease.
// A peer can reclaim only after lease_until expires, and every later write is
// fenced by the returned lease_id.
func (r *WebhookRepository) ClaimDueDeliveries(ctx context.Context, limit int, leaseTTL time.Duration) ([]WebhookDelivery, error) {
	if limit <= 0 {
		limit = 25
	}
	if leaseTTL <= 0 {
		return nil, fmt.Errorf("claim due deliveries: lease TTL must be positive")
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
		 WHERE status = 'pending'
		   AND scheduled_at <= NOW()
		   AND (lease_until IS NULL OR lease_until <= NOW())
		 ORDER BY scheduled_at ASC, id ASC
		 LIMIT $1
		 FOR UPDATE SKIP LOCKED`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("claim due deliveries: %w", err)
	}
	var out []WebhookDelivery
	for rows.Next() {
		var d WebhookDelivery
		if err := rows.Scan(&d.ID, &d.EventID, &d.EndpointID, &d.Attempt, &d.Status,
			&d.RequestLog, &d.ResponseLog, &d.ScheduledAt, &d.CompletedAt, &d.LastError); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan delivery: %w", err)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("read due deliveries: %w", err)
	}
	rows.Close()

	// The candidate rows remain locked until the transaction commits. Stamp
	// each row after closing the SELECT cursor so the same connection can
	// safely execute the UPDATEs.
	for i := range out {
		leaseID := uuid.NewString()
		var leaseUntil, heartbeat time.Time
		if err := tx.QueryRowContext(ctx,
			`UPDATE webhook_deliveries
			 SET attempt = attempt + 1,
			     lease_id = $2::uuid,
			     lease_until = NOW() + ($3 || ' seconds')::INTERVAL,
			     heartbeat_at = NOW()
			 WHERE id = $1
			   AND status = 'pending'
			   AND (lease_until IS NULL OR lease_until <= NOW())
			 RETURNING attempt, lease_until, heartbeat_at`,
			out[i].ID, leaseID, fmt.Sprintf("%f", leaseTTL.Seconds()),
		).Scan(&out[i].Attempt, &leaseUntil, &heartbeat); err != nil {
			return nil, fmt.Errorf("stamp webhook lease: %w", err)
		}
		out[i].LeaseID = leaseID
		out[i].LeaseUntil = &leaseUntil
		out[i].HeartbeatAt = &heartbeat
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit claim tx: %w", err)
	}
	return out, nil
}

// HeartbeatLease extends an active lease only when the supplied lease_id
// still owns the pending row and the previous lease has not expired.
func (r *WebhookRepository) HeartbeatLease(ctx context.Context, id int64, leaseID string, leaseTTL time.Duration) error {
	if leaseID == "" || leaseTTL <= 0 {
		return fmt.Errorf("heartbeat webhook lease: invalid lease arguments")
	}
	result, err := r.db.ExecContext(ctx,
		`UPDATE webhook_deliveries
		 SET lease_until = NOW() + ($3 || ' seconds')::INTERVAL,
		     heartbeat_at = NOW()
		 WHERE id = $1 AND status = 'pending'
		   AND lease_id = $2::uuid AND lease_until > NOW()`,
		id, leaseID, fmt.Sprintf("%f", leaseTTL.Seconds()),
	)
	if err != nil {
		return fmt.Errorf("heartbeat webhook lease: %w", err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: id=%d", ErrWebhookLeaseLost, id)
	}
	return nil
}

// MarkSuccess transitions a delivery to success only for the current,
// unexpired lease. A stale worker gets ErrWebhookLeaseLost and cannot fence
// a result written by the recovering owner.
func (r *WebhookRepository) MarkSuccess(ctx context.Context, id int64, leaseID, responseLog string) error {
	result, err := r.db.ExecContext(ctx,
		`UPDATE webhook_deliveries
		 SET status = 'success', response_log = $3, completed_at = NOW(),
		     lease_id = NULL, lease_until = NULL, heartbeat_at = NULL
		 WHERE id = $1 AND lease_id = $2::uuid AND status = 'pending'
		   AND lease_until > NOW()`,
		id, leaseID, responseLog,
	)
	if err != nil {
		return fmt.Errorf("mark success: %w", err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: id=%d", ErrWebhookLeaseLost, id)
	}
	return nil
}

// MarkRetry reschedules a delivery and releases its lease using CAS.
func (r *WebhookRepository) MarkRetry(ctx context.Context, id int64, leaseID, lastError, requestLog, responseLog string, nextAttemptAt time.Time) error {
	result, err := r.db.ExecContext(ctx,
		`UPDATE webhook_deliveries
		 SET scheduled_at = $3, last_error = $4,
		     request_log = $5, response_log = $6,
		     lease_id = NULL, lease_until = NULL, heartbeat_at = NULL
		 WHERE id = $1 AND lease_id = $2::uuid AND status = 'pending'
		   AND lease_until > NOW()`,
		id, leaseID, nextAttemptAt, lastError, requestLog, responseLog,
	)
	if err != nil {
		return fmt.Errorf("mark retry: %w", err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: id=%d", ErrWebhookLeaseLost, id)
	}
	return nil
}

// MarkDead transitions a delivery to DLQ using lease CAS.
func (r *WebhookRepository) MarkDead(ctx context.Context, id int64, leaseID, lastError, requestLog, responseLog string) error {
	result, err := r.db.ExecContext(ctx,
		`UPDATE webhook_deliveries
		 SET status = 'dead', completed_at = NOW(),
		     last_error = $3, request_log = $4, response_log = $5,
		     lease_id = NULL, lease_until = NULL, heartbeat_at = NULL
		 WHERE id = $1 AND lease_id = $2::uuid AND status = 'pending'
		   AND lease_until > NOW()`,
		id, leaseID, lastError, requestLog, responseLog,
	)
	if err != nil {
		return fmt.Errorf("mark dead: %w", err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: id=%d", ErrWebhookLeaseLost, id)
	}
	return nil
}

// FindDeliveryByID returns (nil, ErrWebhookDeliveryNotFound) when missing.
func (r *WebhookRepository) FindDeliveryByID(ctx context.Context, id int64) (*WebhookDelivery, error) {
	d := &WebhookDelivery{}
	var leaseID sql.NullString
	var leaseUntil, heartbeat sql.NullTime
	err := r.db.QueryRowContext(ctx,
		`SELECT id, event_id, endpoint_id, attempt, status, COALESCE(request_log, ''),
		       COALESCE(response_log, ''), scheduled_at, completed_at, COALESCE(last_error, ''),
		       lease_id, lease_until, heartbeat_at
		FROM webhook_deliveries WHERE id = $1`,
		id,
	).Scan(&d.ID, &d.EventID, &d.EndpointID, &d.Attempt, &d.Status,
		&d.RequestLog, &d.ResponseLog, &d.ScheduledAt, &d.CompletedAt, &d.LastError,
		&leaseID, &leaseUntil, &heartbeat)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrWebhookDeliveryNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find delivery: %w", err)
	}
	if leaseID.Valid {
		d.LeaseID = leaseID.String
	}
	if leaseUntil.Valid {
		t := leaseUntil.Time
		d.LeaseUntil = &t
	}
	if heartbeat.Valid {
		t := heartbeat.Time
		d.HeartbeatAt = &t
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
		     completed_at = NULL, last_error = NULL, response_log = NULL,
		     lease_id = NULL, lease_until = NULL, heartbeat_at = NULL
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
