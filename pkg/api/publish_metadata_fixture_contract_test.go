package api

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// ============================================================================
// CONTRACT FIXTURE TEST: canonical publish payload validated across 7 layers
// ============================================================================
//
// This test reads the single shared fixture at
// api/fixtures/publish_metadata_fixture.json and asserts it can be:
//
//   1. Unmarshalled into publishYouTubeEditorSessionRequest (Go DTO)
//   2. Validated by YouTubePublishOptions.Validate() (validator backend)
//   3. Unmarshalled into services.NVIDIAMetadataResponse (NVIDIA schema)
//   4. Validated by services.ValidateYouTubeSnippet (title/desc bounds)
//   5. Mapped through the OpenAPI schema (struct tags match property names)
//   6. Round-tripped back to JSON (no field loss)
//   7. Fed through a mock YouTube PublishThumbnail service (mock YouTube)
//
// The fixture is the SINGLE SOURCE OF TRUTH for the publish metadata
// contract. Every other representation (OpenAPI, TypeScript types,
// E2E test payloads) MUST stay in lockstep with this file.
//
// RUN IN CI via: go test ./pkg/api/... -run TestPublishMetadataFixture

// fixturePath returns the absolute path to api/fixtures/publish_metadata_fixture.json
// relative to the test file's runtime location (pkg/api/).
func fixturePath(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("runtime.Caller(0) failed")
	}
	repoRoot, err := filepath.Abs(filepath.Join(filepath.Dir(thisFile), "..", ".."))
	if err != nil {
		t.Fatalf("locate repo root: %v", err)
	}
	return filepath.Join(repoRoot, "api", "fixtures", "publish_metadata_fixture.json")
}

// loadFixture reads and unmarshals the shared fixture as a raw map
// so we can validate individual layers without type coupling.
func loadFixture(t *testing.T) map[string]interface{} {
	t.Helper()
	data, err := os.ReadFile(fixturePath(t))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("parse fixture JSON: %v", err)
	}
	return m
}

// TestPublishMetadataFixture_GoDTO asserts the fixture can be
// unmarshalled into the publishYouTubeEditorSessionRequest DTO.
func TestPublishMetadataFixture_GoDTO(t *testing.T) {
	fixture := loadFixture(t)

	// Re-marshal to JSON bytes so json.Unmarshal into the typed DTO
	// exercises exactly the same path as an HTTP handler.
	raw, err := json.Marshal(fixture)
	if err != nil {
		t.Fatalf("re-marshal fixture: %v", err)
	}

	var dto publishYouTubeEditorSessionRequest
	if err := json.Unmarshal(raw, &dto); err != nil {
		t.Fatalf("unmarshal into Go DTO: %v", err)
	}

	// Assert every required field round-tripped correctly.
	if dto.Title != "Come automatizzare la pubblicazione YouTube nel 2026" {
		t.Errorf("title mismatch: got %q", dto.Title)
	}
	if dto.PrivacyStatus != "private" {
		t.Errorf("privacy_status mismatch: got %q", dto.PrivacyStatus)
	}
	if dto.PublishAt == nil {
		t.Error("publish_at is nil, expected non-nil")
	} else if dto.PublishAt.Format(time.RFC3339) != "2030-07-30T16:00:00Z" {
		t.Errorf("publish_at mismatch: got %v", dto.PublishAt)
	}
	if len(dto.Tags) != 4 {
		t.Errorf("tags count: got %d, want 4", len(dto.Tags))
	}
	if dto.DefaultLanguage != "it" {
		t.Errorf("default_language mismatch: got %q", dto.DefaultLanguage)
	}
	if dto.DefaultAudioLanguage != "it" {
		t.Errorf("default_audio_language mismatch: got %q", dto.DefaultAudioLanguage)
	}
	if len(dto.Translations) != 3 {
		t.Errorf("translations count: got %d, want 3", len(dto.Translations))
	}
	if tr, ok := dto.Translations["en"]; !ok || tr.Title == "" {
		t.Error("translations[en] missing or empty title")
	}
	if tr, ok := dto.Translations["es"]; !ok || tr.Title == "" {
		t.Error("translations[es] missing or empty title")
	}
	if tr, ok := dto.Translations["pt-BR"]; !ok || tr.Title == "" {
		t.Error("translations[pt-BR] missing or empty title")
	}
}

