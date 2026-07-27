-- 071_youtube_video_edits_partial_unique.sql
--
-- P0#3 "FindOrCreate session idempotency": enforce that for any
-- (workspace_id, platform_account_id, youtube_video_id) tuple,
-- there is AT MOST ONE row in a non-terminal state
-- ('editing' | 'failed' | 'publishing'). Two concurrent clicks on
-- the same video card converge on a single editor session + a
-- single Velox project: the second INSERT fails with SQLSTATE 23505,
-- the helper re-SELECTs and returns the winner's velox_project_id so
-- the SPA keeps the same Dark Editor URL across retries.
--
-- Why 'published' is EXCLUDED from the partial filter:
--   Once a video is published, the operator may legitimately want to
--   re-edit it in a fresh session (e.g. upload a new thumbnail). The
--   partial UNIQUE filter closes the door only for the "still in
--   flight" states; published rows are free to be superseded.
--
-- Why we do NOT backfill existing rows:
--   Historical duplicates (a buggy prior release generated multiple
--   sessions per video) are kept; the index creation will FAIL with
--   23505 if duplicates exist. Ops must reconcile manually before
--   this migration runs (see *.sql notes for the cleanup query).
--   The handler's mark-publishing + attach-thumbnail flows already
--   guard the live write path so no new duplicates will land after
--   this migration.

CREATE UNIQUE INDEX IF NOT EXISTS uniq_youtube_video_edits_open_session
    ON youtube_video_edits(workspace_id, platform_account_id, youtube_video_id)
    WHERE status IN ('editing', 'failed', 'publishing');
