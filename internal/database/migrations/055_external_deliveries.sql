-- =============================================================================
-- 055_external_deliveries.sql — Velox integration (Phase 2 ingest window)
-- =============================================================================
-- Why this migration exists:
--
-- The Velox <-> InstaEdit delivery contract hands a one-shot
-- artifact reference (sha256 + size + download_url) plus a
-- publish-payload (title/description/tags/privacy) plus a
-- callback channel for status changes. Every accepted
-- delivery must be:
--
--   1. IDEMPOTENT — same idempotency_key + same payload →
--      same social_delivery_id; different payload → 409.
--   2. AUDITABLE — every state transition lands a row update
--      with timestamp + status code + optional reason.
--   3. LINKABLE — once the publish_worker creates an
--      upload_job (ingest) and a post (publish), the
--      delivery row MUST know about both for "by-delivery"
--      dashboards and for Velox callback correlation.
--   4. RESILIENT — Velox may retry; the partial index
--      `(upload_job_id) WHERE upload_job_id IS NOT NULL`
--      makes the "is this delivery already running?" check
--      one range scan.
--
-- This table is the authoritative on-disk journal of the
-- delivery lifecycle. The publish_worker reads it for
-- retry/deliver state, the callback dispatcher reads it to
-- decide which rows to update on Velox webhook events, and
-- the admin dashboard reads it for "what did Velox hand us
-- in the last hour?" reporting.
--
-- State lifecycle (11 values, in TEXT + named CHECK):
--
--   accepted           — POST /deliveries stored, Velox received 202
--   downloading        — worker issued HEAD/GET against download_url
--   artifact_verified  — sha256 + size + mime all match,
--                         file promoted into InstaEdit storage
--   ingest_completed   — upload_job created, asset linked,
--                         state matches publish_worker's
--                         ingest_completed enum value (migration 049a)
--   queued             — publish_pool picked up the row,
--                         publish_at scheduled
--   publishing         — YouTube API videos.insert in flight
--   published          — publish success: platform_media_id +
--                         platform_url populated
--   retry_wait         — transient error, exponential backoff window
--   blocked_auth       — reauth_required on the platform_account;
--                        worker halts; admin must reconnect
--   failed             — terminal non-recoverable (e.g.
--                        artifact SHA mismatch, JSON validation)
--   dead_letter        — max_attempts exhausted; persisted for
--                        ops review; no further processing
--
-- Why TEXT + named CHECK (not ENUM):
--
--   043_oauth_connections used VARCHAR(32) for status; 050
--   used TEXT; 046/049a used ENUM for upload_job_status. We
--   follow 050's pattern (TEXT + CHECK) because (a) the state
--   set is unlikely to change shape for years but new states
--   ARE plausible (e.g. `moderation_required` if a future
--   platform adds explicit review), and (b) CHECK gives a
--   readable failure message in production logs without
--   needing an `ALTER TYPE ADD VALUE` round-trip.
--
-- Request-body SHA — the conflict-detection pair:
--
--   The user's spec says "stessa chiave, payload differente →
--   409". The pair (idempotency_key, request_sha256) is the
--   key: same key + same SHA → reuse social_delivery_id and
--   return identical 202; same key + different SHA → 409 with
--   the documented error. request_sha256 is NOT indexed because
--   it's only read on lookup after the UNIQUE
--   (source_system, idempotency_key) has narrowed to a single
--   row — the planner pulls the single tuple and checks the
--   hash in memory.
--
-- Idempotency:
--
--   Same as 054 — runner has no schema_migrations table, so
--   every DDL MUST be idempotent on its own (`IF NOT EXISTS`
--   for tables, columns, and indexes).
--
-- ON DELETE policy:
--
--   external_destination_id → RESTRICT — preserve historical
--     audit trail even if the destination is removed/replaced.
--     The application layer disables (enabled=false) before
--     deleting.
--   upload_job_id → SET NULL — mirror 050 import_batches.batch_id.
--     If the upload_job is purged (rare; manual cleanup), the
--     delivery journal still exists with NULL upload_job_id —
--     the post_id remains the second lookup handle.
--   post_id → no FK (BIGINT, by spec). Same precedent as
--     migration 036 upload_jobs.post_id — avoids the
--     complications of deliveries that predate successful
--     post materialisation or outlive post deletion.
-- =============================================================================

