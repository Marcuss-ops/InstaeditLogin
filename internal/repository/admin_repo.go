package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/channelimport"
)

// Task 10.10.x polish #2 — const-export production SQL. The
// ListDeadLetterJobs SQL is duplicated as an inline literal in
// internal/worker/task_10_10_recovery_test.go (TEST 6 ListDeadLetterJobs
// block). Moving it to an exported constant here pins the test's
// sqlmock expectation to the production SQL byte-for-byte — a
// production-side change fires a compile error in the test (the
// constant name moves + the regex match fails simultaneously) so
// the drift is caught at PR review, not by a silent sqlmock
// mismatch in CI.
//
// Inline SQL literals elsewhere in this file are still inline;
// extracting EXPORTED constants for the one method whose SQL is
// duplicated in the test file (1/9) is the minimum-viable scope.
// A future commit can sweep the remaining 8 methods if drift
// detection is desired for them too.
const SQLListDeadLetterJobs = `SELECT id, user_id, workspace_id, source_type, source_id,
		        COALESCE(title, '') AS title,
		        status, attempt_count,
		        COALESCE(error_code, '') AS error_code,
		        COALESCE(error_message, '') AS error_message,
		        completed_at
		 FROM upload_jobs
		 WHERE status = 'dead_letter'
		 ORDER BY completed_at DESC NULLS LAST
		 LIMIT $1`

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
			w       AdminInFlightRow
			oldestS sql.NullFloat64
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
	JobID          int64
	UserID         int64
	WorkspaceID    int64
	SourceType     string
	SourceID       string
	Title          string
	Status         string
	AttemptCount   int
	LeaseOwner     string
	HeartbeatAt    *time.Time
	LeaseExpiresAt *time.Time
	StartedAt      *time.Time
	StuckReason    string
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

