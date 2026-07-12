-- =============================================================================
-- 030_connection_states.sql — SPRINT 1.2: per-platform connection state.
-- =============================================================================
-- Replaces the legacy oauth_state_* HttpOnly cookie pattern with a
-- Postgres-backed state store. The connection flow:
--
--   POST /api/v1/connections/{platform}/start (JWT-required)
--     → INSERT connection_states row (user_id, workspace_id, platform,
--       nonce, expires_at=now+15min)
--     → set HttpOnly cookie `connection_state_<id>` carrying
--       `<id>.<nonce>` for the browser to bring back at callback
--     → respond JSON { connection_id, authorize_url }
--
--   GET /api/v1/connections/{platform}/callback?state=<base64(id:nonce)>&code=...
--     → parse id+nonce from base64 state
--     → require HttpOnly cookie `connection_state_<id>` to match
--     → UPDATE connection_states.consumed_at = NOW() (one-time use)
--     → call provider.HandleCallback → UPSERT platform_accounts,
--       UPSERT user_oauth_profiles
--     → vault.Save token
--
-- Workspace isolation invariant: callback rejects if JWT's active
-- workspace_id != connection_states.workspace_id. For SPRINT 1.2 the
-- JWT comes from a product session cookie minted at magic-link verify;
-- handler compares it to the pre-stamped workspace_id before claiming
-- the state row.
--
-- Idempotent: all DDL uses IF NOT EXISTS guards.
-- =============================================================================

CREATE TABLE IF NOT EXISTS connection_states (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    workspace_id BIGINT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    platform     VARCHAR(32) NOT NULL,
    nonce        VARCHAR(64) NOT NULL,
    scopes       TEXT[],
    redirect_uri TEXT,
    expires_at   TIMESTAMPTZ NOT NULL DEFAULT NOW() + INTERVAL '15 minutes',
    consumed_at  TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_connection_states_user_workspace_platform
    ON connection_states(user_id, workspace_id, platform);

CREATE INDEX IF NOT EXISTS idx_connection_states_consumed
    ON connection_states(consumed_at) WHERE consumed_at IS NOT NULL;

-- SPRINT 1.2: workspace-scoped uniqueness on platform_accounts.
-- Existing migration only enforces UNIQUE(user_id, platform, platform_user_id)
-- on user_oauth_profiles. A single social-account (same platform_user_id)
-- can legitimately appear in MANY workspaces if the same social user is
-- imported into different teams. Adding a workspace-scoped uniqueness on
-- platform_accounts prevents the same workspace linking the same social id
-- twice (which would otherwise share tokens — BOLA).
ALTER TABLE platform_accounts
    ADD CONSTRAINT IF NOT EXISTS platform_accounts_ws_platform_puid_uniq
    UNIQUE (workspace_id, platform, platform_user_id);
