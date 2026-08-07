package api

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"gopkg.in/yaml.v3"
)

type youtubeEditorOpenAPI struct {
	Paths      map[string]map[string]interface{} `yaml:"paths"`
	Components struct {
		Schemas map[string]struct {
			Properties map[string]interface{} `yaml:"properties"`
		} `yaml:"schemas"`
	} `yaml:"components"`
}

func loadYouTubeEditorOpenAPI(t *testing.T) youtubeEditorOpenAPI {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Join(filepath.Dir(file), "..", "..")
	body, err := os.ReadFile(filepath.Join(root, "api", "openapi.yaml"))
	if err != nil {
		t.Fatalf("read OpenAPI: %v", err)
	}
	var doc youtubeEditorOpenAPI
	if err := yaml.Unmarshal(body, &doc); err != nil {
		t.Fatalf("parse OpenAPI: %v", err)
	}
	return doc
}

func TestYouTubeEditorRoutesContract_HasNoDuplicateYAMLKeys(t *testing.T) {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Join(filepath.Dir(file), "..", "..")
	body, err := os.ReadFile(filepath.Join(root, "api", "openapi.yaml"))
	if err != nil {
		t.Fatalf("read OpenAPI: %v", err)
	}
	var node yaml.Node
	if err := yaml.Unmarshal(body, &node); err != nil {
		t.Fatalf("parse OpenAPI: %v", err)
	}
	var walk func(*yaml.Node, string)
	walk = func(n *yaml.Node, location string) {
		if n.Kind == yaml.MappingNode {
			seen := make(map[string]struct{}, len(n.Content)/2)
			for i := 0; i+1 < len(n.Content); i += 2 {
				key := n.Content[i].Value
				if _, exists := seen[key]; exists {
					t.Errorf("duplicate YAML key %q at %s", key, location)
				}
				seen[key] = struct{}{}
				walk(n.Content[i+1], location+"."+key)
			}
		} else {
			for _, child := range n.Content {
				walk(child, location)
			}
		}
	}
	walk(&node, "openapi")
}

