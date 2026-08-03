-- 092_media_assets_probe.sql
-- Add ffprobe-derived metadata to media_assets so the live-streaming
-- wizard (and the future Media Library) can show duration, resolution,
-- FPS, audio presence and codecs for each ready asset.
--
-- NULL numeric/boolean columns mean "not probed yet" — assets uploaded
-- via the direct presign flow are never probed; the upload worker runs
-- ffprobe on the assets it ingests (Drive/Velox) right after the
-- verification pass. probed_at is the stamp of the last successful
-- probe. The worker is best-effort: when ffprobe is unavailable the
-- columns stay NULL and the asset remains usable (compatibility is
-- simply unknown rather than false).
ALTER TABLE media_assets
    ADD COLUMN IF NOT EXISTS duration_seconds DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS width INTEGER,
    ADD COLUMN IF NOT EXISTS height INTEGER,
    ADD COLUMN IF NOT EXISTS fps DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS has_audio BOOLEAN,
    ADD COLUMN IF NOT EXISTS video_codec TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS audio_codec TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS probed_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_media_assets_ready_user
    ON media_assets (user_id, status, created_at DESC)
    WHERE status = 'ready';