CREATE TABLE IF NOT EXISTS external_deliveries (
    -- Opaque social-delivery id (ULID with `sdel_` prefix).
    -- Generated application-side at handler POST time. NOT
    -- DB-generated; the handler must enforce the prefix.
    id                       TEXT        PRIMARY KEY,

    -- The upstream discriminator that owns this delivery. Same
    -- semantics as external_destinations.source_system; lets
    -- Velox + future Dropbox co-exist without collisions.
    source_system            TEXT        NOT NULL,

    -- The upstream's own delivery id (Velox: "delivery_8cc0f...").
    -- Visible to operators debugging cross-system traces
    -- (e.g. "show me what happened to Velox delivery X").
    external_delivery_id     TEXT        NOT NULL,

    -- The upstream's idempotency key (Velox: composite "delivery_xxx|
    -- dest_yyy"). UNIQUE together with source_system; the
    -- INSERT path uses ON CONFLICT to reuse social_delivery_id
    -- at retry time.
    idempotency_key          TEXT        NOT NULL,

    -- FK to external_destinations. RESTRICT on delete so a delivery
    -- row stays resolvable for audit even if the destination is
    -- removed (the application disables before deleting).
    external_destination_id  TEXT        NOT NULL
        REFERENCES external_destinations(id) ON DELETE RESTRICT,

    -- ── Artifact triple (the one-shot contract from Velox) ────────────
    -- source_artifact_id   — Velox's own artifact id (e.g.
    --                         "artifact_01JXYZ"), useful for
    --                         cross-system audit.
    -- expected_sha256      — hex-encoded SHA-256 declared by
    --                         Velox at handoff. The downloader
    --                         re-hashes the body and rejects on
    --                         mismatch (PermanentError).
    -- expected_size_bytes   — declared size. The downloader uses
    --                         io.LimitReader with N+1 to detect
    --                         truncation / oversize.
    -- expected_mime_type    — declared MIME. The downloader does
    --                         Content-Type sniff (HEAD on the
    --                         download_url) and rejects on
    --                         mismatch.
    source_artifact_id       TEXT        NOT NULL,
    expected_sha256          TEXT        NOT NULL,
    expected_size_bytes      BIGINT      NOT NULL,
    expected_mime_type       TEXT        NOT NULL,

    -- download_url — presigned S3/MinIO URL (today) or HMAC
    -- signed artifact endpoint (alternate path per the user's
    -- memo). The downloader fetches it once; an expired URL
    -- means we fall through to the artifact endpoint via
    -- a follow-up. Nullable for now because future "metadata
    -- only" deliveries (e.g. embed POSTs) may not carry an
    -- artifact at all; the worker short-circuits in that case.
    download_url             TEXT,

    -- JSONB publish-payload envelope (title, description, tags,
    -- language, etc.). Round-tripped from Velox verbatim so the
    -- worker doesn't need to re-parse from a different shape.
    metadata                 JSONB       NOT NULL DEFAULT '{}'::jsonb,

    -- Optional scheduled publish wall-clock. The publish_worker
    -- honors this as the publish pool's earliest-eligibility
    -- timestamp; ingest (download + verify + create upload_job)
    -- runs IMMEDIATELY regardless, because Velox has already
    -- billed the processing slot and the artifact is canonical.
    publish_at               TIMESTAMPTZ,

    -- Velox callback URL for status updates. The dispatcher
    -- (separate package) issues POSTs here with HMAC headers
    -- (X-Velox-Event-ID / Timestamp / Signature). Nullable for
    -- deliveries where Velox opted out of async notifications.
    callback_url             TEXT,

    -- Lifecycle status. 11-value set; named CHECK constraint
    -- gives readable failure messages in production logs.
    status                   TEXT        NOT NULL DEFAULT 'accepted',

    -- SHA-256 of the raw POST body. Lets the handler detect a
    -- same-key-but-different-payload replay and return 409
    -- (per the Velox <-> InstaEdit idempotency spec). NOT indexed
    -- looked up via UNIQUE(source_system, idempotency_key) once.
    request_sha256           TEXT        NOT NULL,

    -- Link to the canonical upload_job created by the worker's
    -- ingest step. SET NULL on delete preserves the delivery
    -- journal across manual cleanups.
    upload_job_id            BIGINT
        REFERENCES upload_jobs(id) ON DELETE SET NULL,

    -- Link to the published post. BIGINT (no FK by spec —
    -- mirrors 036 upload_jobs.post_id precedent; lets the delivery
    -- outlive post deletion for cross-system audit).
    post_id                  BIGINT,

    -- Populated when status transitions to `published`. platform_url
    -- is the canonical user-facing "share this" link (YouTube watch?v=
    -- or analogous).
    platform_media_id        TEXT,
    platform_url             TEXT,

    -- Stable code for the most recent failure (e.g. youtube_quotaExceeded,
    -- drive_404, artifact_sha256_mismatch). Mirrors
    -- upload_jobs.error_code for consistent dashboard filtering.
    last_error_code          TEXT,
    last_error_message       TEXT,

    created_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at             TIMESTAMPTZ,

    -- Two idempotency lookup paths:
    -- 1. by upstream's delivery id (cross-system trace lookup)
    -- 2. by upstream's idempotency key (replay/retry same row)
    UNIQUE (source_system, external_delivery_id),
    UNIQUE (source_system, idempotency_key)
);

