-- 006_media_assets.sql
-- Taglio 3.2: presigned media upload assets.
--
-- The asset table is the single source of truth for "is this client-uploaded
-- media verified and ready to publish?". The flow is:
--   1. POST /api/v1/media/presign  → creates a row here (status=pending) +
--                                    returns a SigV4-signed PUT URL the
--                                    client uses to upload directly to S3.
--   2. PUT to signed URL           → handled by S3 directly (NOT by us).
--   3. POST /api/v1/media/{id}/complete → backend HEADs the S3 object to
--                                         verify size + content-type, then
--                                         transitions status=ready.
--   4. POST /api/v1/posts { media: [{ asset_id }] } → handler resolves
--                                                     asset_id → trusted
--                                                     S3 URL → stored in
--                                                     post.media_url.
--
-- Security properties:
--   - Posts API never accepts a user-controlled URL (only asset_ids).
--   - Per-asset ownership: only the user that requested the presign can
--     complete the upload (asset.user_id gates /complete).
--   - UUID PK prevents enumeration of pending assets.
--   - expires_at is enforced by the handler (status=expired or 410 Gone).
--
-- pgcrypto provides gen_random_uuid() in PG < 13. PG 13+ has it built-in
-- but CREATE EXTENSION IF NOT EXISTS is harmless and idempotent.
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE IF NOT EXISTS media_assets (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    upload_key    TEXT        NOT NULL UNIQUE,
    content_type  TEXT        NOT NULL,
    size_bytes    BIGINT      NOT NULL,
    status        TEXT        NOT NULL DEFAULT 'pending',  -- pending|ready|failed|expired
    sha256        TEXT,
    error_message TEXT,
    expires_at    TIMESTAMPTZ NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_media_assets_user_id  ON media_assets(user_id);
CREATE INDEX IF NOT EXISTS idx_media_assets_status   ON media_assets(status);
CREATE INDEX IF NOT EXISTS idx_media_assets_expires  ON media_assets(expires_at);
