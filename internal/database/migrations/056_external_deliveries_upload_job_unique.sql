-- =============================================================================
-- 056_external_deliveries_upload_job_unique.sql
-- =============================================================================
-- Enforces the one-to-one invariant between external_deliveries and the
-- upload_job they spawn. The unique partial index only covers non-NULL
-- values so multiple unlinked deliveries are still allowed and the old
-- non-unique partial index is replaced by this one.
-- =============================================================================

DROP INDEX IF EXISTS idx_external_deliveries_upload_job_id;

CREATE UNIQUE INDEX IF NOT EXISTS idx_external_deliveries_upload_job_id
    ON external_deliveries (upload_job_id)
    WHERE upload_job_id IS NOT NULL;
