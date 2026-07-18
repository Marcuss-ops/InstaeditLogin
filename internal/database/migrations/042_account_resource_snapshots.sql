-- 042: account_resource_snapshots
-- Caches remote resource data (channel stats, profile, branding) so the
-- frontend doesn't trigger a YouTube/Graph API call on every render.
-- The snapshot is refreshed on demand (POST /accounts/{id}/sync) or
-- when the stored data is older than 10 minutes.

CREATE TABLE IF NOT EXISTS account_resource_snapshots (
    platform_account_id BIGINT PRIMARY KEY
        REFERENCES platform_accounts(id) ON DELETE CASCADE,

    resource_type TEXT NOT NULL,
    profile       JSONB NOT NULL DEFAULT '{}'::jsonb,
    statistics    JSONB NOT NULL DEFAULT '{}'::jsonb,
    status        JSONB NOT NULL DEFAULT '{}'::jsonb,
    content       JSONB NOT NULL DEFAULT '{}'::jsonb,

    provider_etag TEXT,
    fetched_at    TIMESTAMPTZ NOT NULL,
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Fast lookup for the snapshot freshness check.
CREATE INDEX IF NOT EXISTS idx_account_resource_snapshots_fetched
    ON account_resource_snapshots (fetched_at);
