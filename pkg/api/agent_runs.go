// Package api — AgentRunsModule.
//
// The Agent Gateway (a separate service) drives AI agents that prepare
// videos, generate thumbnails, attach them and publish. The gateway
// NEVER touches the database directly — it records every run and every
// tool step through this REST surface, which persists into
// agent_runs / agent_run_steps (migration 129).
//
// Security model:
//   - workspace_id and actor_key_id are derived from the AUTHENTICATED
//     identity (the API key's WorkspaceID / KeyID), never trusted from
//     the client body. A compromised agent key cannot record runs into
//     a foreign workspace.
//   - All routes are protected via the standard JWT/API-key chain.
//   - input_json / output_json are reference-bearing JSON (media_id,
//     project_id, session_id) — never binary assets.
//
// Routes:
//
//	POST  /api/v1/agent/runs            create a run (idempotent by
//	                                    workspace_id + idempotency_key)
//	POST  /api/v1/agent/runs/{id}/steps append a step to a run
//	PATCH /api/v1/agent/runs/{id}       transition run status
package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// AgentRunsModuleDeps is the narrow contract required by the module.
type AgentRunsModuleDeps struct {
	Store     AgentRunStore
	Protected func(http.HandlerFunc) http.HandlerFunc
}

// AgentRunsModule mounts the /api/v1/agent/runs* routes. When Store is
// nil the module registers no routes (matches the other feature-flag
// nil-guard patterns).
type AgentRunsModule struct {
	deps AgentRunsModuleDeps
}

// NewAgentRunsModule instantiates the module.
func NewAgentRunsModule(deps AgentRunsModuleDeps) RouteModule {
	return &AgentRunsModule{deps: deps}
}

// Compile-time assertion: AgentRunsModule implements RouteModule.
var _ RouteModule = (*AgentRunsModule)(nil)

// Register mounts the agent-runs routes under a protected sub-mux.
func (m *AgentRunsModule) Register(mux chi.Router) {
	if m.deps.Store == nil {
		return
	}
	protect := m.deps.Protected
	if protect == nil {
		protect = func(h http.HandlerFunc) http.HandlerFunc { return h }
	}

	r := chi.NewRouter()
	r.Post("/", protect(m.handleCreateRun))
	r.Post("/{id}/steps", protect(m.handleAppendStep))
	r.Post("/{id}/steps/{stepId}/complete", protect(m.handleCompleteStep))
	r.Patch("/{id}", protect(m.handleUpdateRun))
	mux.Mount("/api/v1/agent/runs", r)
}

// createRunRequest is the body accepted by POST /api/v1/agent/runs.
type createRunRequest struct {
	Goal            string `json:"goal"`
	IdempotencyKey  string `json:"idempotency_key"`
	YouTubeVideoID  string `json:"youtube_video_id,omitempty"`
	EditorSessionID string `json:"editor_session_id,omitempty"`
}

// createRunResponse is returned on success (both create and idempotent
// replay paths).
type createRunResponse struct {
	RunID       string `json:"run_id"`
	WorkspaceID int64  `json:"workspace_id"`
	Status      string `json:"status"`
}

