-- =============================================================================
-- 099_oauth_connection_client_key.sql
--
-- YouTube OAuth Client Pool — the oauth_client_key column records which
-- Google OAuth client issued each grant ("youtube_pool_a" /
-- "youtube_pool_b"). A refresh token must always be renewed with the
-- SAME client_id + client_secret that issued it, so the key is part of
-- the grant identity (platform_accounts → oauth_connections →
-- oauth_client_key → registry.Resolve(key)).
--
-- Legacy rows (single-client deployments) default to youtube_pool_a,
-- which is the honest label for the historical single-client path.
--
-- The anti-duplicate constraint (one active grant per channel + client)
-- is intentionally NOT created here: the schema is subject-keyed
-- (migration 084 — one Google subject owns N channels on one grant),
-- so that constraint needs its own design pass on platform_accounts
-- rather than a naive per-resource index on oauth_connections.
-- =============================================================================

ALTER TABLE oauth_connections
    ADD COLUMN IF NOT EXISTS oauth_client_key TEXT NOT NULL DEFAULT 'youtube_pool_a';

-- Supports the pool metrics + capacity queries grouped by
-- (provider, provider_subject_id, oauth_client_key).
CREATE INDEX IF NOT EXISTS idx_oauth_connections_provider_subject_client
    ON oauth_connections (provider, provider_subject_id, oauth_client_key)
    WHERE provider_subject_id <> '';

COMMENT ON COLUMN oauth_connections.oauth_client_key IS
    'Pool client that issued this grant (youtube_pool_a/b). Legacy single-client rows default to youtube_pool_a. A token must always be refreshed with the same client that issued it.';
