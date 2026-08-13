package services

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// TestUpdateVideoMetadata_MergesOverCanonicalSnippet pins the CORE
// contract of the metadata endpoint: videos.update REPLACES the
// snippet, so a patch must read the current canonical snippet first
// and re-send tags / default languages verbatim — otherwise a
// title-only save would wipe them.
func TestUpdateVideoMetadata_MergesOverCanonicalSnippet(t *testing.T) {
	var gotMethod string
	var gotPart string
	var putBody map[string]interface{}
	putCalled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"items": [{
					"id": "VID123",
					"snippet": {
						"title": "Old title",
						"description": "Old description",
						"channelId": "UC123",
						"tags": ["keep-me", "tag-2"],
						"categoryId": "22",
						"defaultLanguage": "it",
						"defaultAudioLanguage": "en"
					}
				}]
			}`))
			return
		}
		putCalled = true
		gotMethod = r.Method
		gotPart = r.URL.Query().Get("part")
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&putBody); err != nil {
			t.Fatalf("decode put body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	}))
	defer srv.Close()

	svc := newTestYouTubeService(srv)
	title := "Nuovo titolo"
	description := "Nuova descrizione"
	categoryID := "24"
	result, err := svc.UpdateVideoMetadata(t.Context(), "token", "VID123", "UC123", models.YouTubeMetadataPatch{
		Title:       &title,
		Description: &description,
		CategoryID:  &categoryID,
	})
	if err != nil {
		t.Fatalf("UpdateVideoMetadata: %v", err)
	}
	if !putCalled {
		t.Fatal("expected a videos.update PUT call")
	}
	if gotMethod != http.MethodPut {
		t.Errorf("method: want PUT, got %s", gotMethod)
	}
	if gotPart != "snippet" {
		t.Errorf("part: want snippet, got %s", gotPart)
	}
	snippet, ok := putBody["snippet"].(map[string]interface{})
	if !ok {
		t.Fatalf("snippet missing: %v", putBody)
	}
	if snippet["title"] != "Nuovo titolo" {
		t.Errorf("title: want Nuovo titolo, got %v", snippet["title"])
	}
	if snippet["description"] != "Nuova descrizione" {
		t.Errorf("description: want Nuova descrizione, got %v", snippet["description"])
	}
	if snippet["categoryId"] != "24" {
		t.Errorf("categoryId: want 24, got %v", snippet["categoryId"])
	}
	// Tags and omitted fields must survive the update.
	tags, ok := snippet["tags"].([]interface{})
	if !ok {
		t.Fatalf("tags missing or not array: %v", snippet["tags"])
	}
	if len(tags) != 2 || tags[0] != "keep-me" || tags[1] != "tag-2" {
		t.Errorf("tags: want [keep-me tag-2], got %v", tags)
	}
	if snippet["defaultLanguage"] != "it" {
		t.Errorf("defaultLanguage: want it, got %v", snippet["defaultLanguage"])
	}
	if snippet["defaultAudioLanguage"] != "en" {
		t.Errorf("defaultAudioLanguage: want en, got %v", snippet["defaultAudioLanguage"])
	}
	if result.VideoID != "VID123" || result.Title != "Nuovo titolo" || result.Description != "Nuova descrizione" || result.CategoryID != "24" {
		t.Errorf("result: got %+v", result)
	}
}

// TestUpdateVideoMetadata_PartialPatchPreservesUntouchedFields pins
// the pointer semantics: patching only the title keeps the canonical
// description / categoryId untouched.
func TestUpdateVideoMetadata_PartialPatchPreservesUntouchedFields(t *testing.T) {
	var putBody map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"items": [{
					"id": "VID123",
					"snippet": {
						"title": "Old title",
						"description": "Old description",
						"channelId": "UC123",
						"tags": ["t"],
						"categoryId": "20"
					}
				}]
			}`))
			return
		}
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&putBody); err != nil {
			t.Fatalf("decode put body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	}))
	defer srv.Close()

	svc := newTestYouTubeService(srv)
	title := "Solo titolo"
	if _, err := svc.UpdateVideoMetadata(t.Context(), "token", "VID123", "UC123", models.YouTubeMetadataPatch{Title: &title}); err != nil {
		t.Fatalf("UpdateVideoMetadata: %v", err)
	}
	snippet := putBody["snippet"].(map[string]interface{})
	if snippet["title"] != "Solo titolo" {
		t.Errorf("title: want Solo titolo, got %v", snippet["title"])
	}
	if snippet["description"] != "Old description" {
		t.Errorf("description must be preserved, got %v", snippet["description"])
	}
	if snippet["categoryId"] != "20" {
		t.Errorf("categoryId must be preserved, got %v", snippet["categoryId"])
	}
}

// TestUpdateVideoMetadata_RejectsForeignChannelBeforePut gates the
// update to the owner channel: a videos.list hit whose channelId
// differs must 403 WITHOUT burning the quota-expensive PUT.
func TestUpdateVideoMetadata_RejectsForeignChannelBeforePut(t *testing.T) {
	putCalled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"items": [{
					"id": "VID123",
					"snippet": {"title": "T", "description": "D", "channelId": "UC-OTHER", "categoryId": "22"}
				}]
			}`))
			return
		}
		putCalled = true
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	}))
	defer srv.Close()

	svc := newTestYouTubeService(srv)
	title := "X"
	_, err := svc.UpdateVideoMetadata(t.Context(), "token", "VID123", "UC123", models.YouTubeMetadataPatch{Title: &title})
	if err == nil {
		t.Fatal("expected error for foreign channel")
	}
	var apiErr *YouTubeAPIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusForbidden {
		t.Fatalf("want 403 YouTubeAPIError, got %v", err)
	}
	if putCalled {
		t.Error("videos.update must NOT be called for a foreign channel")
	}
}

func TestUpdateVideoMetadata_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"items": []}`))
	}))
	defer srv.Close()

	svc := newTestYouTubeService(srv)
	title := "X"
	_, err := svc.UpdateVideoMetadata(t.Context(), "token", "VID123", "UC123", models.YouTubeMetadataPatch{Title: &title})
	if !errors.Is(err, ErrYouTubeVideoNotFound) {
		t.Fatalf("want ErrYouTubeVideoNotFound, got %v", err)
	}
}

