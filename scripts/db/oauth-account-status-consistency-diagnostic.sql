-- Safe OAuth grant/account consistency diagnostic (read-only).
--
-- Reports only technical IDs, provider/status values, inconsistency reasons,
-- and counts. It never selects tokens, ciphertext, usernames, email, or
-- provider resource identifiers.
--
-- Expected result for a healthy database: zero rows.
WITH account_rows AS (
    SELECT
        pa.id AS platform_account_id,
        pa.oauth_connection_id,
        pa.user_id AS account_user_id,
        pa.platform,
        pa.status AS account_status,
        pa.reauth_required_at,
        oc.id AS connection_id,
        oc.user_id AS connection_user_id,
        oc.provider,
        oc.status AS connection_status,
        oc.reauth_required_at AS connection_reauth_required_at
    FROM public.platform_accounts AS pa
    LEFT JOIN public.oauth_connections AS oc
           ON oc.id = pa.oauth_connection_id
),
classified AS (
    SELECT
        a.*,
        CASE
            WHEN a.connection_id IS NULL THEN 'MISSING_OAUTH_CONNECTION'
            WHEN a.connection_user_id <> a.account_user_id THEN 'OWNER_MISMATCH'
            WHEN a.provider <> a.platform THEN 'PROVIDER_MISMATCH'
            WHEN (a.account_status = 'reauth_required' OR a.reauth_required_at IS NOT NULL)
                 AND a.connection_status = 'active' THEN 'ACCOUNT_REAUTH_GRANT_ACTIVE'
            WHEN a.account_status = 'disconnected'
                 AND a.connection_status = 'active' THEN 'DISCONNECTED_ACCOUNT_GRANT_ACTIVE'
            WHEN a.account_status = 'active'
                 AND a.connection_reauth_required_at IS NOT NULL THEN 'ACTIVE_ACCOUNT_GRANT_REAUTH_REQUIRED'
            WHEN a.account_status = 'active' AND a.connection_status <> 'active' THEN 'ACTIVE_ACCOUNT_NONACTIVE_GRANT'
            WHEN a.account_status <> 'active' AND a.connection_status = 'active' THEN 'NONACTIVE_ACCOUNT_ACTIVE_GRANT'
            ELSE NULL
        END AS inconsistency_reason
    FROM account_rows AS a
)
SELECT
    c.oauth_connection_id,
    c.connection_id AS observed_oauth_connection_id,
    c.platform,
    c.provider,
    c.account_status,
    c.connection_status,
    c.inconsistency_reason,
    COUNT(*) AS affected_platform_account_count,
    COUNT(*) FILTER (WHERE c.account_status = 'active') AS affected_active_account_count
FROM classified AS c
WHERE c.inconsistency_reason IS NOT NULL
GROUP BY
    c.oauth_connection_id,
    c.connection_id,
    c.platform,
    c.provider,
    c.account_status,
    c.connection_status,
    c.inconsistency_reason
ORDER BY
    c.inconsistency_reason,
    c.oauth_connection_id,
    c.platform;
