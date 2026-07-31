-- 079_media_assets_bucket.sql
-- Adds the `bucket` column on media_assets so each row is a complete
-- pointer to the S3 object (id, upload_key, bucket, content_type,
-- size_bytes, sha256). Without this column the publish worker had to
-- re-parse `media_url` to recover the bucket when minting a fresh
-- presigned GET URL at upload time — a fragile coupling between the
-- row layout and the URL host string.
--
-- The canonical S3_BUCKET env var still drives the StorageProvider
-- at runtime, but each row now records the bucket explicitly. This
-- also future-proofs a multi-bucket deployment (one bucket per
-- workspace) where media_assets.bucket is no longer a single
-- constant.
--
-- NULLABLE + no DEFAULT — avoids the full-table rewrite that
-- `NOT NULL DEFAULT ''` would force on a populated table. Legacy
-- rows stay NULL until either: (i) an explicit backfill updates
-- them, or (ii) the publish_worker resolver injects the runtime
-- bucket at usage time via ResolveForKey(bucket, key).
--
-- Idempotent re-run via ADD COLUMN IF NOT EXISTS + CREATE INDEX
-- IF NOT EXISTS.

ALTER TABLE media_assets
    ADD COLUMN IF NOT EXISTS bucket TEXT;

CREATE INDEX IF NOT EXISTS idx_media_assets_bucket
    ON media_assets(bucket);
