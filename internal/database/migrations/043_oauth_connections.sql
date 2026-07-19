-- =============================================================================
-- 043_oauth_connections.sql — P0: structural link between platform_accounts and
-- the OAuth grant rows that produced them.
-- =============================================================================
-- Why this migration exists:
--
-- Today every saved OAuth grant lives implicitly on every
-- platform_accounts row that was attached during the same callback.
-- The YouTube P0 handler fix (commit 501378a3) prevents the multi-channel
-- clone at the application layer, but defence in depth requires the
-- 1:1 (user, provider, resource) link to be a STRUCTURAL invariant.
-- Until this migration, any future regression in attachDiscoveredAccounts,
-- a manual INSERT, or a new provider path that skips the App-layer guard
-- could reintroduce the bug.
--
-- What this migration does:
--
--   1. Creates oauth_connections — one row per OAuth grant the user has
--      authorised. Columns mirror the eval doc spec:
--        user_id                  — owner (FK to users.id, ON DELETE CASCADE)
--        provider                 — "youtube" / "facebook" / ...
--        provider_subject_id      — the per-provider grant owner
--                                   (Google Account "sub", Meta user id, ...)
--        provider_resource_id     — the addressable resource the grant
--                                   targets (YouTube channel ID, Facebook
--                                   Page ID, the platform's own user ID
--                                   for single-account providers)
--        login_hint               — pre-select hint when re-authorising
--        status                   — active / expired / revoked / ...
--        scopes                   — granted scopes
--        expires_at               — when the access token expires
--        last_validated_at        — last channels.list(mine=true) hit
--        last_refresh_at          — last refresh-token exch
--        reauth_required_at       — when the next refresh failed permanently
--        created_at / updated_at  — bookkeeping
--      UNIQUE (user_id, provider, provider_resource_id) is the
--      schema-level "one grant per resource" guard.
--
--   2. Adds platform_accounts.oauth_connection_id as a nullable FK with
--      ON DELETE SET NULL — a revoke can drop the grant without orphaning
--      the historical attach row. The column is empty for any row
--      created BEFORE this migration; the backfill below fills it.
--
--   3. Backfills oauth_connections rows keyed on the existing
--      (user_id, platform, platform_user_id) tuple in platform_accounts.
--      provider_subject_id / scopes / expires_at are intentionally empty
--      — they'll be populated the next time handleLogin / handleCallback
--      reaches vault.Save with an oauth_connection lineage wiring (a
--      future commit hooks the repo + crypto vault to read/write the FK).
--      This is the "gradual migration" promised in the eval plan:
--      structural FK enforced today, dynamic lineage wiring in the next
--      P0 commits.
--
-- Idempotent: every DDL has IF NOT EXISTS, the ALTER is ADD COLUMN IF
-- NOT EXISTS, and the INSERT uses ON CONFLICT DO NOTHING on the
-- unique constraint so a second run on a migrated DB is a no-op. The
-- runner (internal/database/migrations.go::RunMigrations) executes
-- every .sql on every startup without a schema_migrations table, so
-- this migration MUST be idempotent on its own.
-- =============================================================================

CREATE TABLE IF NOT EXISTS oauth_connections (
    id                    BIGSERIAL PRIMARY KEY,
    user_id               BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider              VARCHAR(50) NOT NULL,
    provider_subject_id   TEXT NOT NULL DEFAULT '',
    provider_resource_id  TEXT NOT NULL,
    login_hint            TEXT,
    status                VARCHAR(32) NOT NULL DEFAULT 'active',
    scopes                TEXT[] NOT NULL DEFAULT '{}',
    expires_at            TIMESTAMPTZ,
    last_validated_at     TIMESTAMPTZ,
    last_refresh_at       TIMESTAMPTZ,
    reauth_required_at    TIMESTAMPTZ,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, provider, provider_resource_id)
);

-- Lookup index for "what grants does this user have?" and the per-
-- provider roster dashboard.
CREATE INDEX IF NOT EXISTS idx_oauth_connections_user_id_provider
    ON oauth_connections (user_id, provider);

-- Partial index for the future "needs reauth" dashboard widget.
CREATE INDEX IF NOT EXISTS idx_oauth_connections_reauth_required
    ON oauth_connections (reauth_required_at)
    WHERE reauth_required_at IS NOT NULL;

ALTER TABLE platform_accounts
    ADD COLUMN IF NOT EXISTS oauth_connection_id BIGINT
    REFERENCES oauth_connections(id) ON DELETE SET NULL;

-- Partial index so "list accounts by grant" is one index range scan.
-- The NULL filter matches the historical attach rows that pre-date
-- the migration and might (transiently) lack the FK; platform_accounts
-- created AFTER 043 always have it set.
CREATE INDEX IF NOT EXISTS idx_platform_accounts_oauth_connection_id
    ON platform_accounts (oauth_connection_id)
    WHERE oauth_connection_id IS NOT NULL;

-- Backfill: synthesise one oauth_connection per existing
-- platform_accounts row. ON CONFLICT DO NOTHING keeps a re-run of
-- the runner idempotent (the migration runner is invoked on every
-- boot with no schema_migrations table, so this is critical).
INSERT INTO oauth_connections (user_id, provider, provider_resource_id)
SELECT user_id, platform, platform_user_id
FROM platform_accounts pa
WHERE pa.platform_user_id IS NOT NULL
  AND pa.platform_user_id <> ''
ON CONFLICT (user_id, provider, provider_resource_id) DO NOTHING;

UPDATE platform_accounts pa
SET oauth_connection_id = oc.id
FROM oauth_connections oc
WHERE pa.oauth_connection_id IS NULL
  AND oc.user_id = pa.user_id
  AND oc.provider = pa.platform
  AND oc.provider_resource_id = pa.platform_user_id;
