-- 126_youtube_target_native_publish_at.sql
-- Native YouTube scheduled publishing (status.publishAt).
--
-- When the Phase-1 private videos.insert carries status.publishAt (the
-- post has a FUTURE publish_at AND the desired privacy is public),
-- YouTube owns the private→public transition and publishes the video at
-- that time automatically. The publish worker must then NOT re-issue a
-- videos.update at publish_at — that call would be redundant and burns
-- ~50 units from the 2026 "general" quota bucket per scheduled video.
--
-- native_publish_at records the publish_at value baked into the initial
-- insert so the publish phase can distinguish "YouTube owns the
-- transition" (native_publish_at IS NOT NULL) from "we must flip
-- privacy ourselves" (NULL: immediate publish, or desired privacy
-- != public). Existing rows keep NULL — they were uploaded before this
-- migration and still need the videos.update path.

ALTER TABLE youtube_target_publications
    ADD COLUMN IF NOT EXISTS native_publish_at TIMESTAMPTZ;

COMMENT ON COLUMN youtube_target_publications.native_publish_at IS
'publish_at value baked into the initial videos.insert status.publishAt. '
'Non-NULL means YouTube owns the private->public transition at that time '
'and the publish worker must NOT re-issue a videos.update for the same '
'transition (migration 126). NULL when the upload did not carry a native '
'schedule (immediate publish, or desired privacy != public).';
