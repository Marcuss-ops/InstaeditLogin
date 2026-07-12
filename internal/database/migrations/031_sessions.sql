-- =============================================================================
-- 031_sessions.sql — SPRINT 2.1: real revocable sessions.
-- =============================================================================
-- Per-session row carrying the refresh-token-hash, access JTI, family id,
-- UA / IP-hash for audit. One row per active session; revoked rows are
-- retained for audit. Refresh-token rotation produces a NEW row in the
-- same family (the old row is marked revoked_at = NOW(), reason =
-- 'rotated'). Reuse of a revoked refresh token => mark the entire
-- token_family_id as revoked (the "refresh_reuse" / theft detection
-- path).
--
-- Access JWTs (15 min) are NOT verified against this table on every
-- request — the access token's short lifetime bounds the exposure. A
-- revoked session keeps its 15-min access window; the next refresh
-- attempt is rejected.
--
-- Idempotent. All DDL uses IF NOT EXISTS guards.
-- =============================================================================

CREATE TABLE IF NOT EXISTS sessions (
    id                BIGSERIAL PRIMARY KEY,
    user_id           BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    workspace_id      BIGINT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    token_family_id   TEXT NOT NULL,
    access_jti        TEXT NOT NULL,
    refresh_token_hash BYTEA NOT NULL,
    user_agent        TEXT,
    ip_hash           TEXT,
    created_at        TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    expires_at        TIMESTAMP WITH TIME ZONE NOT NULL,
    refresh_expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
    last_used_at      TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    revoked_at        TIMESTAMP WITH TIME ZONE,
    revoke_reason     TEXT
);

-- Hot path lookup: cookie contains refresh_token plaintext; middleware
-- SHA-256s it and queries by hash.
CREATE INDEX IF NOT EXISTS idx_sessions_refresh_hash
    ON sessions(refresh_token_hash);

-- List active sessions for a user (sessions-management endpoint).
CREATE INDEX IF NOT EXISTS idx_sessions_user_active
    ON sessions(user_id) WHERE revoked_at IS NULL;

-- Family-wide revocation: "if any session in this family is reused
-- after revocation, mark ALL of them revoked with reason =
-- refresh_reuse_detected".
CREATE INDEX IF NOT EXISTS idx_sessions_family
    ON sessions(token_family_id);

-- Optional: enforce that access_jti is unique per row (defense-in-depth
-- against accidental duplicate issuance).
CREATE UNIQUE INDEX IF NOT EXISTS idx_sessions_access_jti
    ON sessions(access_jti);
