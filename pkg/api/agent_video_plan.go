package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
)

type videoPlanRequest struct {
	Topic                 string `json:"topic"`
	Title                 string `json:"title"`
	TargetDurationSeconds int    `json:"target_duration_seconds"`
	Language              string `json:"language"`
	AspectRatio           string `json:"aspect_ratio,omitempty"`
	Voiceover             *bool  `json:"voiceover,omitempty"`
	Overlays              *bool  `json:"overlays,omitempty"`
}

// handleVideoPlan builds the typed input for PipelineGen's autonomous
// video.create parent. Media discovery/acquisition belongs to that workflow;
// the control plane does not select pre-existing clips or construct scenes.
func (m *AgentRunsModule) handleVideoPlan(w http.ResponseWriter, req *http.Request) {
	identity := auth.IdentityFromContext(req.Context())
	if identity == nil || identity.WorkspaceID() <= 0 {
		writeError(w, http.StatusUnauthorized, "missing identity")
		return
	}
	var input videoPlanRequest
	if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	input.Topic = strings.TrimSpace(input.Topic)
	input.Title = strings.TrimSpace(input.Title)
	if len(input.Topic) < 3 || len(input.Topic) > 300 {
		writeError(w, http.StatusBadRequest, "topic must be between 3 and 300 characters")
		return
	}
	if input.TargetDurationSeconds == 0 {
		input.TargetDurationSeconds = 180
	}
	if input.TargetDurationSeconds < 30 || input.TargetDurationSeconds > 1800 {
		writeError(w, http.StatusBadRequest, "target_duration_seconds must be between 30 and 1800")
		return
	}
	if input.Title == "" {
		input.Title = input.Topic
	}
	if input.Language == "" {
		input.Language = "it"
	}
	if input.AspectRatio == "" {
		input.AspectRatio = "16:9"
	}
	voiceover, overlays := true, true
	if input.Voiceover != nil {
		voiceover = *input.Voiceover
	}
	if input.Overlays != nil {
		overlays = *input.Overlays
	}
	generation := map[string]any{
		"topic": input.Topic, "language": input.Language,
		"duration_seconds": input.TargetDurationSeconds,
		"media_sources":    []string{"youtube", "stock"},
		"voiceover":        voiceover, "overlays": overlays,
		"aspect_ratio": input.AspectRatio,
		"metadata":     map[string]any{"title": input.Title},
	}
	writeJSON(w, http.StatusOK, map[string]any{"topic": input.Topic, "generation": generation})
}
