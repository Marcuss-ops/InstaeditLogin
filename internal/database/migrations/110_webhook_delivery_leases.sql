-- Migration 110: durable webhook delivery leases.
--
-- Claiming a delivery must not overload scheduled_at as a five-second
-- pseudo-lease. lease_id fences each owner, lease_until enables crash
-- recovery, and heartbeat_at records liveness.

ALTER TABLE webhook_deliveries
    ADD COLUMN IF NOT EXISTS lease_id UUID,
    ADD COLUMN IF NOT EXISTS lease_until TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS heartbeat_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_webhook_deliveries_lease_due
    ON webhook_deliveries (scheduled_at, lease_until, id)
    WHERE status = 'pending';

COMMENT ON COLUMN webhook_deliveries.lease_id IS
    'UUID fencing token for the worker currently delivering this row.';
COMMENT ON COLUMN webhook_deliveries.lease_until IS
    'Durable delivery lease expiry; expired pending rows are reclaimable.';
COMMENT ON COLUMN webhook_deliveries.heartbeat_at IS
    'Last successful lease heartbeat from the current delivery owner.';
