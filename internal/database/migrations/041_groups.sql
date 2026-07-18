-- Migration 041 — Groups (hierarchical account folders per workspace).
--
-- Adds:
--   * groups (id, workspace_id, parent_group_id NULLABLE, name)
--     — self-referencing so users can nest groups under each other
--       (YouTube-style accounts → "My Brand" → "TikTok Brand" → ...)
--   * group_accounts (group_id, account_id)
--     — many-to-many join between groups and platform_accounts.
--       DECLARED not FK-enforced on account_id because
--       platform_accounts.user_id is the auth scope (not workspace);
--       the repository layer enforces "account belongs to a user who
--       owns the workspace" pre-insert.
--
-- Cycle prevention is enforced by the repository layer (ancestor
-- walk on Update/SetParent). The schema doesn't add a SQL-level
-- recursive CHECK because Postgres has no native cycle prevention
-- in self-referencing FKs — the app layer is the canonical guard.
--
-- The (workspace_id, name) UNIQUE prevents two root groups with the
-- same name; sub-groups can still share names because the unique
-- constraint sits at (workspace_id, parent_group_id NULL, name)
-- (Postgres NULLS NOT DISTINCT semantics — see CREATE INDEX below).
--
-- Audit timestamps (created_at, updated_at) keep the model
-- consistent with workspace_member patterns (TAGLIO 028).

CREATE TABLE IF NOT EXISTS groups (
    id              BIGSERIAL PRIMARY KEY,
    workspace_id    BIGINT      NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    parent_group_id BIGINT      REFERENCES groups(id) ON DELETE CASCADE,
    name            TEXT        NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_groups_workspace ON groups(workspace_id);
CREATE INDEX IF NOT EXISTS idx_groups_parent    ON groups(parent_group_id);

-- Partial unique for root groups (parent IS NULL): same name + same
-- workspace is rejected. Sub-groups (parent IS NOT NULL) reuse the
-- composite uniqueness via the runtime check; we do NOT add a
-- composite UNIQUE here because Postgres NULL handling differs
-- between modes across deployments.
CREATE UNIQUE INDEX IF NOT EXISTS uniq_groups_workspace_root_name
    ON groups(workspace_id, name)
    WHERE parent_group_id IS NULL;

CREATE TABLE IF NOT EXISTS group_accounts (
    group_id    BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    account_id  BIGINT NOT NULL REFERENCES platform_accounts(id) ON DELETE CASCADE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (group_id, account_id)
);

CREATE INDEX IF NOT EXISTS idx_group_accounts_account ON group_accounts(account_id);
