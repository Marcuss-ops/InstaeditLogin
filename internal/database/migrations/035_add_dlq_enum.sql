-- 035_add_dlq_enum.sql
-- SPRINT 5.2 (P1#10) — adds 'dlq' to the post_status enum.
--
-- Splits into its own file per the migration 018 pattern: Postgres
-- forbids ALTER TYPE ADD VALUE in the same transaction as ALTER TABLE
-- on tables using the type (error 55P04). The migration runner
-- (internal/database/migrations.go) runs each .sql file via a single
-- db.Exec call, which becomes an implicit transaction — so the
-- ADD VALUE must be the only thing in the tx, hence the split.
--
-- 035_worker_hardening.sql is the column additions and runs in its
-- own implicit tx after this one completes.
--
-- Lifecycle after this migration:
--
--   draft → queued → publishing → published
--                              → partially_published
--                              → waiting_provider
--                              → retrying ───→ (after next_attempt_at) ─→ publishing
--                              → failed
--                              → dlq         (SPRINT 5.2 — terminal-fail, max attempts exhausted)
--
-- 'dlq' is terminal: no further transitions. The ListPending filter
-- (status IN ('queued', 'waiting_provider')) already excludes it
-- naturally, so no driver changes are required to stop re-picking
-- DLQ'd rows. The ListPublishing filter (status='publishing') also
-- excludes it. Operator queries can join on status='dlq' for triage.

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_enum
        WHERE enumtypid = (SELECT oid FROM pg_type WHERE typname = 'post_status')
          AND enumlabel = 'dlq'
    ) THEN
        ALTER TYPE post_status ADD VALUE 'dlq';
    END IF;
END $$;
