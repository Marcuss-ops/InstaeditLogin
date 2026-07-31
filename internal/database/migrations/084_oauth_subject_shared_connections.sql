-- =============================================================================
-- 084_oauth_subject_shared_connections.sql
--
-- Canonical grant identity for providers that can expose multiple resources
-- (notably one Google account owning multiple YouTube channels).
--
-- oauth_connections is a grant table, while platform_accounts is a resource
-- table. Migration 043 initially used provider_resource_id as the unique key,
-- which made one YouTube channel look like one OAuth grant. New OAuth callbacks
-- populate provider_subject_id with the stable Google subject and all channel
-- rows reference the same oauth_connection_id.
--
-- Legacy rows keep their resource-bound uniqueness and remain valid. The
-- partial subject index applies only to populated subjects, so an old row with
-- provider_subject_id='' cannot collide with a modern subject-keyed grant.
-- This migration is idempotent because the migration runner may execute the
-- SQL against installations that were upgraded in-place.
-- =============================================================================

-- Remove migration 043's resource-bound UNIQUE constraint. It must not
-- remain in place: a resource id is channel data, not grant identity, and
-- the same resource hint may legitimately occur on modern subject-keyed
-- rows. Keep the old uniqueness rule only for legacy rows below.
DO $$
DECLARE
    constraint_name TEXT;
BEGIN
    SELECT c.conname INTO constraint_name
    FROM pg_constraint c
    WHERE c.conrelid = 'oauth_connections'::regclass
      AND c.contype = 'u'
      AND c.conkey = ARRAY[
          (SELECT attnum::smallint FROM pg_attribute
             WHERE attrelid = c.conrelid AND attname = 'user_id'),
          (SELECT attnum::smallint FROM pg_attribute
             WHERE attrelid = c.conrelid AND attname = 'provider'),
          (SELECT attnum::smallint FROM pg_attribute
             WHERE attrelid = c.conrelid AND attname = 'provider_resource_id')
      ]::smallint[]
    LIMIT 1;

    IF constraint_name IS NOT NULL THEN
        EXECUTE format('ALTER TABLE oauth_connections DROP CONSTRAINT %I', constraint_name);
    END IF;
END $$;

-- A prior deployment or manual repair may have created duplicate populated
-- subjects before this invariant existed. Consolidate those rows before the
-- unique index is created. The lowest id is the canonical connection; merge
-- grant metadata first, then retarget all foreign keys before duplicate grants
-- are removed.
WITH duplicate_groups AS (
    SELECT user_id, provider, provider_subject_id,
           MIN(id) AS canonical_id
    FROM oauth_connections
    WHERE provider_subject_id <> ''
    GROUP BY user_id, provider, provider_subject_id
    HAVING COUNT(*) > 1
)
UPDATE oauth_connections canonical
SET scopes = ARRAY(
        SELECT DISTINCT scope
        FROM oauth_connections member
        CROSS JOIN LATERAL unnest(COALESCE(member.scopes, '{}'::TEXT[])) AS scope_values(scope)
        WHERE member.user_id = groups.user_id
          AND member.provider = groups.provider
          AND member.provider_subject_id = groups.provider_subject_id
    ),
    granted_scopes = ARRAY(
        SELECT DISTINCT scope
        FROM oauth_connections member
        CROSS JOIN LATERAL unnest(COALESCE(member.granted_scopes, '{}'::TEXT[])) AS scope_values(scope)
        WHERE member.user_id = groups.user_id
          AND member.provider = groups.provider
          AND member.provider_subject_id = groups.provider_subject_id
    ),
    provider_resource_id = COALESCE(
        (SELECT member.provider_resource_id
         FROM oauth_connections member
         WHERE member.user_id = groups.user_id
           AND member.provider = groups.provider
           AND member.provider_subject_id = groups.provider_subject_id
         ORDER BY member.updated_at DESC, member.id DESC
         LIMIT 1),
        canonical.provider_resource_id
    ),
    login_hint = COALESCE(
        (SELECT member.login_hint
         FROM oauth_connections member
         WHERE member.user_id = groups.user_id
           AND member.provider = groups.provider
           AND member.provider_subject_id = groups.provider_subject_id
           AND member.login_hint IS NOT NULL
         ORDER BY member.updated_at DESC, member.id DESC
         LIMIT 1),
        canonical.login_hint
    ),
    status = CASE
        WHEN EXISTS (
            SELECT 1 FROM oauth_connections member
            WHERE member.user_id = groups.user_id
              AND member.provider = groups.provider
              AND member.provider_subject_id = groups.provider_subject_id
              AND member.status = 'active'
        ) THEN 'active'
        ELSE canonical.status
    END,
    expires_at = COALESCE(
        GREATEST(
            canonical.expires_at,
            (SELECT MAX(member.expires_at)
             FROM oauth_connections member
             WHERE member.user_id = groups.user_id
               AND member.provider = groups.provider
               AND member.provider_subject_id = groups.provider_subject_id)
        ),
        canonical.expires_at
    ),
    last_validated_at = COALESCE(
        GREATEST(
            canonical.last_validated_at,
            (SELECT MAX(member.last_validated_at)
             FROM oauth_connections member
             WHERE member.user_id = groups.user_id
               AND member.provider = groups.provider
               AND member.provider_subject_id = groups.provider_subject_id)
        ),
        canonical.last_validated_at
    ),
    last_refresh_error = COALESCE(
        (SELECT member.last_refresh_error
         FROM oauth_connections member
         WHERE member.user_id = groups.user_id
           AND member.provider = groups.provider
           AND member.provider_subject_id = groups.provider_subject_id
           AND member.last_refresh_error IS NOT NULL
         ORDER BY member.updated_at DESC, member.id DESC
         LIMIT 1),
        canonical.last_refresh_error
    ),
    reauth_required_at = COALESCE(
        (SELECT MIN(member.reauth_required_at)
         FROM oauth_connections member
         WHERE member.user_id = groups.user_id
           AND member.provider = groups.provider
           AND member.provider_subject_id = groups.provider_subject_id),
        canonical.reauth_required_at
    ),
    last_refresh_at = COALESCE(
        GREATEST(
            canonical.last_refresh_at,
            (SELECT MAX(member.last_refresh_at)
             FROM oauth_connections member
             WHERE member.user_id = groups.user_id
               AND member.provider = groups.provider
               AND member.provider_subject_id = groups.provider_subject_id)
        ),
        canonical.last_refresh_at
    ),
    updated_at = NOW()
