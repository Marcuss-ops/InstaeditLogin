-- +goose Up
-- 059_youtube_quota_daily.sql — pre-call gate tracking for the YouTube
-- 2026 videos.insert bucket. One row per UTC date; calls/errors/limit
-- are the operator-facing knobs.
--
-- Usage: the publish_worker pre-call gate calls
--   SELECT calls, "limit" FROM youtube_quota_daily WHERE date=CURRENT_DATE FOR UPDATE
-- in a tx, increments calls if calls < limit, otherwise stamps a
-- retry_wait with retry_after_seconds = seconds_until_next_UTC_midnight.
CREATE TABLE IF NOT EXISTS youtube_quota_daily (
    date DATE PRIMARY KEY,
    calls INT NOT NULL DEFAULT 0,
    errors INT NOT NULL DEFAULT 0,
    "limit" INT NOT NULL DEFAULT 300,
    last_reset_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

COMMENT ON TABLE youtube_quota_daily IS
'Daily YouTube videos.insert quota usage per UTC date. Guarded by '
'the publish_worker pre-call gate. 1 videos.insert = 1 bucket unit '
'(Google 2026 quota model). The publisher refuses to call videos.insert '
'when calls >= limit and writes metadata.retry_after_seconds on the '
'affected post_target pointing at next UTC midnight.';

CREATE INDEX IF NOT EXISTS idx_youtube_quota_daily_last_reset_at
    ON youtube_quota_daily(last_reset_at);

-- +goose Down
DROP TABLE IF EXISTS youtube_quota_daily;
