package repository

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// UploadJobRepository handles persistence for upload_jobs — the background
// queue that downloads videos from Google Drive and publishes them.
type UploadJobRepository struct {
	db *sql.DB
}

// NewUploadJobRepository creates a new UploadJobRepository.
func NewUploadJobRepository(db *sql.DB) *UploadJobRepository {
	return &UploadJobRepository{db: db}
}

// Create inserts a new upload job and returns the generated id plus timestamps.
// scheduled_at and folder_id are written as sql.Null* so the nullable
// migration-037 + migration-038 columns accept both NULL (legacy
// single-file imports without a folder scope) and a populated value.
// Note that Today we use folder_id only on batch-driven drive imports;
// single-file async imports leave it NULL and are excluded from per-folder
// status queries (the partial index covers only non-NULL rows).
func (r *UploadJobRepository) Create(job *models.UploadJob) error {
	targetsJSON, err := job.TargetsJSON()
	if err != nil {
		return fmt.Errorf("failed to marshal upload job targets: %w", err)
	}

	var scheduledAt sql.NullTime
	if job.ScheduledAt != nil {
		scheduledAt = sql.NullTime{Time: *job.ScheduledAt, Valid: true}
	}
	var folderID sql.NullString
	if job.FolderID != nil {
		folderID = sql.NullString{String: *job.FolderID, Valid: true}
	}

	return r.db.QueryRow(
		`INSERT INTO upload_jobs
			(user_id, workspace_id, source_type, source_id, drive_account_id, folder_id, title, caption, targets, status, scheduled_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING id, created_at, updated_at`,
		job.UserID,
		job.WorkspaceID,
		string(job.SourceType),
		job.SourceID,
		job.DriveAccountID,
		folderID,
		job.Title,
		job.Caption,
		targetsJSON,
		string(job.Status),
		scheduledAt,
	).Scan(&job.ID, &job.CreatedAt, &job.UpdatedAt)
}

// FindByID returns the upload job with the given id, or (nil, nil) if not found.
func (r *UploadJobRepository) FindByID(id int64) (*models.UploadJob, error) {
	row := r.db.QueryRow(
		`SELECT id, user_id, workspace_id, source_type, source_id, drive_account_id, folder_id, title, caption,
		        targets, status, error_message, post_id, asset_id, scheduled_at, created_at, updated_at
		 FROM upload_jobs
		 WHERE id = $1`,
		id,
	)
	return scanUploadJob(row)
}

// ClaimNext atomically claims the oldest pending upload job, transitioning
// its status to 'processing'. Returns (nil, nil) when no pending job exists.
func (r *UploadJobRepository) ClaimNext() (*models.UploadJob, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("failed to begin upload job claim tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	row := tx.QueryRow(
		`SELECT id, user_id, workspace_id, source_type, source_id, drive_account_id, folder_id, title, caption,
		         targets, status, error_message, post_id, asset_id, scheduled_at, created_at, updated_at
		  FROM upload_jobs
		 WHERE status = 'pending'
		   AND (scheduled_at IS NULL OR scheduled_at <= NOW())
		 ORDER BY COALESCE(scheduled_at, created_at) ASC, id ASC
		 FOR UPDATE SKIP LOCKED
		 LIMIT 1`,
	)
	job, err := scanUploadJob(row)
	if err != nil {
		return nil, err
	}
	if job == nil {
		return nil, nil
	}

	_, err = tx.Exec(
		`UPDATE upload_jobs SET status = 'processing', updated_at = NOW() WHERE id = $1`,
		job.ID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to mark upload job as processing: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit upload job claim: %w", err)
	}

	job.Status = models.UploadJobStatusProcessing
	return job, nil
}

// MarkCompleted updates the job to completed and stores the resulting post/asset IDs.
func (r *UploadJobRepository) MarkCompleted(id int64, postID int64, assetID string) error {
	_, err := r.db.Exec(
		`UPDATE upload_jobs
		 SET status = 'completed', post_id = $2, asset_id = $3, error_message = NULL, updated_at = NOW()
		 WHERE id = $1`,
		id, postID, assetID,
	)
	if err != nil {
		return fmt.Errorf("failed to mark upload job completed: %w", err)
	}
	return nil
}

