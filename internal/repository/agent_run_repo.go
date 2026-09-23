package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var (
	ErrAgentRunNotFound            = errors.New("agent run not found")
	ErrAgentRunIdempotencyConflict = errors.New("agent run idempotency key conflicts with an existing request")
)

// AgentRun is the persisted record of a single agent execution. It
// carries only REFERENCES (youtube_video_id, editor_session_id) — never
// binary assets. Files live in the Media Library; the run row keeps
// pointers so operators can trace which video/session a run touched.
type AgentRun struct {
	ID              string
	WorkspaceID     int64
	ActorUserID     int64
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
	ID             string
	RunID          string
	ToolName       string
	Status         string
	InputJSON      []byte
	OutputJSON     []byte
	ErrorCode      string
	ErrorMessage   string
	RemoteJobID    string
	IdempotencyKey string
	RemoteStatus   string `json:"remote_status,omitempty"`
	RemoteProgress *int   `json:"remote_progress,omitempty"`
	ProgressJSON   []byte `json:"progress_json,omitempty"`
	StartedAt      time.Time
	CompletedAt    *time.Time
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
			workspace_id, actor_user_id, actor_key_id, goal, youtube_video_id,
			editor_session_id, status, current_step, idempotency_key
		) VALUES ($1, $2, $3, $4, NULLIF($5, ''), $6, $7, NULLIF($8, ''), $9)
		ON CONFLICT (workspace_id, idempotency_key) DO NOTHING
		RETURNING id, created_at, updated_at`,
		run.WorkspaceID, nullableInt64(run.ActorUserID), actorKeyID, run.Goal, run.YouTubeVideoID,
		sessionID, run.Status, run.CurrentStep, run.IdempotencyKey,
	).Scan(&run.ID, &run.CreatedAt, &run.UpdatedAt)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("agent_run CreateRun: %w", err)
	}
	var existing AgentRun
	err = r.db.QueryRowContext(ctx, `
		SELECT id, workspace_id, COALESCE(actor_user_id,0), actor_key_id, goal, COALESCE(youtube_video_id,''),
		       COALESCE(editor_session_id::text,''), status, COALESCE(current_step,''),
		       idempotency_key, created_at, updated_at, completed_at
		FROM agent_runs WHERE workspace_id=$1 AND idempotency_key=$2`,
		run.WorkspaceID, run.IdempotencyKey).Scan(
		&existing.ID, &existing.WorkspaceID, &existing.ActorUserID, &existing.ActorKeyID, &existing.Goal,
		&existing.YouTubeVideoID, &existing.EditorSessionID, &existing.Status,
		&existing.CurrentStep, &existing.IdempotencyKey, &existing.CreatedAt,
		&existing.UpdatedAt, &existing.CompletedAt)
	if err != nil {
		return fmt.Errorf("agent_run CreateRun existing: %w", err)
	}
	if existing.Goal != run.Goal || existing.YouTubeVideoID != run.YouTubeVideoID || existing.EditorSessionID != run.EditorSessionID {
		return ErrAgentRunIdempotencyConflict
	}
	*run = existing
	return nil
}

func (r *AgentRunRepository) GetRun(ctx context.Context, workspaceID int64, runID string) (*AgentRun, error) {
	var run AgentRun
	err := r.db.QueryRowContext(ctx, `
		SELECT id, workspace_id, COALESCE(actor_user_id,0), actor_key_id, goal, COALESCE(youtube_video_id,''),
		       COALESCE(editor_session_id::text,''), status, COALESCE(current_step,''),
		       idempotency_key, created_at, updated_at, completed_at
		FROM agent_runs WHERE id=$1 AND workspace_id=$2`, runID, workspaceID).Scan(
		&run.ID, &run.WorkspaceID, &run.ActorUserID, &run.ActorKeyID, &run.Goal, &run.YouTubeVideoID,
		&run.EditorSessionID, &run.Status, &run.CurrentStep, &run.IdempotencyKey,
		&run.CreatedAt, &run.UpdatedAt, &run.CompletedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrAgentRunNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("agent_run GetRun: %w", err)
	}
	return &run, nil
}

func (r *AgentRunRepository) ListRecoverableRuns(ctx context.Context, limit int) ([]*AgentRun, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT r.id, r.workspace_id, COALESCE(r.actor_user_id,0), r.actor_key_id,
		       r.goal, COALESCE(r.youtube_video_id,''), COALESCE(r.editor_session_id::text,''),
		       r.status, COALESCE(r.current_step,''), r.idempotency_key,
		       r.created_at, r.updated_at, r.completed_at
		FROM agent_runs r
	WHERE r.status='running'
	  AND EXISTS (SELECT 1 FROM agent_run_steps s WHERE s.run_id=r.id AND s.status='running' AND s.remote_job_id IS NOT NULL)
	ORDER BY r.updated_at, r.id
	LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("agent_run ListRecoverableRuns: %w", err)
	}
	defer rows.Close()
	var runs []*AgentRun
	for rows.Next() {
		run := &AgentRun{}
		if err := rows.Scan(&run.ID, &run.WorkspaceID, &run.ActorUserID, &run.ActorKeyID, &run.Goal, &run.YouTubeVideoID, &run.EditorSessionID, &run.Status, &run.CurrentStep, &run.IdempotencyKey, &run.CreatedAt, &run.UpdatedAt, &run.CompletedAt); err != nil {
			return nil, fmt.Errorf("agent_run ListRecoverableRuns scan: %w", err)
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("agent_run ListRecoverableRuns rows: %w", err)
	}
	return runs, nil
}

func (r *AgentRunRepository) ListSteps(ctx context.Context, workspaceID int64, runID string) ([]AgentRunStep, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT s.id, s.run_id, s.tool_name, s.status, s.input_json, s.output_json,
		       COALESCE(s.error_code,''), COALESCE(s.error_message,''),
		       COALESCE(s.remote_job_id,''), COALESCE(s.idempotency_key,''),
		       COALESCE(s.remote_status,''), s.remote_progress, s.progress_json,
		       s.started_at, s.completed_at
		FROM agent_run_steps s JOIN agent_runs r ON r.id=s.run_id
		WHERE s.run_id=$1 AND r.workspace_id=$2 ORDER BY s.started_at ASC`, runID, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("agent_run ListSteps: %w", err)
	}
	defer rows.Close()
	result := []AgentRunStep{}
	for rows.Next() {
		var s AgentRunStep
		if err := rows.Scan(&s.ID, &s.RunID, &s.ToolName, &s.Status, &s.InputJSON, &s.OutputJSON, &s.ErrorCode, &s.ErrorMessage, &s.RemoteJobID, &s.IdempotencyKey, &s.RemoteStatus, &s.RemoteProgress, &s.ProgressJSON, &s.StartedAt, &s.CompletedAt); err != nil {
			return nil, fmt.Errorf("agent_run ListSteps scan: %w", err)
		}
		result = append(result, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("agent_run ListSteps rows: %w", err)
	}
	return result, nil
}

