-- 124_youtube_quota_buckets.sql
-- YouTubeQuotaManager bucket dimension. Migration 059 created a
-- single-bucket counter keyed by date only. The Google 2026 quota
-- model (effective 2026-06-01) splits YouTube Data API v3 usage into
-- three INDEPENDENT daily buckets:
--
--   video_uploads  → videos.insert                 (default 100 calls/day)
--   searches       → search.list                   (default 100 calls/day)
--   general        → videos.update, videos.list,
--                    thumbnails.set, channels.list (default 10000 units/day)
--
-- This migration evolves youtube_quota_daily to a (date, bucket) key so
-- each bucket resets independently and a quota_exceeded in one bucket
-- does not block the others. It converges from BOTH legacy states:
--   * a fresh database, where migration 059's body ran whole and its
--     "+goose Down" DROP TABLE section executed right after CREATE;
--   * an existing database that still carries the legacy single-bucket
--     table (date-only PRIMARY KEY, "limit" default 300).
-- Existing legacy rows migrate to bucket='video_uploads' and keep
-- their stored "limit" (the repository honours inbound bumps and never
-- shrinks a stored ceiling, so operator constraints are preserved).

CREATE TABLE IF NOT EXISTS youtube_quota_daily (
    date DATE NOT NULL,
    bucket TEXT NOT NULL DEFAULT 'video_uploads',
    calls INT NOT NULL DEFAULT 0,
    errors INT NOT NULL DEFAULT 0,
    "limit" INT NOT NULL DEFAULT 100,
    last_reset_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (date, bucket)
);

-- Legacy table (date-only PK): add the bucket column. Existing rows get
-- bucket='video_uploads' via the column default. No-op on a fresh DB.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = current_schema()
          AND table_name = 'youtube_quota_daily'
          AND column_name = 'bucket'
    ) THEN
        ALTER TABLE youtube_quota_daily
            ADD COLUMN bucket TEXT NOT NULL DEFAULT 'video_uploads';
    END IF;
END $$;

-- Rebuild the primary key to (date, bucket) when a legacy date-only PK
-- is present. Idempotent: the fresh-DB schema already has the
-- two-column PK, so it is left untouched.
DO $$
DECLARE
    pk_cols TEXT[];
BEGIN
    SELECT array_agg(a.attname ORDER BY array_position(i.indkey::int2[], a.attnum)) INTO pk_cols
      FROM pg_index i
      JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = ANY(i.indkey)
     WHERE i.indrelid = 'youtube_quota_daily'::regclass
       AND i.indisprimary;

    IF pk_cols IS NULL THEN
        ALTER TABLE youtube_quota_daily ADD PRIMARY KEY (date, bucket);
    ELSIF NOT (pk_cols = ARRAY['date', 'bucket']) THEN
        ALTER TABLE youtube_quota_daily DROP CONSTRAINT youtube_quota_daily_pkey;
        ALTER TABLE youtube_quota_daily ADD PRIMARY KEY (date, bucket);
    END IF;
END $$;

-- The repository always passes the bucket limit explicitly; the column
-- default is only a safety net for ad-hoc INSERTs. 300 (the old
-- default) must not leak into the 2026 model, so pin it to 100.
ALTER TABLE youtube_quota_daily ALTER COLUMN "limit" SET DEFAULT 100;

-- Bucket allow-list at the schema layer (same discipline as migration
-- 060's status CHECK): a typo in a Go bucket constant surfaces at
-- INSERT time instead of silently creating a fourth bucket Google does
-- not know about.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
         WHERE conname = 'youtube_quota_daily_bucket_ck'
           AND conrelid = 'youtube_quota_daily'::regclass
    ) THEN
        ALTER TABLE youtube_quota_daily
            ADD CONSTRAINT youtube_quota_daily_bucket_ck
            CHECK (bucket IN ('video_uploads', 'searches', 'general'));
    END IF;
END $$;

COMMENT ON TABLE youtube_quota_daily IS
'Daily YouTube Data API v3 quota usage per (UTC date, bucket). Buckets '
'match the Google 2026 quota model: video_uploads (videos.insert, '
'default 100 calls/day), searches (search.list, default 100 calls/day), '
'general (videos.update / videos.list / thumbnails.set / channels.list, '
'default 10000 units/day). Guarded by the YouTubeQuotaManager pre-call '
'gate (internal/services/youtube_quota_manager.go).';

CREATE INDEX IF NOT EXISTS idx_youtube_quota_daily_last_reset_at
    ON youtube_quota_daily(last_reset_at);
