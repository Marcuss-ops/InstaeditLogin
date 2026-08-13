// Package repository — YouTube daily quota gate.
//
// youtube_quota_daily (migrations 059 + 124) is the per-day counter the
// YouTubeQuotaManager checks BEFORE every YouTube Data API v3 call. It
// mirrors the Google 2026 quota model (effective 2026-06-01): three
// INDEPENDENT daily buckets, each keyed by (date, bucket):
//
//	video_uploads → videos.insert                 (default 100 calls/day)
//	searches      → search.list                   (default 100 calls/day)
//	general       → videos.update, videos.list,
//	                thumbnails.set, channels.list (default 10000 units/day)
//
// DAY BOUNDARY: Google resets every quota bucket at midnight Pacific
// Time (America/Los_Angeles), NOT midnight UTC. The date key and the
// retry-after window both derive from YouTubeQuotaDay (the canonical
// day function) so the whole system agrees on when a quota day starts
// and ends.
//
// The pattern mirrors internal/repository/rate_limit_repo.go
// (INSERT...ON CONFLICT + FOR UPDATE on the daily row) but is sized
// for quota buckets (1 call == 1 bucket unit in the Google 2026 model)
// and paginates its counter through a single row keyed by (date, bucket).
//
// Concurrency: two pods racing on minute 23:59:59 of day N must NOT
// both succeed past the limit. We serialize via SELECT … FOR UPDATE on
// the daily row, then commit (+ the gate decision). If the row is
// missing for today, the upsert synthesizes it with limit=defaultLimit
// before the SELECT locks it.
package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "time/tzdata" // embed the IANA timezone DB so America/Los_Angeles resolves even in minimal containers
)

// YouTube quota bucket identifiers (Google 2026 quota model). These are
// the storage-level keys in youtube_quota_daily.bucket; the service
// layer re-exports them for callers.
const (
	YouTubeQuotaBucketVideoUploads = "video_uploads"
	YouTubeQuotaBucketSearches     = "searches"
	YouTubeQuotaBucketGeneral      = "general"
)

// youtubeQuotaLocation is America/Los_Angeles — the timezone Google
// uses for YouTube Data API v3 daily quota resets (midnight Pacific
// Time). Loaded once at package init; the embedded IANA database
// (time/tzdata) guarantees resolution even in minimal containers.
var youtubeQuotaLocation = func() *time.Location {
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		// Cannot happen with the embedded tzdata; fail fast if a
		// misbuilt binary drops it.
		panic(fmt.Sprintf("youtube quota: load America/Los_Angeles: %v", err))
	}
	return loc
}()

// YouTubeQuotaDay is the CANONICAL quota-day boundary: midnight Pacific
// Time (America/Los_Angeles) on the calendar day containing `now`.
// Google resets every daily quota bucket at midnight PT — NOT midnight
// UTC — so the youtube_quota_daily date key and the retry-after window
// must BOTH derive from this single function. Every quota-day
// computation in the system goes through here; never compute the day
// with time.Now().UTC().Truncate(24h) again.
//
// The returned time carries the America/Los_Angeles location at
// 00:00:00. Format it with .Format("2006-01-02") to get the Pacific
// calendar date for the DATE column — that string is independent of
// the Postgres session timezone.
func YouTubeQuotaDay(now time.Time) time.Time {
	inLA := now.In(youtubeQuotaLocation)
	return time.Date(inLA.Year(), inLA.Month(), inLA.Day(), 0, 0, 0, 0, youtubeQuotaLocation)
}

// YouTubeDailyQuotaRepository is the Postgres-backed implementation of
// the daily quota gate used by the YouTubeQuotaManager.
type YouTubeDailyQuotaRepository struct {
	db *sql.DB
}

// NewYouTubeDailyQuotaRepository constructs a repository handle against
// the live *sql.DB. Caller owns the DB; the repo does not Close it.
func NewYouTubeDailyQuotaRepository(db *sql.DB) *YouTubeDailyQuotaRepository {
	return &YouTubeDailyQuotaRepository{db: db}
}

