-- =============================================================================
-- 058_fleet_readiness_snapshots.sql — Definition of Done snapshot audit
-- =============================================================================
-- Why this migration exists:
--
-- The new GET /admin/youtube/fleet_readiness endpoint takes an
-- append-only snapshot of the YouTube fleet's Definition of Done state
-- (per docs/OAUTH-PRODUCTION.md 2026 fleet-readiness checklist):
--
--   * youtube_channels_total / active / pending_authorization /
--     reauth_required / revoked / error
--   * refresh_test_ok / scope_youtube_upload_ok / scope_youtube_readonly_ok /
--     channel_binding_ok / private_canary_ok / canary_channel_match_ok
--
-- The aggregate counts are returned in the JSON response body. The
-- per-channel record (platform_account_id, channel_id UC…,
-- channel_name, manager_email_hint, oauth_connection_id,
-- granted_scopes, last_refresh_at, last_binding_check_at,
-- canary_video_id, canary_result, last_error_code) is persisted to
-- the child table so operators can diff snapshots over time and
-- audit which specific channel flipped from "ok" to "ok" between
-- two adjacent snapshots.
--
-- Schema decisions:
--
--   - Parent table has UUID PK (`gen_random_uuid()` requires
--     Postgres 13+) so per-call snapshots are globally unique
--     without needing a monotonic bigserial + dedicated sequence.
--   - summary_json JSONB column mirrors the aggregate counts so a
--     snapshot can be replayed without recomputing it (handy for
--     diff dashboards that don't want to re-run the COUNT(*)
--     FILTER aggregation).
--   - Child table cascades on parent delete (snapshot rolled back
--     -> rows clean up automatically). UNIQUE on
--     (snapshot_id, platform_account_id) prevents accidental
--     double-row insertion if the parent/child INSERT cycle is
--     ever retried inside the same transaction.
--   - granted_scopes is TEXT[] (matches oauth_connections.scopes
--     column type per migration 043) so we can grep both for free;
--     the per-channel INSERT reads scopes from
--     platform_accounts.metadata->'granted_scopes' (the JSONB
--     snapshot operator-side stores post-bind) and casts to TEXT[];
--     OAuth conn-listings without metadata write to [].
--
-- Idempotency / order-independence:
--   Mirrors the canonical migration_runner contract: runner applies
--   every file via embed.FS against a fresh DB; no
--   schema_migrations table; every DDL block must be IF NOT EXISTS
--   -guarded for replay safety.
-- =============================================================================

CREATE TABLE IF NOT EXISTS fleet_readiness_snapshots (
    id                UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    taken_at          TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    -- operator_user_id is the admin who triggered the snapshot --
    -- stamped from r.adminIdentityUserID(req) at handler time.
    -- NOT a FK to users(id) to avoid a migration-order coupling
    -- with migration 003_users; admins can be deleted and we'd lose
    -- audit history otherwise.
    operator_user_id  BIGINT       NOT NULL,
    -- Aggregated Definition-of-Done counts, mirrored from the
    -- JSON response body. Lets dashboards replay a snapshot
    -- without re-running the FILTER aggregation.
    summary_json      JSONB        NOT NULL
);

-- Snapshot rows are immutable once written (no UPDATE in the
-- handler). taken_at is the natural ordering handle for the
-- "latest snapshot" lookup.
CREATE INDEX IF NOT EXISTS idx_fleet_readiness_snapshots_taken_at
    ON fleet_readiness_snapshots (taken_at DESC);

CREATE TABLE IF NOT EXISTS fleet_readiness_snapshot_channels (
    id                     BIGSERIAL    PRIMARY KEY,
    snapshot_id            UUID         NOT NULL
        REFERENCES fleet_readiness_snapshots(id) ON DELETE CASCADE,
    platform_account_id    BIGINT       NOT NULL,
    -- channel_id is the YouTube UC… id. Stored as varchar so the
    -- snapshot row survives even if the channel is later detached
    -- from platform_accounts (audit-pinning pattern).
    channel_id             VARCHAR(64)  NOT NULL DEFAULT '',
    channel_name           VARCHAR(255) NOT NULL DEFAULT '',
    manager_email          VARCHAR(255) NOT NULL DEFAULT '',
    -- Nullable: rows pre-migration 043 (pre-grant-lineage attach)
    -- have oauth_connection_id = NULL on platform_accounts; the
    -- fleet readiness query LEFT JOINs and inserts NULL here.
    oauth_connection_id    BIGINT,
    granted_scopes         TEXT[]       NOT NULL DEFAULT ARRAY[]::TEXT[],
    last_refresh_at        TIMESTAMPTZ,
    -- last_binding_check_at is the binding-check freshness
    -- surrogate. Pre-migration 052 the column was last_validated_at;
    -- we keep the binding-check alias name in the snapshot so
    -- operators grep one term consistently.
    last_binding_check_at  TIMESTAMPTZ,
    canary_video_id        VARCHAR(64)  NOT NULL DEFAULT '',
    canary_result          VARCHAR(64)  NOT NULL DEFAULT '',
    last_error_code        VARCHAR(64)  NOT NULL DEFAULT '',
    UNIQUE (snapshot_id, platform_account_id)
);

CREATE INDEX IF NOT EXISTS idx_fleet_read_snap_channels_snapshot_id
    ON fleet_readiness_snapshot_channels (snapshot_id);

-- Per-platform-account audit: the latest snapshot for a given
-- channel can be located with this index alone. Used by the future
-- /admin/youtube/fleet_readiness/{channel_id}/history endpoint
-- (TBV — committed now so future queries don't have to scan the
-- table; the index is cheap).
CREATE INDEX IF NOT EXISTS idx_fleet_read_snap_channels_pa
    ON fleet_readiness_snapshot_channels (platform_account_id, snapshot_id DESC);

COMMENT ON TABLE  fleet_readiness_snapshots IS
    'Definition-of-Done snapshot parent table. One row per /admin/youtube/fleet_readiness call. Append-only audit history: see id UUID + taken_at; cascade-delete of channels.';
COMMENT ON COLUMN fleet_readiness_snapshots.summary_json IS
    'Mirrors the JSON FleetReadinessCounts struct (12 fields per docs/OAUTH-PRODUCTION.md DoD checklist). Stored JSONB so dashboards can replay without re-running the COUNT(*) FILTER aggregation.';
COMMENT ON TABLE  fleet_readiness_snapshot_channels IS
    'One row per YouTube platform_account per snapshot. Captures the per-channel Definition-of-Done state at the moment the snapshot was taken. CASCADE on snapshot_id delete.';
COMMENT ON COLUMN fleet_readiness_snapshot_channels.granted_scopes IS
    'TEXT[] of all OAuth scopes granted to this channel at snapshot time. Sourced from platform_accounts.metadata->''granted_scopes'' (JSONB array, post-bind). Empty array for channels without metadata write.';
COMMENT ON COLUMN fleet_readiness_snapshot_channels.last_binding_check_at IS
    'Last YouTube/OAuth channel-binding check (channels.list?mine=true validation against platform_accounts.platform_user_id). Sourced from platform_accounts.last_validated_at. NULL means the binding check has not yet been performed for this channel.';
