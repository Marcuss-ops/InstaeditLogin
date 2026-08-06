-- 104_upload_jobs_claim_indexes.sql
-- Queue indexes for the atomic upload_jobs claim statements.
--
-- 046 already created the small partial ordering index used by the
-- worker. These companion indexes align with the deterministic claim
-- ordering (priority, created_at, id), so a large queue does not
-- require scanning every eligible row before FOR UPDATE SKIP LOCKED
-- can find work. Eligibility remains in the query because NOW() is
-- not allowed in an index predicate.

CREATE INDEX IF NOT EXISTS idx_upload_jobs_claim_available
    ON upload_jobs (
        priority ASC,
        created_at ASC,
        id ASC
    )
    WHERE status IN ('pending', 'retry_wait');

CREATE INDEX IF NOT EXISTS idx_upload_jobs_publish_claim
    ON upload_jobs (
        priority ASC,
        created_at ASC,
        id ASC
    )
    WHERE status = 'ingest_completed';
