-- External execution workers can create Calendar video events and safely
-- retry them by workspace-scoped event key.
CREATE UNIQUE INDEX IF NOT EXISTS idx_posts_worker_calendar_event_key
    ON posts (workspace_id, (metadata->>'worker_event_key'))
    WHERE metadata->>'worker_calendar_event'='true'
      AND metadata->>'worker_event_key' IS NOT NULL;
