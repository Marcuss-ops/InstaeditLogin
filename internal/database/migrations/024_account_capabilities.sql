-- Migration 024 -- account_capabilities (Taglio 5.0 LEVEL 2)
--
-- Dedicated cache table for real per-account capability limits
-- discovered by the per-platform CapabilityDiscoverer (Meta-family in
-- this commit; LinkedIn / TikTok / Twitter / YouTube in follow-ups).
--
-- Schema rationale:
--   - platform_account_id PK + FK ON DELETE CASCADE: one row per
--     account; deleting the account also drops its cached caps.
--   - theoretical JSONB: the matrix.For(platform) row at write time
--     (snapshot -- JSON may drift out of sync with
--     config/capabilities.json over time, but the snapshot keeps
--     diagnostics reproducible).
--   - actual JSONB NULLABLE: NULL while discovery is in flight OR for
--     platforms without a registered CapabilityDiscoverer (worker falls
--     back to theoretical in that case).
--   - effective = models.Intersect(theoretical, actual): precomputed
--     at write time so the worker reads a single column instead of
--     running AND/min/intersect on every publish tick.
--   - source_discoverer: which provider's DiscoverCapabilities method
--     produced this row ("instagram", "facebook", "threads", or
--     "theoretical-only" for non-Meta platforms).
--   - last_fetched_at: when DiscoverCapabilities last wrote this row.
--   - expires_at: TTL cutoff; worker treats rows past this as stale.
--   - last_error: non-null when Discovery failed (5xx, expired token).
--     Combined with expires_at: while TTL holds we trust the cached
--     row even with last_error set; after TTL we fall back to L1
--     theoretical. Operators can `?refresh=true` to re-attempt.
--   - revision: bumped by Upsert() ON CONFLICT DO UPDATE. Future L3
--     concurrent-write-proofer field; v1 trusts Postgres serialisation.
--
-- Worker integration:
--   - publishTarget step 6 reads effective via
--     router.PrePublishCheckWithEffective(ctx, account, payload, effective).
--   - HTTP GET /api/v1/accounts/{id}/capabilities reads effective+actual
--     for the dashboard / operability UI.

CREATE TABLE IF NOT EXISTS account_capabilities (
    platform_account_id BIGINT PRIMARY KEY REFERENCES platform_accounts(id) ON DELETE CASCADE,
    theoretical JSONB NOT NULL,
    actual JSONB,
    effective JSONB NOT NULL,
    source_discoverer TEXT NOT NULL,
    last_fetched_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL,
    last_error TEXT,
    revision INT NOT NULL DEFAULT 0
);

-- Index for expiration sweeps if we add a cron reaper later (L3).
CREATE INDEX IF NOT EXISTS idx_account_caps_expires_at ON account_capabilities(expires_at);
