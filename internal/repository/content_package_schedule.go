package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// Schedule lifecycle for Content Packages: upsert/find, worker claims with
// SKIP LOCKED leases, prepared/blocked/retry terminal-and-continue
// transitions, and cancel. Split from content_package_repo.go (see the
// pointer comment there).

func (r *ContentPackageRepository) UpsertSchedule(ctx context.Context, schedule *models.ContentSchedule, expectedVersion int64) error {
	if schedule == nil || schedule.ContentPackageID <= 0 || expectedVersion <= 0 || schedule.Timezone == "" || !schedule.PrepareAt.Before(schedule.ScheduledAt) {
		return errors.New("valid schedule and expected package version are required")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := tx.QueryRowContext(ctx,
		`INSERT INTO content_schedules (content_package_id, scheduled_at, prepare_at, timezone, status, package_version)
		 SELECT $1,$2,$3,$4,'scheduled',version+1 FROM content_packages WHERE id=$1 AND version=$5
		 ON CONFLICT (content_package_id) DO UPDATE SET scheduled_at=EXCLUDED.scheduled_at, prepare_at=EXCLUDED.prepare_at, timezone=EXCLUDED.timezone, status='scheduled', package_version=EXCLUDED.package_version, updated_at=NOW()
		 RETURNING id, content_package_id, scheduled_at, prepare_at, timezone, status, package_version, created_at, updated_at`,
		schedule.ContentPackageID, schedule.ScheduledAt, schedule.PrepareAt, schedule.Timezone, expectedVersion,
	).Scan(&schedule.ID, &schedule.ContentPackageID, &schedule.ScheduledAt, &schedule.PrepareAt,
		&schedule.Timezone, &schedule.Status, &schedule.PackageVersion, &schedule.CreatedAt, &schedule.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrContentPackageVersionConflict
		}
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE content_packages SET state='scheduled', version=version+1, updated_at=NOW() WHERE id=$1 AND version=$2`, schedule.ContentPackageID, expectedVersion); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *ContentPackageRepository) FindSchedule(ctx context.Context, packageID int64) (*models.ContentSchedule, error) {
	s := &models.ContentSchedule{}
	err := r.db.QueryRowContext(ctx,
		`SELECT id, content_package_id, scheduled_at, prepare_at, timezone, status, package_version, created_at, updated_at, lease_owner, lease_expires_at, heartbeat_at, attempt_count, next_attempt_at FROM content_schedules WHERE content_package_id=$1`, packageID).Scan(&s.ID, &s.ContentPackageID, &s.ScheduledAt, &s.PrepareAt, &s.Timezone, &s.Status, &s.PackageVersion, &s.CreatedAt, &s.UpdatedAt, &s.LeaseOwner, &s.LeaseExpiresAt, &s.HeartbeatAt, &s.AttemptCount, &s.NextAttemptAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return s, err
}

func (r *ContentPackageRepository) ClaimDueSchedules(ctx context.Context, workerID string, lease time.Duration, limit int) ([]*models.ContentSchedule, error) {
	if workerID == "" {
		return nil, errors.New("worker id is required")
	}
	if lease <= 0 {
		lease = 5 * time.Minute
	}
	if limit <= 0 || limit > 100 {
		limit = 10
	}
	rows, err := r.db.QueryContext(ctx,
		`WITH candidates AS (
		 SELECT id FROM content_schedules
		 WHERE status IN ('scheduled','preparing') AND prepare_at <= NOW()
		   AND (next_attempt_at IS NULL OR next_attempt_at <= NOW())
		   AND (lease_expires_at IS NULL OR lease_expires_at <= NOW())
		 ORDER BY prepare_at, id LIMIT $1 FOR UPDATE SKIP LOCKED
		 )
		 UPDATE content_schedules s
		 SET status='preparing', lease_owner=$2, lease_expires_at=NOW()+$3::interval,
		     heartbeat_at=NOW(), updated_at=NOW()
		 FROM candidates c WHERE s.id=c.id
		 RETURNING s.id, s.content_package_id, s.scheduled_at, s.prepare_at, s.timezone,
		           s.status, s.package_version, s.created_at, s.updated_at,
		           s.lease_owner, s.lease_expires_at, s.heartbeat_at, s.attempt_count, s.next_attempt_at`,
		limit, workerID, fmt.Sprintf("%f seconds", lease.Seconds()))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.ContentSchedule
	for rows.Next() {
		s := &models.ContentSchedule{}
		if err := rows.Scan(&s.ID, &s.ContentPackageID, &s.ScheduledAt, &s.PrepareAt, &s.Timezone, &s.Status, &s.PackageVersion, &s.CreatedAt, &s.UpdatedAt, &s.LeaseOwner, &s.LeaseExpiresAt, &s.HeartbeatAt, &s.AttemptCount, &s.NextAttemptAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *ContentPackageRepository) MarkSchedulePrepared(ctx context.Context, scheduleID int64, workerID string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var packageID int64
	if err := tx.QueryRowContext(ctx,
		`UPDATE content_schedules
		 SET status='ready_to_publish', lease_owner=NULL, lease_expires_at=NULL,
		     heartbeat_at=NULL, updated_at=NOW()
		 WHERE id=$1 AND status='preparing' AND lease_owner=$2
		 RETURNING content_package_id`, scheduleID, workerID).Scan(&packageID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrContentPackageVersionConflict
		}
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE content_packages SET state='ready_to_publish', updated_at=NOW() WHERE id=$1`, packageID); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *ContentPackageRepository) MarkScheduleBlocked(ctx context.Context, scheduleID int64, workerID, reason string) error {
	_ = reason // The publication event stores the human-readable blocker.
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var packageID int64
	if err := tx.QueryRowContext(ctx,
		`UPDATE content_schedules
		 SET status='blocked', lease_owner=NULL, lease_expires_at=NULL,
		     heartbeat_at=NULL, updated_at=NOW()
		 WHERE id=$1 AND status='preparing' AND lease_owner=$2
		 RETURNING content_package_id`, scheduleID, workerID).Scan(&packageID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrContentPackageVersionConflict
		}
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE content_packages SET state='blocked', updated_at=NOW() WHERE id=$1`, packageID); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *ContentPackageRepository) MarkScheduleRetry(ctx context.Context, scheduleID int64, workerID string, nextAttempt time.Time, reason string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE content_schedules SET status='scheduled', attempt_count=attempt_count+1, next_attempt_at=$1, lease_owner=NULL, lease_expires_at=NULL, heartbeat_at=NULL, updated_at=NOW() WHERE id=$2 AND status='preparing' AND lease_owner=$3`, nextAttempt, scheduleID, workerID)
	return err
}

func (r *ContentPackageRepository) CancelSchedule(ctx context.Context, packageID, expectedVersion int64) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx,
		`UPDATE content_schedules SET status='cancelled', updated_at=NOW()
		 WHERE content_package_id=$1 AND status NOT IN ('published','cancelled')`, packageID)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return ErrContentPackageNotFound
	}
	result, err = tx.ExecContext(ctx,
		`UPDATE content_packages SET state='draft', version=version+1, updated_at=NOW() WHERE id=$1 AND version=$2`, packageID, expectedVersion)
	if err != nil {
		return err
	}
	n, _ = result.RowsAffected()
	if n == 0 {
		return ErrContentPackageVersionConflict
	}
	return tx.Commit()
}
