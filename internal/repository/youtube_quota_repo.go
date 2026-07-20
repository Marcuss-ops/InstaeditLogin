// Package repository — YouTube daily quota gate.
//
// youtube_quota_daily (migration 059) is the per-UTC-day counter the
// publish_worker checks BEFORE every YouTube Data API v3 videos.insert
// call. The pattern mirrors internal/repository/rate_limit_repo.go
// (INSERT...ON CONFLICT + FOR UPDATE on the daily row) but is sized
// for quota buckets (1 call == 1 bucket unit in the Google 2026 model)
// and paginates its counter through a single row keyed by date.
//
// Concurrency: two pods racing on minute 23:59:59 of day N must NOT
// both succeed past the limit. We serialize via SELECT … FOR UPDATE on
// the daily row, then commit (+ the gate decision). If the row is
// missing for today, the upsert synthesizes it with limit=defaultLimit
// before the SELECT locks it.
package repository

import (
	"context"
	"errors"
	"database/sql"
	"fmt"
	"time"
)

// YouTubeDailyQuotaRepository is the Postgres-backed implementation of
// the daily quota gate used by the publisher.
type YouTubeDailyQuotaRepository struct {
	db *sql.DB
}

// NewYouTubeDailyQuotaRepository constructs a repository handle against
// the live *sql.DB. Caller owns the DB; the repo does not Close it.
func NewYouTubeDailyQuotaRepository(db *sql.DB) *YouTubeDailyQuotaRepository {
	return &YouTubeDailyQuotaRepository{db: db}
}

// todayUTC returns the current UTC date truncated to midnight. Used as
// both the UPSERT target and the cache key for the daily row. Pinned
// to UTC because the quota bucket window resets at UTC midnight — NOT
// local midnight — per docs/OAUTH-PRODUCTION.md Step 6.
func todayUTC() time.Time {
	return time.Now().UTC().Truncate(24 * time.Hour)
}

