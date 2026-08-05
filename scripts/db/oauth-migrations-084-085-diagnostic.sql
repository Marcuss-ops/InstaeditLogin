-- Read-only verification for OAuth migrations 084 and 085.
--
-- This psql script inspects only migration metadata and PostgreSQL
-- catalog/index definitions. It never selects tokens, ciphertext, users, or
-- connection credentials. The expected checksums below are for the migration
-- files in this checkout and are compared automatically with schema_migrations.
--
-- Expected result:
--   - both migration rows are APPLIED, not CHECKSUM_MISMATCH;
--   - all five expected indexes have PASS status;
--   - no diagnostic branch performs a write.

SELECT to_regclass('public.schema_migrations') IS NOT NULL AS schema_migrations_exists \gset

\if :schema_migrations_exists
WITH expected_migrations(filename, expected_checksum) AS (
    VALUES
        ('084_oauth_subject_shared_connections.sql', '8feaf557d0ddf611ba8b075ac2862a5ae14fdc63524aad805302f9301c28713a'),
        ('085_grant_scoped_tokens.sql', '197d5322cc2aeedf0988a7273500bdc01bc4335a5e598242450261db290b3b4e')
),
migration_status AS (
    SELECT
        e.filename,
        e.expected_checksum,
        sm.checksum AS recorded_checksum,
        sm.applied_at,
        CASE
            WHEN sm.filename IS NULL THEN 'MISSING'
            WHEN sm.checksum = e.expected_checksum THEN 'APPLIED'
            ELSE 'CHECKSUM_MISMATCH'
        END AS status,
        sm.checksum = e.expected_checksum AS checksum_match
    FROM expected_migrations e
    LEFT JOIN public.schema_migrations sm ON sm.filename = e.filename
)
SELECT
    'migration_status' AS section,
    filename AS object_name,
    status,
    expected_checksum,
    recorded_checksum,
    applied_at,
    checksum_match,
    NULL::BOOLEAN AS is_unique,
    NULL::BOOLEAN AS is_valid,
    NULL::BOOLEAN AS columns_match,
    NULL::BOOLEAN AS predicate_match,
    NULL::TEXT AS actual_columns,
    NULL::TEXT AS predicate,
    NULL::BIGINT AS expected_count,
    NULL::BIGINT AS observed_count
FROM migration_status

UNION ALL

SELECT
    'migration_summary',
    '084/085',
    CASE WHEN COUNT(*) FILTER (WHERE status = 'APPLIED') = 2 THEN 'PASS' ELSE 'FAIL' END,
    NULL,
    NULL,
    NULL,
    BOOL_AND(checksum_match),
    NULL,
    NULL,
    NULL,
    NULL,
    NULL,
    NULL,
    2,
    COUNT(*) FILTER (WHERE status = 'APPLIED')
FROM migration_status;
\else
SELECT
    'migration_status' AS section,
    filename AS object_name,
    'MISSING' AS status,
    expected_checksum,
    NULL::TEXT AS recorded_checksum,
    NULL::TIMESTAMPTZ AS applied_at,
    FALSE AS checksum_match,
    NULL::BOOLEAN AS is_unique,
    NULL::BOOLEAN AS is_valid,
    NULL::BOOLEAN AS columns_match,
    NULL::BOOLEAN AS predicate_match,
    NULL::TEXT AS actual_columns,
    NULL::TEXT AS predicate,
    NULL::BIGINT AS expected_count,
    NULL::BIGINT AS observed_count
FROM (VALUES
    ('084_oauth_subject_shared_connections.sql', '8feaf557d0ddf611ba8b075ac2862a5ae14fdc63524aad805302f9301c28713a'),
    ('085_grant_scoped_tokens.sql', '197d5322cc2aeedf0988a7273500bdc01bc4335a5e598242450261db290b3b4e')
) AS expected(filename, expected_checksum)

UNION ALL

SELECT
    'migration_summary',
    '084/085',
    'FAIL',
    NULL,
    NULL,
    NULL,
    FALSE,
    NULL,
    NULL,
    NULL,
    NULL,
    NULL,
    NULL,
    2,
    0;
\endif

