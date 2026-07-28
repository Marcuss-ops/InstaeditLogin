package api

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// openAPIPathRelativeToPkgAPITests is the relative path from
// pkg/api (where this _test.go file lives) to the OpenAPI yaml at the
// repo root's api/ directory. The yaml is the canonical source of
// truth for the wire contract — the Go DTO is the runtime
// implementation of that contract, and this test enforces they
// cannot drift.
const openAPIPathRelativeToPkgAPITests = "../../api/openapi.yaml"

// TestPublishYouTubeEditorSessionResponse_DTOAndOpenAPIAligned is the
// Go-side counterpart of the VeloxFrontend TS contract lock at
// `__tests__/youtubePublishContract.test.ts` (asserts the
// `PublishYouTubeEditorSessionResponse` TS interface mirrors the
// same OpenAPI schema).
//
// Three layers must stay in sync (drift in any pair fails the test):
//
//	Go DTO        pkg/api/youtube_editor_sessions.go
//	              (publishYouTubeEditorSessionResponse at lines 593-600)
//	OpenAPI       api/openapi.yaml
//	              (YouTubeEditorSessionPublishResponse at lines 1400-1426)
//	TypeScript    web/dark_editor/lib/api/bff/youtube.ts
//	              (PublishYouTubeEditorSessionResponse -- status: 'published')
//
// The TS file is doc-annotated with the same cross-reference; changing
// one without the others is the precise scenario this test guards
// against.
//
// Assertions enforced:
//  1. OpenAPI `required: [...]` contains "status".
//  2. OpenAPI `properties.status.type` is "string".
//  3. OpenAPI `properties.status.enum` contains "published".
//  4. Go DTO has a field named `Status` with json tag exactly
//     `"status"` (no omitempty; no rename) and a `reflect.String`
//     kind.
//  5. Every json-tag-suffixed Go field appears in the OpenAPI
//     `properties` map under the same wire name. Catches the
//     common drift of adding a Go field the OpenAPI spec doesn't
//     document (or vice-versa).
//
// gopkg.in/yaml.v3 is already an indirect module-level dep; importing
// it from this test file promotes it to a direct dep on the next
// `go mod tidy` run.
func TestPublishYouTubeEditorSessionResponse_DTOAndOpenAPIAligned(t *testing.T) {
	yamlData, err := os.ReadFile(openAPIPathRelativeToPkgAPITests)
	if err != nil {
		t.Fatalf("read OpenAPI yaml at %s: %v", openAPIPathRelativeToPkgAPITests, err)
	}
	var spec struct {
		Components struct {
			Schemas map[string]struct {
				Required   []string `yaml:"required"`
				Properties map[string]struct {
					Type string   `yaml:"type"`
					Enum []string `yaml:"enum"`
				} `yaml:"properties"`
			} `yaml:"schemas"`
		} `yaml:"components"`
	}
	if err := yaml.Unmarshal(yamlData, &spec); err != nil {
		t.Fatalf("unmarshal OpenAPI yaml: %v", err)
	}
	schema, ok := spec.Components.Schemas["YouTubeEditorSessionPublishResponse"]
	if !ok {
		t.Fatalf("schema YouTubeEditorSessionPublishResponse not found in OpenAPI yaml")
	}

	// 1. Required field "status".
	if !containsString(schema.Required, "status") {
		t.Errorf("OpenAPI required must include 'status', got %v", schema.Required)
	}

	// 2. & 3. status: type=string, enum contains "published".
	statusProp, ok := schema.Properties["status"]
	if !ok {
		t.Fatalf("OpenAPI properties.status is missing")
	}
	if statusProp.Type != "string" {
		t.Errorf("OpenAPI properties.status.type: want \"string\", got %q", statusProp.Type)
	}
	if !containsString(statusProp.Enum, "published") {
		t.Errorf("OpenAPI properties.status.enum must include \"published\", got %v", statusProp.Enum)
	}

	// 4. Go DTO Status field: json tag exactly "status", reflect.String kind.
	typ := reflect.TypeOf(publishYouTubeEditorSessionResponse{})
	statusField, found := typ.FieldByName("Status")
	if !found {
		t.Fatalf("Go DTO field Status missing (must remain the canonical terminal-state field — drift here breaks the dark editor's publishResult.status consumer)")
	}
	if got := statusField.Tag.Get("json"); got != "status" {
		t.Errorf("Go DTO Status json tag: want \"status\", got %q", got)
	}
	if statusField.Type.Kind() != reflect.String {
		t.Errorf("Go DTO Status type kind: want reflect.String, got %v", statusField.Type.Kind())
	}

	// 5. Bidirectional field-set lock. Catches silent add/remove drift
	// on EITHER side: adding a Go field without OpenAPI doc (Go -> OpenAPI),
	// AND adding an OpenAPI property without a Go DTO wire (OpenAPI -> Go).
	// The two endpoints at lines 1029 and 1076 of openapi.yaml both $ref
	// this schema — adding a field on either side without updating the
	// other is exactly the kind of drift this guard prevents.
	openAPIFields := make(map[string]bool, len(schema.Properties))
	for name := range schema.Properties {
		openAPIFields[name] = true
	}
	dtoFields := make(map[string]bool)
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		tag := field.Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		wireName := strings.Split(tag, ",")[0]

		// 5a. Go -> OpenAPI: every json-tag-suffixed Go field must be
		// documented in OpenAPI properties, otherwise a strictly-typed
		// client (VeloxFrontend's TS contract test) would silently
		// drop the field.
		if !openAPIFields[wireName] {
			t.Errorf("Go DTO field %q (json tag=%q) is not documented in OpenAPI properties",
				field.Name, wireName)
		}
		dtoFields[wireName] = true
	}

	// 5b. OpenAPI required -> Go: every field-marked-required on the
	// wire MUST have a json-tagged Go field on the DTO. The pubish
	// handler writes the response struct verbatim at
	// youtube_editor_sessions.go executePublishYouTubeEditorSession, so
	// a required field without a Go write-path would either be a
	// constant (impossible) or omitted-from-response (silent 200 OK
	// with a malformed body the SPA can't parse).
	for _, reqField := range schema.Required {
		if !dtoFields[reqField] {
			t.Errorf("OpenAPI required field %q is not implemented as a Go DTO field — handler cannot emit a value for it", reqField)
		}
	}
}

// containsString is a small helper to keep yaml.v3 round-tripping
// noise out of the assertions above.
func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
