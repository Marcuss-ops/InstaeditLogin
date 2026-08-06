-- Migration 108: durable reconciler target leases
--
-- The publication lease columns from migration 035 belong to the upload
-- worker. Reconciliation has a separate lifecycle: it must hold ownership
-- across the provider status request without holding a PostgreSQL row lock.

ALTER TABLE post_targets
    ADD COLUMN IF NOT EXISTS reconcile_owner_id      TEXT,
    ADD COLUMN IF NOT EXISTS reconcile_until         TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS reconcile_heartbeat_at  TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_post_targets_reconcile_lease
    ON post_targets (reconcile_until, id)
    WHERE status = 'publishing';

COMMENT ON COLUMN post_targets.reconcile_owner_id IS
    'Durable owner of the current reconciler provider poll lease.';
COMMENT ON COLUMN post_targets.reconcile_until IS
    'Expiry of the current reconciler provider poll lease; expired rows are reclaimable.';
COMMENT ON COLUMN post_targets.reconcile_heartbeat_at IS
    'Last successful CAS heartbeat extending the reconciler lease.';

-- A target entering a new publishing lifecycle must not inherit a stale
-- owner, expiry, heartbeat, or adaptive retry counter from an earlier one.
CREATE OR REPLACE FUNCTION reset_reconcile_schedule_on_publish_start()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.status = 'publishing' AND OLD.status IS DISTINCT FROM NEW.status THEN
        NEW.reconcile_attempt := 0;
        NEW.next_reconcile_at := NOW();
        NEW.reconcile_owner_id := NULL;
        NEW.reconcile_until := NULL;
        NEW.reconcile_heartbeat_at := NULL;
    END IF;
    RETURN NEW;
END;
$$;

-- Migration 107 already installs the canonical trigger for this function.
-- Recreate it here so applying migration 108 upgrades older installations
-- and leaves exactly one trigger after either migration path.
DROP TRIGGER IF EXISTS post_targets_reset_reconcile_schedule
    ON post_targets;

CREATE TRIGGER post_targets_reset_reconcile_schedule
    BEFORE UPDATE OF status ON post_targets
    FOR EACH ROW
    EXECUTE FUNCTION reset_reconcile_schedule_on_publish_start();
