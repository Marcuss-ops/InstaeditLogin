-- 131_webhook_fanout_idempotency.sql
-- Makes the webhook fan-out re-insertion-safe (deferred follow-up promised
-- by internal/services/webhook_dispatcher.go and internal/repository/
-- webhook_repo.go).
--
-- Problem: webhook_events.event_id is the dedup anchor for the event log,
-- but a caller that re-emits the SAME event_id produced duplicate fan-out
-- rows in webhook_deliveries (one per re-insert per endpoint) because the
-- table had no uniqueness on (event_id, endpoint_id). Downstream consumers
-- then receive the same payload twice per endpoint.
--
-- Fix:
--   1. Collapse any existing duplicate (event_id, endpoint_id) rows,
--      keeping the LOWEST id per pair (the oldest attempt set — the row
--      the dispatcher has been retrying; dropping a retried row would
--      restart the backoff curve).
--   2. Create the UNIQUE index backing
--      INSERT ... ON CONFLICT (event_id, endpoint_id) DO NOTHING.
--
-- The index is created via CREATE UNIQUE INDEX (not ADD CONSTRAINT) so the
-- IF NOT EXISTS guard makes the migration idempotent, matching the
-- repo-wide migration style.

DELETE FROM webhook_deliveries a
 USING webhook_deliveries b
 WHERE a.event_id = b.event_id
   AND a.endpoint_id = b.endpoint_id
   AND a.id > b.id;

CREATE UNIQUE INDEX IF NOT EXISTS uq_webhook_deliveries_event_endpoint
    ON webhook_deliveries (event_id, endpoint_id);
