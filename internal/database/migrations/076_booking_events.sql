-- =====================================================================
-- Migration 076: booking_events
--
-- Anonymous lead-capture table for the BookingProvider modal in the
-- marketing funnel. Each row is one visitor-submitted qualification:
-- intent (which CTA opened the modal) + the 3 closed-set answers
-- from the form (goal / budget / ready). No PII is captured here —
-- we deliberately do NOT accept email or name so a follow-up
-- book/discovery call is the first place we ask for contact info.
--
-- GDPR posture:
--   - ip_hash is SHA-256 of the literal peer IP with a fixed project
--     pepper (NOT a per-row salt; documented in
--     pkg/api/booking_events.go::handleCreateBookingEvent). Trades
--     off per-row unguessability for queryability — the sales team
--     can group rows by ip_hash without needing to retain raw IPs.
--   - user_agent / referer are VARCHAR(512) and TRIMMED on the Go
--     side to that length; we drop everything past byte 512.
--   - dedupe_hash is an INSERT-time ON CONFLICT DO UPDATE key so a
--     refresh-spam or double-click from the same browser in a short
--     window does NOT produce multiple rows that block sales review.
--     The full payload (intent/goal/budget/ready) plus ip_hash
--     drives the hash so a deliberate "change my answers" retry
--     from the same IP DOES land as a new row (which is correct).
--
-- Hedging against 268 column-name drift: the
-- migrations_integration_test.go requiredColumns list pins the
-- table/column pair to a single source of truth so a future
-- accidental column rename surfaces in CI rather than at the
-- first production deploy.
-- =====================================================================

CREATE TABLE IF NOT EXISTS booking_events (
    id              BIGSERIAL PRIMARY KEY,

    -- closed-set values from web/src/lib/booking.ts (booking-modal
    -- contract). 32 bytes is enough for the longest enum label.
    intent          VARCHAR(32)  NOT NULL,
    goal            VARCHAR(32)  NOT NULL,
    budget          VARCHAR(32)  NOT NULL,
    ready           VARCHAR(16)  NOT NULL,

    -- SHA-256 hex (64 chars). See header commentary.
    ip_hash         VARCHAR(64)  NOT NULL,

    -- Truncated to 512 chars on the Go side to bound row size.
    user_agent      VARCHAR(512) NOT NULL DEFAULT '',
    referer         VARCHAR(512) NOT NULL DEFAULT '',

    -- Same hash algorithm as ip_hash, but over the full payload
    -- (so an INTENT+GOAL+BUDGET+READY change from the same IP
    -- produces a distinct hash = a NEW row). The ON CONFLICT key
    -- below makes refresh-spam idempotent without losing
    -- answer-change signals.
    dedupe_hash     VARCHAR(64)  NOT NULL UNIQUE,

    -- Free-form JSON for forward-compat fields (future cohort
    -- experiments, ab-test bucket ids, …). Default {} keeps the
    -- column NOT NULL so a future query doesn't have to branch.
    metadata        JSONB        NOT NULL DEFAULT '{}'::jsonb,

    created_at      TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

-- Primary access pattern is "latest 50 events for sales triage".
-- Composite (created_at DESC) covers the LIMIT-OFFSET-by-time
-- pagination the future GET /api/v1/admin/booking_events will
-- need; the bookend column is included so the planner has a
-- single index for `WHERE created_at < $cursor` cursoring.
CREATE INDEX IF NOT EXISTS idx_booking_events_created_at_desc
    ON booking_events (created_at DESC);

-- Sales team grouping ("how many starter-tier leads this week?")
-- is a GROUP BY (intent, date_trunc('day', created_at)) ->
-- using intent alone (without time) covers vanilla grouping; the
-- Postgres planner will combine with the time-series index above
-- for date-bucketed queries.
CREATE INDEX IF NOT EXISTS idx_booking_events_intent_created_at
    ON booking_events (intent, created_at DESC);
