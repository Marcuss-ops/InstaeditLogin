-- =============================================================================
-- 057_delivery_sessions.sql — Task 8/10: Google Drive destination upload state
-- =============================================================================
-- Why this migration exists:
--
-- Task 8/10 introduces a DeliveryProvider for Google Drive (the
-- destination-side provider — distinct from the import-side GoogleDriveOAuthService
-- which remains untouched per spec). The destination uploads the same artifact
-- InstaEdit publishes to YouTube, but into an operator-configured Shared Drive
-- folder. The upload uses Google's resumable upload protocol; on a worker
-- crash mid-upload, the next worker must be able to RESUME from the persisted
-- offset instead of restreaming the whole file from byte 0.
--
-- That's the same problem class as upload_jobs.youtube_session_uri (migration
-- 048) — but for a DIFFERENT lifecycle: the Drive destination is invoked at
-- POST-COMPLETION time from publish_worker_delivery.dispatchPostCompletion,
-- not bound to an upload_job row. So the persisted state lives in a separate
-- table rather than as more columns on upload_jobs.
--
-- Schema decisions:
--
--   - generic deliverable_type column with idempotency_key UNIQUE per type:
--     future Destination providers (S3, MinIO, velox ack retries) reuse
--     the same table once we extend state values. Today only 'google_drive'.
--   - version INT NOT NULL DEFAULT 1 for optimistic-concurrency CAS:
--     parallel to upload_jobs lease_owner CAS but for state owned by the
--     delivery worker (no upload_job lease to attach to).
--   - session_uri_encrypted TEXT NULL: ciphertext of the resumable session
--     URI (parallel to youtube_session_uri ciphertext pattern). NULL after
--     completion so the row doesn't carry a stale, dead URI.
--   - state enum ('initiated'|'uploading'|'completed'|'failed'|'expired'):
--     'initiated' = session URI persisted, no bytes sent yet; 'uploading' =
--     at least one chunk PUT acknowledged; 'completed' = final 200 received,
--     remote_file_id + remote_url stamped; 'failed' = error stamped, retry
--     allowed; 'expired' = session URI hit Google's 7-day TTL, full
--     re-initiation required.
--   - app_properties JSONB stamps the instaedit_delivery_id key on the
--     Drive file for app-property-based idempotency lookup (catches the
--     edge case where the DB row was lost but Drive still has the file).
--   - unique(deliverable_type, idempotency_key) is the database-level
--     guarantee that a retry of the same logical delivery cannot create
--     a second row (the destination's UPSERT path relies on this).
--
-- Idempotency / order-independence:
--
--   Mirrors every prior migration: replay-safe via IF NOT EXISTS + the
--   canonical embed.FS migration_runner contract. No schema_migrations
--   tracking table; runner applies every file against a fresh DB.
-- =============================================================================

CREATE TABLE IF NOT EXISTS delivery_sessions (
    id                       BIGSERIAL    PRIMARY KEY,
    deliverable_type         TEXT         NOT NULL,
    idempotency_key          TEXT         NOT NULL,
    state                    TEXT         NOT NULL DEFAULT 'initiated',
    session_uri_encrypted    TEXT,
    uploaded_bytes           BIGINT       NOT NULL DEFAULT 0,
    total_bytes              BIGINT       NOT NULL,
    chunk_size               BIGINT       NOT NULL,
    mime_type                TEXT         NOT NULL,
    folder_id                TEXT,
    filename                 TEXT,
    app_properties           JSONB        NOT NULL DEFAULT '{}'::jsonb,
    remote_file_id           TEXT,
    remote_url               TEXT,
    worker_id                TEXT,
    lease_expires_at         TIMESTAMPTZ,
    expires_at               TIMESTAMPTZ,
    error_message            TEXT,
    error_code               TEXT,
    attempt_count            INT          NOT NULL DEFAULT 0,
    version                  INT          NOT NULL DEFAULT 1,
    created_at               TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at               TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    CONSTRAINT chk_delivery_sessions_state CHECK (state IN
        ('initiated', 'uploading', 'completed', 'failed', 'expired')),
    CONSTRAINT uq_delivery_sessions_idempotency UNIQUE (deliverable_type, idempotency_key)
);

-- index for the dispatch hot-path "is there an active session for this idempotency?"
-- (FindByIdempotencyKey used on every Deliver call). UNIQUE above also covers
-- this lookup, but adding a state-btree keeps the WHERE state = ... filter
-- index-only when scanning for orphaned in-flight deliveries.
CREATE INDEX IF NOT EXISTS idx_delivery_sessions_state
    ON delivery_sessions (deliverable_type, state);

-- index for the lease-recovery sweep (ReclaimExpired). Same shape as
-- upload_jobs.lease_expires_at btree but on delivery_sessions.
CREATE INDEX IF NOT EXISTS idx_delivery_sessions_lease
    ON delivery_sessions (lease_expires_at)
 WHERE lease_expires_at IS NOT NULL;

COMMENT ON TABLE  delivery_sessions IS
    'Task 8/10: persists resumable-upload state for DeliveryProvider implementations. One row per (deliverable_type, idempotency_key). The Drive destination stores session_uri_encrypted (ciphertext of the Google Drive resumable URI), uploaded_bytes (resumable offset), app_properties (JSONB stamped onto the Drive file for app-property-based idempotency lookup), and remote_file_id/remote_url (the final DeliveryResult values). State machine: initiated → uploading → completed | failed | expired.';

COMMENT ON COLUMN delivery_sessions.deliverable_type IS
    'Provider key (e.g. google_drive today; future s3/minio/velox_ack). Determines the schema-level identifier namespace combined with idempotency_key.';

COMMENT ON COLUMN delivery_sessions.idempotency_key IS
    'Stable identifier of the logical delivery (typically post_target.id encoded). UNIQUE per deliverable_type so a retry of the same delivery cannot create a second row.';

COMMENT ON COLUMN delivery_sessions.session_uri_encrypted IS
    'Ciphertext (base64 of the SessionEncryptor output) of the resumable session URI returned by POST /upload/drive/v3/files. NULL after the upload completes or the row is expired.';

COMMENT ON COLUMN delivery_sessions.app_properties IS
    'JSONB key→value stamped onto the Drive file as appProperties.instaedit_delivery_id (= idempotency_key). Used for app-property-based idempotency lookup on cold restarts (row gone, Drive still has file).';

COMMENT ON COLUMN delivery_sessions.version IS
    'Optimistic-concurrency token. The repository updates WHERE version = $expected AND increment on success so concurrent workers cannot overwrite each other (parallel to upload_jobs lease_owner CAS but cleaner for state owned by the delivery worker).';
