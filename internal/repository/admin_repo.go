package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/channelimport"
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
	Active           int
	Expired          int
	ReauthRequired   int
	Revoked          int
	Disconnected     int
	Error            int
	Total            int
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
	PendingCount    int
	LeasedCount     int
	ProcessingCount int
	IngestCompleted int
	PublishCompleted int
	FailedCount     int
	DeadLetterCount int
	CancelledCount  int
	RetryWaitCount  int
	Total           int
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

// AdminInFlightRow is one row of the GROUP BY lease_owner result.
// "in-flight per worker" answers "what is each crawler pod doing?".
type AdminInFlightRow struct {
	WorkerID  string
	JobCount  int
	OldestAge *time.Duration
}

// InFlightPerWorker returns the per-worker in-flight count +
// oldest-job-age. Empty when nothing is leased.
func (r *AdminRepository) InFlightPerWorker(ctx context.Context) ([]AdminInFlightRow, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT COALESCE(lease_owner, '<unknown>') AS worker,
		        COUNT(*)                          AS job_count,
		        EXTRACT(EPOCH FROM (NOW() - MIN(COALESCE(started_at, lease_expires_at, NOW()))))::bigint
		 FROM upload_jobs
		 WHERE status = 'leased'
		 GROUP BY lease_owner
		 ORDER BY job_count DESC
		 LIMIT 100`,
	)
	if err != nil {
		return nil, fmt.Errorf("admin: in-flight per worker: %w", err)
	}
	defer rows.Close()

	var out []AdminInFlightRow
	for rows.Next() {
		var (
			w        AdminInFlightRow
			oldestS  sql.NullFloat64
		)
		if err := rows.Scan(&w.WorkerID, &w.JobCount, &oldestS); err != nil {
			return nil, fmt.Errorf("admin: scan in-flight row: %w", err)
		}
		if oldestS.Valid {
			d := time.Duration(oldestS.Float64 * float64(time.Second))
			w.OldestAge = &d
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// AdminStuckJobRow is one stuck-job row in the /admin/queue.csv
// export. Combines job metadata + the matching stuck reason so the
// operator doesn't have to guess whether D3.c (wall-clock) or D3.a
// (lease + heartbeat) fired.
type AdminStuckJobRow struct {
	JobID         int64
	UserID        int64
	WorkspaceID   int64
	SourceType    string
	SourceID      string
	Title         string
	Status        string
	AttemptCount  int
	LeaseOwner    string
	HeartbeatAt   *time.Time
	LeaseExpiresAt *time.Time
	StartedAt     *time.Time
	StuckReason   string
}

// ListStuckJobs returns the rows matching D3.c ∪ D3.a. LIMIT 200
// so the CSV export stays bounded; a future follow-up paginates.
func (r *AdminRepository) ListStuckJobs(ctx context.Context, limit int) ([]AdminStuckJobRow, error) {
	if limit <= 0 || limit > 200 {
		limit = 200
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, user_id, workspace_id, source_type, source_id,
		        COALESCE(title, '') AS title,
		        status, attempt_count,
		        COALESCE(lease_owner, '') AS lease_owner,
		        heartbeat_at, lease_expires_at, started_at,
		        CASE
		          WHEN (status = 'leased'
		                AND lease_expires_at < NOW()
		                AND heartbeat_at IS NOT NULL
		                AND heartbeat_at < NOW() - INTERVAL '5 minutes')
		            THEN 'lease_stale'
		          WHEN (status IN ('processing', 'leased')
		                AND started_at IS NOT NULL
		                AND started_at < NOW() - INTERVAL '15 minutes')
		            THEN 'wall_clock_wedged'
		          ELSE 'unknown'
		        END AS stuck_reason
		 FROM upload_jobs
		 WHERE (status = 'leased'
		        AND lease_expires_at < NOW()
		        AND heartbeat_at IS NOT NULL
		        AND heartbeat_at < NOW() - INTERVAL '5 minutes')
		    OR (status IN ('processing', 'leased')
		        AND started_at IS NOT NULL
		        AND started_at < NOW() - INTERVAL '15 minutes')
		 ORDER BY started_at ASC NULLS LAST
		 LIMIT $1`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("admin: list stuck jobs: %w", err)
	}
	defer rows.Close()

	var out []AdminStuckJobRow
	for rows.Next() {
		var r AdminStuckJobRow
		if err := rows.Scan(
			&r.JobID, &r.UserID, &r.WorkspaceID,
			&r.SourceType, &r.SourceID, &r.Title,
			&r.Status, &r.AttemptCount,
			&r.LeaseOwner,
			&r.HeartbeatAt, &r.LeaseExpiresAt, &r.StartedAt,
			&r.StuckReason,
		); err != nil {
			return nil, fmt.Errorf("admin: scan stuck job: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// AdminErrorRateRow is one channel's error rate over a single
// window. The dashboard renders the 1h + 24h envelope so operators
// spot acute spikes AND chronic trends simultaneously.
type AdminErrorRateRow struct {
	PlatformAccountID int64
	Platform          string
	Username          string
	WindowLabel       string
	TotalCount        int
	FailedCount       int
	Rate              float64 // 0.0–1.0; 0 when TotalCount == 0
}

// ErrorRatePerChannel (D5.a) returns one row per platform_account
// per requested window (1h, 24h). Joined via post_targets so the
// per-channel cardinality is the same as the operator's mental
// model (one row per linked channel, not per platform). LIMIT 200
// per window keeps the response bounded.
func (r *AdminRepository) ErrorRatePerChannel(ctx context.Context, windowInterval string, windowLabel string, limit int) ([]AdminErrorRateRow, error) {
	if limit <= 0 || limit > 200 {
		limit = 200
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT pa.id, pa.platform, COALESCE(pa.username, '') AS username,
		        COUNT(*)                                  AS total_count,
		        COUNT(*) FILTER (WHERE pt.status IN ('failed','dead_letter')) AS failed_count
		 FROM platform_accounts pa
		 LEFT JOIN post_targets pt
		   ON pt.platform_account_id = pa.id
		  AND pt.updated_at > NOW() - $1::interval
		 GROUP BY pa.id, pa.platform, pa.username
		 ORDER BY total_count DESC
		 LIMIT $2`,
		windowInterval, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("admin: error rate per channel: %w", err)
	}
	defer rows.Close()

	var out []AdminErrorRateRow
	for rows.Next() {
		var r AdminErrorRateRow
		if err := rows.Scan(
			&r.PlatformAccountID,
			&r.Platform,
			&r.Username,
			&r.TotalCount,
			&r.FailedCount,
		); err != nil {
			return nil, fmt.Errorf("admin: scan error-rate row: %w", err)
		}
		if r.TotalCount > 0 {
			r.Rate = float64(r.FailedCount) / float64(r.TotalCount)
		}
		r.WindowLabel = windowLabel
		out = append(out, r)
	}
	return out, rows.Err()
}

// AdminYouTubeQuota is the /admin/health "quota used / remaining"
// estimate (D2.b — derived approximation, not the live YouTube
// quota API). CostPerUploadUnits defaults to 1600 (YouTube Data API
// v3 docs); an operator may lower it if their traffic is dominantly
// cheaper endpoints. DailyBudgetUnits defaults to 10000.
type AdminYouTubeQuota struct {
	WindowHours         int
	EstimatedUnits      int64
	SuccessCount        int
	QuotaFailures       int
	DailyBudgetUnits    int64
	RemainingEstimate   int64
	CostPerUploadUnits  int64
}

// YouTubeQuotaApproximation (D2.b) reads the existing
// publish_success_total / publish_error_total{outcome=quota}
// counters indirectly via the post_targets audit trail (success
// row per publish; failure rows have error_code='quota_exceeded').
// Single SQL query; no Prometheus scrape needed.
//
// NOTE: this is a proxy for actual YouTube quota usage. Real
// per-endpoint costs vary (videos.insert ~1600, channels.list ~1,
// search.list ~100). Operators can override CostPerUploadUnits via
// cfg.YoutubeQuotaCostPerUpload if their traffic profile diverges
// significantly from the assumed all-uploads shape.
func (r *AdminRepository) YouTubeQuotaApproximation(ctx context.Context, window time.Duration, dailyBudgetUnits, costPerUploadUnits int64) (AdminYouTubeQuota, error) {
	if window <= 0 {
		window = 24 * time.Hour
	}
	if dailyBudgetUnits <= 0 {
		dailyBudgetUnits = 10000
	}
	if costPerUploadUnits <= 0 {
		costPerUploadUnits = 1600
	}

	var q AdminYouTubeQuota
	q.WindowHours = int(window / time.Hour)
	q.DailyBudgetUnits = dailyBudgetUnits
	q.CostPerUploadUnits = costPerUploadUnits

	err := r.db.QueryRowContext(ctx,
		`SELECT
		   COUNT(*) FILTER (WHERE status = 'published')  AS success_count,
		   COUNT(*) FILTER (WHERE last_error_code = 'quota_exceeded' OR last_error_code LIKE 'quota%') AS quota_failures
		 FROM post_targets
		 WHERE updated_at > NOW() - $1::interval
		   AND platform_account_id IN (SELECT id FROM platform_accounts WHERE platform = 'youtube')`,
		fmt.Sprintf("%d hours", q.WindowHours),
	).Scan(&q.SuccessCount, &q.QuotaFailures)
	if err != nil {
		return q, fmt.Errorf("admin: youtube quota query: %w", err)
	}

	totalBillable := int64(q.SuccessCount) + int64(q.QuotaFailures)
	q.EstimatedUnits = totalBillable * costPerUploadUnits
	if q.EstimatedUnits > dailyBudgetUnits {
		q.RemainingEstimate = 0
	} else {
		q.RemainingEstimate = dailyBudgetUnits - q.EstimatedUnits
	}
	return q, nil
}

// UpsertPendingChannel (P2 — admin CSV import) bulk-writes
// pre-resolved channel rows into platform_accounts at
// status='pending_authorization'. Pure delegation to
// channelimport.ImportToDB so the parse → DB-write contract stays
// in one place; the HTTP handler and the offline CLI share the
// exact same SQL shape via this method.
//
// ownerUserID MUST be > 0. Caller is responsible for resolving the
// owner_email (HTTP form field OR CLI --owner flag OR env) to a
// concrete user_id BEFORE calling this method; the AdminStore
// interface does not accept an email to keep the surface narrow.
//
// Returns the aggregate Result. Per-row DB failures surface in
// Result.Errors as channelimport.RowError slices (last-write-wins
// UPSERT) so partial-success visibility is preserved when an
// operator uploads 500-channel sheets.
func (r *AdminRepository) UpsertPendingChannel(ctx context.Context, ownerUserID int64, rows []channelimport.ImportRow) (channelimport.Result, error) {
	return channelimport.ImportToDB(ctx, r.db, ownerUserID, rows)
}
