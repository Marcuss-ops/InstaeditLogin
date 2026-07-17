-- Migration 039: idempotency_batch_replays
--
-- Cached response body for `resource_type = "drive_batch"`
-- idempotent POSTs. The idempotency_records table (migration 021)
-- holds the lookup hot-path (workspace_id + idempotency_key +
-- request_hash + resource_type); this side table holds the
-- serialized response JSON because drive_batch creates up to
-- N=200 upload_jobs in one POST and there's no single underlying
-- post row to re-fetch on replay (cf. resource_type = "post",
-- where replayIdempotentResource re-fetches Post by id).
--
-- =========================================================================
-- Design notes:
--
--   * response_payload JSONB NOT NULL — the entire serialized
--     DriveBatchImportResponse (folder_id echo, scheduled_count,
--     total_runtime_seconds, first_publish_at, last_scheduled_at,
--     next_page_token, entries[], note, etc.) so on replay the
--     handler can serve byte-identical JSON from the cache without
--     re-running the batch. JSONB (not TEXT) so Postgres can index
--     interesting fields later if needed (e.g., to audit which
--     batches had non-empty entries).
--
--   * idempotency_record_id BIGINT PRIMARY KEY REFERENCES
--     idempotency_records(id) ON DELETE CASCADE — one row per
--     batch idempotent POST. CASCADE keeps the side table from
--     outliving its parent record (e.g. when the workspace is
--     deleted by the CASCADE from workspaces through
--     idempotency_records).
--
--   * No created_at index exposed. The 24h-TTL idempotency_records
--     CRON sweep will CASCADE-clear side rows in the same sweep
--     because of the FK relationship. Side rows are otherwise
--     read-only and only ever accessed via the PRIMARY KEY on
--     replay.
-- =========================================================================

CREATE TABLE IF NOT EXISTS idempotency_batch_replays (
    idempotency_record_id BIGINT      PRIMARY KEY
        REFERENCES idempotency_records(id) ON DELETE CASCADE,
    response_payload      JSONB       NOT NULL,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
