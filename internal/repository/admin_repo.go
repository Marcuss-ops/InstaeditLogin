package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// AdminRepository is the read-side aggregate store backing the P2
// ops dashboard (/admin/channels, /admin/queue, /admin/health). All
// queries are bounded (LIMIT 500 default), index-friendly, and
// single-purpose so the handler layer can compose them into JSON
// responses without batching DB roundtrips.
//
// SECURITY: every query is GLOBAL — there is no per-workspace
// scoping. The handler layer is the authz gate (requireAdmin gates
// /admin/* and the ops JWT carries the IsAdmin bool). A future
// multi-tenant admin layer (P3+) will introduce per-region scoping
// here; for now the operator's view is the whole fleet.
type AdminRepository struct {
	db *sql.DB
}

// NewAdminRepository creates a new AdminRepository.
func NewAdminRepository(db *sql.DB) *AdminRepository {
	return &AdminRepository{db: db}
}

// AdminChannelRow is one row in the /admin/channels response.
// Aggregates the per-channel state so the dashboard's "active vs
// reauth-required" headline can be a single COUNT(*)-with-FILTER.
type AdminChannelRow struct {
	PlatformAccountID int64
	UserID            int64
	Platform          string
	Username          string
	Status            string
	ConnectedAt       *time.Time
	LastValidatedAt   *time.Time
	LastRefreshAt     *time.Time
	ReauthRequiredAt  *time.Time
	LastErrorCode     string
	LastErrorMessage  string
	Metadata          map[string]interface{}
}

// AdminChannelCounts is the /admin/channels headline counts. The
// dashboard renders "active 187 / reauth_required 13" as a single
// SUM-after-FILTER query so a 200-channel fleet is one roundtrip.
type AdminChannelCounts struct {
	Active         int
	Expired        int
	ReauthRequired int
	Revoked        int
	Disconnected   int
	Error          int
	Total          int
}

// ChannelCounts returns the per-status counts + total. One
// FILTER-aggregate query, no per-status roundtrips.
func (r *AdminRepository) ChannelCounts(ctx context.Context) (AdminChannelCounts, error) {
	var c AdminChannelCounts
	err := r.db.QueryRowContext(ctx,
		`SELECT
		   COUNT(*) FILTER (WHERE status = 'active')         AS active,
		   COUNT(*) FILTER (WHERE status = 'expired')        AS expired,
		   COUNT(*) FILTER (WHERE status = 'reauth_required') AS reauth_required,
		   COUNT(*) FILTER (WHERE status = 'revoked')        AS revoked,
		   COUNT(*) FILTER (WHERE status = 'disconnected')   AS disconnected,
		   COUNT(*) FILTER (WHERE status = 'error')          AS error,
		   COUNT(*)                                              AS total
		 FROM platform_accounts`,
	).Scan(&c.Active, &c.Expired, &c.ReauthRequired, &c.Revoked, &c.Disconnected, &c.Error, &c.Total)
	if err != nil {
		return c, fmt.Errorf("admin: channel counts query: %w", err)
	}
	return c, nil
}

