-- 053_upload_jobs_and_posts_default_privacy_level.sql
--
-- P1 privacy_level precedence materialisation chain:
--
--   operator UI / API
--        │
--        ▼  (per-post override — may be NULL)
--   posts.privacy_level
--        │
--        ▼  (inherited from upload_job → import_batch if post.privacy_level is NULL)
--   posts.default_privacy_level
--        │
--        │    bound by publish_worker to PublishPayload.PrivacyLevel via the
--        │    allowlist validated in:
--        │      internal/services/youtube_oauth.go::ValidateContent
--        │      internal/services/youtube_oauth.go::validateYouTubePrivacyLevel
--        ▼
--   PublishPayload.PrivacyLevel
--        │
--        ▼  (YouTube API videos.insert — boundary)
--   status.privacyStatus (public|unlisted|private)
--
-- Three persistance points are touched:
--   1. upload_jobs.default_privacy_level  — stamped per-file by
--      internal/worker/drive_batch_crawler.go when the Drive batch crawler
--      fans out a single upload_job per file, copying the value verbatim
--      from the parent import_batch row (handler
--      pkg/api/drive_batch_v2.go::handleDriveBatchImportV2 already
--      allowlist-validates the value at producer boundary).
--   2. posts.default_privacy_level        — copied from upload_job by
--      internal/worker/upload_worker.go::processPublishJob when a single
--      Post is materialised for an upload_job → post fan-out. PublishWorker
--      reads this as the middle term of the precedence cascade.
--   3. posts.privacy_level                — per-post override set by the
--      post-create API endpoint (and editable via post-update). Highest
--      precedence term; UI-grade override.
--
-- The boundary allowlist (public|unlisted|private) is enforced ONLY at
-- the YouTube capability boundary in
-- internal/services/youtube_oauth.go::ValidateContent → validateYouTubePrivacyLevel.
-- The handler at pkg/api/drive_batch_v2.go applies the same allowlist
-- upstream so an operator cannot even submit a malformed batch. The new
-- columns on posts + upload_jobs are NOT bound to a CHECK constraint
-- because they round-trip through the worker chain (the worker's
-- propagation step trusts the upstream allowlist, but a defence-in-depth
-- CHECK would be a single-shot tripwire — a future taglio may add it).

ALTER TABLE upload_jobs
    ADD COLUMN default_privacy_level VARCHAR(50) NOT NULL DEFAULT '';

ALTER TABLE posts
    ADD COLUMN default_privacy_level VARCHAR(50) NOT NULL DEFAULT '';

ALTER TABLE posts
    ADD COLUMN privacy_level        VARCHAR(50) NOT NULL DEFAULT '';

-- DOWN
-- (manual operator invocations; the up above is the canonical direction)
-- ALTER TABLE posts DROP COLUMN privacy_level;
-- ALTER TABLE posts DROP COLUMN default_privacy_level;
-- ALTER TABLE upload_jobs DROP COLUMN default_privacy_level;