// MarkFailed updates the job to failed with an error message.
func (r *UploadJobRepository) MarkFailed(id int64, errMessage string) error {
	_, err := r.db.Exec(
		`UPDATE upload_jobs
		 SET status = 'failed', error_message = $2, updated_at = NOW()
		 WHERE id = $1`,
		id, errMessage,
	)
	if err != nil {
		return fmt.Errorf("failed to mark upload job failed: %w", err)
	}
	return nil
}

// UploadJobListFilter narrows the rows returned by ListByUser and
// ListByAccount. Zero-value fields are interpreted as "no filter"; the
// handler applies only the predicates it has non-zero values for. This
// keeps the SQL simple (one statement, NULL-or-equal predicates) and
// lets the caller opt into any combination of filters without us
// having to maintain N specialised query methods.
type UploadJobListFilter struct {
	AccountID *int64                  // restrict to jobs whose targets @> jsonb_build_array(AccountID)
	Status    *models.UploadJobStatus // restrict to one of the 4 enum values
	From      *time.Time              // scheduled_at >= From (nil = no lower bound)
	To        *time.Time              // scheduled_at <= To   (nil = no upper bound)
	Limit     int                     // hard cap; 0 = default 200
}

// ErrUploadJobNotFound is the typed sentinel Reschedule/Cancel return
// to differentiate "job id doesn't exist" from "job id exists but
// already moved past pending (worker claimed / completed / failed)".
// The handler maps both to 404 — leaking the distinction would let a
// caller probe whether an id has been processed yet.
var ErrUploadJobNotFound = errors.New("upload job not found or no longer pending")

