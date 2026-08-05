-- Safe bearer-without-refresh diagnostic (read-only).
--
-- Finds bearer token rows whose encrypted refresh-token field is NULL or
-- empty. The predicate inspects only ciphertext length; it never returns or
-- decrypts the ciphertext. Output contains technical grant/provider/status
-- fields and aggregate counts only.
--
-- A row with oauth_connection_exists=false identifies an orphaned token
-- binding. A healthy OAuth grant model returns zero rows.
WITH affected_bearers AS (
    SELECT
        t.oauth_connection_id,
        COUNT(*) AS bearer_token_row_count
    FROM public.tokens AS t
    WHERE t.token_type = 'bearer'
      AND COALESCE(octet_length(t.encrypted_refresh_token), 0) = 0
    GROUP BY t.oauth_connection_id
)
SELECT
    a.oauth_connection_id,
    (oc.id IS NOT NULL) AS oauth_connection_exists,
    oc.provider,
    oc.status AS oauth_connection_status,
    a.bearer_token_row_count,
    COUNT(DISTINCT pa.id) AS linked_platform_account_count,
    COUNT(DISTINCT pa.id) FILTER (WHERE pa.status = 'active') AS active_platform_account_count
FROM affected_bearers AS a
LEFT JOIN public.oauth_connections AS oc
       ON oc.id = a.oauth_connection_id
LEFT JOIN public.platform_accounts AS pa
       ON pa.oauth_connection_id = a.oauth_connection_id
GROUP BY
    a.oauth_connection_id,
    (oc.id IS NOT NULL),
    oc.provider,
    oc.status,
    a.bearer_token_row_count
ORDER BY
    active_platform_account_count DESC,
    a.bearer_token_row_count DESC,
    a.oauth_connection_id;
