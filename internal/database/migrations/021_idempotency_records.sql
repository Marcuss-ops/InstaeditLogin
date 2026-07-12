-- 021_idempotency_records.sql
-- Zernio Milestone publish-state-machine: idempotency layer 1.
--
-- The idempotency_records table is the REDUX-style cache for the
-- `Idempotency-Key` HTTP request header. The middleware (pkg/api/idempotency.go,
-- commit-mate of this migration) consults it on every POST request that
-- carries the header:
--
--   * miss                          → handler runs, INSERTs the record
--                                       with the resulting resource_id.
--   * hit + hash(request) == hash(stored) → return original resource.
--   * hit + hash(request) != hash(stored) → 409 idempotency_key_conflict.
--
-- The cache is workspace-scoped (UNIQUE on (workspace_id, idempotency_key))
-- so cross-tenant key-prefix collisions don't poison the lookup.
--
-- =========================================================================
-- Schema notes (mirrored in internal/models/post.go IdempotencyRecord struct):
--
--   * workspace_idFK to workspaces ON DELETE CASCADE — when a workspace
--     dies all of its cached requests die with it. Today workspaces
--     aren't deleted via the API; if/when they are, the cascade is
--     the right behaviour (don't carry orphaned idempotency_records).
--
--   * idempotency_key TEXT NOT NULL — the value of the request header.
--     Stored unredacted; ops reads it for client debugging. We do
--     NOT hash the key itself because the lookup is the WHERE clause
--     (UNIQUE index); hashing would prevent equality lookup.
--
--   * resource_type TEXT NOT NULL — discriminator. Today "post" is the
--     only resource type that runs through this code path; future
--     resources can register "post_target", "workspace", etc. CHECK
--     could be added later, but TEXT is permissive on purpose: a
--     CHECK at this layer would force a migration every time a new
--     resource uses idempotency.
--
--   * resource_id BIGINT NOT NULL — the FK pointer to the actual
--     resource. NO constraints on this column (we don't FK to every
--     possible resource table); a DELETE on the resource must
--     explicitly cascade or rely on the workspace CASCADE above.
--
--   * request_hash BYTEA NOT NULL — 32 bytes (SHA-256) of the request
--     body bytes (the JSON post body verbatim, BEFORE json.Decode
--     re-orders fields). Stable across processes because SHA-256 has
--     no salt. Lookup equality via bytes.Equal.
--
--   * response_status INT NOT NULL — HTTP status code the original
--     handler returned (typically 201 Created). On replay the cached
--     status is re-emitted so clients see the same shape.
--
--   * expires_at TIMESTAMPTZ NOT NULL DEFAULT now() + interval '24 hours'
--     — Stripe-style 24h TTL. The middleware ignores expired rows on
--     lookup (FindActiveByKey filters server-side), so a record that
--     outlives TTL is harmless for correctness; a CRON cleanup sweep
--     DELETEs expired rows so the table doesn't grow forever.
--
-- =========================================================================
-- Indexes:
--
--   * UNIQUE (workspace_id, idempotency_key) is the lookup hot path.
--     Composite because cross-tenant key reuse is a real risk
--     (clients rotating through UUID v4 have ~zero collision risk
--     but ops regressions are easier to spot when tenants can't
--     collide on a shared prefix).
--   * idx_idempotency_records_expires — CRON sweeper query.
--   * idx_idempotency_records_resource — operator drill-down
--     ("show me all cached writes for post X").
-- =========================================================================

CREATE TABLE IF NOT EXISTS idempotency_records (
    id              BIGSERIAL   PRIMARY KEY,
    workspace_id    BIGINT      NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    idempotency_key TEXT        NOT NULL,
    resource_type   TEXT        NOT NULL,
    resource_id     BIGINT      NOT NULL,
    request_hash    BYTEA       NOT NULL,
    response_status INT         NOT NULL,
    expires_at      TIMESTAMPTZ NOT NULL DEFAULT (NOW() + INTERVAL '24 hours'),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (workspace_id, idempotency_key)
);

CREATE INDEX IF NOT EXISTS idx_idempotency_records_expires
    ON idempotency_records(expires_at);

CREATE INDEX IF NOT EXISTS idx_idempotency_records_resource
    ON idempotency_records(resource_type, resource_id);