WITH expected_indexes(index_name, relation_name, expected_unique, expected_columns, predicate_kind) AS (
    VALUES
        ('idx_oauth_connections_legacy_resource_unique', 'oauth_connections', TRUE,  'user_id,provider,provider_resource_id', 'legacy'),
        ('idx_oauth_connections_user_provider_subject',  'oauth_connections', TRUE,  'user_id,provider,provider_subject_id',   'subject'),
        ('idx_oauth_connections_provider_subject',       'oauth_connections', FALSE, 'provider,provider_subject_id',            'subject'),
        ('idx_platform_accounts_oauth_connection_id',   'platform_accounts',  FALSE, 'oauth_connection_id',                       'none'),
        ('idx_tokens_oauth_connection_token_type',      'tokens',             TRUE,  'oauth_connection_id,token_type',          'none')
),
index_observed AS (
    SELECT
        e.index_name,
        e.relation_name,
        e.expected_unique,
        e.expected_columns,
        e.predicate_kind,
        idx.oid AS index_oid,
        rel.relname AS actual_relation_name,
        COALESCE(i.indisunique, FALSE) AS is_unique,
        COALESCE(i.indisvalid, FALSE) AS is_valid,
        CASE WHEN idx.oid IS NULL THEN NULL ELSE (
            SELECT string_agg(a.attname, ',' ORDER BY k.ord)
            FROM unnest(i.indkey) WITH ORDINALITY AS k(attnum, ord)
            JOIN pg_attribute a
              ON a.attrelid = i.indrelid
             AND a.attnum = k.attnum
        ) END AS actual_columns,
        CASE WHEN idx.oid IS NULL THEN NULL ELSE pg_get_expr(i.indpred, i.indrelid) END AS predicate
    FROM expected_indexes e
    LEFT JOIN pg_class idx
      ON idx.relname = e.index_name
     AND EXISTS (
         SELECT 1
         FROM pg_namespace public_ns
         WHERE public_ns.oid = idx.relnamespace
           AND public_ns.nspname = 'public'
     )
    LEFT JOIN pg_namespace ns
      ON ns.oid = idx.relnamespace
     AND ns.nspname = 'public'
    LEFT JOIN pg_index i
      ON i.indexrelid = idx.oid
     AND ns.nspname = 'public'
    LEFT JOIN pg_class rel ON rel.oid = i.indrelid
),
index_status AS (
    SELECT
        *,
        actual_relation_name = relation_name AS relation_match,
        actual_columns = expected_columns AS columns_match,
        CASE
            WHEN index_oid IS NULL THEN FALSE
            WHEN predicate_kind = 'none' THEN predicate IS NULL
            WHEN predicate_kind = 'legacy' THEN predicate LIKE '%provider_subject_id =%'
                AND predicate NOT LIKE '%provider_subject_id <>%'
            WHEN predicate_kind = 'subject' THEN predicate LIKE '%provider_subject_id <>%'
            ELSE FALSE
        END AS predicate_match
    FROM index_observed
)
SELECT
    'index_status' AS section,
    index_name AS object_name,
    CASE
        WHEN index_oid IS NOT NULL
         AND relation_match
         AND is_unique = expected_unique
         AND is_valid
         AND columns_match
         AND predicate_match THEN 'PASS'
        WHEN index_oid IS NULL THEN 'MISSING'
        ELSE 'FAIL'
    END AS status,
    NULL::TEXT AS expected_checksum,
    NULL::TEXT AS recorded_checksum,
    NULL::TIMESTAMPTZ AS applied_at,
    NULL::BOOLEAN AS checksum_match,
    is_unique,
    is_valid,
    columns_match,
    predicate_match,
    actual_columns,
    predicate,
    NULL::BIGINT AS expected_count,
    NULL::BIGINT AS observed_count
FROM index_status

UNION ALL

SELECT
    'index_summary',
    '084/085 index contract',
    CASE WHEN COUNT(*) FILTER (WHERE
        index_oid IS NOT NULL
        AND relation_match
        AND is_unique = expected_unique
        AND is_valid
        AND columns_match
        AND predicate_match
    ) = 5 THEN 'PASS' ELSE 'FAIL' END,
    NULL,
    NULL,
    NULL,
    NULL,
    NULL,
    NULL,
    NULL,
    NULL,
    NULL,
    NULL,
    5,
    COUNT(*) FILTER (WHERE
        index_oid IS NOT NULL
        AND relation_match
        AND is_unique = expected_unique
        AND is_valid
        AND columns_match
        AND predicate_match
    )
FROM index_status
ORDER BY section, object_name;
