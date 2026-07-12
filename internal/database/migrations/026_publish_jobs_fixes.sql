-- ============================================================================
-- 026_publish_jobs_fixes.sql
--
-- Taglio 5.x — second step in the publish_jobs series. Tightens
-- 025_publish_jobs.sql based on integration-review feedback:
--
--   (a) post_target_id FK should preserve publish_jobs history when the
--       parent post_target is deleted → switch from CASCADE to SET NULL.
--       The model docstring calls publish_jobs an append-only audit log
--       ("append-only retry / debugging history"); CASCADE on a parent
--       post_target silently nukes that audit trail. SET NULL preserves
--       the row + keeps the FK relationship for orphaned-history queries.
--
--   (b) Add `version BIGINT NOT NULL DEFAULT 1` optimistic-concurrency
--       column to match the UPDATE … SET status=…, version=version+1
--       pattern every other Taglio 5.x table (posts, post_targets,
--       outbox_events) carries. Without it, two worker replicas can
--       race on the status claim and one wins, the other 0-rows-affected.
--
-- All operations are idempotent: ADD COLUMN IF NOT EXISTS, DO-block
-- guard for constraint rename, ADD CONSTRAINT inside the guard. A
-- database where this migration has already been applied is a no-op.
-- ============================================================================

-- ---------------------------------------------------------------------------
-- (a) post_target_id FK: CASCADE → SET NULL on parent delete
-- ---------------------------------------------------------------------------

-- Drop NOT NULL first; SET NULL semantics requires an nullable column.
ALTER TABLE publish_jobs
    ALTER COLUMN post_target_id DROP NOT NULL;

-- PostgreSQL auto-named the FK created by 025 (typical: '<table>_<col>_fkey').
-- Look it up dynamically so we don't hardcode a brittle name.
DO $$
DECLARE
    constraint_name TEXT;
BEGIN
    SELECT conname INTO constraint_name
      FROM pg_constraint
     WHERE conrelid = 'publish_jobs'::regclass
       AND contype = 'f'
       AND pg_get_constraintdef(oid) ILIKE '%post_target_id%references%';
    IF constraint_name IS NOT NULL THEN
        EXECUTE format('ALTER TABLE publish_jobs DROP CONSTRAINT %I', constraint_name);
    END IF;
END $$;

-- Re-add with SET NULL semantics, naming the FK explicitly. Re-runs of
-- this migration are safe: the previous DO-block already dropped it.
ALTER TABLE publish_jobs
    ADD CONSTRAINT publish_jobs_post_target_id_fkey
    FOREIGN KEY (post_target_id) REFERENCES post_targets(id)
    ON DELETE SET NULL;

-- ---------------------------------------------------------------------------
-- (b) Add `version BIGINT NOT NULL DEFAULT 1` for optimistic concurrency
-- ---------------------------------------------------------------------------

ALTER TABLE publish_jobs
    ADD COLUMN IF NOT EXISTS version BIGINT NOT NULL DEFAULT 1;

-- A btree index on version isn't useful (not a SELECT predicate), but a
-- covering index for the worker's hot path — UPDATE publish_jobs
-- SET status=…, version=version+1 WHERE id=? AND version=? — is satisfied
-- by the existing PK lookup on id. No new index needed.

-- NOTES for the next commit (C3, internal/repository/post_repo.go):
-- The high-severity review item — `outbox_event_id` column on publish_jobs
-- has no matching Go field in models.PublishJob — gets addressed when we
-- extend models.PublishJob to include OutboxEventID *int64 `json:"outbox_
-- event_id,omitempty"` in the C3 commit. That commit is the natural place
-- to wire the same-transaction invariant (CreatePost writes posts + post_
-- targets + publish_jobs + outbox_events in one BEGIN/COMMIT). The hard
-- SQL column is correct on disk already; the Go surface just needs to
-- catch up.
