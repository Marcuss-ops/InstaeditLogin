package repository

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// ListByUser returns upload_jobs scoped to userID, optionally narrowed
// by filter. Ordered by scheduled_at ASC NULLS LAST so the calendar's
// "earliest first" presentation is one round trip. The Limit defaults
// to 200 when zero; callers that need ALL rows should page (call with
// LIMIT + To set to the last seen scheduled_at).
//
// Security: user_id is the FIRST predicate so a stolen job id from a
// different tenant cannot be enumerated — the index btree on
// (user_id, created_at DESC) makes that part O(log n).
//
// Performance: GIN index on `targets` (migration 040) makes the
// per-account filter O(matching-rows) via the jsonb_ops
// containment opclass. Combined with the user_id btree via BitmapAnd,
// the planner sticks to indexes for any reasonable scale.
func (r *UploadJobRepository) ListByUser(userID int64, filter UploadJobListFilter) ([]models.UploadJob, error) {
	if filter.Limit <= 0 {
		filter.Limit = uploadJobListDefaultLimit
	}

	var (
		accountID sql.NullInt64
		status    sql.NullString
		timeFrom  sql.NullTime
		timeTo    sql.NullTime
	)
	if filter.AccountID != nil {
		accountID = sql.NullInt64{Int64: *filter.AccountID, Valid: true}
	}
	if filter.Status != nil {
		status = sql.NullString{String: string(*filter.Status), Valid: true}
	}
	if filter.From != nil {
		timeFrom = sql.NullTime{Time: *filter.From, Valid: true}
	}
	if filter.To != nil {
		timeTo = sql.NullTime{Time: *filter.To, Valid: true}
	}

	// P1 (migration 053) — appended default_privacy_level to the projection so
	// ListByUser returns it for the dashboard's "what privacy will this row
	// publish at" preview column (future taglio).
	rows, err := r.db.Query(
		`SELECT id, user_id, workspace_id, source_type, source_id, drive_account_id, folder_id, title, caption,
		        targets, status, error_message, post_id, asset_id, ingest_after, publish_at, created_at, updated_at,
		        attempt_count, max_attempts, next_attempt_at, lease_owner, lease_expires_at, heartbeat_at,
		        progress_bytes, total_bytes, error_code, priority, started_at, completed_at,
		        youtube_session_uri, youtube_session_offset, youtube_session_expires_at, youtube_chunk_size, youtube_last_chunk_at,
		        default_privacy_level, metadata
		 FROM upload_jobs
		 WHERE user_id = $1
		   AND ($2::bigint              IS NULL OR targets @> jsonb_build_array($2::bigint))
		   AND ($3::upload_job_status   IS NULL OR status = $3::upload_job_status)
		   AND ($4::timestamptz         IS NULL OR publish_at >= $4)
		   AND ($5::timestamptz         IS NULL OR publish_at <= $5)
		   AND (
				$6::bool OR
				(
					$7::timestamptz IS NULL AND $8::bigint = 0 AND $9::bool = false
				)
				OR (
					$9::bool = false AND publish_at IS NOT NULL AND
					($7::timestamptz IS NULL OR (publish_at, id) > ($7, $8))
				)
				OR (
					$9::bool = true AND publish_at IS NULL AND id > $8
				)
			)
		 ORDER BY publish_at ASC NULLS LAST, id ASC
		 LIMIT $10`,
		userID, accountID, status, timeFrom, timeTo,
		filter.AfterPublishAt == nil && filter.AfterID == 0 && !filter.AfterPublishNull,
		filter.AfterPublishAt, filter.AfterID, filter.AfterPublishNull, filter.Limit,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list upload_jobs by user: %w", err)
	}
	defer rows.Close()

	var out []models.UploadJob
	for rows.Next() {
		job, scanErr := scanUploadJobRows(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("failed to scan upload job: %w", scanErr)
		}
		out = append(out, *job)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate upload jobs: %w", err)
	}
	return out, nil
}

