package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/go-chi/chi/v5"
)

type createAgentVideoIntentRequest struct {
	IdempotencyKey string          `json:"idempotency_key"`
	Timezone       string          `json:"timezone,omitempty"`
	Payload        json.RawMessage `json:"payload"`
}

func (m *AgentRunsModule) handleCreateVideoIntent(w http.ResponseWriter, req *http.Request) {
	identity := auth.IdentityFromContext(req.Context())
	if identity == nil || identity.WorkspaceID() <= 0 {
		writeError(w, 401, "missing identity")
		return
	}
	if m.deps.JobMaster == nil {
		writeError(w, http.StatusServiceUnavailable, "remote video generation is not configured")
		return
	}
	remoteTypes, err := m.deps.JobMaster.ListTypes(req.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "list remote tools: "+err.Error())
		return
	}
	available, err := m.deps.Catalog.Available(remoteTypes)
	if err != nil {
		writeError(w, http.StatusBadGateway, "read remote video capabilities: "+err.Error())
		return
	}
	videoReady := false
	for _, tool := range available {
		if tool.Name == "content.create_video" {
			videoReady = tool.Available
			break
		}
	}
	if !videoReady {
		writeError(w, http.StatusUnprocessableEntity, "scheduled video creation is unavailable: the execution plane does not advertise a full-video assembler")
		return
	}
	var body createAgentVideoIntentRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, 400, "invalid JSON: "+err.Error())
		return
	}
	body.IdempotencyKey = strings.TrimSpace(body.IdempotencyKey)
	if body.IdempotencyKey == "" || len(body.IdempotencyKey) > 180 || !json.Valid(body.Payload) {
		writeError(w, 400, "idempotency_key and valid payload are required")
		return
	}
	body.Timezone = strings.TrimSpace(body.Timezone)
	if body.Timezone == "" {
		body.Timezone = "UTC"
	}
	if _, err := time.LoadLocation(body.Timezone); err != nil {
		writeError(w, 400, "timezone must be a valid IANA timezone")
		return
	}
	if err := m.deps.VideoPublisher.Validate(req.Context(), identity, identity.WorkspaceID(), body.Payload); err != nil {
		writeError(w, 422, err.Error())
		return
	}
	var plan createVideoPayload
	if err := json.Unmarshal(body.Payload, &plan); err != nil || len(plan.Pre) == 0 || len(plan.Finalize) == 0 {
		writeError(w, 400, "payload requires pre, finalize, and publish objects")
		return
	}
	var publish videoPublicationRequest
	if err := json.Unmarshal(plan.Publish, &publish); err != nil {
		writeError(w, 400, "invalid publish plan")
		return
	}
	publishAt, _ := time.Parse(time.RFC3339, publish.ScheduledAt)
	generationAt := publishAt.Add(-30 * time.Minute)
	if generationAt.Before(time.Now().UTC()) {
		generationAt = time.Now().UTC()
	}
	hashInput, _ := json.Marshal(struct {
		Timezone string          `json:"timezone"`
		Payload  json.RawMessage `json:"payload"`
	}{Timezone: body.Timezone, Payload: body.Payload})
	hash := sha256.Sum256(hashInput)
	scheduleKey := "agent-video-intent-" + body.IdempotencyKey
	if existing, err := m.deps.VideoIntents.FindAgentVideoIntentByKey(req.Context(), identity.WorkspaceID(), scheduleKey); err != nil {
		writeError(w, 500, "find existing video intent: "+err.Error())
		return
	} else if existing != nil {
		if !videoIntentHashMatchesRequest(existing.Metadata, body.Payload, body.Timezone) {
			writeError(w, 409, repository.ErrIdempotencyConflict.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"intent_id": existing.ID, "post_id": existing.ID, "status": "scheduled", "generation_at": videoIntentGenerationAt(existing.Metadata), "timezone": videoIntentTimezone(existing.Metadata), "post": existing})
		return
	}
	metadata, _ := json.Marshal(map[string]any{
		"agent_video_intent": true, "agent_schedule_key": scheduleKey, "agent_schedule_hash": hex.EncodeToString(hash[:]),
		"generation_payload": body.Payload, "generation_at": generationAt,
		"schedule_timezone": body.Timezone,
		"agent_target_ids": func() []int64 {
			ids := make([]int64, 0, len(publish.Targets))
			for _, target := range publish.Targets {
				ids = append(ids, target.PlatformAccountID)
			}
			return ids
		}(),
		"generation_status": "SCHEDULED", "generation_phase": "SCHEDULED", "generation_progress": 0,
		"generation_snapshot": map[string]any{"phase": "SCHEDULED", "generation_at": generationAt},
	})
	privacy := strings.TrimSpace(publish.Privacy)
	if privacy == "" {
		privacy = "unlisted"
	}
	post := &models.Post{WorkspaceID: identity.WorkspaceID(), Title: strings.TrimSpace(publish.Title), Caption: publish.Caption, PrivacyLevel: privacy, DefaultPrivacyLevel: privacy, PublishAt: &publishAt, Status: models.PostStatusDraft, Metadata: metadata}
	if err := m.deps.VideoIntents.CreateAgentVideoIntent(post); err != nil {
		// The partial unique index closes the concurrent idempotency race.
		if existing, lookupErr := m.deps.VideoIntents.FindAgentVideoIntentByKey(req.Context(), identity.WorkspaceID(), scheduleKey); lookupErr == nil && existing != nil {
			if !videoIntentHashMatchesRequest(existing.Metadata, body.Payload, body.Timezone) {
				writeError(w, 409, repository.ErrIdempotencyConflict.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"intent_id": existing.ID, "post_id": existing.ID, "status": "scheduled", "generation_at": videoIntentGenerationAt(existing.Metadata), "timezone": videoIntentTimezone(existing.Metadata), "post": existing})
			return
		}
		writeError(w, 500, "create scheduled video intent: "+err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"intent_id": post.ID, "post_id": post.ID, "status": "scheduled", "generation_at": generationAt, "timezone": body.Timezone, "post": post})
}

