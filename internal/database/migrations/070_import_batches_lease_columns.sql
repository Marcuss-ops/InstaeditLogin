-- 070_import_batches_lease_columns.sql
-- Fixes missing lease columns on import_batches table.
--
-- Migration 050 created import_batches without lease_owner,
-- lease_expires_at, and heartbeat_at columns. The Go code
-- (import_batch_repo.go) uses these for crawler worker pool
-- semantics (ClaimNextBatch, Heartbeat, ReclaimExpiredBatches).
--
-- All columns are nullable so existing rows (if any) are unaffected.

ALTER TABLE import_batches
    ADD COLUMN IF NOT EXISTS lease_owner      TEXT,
    ADD COLUMN IF NOT EXISTS lease_expires_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS heartbeat_at     TIMESTAMPTZ;

-- Index for the lease reaper (ReclaimExpiredBatches):
--   WHERE status = 'processing' AND lease_expires_at < NOW()
CREATE INDEX IF NOT EXISTS idx_import_batches_lease_recovery
    ON import_batches (lease_expires_at)
    WHERE status = 'processing';

COMMENT ON COLUMN import_batches.lease_owner      IS 'Worker ID holding the lease. Set by ClaimNextBatch, cleared by Mark* or ReclaimExpiredBatches.';
COMMENT ON COLUMN import_batches.lease_expires_at IS 'NOW() + leaseTTL stamped at ClaimNextBatch and renewed by Heartbeat. Reaper scans for lease_expires_at < NOW().';
COMMENT ON COLUMN import_batches.heartbeat_at     IS 'Last Heartbeat time. Operators inspect this to spot a stuck crawler.';