func TestUpdateVideoMetadata_Read404WrapsSentinel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("{}"))
	}))
	defer srv.Close()

	svc := newTestYouTubeService(srv)
	title := "X"
	_, err := svc.UpdateVideoMetadata(t.Context(), "token", "VID123", "UC123", models.YouTubeMetadataPatch{Title: &title})
	if !errors.Is(err, ErrYouTubeVideoNotFound) {
		t.Fatalf("want ErrYouTubeVideoNotFound wrapped, got %v", err)
	}
}

// TestUpdateVideoMetadata_ValidationErrors pins that invalid patches
// are rejected BEFORE any upstream read (no network, no quota burn).
func TestUpdateVideoMetadata_ValidationErrors(t *testing.T) {
	svc, _ := NewYouTubeOAuthService(youtubeTestCfg())

	tooLongTitle := strings.Repeat("a", 101)
	emptyTitle := "   "
	tooLongDescription := strings.Repeat("a", 5001)

	cases := []struct {
		name    string
		videoID string
		patch   models.YouTubeMetadataPatch
		want    string
	}{
		{"empty video id", "", models.YouTubeMetadataPatch{}, "empty video id"},
		{"empty title", "VID", models.YouTubeMetadataPatch{Title: &emptyTitle}, "title cannot be empty"},
		{"title too long", "VID", models.YouTubeMetadataPatch{Title: &tooLongTitle}, "title exceeds"},
		{"description too long", "VID", models.YouTubeMetadataPatch{Description: &tooLongDescription}, "description exceeds"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.UpdateVideoMetadata(t.Context(), "token", tc.videoID, "", tc.patch)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error containing %q, got %v", tc.want, err)
			}
		})
	}
}