func (m *AgentRunsModule) handleRescheduleVideoIntent(w http.ResponseWriter, req *http.Request) {
	identity, post, ok := m.loadVideoIntent(w, req)
	if !ok {
		return
	}
	var body struct {
		PublishAt   *time.Time `json:"publish_at"`
		ScheduledAt *time.Time `json:"scheduled_at"`
		Timezone    string     `json:"timezone,omitempty"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, 400, "invalid JSON: "+err.Error())
		return
	}
	publishAt := body.PublishAt
	if publishAt == nil {
		publishAt = body.ScheduledAt
	}
	if publishAt == nil || !publishAt.After(time.Now().Add(5*time.Second)) {
		writeError(w, 400, "publish_at must be in the future")
		return
	}
	var meta map[string]json.RawMessage
	if json.Unmarshal(post.Metadata, &meta) != nil {
		writeError(w, 500, "invalid stored intent metadata")
		return
	}
	timezone := strings.TrimSpace(body.Timezone)
	if timezone == "" {
		timezone = videoIntentTimezone(post.Metadata)
	}
	if timezone == "" {
		timezone = "UTC"
	}
	if _, err := time.LoadLocation(timezone); err != nil {
		writeError(w, 400, "timezone must be a valid IANA timezone")
		return
	}
	var payload json.RawMessage
	if json.Unmarshal(meta["generation_payload"], &payload) != nil {
		writeError(w, 500, "stored intent payload is invalid")
		return
	}
	var plan createVideoPayload
	if json.Unmarshal(payload, &plan) != nil {
		writeError(w, 500, "stored intent payload cannot be decoded")
		return
	}
	var publish videoPublicationRequest
	if json.Unmarshal(plan.Publish, &publish) != nil {
		writeError(w, 500, "stored publish plan cannot be decoded")
		return
	}
	publish.ScheduledAt = publishAt.UTC().Format(time.RFC3339)
	plan.Publish, _ = json.Marshal(publish)
	payload, _ = json.Marshal(plan)
	if err := m.deps.VideoPublisher.Validate(req.Context(), identity, identity.WorkspaceID(), payload); err != nil {
		writeError(w, 422, err.Error())
		return
	}
	generationAt := publishAt.Add(-30 * time.Minute)
	if generationAt.Before(time.Now().UTC()) {
		generationAt = time.Now().UTC()
	}
	if err := m.deps.VideoIntents.UpdateAgentVideoIntentSchedule(req.Context(), identity.WorkspaceID(), post.ID, publishAt.UTC(), generationAt, timezone, payload); err != nil {
		writeError(w, 409, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"intent_id": post.ID, "status": "scheduled", "generation_at": generationAt, "publish_at": publishAt.UTC(), "timezone": timezone})
}

func (m *AgentRunsModule) handleRunVideoIntentNow(w http.ResponseWriter, req *http.Request) {
	identity, post, ok := m.loadVideoIntent(w, req)
	if !ok {
		return
	}
	if err := m.deps.VideoIntents.RunAgentVideoIntentNow(req.Context(), identity.WorkspaceID(), post.ID, time.Now().UTC()); err != nil {
		writeError(w, 409, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"intent_id": post.ID, "status": "dispatching"})
}

func (m *AgentRunsModule) handleCancelVideoIntent(w http.ResponseWriter, req *http.Request) {
	identity, post, ok := m.loadVideoIntent(w, req)
	if !ok {
		return
	}
	if err := m.deps.VideoIntents.CancelAgentVideoIntent(req.Context(), identity.WorkspaceID(), post.ID); err != nil {
		writeError(w, 409, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"intent_id": post.ID, "status": "cancelled"})
}

func (m *AgentRunsModule) loadVideoIntent(w http.ResponseWriter, req *http.Request) (auth.Identity, *models.Post, bool) {
	identity := auth.IdentityFromContext(req.Context())
	if identity == nil || identity.UserID() <= 0 || identity.WorkspaceID() <= 0 {
		writeError(w, 401, "missing user identity")
		return nil, nil, false
	}
	if err := m.deps.VideoPublisher.Authorize(req.Context(), identity, identity.WorkspaceID()); err != nil {
		writeError(w, 403, err.Error())
		return nil, nil, false
	}
	id, err := strconv.ParseInt(chi.URLParam(req, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, 400, "invalid intent id")
		return nil, nil, false
	}
	post, err := m.deps.VideoIntents.FindAgentVideoIntentByID(req.Context(), identity.WorkspaceID(), id)
	if err != nil {
		writeError(w, 500, "read video intent: "+err.Error())
		return nil, nil, false
	}
	if post == nil {
		writeError(w, 404, "video intent not found")
		return nil, nil, false
	}
	var status string
	_ = json.Unmarshal(extractMetadata(post.Metadata, "generation_status"), &status)
	if status != "SCHEDULED" {
		writeError(w, 409, fmt.Sprintf("video intent is already %s", status))
		return nil, nil, false
	}
	return identity, post, true
}

func extractMetadata(raw []byte, key string) json.RawMessage {
	var m map[string]json.RawMessage
	_ = json.Unmarshal(raw, &m)
	return m[key]
}
func videoIntentHashMatches(raw []byte, expected []byte) bool {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return false
	}
	var got string
	_ = json.Unmarshal(m["agent_schedule_hash"], &got)
	return got == hex.EncodeToString(expected)
}
func videoIntentHashMatchesRequest(raw, payload []byte, timezone string) bool {
	if videoIntentTimezone(raw) == "" {
		// Intents created before schedule_timezone was introduced hashed only
		// the workflow payload. Keep their idempotent replay valid.
		hash := sha256.Sum256(payload)
		return videoIntentHashMatches(raw, hash[:])
	}
	encoded, err := json.Marshal(struct {
		Timezone string          `json:"timezone"`
		Payload  json.RawMessage `json:"payload"`
	}{Timezone: timezone, Payload: payload})
	if err != nil {
		return false
	}
	hash := sha256.Sum256(encoded)
	return videoIntentHashMatches(raw, hash[:])
}
func videoIntentGenerationAt(raw []byte) any {
	var m map[string]json.RawMessage
	_ = json.Unmarshal(raw, &m)
	var v any
	_ = json.Unmarshal(m["generation_at"], &v)
	return v
}
func videoIntentTimezone(raw []byte) string {
	var m map[string]json.RawMessage
	_ = json.Unmarshal(raw, &m)
	var value string
	_ = json.Unmarshal(m["schedule_timezone"], &value)
	return value
}

func agentVideoIntentStoreFrom(posts PostStore) AgentVideoIntentStore {
	store, _ := posts.(AgentVideoIntentStore)
	return store
}

// dispatchDueVideoIntents turns a durable Calendar intent into a normal
// owned agent run. Stable run/step/job keys make lease recovery idempotent.
func (m *AgentRunsModule) dispatchDueVideoIntents(ctx context.Context) error {
	if m.deps.VideoIntents == nil || m.deps.Workspaces == nil || m.deps.JobMaster == nil {
		return nil
	}
	intents, err := m.deps.VideoIntents.ListDueAgentVideoIntents(ctx, time.Now().UTC(), 100)
	if err != nil {
		return err
	}
	for _, intent := range intents {
		claimed, err := m.deps.VideoIntents.ClaimAgentVideoIntent(ctx, intent.WorkspaceID, intent.ID, time.Now().UTC())
		if err != nil || !claimed {
			continue
		}
		fail := func(message string) {
			_ = m.deps.VideoIntents.FailAgentVideoIntent(ctx, intent.WorkspaceID, intent.ID, message)
		}
		var metadata map[string]json.RawMessage
		if json.Unmarshal(intent.Metadata, &metadata) != nil {
			fail("invalid stored intent metadata")
			continue
		}
		var payload json.RawMessage
		if json.Unmarshal(metadata["generation_payload"], &payload) != nil {
			fail("stored intent payload is invalid")
			continue
		}
		var plan map[string]json.RawMessage
		if json.Unmarshal(payload, &plan) != nil {
			fail("stored workflow plan is invalid")
			continue
		}
		var publish map[string]json.RawMessage
		if json.Unmarshal(plan["publish"], &publish) != nil {
			fail("stored publish plan is invalid")
			continue
		}
		postID, _ := json.Marshal(intent.ID)
		publish["calendar_post_id"] = postID
		plan["publish"], _ = json.Marshal(publish)
		payload, _ = json.Marshal(plan)
		workspace, err := m.deps.Workspaces.FindByID(intent.WorkspaceID)
		if err != nil || workspace == nil || workspace.OwnerID <= 0 {
			fail("workspace owner is unavailable")
			continue
		}
		run := &repository.AgentRun{WorkspaceID: intent.WorkspaceID, ActorUserID: workspace.OwnerID, Goal: "Scheduled video: " + intent.Title, Status: "running", CurrentStep: "content.create_video", IdempotencyKey: fmt.Sprintf("calendar-video-intent:%d", intent.ID)}
		if err := m.deps.Store.CreateRun(ctx, run); err != nil {
			fail(err.Error())
			continue
		}
		toolBody := invokeToolRequest{Project: fmt.Sprintf("workspace-%d", intent.WorkspaceID), IdempotencyKey: fmt.Sprintf("calendar-video-intent-%d", intent.ID), Payload: payload}
		encoded, _ := json.Marshal(toolBody)
		identity := auth.NewUserIdentity(workspace.OwnerID, intent.WorkspaceID, 0)
		routeCtx := chi.NewRouteContext()
		routeCtx.URLParams.Add("id", run.ID)
		routeCtx.URLParams.Add("tool", "content.create_video")
		req := httptest.NewRequest(http.MethodPost, "/api/v1/agent/runs/"+url.PathEscape(run.ID)+"/tools/content.create_video", bytes.NewReader(encoded))
		req = req.WithContext(auth.WithIdentity(context.WithValue(ctx, chi.RouteCtxKey, routeCtx), identity))
		response := httptest.NewRecorder()
		m.handleInvokeTool(response, req)
		if response.Code >= 400 {
			fail(strings.TrimSpace(response.Body.String()))
			completed := time.Now().UTC()
			_ = m.deps.Store.UpdateRunOwned(ctx, intent.WorkspaceID, run.ID, "failed", "content.create_video", &completed)
		}
	}
	return nil
}
