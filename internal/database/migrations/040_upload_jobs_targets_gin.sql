-- Migration 040: upload_jobs.targets GIN index (Blocco #6 — Dashboard "Programmati" per-account view).
--
-- Without this index, the per-account query
--   SELECT ... WHERE targets @> '[<account_id>]'::jsonb AND user_id = ...
-- does a full sequential scan over upload_jobs, which scales linearly with
-- the total upload_jobs row count (currently 156 in dev). Once a real user
-- accumulates thousands of jobs across multiple platforms, the dashboard
-- "Programmati" widget would block the request thread per poll.
--
-- The GIN index on the jsonb `targets` column lets Postgres answer the
-- "@> '[N]'" containment query via the index alone for small N, then
-- combine with the user_id btree index via BitmapAnd. The default jsonb_ops
-- operator class supports `@>`, `?`, `?&`, `?|` so no extra opclass spec is
-- needed.
--
-- Targets is a JSONB array of platform_account .id values (always small
-- integers). Even when 100k upload_jobs accumulate, each row's `targets`
-- payload is <500 bytes so the GIN index size stays manageable.
--
-- Note: this index does NOT cover the empty/missing targets case (single-
-- file imports that target zero accounts). Those rows are excluded from
-- per-account lookups by the same JSONB containment predicate at query
-- time; they only cost a tiny row-pointer in the index, not a full JSONB
-- value, because the index keys are derived per array element.

CREATE INDEX IF NOT EXISTS idx_upload_jobs_targets_gin
    ON upload_jobs
    USING GIN (targets);
