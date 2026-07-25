package services

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestUpdateVideoPrivacy_SendsSnippetAndStatus verifies that non-empty
// title/description cause the videos.update request to use
// part=snippet,status and include the snippet fields in the body.
func TestValidateYouTubeSnippet(t *testing.T) {
	cases := []struct {
		name        string
		title       string
		description string
		wantErr     bool
	}{
		{"empty ok", "", "", false},
		{"title only", "Title", "", false},
		{"max title length", strings.Repeat("a", 100), "", false},
		{"title too long", strings.Repeat("a", 101), "", true},
		{"whitespace title only", "   ", "", false},
		{"multi-byte too long", strings.Repeat("é", 101), "", true},
		{"multi-byte ok", strings.Repeat("é", 100), "", false},
		{"description too long", "", strings.Repeat("a", 5001), true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateYouTubeSnippet(tc.title, tc.description)
			if tc.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestUpdateVideoPrivacy_SendsSnippetAndStatus(t *testing.T) {
	var gotPart string
	var body map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPart = r.URL.Query().Get("part")
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	}))
	defer srv.Close()

	svc := newTestYouTubeService(srv)
	if err := svc.UpdateVideoPrivacy(t.Context(), "token", "VID123", "public", nil, "New title", "New description"); err != nil {
		t.Fatalf("UpdateVideoPrivacy error: %v", err)
	}

	if gotPart != "snippet,status" {
		t.Errorf("part: want snippet,status, got %s", gotPart)
	}
	if body["id"] != "VID123" {
		t.Errorf("id: want VID123, got %v", body["id"])
	}
	snippet, ok := body["snippet"].(map[string]interface{})
	if !ok {
		t.Fatalf("snippet not present or not object: %v", body["snippet"])
	}
	if snippet["title"] != "New title" {
		t.Errorf("title: want New title, got %v", snippet["title"])
	}
	if snippet["description"] != "New description" {
		t.Errorf("description: want New description, got %v", snippet["description"])
	}
}

// TestUpdateVideoPrivacy_PartialSnippet verifies that providing only a
// title still uses part=snippet,status and includes only the title in
// the snippet part.
func TestUpdateVideoPrivacy_PartialSnippet(t *testing.T) {
	var gotPart string
	var body map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPart = r.URL.Query().Get("part")
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	}))
	defer srv.Close()

	svc := newTestYouTubeService(srv)
	if err := svc.UpdateVideoPrivacy(t.Context(), "token", "VID123", "public", nil, "Only title", ""); err != nil {
		t.Fatalf("UpdateVideoPrivacy error: %v", err)
	}

	if gotPart != "snippet,status" {
		t.Errorf("part: want snippet,status, got %s", gotPart)
	}
	snippet, ok := body["snippet"].(map[string]interface{})
	if !ok {
		t.Fatalf("snippet not present or not object: %v", body["snippet"])
	}
	if snippet["title"] != "Only title" {
		t.Errorf("title: want Only title, got %v", snippet["title"])
	}
	if _, ok := snippet["description"]; ok {
		t.Errorf("description should not be present when empty")
	}
}

// TestUpdateVideoPrivacy_StatusOnly verifies that an empty title and
// description cause the videos.update request to use part=status only.
func TestUpdateVideoPrivacy_StatusOnly(t *testing.T) {
	var gotPart string
	var body map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPart = r.URL.Query().Get("part")
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	}))
	defer srv.Close()

	svc := newTestYouTubeService(srv)
	if err := svc.UpdateVideoPrivacy(t.Context(), "token", "VID123", "public", nil, "", ""); err != nil {
		t.Fatalf("UpdateVideoPrivacy error: %v", err)
	}

	if gotPart != "status" {
		t.Errorf("part: want status, got %s", gotPart)
	}
	if _, ok := body["snippet"]; ok {
		t.Errorf("snippet should not be present when title/description are empty")
	}
}

// TestUpdateVideoPrivacy_ScheduledPublishing verifies that a future
// publishAt is formatted correctly and forces privacyStatus=private.
func TestUpdateVideoPrivacy_ScheduledPublishing(t *testing.T) {
	var body map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	}))
	defer srv.Close()

	publishAt := time.Date(2026, 8, 1, 18, 0, 0, 0, time.UTC)
	svc := newTestYouTubeService(srv)
	if err := svc.UpdateVideoPrivacy(t.Context(), "token", "VID123", "private", &publishAt, "", ""); err != nil {
		t.Fatalf("UpdateVideoPrivacy error: %v", err)
	}

	status, ok := body["status"].(map[string]interface{})
	if !ok {
		t.Fatalf("status not present or not object: %v", body["status"])
	}
	if status["privacyStatus"] != "private" {
		t.Errorf("privacyStatus: want private, got %v", status["privacyStatus"])
	}
	if status["publishAt"] != "2026-08-01T18:00:00Z" {
		t.Errorf("publishAt: want 2026-08-01T18:00:00Z, got %v", status["publishAt"])
	}
}

// TestUpdateVideoPrivacy_ValidationErrors verifies input validation.
func TestUpdateVideoPrivacy_ValidationErrors(t *testing.T) {
	svc, _ := NewYouTubeOAuthService(youtubeTestCfg())

	cases := []struct {
		name        string
		videoID     string
		privacy     string
		title       string
		description string
		wantErr     string
	}{
		{"empty video id", "", "public", "", "", "empty video id"},
		{"invalid privacy", "VID", "secret", "", "", "invalid privacy status"},
		{"title too long", "VID", "public", strings.Repeat("a", 101), "", "title exceeds"},
		{"description too long", "VID", "public", "", strings.Repeat("a", 5001), "description exceeds"},
		{"multi-byte title too long", "VID", "public", strings.Repeat("é", 101), "", "title exceeds"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := svc.UpdateVideoPrivacy(t.Context(), "token", tc.videoID, tc.privacy, nil, tc.title, tc.description)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}
