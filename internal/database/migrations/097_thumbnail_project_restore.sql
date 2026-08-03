-- 097_thumbnail_project_restore.sql
--
-- A normal snapshot save is deduplicated by the repository, but an explicit
-- restore is an intentional historical event and must create a new immutable
-- revision even when its content matches an older revision. The original
-- migration's hash uniqueness constraint prevented that valid audit event.
-- Keep revision_number unique; remove only the hash constraint.
ALTER TABLE thumbnail_project_revisions
    DROP CONSTRAINT IF EXISTS thumbnail_project_revisions_project_hash_uq;