FROM duplicate_groups groups
WHERE canonical.id = groups.canonical_id;

WITH subject_rows AS (
    SELECT id,
           MIN(id) OVER (
               PARTITION BY user_id, provider, provider_subject_id
           ) AS canonical_id
    FROM oauth_connections
    WHERE provider_subject_id <> ''
), duplicate_map AS (
    SELECT id AS duplicate_id, canonical_id
    FROM subject_rows
    WHERE id <> canonical_id
)
UPDATE platform_accounts pa
SET oauth_connection_id = dm.canonical_id,
    updated_at = NOW()
FROM duplicate_map dm
WHERE pa.oauth_connection_id = dm.duplicate_id;

WITH subject_rows AS (
    SELECT id,
           MIN(id) OVER (
               PARTITION BY user_id, provider, provider_subject_id
           ) AS canonical_id
    FROM oauth_connections
    WHERE provider_subject_id <> ''
), duplicate_map AS (
    SELECT id AS duplicate_id, canonical_id
    FROM subject_rows
    WHERE id <> canonical_id
)
UPDATE tokens t
SET oauth_connection_id = dm.canonical_id
FROM duplicate_map dm
WHERE t.oauth_connection_id = dm.duplicate_id;

WITH subject_rows AS (
    SELECT id,
           MIN(id) OVER (
               PARTITION BY user_id, provider, provider_subject_id
           ) AS canonical_id
    FROM oauth_connections
    WHERE provider_subject_id <> ''
), duplicate_map AS (
    SELECT id AS duplicate_id
    FROM subject_rows
    WHERE id <> canonical_id
)
DELETE FROM oauth_connections oc
USING duplicate_map dm
WHERE oc.id = dm.duplicate_id;

-- Preserve the old resource-level guard only for legacy rows that have no
-- provider subject. Modern subject-keyed rows are intentionally exempt.
CREATE UNIQUE INDEX IF NOT EXISTS idx_oauth_connections_legacy_resource_unique
    ON oauth_connections (user_id, provider, provider_resource_id)
    WHERE provider_subject_id = '';

-- The subject is the grant identity for modern OAuth rows. The WHERE clause
-- deliberately excludes the empty-string legacy default from this index.
CREATE UNIQUE INDEX IF NOT EXISTS idx_oauth_connections_user_provider_subject
    ON oauth_connections (user_id, provider, provider_subject_id)
    WHERE provider_subject_id <> '';

-- Supports subject-scoped operational queries without requiring the partial
-- unique index to serve every planner shape.
CREATE INDEX IF NOT EXISTS idx_oauth_connections_provider_subject
    ON oauth_connections (provider, provider_subject_id)
    WHERE provider_subject_id <> '';

-- The FK/index already created by migration 043 is the fan-out path:
-- multiple platform_accounts may intentionally carry the same value here.
CREATE INDEX IF NOT EXISTS idx_platform_accounts_oauth_connection_id
    ON platform_accounts (oauth_connection_id)
    WHERE oauth_connection_id IS NOT NULL;

COMMENT ON COLUMN oauth_connections.provider_subject_id IS
    'Stable OAuth grant owner (Google subject/sub). Modern shared grants are unique per user/provider/subject.';
COMMENT ON COLUMN oauth_connections.provider_resource_id IS
    'Legacy or representative provider resource id; resource-specific rows belong in platform_accounts.';