// Reschedule atomically updates scheduled_at for a pending upload_job.
// Security: scoped to BOTH id AND user_id — a stolen job id from
// another tenant returns ErrUploadJobNotFound (the handler maps it to
// 404 the same as a non-existent id; no information leak).
//
// Idempotency / state machine: only status='pending' rows can be
// rescheduled. Once the worker has claimed the row (status='processing')
// or it's terminal (completed/failed), the UPDATE matches zero rows and
// we return ErrUploadJobNotFound. This is the desired UX: dragging a
// chip that the worker has already picked up should surface a clean
// error, not silently mutate a row that's mid-publish.
//
// newScheduledAt must be in the future. The handler enforces this with
// 400; the repository itself is permissive (operator scripts may want
// to backdate for testing) — defence-in-depth without an opinionated
// invariant.
func (r *UploadJobRepository) Reschedule(jobID, userID int64, newPublishAt time.Time) (models.UploadJob, error) {
	res, err := r.db.Exec(
		`UPDATE upload_jobs
		 SET publish_at = $3, updated_at = NOW()
		 WHERE id = $1 AND user_id = $2 AND status = 'pending'`,
		jobID, userID, newPublishAt,
	)
	if err != nil {
		return models.UploadJob{}, fmt.Errorf("failed to reschedule upload job: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return models.UploadJob{}, fmt.Errorf("failed to read rows affected: %w", err)
	}
	if n == 0 {
		// Either id is wrong, id belongs to another tenant, OR the
		// row already left 'pending' (worker claimed / ingested / publish_failed).
		// All three return the same sentinel — no information leak.
		return models.UploadJob{}, ErrUploadJobNotFound
	}
	job, err := r.FindByID(jobID)
	if err != nil {
		return models.UploadJob{}, fmt.Errorf("failed to re-read after reschedule: %w", err)
	}
	if job == nil || job.UserID != userID {
		// Defensive: the row was deleted between UPDATE and re-read.
		return models.UploadJob{}, ErrUploadJobNotFound
	}
	return *job, nil
}

// PendingCountsByAccount returns the GROUP BY per target for every
// pending upload owned by userID. Uses jsonb_array_elements_text to
// unnest the JSONB `targets` column into bigints at the SQL layer
// (cheaper than fetching rows + bucketing in Go). The query hits:
//   - the GIN index on targets (migration 040) for the LATERAL unnesting
//   - the (user_id, status) btree for the WHERE clause
//
// so it's an index range scan + a small hash aggregate. Order is
// stable on account_id ASC so the SPA can rely on row order for
// optimistic renders.
func (r *UploadJobRepository) PendingCountsByAccount(userID int64) ([]UploadJobPendingCount, error) {
	rows, err := r.db.Query(
		`SELECT
			e.elem::bigint        AS account_id,
			COUNT(*)              AS pending_count,
			MIN(u.publish_at)     AS next_publish_at
		 FROM upload_jobs u
		 CROSS JOIN LATERAL jsonb_array_elements_text(u.targets) AS e(elem)
		 WHERE u.user_id    = $1
		   AND u.status     = 'pending'
		   AND u.publish_at IS NOT NULL
		 GROUP BY e.elem::bigint
		 ORDER BY account_id ASC`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to aggregate upload_jobs by target: %w", err)
	}
	defer rows.Close()

	var out []UploadJobPendingCount
	for rows.Next() {
		var c UploadJobPendingCount
		var nextAt sql.NullTime
		if err := rows.Scan(&c.AccountID, &c.Count, &nextAt); err != nil {
			return nil, fmt.Errorf("failed to scan pending-count row: %w", err)
		}
		if nextAt.Valid {
			t := nextAt.Time
			c.NextPublishAt = &t
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate pending counts: %w", err)
	}
	return out, nil
}

// PendingDistinctCount returns the user's total number of pending
// upload_jobs as DISTINCT rows (not per-target expansions). The
// dashboard's "Pending uploads" stat reads from this — SUM over the
// PendingCountsByAccount result would over-count one upload that
// targets multiple accounts (e.g. drive_batch on FB+IG). Hits the
// (user_id, status) btree so the planner does an index-only count,
// no row fetch needed.
func (r *UploadJobRepository) PendingDistinctCount(userID int64) (int64, error) {
	var n int64
	err := r.db.QueryRow(
		`SELECT COUNT(*)
		 FROM upload_jobs
		 WHERE user_id = $1 AND status = 'pending'`,
		userID,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("failed to count distinct pending uploads: %w", err)
	}
	return n, nil
}

// Cancel atomically deletes a pending upload_job. Same authz + state-
// machine contract as Reschedule: scoped to (id, user_id, status=pending)
// so a stolen id, or one that's already been claimed/processed/finished,
// returns ErrUploadJobNotFound without leaking the distinction.
//
// Concurrent-claim safety: the publish worker uses
// `SELECT ... FOR UPDATE SKIP LOCKED` on pending rows; this DELETE
// holds an implicit row lock from the WHERE predicate. Whichever tx
// lands first wins; the other sees zero rows affected and surfaces an
// error to the user. The post-Cancel row is gone, so the worker's
// next claim skips it cleanly.
func (r *UploadJobRepository) Cancel(jobID, userID int64) error {
	res, err := r.db.Exec(
		`DELETE FROM upload_jobs
		 WHERE id = $1
		   AND user_id = $2
		   AND status = 'pending'`,
		jobID, userID,
	)
	if err != nil {
		return fmt.Errorf("failed to cancel upload job: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to read rows affected: %w", err)
	}
	if n == 0 {
		return ErrUploadJobNotFound
	}
	return nil
}

// AggregateByFolder returns a per-folder rollup of upload_jobs scoped to
// a single user. Authz: matches BOTH folder_id AND user_id so a folder id
// from another tenant cannot leak counts into the dashboard. Uses a
// single indexed FILTER aggregation (Postgres-specific) instead of
// N separate COUNT queries — one round-trip, one row returned.
//
// If no row matches, the returned BatchStatusSummary has all-zero counts
// and nil timestamps. The handler turns that into a 200 + zero
// dashboard rather than a 404, so an immediate-after-import poll does
// not jump into a not-found UI state.
func (r *UploadJobRepository) AggregateByFolder(folderID string, userID int64) (models.BatchStatusSummary, error) {
	row := r.db.QueryRow(
		`SELECT
			COUNT(*) FILTER (WHERE status = 'pending')         AS pending_count,
			COUNT(*) FILTER (WHERE status = 'retry_wait')      AS retry_wait_count,
			COUNT(*) FILTER (WHERE status = 'leased')          AS leased_count,
			COUNT(*) FILTER (WHERE status = 'processing')      AS processing_count,
			COUNT(*) FILTER (WHERE status = 'ingest_completed') AS ready_to_publish_count,
			COUNT(*) FILTER (WHERE status = 'publish_completed') AS completed_count,
			COUNT(*) FILTER (WHERE status = 'failed')          AS failed_count,
			COUNT(*) FILTER (WHERE status = 'dead_letter')     AS dead_letter_count,
			COUNT(*) FILTER (WHERE status = 'cancelled')       AS cancelled_count,
			MIN(publish_at) AS first_publish_at,
			MAX(publish_at) AS last_publish_at
		 FROM upload_jobs
		 WHERE folder_id = $1
		   AND user_id    = $2`,
		folderID, userID,
	)
	var summary models.BatchStatusSummary
	var firstAt, lastAt sql.NullTime
	if err := row.Scan(
		&summary.PendingCount,
		&summary.RetryWaitCount,
		&summary.LeasedCount,
		&summary.ProcessingCount,
		&summary.ReadyToPublishCount,
		&summary.CompletedCount,
		&summary.FailedCount,
		&summary.DeadLetterCount,
		&summary.CancelledCount,
		&firstAt,
		&lastAt,
	); err != nil {
		return summary, fmt.Errorf("failed to aggregate upload_jobs by folder: %w", err)
	}
	// keep aligned with UploadJobStatus enum — a future new status
	// (e.g. 'cancelled' has been added in migration 045) must add a
	// COUNT FILTER clause above AND a term in this sum, otherwise it
	// silently drops off the dashboard.
	summary.TotalCount = summary.PendingCount + summary.RetryWaitCount + summary.LeasedCount +
		summary.ProcessingCount + summary.ReadyToPublishCount + summary.CompletedCount +
		summary.FailedCount + summary.DeadLetterCount + summary.CancelledCount
	if firstAt.Valid {
		t := firstAt.Time
		summary.FirstPublishAt = &t
	}
	if lastAt.Valid {
		t := lastAt.Time
		summary.LastPublishAt = &t
	}
	return summary, nil
}
