package api

// Localization sub-flow test — the retriable failure contract for
// stage 9 of the orchestrator. Extracted from
// youtube_publish_pipeline_test.go by sub-flow.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// TestPublishPipeline_LocalizationsError_IsRetriable locks the
// "stage 9 / sortedTranslationKeys loop / failure mode is RETRIABLE"
// contract.
//
// First POST: upsertLocalizationsFn returns an error → 502 + the
// row stamped 'failed' (via Update). MarkPublishedWithActualPrivacy
// is NOT called yet (failure short-circuits before the final CAS).
//
// Second POST: same session, status still 'editing'/'failed' →
// orchestrator enters the loop again. The mock now returns nil →
// 200 + MarkPublishedWithActualPrivacy called → row status
// 'published'.
//
// Whichever the publish-and-status sequence, both POSTs end with
// 502 then 200 — the failure was recoverable on retry, not a
// permanent lockdown.
func TestPublishPipeline_LocalizationsError_IsRetriable(t *testing.T) {
	var upsertCalls atomic.Int32
	youTubeSvc := &mockYouTubeOAuthServiceForEditor{
		publishThumbnailFn: func(_ context.Context, _, videoID string, _ []byte, _, _ string, _ *time.Time, _ models.YouTubePublishOptions) (string, error) {
			return "https://www.youtube.com/watch?v=" + videoID, nil
		},
		getVideoFn: func(_ context.Context, _, videoID string) (*models.YouTubeVideoDetails, error) {
			return &models.YouTubeVideoDetails{
				ID:           videoID,
				ChannelID:    "UC123",
				UploadStatus: "processed",
				Privacy:      "public",
			}, nil
		},
		upsertLocalizationsFn: func(_ context.Context, _, _, lang string, _ models.YouTubeTranslation) error {
			upsertCalls.Add(1)
			if upsertCalls.Load() == 1 {
				return &apiError{msg: "videos.update(part=localizations) 503 backend temporarily unavailable[" + lang + "]"}
			}
			return nil
		},
	}

	// Stateful mockYouTubeVideoEditStore.findFn: first POST sees
	// status='editing'; after the first publish, findFn flips to
	// status='failed' (mirroring what the orchestrator does on
	// localizations failure). The store's default MarkPublishing
	// fallback increments markPublishingAttempts which would CAS-
	// loss on call #2 -- so we inject markPublishingFn that
	// always succeeds and rewrites the status to whatever the
	// current findFn says.
	statefulRow := func(status string) *models.YouTubeVideoEdit {
		media := "asset-uuid-123"
		return &models.YouTubeVideoEdit{
			ID: "session-123", WorkspaceID: 7, PlatformAccountID: 42,
			YouTubeVideoID: "ytvideo123", VeloxProjectID: "ve-project-123",
			ThumbnailMediaID: &media, DesiredPrivacy: "public", Status: status,
		}
	}
	var currentStatus atomic.Value
	currentStatus.Store("editing")
	editStore := &mockYouTubeVideoEditStore{
		findFn: func(_ context.Context, id string) (*models.YouTubeVideoEdit, error) {
			if id == "session-123" {
				return statefulRow(currentStatus.Load().(string)), nil
			}
			return nil, nil
		},
		findByProjectFn: func(_ context.Context, projectID string) (*models.YouTubeVideoEdit, error) {
			if projectID == "ve-project-123" {
				return statefulRow(currentStatus.Load().(string)), nil
			}
			return nil, nil
		},
		markPublishingFn: func(_ context.Context, _ string, desiredPrivacy string, publishAt *time.Time, _ time.Duration) (*models.YouTubeVideoEdit, error) {
			row := statefulRow("publishing")
			row.DesiredPrivacy = desiredPrivacy
			row.PublishAt = publishAt
			row.UpdatedAt = time.Now().UTC()
			// MarkPublishing writes 'publishing' to the row.
			currentStatus.Store("publishing")
			return row, nil
		},
		markPublishedWithActualPrivacyFn: func(_ context.Context, id string, actualPrivacy string, syncStatus string) (*models.YouTubeVideoEdit, error) {
			row := statefulRow("published")
			row.ActualPrivacy = &actualPrivacy
			row.YouTubeSyncStatus = &syncStatus
			row.UpdatedAt = time.Now().UTC()
			currentStatus.Store("published")
			return row, nil
		},
		update: func(_ context.Context, edit *models.YouTubeVideoEdit) error {
			// The orchestrator's failure branch calls Update with
			// edit.Status='failed' BEFORE the 502 returns. Mirror
			// that into the findFn state.
			if edit.Status == "failed" {
				currentStatus.Store("failed")
			}
			return nil
		},
	}
	router := backbonePlusCustomMocks(t, youTubeSvc, editStore)

	body := byProjectPublishPayload(t, map[string]any{
		"translations": map[string]models.YouTubeTranslation{
			"it": {Title: "Titolo IT", Description: "Descrizione IT"},
		},
		"default_language": "en",
	})

	post := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/by-project/ve-project-123/publish", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		withBearerJWT(t, req, 1)
		w := httptest.NewRecorder()
		router.Setup().ServeHTTP(w, req)
		return w
	}

	first := post()
	if first.Code != http.StatusBadGateway {
		t.Fatalf("first publish expected 502 (localizations failure), got %d (body=%s)", first.Code, first.Body.String())
	}
	if !strings.Contains(strings.ToLower(first.Body.String()), "localizations") {
		t.Fatalf("first publish body expected to mention localizations, got %s", first.Body.String())
	}
	if currentStatus.Load() != "failed" {
		t.Fatalf("expected findFn state to flip to 'failed' after the first failure, got %q", currentStatus.Load())
	}

	// Second POST: status='failed' is not in the in-flight guard
	// (only 'publishing' is), so the orchestrator proceeds. The
	// upsertLocalizationsFn now returns nil → loop completes →
	// MarkPublishedWithActualPrivacy (mock fallback) flips
	// simulatedStatus='published'.
	second := post()
	if second.Code != http.StatusOK {
		t.Fatalf("second publish expected 200 (retry succeeded), got %d (body=%s)", second.Code, second.Body.String())
	}
	if got := upsertCalls.Load(); got != 2 {
		t.Fatalf("expected upsertLocalizationsFn called 2x across the two POSTs (1 fail + 1 success), got %d", got)
	}
	var resp publishYouTubeEditorSessionResponse
	if err := json.Unmarshal(second.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode second response: %v", err)
	}
	if resp.Status != "published" {
		t.Fatalf("second publish expected response.status=published, got %q", resp.Status)
	}
}
