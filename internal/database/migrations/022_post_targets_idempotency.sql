-- Migration 022 — post_targets idempotency constraints (Taglio 4.7 LEVEL 2)
--
-- Extends post_targets with a column to hold the per-target provider-side
-- idempotency key. The key is computed by the worker after the atomic
-- claim (deterministic: SHA-256 prefix of `("v1:" + post_id + ":" +
-- platform_account_id)`) so retries reuse the same key — the platform's
-- native API dedup catches the duplicate publish on its end and we
-- never double-post.
--
-- Two UNIQUE-ish constraints + one lookup index — chosen for the
-- following reasons:
--
-- 1. UNIQUE(post_id, platform_account_id)
--    Defense-in-depth against accidental duplicate fan-out. The Create
--    path (PostRepository.Create + Save) already prevents a single
--    handler from inserting two targets for the same post+account, but
--    a future bug could regress this; the constraint turns the regression
--    into a 23505 dispatch → ErrPostTargetDuplicate (mapped to 409) instead
--    of silent duplicate publishing.
--
-- 2. PARTIAL UNIQUE INDEX (platform_account_id, provider_idempotency_key)
--    WHERE provider_idempotency_key IS NOT NULL
--    Provider-level dedup at OUR storage layer. A regular UNIQUE
--    constraint would treat NULLs as values and reject rows where two
--    pre-existing targets both have NULL keys — breaking the migration
--    on the existing-data set. The partial index lets us ADD COLUMN
--    with NULL default (backward-compat) and only enforce uniqueness
--    once the worker has stamped a key.
--
-- 3. Worker lookup index. The only "look up by key" query the
--    worker emits today is keyed on (platform_account_id,
--    provider_idempotency_key), which the unique composite index
--    above already serves. No separate single-column index here.
--    (If a future query needs "find ALL targets with this key across
--    tenants" — admin tooling — add a dedicated non-unique index
--    under its own migration so the write-cost is bounded and the
--    index is justified by a real workload.)
--
--    The worker's normal retry flow goes ClaimQueuedTarget (UPDATE) →
--    EnsureProviderIdempotencyKey (UPDATE) → Publish — it never
--    INSERTs a duplicate target row, so this constraint catches only
--    degenerate runbook-style INSERTs. Its real value is the safety
--    net: any future code path that INSERTs a new target with the
--    same (account, key) tuple will get a SQLSTATE 23505 dispatch →
--    ErrProviderIdempotencyConflict (mapped to 409) instead of a
--    silent duplicate publish.
--
-- 3. NON-UNIQUE partial INDEX (provider_idempotency_key)
--    WHERE provider_idempotency_key IS NOT NULL
--    Worker lookup index for "is there an unfinished retry for this key
--    on this account?". Keep it NON-UNIQUE (only the composite above
--    carries the uniqueness check) so we don't bloat writes with a
--    second unique constraint. Pre-existing rows have NULL key — the
--    WHERE clause excludes them, so the index is small at migration
--    time and only grows as the worker stamps keys.
--
-- Idempotency contract review notes:
--   * Same key + same payload on the platform → provider returns the
--     cached media_id (we honour it, no double-publish).
--   * Same key + different payload on the platform → provider returns
--     409 / mismatch → typed pq.Error dispatch in repository maps to
--     ErrProviderIdempotencyConflict (mapped to 409 in handler).
--   * Cross-account same key is NOT a conflict (UNIQUE is composite).

ALTER TABLE post_targets
  ADD COLUMN IF NOT EXISTS provider_idempotency_key TEXT;

-- 1. Defense-in-depth UNIQUE on (post_id, platform_account_id).
--    Existing data: pre-existing duplicate rows would block this. The
--    migration assumes the application invariant already holds; if
--    not, a follow-up must dedupe before this constraint can be added.
--    Wrapped in a DO block so the migration can be applied twice
--    (idempotency gate) without raising "relation already exists".
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conname = 'post_targets_post_id_platform_uniq'
  ) THEN
    ALTER TABLE post_targets
      ADD CONSTRAINT post_targets_post_id_platform_uniq
      UNIQUE (post_id, platform_account_id);
  END IF;
END $$;

-- 2. Partial UNIQUE on (platform_account_id, provider_idempotency_key)
--    WHERE provider_idempotency_key IS NOT NULL. NULLs are excluded
--    from the index, so rows that haven't been keyed yet coexist
--    freely. The constraint kicks in only for keyed rows.
--    This composite index also serves the only "look up by key"
--    query the worker emits today (WHERE platform_account_id = ? AND
--    provider_idempotency_key = ?), so a redundant
--    non-unique single-column index would just bloat writes. If a
--    future query needs "find ALL targets with this key across
--    tenants" (admin tooling), add a separate dedicated index.
CREATE UNIQUE INDEX IF NOT EXISTS post_targets_platform_provider_uniq
  ON post_targets (platform_account_id, provider_idempotency_key)
  WHERE provider_idempotency_key IS NOT NULL;
