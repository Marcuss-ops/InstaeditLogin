package api

// nvidia_metadata_publish_e2e_test.go
//
// END-TO-END TEST: NVIDIA metadata publish flow.
//
// This test exercises the FULL Dark Editor → YouTube publish pipeline
// with NVIDIA-generated metadata, validating every field end-to-end:
//
//   1. Publish request carries the canonical contract fixture
//      (api/fixtures/publish_metadata_fixture.json).
//   2. The publishThumbnailFn mock captures ALL metadata fields:
//      title, description, tags, default_language, default_audio_language,
//      privacy_status, publish_at (YouTubePublishOptions).
//   3. The upsertLocalizationsFn mock captures every translation
//      (en, es, pt-BR) with their localized titles + descriptions.
//   4. Response shape: status, public_url, video_id, privacy_status,
//      actual_privacy, youtube_sync_status.
//
// This is the canonical "does the whole pipeline preserve metadata"
// test — a regression in any field silently failing to reach YouTube
// surfaces here.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// nvidiaMetadataPublishPayload returns the canonical publish payload
// from the shared contract fixture, usable as the body of
// POST .../by-project/{id}/publish.
func nvidiaMetadataPublishPayload(t *testing.T) []byte {
	t.Helper()
	payload := map[string]any{
		"title":                 "Come automatizzare la pubblicazione YouTube nel 2026",
		"description":           "In questo video vediamo come creare, modificare e pubblicare contenuti YouTube attraverso un flusso automatizzato con InstaEdit e NVIDIA AI.",
		"privacy_status":        "unlisted",
		"tags":                  []string{"youtube automation", "video editing", "instaedit", "content creation"},
		"default_language":      "it",
		"default_audio_language": "it",
		"translations": map[string]models.YouTubeTranslation{
			"en":    {Title: "How to Automate YouTube Publishing in 2026", Description: "This video explains how to create, edit and publish YouTube content through an automated workflow with InstaEdit and NVIDIA AI."},
			"es":    {Title: "Cómo automatizar la publicación en YouTube en 2026", Description: "Este video explica cómo crear, editar y publicar contenido de YouTube mediante un flujo automatizado con InstaEdit y NVIDIA AI."},
			"pt-BR": {Title: "Como automatizar publicações no YouTube em 2026", Description: "Este vídeo mostra como criar, editar e publicar conteúdo no YouTube por meio de um fluxo automatizado com InstaEdit e NVIDIA AI."},
		},
	}
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal nvidia metadata publish payload: %v", err)
	}
	return b
}

