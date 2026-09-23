-- Persist execution-plane references on agent steps. This keeps recovery
-- restart-safe without storing worker-specific addresses or credentials.
ALTER TABLE agent_run_steps ADD COLUMN IF NOT EXISTS remote_job_id TEXT;
ALTER TABLE agent_run_steps ADD COLUMN IF NOT EXISTS idempotency_key TEXT;

CREATE INDEX IF NOT EXISTS agent_run_steps_remote_job_idx
    ON agent_run_steps (remote_job_id)
    WHERE remote_job_id IS NOT NULL;
