-- 082_normalize_youtube_long_lived_token.sql
--
-- YouTube's canonical token type is `bearer` (the value written by the
-- OAuth callback and checked first by the provider policy). Older installs
-- could persist the same Google OAuth grant as `long_lived`.
--
-- Compatibility contract:
--   * copy, never delete: the legacy row remains available for one release
--     while older workers are still deployed;
--   * only YouTube rows are touched;
--   * only the newest legacy row per OAuth connection is copied;
--   * do not overwrite an existing canonical bearer row;
--   * preserve ciphertext byte-for-byte and retain all token metadata.
--
-- The migration runner already wraps each migration in a transaction and
-- serializes runners with a PostgreSQL advisory lock. The NOT EXISTS guard
-- makes this statement idempotent if it is applied again by an operator or
-- by a future migration harness.

WITH latest_legacy AS (
    SELECT DISTINCT ON (t.oauth_connection_id)
        t.platform_account_id,
        t.oauth_connection_id,
        t.encrypted_token,
        t.encrypted_refresh_token,
        t.expires_at,
        t.scopes,
        t.key_version,
        t.created_at
    FROM tokens t
    JOIN platform_accounts pa ON pa.id = t.platform_account_id
    WHERE pa.platform = 'youtube'
      AND t.token_type = 'long_lived'
    ORDER BY t.oauth_connection_id, t.created_at DESC, t.id DESC
)
INSERT INTO tokens (
    platform_account_id,
    oauth_connection_id,
    token_type,
    encrypted_token,
    encrypted_refresh_token,
    expires_at,
    scopes,
    key_version,
    created_at
)
SELECT
    legacy.platform_account_id,
    legacy.oauth_connection_id,
    'bearer',
    legacy.encrypted_token,
    legacy.encrypted_refresh_token,
    legacy.expires_at,
    legacy.scopes,
    legacy.key_version,
    legacy.created_at
FROM latest_legacy legacy
WHERE NOT EXISTS (
    SELECT 1
    FROM tokens canonical
    WHERE canonical.oauth_connection_id = legacy.oauth_connection_id
      AND canonical.token_type = 'bearer'
);
