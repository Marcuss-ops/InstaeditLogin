-- 017_api_keys.sql
-- Tenant API keys for machine-to-machine authentication (Taglio 5c).
--
-- api_keys is the database backing the SaaS-style API key auth flow.
-- Each key belongs to a workspace (workspace_id FK to workspaces).
--
-- Taglio 5c: tenant anchor is workspace_id (was organization_id). ProjectID removed
-- — projects are not part of the minimum tenant model.
--
-- Schema notes:
--   * workspace_id FK to workspaces (NOT NULL, ON DELETE CASCADE).
--   * created_by FK to users (NOT NULL, ON DELETE RESTRICT).
--   * environment (test|live): which prefix token the key carries.
--   * key_prefix: visible prefix for the dashboard / audit log (e.g. "sk_test_aB3xY9K2").
--   * key_hash: 32 bytes (SHA-256 output) of the FULL plaintext key. UNIQUE.
--   * permissions TEXT[]: list of capability strings granted by this key.
--   * expires_at: NULLABLE. NULL = never expires.
--   * revoked_at: NULLABLE. NULL = active.
--   * last_used_at: bumped on every successful middleware lookup.

CREATE TABLE IF NOT EXISTS api_keys (
    id              BIGSERIAL    PRIMARY KEY,
    workspace_id    BIGINT       NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    created_by      BIGINT       NOT NULL REFERENCES users(id)    ON DELETE RESTRICT,
    name            TEXT         NOT NULL,
    environment     TEXT         NOT NULL DEFAULT 'test',
    key_prefix      TEXT         NOT NULL,
    key_hash        BYTEA        NOT NULL UNIQUE,
    permissions     TEXT[]       NOT NULL DEFAULT '{}',
    expires_at      TIMESTAMPTZ,
    revoked_at      TIMESTAMPTZ,
    last_used_at    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

-- Workspace filter for "list keys of this workspace".
CREATE INDEX IF NOT EXISTS idx_api_keys_ws ON api_keys(workspace_id);

-- Hot-path lookup: middleware does SELECT WHERE key_hash = $1.
CREATE INDEX IF NOT EXISTS idx_api_keys_hash ON api_keys(key_hash);

-- Permission-list filter.
CREATE INDEX IF NOT EXISTS idx_api_keys_permissions ON api_keys USING GIN (permissions);

-- "Show me keys created by user X".
CREATE INDEX IF NOT EXISTS idx_api_keys_created_by ON api_keys(created_by);

-- "Show me expired-but-not-yet-revoked keys" cleanup sweep.
CREATE INDEX IF NOT EXISTS idx_api_keys_expires_active
    ON api_keys(expires_at)
    WHERE revoked_at IS NULL AND expires_at IS NOT NULL;
