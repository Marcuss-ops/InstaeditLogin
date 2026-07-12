-- =============================================================================
-- 028_multi_tenancy.sql — FASE 2.1: Multi-tenant SaaS data model
-- =============================================================================
-- 
-- This migration restructures the data model to support multi-tenant SaaS:
--   1. Adds SaaS credential columns to users (password_hash, email_verified)
--   2. Creates user_oauth_profiles table (social account linkage by platform)
--   3. Creates workspace_member_role enum + workspace_members table (team)
--   4. Creates workspace_invites table (pending invitations)
--   5. Adds workspace_id FK to platform_accounts (social accounts → workspace)
--   6. Backfills existing workspace owners as admin members
--
-- Idempotent: all DDL uses IF NOT EXISTS / DO-block guards.
-- Non-destructive: existing columns are NOT dropped, FKs are nullable.
-- =============================================================================

-- 1. SaaS credentials on users ------------------------------------------------

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS password_hash BYTEA,
    ADD COLUMN IF NOT EXISTS email_verified BOOLEAN NOT NULL DEFAULT FALSE;

-- 2. User OAuth profiles (social account linkage per user+platform) -----------

CREATE TABLE IF NOT EXISTS user_oauth_profiles (
    id               BIGSERIAL PRIMARY KEY,
    user_id          BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    platform         VARCHAR(32) NOT NULL,
    platform_user_id VARCHAR(255) NOT NULL,
    username         VARCHAR(255),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(user_id, platform, platform_user_id)
);

CREATE INDEX IF NOT EXISTS idx_user_oauth_profiles_user_id ON user_oauth_profiles(user_id);
CREATE INDEX IF NOT EXISTS idx_user_oauth_profiles_platform ON user_oauth_profiles(platform);

-- Backfill existing platform_accounts into user_oauth_profiles.
INSERT INTO user_oauth_profiles (user_id, platform, platform_user_id, username)
SELECT pa.user_id, pa.platform, pa.platform_user_id, pa.username
FROM platform_accounts pa
WHERE NOT EXISTS (
    SELECT 1 FROM user_oauth_profiles uop
    WHERE uop.user_id = pa.user_id AND uop.platform = pa.platform
);

-- 3. Workspace member role enum -----------------------------------------------

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'workspace_member_role') THEN
        CREATE TYPE workspace_member_role AS ENUM ('admin', 'editor', 'viewer');
    END IF;
END$$;

-- 4. Workspace members --------------------------------------------------------

CREATE TABLE IF NOT EXISTS workspace_members (
    id           BIGSERIAL PRIMARY KEY,
    workspace_id BIGINT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    user_id      BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role         workspace_member_role NOT NULL DEFAULT 'editor',
    joined_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(workspace_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_workspace_members_workspace_id ON workspace_members(workspace_id);
CREATE INDEX IF NOT EXISTS idx_workspace_members_user_id ON workspace_members(user_id);

-- 5. Workspace invites --------------------------------------------------------

CREATE TABLE IF NOT EXISTS workspace_invites (
    id           BIGSERIAL PRIMARY KEY,
    workspace_id BIGINT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    email        VARCHAR(255) NOT NULL,
    role         workspace_member_role NOT NULL DEFAULT 'editor',
    token        VARCHAR(128) NOT NULL UNIQUE,
    invited_by   BIGINT REFERENCES users(id) ON DELETE SET NULL,
    expires_at   TIMESTAMPTZ NOT NULL,
    accepted_at  TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Partial unique index: only one pending invite per (workspace, email).
-- After acceptance (accepted_at NOT NULL), a new invite for the same email
-- is allowed — the old row has accepted_at set, so it doesn't match the WHERE.
CREATE UNIQUE INDEX IF NOT EXISTS idx_workspace_invites_pending
    ON workspace_invites(workspace_id, email) WHERE accepted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_workspace_invites_token ON workspace_invites(token);
CREATE INDEX IF NOT EXISTS idx_workspace_invites_workspace_id ON workspace_invites(workspace_id);

-- 6. Platform accounts → workspace FK -----------------------------------------

ALTER TABLE platform_accounts
    ADD COLUMN IF NOT EXISTS workspace_id BIGINT REFERENCES workspaces(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_platform_accounts_workspace_id ON platform_accounts(workspace_id);

-- 7. Backfill: existing workspace owners → workspace_members (admin) ----------

INSERT INTO workspace_members (workspace_id, user_id, role, joined_at)
SELECT w.id, w.owner_id, 'admin'::workspace_member_role, w.created_at
FROM workspaces w
WHERE NOT EXISTS (
    SELECT 1 FROM workspace_members wm
    WHERE wm.workspace_id = w.id AND wm.user_id = w.owner_id
);
