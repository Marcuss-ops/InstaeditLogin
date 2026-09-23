package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
)

type videoPlanRequest struct {
	Topic                 string `json:"topic"`
	Title                 string `json:"title"`
	TargetDurationSeconds int    `json:"target_duration_seconds"`
}

type catalogAsset struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Source       string `json:"source"`
	ContentHash  string `json:"content_hash"`
	HasDriveFile bool   `json:"has_drive_file"`
}

type catalogAssetDetail struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	ContentHash string         `json:"content_hash"`
	Metadata    map[string]any `json:"metadata"`
	Locations   []struct {
		LocationKind string `json:"location_kind"`
		ExternalID   string `json:"external_id"`
		FileSize     int64  `json:"file_size_bytes"`
	} `json:"locations"`
}

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
	if input.TargetDurationSeconds < 30 || input.TargetDurationSeconds > 600 {
		writeError(w, http.StatusBadRequest, "target_duration_seconds must be between 30 and 600")
		return
	}
	if input.Title == "" {
		input.Title = input.Topic
	}
	search, err := m.deps.JobMaster.SearchMedia(req.Context(), input.Topic, 60)
	if err != nil {
		writeError(w, http.StatusBadGateway, "search execution-plane media: "+err.Error())
		return
	}
	pre, selected, err := buildSceneCompositePre(req.Context(), m.deps.JobMaster, input, search)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"topic": input.Topic, "pre": pre, "finalize": map[string]any{},
		"selected_assets": selected, "scene_count": len(selected),
		"estimated_source_seconds": sumAssetDurations(selected),
	})
}

type plannedAsset struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Source   string `json:"source"`
	DriveID  string `json:"drive_file_id"`
	SHA256   string `json:"sha256"`
	Size     int64  `json:"size_bytes"`
	Duration int    `json:"duration_seconds"`
	Text     string `json:"text"`
}

func buildSceneCompositePre(ctx context.Context, master interface {
	GetMediaAsset(context.Context, string) (json.RawMessage, error)
}, input videoPlanRequest, raw json.RawMessage) (map[string]any, []plannedAsset, error) {
	var envelope struct {
		Items []catalogAsset `json:"items"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, nil, fmt.Errorf("decode media search results: %w", err)
	}
	if len(envelope.Items) == 0 {
		return nil, nil, errors.New("no matching execution-plane media assets were found")
	}
	selected := make([]plannedAsset, 0, 20)
	seenSources := make(map[string]bool)
	for _, candidate := range envelope.Items {
		if !candidate.HasDriveFile || candidate.Source != "youtube" || candidate.ID == "" {
			continue
		}
		detailRaw, err := master.GetMediaAsset(ctx, candidate.ID)
		if err != nil {
			continue
		}
		var detail catalogAssetDetail
		if json.Unmarshal(detailRaw, &detail) != nil {
			continue
		}
		var driveID string
		var size int64
		for _, location := range detail.Locations {
			if location.LocationKind == "drive" && location.ExternalID != "" {
				driveID, size = location.ExternalID, location.FileSize
				break
			}
		}
		if driveID == "" {
			continue
		}
		meta := detail.Metadata
		sourceID, _ := meta["source_video_id"].(string)
		if sourceID == "" {
			sourceID = candidate.ID
		}
		if seenSources[sourceID] {
			continue
		}
		start, _ := numeric(meta["start_sec"])
		end, _ := numeric(meta["end_sec"])
		duration := int(math.Round(end - start))
		if duration <= 0 || duration > 180 {
			duration = 30
		}
		hash := detail.ContentHash
		if hash == "" {
			hash = candidate.ContentHash
		}
		if len(hash) != 64 {
			continue
		}
		name := detail.Name
		if name == "" {
			name = candidate.Name
		}
		text := stringValue(meta["summary"])
		if text == "" {
			text = name
		}
		selected = append(selected, plannedAsset{ID: candidate.ID, Name: name, Source: "youtube", DriveID: driveID, SHA256: hash, Size: size, Duration: duration, Text: text})
		seenSources[sourceID] = true
		if sumAssetDurations(selected) >= input.TargetDurationSeconds {
			break
		}
	}
	if len(selected) == 0 {
		return nil, nil, errors.New("matching assets lack a ready Drive location or SHA-256 hash")
	}
	scenes := make([]map[string]any, 0, len(selected))
	for i, asset := range selected {
		clip := map[string]any{
			"asset_id": asset.ID, "drive_file_id": asset.DriveID,
			"url": "velox-drive://" + asset.DriveID, "sha256": asset.SHA256,
			"size_bytes": asset.Size, "duration_ms": asset.Duration * 1000,
		}
		scenes = append(scenes, map[string]any{
			"scene_id": fmt.Sprintf("scene-%02d", i+1), "index": i,
			"kind": "clip", "text": asset.Text, "duration_seconds": asset.Duration,
			"clip": clip,
		})
	}
	return map[string]any{
		"job_type": "scene.composite.v1", "video_name": input.Title,
		"script_text": input.Topic, "scenes": scenes,
		"output":        map[string]any{"width": 1920, "height": 1080, "fps": 24, "format": "mp4"},
		"delivery_plan": []map[string]any{{"destination_id": "drive-production", "priority": 1, "retry_budget": 3}},
	}, selected, nil
}

func numeric(value any) (float64, bool) {
	number, ok := value.(float64)
	return number, ok
}

func stringValue(value any) string {
	s, _ := value.(string)
	return strings.TrimSpace(s)
}

func sumAssetDurations(assets []plannedAsset) int {
	total := 0
	for _, asset := range assets {
		total += asset.Duration
	}
	return total
}
