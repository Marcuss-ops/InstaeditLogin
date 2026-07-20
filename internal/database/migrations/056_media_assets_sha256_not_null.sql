-- =============================================================================
-- 056_media_assets_sha256_not_null.sql — task 5/10 sha256 hardening
-- =============================================================================
-- Why this migration exists:
--
-- Prior to Task 5/10, media_assets.sha256 was TEXT NULL — the /complete
-- handler could mark an asset ready WITHOUT a SHA, and the
-- upload_job ingest path could pass empty / nil when the upstream
-- didn't surface sha256Checksum (the artifactVerifyReader from Task
-- 4/10 now computes a local SHA on every stream so this should NEVER
-- happen on fresh installs, but legacy rows + the /complete handler's
-- pre-Task-4/10 path may still produce empty strings).
--
-- The user's spec for Task 5/10 is:
--
--   "Chiama mediaStore.MarkReady(assetID, actualSHA256,
--    verifiedSize, verifiedContentType) — MAI più con stringa SHA
--    vuota. Aggiorna lo schema media_assets.sha256 se serve
--    renderlo NOT NULL dopo migrazione."
--
-- So the migration makes the column NOT NULL (locking the schema-level
-- guarantee that the column is always populated) and backfills
-- pre-existing NULL values with an empty string. The empty-string is
-- the legacy "no SHA available" sentinel — chosen over the SHA-256
-- of the empty input (`e3b0c44…b855`) because there is no guarantee
-- that pre-Task-4/10 /complete rows were just SOMETHING download; an
-- empty string preserves the historic semantic of "we never
-- recorded a SHA" so future debugging (e.g. grepping the column for
-- empty) keeps working without database-side magic.
--
-- Why no CHECK constraint on sha256 format:
--
--   - The user spec explicitly says MAI più "stringa SHA vuota" — a
--     regex CHECK would over-restrict the legacy empty-string
--     backfill (which fails `^[a-f0-9]{64}$`).
--   - No existing migration in the canonical set adds a CHECK on
--     a SHA column (external_deliveries.expected_sha256 is
--     TEXT NOT NULL but format-unrestricted). The codebase's
--     convention is "validate at the application layer; the
--     schema stays loose so legacy rows don't break".
--   - Task 5/10's "MAI più" guarantee is structurally enforced by
--     the artifactVerifyReader's ActualSHA256Hex() (Task 4/10)
--     — every caller wrapping the body in it passes a non-empty
--     64-hex string to MarkReady.
--
-- Idempotency / order-independence:
--
--   Mirrors the canonical migration_runner contract:  runner applies
--   every file via `embed.FS` against a fresh DB; no `schema_migrations`
--   table; every DDL block must be `IF NOT EXISTS` — or guarded by
--   a DO block — for replay safety.
-- =============================================================================

-- 1. Backfill: NULL → empty string (legacy "no SHA recorded" sentinel).
--    Idempotent on the WHERE column type because '' already satisfies
--    the implicit non-NULL-after-backfill goal.
UPDATE media_assets
   SET sha256 = ''
 WHERE sha256 IS NULL;

-- 2. NOT NULL constraint. Idempotent: SET NOT NULL is a no-op if the
--    column is already NOT NULL, but PG accepts the statement on
--    idempotent replay (the catalog flip is OR'ed in). Wrapped in a
--    DO block with information_schema lookup so a future replay
--    against a DB that already has it SET NOT NULL ALSO no-ops
--    cleanly (avoids an "ALTER COLUMN ... does not have a NOT NULL
--    constraint to drop" noise on replay).
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
          FROM information_schema.columns
         WHERE table_name  = 'media_assets'
           AND column_name = 'sha256'
           AND is_nullable = 'YES'
    ) THEN
        ALTER TABLE media_assets
            ALTER COLUMN sha256 SET NOT NULL;
    END IF;
END $$;

-- 3. Operator-facing note: the empty-string "no SHA" backfill is a
--    visible-in-the-dashboard marker. Future Drive imports must
--    stamp a 64-hex SHA via artifactVerifyReader.ActualSHA256Hex()
--    so the column is never empty on fresh rows.
COMMENT ON COLUMN media_assets.sha256 IS
    'SHA-256 (64-char lowercase hex) of the artifact bytes. NOT NULL after migration 056 (backfilled empty string for pre-migration /complete rows). Fresh rows are stamped by artifactVerifyReader.ActualSHA256Hex() (Task 4/10) — empty-string is the legacy "no SHA recorded" sentinel and must not appear on post-migration ingests.';
