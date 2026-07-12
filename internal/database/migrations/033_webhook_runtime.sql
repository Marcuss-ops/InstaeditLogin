-- =============================================================================
-- 033_webhook_runtime.sql — SPRINT 4.2: production-grade outbound webhooks.
-- =============================================================================
-- Three tables: webhook_endpoints (per-workspace subscriber URLs + secrets
-- + event filters), webhook_events (the dedup-anchored event log — one row
-- per emitted event with a stable event_id), webhook_deliveries (one row
-- per (event, endpoint) attempt-set; the hot path is "claim due pending
-- deliveries, POST, classify, reschedule-or-DLQ").
--
-- Signature scheme: X-Signature: sha256=<hex> where the signed string is
-- timestamp + "." + body. X-Timestamp: <unix>. X-Event-Id: <clear>. The
-- timestamp is part of the HMAC input so a replayed body with a stale
-- timestamp can be rejected by the receiver (the canonical 5-minute
-- tolerance window is the receiver's responsibility, not the server's).
--
-- Storage choice: webhook_endpoints.secret is stored as TEXT (raw, not
-- encrypted). The HMAC signer needs the raw secret to compute the
-- signature; storing a hash would make signing impossible. The threat
-- model accepts that a Postgres read by an admin can read the secret.
-- A future hardening pass can move to a per-row encryption-at-rest with
-- the central CredentialVault; not in this sprint.
--
-- Dedup model: webhook_events.event_id is the dedup anchor. The
-- dispatcher's INSERT ... ON CONFLICT (event_id) DO NOTHING is the
-- canonical dedup. The dispatcher fans out one webhook_deliveries row
-- per matching endpoint on first sight of the event; subsequent emits
-- with the same event_id short-circuit at the ON CONFLICT.
--
-- Backoff: webhook_deliveries.attempt counts the POST attempts. When a
-- 5xx / timeout / 429 is observed and attempt < max_attempts, the
-- dispatcher sets scheduled_at = now() + NextAttempt(attempt) where
-- NextAttempt is the AWS-style decorrelated-jitter curve documented
-- in internal/services/webhook_dispatcher.go. After max_attempts the
-- row is marked status='dead' (DLQ).
--
-- Idempotent. All DDL uses IF NOT EXISTS guards.
-- =============================================================================

-- ----------------------------------------------------------------------------
-- webhook_endpoints: per-workspace subscriber configuration.
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS webhook_endpoints (
    id            BIGSERIAL    PRIMARY KEY,
    workspace_id  BIGINT       NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    url           TEXT         NOT NULL,
    -- The raw HMAC secret. The signer uses it as-is to compute
    -- X-Signature. Stored as TEXT (not BYTEA, not encrypted) because
    -- the HMAC algorithm needs byte-for-byte access; encrypting would
    -- require unwrapping on every delivery. See file header for the
    -- threat-model note.
    secret        TEXT         NOT NULL,
    -- Event filter: which event types this endpoint subscribes to.
    -- The dispatcher only fans out to endpoints whose events array
    -- contains the emitted event type. Empty array = no events
    -- (i.e. disabled until the user adds at least one).
    events        TEXT[]       NOT NULL DEFAULT '{}',
    -- Lifecycle status. 'active' = subscribe + deliver; 'disabled' =
    -- skip fan-out (the user can re-enable without re-creating the
    -- endpoint). No 'deleted' — deletion is a hard DELETE.
    status        VARCHAR(50)  NOT NULL DEFAULT 'active',
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    CONSTRAINT webhook_endpoints_status_check CHECK (status IN ('active', 'disabled'))
);

-- Lookup-by-workspace: GET /api/v1/webhooks/endpoints.
CREATE INDEX IF NOT EXISTS idx_webhook_endpoints_workspace
    ON webhook_endpoints(workspace_id)
    WHERE status = 'active';

-- ----------------------------------------------------------------------------
-- webhook_events: the dedup-anchored event log. One row per emitted
-- event with a stable, client-supplied (or server-generated) event_id.
-- Two emits with the same event_id short-circuit at the ON CONFLICT;
-- the second emit returns the original event row and the dispatcher
-- does NOT create a second fan-out.
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS webhook_events (
    id           BIGSERIAL    PRIMARY KEY,
    -- Client-supplied or server-generated stable id. UNIQUE so the
    -- dispatcher's INSERT ... ON CONFLICT is the dedup anchor.
    event_id     TEXT         NOT NULL UNIQUE,
    event_type   TEXT         NOT NULL,
    workspace_id BIGINT       NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    payload      JSONB        NOT NULL,
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

-- Hot path for "events for workspace" queries (admin UI listing).
CREATE INDEX IF NOT EXISTS idx_webhook_events_workspace
    ON webhook_events(workspace_id, created_at DESC);

-- ----------------------------------------------------------------------------
-- webhook_deliveries: the per-(event, endpoint) attempt set. One row
-- per fan-out. The dispatcher claims due rows, POSTs, classifies the
-- response, and either reschedules (attempt < max_attempts) or marks
-- the row status='dead' (DLQ).
-- ----------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS webhook_deliveries (
    id             BIGSERIAL    PRIMARY KEY,
    event_id       BIGINT       NOT NULL REFERENCES webhook_events(id) ON DELETE CASCADE,
    endpoint_id    BIGINT       NOT NULL REFERENCES webhook_endpoints(id) ON DELETE CASCADE,
    -- 1-based attempt counter. After max_attempts the row is marked
    -- status='dead' (DLQ). Manual replay resets attempt=0 and
    -- status='pending'.
    attempt        INTEGER      NOT NULL DEFAULT 0,
    -- Lifecycle: 'pending' (due, not yet sent), 'success' (2xx
    -- terminal), 'dead' (DLQ — terminal until manual replay). No
    -- 'in_flight' column: the dispatcher uses a short
    -- SELECT ... FOR UPDATE SKIP LOCKED claim with a lease-style
    -- approach similar to the outbox pattern (see internal/worker/
    -- webhook_worker.go).
    status         VARCHAR(50)  NOT NULL DEFAULT 'pending',
    -- Diagnostic logs. request_log = (method, url, headers, body
    -- preview); response_log = (status, headers, body preview).
    -- Truncated to a few KB to bound row size.
    request_log    TEXT,
    response_log   TEXT,
    scheduled_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    completed_at   TIMESTAMPTZ,
    -- Last error message (for 5xx / 4xx / timeout / network blip).
    last_error     TEXT,
    -- FK indexes: the dispatcher's hot path is
    --   SELECT * FROM webhook_deliveries
    --    WHERE status = 'pending' AND scheduled_at <= NOW()
    --    ORDER BY scheduled_at ASC
    --    LIMIT N FOR UPDATE SKIP LOCKED
    -- The (status, scheduled_at) composite index serves the WHERE +
    -- ORDER BY without a sort.
    CONSTRAINT webhook_deliveries_status_check CHECK (status IN ('pending', 'success', 'dead'))
);

CREATE INDEX IF NOT EXISTS idx_webhook_deliveries_due
    ON webhook_deliveries(status, scheduled_at)
    WHERE status = 'pending';

-- Lookup-by-event: "show me every delivery attempt for this event"
-- (the replay UI surface). Btree on event_id only.
CREATE INDEX IF NOT EXISTS idx_webhook_deliveries_event
    ON webhook_deliveries(event_id);

-- Lookup-by-endpoint: "show me every delivery for this endpoint"
-- (the endpoint-management UI). Btree on endpoint_id.
CREATE INDEX IF NOT EXISTS idx_webhook_deliveries_endpoint
    ON webhook_deliveries(endpoint_id);

-- Lookup-by-workspace: "show me every delivery for this workspace"
-- (the workspace admin UI). Joins webhook_endpoints.workspace_id
-- via the endpoint_id FK.
CREATE INDEX IF NOT EXISTS idx_webhook_deliveries_endpoint_status
    ON webhook_deliveries(endpoint_id, status);
