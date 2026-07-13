// Package repository — rate-limit counters.
//
// SPRINT 2.2 multi-tier rate limiting. Shared tiers (per-workspace,
// per-API-key) MUST live in Postgres so the budget is consistent
// across all API replicas. Per-IP and per-endpoint tiers stay
// in-memory (single-replica coarse backstop; the edge tier
// Cloudflare is the real per-IP gate — see docs/OPERATIONS.md).
//
// Algorithm: fixed window. The hot read path is a single
// `INSERT ... ON CONFLICT (scope, window_start) DO UPDATE SET
//
//	count = rate_limit_counters.count + 1
//
// RETURNING count`. Postgres handles the race atomically via the
// PRIMARY KEY (scope, window_start) constraint; no explicit row
// lock needed.
//
// The window is 60 seconds (the user spec'd all budgets in /min).
// `window_start` is stored as unix-seconds floored to the window
// boundary so a 60-second window starting at t=0 ends at t=60.
//
// Idempotency: a successful Increment always returns the new count.
// Callers map `count > limit` → 429. The window is implicit: rows
// older than 1 window are not returned by Increment; the cron
// sweeper (deferred follow-up) reaps them.
package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrRateLimitScopeEmpty is returned by Increment when the supplied
// scope is empty (programming error — every tier must have a
// non-empty scope string like "ws_post:42" or "apikey_read:7").
var ErrRateLimitScopeEmpty = errors.New("rate-limit scope is empty")

// RateLimitRepository is the Postgres-backed fixed-window counter
// store. The interface is local to the repository package — the
// service layer (internal/services/ratelimit.go) is the only
// caller. Tests inject a sql.DB or a tx-wrapped equivalent.
type RateLimitRepository struct {
	db *sql.DB
}

// NewRateLimitRepository constructs the repository.
func NewRateLimitRepository(db *sql.DB) *RateLimitRepository {
	return &RateLimitRepository{db: db}
}

// Increment atomically bumps the counter for (scope, window_start)
// by 1 and returns the new count plus the window's reset time.
//
// window is the duration of the time bucket (e.g. time.Minute).
// The window_start is computed as (now / window) * window — a
// fixed boundary that all replicas converge on without coordination.
// Clock skew between replicas is bounded by NTP; a skew of a few
// seconds shifts the window boundary by the same amount, which is
// acceptable for a soft "you're over budget" gate.
//
// Returns ErrRateLimitScopeEmpty if scope == "". The caller maps
// (count, limit) to the X-RateLimit-* response headers and the
// 429 + Retry-After response when count > limit.
func (r *RateLimitRepository) Increment(ctx context.Context, scope string, window time.Duration) (count int, resetAt time.Time, err error) {
	if scope == "" {
		return 0, time.Time{}, ErrRateLimitScopeEmpty
	}
	if window <= 0 {
		return 0, time.Time{}, fmt.Errorf("rate-limit window must be > 0 (got %v)", window)
	}
	now := time.Now()
	windowSeconds := int64(window / time.Second)
	if windowSeconds == 0 {
		windowSeconds = 1
	}
	windowStart := (now.Unix() / windowSeconds) * windowSeconds
	resetAt = time.Unix(windowStart+windowSeconds, 0).UTC()

	// Hot path. ON CONFLICT handles the race: if two replicas hit
	// the same (scope, window_start) at the same time, exactly one
	// INSERT wins, the other branches to UPDATE. RETURNING gives
	// the post-increment count.
	row := r.db.QueryRowContext(ctx,
		`INSERT INTO rate_limit_counters (scope, window_start, count)
		 VALUES ($1, $2, 1)
		 ON CONFLICT (scope, window_start)
		 DO UPDATE SET count = rate_limit_counters.count + 1
		 RETURNING count`,
		scope, windowStart,
	)
	if err := row.Scan(&count); err != nil {
		return 0, time.Time{}, fmt.Errorf("rate-limit increment: %w", err)
	}
	return count, resetAt, nil
}

// Get returns the current count for (scope, window_start) without
// incrementing. Used by tests to assert the counter state. The
// hot path uses Increment; production code should not call Get.
func (r *RateLimitRepository) Get(ctx context.Context, scope string, window time.Duration) (int, time.Time, error) {
	now := time.Now()
	windowSeconds := int64(window / time.Second)
	if windowSeconds == 0 {
		windowSeconds = 1
	}
	windowStart := (now.Unix() / windowSeconds) * windowSeconds
	resetAt := time.Unix(windowStart+windowSeconds, 0).UTC()
	var count int
	err := r.db.QueryRowContext(ctx,
		`SELECT count FROM rate_limit_counters
		 WHERE scope = $1 AND window_start = $2`,
		scope, windowStart,
	).Scan(&count)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, resetAt, nil
	}
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("rate-limit get: %w", err)
	}
	return count, resetAt, nil
}

// DeleteOlderThan removes all counter rows whose window_start is
// strictly less than the supplied cutoff. Used by the cron sweeper
// (deferred follow-up) to bound table growth. Returns the number
// of rows deleted. NOT on the hot path.
func (r *RateLimitRepository) DeleteOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM rate_limit_counters WHERE window_start < $1`,
		cutoff.Unix(),
	)
	if err != nil {
		return 0, fmt.Errorf("rate-limit delete older than: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("rate-limit delete older than: rows affected: %w", err)
	}
	return n, nil
}