// ReserveQuota atomically:
//
//   1. Upserts today's row at limit=defaultLimit.
//   2. Locks the row via SELECT … FOR UPDATE.
//   3. If calls >= limit, returns (false, retryAfterSeconds, nil) with
//      retryAfterSeconds == seconds until next UTC midnight.
//   4. Else, increments calls by 1, commits, returns (true, 0, nil).
//
// Concurrency: the FOR UPDATE serializes concurrent reservations
// across pods, so the limit is enforced strictly. defaultLimit is
// the YOUTUBE_DAILY_QUOTA_LIMIT value supplied by the publisher (the
// config knob in internal/config/config.go). We honor an inbound bump
// (defaultLimit > stored limit) so an operator can grow the ceiling
// mid-day; we do NOT shrink a stored ceiling that is already larger
// (so an operator's deliberate constraint isn't silently relaxed by
// a config typo).
func (r *YouTubeDailyQuotaRepository) ReserveQuota(ctx context.Context, defaultLimit int) (allowed bool, retryAfterSeconds int, err error) {
	if r == nil || r.db == nil {
		return false, 0, errors.New("youtube quota: nil repo or db")
	}
	if defaultLimit < 1 {
		return false, 0, fmt.Errorf("youtube quota: defaultLimit=%d must be >= 1", defaultLimit)
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, 0, fmt.Errorf("youtube quota: begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	today := todayUTC()

	// (1) Upsert today's row if it doesn't yet exist. The ON CONFLICT
	// DO NOTHING branch preserves any prior metadata so a partial day
	// already populated by RecordError isn't clobbered.
	if _, err = tx.ExecContext(ctx, `
		INSERT INTO youtube_quota_daily (date, calls, errors, "limit", last_reset_at)
		VALUES ($1, 0, 0, $2, NOW())
		ON CONFLICT (date) DO NOTHING
	`, today, defaultLimit); err != nil {
		return false, 0, fmt.Errorf("youtube quota: upsert daily row: %w", err)
	}

	// (2) Acquire row-level lock + read the stored limit.
	var callsStored, limitStored int
	if err = tx.QueryRowContext(ctx, `
		SELECT calls, "limit"
		FROM youtube_quota_daily
		WHERE date = $1
		FOR UPDATE
	`, today).Scan(&callsStored, &limitStored); err != nil {
		return false, 0, fmt.Errorf("youtube quota: lock + read: %w", err)
	}

	// (3) Honor inbound limit bumps; never silently shrink a
	// deliberately-larger stored limit.
	effectiveLimit := limitStored
	if defaultLimit > limitStored {
		if _, err = tx.ExecContext(ctx, `
			UPDATE youtube_quota_daily SET "limit" = $1 WHERE date = $2
		`, defaultLimit, today); err != nil {
			return false, 0, fmt.Errorf("youtube quota: update limit: %w", err)
		}
		effectiveLimit = defaultLimit
	}

	if callsStored >= effectiveLimit {
		// Compute retryAfterSeconds as the wall-clock gap to next UTC
		// midnight (i.e. the bucket window's natural reset boundary).
		nextMidnight := today.Add(24 * time.Hour)
		gap := time.Until(nextMidnight)
		if gap < 0 {
			gap = 0
		}
		return false, int(gap.Seconds()), nil
	}

	// (4) Increment AND commit together. The commit releases the
	// FOR UPDATE lock so the next pod can proceed.
	if _, err = tx.ExecContext(ctx, `
		UPDATE youtube_quota_daily SET calls = calls + 1 WHERE date = $1
	`, today); err != nil {
		return false, 0, fmt.Errorf("youtube quota: increment calls: %w", err)
	}

	if err = tx.Commit(); err != nil {
		return false, 0, fmt.Errorf("youtube quota: commit: %w", err)
	}
	committed = true
	return true, 0, nil
}

// RecordError bumps the errors counter for today (UTC). Called by the
// publisher when an actual videos.insert HTTP call returns 5xx, hits a
// transport error, or fails validation. Distinct from ReserveQuota's
// quota_exceeded path: RecordError is "we tried, Google said no", not
// "we decided not to try". The errors column is informational — it
// does NOT block scheduling, since quota vs. error are orthogonal
// failure modes.
//
// The function synthesizes the daily row on first-call-of-day so the
// errors counter does not silently drop the very first failure.
func (r *YouTubeDailyQuotaRepository) RecordError(ctx context.Context) error {
	if r == nil || r.db == nil {
		return errors.New("youtube quota: nil repo or db")
	}
	today := todayUTC()
	if _, err := r.db.ExecContext(ctx, `
		INSERT INTO youtube_quota_daily (date, calls, errors, "limit", last_reset_at)
		VALUES ($1, 0, 1, 300, NOW())
		ON CONFLICT (date) DO UPDATE SET errors = youtube_quota_daily.errors + 1
	`, today); err != nil {
		return fmt.Errorf("youtube quota: record error: %w", err)
	}
	return nil
}

// GetSnapshot returns the current daily row's (calls, errors, limit,
// last_reset_at) as an externally-readable snapshot. Used by the
// /admin/health endpoint and by the existing YouTubeQuotaApproximation
// rebuild — both are read-only and do NOT touch the row.
//
// Naming: the second return is `errCount` (NOT `errors`) because a
// named return value of type `int` named `errors` would SHADOW the
// imported `errors` package inside this function's scope — every
// `errors.New(...)` / `errors.Is(...)` call would fail to compile
// (the int return has no New/Is method). Renaming to `errCount`
// keeps the package accessible. The DB column name on the
// SELECT remains the literal `errors` — Scan binds via address so
// `&errCount` is what postgres populates with the column value.
func (r *YouTubeDailyQuotaRepository) GetSnapshot(ctx context.Context) (calls, errCount, limit int, lastResetAt time.Time, err error) {
	if r == nil || r.db == nil {
		return 0, 0, 0, time.Time{}, errors.New("youtube quota: nil repo or db")
	}
	today := todayUTC()
	if err := r.db.QueryRowContext(ctx, `
		SELECT calls, errors, "limit", last_reset_at
		FROM youtube_quota_daily
		WHERE date = $1
	`, today).Scan(&calls, &errCount, &limit, &lastResetAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, 0, 0, time.Time{}, nil // no row yet today — zero snapshot is honest
		}
		return 0, 0, 0, time.Time{}, fmt.Errorf("youtube quota: get snapshot: %w", err)
	}
	return calls, errCount, limit, lastResetAt, nil
}
