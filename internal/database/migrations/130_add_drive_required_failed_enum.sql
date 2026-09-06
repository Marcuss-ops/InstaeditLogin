-- 130_add_drive_required_failed_enum.sql
-- Task 8/10.1 — adds 'drive_required_failed' to the post_status enum.
--
-- Splits into its own file per the migration 035 pattern: Postgres
-- forbids ALTER TYPE ADD VALUE in the same transaction as other DDL
-- touching tables that use the type (error 55P04). The migration runner
-- (internal/database/migrations.go) runs each .sql file via a single
-- db.Exec call, which becomes an implicit transaction — so the
-- ADD VALUE must be the only thing in the tx, hence the split.
--
-- Lifecycle after this migration:
--
--   draft → queued → publishing → published
--                              → partially_published
--                              → waiting_provider
--                              → retrying ───→ (after next_attempt_at) ─→ publishing
--                              → failed
--                              → dlq                    (SPRINT 5.2 — terminal)
--                              → blocked_auth           (Task 2/10 — terminal per account)
--                              → drive_required_failed  (Task 8/10.1 — terminal policy violation)
--
-- 'drive_required_failed' is terminal: the platform publish COMPLETED
-- but the operator opted into drive_required=true on the destination
-- and the required Drive upload terminally failed. The row must not
-- read as a clean 'published' — the Drive copy of the artifact is
-- missing. The ListPending filter (status IN ('queued',
-- 'waiting_provider')) and the ListPublishing filter
-- (status='publishing') already exclude it naturally, so no driver
-- changes are required to stop re-picking these rows. Operator
-- queries can join on status='drive_required_failed' for triage; the
-- publish_drive_required_violations_total metric increments on every
-- transition into this state.

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_enum
        WHERE enumtypid = (SELECT oid FROM pg_type WHERE typname = 'post_status')
          AND enumlabel = 'drive_required_failed'
    ) THEN
        ALTER TYPE post_status ADD VALUE 'drive_required_failed';
    END IF;
END $$;
