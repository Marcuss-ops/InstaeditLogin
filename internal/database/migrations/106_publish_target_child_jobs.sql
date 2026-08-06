-- Migration 106: per-target publication child-job queue
--
-- post_targets is the durable fan-out for one parent post. The queue fields
-- added by migrations 018/035 already provide the child-job contract:
-- status, attempt_count, next_attempt_at, lease_owner_id, leased_until,
-- error_message and provider_idempotency_key. This migration only adds the
-- indexes and documentation needed when the publish worker claims children
-- independently instead of processing a parent serially.

-- PostgreSQL partial-index predicates must be immutable; the due-time
-- predicate stays in the query and the index only narrows lifecycle states.
CREATE INDEX IF NOT EXISTS idx_post_targets_publish_child_due
    ON post_targets (next_attempt_at, post_id, id)
    WHERE status IN ('queued', 'waiting_provider', 'retrying');

CREATE INDEX IF NOT EXISTS idx_post_targets_publish_child_lease
    ON post_targets (leased_until, id)
    WHERE status = 'publishing' AND lease_owner_id IS NOT NULL;

COMMENT ON COLUMN post_targets.lease_owner_id IS
    'Publication child-job lease owner. Completion and retry writes must CAS on this value.';
COMMENT ON COLUMN post_targets.leased_until IS
    'Publication child-job lease expiry. Expired publishing children are reclaimable.';
COMMENT ON COLUMN post_targets.provider_idempotency_key IS
    'Stable per-parent/per-platform idempotency key reused by every child retry.';
