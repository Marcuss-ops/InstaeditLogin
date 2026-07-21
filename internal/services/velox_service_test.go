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

	var meta map[string]any
	if err := json.Unmarshal(merged, &meta); err != nil {
		t.Fatalf("merged metadata is not valid JSON: %v", err)
	}

	want := []int64{123}
	gotRaw, ok := meta["target_account_ids"].([]any)
	if !ok || len(gotRaw) != 1 {
		t.Fatalf("target_account_ids = %v, want %v", meta["target_account_ids"], want)
	}
	if gotRaw[0] != float64(123) {
		t.Fatalf("target_account_ids[0] = %v, want %v", gotRaw[0], want[0])
	}

	// Other payload fields are preserved.
	if meta["title"] != "from velox" {
		t.Fatalf("title = %v, want %q", meta["title"], "from velox")
	}

	// Destination defaults are merged when the payload does not supply them.
	if meta["privacy_status"] != "private" {
		t.Fatalf("privacy_status = %v, want %q", meta["privacy_status"], "private")
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

	var meta map[string]any
	if err := json.Unmarshal(merged, &meta); err != nil {
		t.Fatalf("merged metadata is not valid JSON: %v", err)
	}

	gotRaw, ok := meta["target_account_ids"].([]any)
	if !ok || len(gotRaw) != 1 {
		t.Fatalf("target_account_ids = %v, want [456]", meta["target_account_ids"])
	}
	if gotRaw[0] != float64(456) {
		t.Fatalf("target_account_ids[0] = %v, want 456", gotRaw[0])
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

	var meta map[string]any
	if err := json.Unmarshal(merged, &meta); err != nil {
		t.Fatalf("merged metadata is not valid JSON: %v", err)
	}

	// Payload title wins over default.
	if meta["title"] != "velox title" {
		t.Fatalf("title = %v, want %q", meta["title"], "velox title")
	}

	// Default value fills a missing key.
	if meta["privacy_status"] != "public" {
		t.Fatalf("privacy_status = %v, want %q", meta["privacy_status"], "public")
	}

	// Destination still overrides target_account_ids.
	gotRaw, ok := meta["target_account_ids"].([]any)
	if !ok || len(gotRaw) != 1 || gotRaw[0] != float64(789) {
		t.Fatalf("target_account_ids = %v, want [789]", meta["target_account_ids"])
	}
}
