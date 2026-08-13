-- =============================================================================
-- Migration 125: youtube_target_publications → atomic YouTube delivery queue
-- =============================================================================
-- Blocco #1 of the delivery-queue refactor: promote the existing per-target
-- publication row into an independently claimable, retryable, scheduled unit
-- of work (a "delivery"). The row ALREADY connects the three axes —
-- upload_job_id ↔ post_target_id ↔ platform_account_id — and carries the
-- upload / processing / thumbnail / privacy lifecycle. This migration adds
-- the operational fields that turn it into a queue, WITHOUT re-encoding any
-- fact another column already holds.
--
-- NEW COLUMNS:
--   * state               — canonical lifecycle cursor for the WHOLE
--                           delivery. The existing youtube_upload_status /
--                           youtube_processing_status / thumbnail_status
--                           columns are KEPT and demoted to per-phase
--                           observations; `state` is the single operational
--                           cursor (preflight → ready_to_upload → uploading →
--                           youtube_uploaded → processing → thumbnail_* →
--                           scheduled → published → verified, plus the side
--                           states retry_wait / quota_wait / blocked_auth /
--                           copyright_review / processing_stuck / failed /
--                           dead_letter).
--   * priority            — SMALLINT, lower = higher priority (drives the
--                           claim ORDER BY). Mirrors upload_jobs.priority.
--   * prepare_at          — earliest wall-clock time the delivery may be
--                           claimed (the materializer computes it from
--                           publish_at; NULL = eligible immediately).
--   * next_attempt_at     — retry backoff cursor (retry_wait / quota_wait
--                           rows are unclaimable until it elapses).
--   * max_attempts        — retry cap before dead_letter (attempt_count
--                           already exists from migration 066).
--   * lease_owner / lease_expires_at / heartbeat_at — worker claim +
--                           liveness so a crashed worker's row can be
--                           reclaimed (same pattern as upload_jobs /
--                           import_batches / webhook_deliveries).
--   * resume_state        — where a side state (quota_wait / blocked_auth /
--                           retry_wait) returns once its condition clears,
--                           so the worker never has to re-derive context.
--   * last_error_code     — stable machine-readable error class (sister of
--                           last_error, which stays human-readable).
--   * last_transition_at  — stamped on every state change (audit + stuck
--                           detection); NOT NULL DEFAULT NOW() so existing
--                           rows backfill to a sane value.
--   * verified_at         — terminal success stamp (published → verified).
--   * original_publish_at — the user's originally requested publish time,
--                           preserved across capacity spillover so operators
--                           can see what was actually asked for.
--   * spillover_count     — how many days capacity planning moved this
--                           delivery forward (audit of the planner).
--
-- WHAT WE DELIBERATELY DO NOT ADD (no second representation of the same
-- fact — see the refactor design):
--   * no `scheduled_for`  — publish_at already exists.
--   * no `channel_id`     — platform_account_id already names the channel.
--   * no `target_id`      — post_target_id already names the target.
--
-- UNIQUE CONSTRAINT — NOT re-created here. Migration 066 already declares
-- `post_target_id BIGINT NOT NULL UNIQUE`, which is exactly the invariant
-- this queue relies on: 1 PostTarget (YouTube) = 1 publication = 1 delivery.
-- The materializer's INSERT ... ON CONFLICT (post_target_id) DO NOTHING
-- idempotency rests on that existing constraint; adding a second unique
-- index on the same column would only duplicate it.
--
-- IDEMPOTENT: every DDL is ADD COLUMN IF NOT EXISTS / CREATE INDEX IF NOT
-- EXISTS, so re-runs and rolling multi-replica deploys are no-ops. No
-- backfill is invented: existing rows start at state='preflight' (the
-- worker re-derives their real state on first claim) and spillover/original
-- metadata stay NULL/0 because that history was never recorded.
-- =============================================================================

ALTER TABLE youtube_target_publications
    ADD COLUMN IF NOT EXISTS state               TEXT        NOT NULL DEFAULT 'preflight',
    ADD COLUMN IF NOT EXISTS priority            SMALLINT    NOT NULL DEFAULT 100,
    ADD COLUMN IF NOT EXISTS prepare_at          TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS next_attempt_at     TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS max_attempts        INTEGER     NOT NULL DEFAULT 8,
    ADD COLUMN IF NOT EXISTS lease_owner         TEXT,
    ADD COLUMN IF NOT EXISTS lease_expires_at    TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS heartbeat_at        TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS resume_state        TEXT,
    ADD COLUMN IF NOT EXISTS last_error_code     TEXT,
    ADD COLUMN IF NOT EXISTS last_transition_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ADD COLUMN IF NOT EXISTS verified_at         TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS original_publish_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS spillover_count     INTEGER     NOT NULL DEFAULT 0;

-- Claim hot path: the delivery worker repeatedly asks "which rows are in a
-- runnable state AND due (prepare_at / next_attempt_at) AND unlocked
-- (lease_expires_at < NOW())". The partial predicate keeps the index to only
-- the four states the worker ever claims; the trailing ordering columns
-- (priority, publish_at, id) mirror the claim query's ORDER BY so the index
-- can feed ORDER BY without a sort. prepare_at / next_attempt_at sit in the
-- middle to support the `IS NULL OR <= NOW()` due-predicate as an index
-- condition even though an OR predicate can't be a perfect scan bound.
CREATE INDEX IF NOT EXISTS idx_yt_delivery_claim
    ON youtube_target_publications (state, prepare_at, next_attempt_at, priority, publish_at, id)
    WHERE state IN ('preflight', 'ready_to_upload', 'retry_wait', 'quota_wait');

-- Per-channel schedule view: group pending deliveries by channel and publish
-- time (operator dashboard + capacity-planning fan-out read the schedule
-- per platform_account).
CREATE INDEX IF NOT EXISTS idx_yt_delivery_channel_schedule
    ON youtube_target_publications (platform_account_id, publish_at, state);

-- Stuck-detection hot path: find rows that have sat in a given state too
-- long (uploading / processing / scheduled sweeps) ordered by staleness.
CREATE INDEX IF NOT EXISTS idx_yt_delivery_state_updated
    ON youtube_target_publications (state, updated_at);

COMMENT ON TABLE youtube_target_publications IS
'Per-target YouTube delivery (1 PostTarget YouTube = 1 publication = 1 delivery). '
'`state` is the canonical lifecycle cursor (preflight → ready_to_upload → uploading → '
'youtube_uploaded → processing → thumbnail_pending/thumbnail_ready → scheduled → published → '
'verified; side states retry_wait / quota_wait / blocked_auth / copyright_review / '
'processing_stuck / failed / dead_letter), while youtube_upload_status / '
'youtube_processing_status / thumbnail_status remain per-phase observations. Claimed by the '
'delivery worker via lease_owner + lease_expires_at + heartbeat_at (FOR UPDATE SKIP LOCKED), '
'retried via next_attempt_at / max_attempts, and audited via last_transition_at + last_error_code.';
