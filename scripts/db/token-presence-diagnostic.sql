-- Safe OAuth credential diagnostic (requires migration 083 or later).
-- This query intentionally returns only presence booleans, timestamps, scope
-- counts, and grant status. Never add encrypted_* columns directly to the
-- SELECT list and never decrypt values for diagnostics. NULLIF/COALESCE retain
-- compatibility with legacy aliases while both schemas coexist and treat an
-- empty ciphertext as absent; they do not expose ciphertext.
SELECT
    t.oauth_connection_id,
    t.token_type,
    COALESCE(octet_length(COALESCE(NULLIF(t.encrypted_access_token, ''::bytea), NULLIF(t.encrypted_token, ''::bytea))), 0) > 0 AS has_access_token,
    COALESCE(octet_length(t.encrypted_refresh_token), 0) > 0 AS has_refresh_token,
    COALESCE(t.access_token_expires_at, t.expires_at) AS access_token_expires_at,
    t.refresh_token_expires_at,
    COALESCE(
        array_length(NULLIF(oc.granted_scopes, '{}'::TEXT[]), 1),
        array_length(NULLIF(oc.scopes, '{}'::TEXT[]), 1),
        0
    ) AS granted_scope_count,
    oc.status AS oauth_connection_status,
    t.created_at,
    t.updated_at
FROM tokens AS t
LEFT JOIN oauth_connections AS oc ON oc.id = t.oauth_connection_id
ORDER BY t.updated_at DESC;
