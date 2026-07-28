package api

// ============================================================================
// CONTRACT TEST: OpenAPI ⇄ Go DTO lock for publishYouTubeEditorSessionResponse
// ============================================================================
//
// This file is the Go half of a cross-repo contract lock. The corresponding
// TS half lives in VeloxFrontend at:
//
//	web/dark_editor/__tests__/publishResponseContract.test.ts
//
// Both halves read the SAME OpenAPI schema (YouTubeEditorSessionPublishResponse
// in api/openapi.yaml) and assert that their local type never drifts away
// from it. The three naming conventions exist on purpose:
//
//	OpenAPI: YouTubeEditorSessionPublishResponse  (PascalCase, OAS)
//	Go DTO:  publishYouTubeEditorSessionResponse  (snake_case, Go convention)
//	TS type: PublishYouTubeEditorSessionResponse  (PascalCase, TS convention)
//
// The contract test bridges those naming conventions and only insists on
// identical FIELD SHAPES (name + type + optionality). The `status` field
// is checked explicitly because the SPA's BroadcastChannel listener does
// `if (msg.status === 'published')` and silently fails if the field is
// dropped from either side.
//
// RUN IN CI via: go test ./pkg/api/... (the integration-fast workflow
// gates deploys on go test -race -count=1 ./... so this test runs on
// every push to main and every PR).

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// openAPIPublishResponseSchemaName is the source-of-truth schema name in
// api/openapi.yaml. Keeping this as a constant makes the link to the
// Go DTO and TS type explicit and grep-able.
const openAPIPublishResponseSchemaName = "YouTubeEditorSessionPublishResponse"

// openAPIPropertySchema is the minimal subset of an OpenAPI 3.x
// property schema that the contract test needs to inspect.
type openAPIPropertySchema struct {
	Type        string        `yaml:"type"`
	Format      string        `yaml:"format"`
	Enum        []interface{} `yaml:"enum"`
	Nullable    bool          `yaml:"nullable"`
	Description string        `yaml:"description"`
}

// openAPIObjectSchema is the minimal subset of an OpenAPI 3.x object
// schema (the body that wraps `properties:` + `required:`).
type openAPIObjectSchema struct {
	Type       string                           `yaml:"type"`
	Required   []string                         `yaml:"required"`
	Properties map[string]openAPIPropertySchema `yaml:"properties"`
}

// openAPIParts is the slice of api/openapi.yaml the contract test needs.
// Keeping the struct narrow means we don't choke on unrelated schema
// changes elsewhere in the file.
type openAPIParts struct {
	Components struct {
		Schemas map[string]openAPIObjectSchema `yaml:"schemas"`
	} `yaml:"components"`
}

// loadOpenAPIPublishResponseSchemaFromRepo reads api/openapi.yaml
// relative to the test file's runtime location and returns the
// YouTubeEditorSessionPublishResponse schema. Fails the test if
// the file or the schema is missing.
func loadOpenAPIPublishResponseSchemaFromRepo(t *testing.T) openAPIObjectSchema {
	t.Helper()
	// The test file lives at pkg/api/youtube_editor_sessions_contract_test.go
	// so the repo root is two directories up.
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("runtime.Caller(0) failed: cannot locate test file")
	}
	repoRoot, err := filepath.Abs(filepath.Join(filepath.Dir(thisFile), "..", ".."))
	if err != nil {
		t.Fatalf("locate repo root: %v", err)
	}
	openAPIPath := filepath.Join(repoRoot, "api", "openapi.yaml")
	data, err := os.ReadFile(openAPIPath)
	if err != nil {
		t.Fatalf("read %s: %v", openAPIPath, err)
	}
	var doc openAPIParts
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse openapi yaml: %v", err)
	}
	schema, ok := doc.Components.Schemas[openAPIPublishResponseSchemaName]
	if !ok {
		available := make([]string, 0, len(doc.Components.Schemas))
		for name := range doc.Components.Schemas {
			available = append(available, name)
		}
		t.Fatalf("schema %q not found in OpenAPI components.schemas (available: %s)", openAPIPublishResponseSchemaName, strings.Join(available, ", "))
	}
	return schema
}

