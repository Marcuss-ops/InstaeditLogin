-- New Dark Editor sessions are private unless the operator explicitly
-- selects public or unlisted at publish time.
ALTER TABLE youtube_video_edits
    ALTER COLUMN desired_privacy SET DEFAULT 'private';
