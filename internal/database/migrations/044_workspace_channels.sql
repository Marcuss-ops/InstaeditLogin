-- =============================================================================
-- Migration 044: workspace_channels join table
-- =============================================================================
-- P0#4 — bind a platform_account (any provider) to one or more workspaces
-- under an optional group_name tag. Assignment of the 200 YouTube channels
-- to operator workspaces is the canonical use case, but the key-value shape
-- is generic so Facebook Page Pages, LinkedIn Pages, and TikTok accounts all
-- share the same plumbing.
--
-- Why a separate table (not a column on platform_accounts):
--   - One channel can belong to MANY workspaces (e.g. shared agency pool).
--   - Group_name is per-binding (group A in workspace X, group B in workspace Y).
--   - enabled is per-binding (a channel can be disabled in workspace Y but
--     live in workspace Z).
--
-- Foreign-key policy:
--   ON DELETE CASCADE on both columns — when a workspace is removed, its
--   bindings go with it; when a platform_account is removed, its bindings
--   go with it. Matches the cascade policy on workspace_members.api_keys /
--   session cleanup_tags / etc.
--
-- Per the runner (internal/database/migrations.go::RunMigrations) executes
-- every .sql on every startup without a schema_migrations table, this
-- migration MUST be idempotent on its own.
-- =============================================================================

CREATE TABLE IF NOT EXISTS workspace_channels (
    workspace_id        BIGINT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    platform_account_id BIGINT NOT NULL REFERENCES platform_accounts(id) ON DELETE CASCADE,
    group_name          TEXT,
    enabled             BOOLEAN NOT NULL DEFAULT TRUE,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (workspace_id, platform_account_id)
);

-- Partial index for "list channels belonging to a specific group within
-- a workspace" lookups. Excludes the NULL group_name rows (the no-group
-- case) so the index stays tiny even when most channels are ungrouped.
-- Composed on (workspace_id, group_name) so it can satisfy the lookup
-- + remain bounded under tenant-scoping plans.
CREATE INDEX IF NOT EXISTS idx_workspace_channels_group_name
    ON workspace_channels (workspace_id, group_name)
    WHERE group_name IS NOT NULL;

-- Composite index for "all channels belonging to a workspace, ordered
-- by created_at DESC". The PK already covers (workspace_id,
-- platform_account_id) but Postgres' planner cannot use that for
-- ORDER BY created_at queries without an explicit sort; this index
-- lets list queries stream directly.
CREATE INDEX IF NOT EXISTS idx_workspace_channels_workspace_created
    ON workspace_channels (workspace_id, created_at DESC);
