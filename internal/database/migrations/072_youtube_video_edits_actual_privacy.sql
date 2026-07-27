-- 072_youtube_video_edits_actual_privacy.sql
--
-- Adds the YouTube-side projection of the publish outcome so the
-- InstaeditLogin row carries a faithful read of what YouTube accepted,
-- not just what the operator requested.
--
-- Why a CHECK constraint instead of a Postgres ENUM:
--   The drift_reconciler + publish orchestrator both stamp
--   youtube_sync_status from application code; a CHECK constraint
--   lets us add a future value (e.g. 'reconciling', 'unknown') by
--   ALTER TABLE ... ADD CONSTRAINT, NOT by dropping + recreating the
--   ENUM type (which requires a separate migration that scans every
--   row). Treat the constraint as an exhaustive-but-amendable list.
--
-- Why a partial index on `pending`:
--   The drift_reconciler runs a periodic sweep that fetches every
--   row in `youtube_sync_status = 'pending'` to retry the YouTube
--   read-back. Without a partial index the sweep would scan the
--   whole table on every tick (the workspace row count grows with
--   every published video). The partial index keeps the sweep to
--   an index-only scan regardless of workspace footprint.

ALTER TABLE youtube_video_edits
  ADD COLUMN actual_privacy TEXT,
  ADD COLUMN youtube_sync_status TEXT
    CHECK (youtube_sync_status IN ('pending', 'confirmed', 'drift', 'failed'));

CREATE INDEX IF NOT EXISTS idx_youtube_video_edits_sync_pending
  ON youtube_video_edits (updated_at)
  WHERE youtube_sync_status = 'pending';