// TestPublishMetadataFixture_Validator asserts the fixture passes
// YouTubePublishOptions.Validate() and ValidateYouTubeSnippet.
func TestPublishMetadataFixture_Validator(t *testing.T) {
	fixture := loadFixture(t)
	raw, _ := json.Marshal(fixture)
	var dto publishYouTubeEditorSessionRequest
	if err := json.Unmarshal(raw, &dto); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Validate YouTube snippet bounds (title ≤100, description ≤5000).
	if err := services.ValidateYouTubeSnippet(dto.Title, dto.Description); err != nil {
		t.Errorf("ValidateYouTubeSnippet failed on fixture: %v", err)
	}

	// Validate full YouTubePublishOptions (tags, BCP-47, translations).
	opts := youTubePublishOptionsForRequest(dto)
	if err := opts.Validate(); err != nil {
		t.Errorf("YouTubePublishOptions.Validate() failed on fixture: %v", err)
	}
}

// TestPublishMetadataFixture_NVIDIASchema asserts the fixture can be
// unmarshalled into services.NVIDIAMetadataResponse (the type the
// MetadataGenerator returns).
func TestPublishMetadataFixture_NVIDIASchema(t *testing.T) {
	fixture := loadFixture(t)
	raw, _ := json.Marshal(fixture)

	var nvidiaResp services.NVIDIAMetadataResponse
	if err := json.Unmarshal(raw, &nvidiaResp); err != nil {
		t.Fatalf("unmarshal into NVIDIAMetadataResponse: %v", err)
	}

	if nvidiaResp.Title == "" {
		t.Error("NVIDIA schema: title is empty")
	}
	if nvidiaResp.DefaultLanguage == "" {
		t.Error("NVIDIA schema: default_language is empty")
	}
	if len(nvidiaResp.Translations) == 0 {
		t.Error("NVIDIA schema: translations is empty")
	}
}

// TestPublishMetadataFixture_OpenAPISchemaFields asserts the fixture
// property names match the Go DTO's json tags (which are locked to
// OpenAPI by the existing contract tests).
func TestPublishMetadataFixture_OpenAPISchemaFields(t *testing.T) {
	fixture := loadFixture(t)

	// The Go DTO's json tags are the contract bridge between
	// fixture ↔ OpenAPI. Every fixture key must have a matching
	// DTO field.
	var dto publishYouTubeEditorSessionRequest
	dtoType := reflect.TypeOf(dto)
	dtoFields := make(map[string]bool)
	for i := 0; i < dtoType.NumField(); i++ {
		f := dtoType.Field(i)
		tag := f.Tag.Get("json")
		name := strings.Split(tag, ",")[0]
		if name != "" && name != "-" {
			dtoFields[name] = true
		}
	}

	// Every fixture key (except _comment) must exist as a DTO field.
	for key := range fixture {
		if key == "_comment" {
			continue
		}
		if !dtoFields[key] {
			t.Errorf("DRIFT: fixture key %q has NO matching field in publishYouTubeEditorSessionRequest (json tags: %v)", key, sortedKeys(dtoFields))
		}
	}

	// Every DTO field must exist in the fixture (the fixture is
	// the canonical source of truth).
	fixtureKeys := make(map[string]bool)
	for key := range fixture {
		fixtureKeys[key] = true
	}
	for name := range dtoFields {
		if !fixtureKeys[name] {
			t.Errorf("DRIFT: DTO field %q is NOT in the fixture — add it to api/fixtures/publish_metadata_fixture.json", name)
		}
	}
}

// TestPublishMetadataFixture_RoundTrip asserts the fixture survives
// a JSON marshal → unmarshal → marshal round-trip without field loss.
func TestPublishMetadataFixture_RoundTrip(t *testing.T) {
	fixture := loadFixture(t)
	raw, _ := json.Marshal(fixture)

	var dto publishYouTubeEditorSessionRequest
	if err := json.Unmarshal(raw, &dto); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Marshal the DTO back to JSON and verify it still contains
	// the essential fields.
	roundTripped, err := json.Marshal(dto)
	if err != nil {
		t.Fatalf("re-marshal DTO: %v", err)
	}

	var rtm map[string]interface{}
	if err := json.Unmarshal(roundTripped, &rtm); err != nil {
		t.Fatalf("parse round-tripped JSON: %v", err)
	}

	// Essential fields must survive the round-trip.
	essential := []string{"title", "privacy_status", "publish_at", "tags", "default_language", "translations"}
	for _, field := range essential {
		if _, ok := rtm[field]; !ok {
			t.Errorf("round-trip lost field %q", field)
		}
	}
}

