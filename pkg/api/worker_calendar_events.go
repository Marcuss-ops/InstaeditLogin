package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/go-chi/chi/v5"
)

const maxWorkerCalendarBatch = 20

var workerCalendarKinds = map[string]struct{}{
	"script.generate": {}, "media.stock": {}, "youtube_clip.extract": {},
	"voiceover.generate": {}, "image.generate.google": {}, "clip.render": {},
	"video.assemble": {}, "video.create": {},
}

type workerCalendarEventInput struct {
	EventKey    string `json:"event_key"`
	Title       string `json:"title"`
	ScheduledAt string `json:"scheduled_at,omitempty"`
	Kind        string `json:"kind"`
	JobID       string `json:"job_id,omitempty"`
}

type createWorkerCalendarEventsRequest struct {
	Events []workerCalendarEventInput `json:"events"`
}

// handleCreateWorkerCalendarEvents creates up to 20 workspace-owned draft
// posts. Each post is a video event which its worker can update by event_key.
func (m *AgentRunsModule) handleCreateWorkerCalendarEvents(w http.ResponseWriter, req *http.Request) {
	identity := auth.IdentityFromContext(req.Context())
	if identity == nil || identity.WorkspaceID() <= 0 {
		writeError(w, http.StatusUnauthorized, "missing workspace identity")
		return
	}
	var body createWorkerCalendarEventsRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if len(body.Events) == 0 || len(body.Events) > maxWorkerCalendarBatch {
		writeError(w, http.StatusBadRequest, "events must contain between 1 and 20 videos")
		return
	}
	seen := make(map[string]struct{}, len(body.Events))
	for i := range body.Events {
		item := &body.Events[i]
		item.EventKey = strings.TrimSpace(item.EventKey)
		item.Title = strings.TrimSpace(item.Title)
		item.Kind = strings.TrimSpace(item.Kind)
		item.JobID = strings.TrimSpace(item.JobID)
		if item.EventKey == "" || len(item.EventKey) > 180 || strings.ContainsAny(item.EventKey, "/\\\r\n") {
			writeError(w, http.StatusBadRequest, "events[].event_key is required and must be path-safe")
			return
		}
		if _, ok := seen[item.EventKey]; ok {
			writeError(w, http.StatusBadRequest, "event_key values must be unique within the batch")
			return
		}
		seen[item.EventKey] = struct{}{}
		if item.Title == "" || len(item.Title) > 300 {
			writeError(w, http.StatusBadRequest, "events[].title must be between 1 and 300 characters")
			return
		}
		if !workerCalendarKindAllowed(item.Kind) {
			writeError(w, http.StatusBadRequest, "events[].kind is not a supported job kind")
			return
		}
		if len(item.JobID) > 256 || strings.ContainsAny(item.JobID, "/\\\r\n") {
			writeError(w, http.StatusBadRequest, "events[].job_id is invalid")
			return
		}
		if item.ScheduledAt != "" {
			if _, err := time.Parse(time.RFC3339, item.ScheduledAt); err != nil {
				writeError(w, http.StatusBadRequest, "events[].scheduled_at must be RFC3339")
				return
			}
		}
	}

	created := make([]map[string]any, 0, len(body.Events))
	for _, item := range body.Events {
		publishAt := time.Now().UTC()
		if item.ScheduledAt != "" {
			publishAt, _ = time.Parse(time.RFC3339, item.ScheduledAt)
			publishAt = publishAt.UTC()
		}
		requestShape, _ := json.Marshal(item)
		hash := sha256.Sum256(requestShape)
		metadata, _ := json.Marshal(map[string]any{
			"worker_calendar_event": true,
			"worker_event_key":      item.EventKey,
			"worker_event_hash":     hex.EncodeToString(hash[:]),
			"generation_kind":       item.Kind,
			"generation_status":     "QUEUED",
			"generation_progress":   0,
			"generation_phase":      "QUEUED",
			"worker_remote_job_id":  item.JobID,
			"generation_snapshot": map[string]any{
				"phase":        "QUEUED",
				"worker_kinds": map[string]any{item.Kind: map[string]any{"status": "QUEUED", "progress": 0}},
			},
		})
		post := &models.Post{
			WorkspaceID: identity.WorkspaceID(), Title: item.Title,
			Status: models.PostStatusDraft, PublishAt: &publishAt,
			IngestAfter: time.Now().UTC(), Metadata: metadata,
		}
		saved, wasCreated, err := m.deps.CalendarEvents.CreateWorkerCalendarEvent(post)
		if err != nil {
			if errors.Is(err, repository.ErrIdempotencyConflict) {
				writeError(w, http.StatusConflict, "event_key was already used with a different video request")
				return
			}
			writeError(w, http.StatusInternalServerError, "create worker calendar event: "+err.Error())
			return
		}
		created = append(created, map[string]any{
			"event_key": item.EventKey, "post_id": saved.ID,
			"title": saved.Title, "created": wasCreated,
		})
	}
	writeJSON(w, http.StatusCreated, map[string]any{"events": created})
}

