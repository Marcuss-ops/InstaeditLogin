-- Safe duplicate-token diagnostic (read-only).
--
-- This query identifies grant/token-type groups that violate the expected
-- one-row-per-(oauth_connection_id, token_type) invariant. It deliberately
-- returns only the non-secret grant identifier, token type, and row count.
-- PostgreSQL groups NULL oauth_connection_id values together intentionally:
-- that output represents orphaned/unbound token rows and is itself a binding
-- anomaly, not a valid OAuth grant. Never add encrypted_* columns, plaintext
-- token columns, or decrypted values to this diagnostic.
--
-- Expected result after migration 085: zero rows.
SELECT
    oauth_connection_id,
    token_type,
    COUNT(*) AS token_row_count
FROM public.tokens
GROUP BY oauth_connection_id, token_type
HAVING COUNT(*) > 1
ORDER BY token_row_count DESC, oauth_connection_id, token_type;