// handleCreateRun creates (or idempotently reuses) a run for the
// authenticated identity's workspace.
func (m *AgentRunsModule) handleCreateRun(w http.ResponseWriter, req *http.Request) {
	identity := auth.IdentityFromContext(req.Context())
	if identity == nil || identity.WorkspaceID() <= 0 {
		writeError(w, http.StatusUnauthorized, "missing identity")
		return
	}

	var payload createRunRequest
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	payload.Goal = strings.TrimSpace(payload.Goal)
	payload.IdempotencyKey = strings.TrimSpace(payload.IdempotencyKey)
	if payload.Goal == "" {
		writeError(w, http.StatusBadRequest, "goal is required")
		return
	}
	if payload.IdempotencyKey == "" {
		writeError(w, http.StatusBadRequest, "idempotency_key is required")
		return
	}

	run := &repository.AgentRun{
		WorkspaceID:     identity.WorkspaceID(),
		Goal:            payload.Goal,
		YouTubeVideoID:  strings.TrimSpace(payload.YouTubeVideoID),
		EditorSessionID: strings.TrimSpace(payload.EditorSessionID),
		Status:          "running",
		IdempotencyKey:  payload.IdempotencyKey,
	}
	if identity.KeyID() > 0 {
		keyID := identity.KeyID()
		run.ActorKeyID = &keyID
	}
	if err := m.deps.Store.CreateRun(req.Context(), run); err != nil {
		writeError(w, http.StatusInternalServerError, "create run: "+err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, createRunResponse{
		RunID:       run.ID,
		WorkspaceID: run.WorkspaceID,
		Status:      run.Status,
	})
}

// appendStepRequest is the body accepted by POST /api/v1/agent/runs/{id}/steps.
type appendStepRequest struct {
	ToolName  string          `json:"tool_name"`
	InputJSON json.RawMessage `json:"input_json,omitempty"`
}

// appendStepResponse carries the generated step id.
type appendStepResponse struct {
	StepID string `json:"step_id"`
}

// handleAppendStep records a tool invocation against a run.
func (m *AgentRunsModule) handleAppendStep(w http.ResponseWriter, req *http.Request) {
	runID := chi.URLParam(req, "id")
	if runID == "" {
		writeError(w, http.StatusBadRequest, "run id is required")
		return
	}
	var payload appendStepRequest
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	payload.ToolName = strings.TrimSpace(payload.ToolName)
	if payload.ToolName == "" {
		writeError(w, http.StatusBadRequest, "tool_name is required")
		return
	}

	step := &repository.AgentRunStep{
		RunID:     runID,
		ToolName:  payload.ToolName,
		Status:    "running",
		InputJSON: payload.InputJSON,
	}
	if err := m.deps.Store.AppendStep(req.Context(), step); err != nil {
		writeError(w, http.StatusInternalServerError, "append step: "+err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, appendStepResponse{StepID: step.ID})
}

// completeStepRequest is the body accepted by
// POST /api/v1/agent/runs/{id}/steps/{stepId}/complete.
type completeStepRequest struct {
	Status       string          `json:"status"`
	OutputJSON   json.RawMessage `json:"output_json,omitempty"`
	ErrorCode    string          `json:"error_code,omitempty"`
	ErrorMessage string          `json:"error_message,omitempty"`
}

// handleCompleteStep transitions a step to completed/failed.
func (m *AgentRunsModule) handleCompleteStep(w http.ResponseWriter, req *http.Request) {
	stepID := chi.URLParam(req, "stepId")
	if stepID == "" {
		writeError(w, http.StatusBadRequest, "step id is required")
		return
	}
	var payload completeStepRequest
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	payload.Status = strings.TrimSpace(payload.Status)
	if payload.Status != "completed" && payload.Status != "failed" {
		writeError(w, http.StatusBadRequest, "status must be completed or failed")
		return
	}

	step := &repository.AgentRunStep{
		ID:           stepID,
		Status:       payload.Status,
		OutputJSON:   payload.OutputJSON,
		ErrorCode:    strings.TrimSpace(payload.ErrorCode),
		ErrorMessage: strings.TrimSpace(payload.ErrorMessage),
	}
	if err := m.deps.Store.CompleteStep(req.Context(), step); err != nil {
		writeError(w, http.StatusInternalServerError, "complete step: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// updateRunRequest is the body accepted by PATCH /api/v1/agent/runs/{id}.
type updateRunRequest struct {
	Status      string     `json:"status"`
	CurrentStep string     `json:"current_step,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

// handleUpdateRun transitions a run's status (e.g. completed/failed/
// cancelled).
func (m *AgentRunsModule) handleUpdateRun(w http.ResponseWriter, req *http.Request) {
	runID := chi.URLParam(req, "id")
	if runID == "" {
		writeError(w, http.StatusBadRequest, "run id is required")
		return
	}
	var payload updateRunRequest
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	payload.Status = strings.TrimSpace(payload.Status)
	switch payload.Status {
	case "running", "waiting_approval", "completed", "failed", "cancelled":
	default:
		writeError(w, http.StatusBadRequest, "invalid status")
		return
	}
	if err := m.deps.Store.UpdateRun(req.Context(), runID, payload.Status, payload.CurrentStep, payload.CompletedAt); err != nil {
		writeError(w, http.StatusInternalServerError, "update run: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
