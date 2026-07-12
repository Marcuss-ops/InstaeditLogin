-- 018_publish_state_machine.sql
-- Zernio Milestone publish-state-machine: extend post_targets with the
-- retry-aware column set agreed in the strategist pass, AND add the
-- 'retrying' value to the post_status enum.
--
-- Why a dedicated migration rather than folding into 012 or 017:
-- every additive SQL change goes through its own migration file, so a
-- future operator reading git log can pinpoint "this is when the
-- retry-aware fields were added". The pieces (records + transitions
-- + worker logic + outbox) land in sequence as per the project's
-- one-migration-per-piece rule.
--
-- =========================================================================
-- 1. Extend the post_status enum with 'retrying'.
--    The DO-block pattern (idempotent pg_enum check) is the same one
--    012_async_threads_support.sql used for 'queued' / 'waiting_provider'
--    / 'partially_published'. Migration re-runs are no-ops once the value
--    has been added in a previous run.
--
-- Lifecycle, post-commit-018:
--
--   draft → queued → publishing → published
--                              → partially_published
--                              → waiting_provider
--                              → retrying ───→ (after next_attempt_at) ─→ publishing
--                              → failed
--
-- 'retrying' is NOT a terminal state — when next_attempt_at <= now, the
-- worker picks the target up and transitions back to 'publishing' (via
-- the POST-style claim query, see Commit 4). 'retrying' is fully covered
-- by IsActive(now) on the worker side: target.IsTerminal() returns
-- false for 'retrying', so the worker treats it like any other
-- in-flight state.
-- =========================================================================
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_enum
        WHERE enumtypid = (SELECT oid FROM pg_type WHERE typname = 'post_status')
          AND enumlabel = 'retrying'
    ) THEN
        ALTER TYPE post_status ADD VALUE 'retrying';
    END IF;
END $$;

-- =========================================================================
-- 2. Extend post_targets with the retry-aware column set.
--
-- All columns are NULLABLE / default to 0 so the change is fully
-- backward-compatible for existing rows: they pick up progress=0,
-- attempt_count=0, and NULLs for the rest. NOT NULL CHECKs on
-- progress + attempt_count reject negative values post-add (a row
-- with attempt_count < 0 would be a logic bug worth failing loudly).
--
-- Column contract (mirrored in internal/models/post.go PostTarget struct):
--
--   * current_step       — free-form pipeline-stage label written by
--                          the worker ("validating_token", "uploading_media",
--                          "publishing", "reconciling_async"). Operator UI
--                          reads this for progress visualisation.
--   * progress           — 0..100 percent, bumped on every async
--                          CheckPublishStatus when the provider reports
--                          a progress signal. Sync platforms keep 100
--                          on terminal publish.
--   * attempt_count      — retry counter. Monotonically increases every
--                          time the worker re-runs a failed target.
--                          The state machine caps retries via a separate
--                          application-level check (Commit 3 will land
--                          the constant MAX_ATTEMPTS = 5); no upper
--                          bound is enforced here so the column is
--                          safe to extend in future or relax.
--   * next_attempt_at    — TIMESTAMPTZ; worker re-claims a target when
--                          now >= next_attempt_at AND status='retrying'.
--                          NULL while the target is not in retry backoff.
--   * remote_post_id     — the PUBLIC-FACING id of the published post
--                          on the platform (e.g. Twitter tweet_id).
--                          Distinct from platform_post_id which holds
--                          the provider's INTERNAL id (publish_id) on
--                          async platforms. For sync platforms they're
--                          the same value, persisted AT PUBLISH TIME.
--   * remote_post_url    — the canonical URL of the published post
--                          (e.g. https://twitter.com/foo/status/1234).
--                          NULL until a successful publish on async
--                          platforms; sync platforms populate this from
--                          PublishResult.PlatformURL.
--   * last_error_code    — short stable code (e.g. "RATE_LIMITED",
--                          "INVALID_TOKEN", "MEDIA_UNREACHABLE",
--                          "CONTAINER_NOT_READY") useful for dashboards
--                          / retry policies. Distinct from
--                          error_message which is the human-readable
--                          narrative. Codes are platform-agnostic so
--                          dashboards can group-by-error-code.
-- =========================================================================
ALTER TABLE post_targets
    ADD COLUMN IF NOT EXISTS current_step TEXT;
ALTER TABLE post_targets
    ADD COLUMN IF NOT EXISTS progress INT NOT NULL DEFAULT 0
        CHECK (progress >= 0 AND progress <= 100);
ALTER TABLE post_targets
    ADD COLUMN IF NOT EXISTS attempt_count INT NOT NULL DEFAULT 0
        CHECK (attempt_count >= 0);
ALTER TABLE post_targets
    ADD COLUMN IF NOT EXISTS next_attempt_at TIMESTAMPTZ;
ALTER TABLE post_targets
    ADD COLUMN IF NOT EXISTS remote_post_id TEXT;
ALTER TABLE post_targets
    ADD COLUMN IF NOT EXISTS remote_post_url TEXT;
ALTER TABLE post_targets
    ADD COLUMN IF NOT EXISTS last_error_code TEXT;

-- =========================================================================
-- 3. Indexes for the worker pickup query (Commit 4 will use them).
--
-- The worker's pending-target list changes from
--   (status='queued' AND scheduled_at <= now) OR status='waiting_provider'
-- to
--   (status='queued' AND scheduled_at <= now)
--   OR (status='retrying' AND next_attempt_at <= now)
--   OR status='waiting_provider'
--
-- Each branch hits a different partial index. Partial indexes are
-- cheap (only rows in the indexed state are present), so the cost
-- is bounded by the active retry count which is typically << 100.
-- =========================================================================
CREATE INDEX IF NOT EXISTS idx_post_targets_retrying_next_attempt
    ON post_targets(next_attempt_at)
    WHERE status = 'retrying' AND next_attempt_at IS NOT NULL;

-- A non-partial btree on (status, next_attempt_at) is NOT added: the
-- worker only ever reads next_attempt_at when status='retrying' (the
-- partial index covers that), and the production reads are by status
-- already (idx_post_targets_status-equivalents live in their own
-- migrations). Keep this migration minimal.
