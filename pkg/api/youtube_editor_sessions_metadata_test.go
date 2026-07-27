package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// TestPublishYouTubeEditorSession_RejectsTooManyTags is the validation
// gate for the tag-count bound (30). YouTube's videos.update rejects
// more than 30 tags; we 400 BEFORE the API call to avoid burning
// 1600 quota. The mock publishThumbnailFn is wired so that IF the
// handler erroneously bypassed the gate, the test would notice the
// unexpected YouTube API call.
func TestPublishYouTubeEditorSession_RejectsTooManyTags(t *testing.T) {
	workspace := &models.Workspace{ID: 7, OwnerID: 1, Name: "Test Workspace"}
	editStore := &mockYouTubeVideoEditStore{
		findFn: func(ctx context.Context, id string) (*models.YouTubeVideoEdit, error) {
			return &models.YouTubeVideoEdit{
				ID:               "session-123",
				WorkspaceID:      workspace.ID,
				YouTubeVideoID:   "ytvideo123",
				VeloxProjectID:   "ve-project-123",
				Status:           "editing",
				DesiredPrivacy:   "public",
				ThumbnailMediaID: strPtr("asset-uuid-123"),
			}, nil
		},
	}
	var publishCalled bool
	youTubeSvc := &mockYouTubeOAuthServiceForEditor{
		publishThumbnailFn: func(ctx context.Context, accessToken, videoID string, data []byte, mimeType, privacyStatus string, publishAt *time.Time, opts models.YouTubePublishOptions) (string, error) {
			publishCalled = true
			return "", nil
		},
		upsertLocalizationsFn: func(ctx context.Context, accessToken, videoID, lang string, tr models.YouTubeTranslation) error {
			t.Errorf("UpsertLocalizations must NOT be called when validation rejected the request")
			return nil
		},
	}
	r := newPublishRouter(t, workspace, editStore,
		WithMediaStore(newMockMediaStore()),
		WithStorageProvider(newMockStorageProvider()),
		WithYouTubeService(youTubeSvc),
	)

	// 31 tags — one over the YouTube-published bound.
	tags := make([]string, 31)
	for i := range tags {
		tags[i] = "t"
	}
	payload := map[string]any{"tags": tags}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/session-123/publish", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for too-many-tags, got %d: %s", w.Code, w.Body.String())
	}
	if publishCalled {
		t.Fatalf("PublishThumbnail must NOT be called when validation fails")
	}
	if !strings.Contains(w.Body.String(), "too many tags") {
		t.Errorf("expected error body to mention 'too many tags', got %q", w.Body.String())
	}
}

// TestPublishYouTubeEditorSession_RejectsTotalTagsCharLimit gates
// the YouTube tag character-count bound (500 incl. comma separators).
// Mirrors TestPublishYouTubeEditorSession_RejectsTooManyTags in
// shape, but exercises the OTHER axis of the Validate() guard.
func TestPublishYouTubeEditorSession_RejectsTotalTagsCharLimit(t *testing.T) {
	workspace := &models.Workspace{ID: 7, OwnerID: 1, Name: "Test Workspace"}
	editStore := &mockYouTubeVideoEditStore{
		findFn: func(ctx context.Context, id string) (*models.YouTubeVideoEdit, error) {
			return &models.YouTubeVideoEdit{
				ID:               "session-123",
				WorkspaceID:      workspace.ID,
				YouTubeVideoID:   "ytvideo123",
				VeloxProjectID:   "ve-project-123",
				Status:           "editing",
				DesiredPrivacy:   "public",
				ThumbnailMediaID: strPtr("asset-uuid-123"),
			}, nil
		},
	}
	var (
		publishCalled bool
		localCalled   bool
	)
	youTubeSvc := &mockYouTubeOAuthServiceForEditor{
		publishThumbnailFn: func(ctx context.Context, accessToken, videoID string, data []byte, mimeType, privacyStatus string, publishAt *time.Time, opts models.YouTubePublishOptions) (string, error) {
			publishCalled = true
			return "", nil
		},
		upsertLocalizationsFn: func(ctx context.Context, accessToken, videoID, lang string, tr models.YouTubeTranslation) error {
			localCalled = true
			return nil
		},
	}
	r := newPublishRouter(t, workspace, editStore,
		WithMediaStore(newMockMediaStore()),
		WithStorageProvider(newMockStorageProvider()),
		WithYouTubeService(youTubeSvc),
	)
	// Two tags, each ~260 chars + the comma separator → ~521 total
	// (one over the YouTube bound).
	payload := map[string]any{
		"tags": []string{strings.Repeat("a", 260), strings.Repeat("b", 260)},
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/session-123/publish", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for tag-char-limit, got %d: %s", w.Code, w.Body.String())
	}
	if publishCalled || localCalled {
		t.Fatalf("publish + UpsertLocalizations must NOT be called when validation fails (publish=%v local=%v)", publishCalled, localCalled)
	}
	if !strings.Contains(w.Body.String(), "exceeds YouTube bound") {
		t.Errorf("expected error body to mention 'exceeds YouTube bound', got %q", w.Body.String())
	}
}

