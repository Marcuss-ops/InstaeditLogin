package veloxcontract

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAdaptSpecSceneSubmissionMapsAssetsBindingsVisualsAudioAndTimeline(t *testing.T) {
	input := SpecSceneSubmission{
		ContractVersion: "velox.job.v1",
		IdempotencyKey:  "spec-scene-1",
		JobType:         "scene.composite.v1",
		TemplateID:      "documentary.clip-stock.v1",
		TemplateVersion: 1,
		VideoName:       "Five legendary boxers",
		Output:          &JobOutput{Width: 1920, Height: 1080, FPS: 30, Format: "mp4"},
		DeliveryPlan:    DeliveryPlan{Destinations: []DeliveryDestination{{ExternalDestinationID: "dest-1"}}},
		Scenes: []SpecScene{{
			ID:         "scene-1",
			Text:       "Muhammad Ali",
			DurationMS: 2500,
			Assets: &SpecSceneAssets{
				PrimaryClip: &SpecSceneAsset{AssetID: "clip-1", Type: "video"},
				Stock:       &SpecSceneAsset{URI: "velox-asset://stock-1", Fallback: true},
			},
			Bindings: SpecSceneBindings{
				Clip:      &SpecSceneAsset{AssetID: "clip-1"},
				Voiceover: &SpecSceneAsset{URI: "velox-asset://voice-1", Type: "audio"},
			},
			VisualSlots: []SpecSceneVisualSlot{{
				ID: "intro", Asset: &SpecSceneAsset{AssetID: "image-1"}, Layer: "foreground", Width: 1, Height: 1,
			}},
			VisualAssignments: []SpecSceneVisualAssignment{{Slot: "intro", Asset: &SpecSceneAsset{AssetID: "image-1"}}},
			Audio:             &SpecSceneAudio{Voiceover: &SpecSceneAsset{URI: "velox-asset://voice-1"}, Ducking: 0.5},
			Timeline:          &SpecSceneTimeline{StartMS: 0, EndMS: 2500, DurationMS: 2500},
			Subtitles:         []SpecSceneSubtitle{{Text: "Ali", StartMS: 0, EndMS: 500, Language: "it"}},
			Transition:        &SpecSceneTransition{Type: "fade", DurationMS: 200},
		}},
	}

	got, err := AdaptSpecSceneSubmission(input)
	if err != nil {
		t.Fatalf("AdaptSpecSceneSubmission: %v", err)
	}
	if got.IdempotencyKey != input.IdempotencyKey || got.JobType != input.JobType {
		t.Fatalf("envelope identity was not preserved: %+v", got)
	}
	var spec struct {
		Scenes []map[string]json.RawMessage `json:"scenes"`
	}
	if err := json.Unmarshal(got.Spec, &spec); err != nil {
		t.Fatalf("decode canonical spec: %v", err)
	}
	if len(spec.Scenes) != 1 {
		t.Fatalf("scene count = %d, want 1", len(spec.Scenes))
	}
	for _, key := range []string{"assets", "bindings", "visual_slots", "visual_assignments", "audio", "timeline", "subtitles", "transition"} {
		if _, ok := spec.Scenes[0][key]; !ok {
			t.Errorf("canonical scene missing %q: %s", key, got.Spec)
		}
	}
	var assets struct {
		PrimaryClip struct {
			AssetID string `json:"asset_id"`
		} `json:"primary_clip"`
	}
	if err := json.Unmarshal(spec.Scenes[0]["assets"], &assets); err != nil || assets.PrimaryClip.AssetID != "clip-1" {
		t.Fatalf("primary clip mapping incorrect: %v / %+v", err, assets)
	}
	if strings.Contains(string(got.Spec), "local_path") {
		t.Fatal("canonical spec must not contain local_path")
	}
	if err := got.ValidateCanonical(); err != nil {
		t.Fatalf("adapted envelope should validate: %v", err)
	}
	var typed struct {
		Scenes []struct {
			ID string `json:"id"`
		} `json:"scenes"`
	}
	if err := json.Unmarshal(got.Spec, &typed); err != nil || len(typed.Scenes) != 1 || typed.Scenes[0].ID != "scene-1" {
		t.Fatalf("adapted spec is not a canonical scene payload: %v / %+v", err, typed)
	}
}

func TestAdaptSpecSceneSubmissionRejectsUnresolvedLocalPath(t *testing.T) {
	input := minimalSpecSceneSubmission()
	input.Scenes[0].LocalPath = "/tmp/scene.mp4"
	_, err := AdaptSpecSceneSubmission(input)
	if err == nil || !strings.Contains(err.Error(), "local_path") {
		t.Fatalf("error = %v, want scene local_path rejection", err)
	}

	input = minimalSpecSceneSubmission()
	input.Scenes[0].Bindings.Clip = &SpecSceneAsset{LocalPath: "/tmp/clip.mp4"}
	_, err = AdaptSpecSceneSubmission(input)
	if err == nil || !strings.Contains(err.Error(), "local_path") {
		t.Fatalf("error = %v, want local_path rejection", err)
	}
}

func TestAdaptSpecSceneSubmissionAcceptsPortableLocalPathURI(t *testing.T) {
	input := minimalSpecSceneSubmission()
	input.Scenes[0].Bindings.Clip = &SpecSceneAsset{LocalPath: "velox-asset://clip-1"}
	got, err := AdaptSpecSceneSubmission(input)
	if err != nil {
		t.Fatalf("portable local path should adapt: %v", err)
	}
	if !strings.Contains(string(got.Spec), "velox-asset://clip-1") {
		t.Fatalf("portable URI missing from canonical spec: %s", got.Spec)
	}
}

func TestAdaptSpecSceneSubmissionRejectsMissingSceneID(t *testing.T) {
	input := minimalSpecSceneSubmission()
	input.Scenes[0].ID = ""
	_, err := AdaptSpecSceneSubmission(input)
	if err == nil || !strings.Contains(err.Error(), "id is required") {
		t.Fatalf("error = %v, want scene id validation", err)
	}
}

func TestAdaptSpecSceneSubmissionRejectsMissingEnvelopeRequirements(t *testing.T) {
	input := minimalSpecSceneSubmission()
	input.IdempotencyKey = ""
	_, err := AdaptSpecSceneSubmission(input)
	if err == nil || !strings.Contains(err.Error(), "idempotency_key") {
		t.Fatalf("error = %v, want idempotency validation", err)
	}
}

func minimalSpecSceneSubmission() SpecSceneSubmission {
	return SpecSceneSubmission{
		ContractVersion: "velox.job.v1",
		IdempotencyKey:  "spec-scene-minimal",
		JobType:         "scene.composite.v1",
		TemplateID:      "template",
		TemplateVersion: 1,
		VideoName:       "Video",
		Output:          &JobOutput{Width: 1920, Height: 1080, FPS: 30, Format: "mp4"},
		DeliveryPlan:    DeliveryPlan{Destinations: []DeliveryDestination{{ExternalDestinationID: "dest-1"}}},
		Scenes:          []SpecScene{{ID: "scene-1"}},
	}
}
