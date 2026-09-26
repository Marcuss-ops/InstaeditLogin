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
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/agenttools"
	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/jobmaster"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// AgentRunsModuleDeps is the narrow contract required by the module.
type AgentRunsModuleDeps struct {
	Store                   AgentRunStore
	Protected               func(http.HandlerFunc) http.HandlerFunc
	ProtectedWithPermission func(string, http.HandlerFunc) http.HandlerFunc
	Catalog                 agenttools.Catalog
	JobMaster               jobmaster.API
	VideoPublisher          AgentVideoPublisher
	VideoIntents            AgentVideoIntentStore
	Workspaces              WorkspaceStore
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
	agentProtect := protect
	if m.deps.ProtectedWithPermission != nil {
		agentProtect = func(h http.HandlerFunc) http.HandlerFunc {
			return m.deps.ProtectedWithPermission(agenttools.PermissionAutomation, h)
		}
	}

	r := chi.NewRouter()
	r.Post("/", agentProtect(m.handleCreateRun))
	r.Get("/{id}", agentProtect(m.handleGetRun))
	r.Get("/{id}/recovery", agentProtect(m.handleRecovery))
	r.Get("/{id}/steps", agentProtect(m.handleListSteps))
	r.Post("/{id}/steps", agentProtect(m.handleAppendStep))
	r.Post("/{id}/steps/{stepId}/complete", agentProtect(m.handleCompleteStep))
	r.Patch("/{id}", agentProtect(m.handleUpdateRun))
	if m.deps.JobMaster != nil {
		r.Post("/{id}/tools/{tool}", agentProtect(m.handleInvokeTool))
	}
	mux.Mount("/api/v1/agent/runs", r)
	mux.Get("/api/v1/agent/tools", agentProtect(m.handleListTools))
	if m.deps.JobMaster != nil {
		mux.Post("/api/v1/agent/video-plan", agentProtect(m.handleVideoPlan))
	}
	if m.deps.VideoPublisher != nil && m.deps.VideoIntents != nil {
		mux.Post("/api/v1/agent/video-intents", agentProtect(m.handleCreateVideoIntent))
		mux.Patch("/api/v1/agent/video-intents/{id}", agentProtect(m.handleRescheduleVideoIntent))
		mux.Post("/api/v1/agent/video-intents/{id}/run-now", agentProtect(m.handleRunVideoIntentNow))
		mux.Delete("/api/v1/agent/video-intents/{id}", agentProtect(m.handleCancelVideoIntent))
	}
}

