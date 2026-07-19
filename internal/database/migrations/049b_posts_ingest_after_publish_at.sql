-- 049b_posts_ingest_after_publish_at.sql
-- P1#4 — split posts.scheduled_at into posts.ingest_after +
-- posts.publish_at. See 049a for the design overview + the reason
-- the file is split out from 049c (Postgres 55P04 ALTER TYPE +
-- ALTER TABLE parallel constraint).
--
-- What this file does:
--   * Adds posts.ingest_after TIMESTAMPTZ NOT NULL DEFAULT NOW().
--       The earliest time the ingest worker (or a future caller of
--       UploadWorker) is permitted to fetch + copy the asset. In the
--       current architecture the ingest runs as part of
--       upload_jobs.processIngestJob — by definition that runs
--       before any post_targets exist (the post is created inside
--       processPublishJob AFTER ingest completes). So
--       posts.ingest_after is set to NOW() at post-creation time
--       (server-side DEFAULT NOW() lands it on create), and the
--       publish_worker's ListPending SELECT gating
--       (`publish_at IS NULL OR publish_at <= NOW()`) is the only
--       operative time gate. Kept on posts for future migrations
--       that ingest asynchronously-IMPORTED posts.
--   * Adds posts.publish_at TIMESTAMPTZ (nullable). The user-facing
--       "what time should this go live" time. NULL = publish
--       immediately (existing single-file behaviour, same
--       semantics as `scheduled_at IS NULL` today).
--   * Backfills publish_at from the OLD scheduled_at column for
--       rows that had a scheduled value (preserves the original
--       operator-set schedule; ingest_after lands at NOW() — i.e.
--       ingest happens NOW, regardless of how far in the future
--       publish_at is, which is the WHOLE POINT of the split).
--   * DROPs posts.scheduled_at.
--   * Re-creates idx_posts_workspace_scheduled as
--       idx_posts_workspace_publish_at (publish_worker ListPending
--       filter uses no index today, the JOIN relies on
--       post_targets(post_id, status); a worker-scoped query plan
--       shows the planner uses a hash-join, but the user-facing
--       scheduler UI surfaces posts ordered by publish_at, so an
--       index helps there too).

ALTER TABLE posts
    ADD COLUMN IF NOT EXISTS ingest_after TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ADD COLUMN IF NOT EXISTS publish_at   TIMESTAMPTZ;

-- Backfill: any row with a scheduled_at gets a publish_at equal to
-- the scheduled time (timezone preserved). ingest_after keeps its
-- DEFAULT NOW() — i.e. ingest runs the moment this migration
-- applies. This matches the user's "ingest happens well in advance"
-- intent even for the existing scheduled backlog.
UPDATE posts
SET publish_at = scheduled_at
WHERE scheduled_at IS NOT NULL
  AND publish_at IS NULL;

DROP INDEX IF EXISTS idx_posts_workspace_scheduled;

CREATE INDEX IF NOT EXISTS idx_posts_workspace_publish_at
    ON posts(workspace_id, publish_at);

ALTER TABLE posts
    DROP COLUMN IF EXISTS scheduled_at;

COMMENT ON COLUMN posts.ingest_after IS 'P1#4 — earliest time the asset ingest (Drive → S3) may run. Server-side DEFAULT NOW() lands it on insert; first publish_worker tick after this time is eligible to start ingest. For pre-P1#4 rows backfilled via 049b, ingest_after = NOW() (ingest runs at migration apply time).';
COMMENT ON COLUMN posts.publish_at   IS 'P1#4 — user-facing "what time should this post go live". NULL = publish immediately. Replaces the old posts.scheduled_at column (migration 003).';
