ALTER TABLE youtube_target_publications
  ADD COLUMN IF NOT EXISTS copyright_status TEXT NOT NULL DEFAULT 'pending',
  ADD COLUMN IF NOT EXISTS copyright_message TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS copyright_rejection_reason TEXT,
  ADD COLUMN IF NOT EXISTS copyright_failure_reason TEXT,
  ADD COLUMN IF NOT EXISTS copyright_processing_status TEXT,
  ADD COLUMN IF NOT EXISTS copyright_licensed_content BOOLEAN NOT NULL DEFAULT FALSE,
  ADD COLUMN IF NOT EXISTS copyright_blocked_regions TEXT[] NOT NULL DEFAULT '{}',
  ADD COLUMN IF NOT EXISTS copyright_allowed_regions TEXT[] NOT NULL DEFAULT '{}',
  ADD COLUMN IF NOT EXISTS copyright_checked_at TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS copyright_check_error TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_yt_target_publications_copyright_pending
  ON youtube_target_publications (copyright_checked_at, id)
  WHERE youtube_video_id IS NOT NULL
    AND copyright_status IN ('pending', 'processing', 'error');