func TestYouTubeEditorRoutesContract_CoversMountedOperations(t *testing.T) {
	doc := loadYouTubeEditorOpenAPI(t)

	want := map[string][]string{
		"/api/v1/youtube/editor-sessions":                               {"get", "post"},
		"/api/v1/youtube/editor-sessions/by-project/{velox_project_id}": {"get", "patch"},
		"/api/v1/youtube/editor-sessions/by-project/{velox_project_id}/draft": {
			"put",
		},
		"/api/v1/youtube/editor-sessions/by-project/{velox_project_id}/generate-metadata": {
			"post",
		},
		"/api/v1/youtube/editor-sessions/generate-metadata/jobs/{job_id}": {
			"get",
		},
		"/api/v1/youtube/editor-sessions/by-project/{velox_project_id}/publish": {
			"post",
		},
		"/api/v1/youtube/editor-sessions/{id}":           {"get"},
		"/api/v1/youtube/editor-sessions/{id}/publish":   {"post"},
		"/api/v1/youtube/editor-sessions/{id}/thumbnail": {"post"},
	}

	expectedOperationIDs := map[string]map[string]string{
		"/api/v1/youtube/editor-sessions": {
			"get": "listYouTubeEditorSessions", "post": "createYouTubeEditorSession",
		},
		"/api/v1/youtube/editor-sessions/by-project/{velox_project_id}": {
			"get": "getYouTubeEditorSessionByProject", "patch": "updateYouTubeEditorSessionByProject",
		},
		"/api/v1/youtube/editor-sessions/by-project/{velox_project_id}/draft": {
			"put": "saveYouTubeEditorSessionDraft",
		},
		"/api/v1/youtube/editor-sessions/by-project/{velox_project_id}/generate-metadata": {
			"post": "generateYouTubeEditorMetadata",
		},
		"/api/v1/youtube/editor-sessions/generate-metadata/jobs/{job_id}": {
			"get": "pollYouTubeEditorMetadataGeneration",
		},
		"/api/v1/youtube/editor-sessions/{id}": {
			"get": "getYouTubeEditorSessionById",
		},
	}
	expectedRefs := map[string]map[string][]string{
		"/api/v1/youtube/editor-sessions": {
			"get":  {"#/components/schemas/YouTubeEditorSessionListResponse"},
			"post": {"#/components/schemas/YouTubeEditorSessionCreateRequest", "#/components/schemas/YouTubeEditorSessionCreateResponse"},
		},
		"/api/v1/youtube/editor-sessions/by-project/{velox_project_id}": {
			"get":   {"#/components/schemas/YouTubeEditorSessionDetail"},
			"patch": {"#/components/schemas/YouTubeEditorSessionUpdateRequest"},
		},
		"/api/v1/youtube/editor-sessions/by-project/{velox_project_id}/draft": {
			"put": {"#/components/schemas/YouTubeEditorSessionDraftRequest", "#/components/schemas/YouTubeEditorSessionDraftResponse"},
		},
		"/api/v1/youtube/editor-sessions/by-project/{velox_project_id}/generate-metadata": {
			"post": {"#/components/schemas/YouTubeEditorMetadataGenerateRequest", "#/components/schemas/YouTubeEditorMetadataGenerateJobResponse"},
		},
		"/api/v1/youtube/editor-sessions/generate-metadata/jobs/{job_id}": {
			"get": {"#/components/schemas/YouTubeEditorMetadataGenerateJobPollResponse"},
		},
		"/api/v1/youtube/editor-sessions/{id}": {
			"get": {"#/components/schemas/YouTubeEditorSessionDetail"},
		},
	}

	for path, methods := range want {
		operations, ok := doc.Paths[path]
		if !ok {
			t.Errorf("OpenAPI is missing mounted editor route %s", path)
			continue
		}
		for _, method := range methods {
			raw, ok := operations[method]
			if !ok {
				t.Errorf("OpenAPI is missing %s %s", method, path)
				continue
			}
			op, ok := raw.(map[string]interface{})
			if !ok {
				t.Errorf("OpenAPI operation %s %s has unexpected shape %T", method, path, raw)
				continue
			}
			if wantID := expectedOperationIDs[path][method]; wantID != "" {
				if got, _ := op["operationId"].(string); got != wantID {
					t.Errorf("OpenAPI operationId for %s %s = %q, want %q", method, path, got, wantID)
				}
			}
			for _, ref := range expectedRefs[path][method] {
				if !containsOpenAPIRef(op, ref) {
					t.Errorf("OpenAPI operation %s %s is missing ref %q", method, path, ref)
				}
			}
		}
	}
}

func containsOpenAPIRef(value interface{}, want string) bool {
	switch v := value.(type) {
	case map[string]interface{}:
		if ref, ok := v["$ref"].(string); ok && ref == want {
			return true
		}
		for _, child := range v {
			if containsOpenAPIRef(child, want) {
				return true
			}
		}
	case []interface{}:
		for _, child := range v {
			if containsOpenAPIRef(child, want) {
				return true
			}
		}
	}
	return false
}

func TestYouTubeEditorRoutesContract_PreservesLegacyWireNames(t *testing.T) {
	doc := loadYouTubeEditorOpenAPI(t)

	checks := map[string][]string{
		"YouTubeEditorSessionCreateResponse": {"session_id", "velox_project_id", "editor_url"},
		"YouTubeEditorSessionListEntry":      {"editor_url"},
		"YouTubeEditorSessionDetail":         {"velox_project_id"},
	}
	for schemaName, fields := range checks {
		schema, ok := doc.Components.Schemas[schemaName]
		if !ok {
			t.Fatalf("OpenAPI schema %q is missing", schemaName)
		}
		for _, field := range fields {
			if _, ok := schema.Properties[field]; !ok {
				t.Errorf("OpenAPI schema %q lost stable wire field %q", schemaName, field)
			}
		}
	}
}
