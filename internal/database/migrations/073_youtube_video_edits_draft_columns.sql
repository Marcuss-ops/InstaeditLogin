-- Migration 073 — YouTube editor-session draft columns (Dark Editor auto-save)
--
-- What problem this solves
-- =========================
-- The Dark Editor's publish form (title, description, tags, default_language,
-- default_audio_language, translations, desired_privacy) was previously
-- persisted ONLY at the publish boundary. An operator who typed a title,
-- accidentally closed the browser tab mid-edit, and came back lost every
-- keystroke. This migration adds the storage the Dark Editor's
-- PUT /api/v1/youtube/editor-sessions/by-project/{id}/draft endpoint reads
-- + writes.
--
-- Why these columns exactly
-- =========================
-- - draft_title / draft_description / draft_default_language /
--   draft_default_audio_language / draft_desired_privacy: nullable TEXT.
--   NULL = "no draft written yet" (column empty). Empty string = "operator
--   cleared the field intentionally". The two states are distinct and
--   the SPA distinguishes them in the form hydration step.
-- - draft_tags TEXT[]: nullable so a tag-less draft is a NULL array, not
--   an empty array (consistent with the other nullable columns). The
--   handler / repo layer treats NULL and [] as semantically equivalent
--   in the publish path; NULL just keeps the row lighter for the common
--   "no draft yet" case.
-- - draft_translations JSONB: raw map[string]YouTubeTranslation payload.
--   Option A from the architecture verdict — no Postgres-side validation
--   of language codes / title-length; validation lives at the publish
--   boundary where the strict YouTubePublishedBounds gate runs.
-- - draft_updated_at TIMESTAMPTZ: the timestamp the SPA renders next to
--   "Bozza salvata hh:mm" + used as the liveness predicate for the
--   partial-recent index below.
-- - dirty_flag BOOLEAN NOT NULL DEFAULT FALSE: cheap marker for the future
--   "unsaved changes on disk" widget. The Dark Editor sets true
--   immediately on form-change, false on a successful PUT /draft
--   response. NOT NULL so a query without bool-default handling
--   (rare against the API layer) cannot accidentally surface NULL as
--   false-y in a dashboard card.
--
-- CAS predicate for the SaveDraft endpoint
-- ========================================
-- The handler's SaveDraft UPDATEs the row conditioned on
-- status IN ('editing', 'failed'). 'publishing' is excluded because the
-- publish orchestrator owns the row during that window — racing a draft
-- save against a publish would let an operator's keystrokes silently
-- overwrite the privacy/title the publish orchestrator just pushed to
-- YouTube. 'published' is excluded because a re-edit click after a
-- successful publish mints a FRESH session row (re-use semantics from
-- FindOrCreateEditableSession: once a row lands in 'published' it
-- stops matching the partial UNIQUE INDEX and the next click mints a
-- new row). The dirty_flag column is stamped in the same UPDATE as
-- draft_updated_at so a single SQL flips both — no read-modify-write
-- race between the "Bozza salvata" indicator and the dashboard card.
--
-- Migration safety
-- =================
-- - All columns are nullable (NONE of them are NOT NULL except dirty_flag
--   which has an explicit DEFAULT) so existing rows keep their existing
--   NULL draft_state semantics intact. No re-write of the existing
--   publish history.
-- - The partial index `idx_youtube_video_edits_draft_recent` is created
--   for a future "stale draft sweep" cron (e.g. operator abandons a draft
--   for >30 days — flip dirty_flag=false on the row, surface a "Resume
--   draft?" prompt). Drafts older than 30 days are NOT deleted by this
--   migration (no destructive operation).
-- - The dirty_flag column is the dashboard's "unsaved changes" pill.

ALTER TABLE youtube_video_edits
    ADD COLUMN IF NOT EXISTS draft_title TEXT,
    ADD COLUMN IF NOT EXISTS draft_description TEXT,
    ADD COLUMN IF NOT EXISTS draft_tags TEXT[],
    ADD COLUMN IF NOT EXISTS draft_default_language TEXT,
    ADD COLUMN IF NOT EXISTS draft_default_audio_language TEXT,
    ADD COLUMN IF NOT EXISTS draft_translations JSONB,
    ADD COLUMN IF NOT EXISTS draft_desired_privacy TEXT,
    ADD COLUMN IF NOT EXISTS draft_updated_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS dirty_flag BOOLEAN NOT NULL DEFAULT FALSE;

CREATE INDEX IF NOT EXISTS idx_youtube_video_edits_draft_recent
    ON youtube_video_edits (draft_updated_at)
    WHERE draft_updated_at IS NOT NULL;
