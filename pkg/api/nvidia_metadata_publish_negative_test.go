package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

func TestNVIDIAMetadataPublish_Negative_PastDateRejected(t *testing.T) {
	youTubeSvc := &mockYouTubeOAuthServiceForEditor{
		publishThumbnailFn: func(_ context.Context, _, videoID string, _ []byte, _, _ string, _ *time.Time, _ models.YouTubePublishOptions) (string, error) {
			t.Error("publishThumbnailFn should NOT be called for past-date payload")
			return "", nil
		},
	}
	editStore := &mockYouTubeVideoEditStore{}

	router := customBackboneForNegative(t, youTubeSvc, editStore)

	pastISO := "2020-01-01T00:00:00Z"
	payload := []byte(`{
		"privacy_status": "private",
		"publish_at": "` + pastISO + `"
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/by-project/ve-project-123/publish", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	router.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for past publish_at, got %d (body=%s)", w.Code, w.Body.String())
	}
	if !strings.Contains(strings.ToLower(w.Body.String()), "future") {
		t.Errorf("expected error body to mention 'future', got %s", w.Body.String())
	}
}

// TestNVIDIAMetadataPublish_Negative_ScheduledWithPublicRejected asserts
// that scheduling with privacy_status="public" is rejected.
func TestNVIDIAMetadataPublish_Negative_ScheduledWithPublicRejected(t *testing.T) {
	youTubeSvc := &mockYouTubeOAuthServiceForEditor{
		publishThumbnailFn: func(_ context.Context, _, videoID string, _ []byte, _, _ string, _ *time.Time, _ models.YouTubePublishOptions) (string, error) {
			t.Error("publishThumbnailFn should NOT be called for invalid privacy+scheduling combo")
			return "", nil
		},
	}
	editStore := &mockYouTubeVideoEditStore{}

	router := customBackboneForNegative(t, youTubeSvc, editStore)

	futureISO := time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339)
	payload := []byte(`{
		"privacy_status": "public",
		"publish_at": "` + futureISO + `"
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/by-project/ve-project-123/publish", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	router.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for scheduled publish with public privacy, got %d (body=%s)", w.Code, w.Body.String())
	}
	if !strings.Contains(strings.ToLower(w.Body.String()), "private") {
		t.Errorf("expected error body to mention 'private', got %s", w.Body.String())
	}
}

// TestNVIDIAMetadataPublish_Negative_TranslationsWithoutDefaultLanguage
// asserts that translations without default_language are rejected.
func TestNVIDIAMetadataPublish_Negative_TranslationsWithoutDefaultLanguage(t *testing.T) {
	youTubeSvc := &mockYouTubeOAuthServiceForEditor{
		publishThumbnailFn: func(_ context.Context, _, videoID string, _ []byte, _, _ string, _ *time.Time, _ models.YouTubePublishOptions) (string, error) {
			t.Error("publishThumbnailFn should NOT be called for invalid translations")
			return "", nil
		},
	}
	editStore := &mockYouTubeVideoEditStore{}

	router := customBackboneForNegative(t, youTubeSvc, editStore)

	payload := []byte(`{
		"privacy_status": "unlisted",
		"translations": {
			"en": {
				"title": "English Title",
				"description": "English Description"
			}
		}
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/by-project/ve-project-123/publish", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	router.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for translations without default_language, got %d (body=%s)", w.Code, w.Body.String())
	}
}

// TestNVIDIAMetadataPublish_Idempotency_DoublePublishReturns200 asserts
// that a second POST on an already-published session returns 200
// without re-invoking YouTube (idempotency).
func TestNVIDIAMetadataPublish_Negative_ScheduledWithUnlistedRejected(t *testing.T) {
	youTubeSvc := &mockYouTubeOAuthServiceForEditor{
		publishThumbnailFn: func(_ context.Context, _, videoID string, _ []byte, _, _ string, _ *time.Time, _ models.YouTubePublishOptions) (string, error) {
			t.Error("publishThumbnailFn should NOT be called for scheduled+unlisted combo")
			return "", nil
		},
	}
	editStore := &mockYouTubeVideoEditStore{}

	router := customBackboneForNegative(t, youTubeSvc, editStore)

	futureISO := time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339)
	payload := []byte(`{
		"privacy_status": "unlisted",
		"publish_at": "` + futureISO + `"
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/by-project/ve-project-123/publish", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	router.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for scheduled publish with unlisted privacy, got %d (body=%s)", w.Code, w.Body.String())
	}
	if !strings.Contains(strings.ToLower(w.Body.String()), "private") {
		t.Errorf("expected error body to mention 'private', got %s", w.Body.String())
	}
}

