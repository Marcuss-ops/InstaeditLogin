-- 102: account_resource_snapshots.refresh_pending_at
-- Strict rule: opening a channel page must NEVER trigger a provider
-- (YouTube) call. The read path serves the cached snapshot immediately
-- and stamps refresh_pending_at; the background worker refreshes the
-- snapshot asynchronously and clears the flag on upsert.

ALTER TABLE account_resource_snapshots
    ADD COLUMN IF NOT EXISTS refresh_pending_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS refresh_claimed_until TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS refresh_attempts INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS refresh_last_error TEXT;

-- Worker lookup: pending refreshes (non-NULL flag).
CREATE INDEX IF NOT EXISTS idx_account_resource_snapshots_refresh_pending
    ON account_resource_snapshots (refresh_pending_at)
    WHERE refresh_pending_at IS NOT NULL;
