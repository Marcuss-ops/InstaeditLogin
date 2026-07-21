-- 061: account_metric_history
-- Stores a daily time-series of public (and future analytics) metrics
-- for each connected platform account. Populated whenever a snapshot is
-- refreshed (GET /accounts/{id} when stale, POST /accounts/{id}/sync).
-- One row per (platform_account_id, metric_date); upserts keep the
-- latest value for a given day.

CREATE TABLE IF NOT EXISTS account_metric_history (
    id BIGSERIAL PRIMARY KEY,
    platform_account_id BIGINT NOT NULL
        REFERENCES platform_accounts(id) ON DELETE CASCADE,
    metric_date DATE NOT NULL,

    -- Public metrics from channels.list statistics
    subscribers BIGINT NOT NULL DEFAULT 0,
    views       BIGINT NOT NULL DEFAULT 0,
    videos      BIGINT NOT NULL DEFAULT 0,

    -- Future YouTube Analytics metrics (nullable until scope is added)
    watch_time_minutes BIGINT,
    impressions        BIGINT,
    ctr                DOUBLE PRECISION,
    revenue_cents      BIGINT,
    rpm_cents          BIGINT,

    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    UNIQUE (platform_account_id, metric_date)
);

-- Fast lookups for the performance dashboard ranges.
CREATE INDEX IF NOT EXISTS idx_account_metric_history_account_date
    ON account_metric_history (platform_account_id, metric_date DESC);

CREATE INDEX IF NOT EXISTS idx_account_metric_history_date
    ON account_metric_history (metric_date DESC);
