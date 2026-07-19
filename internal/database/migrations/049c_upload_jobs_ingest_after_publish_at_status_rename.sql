-- 049c_upload_jobs_ingest_after_publish_at_status_rename.sql
-- P1#4 — companion to 049a (enum extension) + 049b (posts column
-- split). Sibling file because PostgreSQL forbids ALTER TYPE
-- ADD VALUE inside the same implicit tx as ALTER TABLE (error
-- 55P04); this file does the column work in a fresh implicit tx
-- after 049a's enum ADD VALUE has already committed.
--
-- What this file does:
--   * Adds upload_jobs.ingest_after TIMESTAMPTZ NOT NULL DEFAULT
--       NOW(). Server-side DEFAULT NOW() means an Insert without a
--       publish_at lands at NOW() — by the time the row reaches the
--       ClaimBatch CTE, (ingest_after <= NOW()) is satisfied and
--       the ingest pool claims it. The user-supplied scheduled
--       publishing window no longer blocks ingest — the row's
--       asset is staged in S3 ASAP, the upload_to_provider hop
--       happens at publish_at.
--   * Adds upload_jobs.publish_at TIMESTAMPTZ (nullable). The
--       time the upload_worker processPublishJob stamps onto the
--       created post before calling PublishPost. Mirrors
--       posts.publish_at exactly.
--   * Backfills publish_at = scheduled_at for rows that had a
--       scheduled value. ingest_after keeps its DEFAULT NOW() —
--       i.e. ingest for old scheduled rows starts at migration
--       apply time, AS DESIGNED. ingest_after is also explicitly
--       written to NOW() for any rows where the DEFAULT didn't fire
--       (legacy INSERTs pre-P1#2 worker pool migration may have
--       NULLed it; defensive belt-and-braces).
--   * UPDATEs the upload_jobs rows that were already in the
--       terminal-like 'ready_to_publish' state to
--       'ingest_completed' (the new canonical name), and rows
--       already in 'completed' to 'publish_completed'. Per the
--       049a header, the old values stay on the enum (PG < 18
--       can't DROP VALUE); they're never written by code paths
--       after this commit.
--   * DROPs upload_jobs.scheduled_at.
--   * Re-creates idx_upload_jobs_scheduled as
--       idx_upload_jobs_publish_at — same partial-index shape but
--       on the new column. The old index is dropped with the
--       column. NOT NULL on publish_at is not required here
--       (single-file legacy imports keep publish_at=NULL forever).

ALTER TABLE upload_jobs
    ADD COLUMN IF NOT EXISTS ingest_after TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ADD COLUMN IF NOT EXISTS publish_at   TIMESTAMPTZ;

-- Backfill publish_at from the old scheduled_at for scheduled rows.
-- ingest_after: rely on the DEFAULT NOW() for fresh rows; for legacy
-- rows that may have been inserted with ingest_after explicitly
-- NULL (impossible today because the column is NOT NULL DEFAULT
-- NOW(), but write-defensive in case a dump-restore path stripped
-- defaults) coerce to NOW(). After this migration the column is
-- NOT NULL so future INSERTs are guaranteed non-null.
UPDATE upload_jobs
SET publish_at   = COALESCE(scheduled_at, publish_at),
    ingest_after = COALESCE(ingest_after, NOW())
WHERE publish_at IS NULL OR ingest_after IS NULL;

-- Status rename: 'ready_to_publish' → 'ingest_completed',
-- 'completed' → 'publish_completed'. Both carry forward — old
-- values remain on the enum (049a header) because PostgreSQL < 18
-- cannot DROP VALUE. New Go writes (commit's repo changes) only
-- use the new names.
UPDATE upload_jobs SET status = 'ingest_completed' WHERE status = 'ready_to_publish';
UPDATE upload_jobs SET status = 'publish_completed' WHERE status = 'completed';

DROP INDEX IF EXISTS idx_upload_jobs_scheduled;

-- The new index serves the publish pool's ClaimBatchForPublish
-- CTE WHERE clause:
--   status = 'ingest_completed'
--   AND (publish_at IS NULL OR publish_at <= NOW())
--   ORDER BY priority ASC, created_at ASC
-- The partial predicate keeps the index ~ candidate-row count
-- (~ claim-ready rows), not total row count.
CREATE INDEX IF NOT EXISTS idx_upload_jobs_publish_at
    ON upload_jobs (priority ASC, created_at ASC)
    WHERE status = 'ingest_completed';

ALTER TABLE upload_jobs
    DROP COLUMN IF EXISTS scheduled_at;

COMMENT ON COLUMN upload_jobs.ingest_after IS 'P1#4 — earliest time the ingest pool may claim this row. Server-side DEFAULT NOW() lands it on insert; ClaimBatch CTE adds `AND (ingest_after IS NULL OR ingest_after <= NOW())` so the ingest pool never blocks on the user-supplied publish_at. Replaces upload_jobs.scheduled_at (migration 037).';
COMMENT ON COLUMN upload_jobs.publish_at   IS 'P1#4 — the desired public publish time. Mirrors posts.publish_at 1:1 — the upload_worker stamps this onto the created post. NULL = publish immediately. ClaimBatchForPublish CTE adds `AND (publish_at IS NULL OR publish_at <= NOW())` so the publish pool waits for the schedule without holding a lease.';
