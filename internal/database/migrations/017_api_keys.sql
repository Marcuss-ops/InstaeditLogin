-- 017_api_keys.sql
-- Zernio Milestone 4: tenant API keys for machine-to-machine authentication.
--
-- api_keys is the database backing the SaaS-style API key auth flow:
-- clients (server-to-server scripts, CI jobs, partner integrations)
-- POST a name + permissions (and optionally project scope) and receive
-- ONE plaintext key in the response body. The plaintext is shown ONCE;
-- we keep only key_prefix (for display) + key_hash (SHA-256, for lookup)
-- in the database. Rotation issues a fresh key with the same metadata
-- and revokes the previous one in a single transaction.
--
-- Schema notes (mirrors the conventions of 013_organizations.sql):
--
--   * organization_id FK to organizations (NOT NULL, ON DELETE CASCADE):
--     a key belongs to a tenant. When the tenant is deleted, all its
--     keys die with it — there is no platform-wide "global key".
--   * project_id (nullable, BIGINT, no FK here): an api_key may be
--     scoped to a single project inside the org OR be org-wide (NULL).
--     Multi-tenancy backfill migrations 014 (projects) and 015
--     (organization_members) will land before any code path actually
--     writes through the FK. The BIGINT column + idx_api_keys_project
--     is enough for now; add REFERENCES projects(id) ON DELETE CASCADE
--     in a follow-up migration once 014 lands.
--   * created_by FK to users (NOT NULL, ON DELETE RESTRICT): the user
--     who minted the key. ON DELETE RESTRICT prevents losing the audit
--     trail if the creator account is purged — the SQL will refuse to
--     delete a user with outstanding API keys. Operators must revoke
--     keys first, then delete users.
--   * environment (test|live): which prefix token the key carries.
--     Stored so we can surface it back on GET /api-keys without
--     re-parsing the (no-longer-stored) plaintext.
--   * key_prefix: visible prefix for the dashboard / audit log
--     (e.g. "sk_test_aB3xY9K2"). Long enough to be unique in the UI,
--     short enough not to leak enough entropy to brute-force the rest.
--   * key_hash: 32 bytes (SHA-256 output) of the FULL plaintext key.
--     UNIQUE so two keys can never share a hash (the bf-crypto/rand
--     secret gives 190+ bits of entropy — collision is astronomically
--     unlikely anyway, but UNIQUE makes the invariant explicit).
--     The middleware's hot path is `SELECT WHERE key_hash = $1` against
--     this UNIQUE column — fast index lookup, no scanning.
--   * permissions TEXT[]: list of capability strings granted by this
--     key. The middleware rejects calls whose required permission is
--     not in this set. Defaults to '{}' so a key without explicit
--     permissions is a no-op (fail-closed). GIN index supporting
--     `permissions @> ARRAY['publish']` lookups for future filtering.
--   * expires_at: NULLABLE. NULL = never expires. We still log a
--     warning when a key has been active for >N days so operators can
--     rotate proactively; the Beltsky-equivalent "long-lived without
--     rotation" alert is a future commit.
--   * revoked_at: NULLABLE. NULL = active. Set to NOW() when the
--     DELETE or /rotate endpoint flips the key. The middleware rejects
--     any key with revoked_at IS NOT NULL — looking at lastUsedAt is
--     OK post-revocation but it can't authenticate.
--   * last_used_at: bumped on every successful middleware lookup.
--     Operators use this to evict dormant keys; the cleanup sweep is
--     a future commit (no cron job lands in this migration).
--
-- Update cadence:
--   * updated_at is bumped by the repository layer on every change.
--     No Postgres trigger — application-layer timestamps keep the
--     trigger surface area flat.
--
-- Multi-tenancy future work:
--   * This migration is laid out so that 014 (projects),
--     015 (organization_members), and 016 (platform_tenant_filters)
--     can land later WITHOUT touching api_keys. organization_id and
--     created_by already have FKs; the remaining nullable project_id
--     and the api_keys-via-platform-accounts fanout can be wired up
--     in those subsequent migrations.

CREATE TABLE IF NOT EXISTS api_keys (
    id              BIGSERIAL    PRIMARY KEY,
    organization_id BIGINT       NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    project_id      BIGINT,                                  -- nullable; FK deferred to migration that introduces projects table
    created_by      BIGINT       NOT NULL REFERENCES users(id)    ON DELETE RESTRICT,
    name            TEXT         NOT NULL,
    environment     TEXT         NOT NULL DEFAULT 'test',          -- test|live
    key_prefix      TEXT         NOT NULL,                         -- e.g. "sk_test_aB3xY9K2"
    key_hash        BYTEA        NOT NULL UNIQUE,                  -- SHA-256 of the full key (32 bytes)
    permissions     TEXT[]       NOT NULL DEFAULT '{}',
    expires_at      TIMESTAMPTZ,
    revoked_at      TIMESTAMPTZ,
    last_used_at    TIMESTAMPTZ,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

-- Tenant filter for "list keys of this org". The two common queries —
-- org-wide and project-wide — both land on this index.
CREATE INDEX IF NOT EXISTS idx_api_keys_org ON api_keys(organization_id);

-- Hot-path lookup: middleware does `SELECT ... WHERE key_hash = $1`.
-- UNIQUE already creates a btree on key_hash, but the explicit index
-- name documents intent for the operator reading pg_indexes.
CREATE INDEX IF NOT EXISTS idx_api_keys_hash ON api_keys(key_hash);

-- Permission-list filter (e.g. "show all keys that can publish"). GIN
-- supports @> containment checks. Without this, list-by-permission
-- would be a seq scan of the org's api_keys.
CREATE INDEX IF NOT EXISTS idx_api_keys_permissions ON api_keys USING GIN (permissions);

-- "Show me keys created by user X" — common for the audit log
-- (revoked-keys-by-user reports).
CREATE INDEX IF NOT EXISTS idx_api_keys_created_by ON api_keys(created_by);

-- "Show me expired-but-not-yet-revoked keys" cleanup sweep.
-- Partial index: only rows where expires_at IS NOT NULL. Saves space
-- when most keys never expire.
CREATE INDEX IF NOT EXISTS idx_api_keys_expires_active
    ON api_keys(expires_at)
    WHERE revoked_at IS NULL AND expires_at IS NOT NULL;