// ListChannelsForOps returns the platform_accounts rows behind the
// /admin/channels table. Optional filters narrow by status + platform.
// LIMIT 500 cap — the dashboard paginates beyond; a future
// query-param ?cursor=follows.
func (r *AdminRepository) ListChannelsForOps(ctx context.Context, statusFilter, platformFilter string, limit int) ([]AdminChannelRow, error) {
	if limit <= 0 || limit > 500 {
		limit = 500
	}
	var (
		statusArg   sql.NullString
		platformArg sql.NullString
	)
	if statusFilter != "" {
		statusArg = sql.NullString{String: statusFilter, Valid: true}
	}
	if platformFilter != "" {
		platformArg = sql.NullString{String: platformFilter, Valid: true}
	}

	rows, err := r.db.QueryContext(ctx,
		`SELECT id, user_id, platform, COALESCE(username, '') AS username,
		        status, connected_at, last_validated_at, last_refresh_at,
		        reauth_required_at,
		        COALESCE(last_error_code, '')    AS last_error_code,
		        COALESCE(last_error_message, '') AS last_error_message,
		        metadata
		 FROM platform_accounts
		 WHERE ($1::text IS NULL OR status   = $1)
		   AND ($2::text IS NULL OR platform = $2)
		 ORDER BY (status = 'reauth_required') DESC, connected_at DESC NULLS LAST
		 LIMIT $3`,
		statusArg, platformArg, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("admin: list channels: %w", err)
	}
	defer rows.Close()

	var out []AdminChannelRow
	for rows.Next() {
		var (
			row      AdminChannelRow
			metadata []byte
		)
		if err := rows.Scan(
			&row.PlatformAccountID,
			&row.UserID,
			&row.Platform,
			&row.Username,
			&row.Status,
			&row.ConnectedAt,
			&row.LastValidatedAt,
			&row.LastRefreshAt,
			&row.ReauthRequiredAt,
			&row.LastErrorCode,
			&row.LastErrorMessage,
			&metadata,
		); err != nil {
			return nil, fmt.Errorf("admin: scan channel row: %w", err)
		}
		row.Metadata = scanMetadata(metadata)
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("admin: iterate channels: %w", err)
	}
	return out, nil
}

// AdminQueueCounts is the /admin/queue headline gauge set. The
// dashboard renders "depth=47 / stuck=2 / in-flight=3" without
// hitting the upload_jobs table for every row.
type AdminQueueCounts struct {
	PendingCount     int
	LeasedCount      int
	ProcessingCount  int
	IngestCompleted  int
	PublishCompleted int
	FailedCount      int
	DeadLetterCount  int
	CancelledCount   int
	RetryWaitCount   int
	Total            int
	// StuckCount is the combined D3.c ∪ D3.a match: rows that are
	// (status='leased' AND heartbeat stale AND lease_expired) OR
	// (status IN ('processing','leased') AND started_at < NOW() - 15m).
	StuckCount int
}

// QueueCounts returns the per-status breakdown + stuck count.
func (r *AdminRepository) QueueCounts(ctx context.Context) (AdminQueueCounts, error) {
	var c AdminQueueCounts
	err := r.db.QueryRowContext(ctx,
		`SELECT
		   COUNT(*) FILTER (WHERE status = 'pending')          AS pending,
		   COUNT(*) FILTER (WHERE status = 'leased')           AS leased,
		   COUNT(*) FILTER (WHERE status = 'processing')       AS processing,
		   COUNT(*) FILTER (WHERE status = 'ingest_completed') AS ingest_completed,
		   COUNT(*) FILTER (WHERE status = 'publish_completed') AS publish_completed,
		   COUNT(*) FILTER (WHERE status = 'failed')           AS failed,
		   COUNT(*) FILTER (WHERE status = 'dead_letter')      AS dead_letter,
		   COUNT(*) FILTER (WHERE status = 'cancelled')        AS cancelled,
		   COUNT(*) FILTER (WHERE status = 'retry_wait')       AS retry_wait,
		   COUNT(*)                                                AS total
		 FROM upload_jobs`,
	).Scan(&c.PendingCount, &c.LeasedCount, &c.ProcessingCount, &c.IngestCompleted,
		&c.PublishCompleted, &c.FailedCount, &c.DeadLetterCount, &c.CancelledCount,
		&c.RetryWaitCount, &c.Total)
	if err != nil {
		return c, fmt.Errorf("admin: queue counts: %w", err)
	}
	// Stuck-job query: combine D3.c (wall-clock since started_at > 15m)
	// ∪ D3.a (reaper's candidate set). One SELECT, two predicates OR'd.
	if err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM upload_jobs
		 WHERE (status = 'leased'
		        AND lease_expires_at < NOW()
		        AND heartbeat_at IS NOT NULL
		        AND heartbeat_at < NOW() - INTERVAL '5 minutes')
		    OR (status IN ('processing', 'leased')
		        AND started_at IS NOT NULL
		        AND started_at < NOW() - INTERVAL '15 minutes')`,
	).Scan(&c.StuckCount); err != nil {
		return c, fmt.Errorf("admin: stuck count: %w", err)
	}
	return c, nil
}