// customBackboneForNegative builds a router with full mock control,
// bypassing commonPublishBackbone's default overrides. Needed when
// tests require stateful mocks (e.g. MarkPublishing CAS-loss, session
// status tracking across retries).
func TestNVIDIAMetadataPublish_Negative_SimultaneousPublishReturns409(t *testing.T) {
	var publishCount int
	var publishCountMu sync.Mutex

	youTubeSvc := &mockYouTubeOAuthServiceForEditor{
		publishThumbnailFn: func(_ context.Context, _, videoID string, _ []byte, _, _ string, _ *time.Time, _ models.YouTubePublishOptions) (string, error) {
			publishCountMu.Lock()
			publishCount++
			publishCountMu.Unlock()
			return "https://www.youtube.com/watch?v=" + videoID, nil
		},
		getVideoFn: func(_ context.Context, _, videoID string) (*models.YouTubeVideoDetails, error) {
			return &models.YouTubeVideoDetails{
				ID:           videoID,
				ChannelID:    "UC123",
				UploadStatus: "processed",
				Privacy:      "unlisted",
			}, nil
		},
	}

	var markCallCount int
	var markMu sync.Mutex
	editStore := &mockYouTubeVideoEditStore{
		markPublishingFn: func(_ context.Context, id string, desiredPrivacy string, publishAt *time.Time, _ time.Duration) (*models.YouTubeVideoEdit, error) {
			markMu.Lock()
			markCallCount++
			call := markCallCount
			markMu.Unlock()
			if call == 1 {
				return &models.YouTubeVideoEdit{
					ID: id, WorkspaceID: 7, PlatformAccountID: 42,
					YouTubeVideoID: "ytvideo123", VeloxProjectID: "ve-project-123",
					Status: "publishing", DesiredPrivacy: desiredPrivacy,
				}, nil
			}
			return nil, fmt.Errorf("%w: simulated CAS-loss", repository.ErrYouTubeVideoEditNotFound)
		},
		markPublishedWithActualPrivacyFn: func(_ context.Context, id string, actualPrivacy string, syncStatus string) (*models.YouTubeVideoEdit, error) {
			return &models.YouTubeVideoEdit{
				ID: id, Status: "published", DesiredPrivacy: "unlisted",
				ActualPrivacy: &actualPrivacy, YouTubeSyncStatus: &syncStatus,
			}, nil
		},
	}

	router, _ := commonPublishBackbone(t, youTubeSvc, editStore)

	payload := nvidiaMetadataPublishPayload(t)

	post := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/by-project/ve-project-123/publish", bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		withBearerJWT(t, req, 1)
		w := httptest.NewRecorder()
		router.Setup().ServeHTTP(w, req)
		return w
	}

	first := post()
	second := post()

	codes := []int{first.Code, second.Code}
	has200 := false
	has409 := false
	for _, c := range codes {
		if c == http.StatusOK {
			has200 = true
		}
		if c == http.StatusConflict {
			has409 = true
		}
	}
	if !has200 {
		t.Errorf("simultaneous publish: expected at least one 200, got codes %v (body1=%s, body2=%s)",
			codes, first.Body.String(), second.Body.String())
	}
	if !has409 {
		t.Errorf("simultaneous publish: expected at least one 409 (CAS-loss), got codes %v (body1=%s, body2=%s)",
			codes, first.Body.String(), second.Body.String())
	}

	publishCountMu.Lock()
	count := publishCount
	publishCountMu.Unlock()
	if count != 1 {
		t.Errorf("simultaneous publish: expected exactly 1 publishThumbnail call, got %d", count)
	}
}

