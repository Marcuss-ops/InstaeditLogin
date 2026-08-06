-- Migration 107: bounded/adaptive reconciler polling
--
-- Reconciler reads are now driven by the per-target due time instead of
-- scanning every publishing row on every tick. The attempt counter is used
-- by the worker to apply bounded adaptive backoff.

ALTER TABLE post_targets
    ADD COLUMN IF NOT EXISTS next_reconcile_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ADD COLUMN IF NOT EXISTS reconcile_attempt INT NOT NULL DEFAULT 0;

-- NOW() cannot appear in an index predicate because it is not immutable. Keep
-- the dynamic due-time predicate in the query and use the partial index for
-- the static publishing/readiness filters plus ordered due-time lookup.
CREATE INDEX IF NOT EXISTS idx_post_targets_reconcile_ready
    ON post_targets (next_reconcile_at, id)
    WHERE status = 'publishing'
      AND platform_post_id IS NOT NULL
      AND platform_post_id <> '';

CREATE OR REPLACE FUNCTION reset_reconcile_schedule_on_publish_start()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.status = 'publishing' AND OLD.status IS DISTINCT FROM NEW.status THEN
        -- A retry/reclaim is a new provider-polling lifecycle. Do not inherit
        -- a future schedule or attempt counter from the previous lifecycle.
        NEW.reconcile_attempt := 0;
        NEW.next_reconcile_at := NOW();
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS post_targets_reset_reconcile_schedule
    ON post_targets;

CREATE TRIGGER post_targets_reset_reconcile_schedule
    BEFORE UPDATE OF status ON post_targets
    FOR EACH ROW
    EXECUTE FUNCTION reset_reconcile_schedule_on_publish_start();

COMMENT ON COLUMN post_targets.next_reconcile_at IS
    'Earliest time the reconciler should poll this publishing target.';
COMMENT ON COLUMN post_targets.reconcile_attempt IS
    'Number of reconciler polls already scheduled for this publishing target.';