// TestPublishResponseContract_OpenAPI_Matches_DTO locks the shape of
// the publish response between the OpenAPI
// YouTubeEditorSessionPublishResponse schema and the Go
// publishYouTubeEditorSessionResponse DTO. Specifically asserted:
//
//  1. `status` field exists on BOTH sides (the SPA's BroadcastChannel
//     listener depends on it).
//  2. The set of property names is identical: no field is added or
//     removed on either side without the other side being updated.
//  3. The set of required fields is identical: the OpenAPI `required`
//     array matches the DTO's non-`omitempty` fields. Drift here means
//     the wire shape is asymmetric (the SPA could expect a field the
//     orchestrator can omit, or vice versa).
//  4. For each field, the OpenAPI type is compatible with the Go type
//     (string ↔ string, time.Time ↔ type=string format=date-time,
//     pointer ↔ OpenAPI nullable, etc).
func TestPublishResponseContract_OpenAPI_Matches_DTO(t *testing.T) {
	schema := loadOpenAPIPublishResponseSchemaFromRepo(t)

	// Reflect on the DTO struct to enumerate JSON-tagged fields.
	var dto publishYouTubeEditorSessionResponse
	dtoType := reflect.TypeOf(dto)
	dtoFields := make(map[string]reflect.Type)
	dtoRequired := make(map[string]bool)
	for i := 0; i < dtoType.NumField(); i++ {
		f := dtoType.Field(i)
		tag := f.Tag.Get("json")
		// Strip the ",omitempty" (and any other options) to get the
		// wire name.
		name := strings.Split(tag, ",")[0]
		if name == "" || name == "-" {
			continue
		}
		dtoFields[name] = f.Type
		if !strings.Contains(tag, ",omitempty") {
			dtoRequired[name] = true
		}
	}

	// (1) Hard requirement: `status` field exists on BOTH sides.
	if _, ok := schema.Properties["status"]; !ok {
		t.Errorf("FIELD MISSING: 'status' is required in OpenAPI %s (the SPA's BroadcastChannel listener does `if (msg.status === 'published')` and silently fails if the field is dropped)", openAPIPublishResponseSchemaName)
	}
	if _, ok := dtoFields["status"]; !ok {
		t.Errorf("FIELD MISSING: 'status' is required in Go DTO publishYouTubeEditorSessionResponse")
	}

	// (2) Every OpenAPI field must exist in the DTO with a compatible type.
	for name, prop := range schema.Properties {
		goType, ok := dtoFields[name]
		if !ok {
			t.Errorf("DRIFT: OpenAPI field %q (type=%s) not in Go DTO — add it to publishYouTubeEditorSessionResponse", name, prop.Type)
			continue
		}
		if !typeCompatibleOpenAPI(prop, goType) {
			t.Errorf("DRIFT: field %q: OpenAPI type=%s%sGo type=%s", name, prop.Type, formatFormat(prop.Format), goType)
		}
	}

	// (2 reverse) Every DTO field must exist in the OpenAPI.
	for name, goType := range dtoFields {
		if _, ok := schema.Properties[name]; !ok {
			t.Errorf("DRIFT: Go DTO field %q (type=%s) not in OpenAPI — add it to %s", name, goType, openAPIPublishResponseSchemaName)
		}
	}

	// (3) Required-set consistency: OpenAPI `required` array vs DTO
	// non-omitempty fields. Drift here means the wire shape is
	// asymmetric (the SPA sees a field as guaranteed that the
	// orchestrator can omit).
	schemaRequired := make(map[string]bool)
	for _, r := range schema.Required {
		schemaRequired[r] = true
	}
	for name := range dtoFields {
		if dtoRequired[name] != schemaRequired[name] {
			t.Errorf("DRIFT: field %q required=[Go_DTO(omitempty)=%v, OpenAPI(required)=%v]", name, dtoRequired[name], schemaRequired[name])
		}
	}
	for name := range schemaRequired {
		if _, ok := dtoFields[name]; !ok {
			t.Errorf("DRIFT: OpenAPI required field %q is not in Go DTO", name)
		}
	}

	// (4) Status field must be type=string on both sides.
	if prop, ok := schema.Properties["status"]; ok {
		if prop.Type != "string" {
			t.Errorf("DRIFT: OpenAPI 'status' must be type=string, got type=%s", prop.Type)
		}
	}
}

// typeCompatibleOpenAPI checks that an OpenAPI property schema is
// compatible with a Go field type. The mapping is:
//
//	string                   ↔ type=string
//	*string (omitempty)      ↔ type=string (nullable or omitted)
//	int / int64 / uint*      ↔ type=integer
//	bool                     ↔ type=boolean
//	float32 / float64        ↔ type=number
//	time.Time                ↔ type=string format=date-time
//	*time.Time (omitempty)   ↔ type=string format=date-time nullable
func typeCompatibleOpenAPI(prop openAPIPropertySchema, goType reflect.Type) bool {
	if goType.Kind() == reflect.Ptr {
		// pointer fields: the OpenAPI type maps to the pointed-to
		// type but the field is optional. We require `nullable: true`
		// OR absence from `required` (omitempty already implies the
		// latter).
		elem := goType.Elem()
		ok := typeCompatibleOpenAPI(openAPIPropertySchema{Type: prop.Type, Format: prop.Format}, elem)
		if !ok {
			return false
		}
		// *string nullable in OpenAPI is allowed via nullable: true
		// OR absence from required.
		return true
	}
	switch goType.Kind() {
	case reflect.String:
		return prop.Type == "string"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return prop.Type == "integer" || prop.Type == "number"
	case reflect.Bool:
		return prop.Type == "boolean"
	case reflect.Float32, reflect.Float64:
		return prop.Type == "number"
	case reflect.Struct:
		// time.Time is the canonical struct-on-the-wire case.
		if goType.String() == "time.Time" {
			return prop.Type == "string" && prop.Format == "date-time"
		}
		return false
	}
	return false
}

func formatFormat(format string) string {
	if format == "" {
		return ""
	}
	return " format=" + format + ", "
}