// TestPublishYouTubeEditorSession_RejectsMalformedLanguage gates
// the BCP-47 sanity check. The orchestrator must 400 BEFORE the
// API call so a code with a slash / letterless subtag doesn't waste
// 1600 quota.
func TestPublishYouTubeEditorSession_RejectsMalformedLanguage(t *testing.T) {
	workspace := &models.Workspace{ID: 7, OwnerID: 1, Name: "Test Workspace"}
	editStore := &mockYouTubeVideoEditStore{
		findFn: func(ctx context.Context, id string) (*models.YouTubeVideoEdit, error) {
			return &models.YouTubeVideoEdit{
				ID:               "session-123",
				WorkspaceID:      workspace.ID,
				YouTubeVideoID:   "ytvideo123",
				VeloxProjectID:   "ve-project-123",
				Status:           "editing",
				DesiredPrivacy:   "public",
				ThumbnailMediaID: strPtr("asset-uuid-123"),
			}, nil
		},
	}
	youTubeSvc := &mockYouTubeOAuthServiceForEditor{
		publishThumbnailFn: func(ctx context.Context, accessToken, videoID string, data []byte, mimeType, privacyStatus string, publishAt *time.Time, opts models.YouTubePublishOptions) (string, error) {
			t.Errorf("PublishThumbnail must NOT be called when validation rejected the request")
			return "", nil
		},
	}
	r := newPublishRouter(t, workspace, editStore,
		WithMediaStore(newMockMediaStore()),
		WithStorageProvider(newMockStorageProvider()),
		WithYouTubeService(youTubeSvc),
	)

	// "/it" — slash is a forbidden top-level subtag char per BCP-47.
	payload := map[string]any{"default_language": "/it"}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/session-123/publish", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for malformed language, got %d: %s", w.Code, w.Body.String())
	}
	respBody := w.Body.String()
	if !strings.Contains(respBody, "forbidden character") && !strings.Contains(respBody, "malformed") {
		t.Errorf("expected error body to mention 'forbidden character' or 'malformed', got %q", respBody)
	}
}

// TestPublishYouTubeEditorSession_RejectsTranslationsWithoutDefaultLanguage
// gates the YouTube-published invariant: localizations require a
// default_language, otherwise the API returns 4xx. Failing fast on
// the validator saves a paid-for 4xx response.
func TestPublishYouTubeEditorSession_RejectsTranslationsWithoutDefaultLanguage(t *testing.T) {
	workspace := &models.Workspace{ID: 7, OwnerID: 1, Name: "Test Workspace"}
	editStore := &mockYouTubeVideoEditStore{
		findFn: func(ctx context.Context, id string) (*models.YouTubeVideoEdit, error) {
			return &models.YouTubeVideoEdit{
				ID:               "session-123",
				WorkspaceID:      workspace.ID,
				YouTubeVideoID:   "ytvideo123",
				VeloxProjectID:   "ve-project-123",
				Status:           "editing",
				DesiredPrivacy:   "public",
				ThumbnailMediaID: strPtr("asset-uuid-123"),
			}, nil
		},
	}
	youTubeSvc := &mockYouTubeOAuthServiceForEditor{
		publishThumbnailFn: func(ctx context.Context, accessToken, videoID string, data []byte, mimeType, privacyStatus string, publishAt *time.Time, opts models.YouTubePublishOptions) (string, error) {
			t.Errorf("PublishThumbnail must NOT be called when validation rejected the request")
			return "", nil
		},
		upsertLocalizationsFn: func(ctx context.Context, accessToken, videoID, lang string, tr models.YouTubeTranslation) error {
			t.Errorf("UpsertLocalizations must NOT be called when validation rejected the request")
			return nil
		},
	}
	r := newPublishRouter(t, workspace, editStore,
		WithMediaStore(newMockMediaStore()),
		WithStorageProvider(newMockStorageProvider()),
		WithYouTubeService(youTubeSvc),
	)

	payload := map[string]any{
		"translations": map[string]map[string]string{
			"en": {"title": "English title", "description": "English desc"},
		},
		// default_language DELIBERATELY omitted.
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/session-123/publish", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for translations-without-default-language, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "default_language") {
		t.Errorf("expected error body to mention 'default_language', got %q", w.Body.String())
	}
}

