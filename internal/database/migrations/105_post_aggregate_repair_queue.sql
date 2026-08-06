-- Migration 105: targeted post aggregate repair queue
--
-- Normal target transitions already recompute posts.status in their
-- transaction. This queue is a durable safety net for targeted repair: a
-- changed target marks only its parent post dirty, so the repair worker never
-- needs to scan the complete posts table.

CREATE TABLE IF NOT EXISTS post_aggregate_repair_queue (
    post_id BIGINT PRIMARY KEY REFERENCES posts(id) ON DELETE CASCADE,
    queued_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_post_aggregate_repair_queue_queued_at
    ON post_aggregate_repair_queue (queued_at, post_id);

-- Seed every existing parent once during rollout. This preserves repair
-- coverage for drift that predates the trigger; the queue remains bounded and
-- deduplicated by its primary key.
INSERT INTO post_aggregate_repair_queue (post_id)
SELECT id FROM posts
ON CONFLICT (post_id) DO NOTHING;

CREATE OR REPLACE FUNCTION enqueue_post_aggregate_repair()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        INSERT INTO post_aggregate_repair_queue (post_id)
        VALUES (NEW.post_id)
        ON CONFLICT (post_id) DO NOTHING;
    ELSIF NEW.status IS DISTINCT FROM OLD.status THEN
        INSERT INTO post_aggregate_repair_queue (post_id)
        VALUES (NEW.post_id)
        ON CONFLICT (post_id) DO NOTHING;
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS post_targets_enqueue_aggregate_repair
    ON post_targets;

CREATE TRIGGER post_targets_enqueue_aggregate_repair
    AFTER INSERT OR UPDATE OF status ON post_targets
    FOR EACH ROW
    EXECUTE FUNCTION enqueue_post_aggregate_repair();
