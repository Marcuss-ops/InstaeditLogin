-- A scheduled generation is represented by its durable Calendar draft. This
-- prevents duplicate intent cards when a client retries after a lost response.
CREATE UNIQUE INDEX IF NOT EXISTS idx_posts_agent_video_schedule_key
    ON posts (workspace_id, (metadata->>'agent_schedule_key'))
    WHERE metadata->>'agent_schedule_key' IS NOT NULL;
