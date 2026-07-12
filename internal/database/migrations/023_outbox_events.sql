-- Migration 023 — outbox_events table (Taglio 5.0 transactional outbox)
--
-- Pattern: a side-car event log written atomically inside the same
-- transaction that mutates the aggregate (here: posts + post_targets).
-- A separate dispatcher goroutine reads rows whose lease has expired
-- (or has never been acquired), processes them, and clears the lease.
-- At-least-once delivery: the dispatcher's idempotency guarantee comes
-- from UNIQUE(publish_jobs.outbox_event_id) — a re-dispatch is
-- absorbed by a 23505 dispatch path on the way to the publish_jobs
-- insert.
--
-- The dispatcher lease (lease_id, lease_until) is what an external
-- goroutine uses to claim a row atomically without LONG-running
-- postgres locks (advisory_xact_lock would force the work into a tx).
-- The lease is a CAS-style column update:
--
--   UPDATE outbox_events
--   SET lease_id = $1, lease_until = now() + ($2)::interval
--   WHERE id = (
--     SELECT id FROM outbox_events
--     WHERE status = 'pending'
--       AND (lease_until IS NULL OR lease_until < now())
--       AND (next_attempt_at IS NULL OR next_attempt_at <= now())
--     ORDER BY next_attempt_at NULLS FIRST, created_at ASC
--     FOR UPDATE SKIP LOCKED
--     LIMIT 1
--   )
--   RETURNING *;
--
-- This is the canonical Postgres queue-table pattern (see
-- SELECT FOR UPDATE SKIP LOCKED docs) plus the CAS lease column for
-- crash-recovery across multi-process dispatchers.
--
-- Lifecycle of one outbox row:
--   1. Inserted by PostRepository.CreateWithOutbox in same tx as the
--      post+target writes (status='pending', lease NULL).
--   2. Dispatcher claims (lease set, lease_until = now() + TTL) and
--      starts the heartbeat goroutine.
--   3. On success: UPDATE status='processed', processed_at=now().
--   4. On failure (transient): UPDATE attempt_count++, last_error,
--      next_attempt_at = now() + decorrelated_jitter.
--   5. After max_attempts: UPDATE status='dead_letter' (DLQ).
--
-- The dispatcher's heartbeat renews lease_until every 20s so a slow
-- dispatch (5-30s typical) doesn't lose its claim to a peer that
-- picks up the same row at lease expiry.
--
-- Idempotency on retry: a "processed" row that the dispatcher
-- re-processes (e.g. after heartbeat race) is caught by
-- UNIQUE(publish_jobs.outbox_event_id) — the second INSERT into
-- publish_jobs returns 23505 which the repository translates to a
-- no-op (the event is already fully processed).
--
-- Failure injectability: status='pending' + a non-null last_error +
-- attempt_count = N surfaces as a stuck row that operators can
-- investigate via SELECT * FROM outbox_events WHERE status='pending'
-- ORDER BY created_at. Rerun via UPDATE outbox_events SET
-- next_attempt_at=NULL WHERE id=$1.

CREATE TABLE IF NOT EXISTS outbox_events (
    id BIGSERIAL PRIMARY KEY,

    -- Aggregate identification. Generic on purpose — future events
    -- for non-post-target aggregates (workspace_member_invited,
    -- api_key_rotated, audit_log_emitted, etc.) reuse the same table
    -- with their own aggregate_type value. The dispatcher dispatches
    -- by aggregate_type + event_type.
    aggregate_type VARCHAR(50) NOT NULL,
    aggregate_id   BIGINT       NOT NULL,

    -- Event semantics. Examples: 'post_target.created', 'post.cancelled',
    -- 'workspace.member.invited'. The dispatcher routes based on this.
    event_type VARCHAR(50) NOT NULL,

    -- JSON payload — opaque to the outbox table itself; the consumer
    -- (dispatcher) parses it based on event_type. Storing as JSONB
    -- keeps the schema flexible for future event variants without
    -- migrations.
    payload JSONB NOT NULL,

    -- pending → processed OR dead_letter (terminal). The dispatcher
    -- never transitions out of 'processed' or 'dead_letter' except
    -- via manual operator SQL (which is the deliberate design choice
    -- for DLQ).
    status VARCHAR(20) NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'processed', 'dead_letter')),

    -- Lease: ClaimNext sets these; heartbeat renews lease_until;
    -- MarkProcessed / MarkFailed / MarkDeadLetter clears them.
    -- UUID lease_id so multi-process dispatchers can't accidentally
    -- steal each other's in-flight rows (their `SELECT FOR UPDATE
    -- SKIP LOCKED LIMIT 1` claim picks an UNCLAIMED row because the
    -- WHERE clause excludes active leases).
    lease_id     UUID,
    lease_until  TIMESTAMPTZ,

    -- Retry bookkeeping. attempt_count = 0 on first attempt,
    -- increments on each MarkFailed. next_attempt_at is NULL while
    -- the row is actively being processed (between Claim and
    -- MarkProcessed/MarkFailed) — a non-null future timestamp means
    -- "we tried but failed; try again at this time". The dispatcher's
    -- claim query excludes rows where next_attempt_at > now(), so
    -- failed rows are auto-skipped until their backoff window opens.
    attempt_count  INT          NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ,

    -- Last error captured for debugging. Surface these in dashboards
    -- (outbox.last_error IS NOT NULL is a "needs operator attention"
    -- view). Clear on retry if the error no longer applies.
    last_error TEXT,

    -- Audit timestamps.
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    processed_at TIMESTAMPTZ
);

-- Worker pickup index: ORDER BY next_attempt_at NULLS FIRST, created_at ASC
-- is the dispatcher's main loop query. The partial WHERE clause
-- excludes dead_letter rows from the hot path so the index stays
-- compact in steady state.
CREATE INDEX IF NOT EXISTS idx_outbox_pending
    ON outbox_events (next_attempt_at NULLS FIRST, created_at)
    WHERE status = 'pending';

-- DLQ observability: a query for "everything in DLQ ordered by
-- event_type" is a likely dashboard/alerting query. Full-table
-- index is acceptable because (a) DLQ should be small, and (b)
-- an index on STATUS alone is useless for status='dead_letter' so
-- a typed partial is better.
CREATE INDEX IF NOT EXISTS idx_outbox_dlq
    ON outbox_events (event_type, created_at DESC)
    WHERE status = 'dead_letter';

-- Reverse lookup: "find events for this aggregate_id" (e.g. show
-- all outbox events for one post_target). Index supports the
-- aggregate_id-partitioned DELETE / replay tool future-work.
CREATE INDEX IF NOT EXISTS idx_outbox_aggregate
    ON outbox_events (aggregate_type, aggregate_id, created_at);
