-- 051_users_admin.sql
-- P2 — ops dashboard admin gate.
--
-- Adds users.is_admin + admin_granted_at + admin_granted_by so /admin/*
-- endpoints (channels / queue / health) can be gated on a stable user
-- attribute, enforced via the JWT admin claim (Identity.IsAdmin() bool).
-- Bootstrap via cmd/grant-admin --email <ops@instaedit.org>; subsequent
-- promotions via the same CLI (or followup POST /admin/users/{id}/grant-admin).
--
-- Why a separate column on `users` rather than a permissions table:
--   1. The set of admins is small (1-3 operators); a join is overkill.
--   2. Bootstrap is one-shot; rotating operators happens via CLI.
--   3. The Identity interface gains IsAdmin() bool, surfaced through
--      the JWT (see internal/auth/Claims.Admin), and gates requireAdmin.
--
-- Idempotent: IF NOT EXISTS so a re-apply is a no-op.

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS is_admin BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS admin_granted_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS admin_granted_by BIGINT REFERENCES users(id) ON DELETE SET NULL;

-- Lookup by email is rare but the bootstrap CLI uses it (cmd/grant-admin
-- --email). The btree on email already exists (canonical users.email is
-- UNIQUE); no extra index needed.

COMMENT ON COLUMN users.is_admin IS 'Operator flag: gates /admin/* endpoints. Bootstrap via cmd/grant-admin --email <email>; Idempotent (re-run no-ops). Independent of ApiKey entities (whose PermissionAdmin does NOT elevate a JWT user — JWT users must be admins first).';
COMMENT ON COLUMN users.admin_granted_at IS 'Wall-clock when is_admin was last flipped to TRUE. Reset to NULL when demoted (future followup).';
COMMENT ON COLUMN users.admin_granted_by IS 'Self-FK to the user (operator) who flipped is_admin to TRUE. Self-grant (granted_by = id) is allowed at bootstrap; subsequent promotions track chain.';
