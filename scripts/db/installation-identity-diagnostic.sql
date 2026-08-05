-- Read-only installation identity diagnostic.
--
-- The wrapper script supplies the expected UUID through the session-local
-- custom setting app.diagnostic_expected_installation_uuid. This query emits
-- only a classification; it never returns the database UUID or any secret.
-- Run through scripts/db/installation-identity-diagnostic.sh so the expected
-- value and password-bearing connection details are not printed.
SELECT to_regclass('public.system_installation') IS NOT NULL AS table_exists \gset

\if :table_exists
WITH identity AS (
    SELECT installation_uuid::text AS actual_uuid
      FROM public.system_installation
     WHERE id = 1
)
SELECT CASE
         WHEN COUNT(*) = 0 THEN 'MISSING'
         WHEN BOOL_AND(
              actual_uuid::uuid = current_setting(
                  'app.diagnostic_expected_installation_uuid', true
              )::uuid
         ) THEN 'MATCH'
         ELSE 'MISMATCH'
       END AS installation_identity_status
  FROM identity;
\else
SELECT 'MISSING' AS installation_identity_status;
\endif
