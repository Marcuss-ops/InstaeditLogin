package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"
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
