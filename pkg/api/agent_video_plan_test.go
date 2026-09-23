package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/agenttools"
	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/jobmaster"
	"github.com/go-chi/chi/v5"
)

type plannerMediaFake struct{ details map[string]json.RawMessage }

func (f plannerMediaFake) GetMediaAsset(_ context.Context, id string) (json.RawMessage, error) {
	return f.details[id], nil
}

type plannerAPIStub struct {
	jobmaster.API
	plannerMediaFake
	search json.RawMessage
}

func (f plannerAPIStub) SearchMedia(context.Context, string, int) (json.RawMessage, error) {
	return f.search, nil
}

func (f plannerAPIStub) GetMediaAsset(ctx context.Context, id string) (json.RawMessage, error) {
	return f.plannerMediaFake.GetMediaAsset(ctx, id)
}

func TestBuildSceneCompositePreUsesSearchableReadyDriveAssets(t *testing.T) {
	search := json.RawMessage(`{"items":[{"id":"yt_1","name":"Tyson training","source":"youtube","content_hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","has_drive_file":true},{"id":"yt_2","name":"Tyson interview","source":"youtube","content_hash":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","has_drive_file":true}]}`)
	detail := func(id, name, hash, drive, source string, start, end float64) json.RawMessage {
		value, _ := json.Marshal(map[string]any{
			"id": id, "name": name, "content_hash": hash,
			"metadata":  map[string]any{"source_video_id": source, "start_sec": start, "end_sec": end, "summary": name + " summary"},
			"locations": []map[string]any{{"location_kind": "drive", "external_id": drive, "file_size_bytes": 1024}},
		})
		return value
	}
	master := plannerMediaFake{details: map[string]json.RawMessage{
		"yt_1": detail("yt_1", "Tyson training", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "drive-1", "source-1", 10, 70),
		"yt_2": detail("yt_2", "Tyson interview", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "drive-2", "source-2", 100, 190),
	}}
	pre, selected, err := buildSceneCompositePre(context.Background(), master, videoPlanRequest{Topic: "Mike Tyson", Title: "Tyson", TargetDurationSeconds: 120}, search)
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 2 || sumAssetDurations(selected) != 150 {
		t.Fatalf("selected assets=%+v, want 2 scenes / 150s", selected)
	}
	scenes, ok := pre["scenes"].([]map[string]any)
	if !ok || len(scenes) != 2 {
		t.Fatalf("pre scenes=%#v", pre["scenes"])
	}
	clip := scenes[0]["clip"].(map[string]any)
	if clip["drive_file_id"] != "drive-1" || clip["url"] != "velox-drive://drive-1" || clip["sha256"] != selected[0].SHA256 {
		t.Fatalf("generated clip reference=%+v", clip)
	}
	if pre["script_text"] != "Mike Tyson" || pre["video_name"] != "Tyson" {
		t.Fatalf("generated plan metadata=%+v", pre)
	}
}

func TestBuildSceneCompositePreRejectsMediaWithoutDriveLocation(t *testing.T) {
	search := json.RawMessage(`{"items":[{"id":"yt_1","name":"Tyson","source":"youtube","content_hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","has_drive_file":true}]}`)
	master := plannerMediaFake{details: map[string]json.RawMessage{"yt_1": json.RawMessage(`{"id":"yt_1","content_hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","locations":[]}`)}}
	if _, _, err := buildSceneCompositePre(context.Background(), master, videoPlanRequest{Topic: "Mike Tyson", TargetDurationSeconds: 60}, search); err == nil {
		t.Fatal("expected missing Drive source to be rejected")
	}
}

func TestAgentVideoPlanRouteBuildsScenePlanFromM2MCatalog(t *testing.T) {
	hash := strings.Repeat("a", 64)
	media := plannerAPIStub{
		search: json.RawMessage(`{"items":[{"id":"yt_1","name":"Tyson training","source":"youtube","content_hash":"` + hash + `","has_drive_file":true}]}`),
		plannerMediaFake: plannerMediaFake{details: map[string]json.RawMessage{
			"yt_1": json.RawMessage(`{"id":"yt_1","name":"Tyson training","content_hash":"` + hash + `","metadata":{"start_sec":0,"end_sec":40,"source_video_id":"source-1"},"locations":[{"location_kind":"drive","external_id":"drive-1","file_size_bytes":1000}]}`),
		}},
	}
	module := NewAgentRunsModule(AgentRunsModuleDeps{
		Store: newFakeAgentRunStore(), Catalog: agenttools.NewCatalog(), JobMaster: media,
		Protected:               func(h http.HandlerFunc) http.HandlerFunc { return h },
		ProtectedWithPermission: func(_ string, h http.HandlerFunc) http.HandlerFunc { return h },
	})
	mux := chi.NewRouter()
	module.Register(mux)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agent/video-plan", bytes.NewBufferString(`{"topic":"Mike Tyson training","target_duration_seconds":60}`))
	req = req.WithContext(auth.WithIdentity(req.Context(), auth.NewApiKeyIdentity(9, 42, 7, []string{agenttools.PermissionAutomation})))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("video plan: status=%d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		SceneCount int `json:"scene_count"`
		Pre        struct {
			Scenes []map[string]any `json:"scenes"`
		} `json:"pre"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.SceneCount != 1 || len(body.Pre.Scenes) != 1 {
		t.Fatalf("plan scene counts: response=%+v", body)
	}
}
