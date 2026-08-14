package services

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// TestPublishThumbnail_TwentyVideo_PublicAndScheduled is the batch regression
// test for the YouTube thumbnail publication flow. It exercises 20 distinct
// videos through the same production orchestrator used by the BFF:
//   - videos 01-10: thumbnail + immediate public publication
//   - videos 11-20: thumbnail + future scheduled publication
//
// For every video the thumbnail must be uploaded before videos.update. Future
// publication requests deliberately enter with privacy="public" so the test
// also pins CoercePrivacyForUpdate's YouTube-required private + publishAt shape.
func TestPublishThumbnail_TwentyVideo_PublicAndScheduled(t *testing.T) {
	type capturedUpdate struct {
		Status  map[string]interface{}
		Snippet map[string]interface{}
	}

	var events []string
	thumbnailCalls := make(map[string]int)
	updateCalls := make(map[string]int)
	updates := make(map[string]capturedUpdate)

	mux := http.NewServeMux()
	mux.HandleFunc("/upload/youtube/v3/thumbnails/set", func(w http.ResponseWriter, r *http.Request) {
		videoID := r.URL.Query().Get("videoId")
		if videoID == "" {
			http.Error(w, "missing videoId", http.StatusBadRequest)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil || len(body) == 0 {
			http.Error(w, "missing thumbnail body", http.StatusBadRequest)
			return
		}
		if got := r.Header.Get("Content-Type"); got != "image/jpeg" {
			http.Error(w, "unexpected thumbnail content type", http.StatusBadRequest)
			return
		}

		thumbnailCalls[videoID]++
		events = append(events, "thumbnail:"+videoID)
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/youtube/v3/videos", func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, "invalid update payload", http.StatusBadRequest)
			return
		}
		videoID, _ := payload["id"].(string)
		if videoID == "" {
			http.Error(w, "missing update video id", http.StatusBadRequest)
			return
		}
		status, _ := payload["status"].(map[string]interface{})
		snippet, _ := payload["snippet"].(map[string]interface{})
		if status == nil {
			http.Error(w, "missing update status", http.StatusBadRequest)
			return
		}

		updateCalls[videoID]++
		updates[videoID] = capturedUpdate{Status: status, Snippet: snippet}
		events = append(events, "update:"+videoID)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	svc := newTestYouTubeService(srv)
	fixedNow := time.Date(2030, time.January, 2, 9, 0, 0, 0, time.UTC)
	svc.clock = func() time.Time { return fixedNow }

	for i := 1; i <= 20; i++ {
		videoID := fmt.Sprintf("batch-video-%02d", i)
		var publishAt *time.Time
		if i > 10 {
			scheduledAt := fixedNow.Add(time.Duration(i-10) * time.Hour)
			publishAt = &scheduledAt
		}

		watchURL, err := svc.PublishThumbnail(
			context.Background(),
			"batch-test-token",
			videoID,
			[]byte(fmt.Sprintf("thumbnail-%02d", i)),
			"image/jpeg",
			"public",
			publishAt,
			models.YouTubePublishOptions{
				Title:       fmt.Sprintf("Batch Video %02d", i),
				Description: fmt.Sprintf("Twenty-video regression case %02d", i),
			},
		)
		if err != nil {
			t.Fatalf("video %02d PublishThumbnail failed: %v", i, err)
		}
		wantURL := "https://www.youtube.com/watch?v=" + videoID
		if watchURL != wantURL {
			t.Fatalf("video %02d watch URL = %q, want %q", i, watchURL, wantURL)
		}
	}

	if len(events) != 40 {
		t.Fatalf("captured %d operations, want 40 (2 per video)", len(events))
	}
	if len(thumbnailCalls) != 20 {
		t.Fatalf("thumbnail calls covered %d videos, want 20", len(thumbnailCalls))
	}
	if len(updateCalls) != 20 {
		t.Fatalf("update calls covered %d videos, want 20", len(updateCalls))
	}

	for i := 1; i <= 20; i++ {
		videoID := fmt.Sprintf("batch-video-%02d", i)
		if thumbnailCalls[videoID] != 1 {
			t.Errorf("%s thumbnail calls = %d, want 1", videoID, thumbnailCalls[videoID])
		}
		if updateCalls[videoID] != 1 {
			t.Errorf("%s update calls = %d, want 1", videoID, updateCalls[videoID])
		}

		wantThumbnailEvent := "thumbnail:" + videoID
		wantUpdateEvent := "update:" + videoID
		base := (i - 1) * 2
		if events[base] != wantThumbnailEvent || events[base+1] != wantUpdateEvent {
			t.Errorf("%s operation order = [%q, %q], want [%q, %q]", videoID, events[base], events[base+1], wantThumbnailEvent, wantUpdateEvent)
		}

		update, ok := updates[videoID]
		if !ok {
			t.Errorf("%s missing captured update", videoID)
			continue
		}

		if i <= 10 {
			if got := update.Status["privacyStatus"]; got != "public" {
				t.Errorf("%s privacyStatus = %v, want public", videoID, got)
			}
			if _, exists := update.Status["publishAt"]; exists {
				t.Errorf("%s immediate publication unexpectedly contains publishAt", videoID)
			}
		} else {
			if got := update.Status["privacyStatus"]; got != "private" {
				t.Errorf("%s scheduled privacyStatus = %v, want private", videoID, got)
			}
			wantPublishAt := fixedNow.Add(time.Duration(i-10) * time.Hour).Format(time.RFC3339)
			if got := update.Status["publishAt"]; got != wantPublishAt {
				t.Errorf("%s publishAt = %v, want %s", videoID, got, wantPublishAt)
			}
		}

		if got := update.Snippet["title"]; got != fmt.Sprintf("Batch Video %02d", i) {
			t.Errorf("%s title = %v", videoID, got)
		}
		if got := update.Snippet["categoryId"]; got != "22" {
			t.Errorf("%s categoryId = %v, want 22", videoID, got)
		}
	}
}