func (r *AgentRunRepository) AppendStepOwned(ctx context.Context, workspaceID int64, step *AgentRunStep) error {
	if _, err := r.GetRun(ctx, workspaceID, step.RunID); err != nil {
		return err
	}
	return r.AppendStep(ctx, step)
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
		INSERT INTO agent_run_steps (run_id, tool_name, status, input_json, idempotency_key)
		VALUES ($1, $2, $3, $4::jsonb, NULLIF($5,''))
		RETURNING id, started_at`,
		step.RunID, step.ToolName, step.Status, string(inputJSON), step.IdempotencyKey,
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

func (r *AgentRunRepository) CompleteStepOwned(ctx context.Context, workspaceID int64, runID string, step *AgentRunStep) error {
	if _, err := r.GetRun(ctx, workspaceID, runID); err != nil {
		return err
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE agent_run_steps SET status=$2, output_json=$3::jsonb, error_code=$4,
			error_message=$5, completed_at=NOW() WHERE id=$1 AND run_id=$6`,
		step.ID, step.Status, jsonOrEmpty(step.OutputJSON), nullable(step.ErrorCode), nullable(step.ErrorMessage), runID)
	if err != nil {
		return fmt.Errorf("agent_run CompleteStepOwned: %w", err)
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return ErrAgentRunNotFound
	}
	return nil
}

func (r *AgentRunRepository) SetStepRemoteJob(ctx context.Context, workspaceID int64, runID, stepID, remoteJobID, idempotencyKey string) error {
	if _, err := r.GetRun(ctx, workspaceID, runID); err != nil {
		return err
	}
	result, err := r.db.ExecContext(ctx, `UPDATE agent_run_steps SET remote_job_id=$3, idempotency_key=$4 WHERE id=$1 AND run_id=$2`, stepID, runID, remoteJobID, idempotencyKey)
	if err != nil {
		return fmt.Errorf("agent_run SetStepRemoteJob: %w", err)
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return ErrAgentRunNotFound
	}
	return nil
}

func (r *AgentRunRepository) UpdateStepProgressOwned(ctx context.Context, workspaceID int64, runID, stepID, remoteStatus string, remoteProgress *int, progressJSON []byte) error {
	if _, err := r.GetRun(ctx, workspaceID, runID); err != nil {
		return err
	}
	if len(progressJSON) == 0 {
		progressJSON = []byte(`{}`)
	}
	if !json.Valid(progressJSON) {
		return fmt.Errorf("agent_run UpdateStepProgressOwned: progress_json is not valid JSON")
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE agent_run_steps SET remote_status=$3, remote_progress=$4, progress_json=$5::jsonb
		WHERE id=$1 AND run_id=$2`, stepID, runID, nullable(remoteStatus), remoteProgress, string(progressJSON))
	if err != nil {
		return fmt.Errorf("agent_run UpdateStepProgressOwned: %w", err)
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return ErrAgentRunNotFound
	}
	return nil
}

func jsonOrEmpty(raw []byte) string {
	if len(raw) == 0 {
		return "{}"
	}
	return string(raw)
}
func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}
func nullableInt64(value int64) any {
	if value <= 0 {
		return nil
	}
	return value
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

func (r *AgentRunRepository) UpdateRunOwned(ctx context.Context, workspaceID int64, runID, status, currentStep string, completedAt *time.Time) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE agent_runs SET status=$3, current_step=NULLIF($4,''),
			completed_at=COALESCE($5, completed_at), updated_at=NOW()
		WHERE id=$2 AND workspace_id=$1`, workspaceID, runID, status, currentStep, completedAt)
	if err != nil {
		return fmt.Errorf("agent_run UpdateRunOwned: %w", err)
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return ErrAgentRunNotFound
	}
	return nil
}