// TestPublishYouTubeEditorSession_HappyPathWithTagsAndTranslations
// is the consolidated happy-path for the P1 metadata extensions:
// tags + default_language + 2 translations. It exercises the
// three guarantees the orchestrator makes:
//
//  1. The snippet+status payload includes title, description, tags,
//     default_language — verified via the PublishThumbnail mock
//     capturing opts.
//  2. UpsertLocalizations is called ONCE per translation language,
//     IN sorted order (en, pt → alphabetical), so retries converge.
//  3. A mid-loop failure flips status='failed' + records the
//     failing lang on last_error so a retry picks up at the
//     right point.
//
// Step 3 is the hard one: it asserts against a custom
// upsertLocalizationsFn that fails on 'pt'. The test is the
// regression guard for the YouTube per-language retry semantics
// (idempotent on success, observable on partial failure).
func TestPublishYouTubeEditorSession_HappyPathWithTagsAndTranslations(t *testing.T) {
	account := &models.PlatformAccount{
		ID:             42,
		UserID:         1,
		Platform:       models.PlatformYouTube,
		PlatformUserID: "UC123",
		Username:       "testchannel",
		Status:         models.AccountStatusActive,
	}
	workspace := &models.Workspace{ID: 7, OwnerID: 1, Name: "Test Workspace"}
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			if id == account.ID {
				return account, nil
			}
			return nil, nil
		},
	}
	workspaceStore := &mockWorkspaceStore{
		findByIDFn: func(id int64) (*models.Workspace, error) {
			if id == workspace.ID {
				return workspace, nil
			}
			return nil, nil
		},
	}
	mediaStore := newMockMediaStore()
	mediaStore.assets["asset-uuid-123"] = &models.MediaAsset{
		ID:          "asset-uuid-123",
		UserID:      1,
		UploadKey:   "uploads/1/thumb.jpg",
		ContentType: "image/jpeg",
		SizeBytes:   1024,
		Status:      models.MediaAssetStatusReady,
	}

	var (
		updated               *models.YouTubeVideoEdit
		publishCalled         bool
		capturedOpts          models.YouTubePublishOptions
		localOrderMu          sync.Mutex
		localOrderCallCount   int
		localOrderLanguages   []string
		localFailingLanguage  string
	)
	editStore := &mockYouTubeVideoEditStore{
		findFn: func(ctx context.Context, id string) (*models.YouTubeVideoEdit, error) {
			return &models.YouTubeVideoEdit{
				ID:                "session-123",
				WorkspaceID:       workspace.ID,
				PlatformAccountID: account.ID,
				YouTubeVideoID:    "ytvideo123",
				VeloxProjectID:    "ve-project-123",
				ThumbnailMediaID:  strPtr("asset-uuid-123"),
				DesiredPrivacy:    "public",
				Status:            "editing",
			}, nil
		},
		update: func(ctx context.Context, edit *models.YouTubeVideoEdit) error {
			updated = edit
			return nil
		},
	}

	thumbnailBytes := []byte("fake-thumbnail-bytes")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write(thumbnailBytes)
	}))
	defer server.Close()

	storage := newMockStorageProvider()
	storage.assetURLFn = func(key string) string { return server.URL + "/" + key }

	youTubeSvc := &mockYouTubeOAuthServiceForEditor{
		publishThumbnailFn: func(ctx context.Context, accessToken, videoID string, data []byte, mimeType, privacyStatus string, publishAt *time.Time, opts models.YouTubePublishOptions) (string, error) {
			publishCalled = true
			capturedOpts = opts
			return "https://www.youtube.com/watch?v=" + videoID, nil
		},
		upsertLocalizationsFn: func(ctx context.Context, accessToken, videoID, lang string, tr models.YouTubeTranslation) error {
			localOrderMu.Lock()
			defer localOrderMu.Unlock()
			localOrderCallCount++
			localOrderLanguages = append(localOrderLanguages, lang)
			if lang == localFailingLanguage {
				return errors.New("simulated mid-loop YouTube failure for lang=" + lang + " on attempt")
			}
			return nil
		},
	}

	r := mustNewRouterWithDefaults(
		services.NewCapabilityRouter(),
		store,
		auth.NewManager(testJWTSecret, 24),
		"https://app.instaedit.org",
		nil,
		WithWorkspaceStore(workspaceStore),
		WithYouTubeVideoEditStore(editStore),
		WithMediaStore(mediaStore),
		WithStorageProvider(storage),
		WithYouTubeService(youTubeSvc),
		WithCredentialVault(&mockCredentialVault{
			getFn: func(ctx context.Context, id int64, tt string) (*models.OAuthToken, error) {
				if id == account.ID {
					return &models.OAuthToken{AccessToken: "valid-token"}, nil
				}
				return nil, errors.New("token not found")
			},
		}),
	)

	// First leg: both translations succeed. Single sorted-order
	// iteration ("en" before "pt" alphabetically). Capture tags +
	// default_language in opts.
	payload := map[string]any{
		"privacy_status":        "public",
		"title":                 "Titolo principale",
		"description":           "Descrizione principale",
		"tags":                  []string{"news", "italia", "video"},
		"default_language":      "it",
		"default_audio_language": "it",
		"translations": map[string]map[string]string{
			"en": {"title": "English title", "description": "English description"},
			"pt": {"title": "Título em português", "description": "Descrição em português"},
		},
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/session-123/publish", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 happy path with tags + translations, got %d: %s", w.Code, w.Body.String())
	}
	if !publishCalled {
		t.Fatalf("expected PublishThumbnail to be called once")
	}
	if capturedOpts.Title != "Titolo principale" || capturedOpts.Description != "Descrizione principale" {
		t.Errorf("captured opts lost title/description: title=%q description=%q", capturedOpts.Title, capturedOpts.Description)
	}
	if len(capturedOpts.Tags) != 3 || capturedOpts.Tags[0] != "news" || capturedOpts.Tags[1] != "italia" || capturedOpts.Tags[2] != "video" {
		t.Errorf("captured opts lost tags: %v", capturedOpts.Tags)
	}
	if capturedOpts.DefaultLanguage != "it" {
		t.Errorf("captured opts lost default_language: got %q", capturedOpts.DefaultLanguage)
	}
	if capturedOpts.DefaultAudioLanguage != "it" {
		t.Errorf("captured opts lost default_audio_language: got %q", capturedOpts.DefaultAudioLanguage)
	}
	if len(capturedOpts.Translations) != 2 {
		t.Errorf("captured opts lost translations: got %d entries", len(capturedOpts.Translations))
	}
	localOrderMu.Lock()
	localOrderCallCountSnapshot := localOrderCallCount
	localOrderLanguagesSnapshot := append([]string(nil), localOrderLanguages...)
	localOrderMu.Unlock()
	if localOrderCallCountSnapshot != 2 {
		t.Errorf("expected 2 UpsertLocalizations calls, got %d", localOrderCallCountSnapshot)
	}
	if len(localOrderLanguagesSnapshot) != 2 || localOrderLanguagesSnapshot[0] != "en" || localOrderLanguagesSnapshot[1] != "pt" {
		t.Errorf("expected sorted languages [en, pt], got %v", localOrderLanguagesSnapshot)
	}

	// Second leg: same payload but flip a flag on the mock so that
	// UpsertLocalizations fails mid-loop on 'pt'. The orchestrator
	// must (a) NOT flip status='published' on the session row,
	// (b) record the failing lang on last_error, and (c) return 502
	// to the caller. Both 'en' (first in sorted order) and 'pt'
	// were attempted; the failing lang is the one after en.
	localFailingLanguage = "pt"
	updated = nil                                                     // reset to detect this run's update
	// Reset the CAS simulation state so MarkPublishing succeeds again
	// on the second request (the first leg consumed attempt 1).
	editStore.markPublishingMu.Lock()
	editStore.markPublishingAttempts = 0
	editStore.simulatedStatus = ""
	editStore.markPublishingMu.Unlock()
	localOrderMu.Lock()
	localOrderCallCount = 0
	localOrderLanguages = nil
	localOrderMu.Unlock()

	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/session-123/publish", bytes.NewReader(body))
	req2.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req2, 1)
	w2 := httptest.NewRecorder()
	r.Setup().ServeHTTP(w2, req2)

	if w2.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 on mid-loop failure, got %d: %s", w2.Code, w2.Body.String())
	}
	if updated == nil || updated.Status != "failed" {
		t.Fatalf("expected session status failed after mid-loop error, got %v", updated)
	}
	if !strings.Contains(updated.LastError, "pt") {
		t.Errorf("expected last_error to mention the failing language 'pt', got %q", updated.LastError)
	}
	// Both translations were attempted — first succeeds, second fails.
	// 'en' is sorted before 'pt'; the rationale is the orchestrator
	// stops at the first failure rather than skipping onward.
	localOrderMu.Lock()
	localOrderCallCountSnapshot2 := localOrderCallCount
	localOrderMu.Unlock()
	if localOrderCallCountSnapshot2 != 2 {
		t.Errorf("orchestrator must attempt every language up to the first failure (sorted order [en, pt] → 2 calls); got %d", localOrderCallCountSnapshot2)
	}
}
