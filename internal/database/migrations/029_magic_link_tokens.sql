-- =============================================================================
-- 029_magic_link_tokens.sql — SPRINT 1.2: Magic-link login tokens.
-- =============================================================================
-- Stores magic-link login tokens issued by POST /api/v1/auth/magic-link/start.
-- Email-only login (V1): user types email → server mints a token, emails it.
-- Token is single-use, 15-minute TTL. token_hash is SHA-256 of the URL-safe
-- plaintext we send in the email; the plaintext itself is never persisted.
--
-- user_id is nullable because the token may be issued BEFORE the user
-- exists (first-time signup). Verification handler creates the user
-- row if email is unknown, then marks user_id.
--
-- Idempotent: all DDL uses IF NOT EXISTS guards.
-- =============================================================================

CREATE TABLE IF NOT EXISTS magic_link_tokens (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      BIGINT REFERENCES users(id) ON DELETE CASCADE,
    email        VARCHAR(255) NOT NULL,
    token_hash   BYTEA NOT NULL,
    purpose      VARCHAR(32) NOT NULL DEFAULT 'login_signup',
    expires_at   TIMESTAMPTZ NOT NULL DEFAULT NOW() + INTERVAL '15 minutes',
    consumed_at  TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(token_hash)
);

CREATE INDEX IF NOT EXISTS idx_magic_link_tokens_email
    ON magic_link_tokens(email);

CREATE INDEX IF NOT EXISTS idx_magic_link_tokens_expires
    ON magic_link_tokens(expires_at);
