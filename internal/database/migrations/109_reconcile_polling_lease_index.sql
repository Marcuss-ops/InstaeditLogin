-- Migration 109: optimize due reconciler polling with active-lease exclusion
--
-- next_reconcile_at <= NOW() and reconcile_until <= NOW() remain dynamic
-- query predicates; PostgreSQL partial-index predicates must be immutable.
-- The partial index therefore covers only static publishing/readiness and
-- currently-unleased rows, while the query evaluates due times at runtime.

CREATE INDEX IF NOT EXISTS idx_post_targets_reconcile_due_unleased
    ON post_targets (next_reconcile_at, id)
    WHERE status = 'publishing'
      AND platform_post_id IS NOT NULL
      AND platform_post_id <> ''
      AND reconcile_owner_id IS NULL;

COMMENT ON INDEX idx_post_targets_reconcile_due_unleased IS
    'Due-time reconciler index for publishing targets not currently leased; dynamic expiry stays in the query.';
