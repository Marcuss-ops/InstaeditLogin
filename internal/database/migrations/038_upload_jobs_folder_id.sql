-- Migration 038: upload_jobs.folder_id
-- Adds a nullable folder_id column so the new
-- GET /api/v1/media/import/drive/batch/status endpoint can GROUP BY
-- per Drive folder. We store the folder_id on each upload_job so the
-- aggregation query (one row per folder_id) can scan an index range
-- instead of doing a full-table scan each time the dashboard polls.
--
-- Nullable → existing single-file async imports (drive import, single
-- video) leave folder_id = NULL and are excluded from per-folder
-- status queries (the WHERE clause matches only folder_id != '').
-- Backwards-compatible with rows written before this migration.
--
-- The partial index covers only rows that have folder_id set, so the
-- small set of single-file imports doesn't pollute the index. Drive
-- folder ids are typically ~33 chars URL-safe base64ish strings
-- (e.g. 1HregS58okcSoe8597qdXgpZM6K4CwEBD).

ALTER TABLE upload_jobs ADD COLUMN IF NOT EXISTS folder_id TEXT;

-- Partial index: only rows with a folder_id are indexed. The
-- aggregation query always carries folder_id in WHERE so this index
-- scopes the scan tightly; single-file imports (folder_id NULL) are
-- intentionally excluded so they don't bloat the index.
--
-- btree on TEXT is sufficient — Postgres handles ~33-char strings
-- efficiently and we never query by prefix on this column (exact match).
CREATE INDEX IF NOT EXISTS idx_upload_jobs_folder_id
    ON upload_jobs(folder_id)
    WHERE folder_id IS NOT NULL;
