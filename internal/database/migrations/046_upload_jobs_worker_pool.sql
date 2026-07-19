-- 046_upload_jobs_worker_pool.sql
-- P1 — companion to 045_upload_jobs_worker_pool_enum.sql.
--
-- Adds the 12 columns required by the upload-jobs worker pool:
--
--   attempt_count      INT NOT NULL DEFAULT 0
--     Incremented inside ClaimBatch (every successful claim).
--     Bounded by max_attempts before MarkDeadLetter.
--   max_attempts       INT NOT NULL DEFAULT 8
--     Default 8 = ~5x the historical 1-attempt behaviour with a
--     sensible ceiling for transient provider-side errors. The worker
--     reads this column to decide retry vs dead-letter.
--   next_attempt_at    TIMESTAMPTZ (nullable)
--     Stamped by MarkRetry. ClaimBatch's WHERE clause skips a
--     retry_wait row while next_attempt_at > NOW() — once the backoff
--     window elapses the row re-enters the claim pool automatically.
--   lease_owner        TEXT (nullable)
--     Worker ID holding the lease. Set by ClaimBatch, cleared by
--     Mark* / ReclaimExpiredLeases / release-on-shutdown. Doubles as
--     the CAS key for Mark* updates so a late delivery from a worker
--     whose lease expired cannot overwrite a peer's updates.
--   lease_expires_at   TIMESTAMPTZ (nullable)
--     Stamped to NOW() + leaseTTL at ClaimBatch, refreshed by every
--     Heartbeat. ReclaimExpiredLeases scans for (status='leased'
--     AND lease_expires_at < NOW()) to recover work from crashed
--     workers.
--   heartbeat_at       TIMESTAMPTZ (nullable)
--     Last Heartbeat time. Operators inspect this to spot a stuck
--     worker (lease_expires_at far in the future but heartbeat_at
--     long stale means the heartbeat goroutine itself died).
--   progress_bytes     BIGINT NOT NULL DEFAULT 0
--     Bytes streamed so far. Updated by Heartbeat or a dedicated
--     progress callback; used by the dashboard for resumable-upload
--     tracking (P1#5 will wire the actual byte counter).
--   total_bytes        BIGINT (nullable)
--     Total expected bytes for the source file. The worker stamps
--     this once it learns the size from the Drive Content-Length /
--     resumble-upload session URI metadata.
--   error_code         TEXT (nullable)
--     Stable provider/service code (e.g. youtube_quotaExceeded,
--     drive_404, s3_5xx) for retry classification + dashboard
--     filtering. Distinguishes retryable from terminal failures.
--   priority           INT NOT NULL DEFAULT 100
--     Default 100; lower = higher priority. Drives ClaimBatch's
--     ORDER BY priority ASC, created_at ASC.
--   started_at         TIMESTAMPTZ (nullable)
--     First-claim time. COALESCE-preserved across retry_wait →
--     leased transitions so SLA reporting reflects total time-in-
--     flight, not just the current attempt.
--   completed_at       TIMESTAMPTZ (nullable)
--     Time the row reached a terminal state (completed / failed /
--     dead_letter / cancelled).
--
-- All columns are nullable OR have server-side DEFAULTs so legacy
-- INSERTs (migration 036's Create + CreateIfSourceAbsent shape with
-- only the original 11 columns in the VALUES list) continue to work
-- without any Go-side change — the new columns take their DEFAULTs
-- automatically.

ALTER TABLE upload_jobs
    ADD COLUMN IF NOT EXISTS attempt_count    INTEGER     NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS max_attempts     INTEGER     NOT NULL DEFAULT 8,
    ADD COLUMN IF NOT EXISTS next_attempt_at  TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS lease_owner      TEXT,
    ADD COLUMN IF NOT EXISTS lease_expires_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS heartbeat_at     TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS progress_bytes   BIGINT      NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS total_bytes      BIGINT,
    ADD COLUMN IF NOT EXISTS error_code       TEXT,
    ADD COLUMN IF NOT EXISTS priority         INTEGER     NOT NULL DEFAULT 100,
    ADD COLUMN IF NOT EXISTS started_at       TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS completed_at     TIMESTAMPTZ;

-- (1) ClaimBatch primary index.
--     WHERE status IN ('pending','retry_wait')
--     ORDER BY priority ASC, created_at ASC
-- A partial predicate so historical leased/completed/failed/dead_letter/
-- cancelled rows never enter the candidate set — the index stays small
-- even when the upload_jobs table accumulates tens of thousands of rows.
CREATE INDEX IF NOT EXISTS idx_upload_jobs_claim
    ON upload_jobs (priority ASC, created_at ASC)
    WHERE status IN ('pending', 'retry_wait');

-- (2) Lease reaper index. The reaper goroutine runs the SQL:
--     SELECT id FROM upload_jobs
--     WHERE status='leased' AND lease_expires_at < NOW()
--     ORDER BY lease_expires_at ASC LIMIT $1
--     FOR UPDATE SKIP LOCKED
-- Partial predicate keeps the index ~ lease-active-row count, not
-- total row count.
CREATE INDEX IF NOT EXISTS idx_upload_jobs_lease_recovery
    ON upload_jobs (lease_expires_at)
    WHERE status = 'leased';

-- Pre-upgrade sweep: any rows stuck in the OLD single-process
-- worker's terminal 'processing' state (the worker died mid-upload
-- before this migration ran) are released back to 'pending' so the
-- new ClaimBatch can claim them. attempt_count / max_attempts keep
-- their DEFAULTs so the new retry budget applies from the first
-- re-claim; a row that crashes three times across the upgrade will
-- then be subject to the new lease + retry semantics like any other
-- fresh row.
UPDATE upload_jobs
SET status           = 'pending',
    lease_owner      = NULL,
    lease_expires_at = NULL,
    heartbeat_at     = NULL,
    started_at       = NULL,
    updated_at       = NOW()
