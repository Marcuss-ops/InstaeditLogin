-- =============================================================================
-- 057_external_deliveries_queue.sql — durable Velox downloader queue
-- =============================================================================
-- Replaces the in-process API→worker channel with a database-backed queue.
-- Adds lease/attempt/retry columns to external_deliveries and a partial index
-- for the atomic FOR UPDATE SKIP LOCKED claim used by the worker.
-- =============================================================================

ALTER TABLE external_deliveries
    ADD COLUMN IF NOT EXISTS lease_expires_at   TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS attempt_count      INT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS next_attempt_at    TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS leased_by_worker_id TEXT,
    ADD COLUMN IF NOT EXISTS max_attempts        INT NOT NULL DEFAULT 5;

-- Claim index: 'accepted' rows only. The time predicates (lease_expired,
-- next_attempt_at window) cannot use NOW() in a partial-index predicate
-- because NOW() is STABLE, not IMMUTABLE. The claim query applies the
-- time filters explicitly and still benefits from this partial index.
CREATE INDEX IF NOT EXISTS idx_external_deliveries_claim
    ON external_deliveries (created_at ASC)
    WHERE status = 'accepted';
