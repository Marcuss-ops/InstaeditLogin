-- 013_organizations.sql
-- Zernio Milestone 4: multi-tenancy foundation.
--
-- Organizations are the top-level tenant container per Zernio's
-- hierarchy: Organization > Project > Social Account > Post > Target.
-- Each user-account-session scope lives inside ONE organization for
-- tenant isolation; cross-tenant data access is enforced at the query
-- layer (AND organization_id = $X) — see migration
-- 016_platform_tenant_filters.sql where the tenant columns are wired
-- onto platform_accounts.
--
-- slug is UNIQUE so the dashboard URLs can resolve orgs by a
-- human-readable key (e.g. /o/acme-corp/) without exposing internal
-- numeric ids. /o/{slug}/... is the canonical org-scoped UI path.
--
-- plan is the future-billing anchor: free / starter / pro / enterprise
-- / custom. Per Zernio's SaaS pricing model. Default 'free' so a fresh
-- org does not require plan selection at signup.
--
-- status tracks the org lifecycle (active, suspended, archived).
-- Suspension: login blocked, but data preserved for reactivation.
-- Archival: read-only, eventually purged after retention period.
--
-- updated_at is bumped by Update() (repository layer); we don't use a
-- Postgres trigger here because the application owns the column.
--
-- All statements are idempotent so ReMigrations on an already-migrated
-- database is a no-op (re-runs land on the same DDL but skip cells
-- that already exist).

CREATE TABLE IF NOT EXISTS organizations (
    id         BIGSERIAL   PRIMARY KEY,
    name       TEXT        NOT NULL,
    slug       TEXT        NOT NULL UNIQUE,
    status     TEXT        NOT NULL DEFAULT 'active',  -- active|suspended|archived
    plan       TEXT        NOT NULL DEFAULT 'free',    -- free|starter|pro|enterprise|custom
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- created_at DESC index for "recent orgs" listings on the operator-level
-- dashboard (superadmin view). Tenant-scoped UIs (org members, project
-- lists) do NOT query this index — they query the FK index on the
-- child table (e.g. projects.organization_id).
CREATE INDEX IF NOT EXISTS idx_organizations_created_at ON organizations(created_at DESC);