// AdminDeadLetterJobRow (Task 10/10 — operator triage endpoint)
// surfaces upload_jobs rows whose retry budget has been
// exhausted (status='dead_letter'). The handler at
// /admin/upload_jobs/dead_letter (and its .csv companion) renders
// the operator's actionable triage list. Distinct from
// AdminStuckJobRow because (a) the filter is terminal-status only
// (no wall-clock/heartbeat coupling) and (b) the operator wants
// `error_code` + `error_message` to drive the triage decision, not
// the row internals.
type AdminDeadLetterJobRow struct {
	JobID          int64      `json:"job_id"`
	UserID         int64      `json:"user_id"`
	WorkspaceID    int64      `json:"workspace_id"`
	SourceType     string     `json:"source_type"`
	SourceID       string     `json:"source_id"`
	Title          string     `json:"title"`
	Status         string     `json:"status"`
	AttemptCount   int        `json:"attempt_count"`
	ErrorCode      string     `json:"error_code"`
	ErrorMessage   string     `json:"error_message"`
	DeadLetteredAt *time.Time `json:"dead_lettered_at,omitempty"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
}

// ListDeadLetterJobs returns upload_jobs rows in status='dead_letter',
// ordered by completed_at DESC NULLS LAST (most recent failures
// first). Bounded by `limit` (max 500) so the JSON payload stays
// under the dashboard render budget. The Task 10/10 acceptance
// criterion: a row that hits max_attempts MUST surface here so the
// operator can decide between manual retry / cancel / ignore.
//
// Single-statement SELECT — no joins, no aggregation. The columns
// are documented in migration 046 (upload_jobs.error_code,
// error_message, completed_at). Migration 045 added the 'dead_letter'
// enum value; the index idx_upload_jobs_status_dead_letter (added
// in migration 046) keeps this query fast even at 1M+ row scale.
func (r *AdminRepository) ListDeadLetterJobs(ctx context.Context, limit int) ([]AdminDeadLetterJobRow, error) {
	if limit <= 0 || limit > 500 {
		limit = 500
	}
	rows, err := r.db.QueryContext(ctx,
		SQLListDeadLetterJobs,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("admin: list dead-letter jobs: %w", err)
	}
	defer rows.Close()

	var out []AdminDeadLetterJobRow
	for rows.Next() {
		var jr AdminDeadLetterJobRow
		if err := rows.Scan(
			&jr.JobID, &jr.UserID, &jr.WorkspaceID,
			&jr.SourceType, &jr.SourceID, &jr.Title,
			&jr.Status, &jr.AttemptCount,
			&jr.ErrorCode, &jr.ErrorMessage,
			&jr.CompletedAt,
		); err != nil {
			return nil, fmt.Errorf("admin: scan dead-letter row: %w", err)
		}
		// deadLetteredAt mirrors completed_at for terminal-status
		// rows: completed_at is set when the row reaches a
		// terminal status (published/failed/dead_letter/etc per
		// migration 046's CHECK constraint). The dashboard prefers
		// the semantic name; SQL stays neutral for non-terminal
		// rows that share the same column.
		if jr.CompletedAt != nil {
			t := *jr.CompletedAt
			jr.DeadLetteredAt = &t
		}
		out = append(out, jr)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("admin: iterate dead-letter jobs: %w", err)
	}
	return out, nil
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
// quota API). CostPerUploadUnits defaults to 1 (1 unit per videos.insert — YouTube 2026 bucket model).
// (YouTube Data API v3 — pre-2026-06-01 was 1600 units per call.)
// An operator may lower it if their traffic is dominantly cheaper
// endpoints. DailyBudgetUnits defaults to 10000.
type AdminYouTubeQuota struct {
	WindowHours        int
	EstimatedUnits     int64
	SuccessCount       int
	QuotaFailures      int
	DailyBudgetUnits   int64
	RemainingEstimate  int64
	CostPerUploadUnits int64
}

// YouTubeQuotaApproximation (D2.b) reads the existing
// publish_success_total / publish_error_total{outcome=quota}
// counters indirectly via the post_targets audit trail (success
// row per publish; failure rows have error_code='quota_exceeded').
// Single SQL query; no Prometheus scrape needed.
//
// NOTE: this is a proxy for actual YouTube quota usage. Real
// per-endpoint costs vary (videos.insert = 1 unit under 2026 bucket model, channels.list ~1,
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
		// 2026 bucket model: 1 videos.insert = 1 unit. (Was 1600 pre-2026-06-01).
		costPerUploadUnits = 1
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

// AdminSubjectRow is one row in the /admin/health "Token rotation"
// section AND the underlying data shape for the
// oauth_connections_per_subject_total Prometheus gauge.
//
// The Subject field is the granter's stable subject id (Google
// Account's stable OIDC `sub` claim — internally a long opaque
// string, ~22 chars per Google's docs). Treat it as sensitive:
// the handler layer MUST truncate it before returning to the SPA
// (e.g. first 4 chars + last 4 chars + "…") so a copy/paste into
// a public channel doesn't leak a stable identifier. The
// collector passes it raw to the gauge label — that's intentional
// because Prometheus queries can group by subject ("which subject
// is at 90 connections?") and operators can rename/redact via
// Grafana legend processing.
type AdminSubjectRow struct {
	Subject             string     `json:"subject"`
	Provider            string     `json:"provider"`
	ConnectionCount     int        `json:"connection_count"`
	LastRefreshAt       *time.Time `json:"last_refresh_at,omitempty"`
	EarliestExpiresAt   *time.Time `json:"earliest_expires_at,omitempty"`
	ReauthRequiredCount int        `json:"reauth_required_count"`
}

// ConnectionsPerSubject (P2 ops — Token rotation + the alert at >=80)
// returns one AdminSubjectRow per (provider, provider_subject_id)
// where the count crosses the supplied threshold.
//
// Two call sites intentional:
//   - pkg/metrics/collector.go::collectOAuthConnectionsPerSubject
//     passes threshold=0 + provider="google" to see EVERY Google
//     subject on the fleet (drives the gauge + the alert at >80).
//   - pkg/api/admin_health.go::handleAdminHealth passes
//     threshold=50 + provider="google" to render only subjects
//     approaching the cap (keeps the JSON bounded for a 4×50
//     fleet; the long tail below 50 is implicit in the
//     Prometheus scrape data instead).
//
// expireWindow filters EarliestExpiresAt to tokens whose refresh
// window falls within the supplied lookahead. Default 7d
// (matches the JWT refresh cadence for most OAuth providers).
func (r *AdminRepository) ConnectionsPerSubject(ctx context.Context, provider string, threshold int, expireWindow time.Duration) ([]AdminSubjectRow, error) {
	if provider == "" {
		provider = "google"
	}
	if expireWindow <= 0 {
		expireWindow = 7 * 24 * time.Hour
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT provider_subject_id AS subject,
		        provider,
		        COUNT(*)                 AS connection_count,
		        MAX(last_refresh_at)     AS last_refresh_at,
		        MIN(expires_at) FILTER (
		            WHERE expires_at IS NOT NULL
		              AND expires_at <= NOW() + ($2 || ' seconds')::interval
		        ) AS earliest_expires_at,
		        COUNT(*) FILTER (WHERE status = 'reauth_required') AS reauth_required_count
		 FROM oauth_connections
		 WHERE provider = $1
		 GROUP BY provider_subject_id, provider
		 HAVING COUNT(*) >= $3
		 ORDER BY connection_count DESC
		 LIMIT 200`,
		provider, int64(expireWindow.Seconds()), threshold,
	)
	if err != nil {
		return nil, fmt.Errorf("admin: connections per subject: %w", err)
	}
	defer rows.Close()

	var out []AdminSubjectRow
	for rows.Next() {
		var row AdminSubjectRow
		if err := rows.Scan(
			&row.Subject, &row.Provider, &row.ConnectionCount,
			&row.LastRefreshAt, &row.EarliestExpiresAt, &row.ReauthRequiredCount,
		); err != nil {
			return nil, fmt.Errorf("admin: scan connections subject row: %w", err)
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// FleetReadinessCounts is the 12-field Definition-of-Done breakdown
// the docs/OAUTH-PRODUCTION.md Step 10 readiness checklist calls out
// for the 200-channel rollout. The JSON tags exactly match the field
// names the operator dashboard reads on every render; do NOT
// rename without a coordinated dashboard re-skin.
//
// Sourcing:
//
//   - the 6 status counters (Active / Pending / Reauth / Revoked /
//     Error / Total) come from COUNT(*) FILTER (WHERE status = ...)
//     on platform_accounts WHERE platform = 'youtube'.
//
//   - the 6 DoD-OK counters (refresh_test_ok / scope_youtube_upload_ok /
//     scope_youtube_readonly_ok / channel_binding_ok /
//     private_canary_ok / canary_channel_match_ok) derive from
//     platform_accounts state columns + the metadata JSONB:
//
//     refresh_test_ok         -> last_refresh_at IS NOT NULL AND status='active'
//     scope_youtube_upload_ok -> metadata->>'granted_scopes' LIKE '%youtube.upload%'
//     scope_youtube_readonly_ok -> metadata->>'granted_scopes' LIKE '%youtube.readonly%'
//     channel_binding_ok      -> last_validated_at IS NOT NULL
//     private_canary_ok       -> metadata->>'canary_result' = 'ok'
//     canary_channel_match_ok -> metadata->>'canary_channel_match' = 'true'
//
// All 12 counts come from a SINGLE round-trip: one platform_accounts
// SELECT with 12 FILTER clauses. Mirrors the existing ChannelCounts
// pattern at the top of this file.
type FleetReadinessCounts struct {
	Total                  int `json:"youtube_channels_total"`
	Active                 int `json:"active"`
	PendingAuthorization   int `json:"pending_authorization"`
	ReauthRequired         int `json:"reauth_required"`
	Revoked                int `json:"revoked"`
	Error                  int `json:"error"`
	RefreshTestOK          int `json:"refresh_test_ok"`
	ScopeYoutubeUploadOK   int `json:"scope_youtube_upload_ok"`
	ScopeYoutubeReadonlyOK int `json:"scope_youtube_readonly_ok"`
	ChannelBindingOK       int `json:"channel_binding_ok"`
	PrivateCanaryOK        int `json:"private_canary_ok"`
	CanaryChannelMatchOK   int `json:"canary_channel_match_ok"`
}

// FleetReadinessSnapshotResponse is the JSON envelope for
// GET /admin/youtube/fleet_readiness. The DoD counters live under
// fleet_readiness (matching the field names in docs/OAUTH-PRODUCTION.md
// Step 10 verbatim) and the snapshot_id lets the operator correlate
// this single response with the per-channel rows persisted to
// fleet_readiness_snapshot_channels.
type FleetReadinessSnapshotResponse struct {
	FleetReadiness FleetReadinessCounts `json:"fleet_readiness"`
	SnapshotID     string               `json:"snapshot_id"`
	TakenAt        time.Time            `json:"taken_at"`
}

// CountFleetReadiness is the read-only aggregate query behind the
// platform_accounts side of the Definition-of-Done readiness
// readout (the 12-count breakdown per docs/OAUTH-PRODUCTION.md Step
// 10). It returns the current counts WITHOUT writing a snapshot row
// -- ideal for ad-hoc dashboards / monitoring scrapes that want
// the latest numbers but should not consume a snapshot_id.
//
// NOT USED inside the tx in CreateFleetReadinessSnapshot: the
// snapshot legitimately needs tx-pinned REPEATABLE READ so the
// aggregate counts and the per-channel INSERT...SELECT match.
// This function is the canonical single-roundtrip FILTER aggregate
// for any caller that does not need snapshot-time consistency.
//
// Single round-trip: 12 COUNT(*) FILTER clauses on platform_accounts
// WHERE platform='youtube'. The 6 status counters come from the
// status column directly; the 6 DoD-OK counters derive from the
// state columns (last_refresh_at, last_validated_at) and the
// metadata JSONB keys (granted_scopes, canary_result,
// canary_channel_match). JSON field names are byte-equal to
// docs/OAUTH-PRODUCTION.md Step 10.
func (r *AdminRepository) CountFleetReadiness(ctx context.Context) (FleetReadinessCounts, error) {
	var c FleetReadinessCounts
	err := r.db.QueryRowContext(ctx, `
		SELECT
		    COUNT(*)                                              AS total,
		    COUNT(*) FILTER (WHERE status = 'active')             AS active,
		    COUNT(*) FILTER (WHERE status = 'pending_authorization') AS pending_authorization,
		    COUNT(*) FILTER (WHERE status = 'reauth_required')    AS reauth_required,
		    COUNT(*) FILTER (WHERE status = 'revoked')             AS revoked,
		    COUNT(*) FILTER (WHERE status = 'error')               AS error,
		    COUNT(*) FILTER (WHERE last_refresh_at  IS NOT NULL
		                       AND status = 'active')               AS refresh_test_ok,
		    COUNT(*) FILTER (WHERE COALESCE(metadata->>'granted_scopes', '') LIKE '%youtube.upload%')    AS scope_youtube_upload_ok,
		    COUNT(*) FILTER (WHERE COALESCE(metadata->>'granted_scopes', '') LIKE '%youtube.readonly%') AS scope_youtube_readonly_ok,
		    COUNT(*) FILTER (WHERE last_validated_at IS NOT NULL)  AS channel_binding_ok,
		    COUNT(*) FILTER (WHERE COALESCE(metadata->>'canary_result', '') = 'ok') AS private_canary_ok,
		    COUNT(*) FILTER (WHERE COALESCE(metadata->>'canary_channel_match', 'false') = 'true')  AS canary_channel_match_ok
		FROM platform_accounts
		WHERE platform = 'youtube'`,
	).Scan(
		&c.Total,
		&c.Active,
		&c.PendingAuthorization,
		&c.ReauthRequired,
		&c.Revoked,
		&c.Error,
		&c.RefreshTestOK,
		&c.ScopeYoutubeUploadOK,
		&c.ScopeYoutubeReadonlyOK,
		&c.ChannelBindingOK,
		&c.PrivateCanaryOK,
		&c.CanaryChannelMatchOK,
	)
	if err != nil {
		return c, fmt.Errorf("admin: count fleet readiness: %w", err)
	}
	return c, nil
}

// // CreateFleetReadinessSnapshot is the single round-trip service
// behind GET /admin/youtube/fleet_readiness. In ONE transaction
// (RepeatableRead isolation) it:
//
//  1. Aggregates the 12 DoD counters via a COUNT(*) FILTER query on
//     platform_accounts (one roundtrip; mirrors the ChannelCounts
//     pattern at the top of this file).
//  2. Inserts a fleet_readiness_snapshots parent row carrying
//     adminUserID + the JSON-marshalled counts as summary_json.
//  3. INSERT…SELECTs every YouTube platform_account row into
//     fleet_readiness_snapshot_channels with the 12 per-channel
//     fields docs/OAUTH-PRODUCTION.md Step 10 names verbatim.
//
// Transaction guarantees: callers see consistent aggregate + row
// data within a single snapshot. A concurrent platform_accounts
// UPDATE during the snapshot either:
//   - happens BEFORE the snapshot started -> invisible (RepeatableRead
//     snapshots the row state at tx-start time);
//   - happens AFTER the snapshot committed -> visible in the next
//     snapshot, NOT this one.
//
// Without the tx + RepeatableRead, the 3 queries (aggregate / parent
// insert / per-channel insert...select) would race against any
// concurrent bind / disconnect / status-change, and the JSON
// counters would not be guaranteed to match the per-channel rows.
// REPEATABLE READ in Postgres is the dedicated snapshot-isolation
// mode (vs SERIALIZABLE which adds Serializable Snapshot Isolation
// on top + aborts on detected conflicts); REPEATABLE READ here is
// the minimum needed for the audit-consistency contract.
//
// The granted_scopes column is sourced from
// platform_accounts.metadata->'granted_scopes' (a JSONB array
// written by the OAuth callback chain post-bind). Empty array when
// the metadata key is missing. The TEXT[] type matches
// oauth_connections.scopes (migration 043) so a future follow-up
// can JOIN instead without a column-type migration.
//
// Returns the snapshot response (counts + snapshot UUID + taken_at)
// for the operator dashboard to render.
func (r *AdminRepository) CreateFleetReadinessSnapshot(ctx context.Context, adminUserID int64) (FleetReadinessSnapshotResponse, error) {
	var resp FleetReadinessSnapshotResponse

	// Open a single tx at REPEATABLE READ. The RepeatableRead
	// isolation level guarantees the per-statement snapshots are
	// pinned to tx-start so the aggregate + per-channel reads see
	// the same row state. Rollback on any non-commit path (the
	// closed flag guards against double-Rollback if Commit fires
	// first).
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return resp, fmt.Errorf("admin: begin fleet readiness tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	// Single round-trip on the aggregates. Mirrors the ChannelCounts
	// shape (this package's other COUNT(*) FILTER pattern).
	err = tx.QueryRowContext(ctx, `
		SELECT
		    COUNT(*)                                              AS total,
		    COUNT(*) FILTER (WHERE status = 'active')             AS active,
		    COUNT(*) FILTER (WHERE status = 'pending_authorization') AS pending_authorization,
		    COUNT(*) FILTER (WHERE status = 'reauth_required')    AS reauth_required,
		    COUNT(*) FILTER (WHERE status = 'revoked')             AS revoked,
		    COUNT(*) FILTER (WHERE status = 'error')               AS error,
		    COUNT(*) FILTER (WHERE last_refresh_at  IS NOT NULL
		                       AND status = 'active')               AS refresh_test_ok,
		    COUNT(*) FILTER (WHERE COALESCE(metadata->>'granted_scopes', '') LIKE '%youtube.upload%')    AS scope_youtube_upload_ok,
		    COUNT(*) FILTER (WHERE COALESCE(metadata->>'granted_scopes', '') LIKE '%youtube.readonly%') AS scope_youtube_readonly_ok,
		    COUNT(*) FILTER (WHERE last_validated_at IS NOT NULL)  AS channel_binding_ok,
		    COUNT(*) FILTER (WHERE COALESCE(metadata->>'canary_result', '') = 'ok') AS private_canary_ok,
		    COUNT(*) FILTER (WHERE COALESCE(metadata->>'canary_channel_match', 'false') = 'true')  AS canary_channel_match_ok
		FROM platform_accounts
		WHERE platform = 'youtube'`,
	).Scan(
		&resp.FleetReadiness.Total,
		&resp.FleetReadiness.Active,
		&resp.FleetReadiness.PendingAuthorization,
		&resp.FleetReadiness.ReauthRequired,
		&resp.FleetReadiness.Revoked,
		&resp.FleetReadiness.Error,
		&resp.FleetReadiness.RefreshTestOK,
		&resp.FleetReadiness.ScopeYoutubeUploadOK,
		&resp.FleetReadiness.ScopeYoutubeReadonlyOK,
		&resp.FleetReadiness.ChannelBindingOK,
		&resp.FleetReadiness.PrivateCanaryOK,
		&resp.FleetReadiness.CanaryChannelMatchOK,
	)
	if err != nil {
		return resp, fmt.Errorf("admin: fleet readiness aggregate counts: %w", err)
	}

	// Marshal the (now-populated) counts for the parent row's
	// summary_json mirror field.
	summaryJSON, _ := json.Marshal(resp.FleetReadiness)

	// Insert parent row. RETURNING gives us the generated UUID
	// (gen_random_uuid()) AND taken_at (server time, NOT the
	// wall-clock before the tx started); both round-trip in the
	// same network message.
	var snapshotID string
	var takenAt time.Time
	err = tx.QueryRowContext(ctx, `
		INSERT INTO fleet_readiness_snapshots (operator_user_id, summary_json)
		VALUES ($1, $2)
		RETURNING id, taken_at`,
		adminUserID, summaryJSON,
	).Scan(&snapshotID, &takenAt)
	if err != nil {
		return resp, fmt.Errorf("admin: insert fleet readiness parent snapshot: %w", err)
	}

	// Per-channel dump. INSERT…SELECT keeps the round-trip count
	// to 1 for the entire snapshot (aggregate + parent + child =
	// 3 query-messages inside ONE tx). For a 200-channel fleet this
	// is 200 rows; bounded by the platform_accounts WHERE
	// platform = 'youtube' predicate (we never include Drive/IG/etc.).
	_, err = tx.ExecContext(ctx, `
		INSERT INTO fleet_readiness_snapshot_channels (
		    snapshot_id, platform_account_id, channel_id, channel_name,
		    manager_email, oauth_connection_id, granted_scopes,
		    last_refresh_at, last_binding_check_at,
		    canary_video_id, canary_result, last_error_code
		)
		SELECT
		    $1::uuid,
		    pa.id,
		    COALESCE(pa.platform_user_id, ''),
		    COALESCE(pa.username, ''),
		    COALESCE(pa.metadata->>'manager_email_hint', ''),
		    pa.oauth_connection_id,
		    COALESCE(
		        ARRAY(
		            SELECT jsonb_array_elements_text(
		                COALESCE(pa.metadata->'granted_scopes', '[]'::jsonb)
		            )
		        ),
		        ARRAY[]::TEXT[]
		    ),
		    pa.last_refresh_at,
		    pa.last_validated_at,
		    COALESCE(pa.metadata->>'canary_video_id', ''),
		    COALESCE(pa.metadata->>'canary_result', ''),
		    COALESCE(pa.last_error_code, '')
		FROM platform_accounts pa
		WHERE pa.platform = 'youtube'`,
		snapshotID,
	)
	if err != nil {
		return resp, fmt.Errorf("admin: insert fleet readiness per-channel rows: %w", err)
	}

	// Final commit. The defer'd Rollback sees `committed=true` and
	// skips. If we never reach this line (a panic in BETWEEN), the
	// defer fires Rollback and Postgres releases the row-lock set
	// by the tx.
	if err := tx.Commit(); err != nil {
		return resp, fmt.Errorf("admin: commit fleet readiness tx: %w", err)
	}
	committed = true

	resp.SnapshotID = snapshotID
	resp.TakenAt = takenAt
	return resp, nil
}
