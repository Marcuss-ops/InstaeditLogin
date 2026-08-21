package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCreatePost_SendsMediaTargetAndIdempotencyKey(t *testing.T) {
	var gotBody postCreateRequest
	var gotIdempotencyKey string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/posts/" {
			t.Errorf("request = %s %s, want POST /api/v1/posts/", r.Method, r.URL.Path)
		}
		gotIdempotencyKey = r.Header.Get("Idempotency-Key")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatalf("decode create post body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      99,
			"targets": []map[string]any{{"id": 1001, "post_id": 99, "status": "queued"}},
		})
	}))
	defer server.Close()

	c := &client{baseURL: server.URL, apiKey: "sk_test_posts", http: server.Client()}
	response, err := createPost(c, 12, 34, "asset_abc", "Title", "Caption")
	if err != nil {
		t.Fatalf("createPost() error = %v", err)
	}
	if response.ID != 99 || response.Targets[0].ID != 1001 {
		t.Fatalf("response = %+v, want post 99 / target 1001", response)
	}
	if gotBody.WorkspaceID != 12 || gotBody.Content.Title != "Title" || gotBody.Content.Caption != "Caption" {
		t.Fatalf("request body = %+v", gotBody)
	}
	if len(gotBody.Content.Media) != 1 || gotBody.Content.Media[0].AssetID != "asset_abc" {
		t.Fatalf("media = %+v, want asset_abc", gotBody.Content.Media)
	}
	if len(gotBody.Targets) != 1 || gotBody.Targets[0].PlatformAccountID != 34 {
		t.Fatalf("targets = %+v, want account 34", gotBody.Targets)
	}
	if gotIdempotencyKey == "" {
		t.Fatal("Idempotency-Key was empty")
	}
}

func TestWaitForPostTarget_ReturnsRemoteYouTubeID(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/post-targets/1001" {
			t.Errorf("request = %s %s, want GET /api/v1/post-targets/1001", r.Method, r.URL.Path)
		}
		calls++
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			_ = json.NewEncoder(w).Encode(postTargetResponse{ID: 1001, Status: "publishing"})
			return
		}
		_ = json.NewEncoder(w).Encode(postTargetResponse{
			ID:            1001,
			Status:        "published",
			RemotePostID:  "youtube_abc",
			RemotePostURL: "https://youtube.example/watch?v=youtube_abc",
		})
	}))
	defer server.Close()

	c := &client{baseURL: server.URL, apiKey: "sk_test_posts", http: server.Client()}
	target, err := waitForPostTarget(c, 1001, time.Second, time.Millisecond)
	if err != nil {
		t.Fatalf("waitForPostTarget() error = %v", err)
	}
	if target.RemotePostID != "youtube_abc" {
		t.Errorf("remote post id = %q, want youtube_abc", target.RemotePostID)
	}
	if calls != 2 {
		t.Errorf("poll calls = %d, want 2", calls)
	}
}

func TestWaitForPostTarget_ReturnsTerminalFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(postTargetResponse{
			ID:           1001,
			Status:       "failed",
			ErrorMessage: "YouTube upload rejected",
		})
	}))
	defer server.Close()

	c := &client{baseURL: server.URL, apiKey: "sk_test_posts", http: server.Client()}
	if _, err := waitForPostTarget(c, 1001, time.Second, time.Millisecond); err == nil {
		t.Fatal("waitForPostTarget() error = nil, want terminal failure")
	}
}
