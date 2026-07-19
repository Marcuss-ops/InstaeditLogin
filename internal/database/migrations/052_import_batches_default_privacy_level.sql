-- Migration 052: P1 refactor — add default_privacy_level to import_batches + upload_jobs.
--
-- Context (P1 Drive batch refactor):
--   The P1#7 producer handler accepted a generic request envelope with
--   schedule + targets, but NO per-batch privacy level. The publish_worker
--   later defaulted to "private" for YouTube + "PUBLIC_TO_EVERYONE" for
--   TikTok when the per-post payload didn't carry a PrivacyLevel, which
--   silently surprised operators who wanted their scheduled folder batch
--   to land as a public post.
--
--   This migration adds a single NOT NULL column with a safe default
--   ('private', matches YouTube's safe-default semantic) + a CHECK
--   constraint that enforces the YouTube allowlist at the DB layer so a
--   misconfigured handler cannot persist an invalid value. The same
--   column is added to upload_jobs so the crawler can stamp the
--   per-job privacy at fan-out time without a JOIN back to the batch
--   header (the publish_worker reads upload_job.default_privacy_level
--   when building the per-platform PublishPayload — wired in a follow-up
--   commit; this commit lands the schema + the producer-side contract).
--
--   We pick the YouTube allowlist (public/unlisted/private) as the
--   canonical set because:
--     1. It's the smallest meaningful set — TikTok's uppercase values
--        are normalised by the publish_worker before the row is read;
--        LinkedIn's PUBLIC/CONNECTIONS are normalised too.
--     2. 'private' is the safest default for any cross-platform batch
--        (operator must opt in to public explicitly).
--     3. Rejecting 'PUBLIC_TO_EVERYONE' at the DB level keeps the
--        producer contract simple — operators paste lowercase YouTube
--        values and the publish_worker normalises the rest.
--
--   NOT NULL + DEFAULT is the migration shape from D3: every existing
--   row gets 'private' silently on the ALTER, no operator backfill
--   required. If a deploy needs stricter safety, swap to NOT NULL
--   without DEFAULT in a future commit (would require UPDATE-then-ALTER).

ALTER TABLE import_batches
    ADD COLUMN default_privacy_level TEXT NOT NULL DEFAULT 'private'
    CHECK (default_privacy_level IN ('public', 'unlisted', 'private'));

ALTER TABLE upload_jobs
    ADD COLUMN default_privacy_level TEXT NOT NULL DEFAULT 'private'
    CHECK (default_privacy_level IN ('public', 'unlisted', 'private'));

-- Backfill index: the publish_worker integration follow-up will query
-- upload_jobs by (status, default_privacy_level) to surface "posts
-- scheduled with public privacy" on the dashboard. The partial index
-- only covers the non-private values (the "operator opted-in" set)
-- so the index stays small even at 100k+ upload_jobs.
CREATE INDEX idx_upload_jobs_default_privacy_public
    ON upload_jobs (status, scheduled_at)
    WHERE default_privacy_level = 'public';
