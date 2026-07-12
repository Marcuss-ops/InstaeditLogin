-- 011_target_publish_state.sql
-- Taglio 4.2: add publish_state column to post_targets for async publish
-- state tracking (replaces the synchronous 30x2s polling loop in TikTok
-- ReconcilePublish with a state machine: worker → reconciler → worker).
--
-- Why a separate column from `platform_post_id` and `status`:
--   - platform_post_id: the TikTok publish_id (immutable after StartPublish)
--   - status: the lifecycle state (queued / publishing / published / failed)
--   - publish_state: the platform-specific current state (PROCESSING_UPLOAD /
--     PENDING_PUBLISH / IN_REVIEW / PUBLISH_COMPLETE / FAILED), for debugging
--     and observability. Updated by the reconciler on every CheckPublishStatus.
--
-- Why a partial index on (status, platform_post_id) WHERE status='publishing':
--   The reconciler needs to find targets that are waiting for a platform
--   callback. The partial index keeps the lookup O(log n) without bloating
--   the index with rows that aren't being reconciled.

ALTER TABLE post_targets
    ADD COLUMN IF NOT EXISTS publish_state TEXT;

-- Partial index for the reconciler's hot-path query:
--   SELECT * FROM post_targets WHERE status='publishing' AND platform_post_id IS NOT NULL
-- Only rows in 'publishing' with a non-null publish_id are candidates for
-- reconciliation, so the partial WHERE clause keeps the index small.
CREATE INDEX IF NOT EXISTS idx_post_targets_publishing_publish_id
    ON post_targets(id) WHERE status = 'publishing' AND platform_post_id IS NOT NULL;