type AgentVideoIntentStore interface {
	CreateAgentVideoIntent(*models.Post) error
	FindAgentVideoIntentByKey(context.Context, int64, string) (*models.Post, error)
	FindAgentVideoIntentByID(context.Context, int64, int64) (*models.Post, error)
	ListDueAgentVideoIntents(context.Context, time.Time, int) ([]models.Post, error)
	ClaimAgentVideoIntent(context.Context, int64, int64, time.Time) (bool, error)
	LinkAgentVideoIntent(context.Context, int64, int64, string, string) error
	UpdateAgentVideoIntentSchedule(context.Context, int64, int64, time.Time, time.Time, string, []byte) error
	RunAgentVideoIntentNow(context.Context, int64, int64, time.Time) error
	CancelAgentVideoIntent(context.Context, int64, int64) error
	FailAgentVideoIntent(context.Context, int64, int64, string) error
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
		ActorUserID:     identity.UserID(),
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
		if errors.Is(err, repository.ErrAgentRunIdempotencyConflict) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
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
	if len(m.deps.Catalog.Names()) > 0 {
		if _, ok := m.deps.Catalog.Resolve(payload.ToolName); !ok {
			writeError(w, http.StatusUnprocessableEntity, "tool is not in the agent catalog")
			return
		}
	}

	step := &repository.AgentRunStep{
		RunID:     runID,
		ToolName:  payload.ToolName,
		Status:    "running",
		InputJSON: payload.InputJSON,
	}
	identity := auth.IdentityFromContext(req.Context())
	if identity == nil || identity.WorkspaceID() <= 0 {
		writeError(w, http.StatusUnauthorized, "missing identity")
		return
	}
	if err := m.deps.Store.AppendStepOwned(req.Context(), identity.WorkspaceID(), step); err != nil {
		if errors.Is(err, repository.ErrAgentRunNotFound) {
			writeError(w, http.StatusNotFound, "run not found")
			return
		}
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
	runID := chi.URLParam(req, "id")
	identity := auth.IdentityFromContext(req.Context())
	if identity == nil || identity.WorkspaceID() <= 0 {
		writeError(w, http.StatusUnauthorized, "missing identity")
		return
	}
	if err := m.deps.Store.CompleteStepOwned(req.Context(), identity.WorkspaceID(), runID, step); err != nil {
		if errors.Is(err, repository.ErrAgentRunNotFound) {
			writeError(w, http.StatusNotFound, "run or step not found")
			return
		}
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
	identity := auth.IdentityFromContext(req.Context())
	if identity == nil || identity.WorkspaceID() <= 0 {
		writeError(w, http.StatusUnauthorized, "missing identity")
		return
	}
	if err := m.deps.Store.UpdateRunOwned(req.Context(), identity.WorkspaceID(), runID, payload.Status, payload.CurrentStep, payload.CompletedAt); err != nil {
		if errors.Is(err, repository.ErrAgentRunNotFound) {
			writeError(w, http.StatusNotFound, "run not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "update run: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type agentRunResponse struct {
	Run   *repository.AgentRun      `json:"run"`
	Steps []repository.AgentRunStep `json:"steps"`
}

func (m *AgentRunsModule) handleGetRun(w http.ResponseWriter, req *http.Request) {
	identity := auth.IdentityFromContext(req.Context())
	if identity == nil || identity.WorkspaceID() <= 0 {
		writeError(w, http.StatusUnauthorized, "missing identity")
		return
	}
	run, err := m.deps.Store.GetRun(req.Context(), identity.WorkspaceID(), chi.URLParam(req, "id"))
	if errors.Is(err, repository.ErrAgentRunNotFound) {
		writeError(w, http.StatusNotFound, "run not found")
		return
	}
	if err != nil {
		writeError(w, 500, "get run: "+err.Error())
		return
	}
	steps, err := m.deps.Store.ListSteps(req.Context(), identity.WorkspaceID(), run.ID)
	if err != nil {
		writeError(w, 500, "list steps: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, agentRunResponse{Run: run, Steps: steps})
}

func (m *AgentRunsModule) handleListSteps(w http.ResponseWriter, req *http.Request) {
	identity := auth.IdentityFromContext(req.Context())
	if identity == nil || identity.WorkspaceID() <= 0 {
		writeError(w, http.StatusUnauthorized, "missing identity")
		return
	}
	if _, err := m.deps.Store.GetRun(req.Context(), identity.WorkspaceID(), chi.URLParam(req, "id")); err != nil {
		if errors.Is(err, repository.ErrAgentRunNotFound) {
			writeError(w, http.StatusNotFound, "run not found")
		} else {
			writeError(w, http.StatusInternalServerError, "get run: "+err.Error())
		}
		return
	}
	steps, err := m.deps.Store.ListSteps(req.Context(), identity.WorkspaceID(), chi.URLParam(req, "id"))
	if err != nil {
		writeError(w, 500, "list steps: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"steps": steps})
}

// handleRecovery is the restart path: it returns persisted steps and, when a
// remote id exists, asks the execution plane for the latest status. The
// control plane never reconstructs a workflow from process memory.
func (m *AgentRunsModule) handleRecovery(w http.ResponseWriter, req *http.Request) {
	identity := auth.IdentityFromContext(req.Context())
	if identity == nil || identity.WorkspaceID() <= 0 {
		writeError(w, http.StatusUnauthorized, "missing identity")
		return
	}
	runID := chi.URLParam(req, "id")
	run, err := m.deps.Store.GetRun(req.Context(), identity.WorkspaceID(), runID)
	if err != nil {
		if errors.Is(err, repository.ErrAgentRunNotFound) {
			writeError(w, http.StatusNotFound, "run not found")
		} else {
			writeError(w, http.StatusInternalServerError, "get run: "+err.Error())
		}
		return
	}
	steps, err := m.deps.Store.ListSteps(req.Context(), identity.WorkspaceID(), runID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list steps: "+err.Error())
		return
	}
	type remoteStep struct {
		Step         repository.AgentRunStep `json:"step"`
		RemoteStatus json.RawMessage         `json:"remote_status,omitempty"`
	}
	result := make([]remoteStep, 0, len(steps))
	for _, step := range steps {
		item := remoteStep{Step: step}
		if step.RemoteJobID != "" && m.deps.JobMaster != nil {
			status, getErr := m.deps.JobMaster.Get(req.Context(), step.RemoteJobID)
			if getErr != nil {
				writeError(w, http.StatusBadGateway, "read remote job: "+getErr.Error())
				return
			}
			item.RemoteStatus = status
			remoteState, remoteProgress, progressSnapshot := remoteProgressSnapshot(status)
			if err := m.deps.Store.UpdateStepProgressOwned(req.Context(), identity.WorkspaceID(), runID, step.ID, remoteState, remoteProgress, progressSnapshot); err != nil {
				writeError(w, http.StatusInternalServerError, "persist remote progress: "+err.Error())
				return
			}
			step.RemoteStatus, step.RemoteProgress, step.ProgressJSON = remoteState, remoteProgress, progressSnapshot
			item.Step = step
			if step.ToolName == "content.create_video" && m.deps.VideoPublisher != nil {
				if projector, ok := m.deps.VideoPublisher.(interface {
					UpdateProgress(context.Context, int64, string, repository.AgentRunStep, string, *int, []byte) error
				}); ok {
					if err := projector.UpdateProgress(req.Context(), identity.WorkspaceID(), runID, step, remoteState, remoteProgress, progressSnapshot); err != nil {
						writeError(w, http.StatusInternalServerError, "persist calendar progress: "+err.Error())
						return
					}
				}
			}
			if terminal, ok := terminalStepStatus(status); ok && step.Status == "running" {
				updated := step
				updated.Status = terminal
				updated.OutputJSON = status
				if terminal == "completed" && step.ToolName == "content.create_video" {
					if m.deps.VideoPublisher == nil {
						writeError(w, http.StatusServiceUnavailable, "generated video delivery is not configured")
						return
					}
					deliverCtx := req.Context()
					if downloader, ok := m.deps.JobMaster.(artifactDownloader); ok {
						deliverCtx = withArtifactDownloader(deliverCtx, downloader)
					} else {
						writeError(w, http.StatusServiceUnavailable, "job master artifact download is not available")
						return
					}
					publication, publishErr := m.deps.VideoPublisher.Publish(deliverCtx, identity, identity.WorkspaceID(), runID, step, status)
					if publishErr != nil {
						writeError(w, http.StatusBadGateway, "import and schedule generated video: "+publishErr.Error())
						return
					}
					updated.OutputJSON = publication
				}
				if terminal == "failed" {
					updated.ErrorCode = "REMOTE_JOB_FAILED"
				}
				if err := m.deps.Store.CompleteStepOwned(req.Context(), identity.WorkspaceID(), runID, &updated); err != nil {
					writeError(w, http.StatusInternalServerError, "persist remote status: "+err.Error())
					return
				}
				step = updated
				item.Step = updated
			}
		}
		result = append(result, item)
	}
	allTerminal := len(result) > 0
	anyFailed := false
	currentStep := ""
	for _, item := range result {
		if item.Step.Status == "running" {
			allTerminal = false
			if currentStep == "" {
				currentStep = item.Step.ToolName
			}
		}
		if item.Step.Status == "failed" {
			anyFailed = true
		}
	}
	if len(result) > 0 {
		if allTerminal {
			status := "completed"
			if anyFailed {
				status = "failed"
			}
			completedAt := time.Now().UTC()
			if err := m.deps.Store.UpdateRunOwned(req.Context(), identity.WorkspaceID(), runID, status, currentStep, &completedAt); err != nil {
				writeError(w, http.StatusInternalServerError, "persist run status: "+err.Error())
				return
			}
			run.Status, run.CompletedAt = status, &completedAt
		} else {
			if err := m.deps.Store.UpdateRunOwned(req.Context(), identity.WorkspaceID(), runID, "running", currentStep, nil); err != nil {
				writeError(w, http.StatusInternalServerError, "persist run progress: "+err.Error())
				return
			}
			run.Status, run.CurrentStep = "running", currentStep
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"run": run, "steps": result})
}

// terminalStepStatus normalizes execution-plane vocabulary without leaking
// provider-specific states into the control-plane database.
func terminalStepStatus(raw json.RawMessage) (string, bool) {
	var value map[string]json.RawMessage
	if json.Unmarshal(raw, &value) != nil {
		return "", false
	}
	if nested, ok := value["job"]; ok {
		return terminalStepStatus(nested)
	}
	var status string
	for _, key := range []string{"status", "state", "overall_status"} {
		if json.Unmarshal(value[key], &status) == nil && strings.TrimSpace(status) != "" {
			break
		}
	}
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "completed", "complete", "succeeded", "success", "published":
		return "completed", true
	case "failed", "error", "cancelled", "canceled":
		return "failed", true
	default:
		return "", false
	}
}

// remoteProgressSnapshot keeps only status/progress/timeline-shaped fields,
// never the potentially large final artifact/result payload.
func remoteProgressSnapshot(raw json.RawMessage) (string, *int, json.RawMessage) {
	var value map[string]json.RawMessage
	if json.Unmarshal(raw, &value) != nil {
		return "", nil, json.RawMessage(`{}`)
	}
	if nested, ok := value["job"]; ok {
		var jobValue map[string]json.RawMessage
		if json.Unmarshal(nested, &jobValue) == nil {
			// The Job Master wraps its durable job row under `job` but keeps
			// live projection fields (current_stage, events, timeline, error)
			// on the outer response. Merge instead of recursing so those fields
			// reach the Calendar snapshot.
			for key, rawValue := range value {
				if key != "job" {
					jobValue[key] = rawValue
				}
			}
			value = jobValue
		}
	}
	state := ""
	for _, key := range []string{"status", "state", "overall_status"} {
		var candidate string
		if json.Unmarshal(value[key], &candidate) == nil && strings.TrimSpace(candidate) != "" {
			state = strings.TrimSpace(candidate)
			break
		}
	}
	var progress *int
	var number float64
	if json.Unmarshal(value["progress"], &number) == nil {
		bounded := int(math.Round(number))
		if bounded < 0 {
			bounded = 0
		} else if bounded > 100 {
			bounded = 100
		}
		progress = &bounded
	}
	snapshot := make(map[string]json.RawMessage)
	for _, key := range []string{"status", "state", "overall_status", "progress", "phase", "current_phase", "current_stage", "current_step", "stage_progress", "timeline", "events", "error", "steps"} {
		if rawValue, ok := value[key]; ok {
			snapshot[key] = compactRemoteProgressField(key, rawValue)
		}
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return state, progress, json.RawMessage(`{}`)
	}
	if len(encoded) > 64*1024 {
		for key := range snapshot {
			switch key {
			case "status", "state", "overall_status", "progress", "phase", "current_phase", "current_stage", "current_step":
			default:
				delete(snapshot, key)
			}
		}
		encoded, err = json.Marshal(snapshot)
		if err != nil {
			return state, progress, json.RawMessage(`{}`)
		}
	}
	return state, progress, encoded
}

// compactRemoteProgressField retains the newest part of the Master event
// history. A long-running job must not grow a Calendar row without bound.
func compactRemoteProgressField(key string, raw json.RawMessage) json.RawMessage {
	if key != "events" && key != "timeline" {
		return raw
	}
	var entries []json.RawMessage
	if json.Unmarshal(raw, &entries) != nil || len(entries) <= 50 {
		return raw
	}
	encoded, err := json.Marshal(entries[len(entries)-50:])
	if err != nil {
		return json.RawMessage(`[]`)
	}
	return encoded
}

func (m *AgentRunsModule) handleListTools(w http.ResponseWriter, req *http.Request) {
	var remote json.RawMessage = json.RawMessage(`{"types":[]}`)
	if m.deps.JobMaster != nil {
		var err error
		remote, err = m.deps.JobMaster.ListTypes(req.Context())
		if err != nil {
			writeError(w, http.StatusBadGateway, "list remote tools: "+err.Error())
			return
		}
	}
	tools, err := m.deps.Catalog.Available(remote)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if m.deps.VideoPublisher == nil {
		for i := range tools {
			if tools[i].Name == "content.create_video" {
				tools[i].Available = false
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"tools": tools})
}

type invokeToolRequest struct {
	Project        string          `json:"project"`
	IdempotencyKey string          `json:"idempotency_key"`
	Payload        json.RawMessage `json:"payload"`
}

func (m *AgentRunsModule) handleInvokeTool(w http.ResponseWriter, req *http.Request) {
	identity := auth.IdentityFromContext(req.Context())
	if identity == nil || identity.WorkspaceID() <= 0 {
		writeError(w, http.StatusUnauthorized, "missing identity")
		return
	}
	runID := chi.URLParam(req, "id")
	toolName := strings.TrimSpace(chi.URLParam(req, "tool"))
	if _, err := m.deps.Store.GetRun(req.Context(), identity.WorkspaceID(), runID); err != nil {
		if errors.Is(err, repository.ErrAgentRunNotFound) {
			writeError(w, http.StatusNotFound, "run not found")
		} else {
			writeError(w, http.StatusInternalServerError, "get run: "+err.Error())
		}
		return
	}
	definition, ok := m.deps.Catalog.Resolve(toolName)
	if !ok || !definition.Submit {
		writeError(w, http.StatusNotFound, "unknown agent tool")
		return
	}
	var body invokeToolRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, 400, "invalid JSON: "+err.Error())
		return
	}
	body.Project = strings.TrimSpace(body.Project)
	body.IdempotencyKey = strings.TrimSpace(body.IdempotencyKey)
	if body.Project == "" || body.IdempotencyKey == "" {
		writeError(w, 400, "project and idempotency_key are required")
		return
	}
	if len(body.Payload) == 0 {
		body.Payload = json.RawMessage(`{}`)
	}
	if !json.Valid(body.Payload) {
		writeError(w, 400, "payload must be valid JSON")
		return
	}
	remoteTypes, err := m.deps.JobMaster.ListTypes(req.Context())
	if err != nil {
		writeError(w, 502, "list remote tools: "+err.Error())
		return
	}
	available, err := m.deps.Catalog.Available(remoteTypes)
	if err != nil {
		writeError(w, 502, err.Error())
		return
	}
	found := false
	for _, d := range available {
		if d.Name == toolName {
			found = d.Available
		}
	}
	if !found {
		writeError(w, 422, "agent tool is not available on the execution plane")
		return
	}
	input, _ := json.Marshal(map[string]any{"project": body.Project, "idempotency_key": body.IdempotencyKey, "payload": json.RawMessage(body.Payload)})
	// Replays must recover the original local step as well as relying on the
	// Master idempotency key. This also repairs the crash window where the
	// Master accepted a job but the API process died before saving its ID.
	existingSteps, err := m.deps.Store.ListSteps(req.Context(), identity.WorkspaceID(), runID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list existing steps: "+err.Error())
		return
	}
	var step *repository.AgentRunStep
	for _, existing := range existingSteps {
		if existing.IdempotencyKey != body.IdempotencyKey {
			continue
		}
		if existing.ToolName != toolName || !sameJSON(existing.InputJSON, input) {
			writeError(w, http.StatusConflict, "step idempotency key conflicts with an existing request")
			return
		}
		if existing.RemoteJobID != "" {
			if toolName == "content.create_video" {
				if existing.Status != "running" {
					writeJSON(w, http.StatusOK, map[string]any{"step_id": existing.ID, "tool": toolName, "remote_job_id": existing.RemoteJobID, "idempotency_key": body.IdempotencyKey, "status": existing.Status})
					return
				}
				step = &existing
				break
			}
			writeJSON(w, http.StatusAccepted, map[string]any{"step_id": existing.ID, "tool": toolName, "remote_job_id": existing.RemoteJobID, "idempotency_key": body.IdempotencyKey, "status": existing.Status})
			return
		}
		step = &existing
		break
	}
	if step == nil {
		step = &repository.AgentRunStep{RunID: runID, ToolName: toolName, Status: "running", InputJSON: input, IdempotencyKey: body.IdempotencyKey}
		if err := m.deps.Store.AppendStepOwned(req.Context(), identity.WorkspaceID(), step); err != nil {
			writeError(w, 500, "append step: "+err.Error())
			return
		}
	}
	if toolName == "content.create_video" {
		m.submitVideoWorkflow(w, req, identity.WorkspaceID(), runID, body, step)
		return
	}
	result, err := m.deps.JobMaster.Submit(req.Context(), jobmaster.SubmitRequest{Type: definition.RemoteType, Project: body.Project, IdempotencyKey: body.IdempotencyKey, Payload: body.Payload})
	if err != nil {
		_ = m.deps.Store.CompleteStepOwned(req.Context(), identity.WorkspaceID(), runID, &repository.AgentRunStep{ID: step.ID, Status: "failed", ErrorCode: "REMOTE_SUBMIT_FAILED", ErrorMessage: err.Error()})
		writeError(w, 502, "submit remote tool: "+err.Error())
		return
	}
	remoteID := remoteJobID(result)
	if remoteID == "" {
		_ = m.deps.Store.CompleteStepOwned(req.Context(), identity.WorkspaceID(), runID, &repository.AgentRunStep{ID: step.ID, Status: "failed", ErrorCode: "REMOTE_JOB_ID_MISSING", ErrorMessage: "remote submit returned no job id"})
		writeError(w, 502, "remote submit returned no job id")
		return
	}
	if err := m.deps.Store.SetStepRemoteJob(req.Context(), identity.WorkspaceID(), runID, step.ID, remoteID, body.IdempotencyKey); err != nil {
		writeError(w, 500, "persist remote job: "+err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"step_id": step.ID, "tool": toolName, "remote_job_id": remoteID, "idempotency_key": body.IdempotencyKey, "status": "running"})
}

type createVideoPayload struct {
	Generation json.RawMessage `json:"generation"`
	Publish    json.RawMessage `json:"publish"`
}

func (m *AgentRunsModule) submitVideoWorkflow(w http.ResponseWriter, req *http.Request, workspaceID int64, runID string, body invokeToolRequest, step *repository.AgentRunStep) {
	if m.deps.VideoPublisher == nil {
		writeError(w, http.StatusServiceUnavailable, "generated video delivery is not configured")
		return
	}
	var plan createVideoPayload
	if err := json.Unmarshal(body.Payload, &plan); err != nil || len(plan.Generation) == 0 || len(plan.Publish) == 0 {
		writeError(w, http.StatusBadRequest, "content.create_video payload requires generation and publish objects")
		return
	}
	if err := validateVideoGeneration(plan.Generation); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(body.IdempotencyKey) > 180 {
		writeError(w, http.StatusBadRequest, "idempotency_key must be at most 180 characters")
		return
	}
	var publish struct {
		Title       string               `json:"title"`
		Caption     string               `json:"caption"`
		Language    string               `json:"language"`
		ScheduledAt string               `json:"scheduled_at"`
		Privacy     string               `json:"privacy"`
		Targets     []videoPublishTarget `json:"targets"`
	}
	if err := json.Unmarshal(plan.Publish, &publish); err != nil || strings.TrimSpace(publish.Title) == "" || len(publish.Targets) == 0 {
		writeError(w, http.StatusBadRequest, "publish requires title and at least one target")
		return
	}
	if _, err := time.Parse(time.RFC3339, publish.ScheduledAt); err != nil {
		writeError(w, http.StatusBadRequest, "publish.scheduled_at must be RFC3339")
		return
	}
	if publish.Privacy != "" && publish.Privacy != "public" && publish.Privacy != "unlisted" && publish.Privacy != "private" {
		writeError(w, http.StatusBadRequest, "publish.privacy must be public, unlisted, or private")
		return
	}
	for _, target := range publish.Targets {
		if target.PlatformAccountID <= 0 {
			writeError(w, http.StatusBadRequest, "publish target platform_account_id must be positive")
			return
		}
	}
	identity := auth.IdentityFromContext(req.Context())
	if err := m.deps.VideoPublisher.Validate(req.Context(), identity, workspaceID, body.Payload); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	calendarEvent, reserveErr := m.deps.VideoPublisher.Reserve(req.Context(), identity, workspaceID, runID, *step)
	if reserveErr != nil {
		writeError(w, http.StatusInternalServerError, "create scheduled calendar event: "+reserveErr.Error())
		return
	}
	markStartFailed := func(code string, cause error) {
		completedAt := time.Now().UTC()
		failed := *step
		failed.Status, failed.ErrorCode, failed.ErrorMessage, failed.CompletedAt = "failed", code, cause.Error(), &completedAt
		_ = m.deps.Store.CompleteStepOwned(req.Context(), workspaceID, runID, &failed)
		_ = m.deps.Store.UpdateRunOwned(req.Context(), workspaceID, runID, "failed", "content.create_video", &completedAt)
		if projector, ok := m.deps.VideoPublisher.(interface {
			UpdateProgress(context.Context, int64, string, repository.AgentRunStep, string, *int, []byte) error
		}); ok {
			snapshot, _ := json.Marshal(map[string]any{"phase": "FAILED", "error_code": code, "error": cause.Error()})
			_ = projector.UpdateProgress(req.Context(), workspaceID, runID, *step, "FAILED", nil, snapshot)
		}
	}
	if step.RemoteJobID == "" {
		result, submitErr := m.deps.JobMaster.Submit(req.Context(), jobmaster.SubmitRequest{
			Type: "video.create", Project: body.Project, IdempotencyKey: body.IdempotencyKey, Payload: plan.Generation,
		})
		if submitErr != nil {
			markStartFailed("VIDEO_CREATE_SUBMIT_FAILED", submitErr)
			writeError(w, http.StatusBadGateway, "submit video.create: "+submitErr.Error())
			return
		}
		remoteID := remoteJobID(result)
		if remoteID == "" {
			markStartFailed("REMOTE_JOB_ID_MISSING", errors.New("video.create response returned no job id"))
			writeError(w, http.StatusBadGateway, "video.create response returned no job id")
			return
		}
		if err := m.deps.Store.SetStepRemoteJob(req.Context(), workspaceID, runID, step.ID, remoteID, body.IdempotencyKey); err != nil {
			writeError(w, http.StatusInternalServerError, "persist video.create job: "+err.Error())
			return
		}
		step.RemoteJobID = remoteID
		step.RemoteStatus = "QUEUED"
		if err := m.deps.Store.UpdateStepProgressOwned(req.Context(), workspaceID, runID, step.ID, "QUEUED", nil, json.RawMessage(`{"phase":"QUEUED"}`)); err != nil {
			writeError(w, http.StatusInternalServerError, "persist queued phase: "+err.Error())
			return
		}
	}
	var event struct {
		PostID int64 `json:"post_id"`
	}
	_ = json.Unmarshal(calendarEvent, &event)
	writeJSON(w, http.StatusAccepted, map[string]any{"step_id": step.ID, "tool": "content.create_video", "remote_job_id": step.RemoteJobID, "calendar_post_id": event.PostID, "idempotency_key": body.IdempotencyKey, "status": "running", "phase": "QUEUED"})
}

type videoPublishTarget struct {
	PlatformAccountID int64 `json:"platform_account_id"`
}

func withJSONFields(raw json.RawMessage, fields map[string]any) (json.RawMessage, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return nil, errors.New("expected a JSON object")
	}
	for key, value := range fields {
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		obj[key] = encoded
	}
	encoded, err := json.Marshal(obj)
	return json.RawMessage(encoded), err
}

func remoteJobID(raw json.RawMessage) string {
	var v map[string]json.RawMessage
	if json.Unmarshal(raw, &v) != nil {
		return ""
	}
	for _, key := range []string{"job_id", "id"} {
		var s string
		if json.Unmarshal(v[key], &s) == nil && s != "" {
			return s
		}
	}
	if nested, ok := v["job"]; ok {
		return remoteJobID(nested)
	}
	return ""
}

// sameJSON compares persisted request envelopes independent of JSON object key order.
func sameJSON(a, b []byte) bool {
	var left, right any
	return json.Unmarshal(a, &left) == nil && json.Unmarshal(b, &right) == nil && reflect.DeepEqual(left, right)
}
