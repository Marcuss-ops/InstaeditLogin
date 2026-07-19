-- 045_upload_jobs_worker_pool_enum.sql
-- P1 — worker pool scaffolding (ClaimBatch) + bounded retry.
--
-- Extends the upload_job_status enum with the four new states the
-- worker pool needs:
--
--   leased        — claimed by a worker via ClaimBatch; lease_owner +
--                   lease_expires_at + heartbeat_at are set; the row is
--                   off-limits to other workers until the lease is
--                   renewed (Heartbeat) or expires (ReclaimExpiredLeases).
--   retry_wait    — transient failure; next_attempt_at in the future;
--                   visible to ClaimBatch once next_attempt_at <= NOW().
--   dead_letter   — terminal: attempt_count reached max_attempts and the
--                   last Mark* call routed the row here instead of back
--                   to retry_wait. Operator triage.
--   cancelled     — user requested cancel while the row was still
--                   eligible (status IN ('pending','retry_wait')); never
--                   reached the worker.
--
-- Why this file is separate from 046_upload_jobs_worker_pool.sql:
-- PostgreSQL forbids ALTER TYPE ADD VALUE inside the same (implicit)
-- transaction as ALTER TABLE on the dependent table — error 55P04.
-- The migration runner executes every .sql file via a single db.Exec
-- call that forms one implicit transaction, so the enum work must
-- live in its own file (sibling 046 does the column work in a fresh
-- tx). Same pattern as 035_add_dlq_enum.sql + 035_worker_hardening.sql.
--
-- Idempotency: each ADD VALUE is guarded by a pg_enum lookup so a DB
-- that already has the value (re-applied migration, partial recovery
-- from a crashed run) skips the statement silently.

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_enum
        WHERE enumtypid = (SELECT oid FROM pg_type WHERE typname = 'upload_job_status')
          AND enumlabel = 'leased'
    ) THEN
        ALTER TYPE upload_job_status ADD VALUE 'leased';
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_enum
        WHERE enumtypid = (SELECT oid FROM pg_type WHERE typname = 'upload_job_status')
          AND enumlabel = 'retry_wait'
    ) THEN
        ALTER TYPE upload_job_status ADD VALUE 'retry_wait';
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_enum
        WHERE enumtypid = (SELECT oid FROM pg_type WHERE typname = 'upload_job_status')
          AND enumlabel = 'dead_letter'
    ) THEN
        ALTER TYPE upload_job_status ADD VALUE 'dead_letter';
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_enum
        WHERE enumtypid = (SELECT oid FROM pg_type WHERE typname = 'upload_job_status')
          AND enumlabel = 'cancelled'
    ) THEN
        ALTER TYPE upload_job_status ADD VALUE 'cancelled';
    END IF;
END
$$;
