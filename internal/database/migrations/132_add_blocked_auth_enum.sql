-- 132_add_blocked_auth_enum.sql
-- Adds the missing 'blocked_auth' label to the post_status enum.
--
-- WHY THIS MIGRATION EXISTS: the Go model (models.PostStatusBlockedAuth),
-- the Task 2/10 worker helper (markPublishBlockedAuth), the reconcile
-- worker, and several enum-coerced SQL literals
-- (post_targets.status NOT IN (..., 'blocked_auth')) all reference the
-- value, but NO migration ever added the label to the post_status type
-- (003 creates it, 012/018/035/130 add other values). Against real
-- Postgres every query carrying the literal fails with 22P02 ("invalid
-- input value for enum") because the NOT IN list coerces the untyped
-- literal to the column's enum type; blocked_auth writebacks fail and
-- are Warn-swallowed, so affected targets never reach their intended
-- terminal state and churn on lease expiry. sqlmock-based unit tests
-- could not catch this (sqlmock does not validate enum labels); the
-- migrations integration test's enum map is the guard that did.
--
-- Splits into its own file per the migration 035/130 pattern: Postgres
-- forbids ALTER TYPE ADD VALUE in the same transaction as other DDL
-- touching tables that use the type (error 55P04).
--
-- Lifecycle entry added by this migration:
--
--   → blocked_auth  (Task 2/10 — terminal per account: OAuth grant
--     invalid / channel-binding drift; the worker skips these rows
--     until the operator reconnects the grant; the admin reconnect
--     path is the FSM's blocked_auth → queued edge)
--
-- ListPending (status IN ('queued','waiting_provider')), ListPublishing
-- (status='publishing') and every terminal-guard clause already treat
-- 'blocked_auth' as excluded/terminal, so no driver changes are
-- required.

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_enum
        WHERE enumtypid = (SELECT oid FROM pg_type WHERE typname = 'post_status')
          AND enumlabel = 'blocked_auth'
    ) THEN
        ALTER TYPE post_status ADD VALUE 'blocked_auth';
    END IF;
END $$;
