-- 122_thumbnail_export_idempotency.sql
-- Persist the complete render profile so retries of the same immutable
-- project revision converge on one export and one durable checksum.
-- Empty profiles are retained for legacy/manual rows; new renderer jobs
-- always provide a non-empty canonical profile.

ALTER TABLE thumbnail_exports
    ADD COLUMN IF NOT EXISTS render_profile TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

CREATE INDEX IF NOT EXISTS thumbnail_exports_render_profile_idx
    ON thumbnail_exports (project_id, revision_id, render_profile)
    WHERE btrim(render_profile) <> '';

CREATE UNIQUE INDEX IF NOT EXISTS thumbnail_exports_render_profile_uq
    ON thumbnail_exports (project_id, revision_id, render_profile)
    WHERE btrim(render_profile) <> '';
