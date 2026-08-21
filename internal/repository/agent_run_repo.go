package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// AgentRun is the persisted record of a single agent execution. It
// carries only REFERENCES (youtube_video_id, editor_session_id) — never
// binary assets. Files live in the Media Library; the run row keeps
// pointers so operators can trace which video/session a run touched.
type AgentRun struct {
	ID              string
	WorkspaceID     int64
	ActorKeyID      *int64
	Goal            string
	YouTubeVideoID  string
	EditorSessionID string
	Status          string
	CurrentStep     string
	IdempotencyKey  string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	CompletedAt     *time.Time
}

// AgentRunStep is a single tool invocation within a run, with its JSON
// input/output references and failure details. The JSON columns hold
// references (media_id, project_id), never base64 assets.
type AgentRunStep struct {
	ID           string
	RunID        string
	ToolName     string
	Status       string
	InputJSON    []byte
	OutputJSON   []byte
	ErrorCode    string
	ErrorMessage string
	StartedAt    time.Time
	CompletedAt  *time.Time
}

// AgentRunRepository persists agent_runs and agent_run_steps (migration
// 129). The Agent Gateway never touches the database directly — it
// records runs through the InstaeditLogin REST API, which uses this
// repository.
type AgentRunRepository struct {
	db *sql.DB
}

// NewAgentRunRepository constructs an AgentRunRepository bound to the
// supplied *sql.DB.
func NewAgentRunRepository(db *sql.DB) *AgentRunRepository {
	return &AgentRunRepository{db: db}
}

// CreateRun inserts a new run, or reuses the existing row when a run
// with the same (workspace_id, idempotency_key) already exists. The
// ON CONFLICT branch is a true no-op (updated_at = updated_at) so a
// network retry of a run creation never duplicates the row. The id is
// returned on both paths.
func (r *AgentRunRepository) CreateRun(ctx context.Context, run *AgentRun) error {
	var actorKeyID any
	if run.ActorKeyID != nil {
		actorKeyID = *run.ActorKeyID
	}
	// editor_session_id is a UUID column; pass nil for empty.
	var sessionID any
	if run.EditorSessionID != "" {
		sessionID = run.EditorSessionID
	}
	err := r.db.QueryRowContext(ctx, `
		INSERT INTO agent_runs (
			workspace_id, actor_key_id, goal, youtube_video_id,
			editor_session_id, status, current_step, idempotency_key
		) VALUES ($1, $2, $3, NULLIF($4, ''), $5, $6, NULLIF($7, ''), $8)
		ON CONFLICT (workspace_id, idempotency_key) DO UPDATE
			SET updated_at = agent_runs.updated_at
		RETURNING id, created_at, updated_at`,
		run.WorkspaceID, actorKeyID, run.Goal, run.YouTubeVideoID,
		sessionID, run.Status, run.CurrentStep, run.IdempotencyKey,
	).Scan(&run.ID, &run.CreatedAt, &run.UpdatedAt)
	if err != nil {
		return fmt.Errorf("agent_run CreateRun: %w", err)
	}
	return nil
}

// AppendStep inserts a step for a run and returns the generated id +
// started_at. input_json is stored as a reference-bearing JSON object
// (the gateway never writes binary assets here).
func (r *AgentRunRepository) AppendStep(ctx context.Context, step *AgentRunStep) error {
	var inputJSON []byte = []byte("{}")
	if len(step.InputJSON) > 0 {
		if !json.Valid(step.InputJSON) {
			return fmt.Errorf("agent_run AppendStep: input_json is not valid JSON")
		}
		inputJSON = step.InputJSON
	}
	err := r.db.QueryRowContext(ctx, `
		INSERT INTO agent_run_steps (run_id, tool_name, status, input_json)
		VALUES ($1, $2, $3, $4::jsonb)
		RETURNING id, started_at`,
		step.RunID, step.ToolName, step.Status, string(inputJSON),
	).Scan(&step.ID, &step.StartedAt)
	if err != nil {
		return fmt.Errorf("agent_run AppendStep: %w", err)
	}
	return nil
}

// CompleteStep transitions a step to its terminal state with the
// reference-bearing output and optional failure details.
func (r *AgentRunRepository) CompleteStep(ctx context.Context, step *AgentRunStep) error {
	var outputJSON []byte = []byte("{}")
	if len(step.OutputJSON) > 0 {
		if !json.Valid(step.OutputJSON) {
			return fmt.Errorf("agent_run CompleteStep: output_json is not valid JSON")
		}
		outputJSON = step.OutputJSON
	}
	var errorCode any
	if step.ErrorCode != "" {
		errorCode = step.ErrorCode
	}
	var errorMessage any
	if step.ErrorMessage != "" {
		errorMessage = step.ErrorMessage
	}
	_, err := r.db.ExecContext(ctx, `
		UPDATE agent_run_steps
		SET status = $2, output_json = $3::jsonb, error_code = $4,
		    error_message = $5, completed_at = NOW()
		WHERE id = $1`,
		step.ID, step.Status, string(outputJSON), errorCode, errorMessage,
	)
	if err != nil {
		return fmt.Errorf("agent_run CompleteStep: %w", err)
	}
	return nil
}

// UpdateRun transitions a run's status/current_step and optionally
// stamps completed_at (nil keeps the current value).
func (r *AgentRunRepository) UpdateRun(ctx context.Context, runID, status, currentStep string, completedAt *time.Time) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE agent_runs
		SET status = $2, current_step = NULLIF($3, ''),
		    completed_at = COALESCE($4, completed_at),
		    updated_at = NOW()
		WHERE id = $1`,
		runID, status, currentStep, completedAt,
	)
	if err != nil {
		return fmt.Errorf("agent_run UpdateRun: %w", err)
	}
	return nil
}
