-- =============================================================================
-- Migration 066: youtube_target_publications
-- =============================================================================
-- P0 — per-target YouTube pipeline state (Blocco #1: separare upload YouTube
-- privato dalla pubblicazione programmata).
--
-- Connects three axes of state into one row keyed on the fan-out target:
--   * upload_jobs.id               — the denormalised ingest/publish queue
--                                    parent for this video.
--   * post_targets.id              — the per-channel fan-out row the publish
--                                    worker already drove into status history.
--   * platform_accounts.id         — the YouTube channel that must own this
--                                    upload; token-binding lives on the
--                                    account's rows.
--
-- Why a separate table (vs cramming fields onto post_targets or upload_jobs):
--   * upload_jobs is shared across many platforms and many videos; per-target
--     YouTube-only columns (velox_project_id, thumbnail_status, ...) would
--     pollute its generic shape.
--   * post_targets already carries the cross-platform retry/last_error
--     accounting; mixing in YouTube-specific lifecycle fields (drive_queued,
--     youtube_uploading, thumbnail_editing, thumbnail_ready, scheduled,
--     publishing, published) plus dead-letter side states would force the
--     canonical PostStatus enum to know about YouTube-only substates.
--
-- Lifecycle the column set supports (documented here so future workers
-- pick a coherent vocabulary):
--
--   youtube_upload_status:    youtube_uploading | youtube_uploaded
--                                | youtube_processing | retry_wait
--                                | blocked_auth | failed | dead_letter
--   youtube_processing_status: pending | processed  (raw YouTube API echo,
--                                                    set when we poll the
--                                                    video's processingStatus)
--   thumbnail_status:           pending | thumbnail_editing | thumbnail_ready
--                                | failed
--   desired_privacy:            public|unlisted|private  (snapshot from
--                                       upload_job.default_privacy_level
--                                       OR post.privacy_level cascade)
--
-- FK POLICY:
--   * post_target_id   → post_targets(id) ON DELETE CASCADE. Deleting a
--                        post drops its per-target publication rows
--                        (matches post_targets.cascade-from-posts).
--   * platform_account_id → platform_accounts(id) ON DELETE CASCADE.
--                        Unlinking an account drops its pending
--                        publications; we never want to silently retry
--                        against an unlinked account.
--   * upload_job_id    → BIGINT (intentionally NOT a foreign key constraint).
--                        upload_jobs is itself denormalised and the
--                        relationship is soft; we want the publication
--                        history to survive any future upload_jobs rotation
--                        (e.g. ingest re-runs that delete+recreate upload
--                        rows). Index only.
--
-- TEXT status (NOT PG ENUM) — mirrors youtube_video_edits.query design so
-- future schema drops don't get blocked by PG < 18's no-DROP-VALUE rule.
--
-- IDEMPOTENT: every DDL is `CREATE ... IF NOT EXISTS` and the table is
-- additive (no ALTER on pre-existing tables). Re-runs are no-ops.
-- =============================================================================

CREATE TABLE IF NOT EXISTS youtube_target_publications (
    id BIGSERIAL PRIMARY KEY,
    upload_job_id BIGINT NOT NULL,
    post_target_id BIGINT NOT NULL UNIQUE REFERENCES post_targets(id) ON DELETE CASCADE,
    platform_account_id BIGINT NOT NULL REFERENCES platform_accounts(id) ON DELETE CASCADE,
    youtube_video_id TEXT,
    youtube_upload_status TEXT NOT NULL DEFAULT 'youtube_uploading',
    youtube_processing_status TEXT,
    editor_session_id TEXT,
    velox_project_id TEXT,
    thumbnail_media_id TEXT,
    thumbnail_status TEXT,
    desired_privacy TEXT NOT NULL DEFAULT 'public',
    publish_at TIMESTAMPTZ,
    published_at TIMESTAMPTZ,
    last_error TEXT NOT NULL DEFAULT '',
    attempt_count INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Webhook callback lookup: YouTube tells us which video our status push is
-- about, we resolve it back to the per-target publication row.
-- Partial index keeps the index small: rows where youtube_video_id is NULL
-- (upload still pending) cannot be the target of a webhook lookup, so we
-- don't pay index maintenance for them.
CREATE INDEX IF NOT EXISTS idx_youtube_target_pubs_video_id
    ON youtube_target_publications(youtube_video_id)
    WHERE youtube_video_id IS NOT NULL;

-- Pipeline view lookup: GET /api/v1/content/{content_id}/pipeline fans
-- out from upload_job.id to N per-target rows. Index keeps it O(N+logN).
CREATE INDEX IF NOT EXISTS idx_youtube_target_pubs_upload_job
    ON youtube_target_publications(upload_job_id);
