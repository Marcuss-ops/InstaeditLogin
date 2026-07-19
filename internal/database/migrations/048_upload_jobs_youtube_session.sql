-- 048_upload_jobs_youtube_session.sql
-- P1#5 — persist YouTube resumable-upload session state on each
-- upload_job so a crashed / restarted worker can resume the upload
-- from the byte the previous worker left off (instead of restreaming
-- the whole file from byte 0).
--
-- Adds five nullable columns to upload_jobs. All five are NULL on a
-- fresh row (no session yet); they're stamped by processPublishJob
-- right after initiateResumableSession issues the upload URI.
--
--   youtube_session_uri           TEXT (nullable)
--     The Location header from POST /upload/youtube/v3/videos?uploadType=resumable.
--     Treat as credential-adjacent: anyone with this URI can resume
--     the upload until youtube_session_expires_at. NEVER logged in
--     full — see internal/worker/redactYouTubeSessionURI for the
--     "first-8-chars + …" shape used in operator-facing traces.
--   youtube_session_offset        BIGINT (nullable)
--     Bytes the server has acknowledged; updated by the worker
--     after every successful chunk PUT. Default NULL = no progress.
--   youtube_session_expires_at    TIMESTAMPTZ (nullable)
--     YouTube-side expiry timestamp (typically 1 week, but
--     server-controlled). The worker checks before attempting a
--     ResumeSession probe; if NOW() >= expires_at the saved URI is
--     treated as dead and initiateResumableSession is called fresh.
--   youtube_chunk_size            BIGINT (nullable)
--     The chunkSize the worker used when it stamped youtube_session_offset.
--     Persisted so a restarted worker picks up the same chunk size
--     (avoids mixed-size PUTs which YouTube's resumable protocol
--     accepts but is operationally a foot-gun).
--   youtube_last_chunk_at         TIMESTAMPTZ (nullable)
--     Wall-clock of the last successful PUT against this session.
--     Used by the dashboard's "X MB uploaded N seconds ago" widget.
--
-- The ReclaimExpiredLeases SQL in internal/repository/upload_job_repo.go
-- is extended (in the same commit) to ALSO null the 5 youtube_*
-- columns when it returns a leased → pending row, so a recovered
-- row doesn't carry a stale session URI from its dead predecessor.

ALTER TABLE upload_jobs
    ADD COLUMN IF NOT EXISTS youtube_session_uri        TEXT,
    ADD COLUMN IF NOT EXISTS youtube_session_offset     BIGINT,
    ADD COLUMN IF NOT EXISTS youtube_session_expires_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS youtube_chunk_size         BIGINT,
    ADD COLUMN IF NOT EXISTS youtube_last_chunk_at      TIMESTAMPTZ;

-- Partial index for the dashboard's "in-flight YouTube uploads"
-- widget and any future per-session-idleness reaper. The WHERE
-- clause keeps the index ~ lease-active-row count, not total row
-- count — important once upload_jobs accumulates thousands of
-- terminal-state rows.
CREATE INDEX IF NOT EXISTS idx_upload_jobs_youtube_session_active
    ON upload_jobs (youtube_last_chunk_at)
    WHERE youtube_session_uri IS NOT NULL
      AND status = 'leased';

COMMENT ON COLUMN upload_jobs.youtube_session_uri        IS 'Resumable-upload Location header from POST /upload/youtube/v3/videos?uploadType=resumable. Treat as credential-adjacent: redact from logs (see redactYouTubeSessionURI).';
COMMENT ON COLUMN upload_jobs.youtube_session_offset     IS 'Bytes the YouTube server has acknowledged for the current session. NULL = no progress.';
COMMENT ON COLUMN upload_jobs.youtube_session_expires_at IS 'YouTube-side expiry of the session URI. If NOW() >= this, the worker treats the session as dead and re-initiates.';
COMMENT ON COLUMN upload_jobs.youtube_chunk_size         IS 'ChunkSize (bytes) the worker used when it last stamped youtube_session_offset. Persisted so a restart uses the same size and avoids mixed-size PUTs.';
COMMENT ON COLUMN upload_jobs.youtube_last_chunk_at      IS 'Wall-clock of the last successful chunk PUT against this session. Dashboard refresh + future per-session reaper.';
