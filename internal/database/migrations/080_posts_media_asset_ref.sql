-- 080_posts_media_asset_ref.sql
-- Migration that decouples post.media_url from the canonical S3
-- source-of-truth. The presigned GET URL is volatile (it expires)
-- so persisting it on the post row creates a class of "scheduled post
-- runs overnight, URL expired, MinIO 403" bugs (the followup's
-- stated motivation).
--
-- Schema additions (all NULLABLE, no DEFAULT, no value rewrite):
--   * posts.media_asset_id     UUID REFERENCES media_assets(id)
--                                ON DELETE SET NULL — the bind-back
--                                to the Source Of Truth row. A new
--                                ingest ETA on a media_asset updates
--                                behaviour downstream without
--                                rewriting the post row.
--   * posts.storage_object_key TEXT  — mirror of media_assets.upload_key
--                                       at insert time. The resolver
--                                       reads this so a stale URL on
--                                       media_url still works.
--   * posts.bucket             TEXT  — mirror of media_assets.bucket
--                                       at insert time. Same rationale.
--
-- posts.media_url            TEXT  — DEPRECATED for new writes. Kept
--                                       nullable for back-compat with
--                                       existing rows + clients reading
--                                       the post shape (BFF response
--                                       still includes media_url as a
--                                       DERIVED value, never the source
--                                       of truth). The publish worker
--                                       must regenerate a fresh
--                                       presigned URL at upload time
--                                       via MediaDownloadResolver.
--
-- No DO-block backfill: legacy rows where media_url is set but
-- media_asset_id + storage_object_key + bucket are NULL are
-- handled at runtime by the resolver's ResolveForKey path
-- (signature: (bucket, key) → fresh presigned GET URL). This
-- avoids coupling the migration to runtime config storage that
-- doesn't exist on this codebase.
--
-- Idempotent re-run via ADD COLUMN IF NOT EXISTS + CREATE INDEX
-- IF NOT EXISTS.

ALTER TABLE posts
    ADD COLUMN IF NOT EXISTS media_asset_id     UUID REFERENCES media_assets(id) ON DELETE SET NULL;

ALTER TABLE posts
    ADD COLUMN IF NOT EXISTS storage_object_key TEXT;

ALTER TABLE posts
    ADD COLUMN IF NOT EXISTS bucket             TEXT;

-- Composite partial index on workspace_id + media_asset_id drives
-- the "find posts bound to a given asset" lookup the asset re-upload
-- path needs (so we can rewrite the storage_object_key on a new
-- upload without re-stamping the post row's id). The partial
-- predicate keeps the index size linear in the count of posts that
-- actually have a media_asset_id.
CREATE INDEX IF NOT EXISTS idx_posts_media_asset_id
    ON posts(workspace_id, media_asset_id)
    WHERE media_asset_id IS NOT NULL;

-- Composite partial index on workspace_id + storage_object_key
-- supports the resolver's legacy-path lookup: "given a video URL,
-- find the post row that owns it" for re-publishing / auditing.
CREATE INDEX IF NOT EXISTS idx_posts_storage_object_key
    ON posts(workspace_id, storage_object_key)
    WHERE storage_object_key IS NOT NULL;
