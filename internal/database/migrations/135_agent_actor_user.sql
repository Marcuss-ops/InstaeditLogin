-- Keep the human principal on a durable agent run so server-side recovery
-- workers can enforce workspace membership when they advance the workflow.
ALTER TABLE agent_runs ADD COLUMN IF NOT EXISTS actor_user_id BIGINT
    REFERENCES users(id) ON DELETE SET NULL;

UPDATE agent_runs ar
SET actor_user_id = ak.created_by
FROM api_keys ak
WHERE ar.actor_user_id IS NULL AND ar.actor_key_id = ak.id;

UPDATE agent_runs ar
SET actor_user_id = w.owner_id
FROM workspaces w
WHERE ar.actor_user_id IS NULL AND ar.workspace_id = w.id;

CREATE INDEX IF NOT EXISTS agent_runs_recovery_idx
    ON agent_runs (updated_at, id)
    WHERE status = 'running';