WHERE status = 'processing';

COMMENT ON COLUMN upload_jobs.attempt_count   IS 'Incremented at every ClaimBatch; bounded by max_attempts before MarkDeadLetter.';
COMMENT ON COLUMN upload_jobs.max_attempts    IS 'Maximum ClaimBatch attempts before MarkDeadLetter. Default 8.';
COMMENT ON COLUMN upload_jobs.next_attempt_at IS 'When retry_wait status transitions back to pending-eligible for ClaimBatch.';
COMMENT ON COLUMN upload_jobs.lease_owner     IS 'Worker ID that holds the lease; cleared on Mark* or recovery. Doubles as CAS key for Mark* updates.';
COMMENT ON COLUMN upload_jobs.lease_expires_at IS 'NOW() + leaseTTL stamped at ClaimBatch and renewed by every Heartbeat. Lease-reaper scans for lease_expires_at < NOW().';
COMMENT ON COLUMN upload_jobs.heartbeat_at    IS 'Last time the claiming worker renewed lease_expires_at via Heartbeat().';
COMMENT ON COLUMN upload_jobs.progress_bytes  IS 'Bytes streamed so far (progress tracking for resumable uploads). Default 0.';
COMMENT ON COLUMN upload_jobs.total_bytes     IS 'Total expected bytes (set when ClaimBatch transitions to leased for size tracking).';
COMMENT ON COLUMN upload_jobs.error_code      IS 'Stable provider/service code (e.g. youtube_quotaExceeded, drive_404) for retry classification + dashboard filter.';
COMMENT ON COLUMN upload_jobs.priority        IS '100 = default; lower = higher priority (drives ClaimBatch ORDER BY).';
COMMENT ON COLUMN upload_jobs.started_at      IS 'When this row was first claimed (preserved across retry_wait → leased transitions).';
COMMENT ON COLUMN upload_jobs.completed_at    IS 'When this row reached a terminal state (completed / failed / dead_letter / cancelled).';
