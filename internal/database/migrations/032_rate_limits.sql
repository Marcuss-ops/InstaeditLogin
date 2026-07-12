-- =============================================================================
-- 032_rate_limits.sql — SPRINT 2.2: multi-tier rate limiting.
-- =============================================================================
-- Per-scope fixed-window counter table. One row per (scope, window_start).
-- Hot path: INSERT ... ON CONFLICT (scope, window_start) DO UPDATE SET
--   count = rate_limit_counters.count + 1
-- RETURNING count, window_start. The middleware translates count vs limit
-- into X-RateLimit-* headers + 429 + Retry-After.
--
-- UNLOGGED: the table is intentionally UNLOGGED so writes skip WAL
-- (the counter is transient — losing a few increments on a replica
-- crash is acceptable; we never depend on a precise count for
-- correctness, only for the soft "you're over budget" gate). This
-- gives ~2-3x write throughput on the hot read path.
--
-- The PRIMARY KEY (scope, window_start) is the only access pattern;
-- no other indexes are needed for the application. The cron sweeper
-- (deferred) uses window_start alone, so we add a btree index there.
--
-- Storage choice rationale: the user explicitly forbade in-memory
-- limiters for tiers that must be shared across >1 API replica
-- (per-workspace, per-API-key). Postgres is the stop-gap until a
-- Redis layer is introduced. Per-IP and per-endpoint (single-
-- replica coarse backstops) stay in-memory; the edge tier
-- (Cloudflare/reverse proxy) is the real per-IP gate and is
-- documented in docs/OPERATIONS.md.
--
-- Idempotent. All DDL uses IF NOT EXISTS guards.
-- =============================================================================

CREATE UNLOGGED TABLE IF NOT EXISTS rate_limit_counters (
    scope        TEXT   NOT NULL,
    window_start BIGINT NOT NULL,  -- unix seconds, floored to window boundary
    count        INT    NOT NULL DEFAULT 1,
    PRIMARY KEY (scope, window_start)
);

-- Hot path for the cron sweeper (deferred follow-up):
-- DELETE FROM rate_limit_counters WHERE window_start < $1.
CREATE INDEX IF NOT EXISTS idx_rate_limit_counters_window
    ON rate_limit_counters(window_start);
