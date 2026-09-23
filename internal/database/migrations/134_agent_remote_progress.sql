-- The control plane receives progress snapshots while polling the execution
-- plane. The snapshot is deliberately generic so new phases (script, clips,
-- voiceover, render, etc.) do not require a schema change.
ALTER TABLE agent_run_steps ADD COLUMN IF NOT EXISTS remote_status TEXT;
ALTER TABLE agent_run_steps ADD COLUMN IF NOT EXISTS remote_progress INTEGER;
ALTER TABLE agent_run_steps ADD COLUMN IF NOT EXISTS progress_json JSONB NOT NULL DEFAULT '{}'::jsonb;