type updateWorkerCalendarProgressRequest struct {
	Kind     string               `json:"kind"`
	Status   string               `json:"status"`
	Progress *int                 `json:"progress,omitempty"`
	Phase    string               `json:"phase,omitempty"`
	Snapshot json.RawMessage      `json:"snapshot,omitempty"`
	Error    *workerCalendarError `json:"error,omitempty"`
}

type workerCalendarError struct {
	ErrorCode  string `json:"error_code"`
	Reason     string `json:"reason"`
	OutputTail string `json:"output_tail,omitempty"`
}

func (m *AgentRunsModule) handleUpdateWorkerCalendarEventProgress(w http.ResponseWriter, req *http.Request) {
	identity := auth.IdentityFromContext(req.Context())
	if identity == nil || identity.WorkspaceID() <= 0 {
		writeError(w, http.StatusUnauthorized, "missing workspace identity")
		return
	}
	eventKey := strings.TrimSpace(chi.URLParam(req, "eventKey"))
	if eventKey == "" || len(eventKey) > 180 {
		writeError(w, http.StatusBadRequest, "invalid event key")
		return
	}
	var body updateWorkerCalendarProgressRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	body.Kind = strings.TrimSpace(body.Kind)
	body.Status = strings.ToUpper(strings.TrimSpace(body.Status))
	body.Phase = strings.TrimSpace(body.Phase)
	if !workerCalendarKindAllowed(body.Kind) {
		writeError(w, http.StatusBadRequest, "kind is not a supported job kind")
		return
	}
	switch body.Status {
	case "QUEUED", "RUNNING", "SUCCEEDED", "COMPLETED", "FAILED", "CANCELLED":
	default:
		writeError(w, http.StatusBadRequest, "status must be QUEUED, RUNNING, SUCCEEDED, COMPLETED, FAILED, or CANCELLED")
		return
	}
	if body.Progress != nil && (*body.Progress < 0 || *body.Progress > 100) {
		writeError(w, http.StatusBadRequest, "progress must be between 0 and 100")
		return
	}
	if body.Status == "FAILED" {
		if body.Error == nil || strings.TrimSpace(body.Error.ErrorCode) == "" || strings.TrimSpace(body.Error.Reason) == "" {
			writeError(w, http.StatusBadRequest, "FAILED status requires error.error_code and error.reason")
			return
		}
		body.Error.ErrorCode = strings.TrimSpace(body.Error.ErrorCode)
		body.Error.Reason = strings.TrimSpace(body.Error.Reason)
		if len(body.Error.ErrorCode) > 120 || len(body.Error.Reason) > 2000 || len(body.Error.OutputTail) > 12000 {
			writeError(w, http.StatusBadRequest, "error fields exceed their size limits")
			return
		}
		body.Snapshot, _ = json.Marshal(map[string]any{"error": body.Error})
	}
	if len(body.Snapshot) > 128*1024 || (len(body.Snapshot) > 0 && !json.Valid(body.Snapshot)) {
		writeError(w, http.StatusBadRequest, "snapshot must be valid JSON no larger than 128 KiB")
		return
	}
	if len(body.Snapshot) == 0 {
		body.Snapshot = json.RawMessage(`{}`)
	}
	var snapshotObject map[string]json.RawMessage
	if json.Unmarshal(body.Snapshot, &snapshotObject) != nil || snapshotObject == nil {
		writeError(w, http.StatusBadRequest, "snapshot must be a JSON object")
		return
	}
	if body.Phase == "" {
		body.Phase = body.Status
	}
	if len(body.Phase) > 120 {
		writeError(w, http.StatusBadRequest, "phase must be no longer than 120 characters")
		return
	}
	if err := m.deps.CalendarEvents.UpdateWorkerCalendarEventProgress(req.Context(), identity.WorkspaceID(), eventKey, body.Kind, body.Status, body.Phase, body.Progress, body.Snapshot); err != nil {
		if errors.Is(err, repository.ErrAgentRunNotFound) {
			writeError(w, http.StatusNotFound, "calendar event not found")
		} else {
			writeError(w, http.StatusInternalServerError, "update worker calendar event: "+err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"event_key": eventKey, "kind": body.Kind, "status": body.Status, "progress": body.Progress, "phase": body.Phase})
}

func workerCalendarKindAllowed(kind string) bool {
	_, ok := workerCalendarKinds[kind]
	return ok
}

type editWorkerCalendarEventRequest struct {
	Title       *string `json:"title,omitempty"`
	ScheduledAt *string `json:"scheduled_at,omitempty"`
}

func (m *AgentRunsModule) handleEditWorkerCalendarEvent(w http.ResponseWriter, req *http.Request) {
	identity := auth.IdentityFromContext(req.Context())
	if identity == nil || identity.WorkspaceID() <= 0 {
		writeError(w, http.StatusUnauthorized, "missing workspace identity")
		return
	}
	key := strings.TrimSpace(chi.URLParam(req, "eventKey"))
	if key == "" || len(key) > 180 || strings.ContainsAny(key, "/\\\r\n") {
		writeError(w, http.StatusBadRequest, "invalid event key")
		return
	}
	var body editWorkerCalendarEventRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if body.Title == nil && body.ScheduledAt == nil {
		writeError(w, http.StatusBadRequest, "title or scheduled_at is required")
		return
	}
	if body.Title != nil {
		value := strings.TrimSpace(*body.Title)
		if value == "" || len(value) > 300 {
			writeError(w, http.StatusBadRequest, "title must be between 1 and 300 characters")
			return
		}
		body.Title = &value
	}
	var scheduledAt *time.Time
	if body.ScheduledAt != nil {
		parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(*body.ScheduledAt))
		if err != nil {
			writeError(w, http.StatusBadRequest, "scheduled_at must be RFC3339")
			return
		}
		parsed = parsed.UTC()
		scheduledAt = &parsed
	}
	if err := m.deps.CalendarEvents.UpdateWorkerCalendarEvent(req.Context(), identity.WorkspaceID(), key, body.Title, scheduledAt); err != nil {
		if errors.Is(err, repository.ErrAgentRunNotFound) {
			writeError(w, http.StatusNotFound, "editable calendar event not found")
		} else {
			writeError(w, http.StatusInternalServerError, "update worker calendar event: "+err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"event_key": key, "updated": true})
}

func (m *AgentRunsModule) handleDeleteWorkerCalendarEvent(w http.ResponseWriter, req *http.Request) {
	identity := auth.IdentityFromContext(req.Context())
	if identity == nil || identity.WorkspaceID() <= 0 {
		writeError(w, http.StatusUnauthorized, "missing workspace identity")
		return
	}
	key := strings.TrimSpace(chi.URLParam(req, "eventKey"))
	if key == "" || len(key) > 180 || strings.ContainsAny(key, "/\\\r\n") {
		writeError(w, http.StatusBadRequest, "invalid event key")
		return
	}
	if err := m.deps.CalendarEvents.DeleteWorkerCalendarEvent(req.Context(), identity.WorkspaceID(), key); err != nil {
		if errors.Is(err, repository.ErrAgentRunNotFound) {
			writeError(w, http.StatusNotFound, "deletable calendar event not found")
		} else {
			writeError(w, http.StatusInternalServerError, "delete worker calendar event: "+err.Error())
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