const uploadJobListDefaultLimit = 200

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

	rows, err := r.db.Query(
		`SELECT id, user_id, workspace_id, source_type, source_id, drive_account_id, folder_id, title, caption,
		        targets, status, error_message, post_id, asset_id, scheduled_at, created_at, updated_at
		 FROM upload_jobs
		 WHERE user_id = $1
		   AND ($2::bigint              IS NULL OR targets @> jsonb_build_array($2::bigint))
		   AND ($3::upload_job_status   IS NULL OR status = $3::upload_job_status)
		   AND ($4::timestamptz         IS NULL OR scheduled_at >= $4)
		   AND ($5::timestamptz         IS NULL OR scheduled_at <= $5)
		 ORDER BY scheduled_at ASC NULLS LAST, id ASC
		 LIMIT $6`,
		userID, accountID, status, timeFrom, timeTo, filter.Limit,
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
func (r *UploadJobRepository) Reschedule(jobID, userID int64, newScheduledAt time.Time) (models.UploadJob, error) {
	res, err := r.db.Exec(
		`UPDATE upload_jobs
		 SET scheduled_at = $3, updated_at = NOW()
		 WHERE id = $1 AND user_id = $2 AND status = 'pending'`,
		jobID, userID, newScheduledAt,
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
		// row already left 'pending' (worker claimed / published / failed).
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

// UploadJobPendingCount is the per-account rollup returned by
// PendingCountsByAccount — the single-query aggregate that backs the
// dashboard widget's per-account "Programmati" badge. It's exposed
// at the repository level so the dashboard handler can stream an
// exact count + earliest-scheduled row for every target the user
// has, in one SELECT — no client-side bucketing and no limit cap
// hiding uploads past the 200-row budget that drives the per-account
// list endpoint.
type UploadJobPendingCount struct {
	AccountID     int64
	Count         int
	NextPublishAt *time.Time
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
			MIN(u.scheduled_at)   AS next_publish_at
		 FROM upload_jobs u
		 CROSS JOIN LATERAL jsonb_array_elements_text(u.targets) AS e(elem)
		 WHERE u.user_id    = $1
		   AND u.status     = 'pending'
		   AND u.scheduled_at IS NOT NULL
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
			COUNT(*) FILTER (WHERE status = 'pending')    AS pending_count,
			COUNT(*) FILTER (WHERE status = 'processing') AS processing_count,
			COUNT(*) FILTER (WHERE status = 'completed')  AS completed_count,
			COUNT(*) FILTER (WHERE status = 'failed')     AS failed_count,
			MIN(scheduled_at) AS first_publish_at,
			MAX(scheduled_at) AS last_publish_at
		 FROM upload_jobs
		 WHERE folder_id = $1
		   AND user_id    = $2`,
		folderID, userID,
	)
	var summary models.BatchStatusSummary
	var firstAt, lastAt sql.NullTime
	if err := row.Scan(
		&summary.PendingCount,
		&summary.ProcessingCount,
		&summary.CompletedCount,
		&summary.FailedCount,
		&firstAt,
		&lastAt,
	); err != nil {
		return summary, fmt.Errorf("failed to aggregate upload_jobs by folder: %w", err)
	}
	// keep aligned with UploadJobStatus enum — a future new status
	// (e.g. 'cancelled') must add a COUNT FILTER clause above AND a
	// term in this sum, otherwise it silently drops off the dashboard.
	summary.TotalCount = summary.PendingCount + summary.ProcessingCount + summary.CompletedCount + summary.FailedCount
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

// scanUploadJobRows is the *sql.Rows equivalent of scanUploadJob —
// the column list is identical but we iterate in the caller. Splitting
// them keeps the row-vs-rows contract distinct at the type level so
// adding a column doesn't have to thread through two function bodies.
func scanUploadJobRows(rows *sql.Rows) (*models.UploadJob, error) {
	var job models.UploadJob
	var rawStatus, rawSource string
	var targetsJSON []byte
	var scheduledAt sql.NullTime
	var folderID sql.NullString
	var driveAccountID sql.NullInt64
	var errorMessage sql.NullString
	var assetID sql.NullString

	err := rows.Scan(
		&job.ID,
		&job.UserID,
		&job.WorkspaceID,
		&rawSource,
		&job.SourceID,
		&driveAccountID,
		&folderID,
		&job.Title,
		&job.Caption,
		&targetsJSON,
		&rawStatus,
		&errorMessage,
		&job.PostID,
		&assetID,
		&scheduledAt,
		&job.CreatedAt,
		&job.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to scan upload job: %w", err)
	}

	job.SourceType = models.UploadJobSource(rawSource)
	job.Status = models.UploadJobStatus(rawStatus)
	if driveAccountID.Valid {
		v := driveAccountID.Int64
		job.DriveAccountID = &v
	}
	if folderID.Valid {
		v := folderID.String
		job.FolderID = &v
	}
	if scheduledAt.Valid {
		t := scheduledAt.Time
		job.ScheduledAt = &t
	}
	if errorMessage.Valid {
		job.ErrorMessage = errorMessage.String
	}
	if assetID.Valid {
		v := assetID.String
		job.AssetID = &v
	}
	if err := json.Unmarshal(targetsJSON, &job.Targets); err != nil {
		return nil, fmt.Errorf("failed to unmarshal upload job targets: %w", err)
	}

	return &job, nil
}

func scanUploadJob(row *sql.Row) (*models.UploadJob, error) {
	var job models.UploadJob
	var rawStatus, rawSource string
	var targetsJSON []byte
	var scheduledAt sql.NullTime
	var folderID sql.NullString
	var driveAccountID sql.NullInt64
	var errorMessage sql.NullString
	var assetID sql.NullString

	err := row.Scan(
		&job.ID,
		&job.UserID,
		&job.WorkspaceID,
		&rawSource,
		&job.SourceID,
		&driveAccountID,
		&folderID,
		&job.Title,
		&job.Caption,
		&targetsJSON,
		&rawStatus,
		&errorMessage,
		&job.PostID,
		&assetID,
		&scheduledAt,
		&job.CreatedAt,
		&job.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to scan upload job: %w", err)
	}

	job.SourceType = models.UploadJobSource(rawSource)
	job.Status = models.UploadJobStatus(rawStatus)
	if driveAccountID.Valid {
		v := driveAccountID.Int64
		job.DriveAccountID = &v
	}
	if folderID.Valid {
		v := folderID.String
		job.FolderID = &v
	}
	if scheduledAt.Valid {
		t := scheduledAt.Time
		job.ScheduledAt = &t
	}
	if errorMessage.Valid {
		job.ErrorMessage = errorMessage.String
	}
	if assetID.Valid {
		v := assetID.String
		job.AssetID = &v
	}
	if err := json.Unmarshal(targetsJSON, &job.Targets); err != nil {
		return nil, fmt.Errorf("failed to unmarshal upload job targets: %w", err)
	}

	return &job, nil
}
