-- 081_publish_legacy_url_to_asset.sql
-- Migration companion to the resolver's runtime reverse-lookup
-- (internal/services/media_resolver.go::extractUploadKeyFromMediaURL +
-- MediaAssetStore.FindByUploadKey). This migration is the AUTHORITATIVE
-- production cutover for legacy posts that pre-date migration 080 (and
-- therefore have only posts.media_url set, no posts.media_asset_id).
--
-- Going forward the runtime fallback handles:
--   - rows inserted after migration 081 was applied (already canonical)
--   - rows whose media_url is corrupted (resolves to ErrMediaURLNoMatchingAsset)
-- Migration 081's job is to maximise the set of legacy rows that the
-- runtime fallback NEVER has to consider, by populating media_asset_id
-- + storage_object_key + bucket on every row whose URL is parseable.
--
-- Idempotency strategy:
--   - WHERE clause restricts to posts.media_asset_id IS NULL, so the
--     migration is a no-op on re-run for any row that has already been
--     promoted to canonical source-of-truth.
--   - The CTE parses the upload_key once. The outer UPDATE joins to
--     media_assets on upload_key (UNIQUE index, O(1) lookup).
--   - The bucket is NOT extracted from the URL — it is read directly
--     from media_assets.bucket (migration 079). Posts that pre-date 080
--     have NULL bucket; the migration sets bucket = ma.bucket so the
--     row is fully populated and the resolver can run its canonical
--     branch on the first replay tick.
--
-- URL SHAPE assumption:
--   - Path-style S3 addressing: https://{host}/{bucket}/{upload_key}?X-Amz-...
--   - The presign handler (pkg/api/media_handlers.go::AssetURL) builds
--     the URL exactly this way via S3Provider.GetObject + StoragePath.
--   - Virtual-hosted addressing (https://{bucket}.{host}/{key}) is NOT
--     supported today. If the deployment ever migrates from on-prem
--     MinIO to AWS S3 native, re-adapt this regex accordingly.
--
-- TEST rows that this migration will skip:
--   - posts with no media_url (NULL or empty)
--   - posts where the URL parse fails (no scheme:// prefix; or path
--     doesn't have at least 2 path segments after the host)
--   - posts where the parsed upload_key matches no media_assets row
--     (cleanup pass already hard-deleted the asset, or operator
--     hand-edited the URL). These rows remain with media_asset_id
--     IS NULL and will surface ErrMediaURLNoMatchingAsset at the next
--     worker tick — that's the contract this migration was designed
--     not to violate.
--
-- ROW COUNT side-effect:
--   The UPDAATE itself does not return rows; the CTE inner SELECT
--   returns ALL legacy posts (with NULL parse filter applied). To
--   audit post-migration, run separately:
--     SELECT count(*) FROM posts WHERE media_asset_id IS NOT NULL
--       AND storage_object_key IS NOT NULL;
--   The diff vs. pre-migration count quantifies the backfill.

BEGIN;

WITH legacy_posts AS (
    -- Step 1: select every post whose media_url is parseable as path-style
    -- S3 (https?://host/bucket/{upload_key}?...) AND whose media_asset_id
    -- is still NULL (idempotency guard). The CTE materialises the parse
    -- once and reduces redundant work in step 2.
    SELECT
        p.id AS post_id,
        -- Strip scheme + host + bucket prefix from posts.media_url, then
        -- strip the SigV4 query string. Resolves to the upload_key path
        -- segment that matches media_assets.upload_key.
        regexp_replace(
            regexp_replace(p.media_url, '^https?://[^/]+/[^/]+/', ''),
            '\?.*$', ''
        ) AS parsed_upload_key
    FROM posts p
    WHERE p.media_asset_id IS NULL
      AND p.media_url IS NOT NULL
      AND p.media_url <> ''
      AND p.media_url ~ '^https?://'
      -- 4th forward-slash AFTER the scheme sep makes the URL have
      -- at least one path segment after the bucket (i.e. uploads/1/foo
      -- shape): scheme="https" + "://" + "host/" + "bucket/" + "key".
      -- Without this filter, "https://host/" or "https://host/bucket/"
      -- would yield empty parsed_upload_key; the JOIN below would then
      -- skip them naturally (no media_assets row with empty upload_key)
      -- but defensive WHERE here makes the log SQL cheaper to reason
      -- about during audit.
      AND p.media_url LIKE '%/%/%/%/%'
)
UPDATE posts p
SET
    media_asset_id    = ma.id,
    storage_object_key = ma.upload_key,
    bucket            = ma.bucket
FROM legacy_posts lp, media_assets ma
WHERE p.id = lp.post_id
  AND ma.upload_key = lp.parsed_upload_key;

COMMIT;