// TestNVIDIAMetadataPublish_Negative_InvalidMetadataNoYouTubeCall asserts
// that when the publish payload fails pre-flight validation (e.g. title
// exceeds 100 characters), the orchestrator rejects the request BEFORE
// any YouTube API call is made. This is the safety net for invalid NVIDIA
// responses: no thumbnail upload, no thumbnail attach, no YouTube call.
func TestNVIDIAMetadataPublish_Negative_InvalidMetadataNoYouTubeCall(t *testing.T) {
	youTubeSvc := &mockYouTubeOAuthServiceForEditor{
		publishThumbnailFn: func(_ context.Context, _, videoID string, _ []byte, _, _ string, _ *time.Time, _ models.YouTubePublishOptions) (string, error) {
			t.Error("publishThumbnailFn must NOT be called when validation rejects the request — this guards against invalid NVIDIA responses leaking to YouTube")
			return "", nil
		},
		upsertLocalizationsFn: func(_ context.Context, _, _, lang string, _ models.YouTubeTranslation) error {
			t.Error("upsertLocalizationsFn must NOT be called when validation rejects")
			return nil
		},
	}
	editStore := &mockYouTubeVideoEditStore{}

	router := customBackboneForNegative(t, youTubeSvc, editStore)

	// Title exceeds YouTube's 100-character limit — simulates
	// an invalid NVIDIA response that wasn't caught by the generator.
	toolong := strings.Repeat("x", 150)
	payload := []byte(`{
		"title": "` + toolong + `",
		"privacy_status": "unlisted"
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/by-project/ve-project-123/publish", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	router.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid metadata (title too long), got %d (body=%s)", w.Code, w.Body.String())
	}
	// The error must mention what was wrong — correctable by the operator.
	if !strings.Contains(strings.ToLower(w.Body.String()), "title") &&
		!strings.Contains(strings.ToLower(w.Body.String()), "100") {
		t.Errorf("expected error body to mention 'title' or '100' so operator can correct, got %s", w.Body.String())
	}
}

// TestNVIDIAMetadataPublish_Negative_LocalizationErrorSingleLanguage_SessionFailed_RetryAvailable
// asserts that when ONE language's localization upsert fails (e.g. "es"),
// the session is marked 'failed' with the error message identifying which
// language failed, AND the publish is RETRIABLE — a second POST succeeds.
//
// This matches the real-world scenario:
//   - YouTube's videos.update(part=localizations) returns 503 for a single
//     language (e.g. Spanish), but succeeded for English and Portuguese.
//   - The operator retries → the retry re-plays the localizations loop,
//     and the previously-failed language now succeeds.
func TestNVIDIAMetadataPublish_Negative_LocalizationErrorSingleLanguage_SessionFailed_RetryAvailable(t *testing.T) {
	var upsertCalls int
	var upsertMu sync.Mutex
	var capturedLastError string

	youTubeSvc := &mockYouTubeOAuthServiceForEditor{
		publishThumbnailFn: func(_ context.Context, _, videoID string, _ []byte, _, _ string, _ *time.Time, _ models.YouTubePublishOptions) (string, error) {
			return "https://www.youtube.com/watch?v=" + videoID, nil
		},
		getVideoFn: func(_ context.Context, _, videoID string) (*models.YouTubeVideoDetails, error) {
			return &models.YouTubeVideoDetails{
				ID:           videoID,
				ChannelID:    "UC123",
				UploadStatus: "processed",
				Privacy:      "unlisted",
			}, nil
		},
		upsertLocalizationsFn: func(_ context.Context, _, _, lang string, _ models.YouTubeTranslation) error {
			upsertMu.Lock()
			upsertCalls++
			call := upsertCalls
			upsertMu.Unlock()
			// First POST: fail on "es" (Spanish) — the second language in
			// alphabetical order (en, es, pt-BR).
			// Second POST: succeed for all languages.
			if call <= 3 && lang == "es" {
				return &apiError{msg: "videos.update(part=localizations) 503 backend temporarily unavailable[" + lang + "]"}
			}
			return nil
		},
	}

	// Stateful mock: first POST sees status='editing' and the
	// orchestrator stamps status='failed' on localization error.
	// Second POST sees 'failed' (which is retriable — NOT terminal)
	// and the orchestrator re-enters the publish loop.
	sessionStatus := "editing"
	var statusMu sync.Mutex
	editStore := &mockYouTubeVideoEditStore{
		update: func(_ context.Context, edit *models.YouTubeVideoEdit) error {
			statusMu.Lock()
			if edit.Status == "failed" {
				sessionStatus = "failed"
				capturedLastError = edit.LastError
			}
			statusMu.Unlock()
			return nil
		},
		markPublishingFn: func(_ context.Context, id string, desiredPrivacy string, publishAt *time.Time, _ time.Duration) (*models.YouTubeVideoEdit, error) {
			return &models.YouTubeVideoEdit{
				ID: id, WorkspaceID: 7, PlatformAccountID: 42,
				YouTubeVideoID: "ytvideo123", VeloxProjectID: "ve-project-123",
				Status: "publishing", DesiredPrivacy: desiredPrivacy,
			}, nil
		},
		findByProjectFn: func(_ context.Context, projectID string) (*models.YouTubeVideoEdit, error) {
			if projectID == "ve-project-123" {
				media := "asset-uuid-123"
				statusMu.Lock()
				s := sessionStatus
				statusMu.Unlock()
				return &models.YouTubeVideoEdit{
					ID: "session-123", WorkspaceID: 7, PlatformAccountID: 42,
					YouTubeVideoID: "ytvideo123", VeloxProjectID: "ve-project-123",
					ThumbnailMediaID: &media, DesiredPrivacy: "unlisted", Status: s,
				}, nil
			}
			return nil, nil
		},
		markPublishedWithActualPrivacyFn: func(_ context.Context, id string, actualPrivacy string, syncStatus string) (*models.YouTubeVideoEdit, error) {
			statusMu.Lock()
			sessionStatus = "published"
			statusMu.Unlock()
			return &models.YouTubeVideoEdit{
				ID: id, Status: "published", DesiredPrivacy: "unlisted",
				ActualPrivacy: &actualPrivacy, YouTubeSyncStatus: &syncStatus,
			}, nil
		},
	}

	router := customBackboneForNegative(t, youTubeSvc, editStore)

	payload := nvidiaMetadataPublishPayload(t)

	post := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/by-project/ve-project-123/publish", bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		withBearerJWT(t, req, 1)
		w := httptest.NewRecorder()
		router.Setup().ServeHTTP(w, req)
		return w
	}

	// ── First publish: localization error → 502, session=failed ──
	first := post()
	if first.Code != http.StatusBadGateway {
		t.Fatalf("first publish (localization error): expected 502, got %d (body=%s)", first.Code, first.Body.String())
	}
	if !strings.Contains(strings.ToLower(first.Body.String()), "localization") {
		t.Errorf("expected error body to mention 'localization', got %s", first.Body.String())
	}
	statusMu.Lock()
	s := sessionStatus
	statusMu.Unlock()
	if s != "failed" {
		t.Errorf("expected session status='failed' after localization error, got %q", s)
	}
	if capturedLastError == "" {
		t.Error("expected last_error to be set with the YouTube error message")
	}
	if capturedLastError != "" && !strings.Contains(capturedLastError, "es") {
		t.Errorf("expected last_error to identify failed language 'es', got %q", capturedLastError)
	}

	// ── Second publish: retry succeeds, session=published ──
	second := post()
	if second.Code != http.StatusOK {
		t.Fatalf("second publish (retry): expected 200, got %d (body=%s)", second.Code, second.Body.String())
	}
	statusMu.Lock()
	s = sessionStatus
	statusMu.Unlock()
	if s != "published" {
		t.Errorf("expected session status='published' after retry, got %q", s)
	}
	var resp publishYouTubeEditorSessionResponse
	if err := json.Unmarshal(second.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode retry response: %v", err)
	}
	if resp.Status != "published" {
		t.Errorf("retry response.status: want 'published', got %q", resp.Status)
	}
}
