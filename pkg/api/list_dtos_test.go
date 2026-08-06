package api

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

func TestPostListResponseUsesLightweightDTOAndCursor(t *testing.T) {
	when := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	posts := []models.Post{{ID: 7, WorkspaceID: 2, Title: "Post", Caption: "caption", Status: models.PostStatusDraft, CreatedAt: when, Metadata: []byte(`{"large":true}`)}}
	response := postListResponse(posts, true, "workspace_id=2")
	if response.NextCursor == "" || !response.HasMore || len(response.Posts) != 1 {
		t.Fatalf("unexpected response: %+v", response)
	}
	payload, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) == "" || containsJSONKey(payload, "metadata") {
		t.Fatalf("list DTO leaked metadata: %s", payload)
	}
}

func TestGroupListResponseUsesLightweightDTO(t *testing.T) {
	response := groupListResponse([]models.Group{{ID: 3, WorkspaceID: 2, Name: "Group"}}, false, "")
	if response.HasMore || len(response.Groups) != 1 || response.NextCursor != "" {
		t.Fatalf("unexpected response: %+v", response)
	}
}

func containsJSONKey(payload []byte, key string) bool {
	var value map[string]any
	if json.Unmarshal(payload, &value) != nil {
		return false
	}
	_, ok := value[key]
	return ok
}
