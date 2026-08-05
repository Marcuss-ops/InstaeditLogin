-- Read-only YouTube refresh-token eviction diagnostic.
--
-- Purpose: detect SILENT refresh-token eviction. Google invalidates the
-- oldest refresh token of a (Google Account, OAuth client) pair once the
-- pair reaches ~50-100 active tokens, WITHOUT notifying the app (see
-- docs/oauth-google-limits.md). Eviction has no direct on-disk signal on
-- our side; this diagnostic surfaces the risk indicators:
--
--   A. subject_client          tokens per (provider_subject_id,
--                              oauth_client_key) vs the recommended
--                              capacity (50), the pool critical
--                              threshold (90) and Google's hard cap
--                              (100);
--   B. eviction_signals        invalid_grant / reauth_required /
--                              non-active connections — the observable
--                              symptom once the refresh sweep meets a
--                              dead token;
--   C. orphan_tokens           token rows whose oauth_connection is gone;
--   D. channel_grant_consistency  channels attached to dead grants and
--                              grant fan-out per connection;
--   E. summary                 PASS/CHECK aggregates.
--
-- This script is STRICTLY READ-ONLY and secret-free: it projects only
-- identifiers, statuses, timestamps, error codes and counts. It never
-- selects encrypted_* / ciphertext / token material and never writes.
--
-- Usage (password-free URL + protected PGPASSFILE, never a DSN in argv):
--   PGPASSFILE="$HOME/.pgpass-instaedit" \
--     psql "postgresql://db-host:5432/instaedit?sslmode=verify-full" \
--     -X -q -w -v ON_ERROR_STOP=1 \
--     -f scripts/db/refresh-token-eviction-diagnostic.sql

-- A. Tokens per (Google subject, OAuth client) vs the capacity bands.
WITH subject_client AS (
    SELECT
        oc.provider_subject_id,
        oc.oauth_client_key,
        COUNT(DISTINCT oc.id) AS connection_count,
        COUNT(t.id) AS token_row_count
    FROM oauth_connections oc
    LEFT JOIN tokens t ON t.oauth_connection_id = oc.id
    GROUP BY oc.provider_subject_id, oc.oauth_client_key
)
SELECT
    'subject_client' AS section,
    provider_subject_id,
    oauth_client_key,
    connection_count,
    token_row_count,
    token_row_count >= 40 AS near_recommended_cap,
    token_row_count >= 50 AS at_or_over_recommended_cap,
    token_row_count >= 90 AS over_critical_threshold,
    token_row_count >= 100 AS at_google_hard_cap
FROM subject_client
ORDER BY token_row_count DESC, provider_subject_id;

-- B. Observable eviction / dead-grant signals from the refresh sweep.
SELECT
    'eviction_signals' AS section,
    oc.id AS connection_id,
    oc.provider,
    oc.provider_subject_id,
    oc.oauth_client_key,
    oc.status,
    oc.last_refresh_at,
    oc.reauth_required_at,
    oc.last_refresh_error
FROM oauth_connections oc
WHERE oc.reauth_required_at IS NOT NULL
   OR oc.last_refresh_error ILIKE '%invalid_grant%'
   OR oc.last_refresh_error ILIKE '%quota%'
   OR oc.status <> 'active'
ORDER BY oc.reauth_required_at DESC NULLS LAST, oc.updated_at DESC;

-- C. Token rows whose grant row no longer exists.
SELECT
    'orphan_tokens' AS section,
    t.oauth_connection_id,
    COUNT(*) AS orphan_token_count,
    BOOL_OR(t.token_type = 'bearer') AS has_bearer_token
FROM tokens t
LEFT JOIN oauth_connections oc ON oc.id = t.oauth_connection_id
WHERE oc.id IS NULL
GROUP BY t.oauth_connection_id
ORDER BY orphan_token_count DESC;

-- D. Grant fan-out and channel status per connection.
SELECT
    'channel_grant_consistency' AS section,
    oc.id AS connection_id,
    oc.status AS connection_status,
    oc.oauth_client_key,
    COUNT(pa.id) AS channel_count,
    COUNT(pa.id) FILTER (WHERE pa.status = 'active') AS active_channels,
    COUNT(pa.id) FILTER (WHERE pa.status IN ('disconnected', 'deleted')) AS removed_channels
FROM oauth_connections oc
LEFT JOIN platform_accounts pa ON pa.oauth_connection_id = oc.id
GROUP BY oc.id, oc.status, oc.oauth_client_key
ORDER BY channel_count DESC;

-- E. Aggregate PASS/CHECK statuses (always returns rows).
WITH subject_client AS (
    SELECT
        oc.provider_subject_id,
        oc.oauth_client_key,
        COUNT(t.id) AS token_row_count
    FROM oauth_connections oc
    LEFT JOIN tokens t ON t.oauth_connection_id = oc.id
    GROUP BY oc.provider_subject_id, oc.oauth_client_key
)
SELECT
    'summary' AS section,
    'subject_client_pairs_at_or_over_cap_50' AS metric,
    COUNT(*) FILTER (WHERE token_row_count >= 50) AS observed_count,
    NULL::BIGINT AS expected_count,
    CASE WHEN COUNT(*) FILTER (WHERE token_row_count >= 50) = 0 THEN 'PASS' ELSE 'CHECK' END AS status
FROM subject_client

UNION ALL

SELECT
    'summary',
    'invalid_grant_or_reauth_signals',
    COUNT(*)::BIGINT,
    NULL,
    CASE WHEN COUNT(*) = 0 THEN 'PASS' ELSE 'CHECK' END
FROM oauth_connections
WHERE last_refresh_error ILIKE '%invalid_grant%'
   OR reauth_required_at IS NOT NULL

UNION ALL

SELECT
    'summary',
    'orphan_token_rows',
    COUNT(*)::BIGINT,
    NULL,
    CASE WHEN COUNT(*) = 0 THEN 'PASS' ELSE 'CHECK' END
FROM tokens t
LEFT JOIN oauth_connections oc ON oc.id = t.oauth_connection_id
WHERE oc.id IS NULL;
