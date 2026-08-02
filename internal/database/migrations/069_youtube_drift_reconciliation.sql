-- =============================================================================
-- Migration 069: youtube_target_publications drift-reconciliation telemetry
-- =============================================================================
-- Periodic YouTube-drift reconciliation worker (commit drift-recon).
--
-- The worker reads `youtube_target_publications` rows that have already
-- been uploaded to YouTube (`youtube_video_id IS NOT NULL`) AND whose
-- reconciliation record is stale (reconciled_at NULL OR
-- reconciled_at < NOW() - threshold), then compares the local row against
-- the live YouTube Data API (videos.list?part=status,processingDetails):
--
--   * privacyStatus         — what YouTube says NOW vs desired_privacy
--   * processingStatus      — what YouTube says NOW vs youtube_processing_status
--   * publishAt             — what YouTube says NOW vs publish_at
--
-- On drift, the worker issues songs.update to push the canonical DB state
-- to YouTube, then stamps reconciled_at + reconcile_status. On error, the
-- worker stamps last_reconcile_error + reconcile_status='error' and
-- schedules a follow-up tick.
--
-- COLUMNS (all ADDS, all nullable so pre-069 rows round-trip cleanly):
--   * reconciled_at              — TIMESTAMPTZ, last successful reconcile
--                                   timestamp. COALESCE-preserved by the
--                                   worker's MarkReconciled so a re-tick
--                                   against a steady-state row doesn't keep
--                                   bumping the timestamp.
--   * reconcile_status           — TEXT, enum-style: 'ok', 'noop',
--                                   'error' (no constraint enforcement —
--                                   model-level + test-level pin canonical
--                                   values).
--   * last_reconcile_error       — TEXT, last error message observed
--                                   during reconcile. Stays SET under
--                                   'ok' transitions deliberately so the
--                                   operator triage dashboard can read
--                                   "last error + cleared at field X"
--                                   in a single SELECT.
--   * last_reconciled_privacy    — TEXT, snapshot of what YouTube said on
--                                   the last reconcile. Operator-trace
--                                   breadcrumb for "DB says X but YouTube
--                                   says Y AND a previous reconcile
--                                   observed Z".
--
-- INDEX design:
--   The ListNeedsReconcile query is the canonical hot-path
--   (SELECT ... WHERE youtube_video_id IS NOT NULL
--         AND (reconciled_at IS NULL OR reconciled_at < NOW()||$1)
--    ORDER BY reconciled_at ASC NULLS FIRST LIMIT $2)
--
--   A partial index keyed on `reconciled_at ASC NULLS FIRST` filtered to
--   `youtube_video_id IS NOT NULL` keeps the working set bounded to ONLY
--   rows the worker can act on (no upload — no work). Rows in the
--   'publisher still uploading' sub-states with `youtube_video_id = NULL`
--   are excluded by the partial predicate so they don't bloat the index.
--   `NULLS FIRST` matches the ORDER BY so Postgres can return the
--   never-reconciled rows at the head of the index without a sort node.
--
--   Migration is ADDITIVE — no schema fork, no renames. Pre-069 rows
--   round-trip with NULL on every new column; the worker's MarkReconciled
--   stamps them in the +1 transition path.

ALTER TABLE youtube_target_publications
    ADD COLUMN IF NOT EXISTS reconciled_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS reconcile_status TEXT,
    ADD COLUMN IF NOT EXISTS last_reconcile_error TEXT,
    ADD COLUMN IF NOT EXISTS last_reconciled_privacy TEXT;

CREATE INDEX IF NOT EXISTS idx_yt_pubs_drift_reconcile
    ON youtube_target_publications (reconciled_at ASC NULLS FIRST)
    WHERE youtube_video_id IS NOT NULL;
