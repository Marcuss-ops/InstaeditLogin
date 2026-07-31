-- =============================================================================
-- 085_grant_scoped_tokens.sql
--
-- A token belongs to the OAuth grant, not to one resource discovered through
-- that grant. Modern YouTube grants can own several channels, so the channel
-- reference on tokens is optional metadata and must not be the credential
-- identity. The canonical identity is (oauth_connection_id, token_type).
--
-- Legacy provider rows may retain platform_account_id for compatibility. New
-- subject-keyed YouTube rows use NULL there; platform_accounts.oauth_connection_id
-- remains the fan-out link for every channel owned by the grant.
-- =============================================================================

-- Migration 053 made this column NOT NULL while tokens were still treated as
-- resource-scoped. Relax only this channel-side reference; the grant FK stays
-- NOT NULL and remains the credential owner.
ALTER TABLE tokens
    ALTER COLUMN platform_account_id DROP NOT NULL;

-- Migration 084 can consolidate duplicate subject-keyed oauth_connections and
-- retarget their token rows to one canonical grant. Before adding the database
-- invariant below, consolidate any resulting duplicate token rows. Keep the
-- newest token row as canonical and recover a non-empty refresh grant/scope
-- metadata from the newest older row when the newest row omitted it.
WITH ranked AS (
    SELECT
        id,
        oauth_connection_id,
        token_type,
        ROW_NUMBER() OVER (
            PARTITION BY oauth_connection_id, token_type
            ORDER BY created_at DESC, id DESC
        ) AS row_number,
        FIRST_VALUE(id) OVER (
            PARTITION BY oauth_connection_id, token_type
            ORDER BY created_at DESC, id DESC
        ) AS canonical_id
    FROM tokens
), duplicate_groups AS (
    SELECT DISTINCT canonical_id
    FROM ranked
    WHERE row_number > 1
)
UPDATE tokens canonical
SET encrypted_refresh_token = COALESCE(
        canonical.encrypted_refresh_token,
        (SELECT older.encrypted_refresh_token
           FROM tokens older
           JOIN ranked older_rank ON older_rank.id = older.id
          WHERE older_rank.canonical_id = canonical.id
            AND older.encrypted_refresh_token IS NOT NULL
          ORDER BY older.created_at DESC, older.id DESC
          LIMIT 1)
    ),
    refresh_token_expires_at = COALESCE(
        canonical.refresh_token_expires_at,
        (SELECT older.refresh_token_expires_at
           FROM tokens older
           JOIN ranked older_rank ON older_rank.id = older.id
          WHERE older_rank.canonical_id = canonical.id
            AND older.refresh_token_expires_at IS NOT NULL
          ORDER BY older.created_at DESC, older.id DESC
          LIMIT 1)
    ),
    scopes = COALESCE(
        canonical.scopes,
        (SELECT older.scopes
           FROM tokens older
           JOIN ranked older_rank ON older_rank.id = older.id
          WHERE older_rank.canonical_id = canonical.id
            AND older.scopes IS NOT NULL
          ORDER BY older.created_at DESC, older.id DESC
          LIMIT 1)
    )
FROM duplicate_groups
WHERE canonical.id = duplicate_groups.canonical_id;

WITH ranked AS (
    SELECT
        id,
        ROW_NUMBER() OVER (
            PARTITION BY oauth_connection_id, token_type
            ORDER BY created_at DESC, id DESC
        ) AS row_number
    FROM tokens
)
DELETE FROM tokens duplicate
USING ranked
WHERE duplicate.id = ranked.id
  AND ranked.row_number > 1;

-- Modern YouTube grant tokens are deliberately channel-independent. Existing
-- legacy YouTube rows are converted once the connection has a stable subject;
-- legacy/subjectless rows retain their historical channel reference.
UPDATE tokens t
SET platform_account_id = NULL
FROM oauth_connections oc
WHERE oc.id = t.oauth_connection_id
  AND oc.provider = 'youtube'
  AND oc.provider_subject_id <> '';

-- The repository uses an atomic grant-keyed upsert. This constraint makes the
-- repository contract true at the database boundary and prevents concurrent
-- refreshes/reconnects from creating duplicate credential rows.
CREATE UNIQUE INDEX IF NOT EXISTS idx_tokens_oauth_connection_token_type
    ON tokens (oauth_connection_id, token_type);

COMMENT ON COLUMN tokens.platform_account_id IS
    'Optional legacy/resource hint. Modern OAuth grant tokens are scoped by oauth_connection_id and may be NULL.';
