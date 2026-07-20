-- +goose Up
-- 060_account_status_suspended.sql — lock the platform_accounts.status
-- allow-list at the schema layer via a CHECK constraint AND admit the
-- new 'suspended' literal.
--
-- Why a CHECK (deviation from the codebase pattern):
--
-- Every other status-shaped column across the codebase is just
-- VARCHAR(N) / TEXT with a DEFAULT + NOT NULL (e.g., users, billing,
-- outbox_events, webhooks, oauth_connections, media_assets,
-- external_deliveries). That kept prior migrations minimal but
-- left drift un-caught at the schema layer: a typo in a Go constant
-- or an old worker path could INSERT 'activ' or 'reauth' and the
-- db would happily accept it. Migration 030 (connection_states) and
-- 033 (webhook_runtime) introduced the CHECK pattern for the
-- stricter-recovery-status columns where a typo would have caused
-- a silent dead-letter; we extend the same discipline to
-- platform_accounts.status because the OAuth callback promotion path
-- (ChannelAuthorizationService.AuthorizeChannel) reads this column
-- back as platform_accounts.status and the eligibility gate
-- (internal/services/eligibility_gate.go::IsEligibleForActivePromotion)
-- assumes a fixed literal set. A regression that adds an unknown
-- value at INSERT time would be caught by this constraint BEFORE
-- the eligibility gate can erroneously accept it.
--
-- Backfill: NONE required. The new value 'suspended' is additive —
-- existing rows keep their pre-migration status and never fail the
-- constraint (all 8 values in the IN list are valid today).
--
-- Roll-forward compatibility with the existing platform_accounts
-- rowset: the IN list contains every existing AccountStatus* constant
-- in internal/models/user.go (verified at the Go layer via the
-- table-driven test internal/services/eligibility_gate_test.go::
-- TestIsEligibleForActivePromotion's 9 subtests today; will be a
-- 10th subtest once this migration lands). A future constant addition
-- MUST be paired with a follow-up migration that ALTERs this
-- constraint.
ALTER TABLE platform_accounts
    ADD CONSTRAINT platform_accounts_status_check
    CHECK (status IN (
        'active',
        'expired',
        'reauth_required',
        'revoked',
        'disconnected',
        'error',
        'pending_authorization',
        'suspended'
    ));

-- +goose Down
ALTER TABLE platform_accounts
    DROP CONSTRAINT IF EXISTS platform_accounts_status_check;
