package services

import (
	"encoding/json"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

func TestMergeVeloxDestinationMetadata_OverridesPayloadTargetAccountIDs(t *testing.T) {
	dest := &models.ExternalDestination{
		ID:                "extdst_01Jtest0000000000000000000",
		WorkspaceID:       7,
		PlatformAccountID: 123,
		DefaultMetadata:   json.RawMessage(`{"privacy_status":"private"}`),
	}

	// Velox attempts to redirect the upload to a different account.
	payload := json.RawMessage(`{"target_account_ids":[999,888],"title":"from velox"}`)

	merged := MergeVeloxDestinationMetadata(dest, payload)

	var meta models.VeloxDeliveryMetadata
	if err := json.Unmarshal(merged, &meta); err != nil {
		t.Fatalf("merged metadata is not valid JSON: %v", err)
	}

	if len(meta.TargetAccountIDs) != 1 || meta.TargetAccountIDs[0] != 123 {
		t.Fatalf("target_account_ids = %v, want [123]", meta.TargetAccountIDs)
	}
	if meta.Title != "from velox" {
		t.Fatalf("title = %q, want %q", meta.Title, "from velox")
	}
	if meta.PrivacyStatus != "private" {
		t.Fatalf("privacy_status = %q, want %q", meta.PrivacyStatus, "private")
	}
}

func TestMergeVeloxDestinationMetadata_SetsTargetAccountIDsWhenAbsent(t *testing.T) {
	dest := &models.ExternalDestination{
		ID:                "extdst_01Jtest0000000000000000001",
		WorkspaceID:       7,
		PlatformAccountID: 456,
		DefaultMetadata:   json.RawMessage(`{}`),
	}

	payload := json.RawMessage(`{"title":"no targets"}`)

	merged := MergeVeloxDestinationMetadata(dest, payload)

	var meta models.VeloxDeliveryMetadata
	if err := json.Unmarshal(merged, &meta); err != nil {
		t.Fatalf("merged metadata is not valid JSON: %v", err)
	}

	if len(meta.TargetAccountIDs) != 1 || meta.TargetAccountIDs[0] != 456 {
		t.Fatalf("target_account_ids = %v, want [456]", meta.TargetAccountIDs)
	}
	if meta.Title != "no targets" {
		t.Fatalf("title = %q, want %q", meta.Title, "no targets")
	}
}

func TestMergeVeloxDestinationMetadata_DestinationDefaultsDoNotOverridePayload(t *testing.T) {
	dest := &models.ExternalDestination{
		ID:                "extdst_01Jtest0000000000000000002",
		WorkspaceID:       7,
		PlatformAccountID: 789,
		DefaultMetadata:   json.RawMessage(`{"title":"default title","privacy_status":"public"}`),
	}

	payload := json.RawMessage(`{"title":"velox title","target_account_ids":[111]}`)

	merged := MergeVeloxDestinationMetadata(dest, payload)

	var meta models.VeloxDeliveryMetadata
	if err := json.Unmarshal(merged, &meta); err != nil {
		t.Fatalf("merged metadata is not valid JSON: %v", err)
	}

	// Payload title wins over default.
	if meta.Title != "velox title" {
		t.Fatalf("title = %q, want %q", meta.Title, "velox title")
	}
	// Default value fills a missing key.
	if meta.PrivacyStatus != "public" {
		t.Fatalf("privacy_status = %q, want %q", meta.PrivacyStatus, "public")
	}
	// Destination still overrides target_account_ids.
	if len(meta.TargetAccountIDs) != 1 || meta.TargetAccountIDs[0] != 789 {
		t.Fatalf("target_account_ids = %v, want [789]", meta.TargetAccountIDs)
	}
}

// TestMergeVeloxDestinationMetadata_PreservesExtraFields verifies that
// fields not modelled in VeloxDeliveryMetadata (e.g. language, timezone,
// tags) are preserved byte-for-byte during the merge.
func TestMergeVeloxDestinationMetadata_PreservesExtraFields(t *testing.T) {
	dest := &models.ExternalDestination{
		ID:                "extdst_01Jtest0000000000000000003",
		WorkspaceID:       7,
		PlatformAccountID: 42,
		DefaultMetadata:   json.RawMessage(`{"timezone":"Europe/Rome"}`),
	}

	payload := json.RawMessage(`{"title":"extra","language":"it","tags":["a","b"]}`)

	merged := MergeVeloxDestinationMetadata(dest, payload)

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(merged, &raw); err != nil {
		t.Fatalf("merged metadata is not valid JSON: %v", err)
	}

	if string(raw["title"]) != `"extra"` {
		t.Fatalf("title = %s, want %q", raw["title"], "extra")
	}
	if string(raw["language"]) != `"it"` {
		t.Fatalf("language = %s, want %q", raw["language"], "it")
	}
	if string(raw["tags"]) != `["a","b"]` {
		t.Fatalf("tags = %s, want [\"a\",\"b\"]", raw["tags"])
	}
	if string(raw["timezone"]) != `"Europe/Rome"` {
		t.Fatalf("timezone = %s, want %q", raw["timezone"], "Europe/Rome")
	}
	if string(raw["target_account_ids"]) != `[42]` {
		t.Fatalf("target_account_ids = %s, want [42]", raw["target_account_ids"])
	}
}

func TestIsNonEmptyJSONObject(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  bool
	}{
		{"empty", "", false},
		{"null", "null", false},
		{"empty object", "{}", false},
		{"object with space", "  { }  ", false},
		{"non-empty object", `{"a":1}`, true},
		{"array", `[1]`, false},
		{"string", `"hello"`, false},
		{"invalid json", `{"a":`, false},
		{"nested object", `{"a":{"b":1}}`, true},
		{"whitespace only", "   ", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := IsNonEmptyJSONObject(json.RawMessage(tc.input))
			if got != tc.want {
				t.Fatalf("IsNonEmptyJSONObject(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}
