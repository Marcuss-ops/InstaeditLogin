-- 127_youtube_video_edits_category_id.sql
--
-- Persists the authoritative YouTube snippet categoryId on the editor
-- session row. The extended session contract (thumbnail_url,
-- category_id, privacy_status) is served to InstaEditor by the
-- GET by-project / by-id detail endpoints; category_id is the one
-- field of that contract that has no existing column, so it is added
-- here and stamped by CreateEditorSession from the videos.list
-- projection fetched during creation (no extra YouTube round-trip on
-- the GET path).
--
-- Nullable on purpose: a session minted before this migration, or a
-- video whose videos.list read returned no category, simply omits the
-- field from the detail DTO (omitempty) rather than fabricating a value.

ALTER TABLE youtube_video_edits
    ADD COLUMN IF NOT EXISTS category_id TEXT;
