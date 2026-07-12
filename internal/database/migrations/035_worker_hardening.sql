-- 035_worker_hardening.sql
-- SPRINT 5.2 (P1#10) — worker hardening for long uploads.
--
-- Adds the columns the PublishWorker needs to:
--   1. Hold a per-replica lease on a row mid-publish (leased_until +
--      lease_owner_id + heartbeat_at) so a crashed worker doesn't
--      leak the row forever.
--   2. Cap retries (max_attempts INT DEFAULT 5) so a transient
--      flapping platform doesn't loop the row indefinitely — after
--      5 failed attempts the row is marked status='dlq' for
--      operator triage.
--   3. Resume chunked uploads after a crash (upload_offset BIGINT) —
--      on the next pick, the worker passes the offset to the
--      platform's resume API so we don't re-upload bytes 0..N.
--   4. Respect rate limits (rate_limit_reset_at) — when the
--      platform returns 429 with Retry-After, the worker stamps
--      next_retry_at and rate_limit_reset_at to the platform's
--      hint, NOT a fixed backoff. attempt_count is NOT incremented
--      for rate-limit (it's not a fault, the platform told us when
--      to come back).
--
-- All columns are nullable (or have a DEFAULT) so existing rows
-- (written before this migration) get sane values and the rotation
-- worker (if any) can populate them lazily.
--
-- The enum value 'dlq' is added in a separate file
-- (035_add_dlq_enum.sql) per the Postgres 55P04 restriction.

ALTER TABLE post_targets
    ADD COLUMN IF NOT EXISTS leased_until      TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS lease_owner_id    TEXT,
    ADD COLUMN IF NOT EXISTS heartbeat_at      TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS max_attempts      INT NOT NULL DEFAULT 5,
    ADD COLUMN IF NOT EXISTS upload_offset     BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS rate_limit_reset_at TIMESTAMPTZ,
    -- completed_at (SPRINT 5.2) is the timestamp a row reached a
    -- TERMINAL state: published (via published_at) or dlq / failed
    -- (via this column). Operators query `WHERE status IN ('dlq',
    -- 'failed') AND completed_at > now() - interval '7 days'` for
    -- weekly triage reports. published_at remains the canonical
    -- success timestamp; completed_at is the catch-all for terminal
    -- non-success states.
    ADD COLUMN IF NOT EXISTS completed_at      TIMESTAMPTZ;

-- Indexes for the new worker loops.

-- (1) Heartbeat goroutine + ReclaimExpiredLeases scan on
--     (status, lease_owner_id, leased_until).
CREATE INDEX IF NOT EXISTS idx_post_targets_lease
    ON post_targets(lease_owner_id, leased_until)
    WHERE lease_owner_id IS NOT NULL;

-- (2) ListPending's new "next_retry_at <= NOW()" filter (the existing
--     ListPending only filters status; the rate-limit retry path
--     stamps next_retry_at to the platform's hint and we must NOT
--     pick the row up before then).
CREATE INDEX IF NOT EXISTS idx_post_targets_status_next_retry
    ON post_targets(status, next_retry_at)
    WHERE status IN ('queued', 'waiting_provider', 'retrying');