-- Enforced via named CHECK constraint for readable failures.
-- Matches 051's CHECK pattern + the spec's enumerated state list.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'chk_external_deliveries_status'
    ) THEN
        ALTER TABLE external_deliveries
            ADD CONSTRAINT chk_external_deliveries_status
            CHECK (status IN (
                'accepted',
                'downloading',
                'artifact_verified',
                'ingest_completed',
                'queued',
                'publishing',
                'published',
                'retry_wait',
                'blocked_auth',
                'failed',
                'dead_letter'
            ));
    END IF;
END $$;

-- Worker pool's "claim next batch" partial index. Mirrors the
-- ClaimBatchForPublish CTE pattern from migration 046:
--   SELECT id FROM external_deliveries
--    WHERE status IN (active set)
--      AND created_at <= NOW()
--      [AND next_attempt_at IS NULL OR next_attempt_at <= NOW()]
--    ORDER BY created_at ASC
--    LIMIT $1 FOR UPDATE SKIP LOCKED
CREATE INDEX IF NOT EXISTS idx_external_deliveries_worker_pool
    ON external_deliveries (status, created_at)
    WHERE status IN (
        'accepted',
        'downloading',
        'artifact_verified',
        'ingest_completed',
        'queued',
        'publishing',
        'retry_wait'
    );

-- By-upload-job lookup. The publish_worker, when it creates
-- upload_job, stamps the delivery's upload_job_id. The
-- dashboard queries "show me the delivery that produced this
-- upload_job" — partial index excludes the historical
-- pre-link rows where upload_job_id is NULL.
CREATE INDEX IF NOT EXISTS idx_external_deliveries_upload_job_id
    ON external_deliveries (upload_job_id)
    WHERE upload_job_id IS NOT NULL;

-- Reconciliation: GET /internal/v1/deliveries/{id} + admin
-- dashboard "list deliveries for destination X". Drives the
-- callback dispatcher's lookup too (which delivery triggered
-- this callback?).
CREATE INDEX IF NOT EXISTS idx_external_deliveries_destination_status
    ON external_deliveries (external_destination_id, status, created_at DESC);

-- Cross-system trace lookup by upstream delivery id. Velox
-- support flow: operator reports a Velox delivery id, we
-- look up here to find the InstaEdit acceptance + final
-- status in one index range scan.
CREATE INDEX IF NOT EXISTS idx_external_deliveries_source_external_id
    ON external_deliveries (source_system, external_delivery_id);

COMMENT ON TABLE  external_deliveries IS
    'Velox → InstaEdit delivery journal. One row per accepted POST /internal/v1/deliveries. 11-state lifecycle (TEXT+CHECK). Survives retries via (source_system, idempotency_key) UNIQUE guard; 409 on same-key-different-body via request_sha256.';
COMMENT ON COLUMN external_deliveries.id IS
    'Opaque ULID with `sdel_` prefix generated application-side at POST /deliveries. Distinct from external_delivery_id (Velox''s own id) for log scannability.';
COMMENT ON COLUMN external_deliveries.external_delivery_id IS
    'Upstream''s own delivery id (Velox: "delivery_8cc0f..."). For cross-system audit; second UNIQUE collision surface with source_system.';
COMMENT ON COLUMN external_deliveries.idempotency_key IS
    'Upstream''s idempotency string (Velox: composite "delivery_xxx|dest_yyy"). Primary idempotency lookup handle; same key + same body returns the existing social_delivery_id; same key + different body returns 409.';
COMMENT ON COLUMN external_deliveries.status IS
    '11-value lifecycle (TEXT + named CHECK). Translates accepted → downloading → artifact_verified → ingest_completed → queued → publishing → published; error states retry_wait / blocked_auth / failed / dead_letter. State transitions stamped in updated_at column.';
COMMENT ON COLUMN external_deliveries.request_sha256 IS
    'SHA-256 of the raw POST body. Detects same-key-different-body replays → 409. NOT indexed — looked up via the (source_system, idempotency_key) UNIQUE on arrival, then in-memory compared.';
COMMENT ON COLUMN external_deliveries.callback_url IS
    'Velox callback URL; numeric dispatcher POSTs status updates here with HMAC headers. Nullable for deliveries where Velox opted out of async notifications.';
COMMENT ON COLUMN external_deliveries.upload_job_id IS
    'FK to upload_jobs (SET NULL on delete). Set by publish_worker when ingest creates the canonical upload_job row. NULL preserves the journal row when the upload_job is purged.';
COMMENT ON COLUMN external_deliveries.post_id IS
    'BIGINT (no FK by spec). Set when status → published. Mirrors 036 upload_jobs.post_id precedent — survives post deletion, no lock-coupling in the publish path.';
COMMENT ON COLUMN external_deliveries.completed_at IS
    'Set ONCE when status reaches a terminal state (published / failed / dead_letter / blocked_auth). NULL while the row is mid-lifecycle. Distinct from updated_at which transitions on every state change.';