// quotaDayKey returns the Pacific calendar date string (YYYY-MM-DD) for
// the quota day containing `now`. The date column is keyed on the
// Pacific day, matching Google's midnight-PT reset.
func quotaDayKey(now time.Time) string {
	return YouTubeQuotaDay(now).Format("2006-01-02")
}

// ReserveQuota atomically:
//
//  1. Upserts today's (date, bucket) row at limit=defaultLimit.
//  2. Locks the row via SELECT … FOR UPDATE.
//  3. If calls+cost > limit, returns (false, retryAfterSeconds, nil)
//     with retryAfterSeconds == seconds until the next Pacific midnight
//     (the bucket window's natural reset boundary).
//  4. Else, increments calls by cost, commits, returns (true, 0, nil).
//
// Concurrency: the FOR UPDATE serializes concurrent reservations
// across pods, so the limit is enforced strictly. defaultLimit is the
// bucket ceiling supplied by the YouTubeQuotaManager (the config knob
// in internal/config/config.go). We honor an inbound bump
// (defaultLimit > stored limit) so an operator can grow the ceiling
// mid-day; we do NOT shrink a stored ceiling that is already larger
// (so an operator's deliberate constraint isn't silently relaxed by
// a config typo).
func (r *YouTubeDailyQuotaRepository) ReserveQuota(ctx context.Context, bucket string, cost, defaultLimit int) (allowed bool, retryAfterSeconds int, err error) {
	if r == nil || r.db == nil {
		return false, 0, errors.New("youtube quota: nil repo or db")
	}
	if bucket == "" {
		return false, 0, errors.New("youtube quota: bucket must not be empty")
	}
	if cost < 1 {
		return false, 0, fmt.Errorf("youtube quota: cost=%d must be >= 1", cost)
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

	now := time.Now()
	today := quotaDayKey(now)

	// (1) Upsert today's row if it doesn't yet exist. The ON CONFLICT
	// DO NOTHING branch preserves any prior metadata so a partial day
	// already populated by RecordError isn't clobbered.
	if _, err = tx.ExecContext(ctx, `
		INSERT INTO youtube_quota_daily (date, bucket, calls, errors, "limit", last_reset_at)
		VALUES ($1, $2, 0, 0, $3, NOW())
		ON CONFLICT (date, bucket) DO NOTHING
	`, today, bucket, defaultLimit); err != nil {
		return false, 0, fmt.Errorf("youtube quota: upsert daily row: %w", err)
	}

	// (2) Acquire row-level lock + read the stored limit.
	var callsStored, limitStored int
	if err = tx.QueryRowContext(ctx, `
		SELECT calls, "limit"
		FROM youtube_quota_daily
		WHERE date = $1 AND bucket = $2
		FOR UPDATE
	`, today, bucket).Scan(&callsStored, &limitStored); err != nil {
		return false, 0, fmt.Errorf("youtube quota: lock + read: %w", err)
	}

	// (3) Honor inbound limit bumps; never silently shrink a
	// deliberately-larger stored limit.
	effectiveLimit := limitStored
	if defaultLimit > limitStored {
		if _, err = tx.ExecContext(ctx, `
			UPDATE youtube_quota_daily SET "limit" = $1 WHERE date = $2 AND bucket = $3
		`, defaultLimit, today, bucket); err != nil {
			return false, 0, fmt.Errorf("youtube quota: update limit: %w", err)
		}
		effectiveLimit = defaultLimit
	}

	if callsStored+cost > effectiveLimit {
		// Compute retryAfterSeconds as the wall-clock gap to the next
		// Pacific midnight. Advancing the quota day with AddDate (one
		// CALENDAR day in America/Los_Angeles) instead of adding 24
		// absolute hours keeps the boundary exact across DST: a spring-
		// forward Pacific day is 23h, a fall-back day is 25h, and
		// today+24h would land on the wrong side of the boundary.
		nextMidnight := YouTubeQuotaDay(now).AddDate(0, 0, 1)
		gap := nextMidnight.Sub(now)
		if gap < 0 {
			gap = 0
		}
		return false, int(gap.Seconds()), nil
	}

	// (4) Increment AND commit together. The commit releases the
	// FOR UPDATE lock so the next pod can proceed.
	if _, err = tx.ExecContext(ctx, `
		UPDATE youtube_quota_daily SET calls = calls + $1 WHERE date = $2 AND bucket = $3
	`, cost, today, bucket); err != nil {
		return false, 0, fmt.Errorf("youtube quota: increment calls: %w", err)
	}

	if err = tx.Commit(); err != nil {
		return false, 0, fmt.Errorf("youtube quota: commit: %w", err)
	}
	committed = true
	return true, 0, nil
}

// RecordError bumps the errors counter for the current Pacific quota
// day in the given bucket. Called by the YouTubeQuotaManager when an
// actual API call returns 5xx, hits a transport error, or fails
// validation. Distinct from ReserveQuota's quota_exceeded path:
// RecordError is "we tried, Google said no", not "we decided not to
// try". The errors column is informational — it does NOT block
// scheduling, since quota vs. error are orthogonal failure modes.
//
// The function synthesizes the daily row on first-call-of-day so the
// errors counter does not silently drop the very first failure.
// defaultLimit seeds a freshly-created row (the manager passes its
// configured bucket ceiling).
func (r *YouTubeDailyQuotaRepository) RecordError(ctx context.Context, bucket string, defaultLimit int) error {
	if r == nil || r.db == nil {
		return errors.New("youtube quota: nil repo or db")
	}
	if bucket == "" {
		return errors.New("youtube quota: bucket must not be empty")
	}
	today := quotaDayKey(time.Now())
	if _, err := r.db.ExecContext(ctx, `
		INSERT INTO youtube_quota_daily (date, bucket, calls, errors, "limit", last_reset_at)
		VALUES ($1, $2, 0, 1, $3, NOW())
		ON CONFLICT (date, bucket) DO UPDATE SET errors = youtube_quota_daily.errors + 1
	`, today, bucket, defaultLimit); err != nil {
		return fmt.Errorf("youtube quota: record error: %w", err)
	}
	return nil
}

// GetSnapshot returns the current Pacific quota day's row's (calls,
// errors, limit, last_reset_at) for a bucket as an externally-readable
// snapshot. Used by the /admin/health surface and by operators — both
// read-only and do NOT touch the row.
//
// Naming: the second return is `errCount` (NOT `errors`) because a
// named return value of type `int` named `errors` would SHADOW the
// imported `errors` package inside this function's scope — every
// `errors.New(...)` / `errors.Is(...)` call would fail to compile
// (the int return has no New/Is method). Renaming to `errCount`
// keeps the package accessible. The DB column name on the
// SELECT remains the literal `errors` — Scan binds via address so
// `&errCount` is what postgres populates with the column value.
func (r *YouTubeDailyQuotaRepository) GetSnapshot(ctx context.Context, bucket string) (calls, errCount, limit int, lastResetAt time.Time, err error) {
	if r == nil || r.db == nil {
		return 0, 0, 0, time.Time{}, errors.New("youtube quota: nil repo or db")
	}
	if bucket == "" {
		return 0, 0, 0, time.Time{}, errors.New("youtube quota: bucket must not be empty")
	}
	today := quotaDayKey(time.Now())
	if err := r.db.QueryRowContext(ctx, `
		SELECT calls, errors, "limit", last_reset_at
		FROM youtube_quota_daily
		WHERE date = $1 AND bucket = $2
	`, today, bucket).Scan(&calls, &errCount, &limit, &lastResetAt); err != nil {
		// DO NOT rename `errCount` back to `errors` — the int named
		// return above would shadow the `errors` package, the call
		// below would fail to compile (type int has no method Is),
		// and the same shadowing propagates to any other errors.New /
		// errors.Is / errors.As call in this function's scope.
		if errors.Is(err, sql.ErrNoRows) {
			return 0, 0, 0, time.Time{}, nil // no row yet today — zero snapshot is honest
		}
		return 0, 0, 0, time.Time{}, fmt.Errorf("youtube quota: get snapshot: %w", err)
	}
	return calls, errCount, limit, lastResetAt, nil
}