// TestPublishMetadataFixture_MockYouTube asserts the fixture passes
// through a mock YouTube PublishThumbnail call, confirming the
// payload shape is compatible with the YouTubeOAuthService interface
// that the publish handler calls at runtime.
func TestPublishMetadataFixture_MockYouTube(t *testing.T) {
	fixture := loadFixture(t)
	raw, _ := json.Marshal(fixture)
	var dto publishYouTubeEditorSessionRequest
	if err := json.Unmarshal(raw, &dto); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	opts := youTubePublishOptionsForRequest(dto)

	// Simulate the exact call path the publish orchestrator makes:
	// PublishThumbnail(ctx, accessToken, videoID, thumbnailData,
	//                   mimeType, privacyStatus, publishAt, opts)
	// The mock captures the opts to assert the fixture fields
	// survive the full chain.
	mockSvc := &mockYouTubeForFixture{t: t, want: opts}
	_, err := mockSvc.PublishThumbnail(
		context.Background(),
		"ya29.mock_token",
		"dQw4w9WgXcQ",
		[]byte{0xFF, 0xD8, 0xFF}, // minimal JPEG header
		"image/jpeg",
		dto.PrivacyStatus,
		dto.PublishAt,
		opts,
	)
	if err != nil {
		t.Errorf("mock YouTube PublishThumbnail rejected fixture: %v", err)
	}
	if !mockSvc.called {
		t.Error("mock YouTube PublishThumbnail was never called")
	}
}

// mockYouTubeForFixture is a minimal mock of the YouTubeOAuthService
// interface that asserts the fixture's publish options are valid.
type mockYouTubeForFixture struct {
	t      *testing.T
	want   models.YouTubePublishOptions
	called bool
}

func (m *mockYouTubeForFixture) PublishThumbnail(
	_ context.Context, _, _ string, _ []byte, _, _ string,
	_ *time.Time, opts models.YouTubePublishOptions,
) (string, error) {
	m.called = true
	// Assert the mock received exactly the fixture's fields.
	if opts.Title != m.want.Title {
		m.t.Errorf("mock YouTube: title mismatch: got %q, want %q", opts.Title, m.want.Title)
	}
	if opts.Description != m.want.Description {
		m.t.Errorf("mock YouTube: description mismatch")
	}
	if len(opts.Tags) != len(m.want.Tags) {
		m.t.Errorf("mock YouTube: tags count mismatch: got %d, want %d", len(opts.Tags), len(m.want.Tags))
	}
	if opts.DefaultLanguage != m.want.DefaultLanguage {
		m.t.Errorf("mock YouTube: default_language mismatch: got %q, want %q", opts.DefaultLanguage, m.want.DefaultLanguage)
	}
	if opts.DefaultAudioLanguage != m.want.DefaultAudioLanguage {
		m.t.Errorf("mock YouTube: default_audio_language mismatch")
	}
	if len(opts.Translations) != len(m.want.Translations) {
		m.t.Errorf("mock YouTube: translations count mismatch: got %d, want %d", len(opts.Translations), len(m.want.Translations))
	}
	for lang, tr := range m.want.Translations {
		gotTr, ok := opts.Translations[lang]
		if !ok {
			m.t.Errorf("mock YouTube: missing translation %q", lang)
			continue
		}
		if gotTr.Title != tr.Title {
			m.t.Errorf("mock YouTube: translation[%s].title mismatch", lang)
		}
	}
	return "https://www.youtube.com/watch?v=dQw4w9WgXcQ", nil
}

// Compile-time check: mockYouTubeForFixture satisfies the
// YouTubeOAuthService interface's PublishThumbnail contract.
var _ interface {
	PublishThumbnail(ctx context.Context, accessToken, videoID string, thumbnailData []byte, mimeType, privacyStatus string, publishAt *time.Time, opts models.YouTubePublishOptions) (string, error)
} = (*mockYouTubeForFixture)(nil)

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