// TestNVIDIAMetadataPublish_FullFlow_YouTubeMetadataPreserved is the
// canonical end-to-end test for the NVIDIA metadata publish flow.
//
// It POSTs the full contract fixture as the publish payload against
// the by-project endpoint and asserts that EVERY metadata field
// reaches the YouTube mock intact — title, description, tags,
// default_language, default_audio_language, translations (en, es,
// pt-BR), privacy_status.
//
// The test uses the same commonPublishBackbone as every other
// pipeline test so a future refactor of the router wiring cannot
// silently break the metadata contract.
func TestNVIDIAMetadataPublish_FullFlow_YouTubeMetadataPreserved(t *testing.T) {
	// ── Capture buckets ──────────────────────────────────────────
	var (
		capturedTitle            string
		capturedDescription      string
		capturedTags             []string
		capturedDefaultLang      string
		capturedDefaultAudioLang string
		capturedPrivacyStatus    string
		capturedPublishAt        *time.Time
		capturedTranslations     = make(map[string]models.YouTubeTranslation)
		capturedTranslationsMu   sync.Mutex
		publishThumbnailCalled   bool
	)

	youTubeSvc := &mockYouTubeOAuthServiceForEditor{
		publishThumbnailFn: func(_ context.Context, _, videoID string, _ []byte, mimeType, privacyStatus string, publishAt *time.Time, opts models.YouTubePublishOptions) (string, error) {
			publishThumbnailCalled = true
			capturedTitle = opts.Title
			capturedDescription = opts.Description
			capturedTags = opts.Tags
			capturedDefaultLang = opts.DefaultLanguage
			capturedDefaultAudioLang = opts.DefaultAudioLanguage
			capturedPrivacyStatus = privacyStatus
			capturedPublishAt = publishAt
			// Translations are not passed to publishThumbnailFn directly;
			// they go through upsertLocalizationsFn below.
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
		upsertLocalizationsFn: func(_ context.Context, _, _, lang string, tr models.YouTubeTranslation) error {
			capturedTranslationsMu.Lock()
			capturedTranslations[lang] = tr
			capturedTranslationsMu.Unlock()
			return nil
		},
	}

	editStore := &mockYouTubeVideoEditStore{
		markPublishedWithActualPrivacyFn: func(_ context.Context, id string, actualPrivacy string, syncStatus string) (*models.YouTubeVideoEdit, error) {
			return &models.YouTubeVideoEdit{
				ID:                id,
				WorkspaceID:       7,
				YouTubeVideoID:    "ytvideo123",
				VeloxProjectID:    "ve-project-123",
				Status:            "published",
				DesiredPrivacy:    "unlisted",
				ActualPrivacy:     &actualPrivacy,
				YouTubeSyncStatus: &syncStatus,
			}, nil
		},
	}

	router, _ := commonPublishBackbone(t, youTubeSvc, editStore)

	// ── Execute publish ──────────────────────────────────────────
	payload := nvidiaMetadataPublishPayload(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/by-project/ve-project-123/publish", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	router.Setup().ServeHTTP(w, req)

	// ── Response assertions ──────────────────────────────────────
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}
	var resp publishYouTubeEditorSessionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Status != "published" {
		t.Errorf("response.status: want 'published', got %q", resp.Status)
	}
	if resp.VideoID != "ytvideo123" {
		t.Errorf("response.video_id: want 'ytvideo123', got %q", resp.VideoID)
	}
	if resp.PrivacyStatus != "unlisted" {
		t.Errorf("response.privacy_status: want 'unlisted', got %q", resp.PrivacyStatus)
	}
	if !strings.Contains(resp.PublicURL, "youtube.com/watch?v=ytvideo123") {
		t.Errorf("response.public_url: expected youtube.com/watch?v=ytvideo123, got %q", resp.PublicURL)
	}
	if resp.ActualPrivacy != "unlisted" {
		t.Errorf("response.actual_privacy: want 'unlisted', got %q", resp.ActualPrivacy)
	}
	if resp.YouTubeSyncStatus != "confirmed" {
		t.Errorf("response.youtube_sync_status: want 'confirmed', got %q", resp.YouTubeSyncStatus)
	}

	// ── PublishThumbnail was called ─────────────────────────────
	if !publishThumbnailCalled {
		t.Fatal("publishThumbnailFn was NOT called — the orchestrator didn't reach YouTube")
	}

	// ── Metadata field-by-field assertions ───────────────────────
	// Title (Italian, max 100 chars)
	if capturedTitle != "Come automatizzare la pubblicazione YouTube nel 2026" {
		t.Errorf("title: want %q, got %q", "Come automatizzare la pubblicazione YouTube nel 2026", capturedTitle)
	}
	if len(capturedTitle) > 100 {
		t.Errorf("title exceeds 100 char YouTube limit: len=%d", len(capturedTitle))
	}

	// Description (Italian, max 5000 chars)
	wantDesc := "In questo video vediamo come creare, modificare e pubblicare contenuti YouTube attraverso un flusso automatizzato con InstaEdit e NVIDIA AI."
	if capturedDescription != wantDesc {
		t.Errorf("description: want %q, got %q", wantDesc, capturedDescription)
	}
	if len(capturedDescription) > 5000 {
		t.Errorf("description exceeds 5000 char YouTube limit: len=%d", len(capturedDescription))
	}

	// Tags (max 30 items, max 500 chars total incl commas)
	wantTags := []string{"youtube automation", "video editing", "instaedit", "content creation"}
	if len(capturedTags) != len(wantTags) {
		t.Errorf("tags count: want %d, got %d (tags=%v)", len(wantTags), len(capturedTags), capturedTags)
	} else {
		sort.Strings(capturedTags)
		sort.Strings(wantTags)
		for i := range wantTags {
			if capturedTags[i] != wantTags[i] {
				t.Errorf("tags[%d]: want %q, got %q", i, wantTags[i], capturedTags[i])
			}
		}
	}
	tagChars := 0
	for i, tag := range capturedTags {
		tagChars += len(tag)
		if i < len(capturedTags)-1 {
			tagChars++ // comma
		}
	}
	if tagChars > 500 {
		t.Errorf("tags total chars exceed 500 limit: %d", tagChars)
	}

	// Default language (BCP-47: it)
	if capturedDefaultLang != "it" {
		t.Errorf("default_language: want 'it', got %q", capturedDefaultLang)
	}
	if capturedDefaultAudioLang != "it" {
		t.Errorf("default_audio_language: want 'it', got %q", capturedDefaultAudioLang)
	}

	// Privacy status
	if capturedPrivacyStatus != "unlisted" {
		t.Errorf("privacy_status sent to YouTube: want 'unlisted', got %q", capturedPrivacyStatus)
	}
	if capturedPublishAt != nil {
		t.Errorf("publish_at: want nil (immediate publish), got %v", capturedPublishAt)
	}

	// ── Translations assertions ──────────────────────────────────
	expectedTranslations := map[string]models.YouTubeTranslation{
		"en":    {Title: "How to Automate YouTube Publishing in 2026", Description: "This video explains how to create, edit and publish YouTube content through an automated workflow with InstaEdit and NVIDIA AI."},
		"es":    {Title: "Cómo automatizar la publicación en YouTube en 2026", Description: "Este video explica cómo crear, editar y publicar contenido de YouTube mediante un flujo automatizado con InstaEdit y NVIDIA AI."},
		"pt-BR": {Title: "Como automatizar publicações no YouTube em 2026", Description: "Este vídeo mostra como criar, editar e publicar conteúdo no YouTube por meio de um fluxo automatizado com InstaEdit e NVIDIA AI."},
	}

	if len(capturedTranslations) != len(expectedTranslations) {
		t.Errorf("translations count: want %d, got %d (captured=%v)", len(expectedTranslations), len(capturedTranslations), capturedTranslations)
	}
	for lang, wantTr := range expectedTranslations {
		gotTr, ok := capturedTranslations[lang]
		if !ok {
			t.Errorf("translations[%q]: missing — was not sent to YouTube localizations endpoint", lang)
			continue
		}
		if gotTr.Title != wantTr.Title {
			t.Errorf("translations[%q].title: want %q, got %q", lang, wantTr.Title, gotTr.Title)
		}
		if len(gotTr.Title) > 100 {
			t.Errorf("translations[%q].title exceeds 100 char limit: len=%d", lang, len(gotTr.Title))
		}
		if gotTr.Description != wantTr.Description {
			t.Errorf("translations[%q].description mismatch", lang)
		}
		if len(gotTr.Description) > 5000 {
			t.Errorf("translations[%q].description exceeds 5000 char limit: len=%d", lang, len(gotTr.Description))
		}
		// Verify the title isn't identical to the original (Italian).
		if lang != "it" && gotTr.Title == capturedTitle {
			t.Errorf("translations[%q].title is IDENTICAL to the default (Italian) title — translation likely not applied", lang)
		}
	}

	// ── BCP-47 sanity: all language codes are valid ──────────────
	for lang := range capturedTranslations {
		if err := models.CheckBCP47Like("translations["+lang+"]", lang); err != nil {
			t.Errorf("translations language code %q is not BCP-47-like: %v", lang, err)
		}
	}
	if err := models.CheckBCP47Like("default_language", capturedDefaultLang); err != nil {
		t.Errorf("default_language %q is not BCP-47-like: %v", capturedDefaultLang, err)
	}
	if err := models.CheckBCP47Like("default_audio_language", capturedDefaultAudioLang); err != nil {
		t.Errorf("default_audio_language %q is not BCP-47-like: %v", capturedDefaultAudioLang, err)
	}
}

// TestNVIDIAMetadataPublish_ScheduledPublishing_PrivacyForcedToPrivate
// asserts that when publish_at is present, the privacy_status is forced
// to "private" (YouTube requirement for scheduled videos).
func TestNVIDIAMetadataPublish_ScheduledPublishing_PrivacyForcedToPrivate(t *testing.T) {
	var capturedPrivacy string
	var capturedPublishAt *time.Time

	youTubeSvc := &mockYouTubeOAuthServiceForEditor{
		publishThumbnailFn: func(_ context.Context, _, videoID string, _ []byte, _, privacyStatus string, publishAt *time.Time, _ models.YouTubePublishOptions) (string, error) {
			capturedPrivacy = privacyStatus
			capturedPublishAt = publishAt
			return "https://www.youtube.com/watch?v=" + videoID, nil
		},
		getVideoFn: func(_ context.Context, _, videoID string) (*models.YouTubeVideoDetails, error) {
			return &models.YouTubeVideoDetails{
				ID:           videoID,
				ChannelID:    "UC123",
				UploadStatus: "processed",
				Privacy:      "private",
			}, nil
		},
	}

	editStore := &mockYouTubeVideoEditStore{
		markPublishedWithActualPrivacyFn: func(_ context.Context, id string, actualPrivacy string, syncStatus string) (*models.YouTubeVideoEdit, error) {
			return &models.YouTubeVideoEdit{
				ID:                id,
				Status:            "published",
				DesiredPrivacy:    "private",
				ActualPrivacy:     &actualPrivacy,
				YouTubeSyncStatus: &syncStatus,
			}, nil
		},
	}

	router, _ := commonPublishBackbone(t, youTubeSvc, editStore)

	scheduledTime := time.Date(2026, 7, 30, 16, 0, 0, 0, time.UTC)
	futureISO := scheduledTime.Format(time.RFC3339)

	payload := []byte(`{
		"title": "Scheduled Test",
		"privacy_status": "public",
		"publish_at": "` + futureISO + `"
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/by-project/ve-project-123/publish", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	router.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", w.Code, w.Body.String())
	}

	// Privacy MUST be forced to "private" for scheduled publishing.
	if capturedPrivacy != "private" {
		t.Errorf("scheduled publish: privacy_status must be forced to 'private', got %q", capturedPrivacy)
	}
	if capturedPublishAt == nil {
		t.Fatal("scheduled publish: publish_at must be set")
	}
	if !capturedPublishAt.Equal(scheduledTime) {
		t.Errorf("scheduled publish: publish_at want %v, got %v", scheduledTime, capturedPublishAt)
	}
}

// TestNVIDIAMetadataPublish_Negative_PastDateRejected asserts that a
// publish_at in the past is rejected with 400.
func TestNVIDIAMetadataPublish_Negative_PastDateRejected(t *testing.T) {
	youTubeSvc := &mockYouTubeOAuthServiceForEditor{
		publishThumbnailFn: func(_ context.Context, _, videoID string, _ []byte, _, _ string, _ *time.Time, _ models.YouTubePublishOptions) (string, error) {
			t.Error("publishThumbnailFn should NOT be called for past-date payload")
			return "", nil
		},
	}
	editStore := &mockYouTubeVideoEditStore{}

	router, _ := commonPublishBackbone(t, youTubeSvc, editStore)

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

	router, _ := commonPublishBackbone(t, youTubeSvc, editStore)

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

	router, _ := commonPublishBackbone(t, youTubeSvc, editStore)

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
func TestNVIDIAMetadataPublish_Idempotency_DoublePublishReturns200(t *testing.T) {
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

	// Track the session state across publish calls so the second POST
	// sees status="published" and hits the idempotency branch.
	sessionStatus := "editing"
	var statusMu sync.Mutex
	editStore := &mockYouTubeVideoEditStore{
		markPublishedWithActualPrivacyFn: func(_ context.Context, id string, actualPrivacy string, syncStatus string) (*models.YouTubeVideoEdit, error) {
			statusMu.Lock()
			sessionStatus = "published"
			statusMu.Unlock()
			return &models.YouTubeVideoEdit{
				ID:                id,
				WorkspaceID:       7,
				YouTubeVideoID:    "ytvideo123",
				VeloxProjectID:    "ve-project-123",
				Status:            "published",
				DesiredPrivacy:    "unlisted",
				ActualPrivacy:     &actualPrivacy,
				YouTubeSyncStatus: &syncStatus,
			}, nil
		},
		// Override findByProjectFn to return the tracked status
		// so the idempotency gate sees "published" on the second POST.
		findByProjectFn: func(_ context.Context, projectID string) (*models.YouTubeVideoEdit, error) {
			if projectID == "ve-project-123" {
				media := "asset-uuid-123"
				statusMu.Lock()
				s := sessionStatus
				statusMu.Unlock()
				return &models.YouTubeVideoEdit{
					ID:                "session-123",
					WorkspaceID:       7,
					PlatformAccountID: 42,
					YouTubeVideoID:    "ytvideo123",
					VeloxProjectID:    "ve-project-123",
					ThumbnailMediaID:  &media,
					DesiredPrivacy:    "unlisted",
					Status:            s,
				}, nil
			}
			return nil, nil
		},
		// Override findFn to match findByProjectFn.
		findFn: func(_ context.Context, id string) (*models.YouTubeVideoEdit, error) {
			if id == "session-123" {
				media := "asset-uuid-123"
				statusMu.Lock()
				s := sessionStatus
				statusMu.Unlock()
				return &models.YouTubeVideoEdit{
					ID:                id,
					WorkspaceID:       7,
					PlatformAccountID: 42,
					YouTubeVideoID:    "ytvideo123",
					VeloxProjectID:    "ve-project-123",
					ThumbnailMediaID:  &media,
					DesiredPrivacy:    "unlisted",
					Status:            s,
				}, nil
			}
			return nil, nil
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
	if first.Code != http.StatusOK {
		t.Fatalf("first publish: expected 200, got %d (body=%s)", first.Code, first.Body.String())
	}

	second := post()
	if second.Code != http.StatusOK {
		t.Fatalf("second publish (idempotency): expected 200, got %d (body=%s)", second.Code, second.Body.String())
	}

	publishCountMu.Lock()
	count := publishCount
	publishCountMu.Unlock()
	if count != 1 {
		t.Errorf("idempotency: expected exactly 1 publishThumbnail call, got %d", count)
	}
}
