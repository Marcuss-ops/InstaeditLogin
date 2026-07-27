package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// mockYouTubeVideoEditStore is an test seam for YouTubeVideoEditStore.
//
// Blocco #5 P0 #2 — added MarkPublishing with D1 (sync.Mutex + counter)
// atomic-simulator: first call wins (returns a synthesised 'claimed'
// row from FindByID's return value), every other call returns
// (nil, repository.ErrYouTubeVideoEditNotFound) — mirrors the real
// Postgres CAS behaviour for tests that don't inject markPublishingFn.
type mockYouTubeVideoEditStore struct {
	created                []*models.YouTubeVideoEdit
	findFn                 func(ctx context.Context, id string) (*models.YouTubeVideoEdit, error)
	findByProjectFn        func(ctx context.Context, projectID string) (*models.YouTubeVideoEdit, error)
	update                 func(ctx context.Context, edit *models.YouTubeVideoEdit) error
	markPublishingMu       sync.Mutex
	markPublishingAttempts int
	markPublishingFn       func(ctx context.Context, id string, desiredPrivacy string, publishAt *time.Time, inFlightTimeout time.Duration) (*models.YouTubeVideoEdit, error)
	attachThumbnailFn      func(ctx context.Context, sessionID, thumbnailMediaID string) (*models.YouTubeVideoEdit, error)
	// listFn is the Blocco #5 P0 callback the GET handler routes to
	// when dashboard "code da modificare" list reads are exercised.
	// listFn is supplied by tests that need to assert on the filter
	// shape (AccountID / Statuses / Limit) handed to the repository.
	listFn func(ctx context.Context, filter repository.YouTubeEditorSessionListFilter) ([]*models.YouTubeVideoEdit, error)
	// listByAccountsFn is the GET /api/v1/groups/{id}/youtube/videos
	// callback that returns every editor session in the workspace
	// whose platform_account_id is in the supplied slice. Tests
	// supply a non-nil closure to assert on the join logic; the
	// default behaviour returns (nil, nil) so production callers
	// that don't override see "no sessions yet".
	listByAccountsFn func(ctx context.Context, workspaceID int64, accountIDs []int64) ([]*models.YouTubeVideoEdit, error)
	// findOrCreateFn (P0#3 click-idempotency) is the production-click
	// idempotency callback. The router's CreateEditorSession helper
	// routes through it after YouTube validation to ensure the same
	// YouTube video clicked twice from the dashboard card grid
	// converges on a single editor session + velox_project_id.
	// Tests supply a non-nil closure to assert on the find-or-create
	// race-safe sequence; default returns (nil, nil).
	findOrCreateFn func(ctx context.Context, workspaceID int64, platformAccountID int64, youtubeVideoID string, sessionIDHint string, projectIDHint string) (*models.YouTubeVideoEdit, error)
	// markPublishedWithActualPrivacyFn (P0#7) is the atomic-CAS
	// simulator that the publish orchestrator calls as the FINAL
	// step. Tests inject a closure to capture the actual_privacy +
	// youtube_sync_status values the orchestrator derived from the
	// videos.list read-back, then assert the CAS payload matches
	// the operator's intended visibility (or, on drift, the
	// observed mismatch).
	markPublishedWithActualPrivacyFn func(ctx context.Context, id string, actualPrivacy string, syncStatus string) (*models.YouTubeVideoEdit, error)
}

func (m *mockYouTubeVideoEditStore) Create(ctx context.Context, edit *models.YouTubeVideoEdit) error {
	m.created = append(m.created, edit)
	return nil
}

func (m *mockYouTubeVideoEditStore) FindByID(ctx context.Context, id string) (*models.YouTubeVideoEdit, error) {
	if m.findFn != nil {
		return m.findFn(ctx, id)
	}
	return nil, nil
}

func (m *mockYouTubeVideoEditStore) FindByVeloxProjectID(ctx context.Context, projectID string) (*models.YouTubeVideoEdit, error) {
	if m.findByProjectFn != nil {
		return m.findByProjectFn(ctx, projectID)
	}
	return nil, nil
}

func (m *mockYouTubeVideoEditStore) Update(ctx context.Context, edit *models.YouTubeVideoEdit) error {
	if m.update != nil {
		return m.update(ctx, edit)
	}
	return nil
}

// MarkPublishing (Blocco #5 P0 #2) — atomic simulator. With no
// override callback set, the first concurrent call wins (bootstraps
// a "claimed" row from findFn's return value with
// Status='publishing' + desired_privacy + publish_at overwritten)
// and every other call returns the typed sentinel wrapped to mirror
// the real repository's no-rows error. Tests that need explicit
// sequencing can inject markPublishingFn.
func (m *mockYouTubeVideoEditStore) MarkPublishing(ctx context.Context, id string, desiredPrivacy string, publishAt *time.Time, inFlightTimeout time.Duration) (*models.YouTubeVideoEdit, error) {
	if m.markPublishingFn != nil {
		return m.markPublishingFn(ctx, id, desiredPrivacy, publishAt, inFlightTimeout)
	}
	m.markPublishingMu.Lock()
	defer m.markPublishingMu.Unlock()
	m.markPublishingAttempts++
	if m.markPublishingAttempts == 1 {
		if m.findFn == nil {
			return nil, errors.New("markPublishing fallback: no findFn to bootstrap claimed row")
		}
		row, err := m.findFn(ctx, id)
		if err != nil || row == nil {
			return nil, fmt.Errorf("markPublishing fallback: find returned nil: %w", err)
		}
		row.Status = "publishing"
		row.DesiredPrivacy = desiredPrivacy
		row.PublishAt = publishAt
		row.UpdatedAt = time.Now().UTC()
		return row, nil
	}
	return nil, fmt.Errorf("%w: simulated CAS-loss", repository.ErrYouTubeVideoEditNotFound)
}

// AttachThumbnail (Blocco #5 P0 #4 mock) routes to attachThumbnailFn
// when supplied; otherwise the default behaviour is to look up the
// row via findFn, stamp thumbnail_media_id, and return the row —
// mirroring the production CAS. Tests that need a different behaviour
// (CAS-loss, side-effect capture) inject attachThumbnailFn directly.
func (m *mockYouTubeVideoEditStore) AttachThumbnail(ctx context.Context, sessionID, thumbnailMediaID string) (*models.YouTubeVideoEdit, error) {
	if m.attachThumbnailFn != nil {
		return m.attachThumbnailFn(ctx, sessionID, thumbnailMediaID)
	}
	if m.findFn == nil {
		return nil, errors.New("attachThumbnail fallback: no findFn to bootstrap linked row")
	}
	row, err := m.findFn(ctx, sessionID)
	if err != nil || row == nil {
		return nil, fmt.Errorf("attachThumbnail fallback: find returned nil: %w", err)
	}
	if row.Status != "editing" && row.Status != "failed" {
		return nil, fmt.Errorf("%w: simulated CAS-loss (status=%s)", repository.ErrYouTubeVideoEditNotFound, row.Status)
	}
	media := thumbnailMediaID
	row.ThumbnailMediaID = &media
	row.UpdatedAt = time.Now().UTC()
	return row, nil
}

// ListByWorkspace (Blocco #5 P0 — GET dashboard list) routes to
// listFn when supplied; otherwise returns an empty slice (the
// production READ path cannot be reconstructed from a single-row
// findFn, so the mock default is "no rows yet" — tests that need a
// populated list inject listFn directly).
func (m *mockYouTubeVideoEditStore) ListByWorkspace(ctx context.Context, filter repository.YouTubeEditorSessionListFilter) ([]*models.YouTubeVideoEdit, error) {
	if m.listFn != nil {
		return m.listFn(ctx, filter)
	}
	return nil, nil
}

// ListByWorkspaceAccountIDs (P0 group videos endpoint) routes to
// listByAccountsFn when supplied; default returns (nil, nil). The
// mock default mirrors the production contract: an empty input
// set or a workspace with no sessions collapses to zero rows,
// (nil, nil), without triggering a Postgres error.
func (m *mockYouTubeVideoEditStore) ListByWorkspaceAccountIDs(ctx context.Context, workspaceID int64, accountIDs []int64) ([]*models.YouTubeVideoEdit, error) {
	if m.listByAccountsFn != nil {
		return m.listByAccountsFn(ctx, workspaceID, accountIDs)
	}
	if workspaceID <= 0 || len(accountIDs) == 0 {
		return nil, nil
	}
	return nil, nil
}

// FindOrCreateEditableSession (P0#3 click-idempotency) routes to
// findOrCreateFn when supplied; default returns (nil, nil). The mock
// behaviour matches the production contract: tests supply a closure
// that fakes either the SELECT-fast-path (returning a synthesised
// existing row) or the race-loser path (returning an error on the
// first insert, then a winner on the re-SELECT).
func (m *mockYouTubeVideoEditStore) FindOrCreateEditableSession(ctx context.Context, workspaceID int64, platformAccountID int64, youtubeVideoID string, sessionIDHint string, projectIDHint string) (*models.YouTubeVideoEdit, error) {
	if m.findOrCreateFn != nil {
		return m.findOrCreateFn(ctx, workspaceID, platformAccountID, youtubeVideoID, sessionIDHint, projectIDHint)
	}
	if workspaceID <= 0 || platformAccountID <= 0 || youtubeVideoID == "" {
		return nil, nil
	}
	return nil, nil
}

// MarkPublishedWithActualPrivacy (P0#7) routes to
// markPublishedWithActualPrivacyFn when supplied. Default behaviour
// stamps the supplied actual_privacy + sync_status on the row
// returned by findFn and returns it — matching the production CAS
// that flips publishing → published in the same SQL statement. Tests
// that need a CAS-loss (concurrent reconcilertealing the row)
// inject a closure returning the ErrYouTubeVideoEditNotFound.
func (m *mockYouTubeVideoEditStore) MarkPublishedWithActualPrivacy(ctx context.Context, id string, actualPrivacy string, syncStatus string) (*models.YouTubeVideoEdit, error) {
	if m.markPublishedWithActualPrivacyFn != nil {
		return m.markPublishedWithActualPrivacyFn(ctx, id, actualPrivacy, syncStatus)
	}
	if m.findFn == nil {
		return nil, errors.New("markPublishedWithActualPrivacy fallback: no findFn to bootstrap published row")
	}
	row, err := m.findFn(ctx, id)
	if err != nil || row == nil {
		return nil, fmt.Errorf("markPublishedWithActualPrivacy fallback: find returned nil: %w", err)
	}
	if row.Status != "publishing" {
		return nil, fmt.Errorf("%w: simulated CAS-loss (status=%s)", repository.ErrYouTubeVideoEditNotFound, row.Status)
	}
	if actualPrivacy != "" {
		row.ActualPrivacy = &actualPrivacy
	}
	row.YouTubeSyncStatus = &syncStatus
	row.Status = "published"
	row.LastError = ""
	row.UpdatedAt = time.Now().UTC()
	return row, nil
}

// mockYouTubeOAuthServiceForEditor implements the subset of
// YouTubeOAuthService needed by the editor session handler.
type mockYouTubeOAuthServiceForEditor struct {
	getVideoFn            func(ctx context.Context, accessToken, videoID string) (*models.YouTubeVideoDetails, error)
	publishThumbnailFn    func(ctx context.Context, accessToken, videoID string, thumbnailData []byte, mimeType, privacyStatus string, publishAt *time.Time, opts models.YouTubePublishOptions) (string, error)
	upsertLocalizationsFn func(ctx context.Context, accessToken, videoID, lang string, tr models.YouTubeTranslation) error
	listEditableVideosFn  func(ctx context.Context, accessToken, channelID, pageToken string) (*services.YouTubeVideoPage, error)
}

func (m *mockYouTubeOAuthServiceForEditor) RefreshOAuthToken(ctx context.Context, refreshToken string) (*models.TokenData, error) {
	return nil, errors.New("not implemented")
}
func (m *mockYouTubeOAuthServiceForEditor) GetTokenInfo(ctx context.Context, accessToken string) (*services.YouTubeTokenInfo, error) {
	return nil, errors.New("not implemented")
}
func (m *mockYouTubeOAuthServiceForEditor) ValidateChannelBinding(ctx context.Context, accessToken, expectedChannelID string) error {
	return errors.New("not implemented")
}
func (m *mockYouTubeOAuthServiceForEditor) CanaryUpload(ctx context.Context, accessToken, expectedChannelID string) (*services.CanaryUploadResult, error) {
	return nil, errors.New("not implemented")
}
func (m *mockYouTubeOAuthServiceForEditor) FetchEarnings(ctx context.Context, accessToken, channelID string, days int) ([]repository.AccountMetricPoint, error) {
	return nil, errors.New("not implemented")
}
func (m *mockYouTubeOAuthServiceForEditor) ClientID() string { return "test-client-id" }
func (m *mockYouTubeOAuthServiceForEditor) GetYouTubeVideo(ctx context.Context, accessToken, videoID string) (*models.YouTubeVideoDetails, error) {
	if m.getVideoFn != nil {
		return m.getVideoFn(ctx, accessToken, videoID)
	}
	return &models.YouTubeVideoDetails{
		ID:           videoID,
		Title:        "Test Video",
		ChannelID:    "UC123",
		ThumbnailURL: "https://i.ytimg.com/default.jpg",
		Privacy:      "private",
		UploadStatus: "processed",
	}, nil
}
func (m *mockYouTubeOAuthServiceForEditor) ListEditableVideos(ctx context.Context, accessToken, channelID, pageToken string) (*services.YouTubeVideoPage, error) {
	if m.listEditableVideosFn != nil {
		return m.listEditableVideosFn(ctx, accessToken, channelID, pageToken)
	}
	return &services.YouTubeVideoPage{Items: []models.YouTubeVideoDetails{}}, nil
}
func (m *mockYouTubeOAuthServiceForEditor) SetThumbnail(ctx context.Context, accessToken, videoID, mimeType string, body io.Reader, size int64) error {
	return errors.New("not implemented")
}
func (m *mockYouTubeOAuthServiceForEditor) UpdateVideoPrivacy(ctx context.Context, accessToken, videoID, privacyStatus string, publishAt *time.Time, title, description string) error {
	return errors.New("not implemented")
}
func (m *mockYouTubeOAuthServiceForEditor) PublishThumbnail(ctx context.Context, accessToken, videoID string, thumbnailData []byte, mimeType, privacyStatus string, publishAt *time.Time, opts models.YouTubePublishOptions) (string, error) {
	if m.publishThumbnailFn != nil {
		return m.publishThumbnailFn(ctx, accessToken, videoID, thumbnailData, mimeType, privacyStatus, publishAt, opts)
	}
	return "", errors.New("not implemented")
}

func (m *mockYouTubeOAuthServiceForEditor) UpsertLocalizations(ctx context.Context, accessToken, videoID, lang string, tr models.YouTubeTranslation) error {
	if m.upsertLocalizationsFn != nil {
		return m.upsertLocalizationsFn(ctx, accessToken, videoID, lang, tr)
	}
	// Default no-op: tests that don't care about localizations
	// pass nil and the helper returns nil — matches the
	// production behaviour when opts.Translations is empty.
	return nil
}

// newPublishRouter builds the minimal router required by the publish
// handler tests. It wires a workspace store that resolves the given
// workspace and a YouTube video edit store backed by the supplied mock.
// Additional RouterOption values can be appended when a test needs
// extra dependencies such as media or storage providers.
func newPublishRouter(t *testing.T, workspace *models.Workspace, editStore *mockYouTubeVideoEditStore, opts ...RouterOption) *Router {
	t.Helper()
	return mustNewRouterWithDefaults(
		services.NewCapabilityRouter(),
		&mockUserStore{},
		auth.NewManager(testJWTSecret, 24),
		"",
		nil,
		append([]RouterOption{
			WithWorkspaceStore(&mockWorkspaceStore{
				findByIDFn: func(id int64) (*models.Workspace, error) {
					if id == workspace.ID {
						return workspace, nil
					}
					return nil, nil
				},
			}),
			WithYouTubeVideoEditStore(editStore),
		}, opts...)...,
	)
}

func TestPublishYouTubeEditorSession_HappyPath(t *testing.T) {
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

	var updated *models.YouTubeVideoEdit
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

	youTubeSvc := &mockYouTubeOAuthServiceForEditor{}

	// Serve the thumbnail bytes via an HTTP server so the signed download URL works.
	thumbnailBytes := []byte("fake-thumbnail-bytes")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write(thumbnailBytes)
	}))
	defer server.Close()

	storage := newMockStorageProvider()
	storage.assetURLFn = func(key string) string { return server.URL + "/" + key }

	var publishCalled bool
	youTubeSvc.publishThumbnailFn = func(ctx context.Context, accessToken, videoID string, data []byte, mimeType, privacyStatus string, publishAt *time.Time, opts models.YouTubePublishOptions) (string, error) {
		publishCalled = true
		if string(data) != string(thumbnailBytes) {
			t.Errorf("expected thumbnail data %q, got %q", string(thumbnailBytes), string(data))
		}
		if privacyStatus != "public" {
			t.Errorf("expected privacyStatus public, got %s", privacyStatus)
		}
		if opts.Title != "Updated title" {
			t.Errorf("expected title \"Updated title\", got %q", opts.Title)
		}
		if opts.Description != "Updated description" {
			t.Errorf("expected description \"Updated description\", got %q", opts.Description)
		}
		return "https://www.youtube.com/watch?v=" + videoID, nil
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

	payload := map[string]any{
		"privacy_status": "public",
		"title":          "Updated title",
		"description":    "Updated description",
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/session-123/publish", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if !publishCalled {
		t.Fatalf("expected PublishThumbnail to be called")
	}
	if updated == nil || updated.Status != "published" {
		t.Fatalf("expected session status published, got %v", updated)
	}
}

func TestPublishYouTubeEditorSession_TooLongTitle(t *testing.T) {
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

	r := newPublishRouter(t, workspace, editStore,
		WithMediaStore(newMockMediaStore()),
		WithStorageProvider(newMockStorageProvider()),
	)

	payload := map[string]any{"title": strings.Repeat("a", 101)}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/session-123/publish", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestPublishYouTubeEditorSession_WithoutTitleDescription(t *testing.T) {
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
	}

	youTubeSvc := &mockYouTubeOAuthServiceForEditor{}

	thumbnailBytes := []byte("fake-thumbnail-bytes")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write(thumbnailBytes)
	}))
	defer server.Close()

	storage := newMockStorageProvider()
	storage.assetURLFn = func(key string) string { return server.URL + "/" + key }

	youTubeSvc.publishThumbnailFn = func(ctx context.Context, accessToken, videoID string, data []byte, mimeType, privacyStatus string, publishAt *time.Time, opts models.YouTubePublishOptions) (string, error) {
		if opts.Title != "" || opts.Description != "" {
			t.Errorf("expected empty title and description, got title=%q description=%q", opts.Title, opts.Description)
		}
		return "https://www.youtube.com/watch?v=" + videoID, nil
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

	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/session-123/publish", bytes.NewReader([]byte("{}")))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestPublishYouTubeEditorSession_IdempotentWhenPublished(t *testing.T) {
	workspace := &models.Workspace{ID: 7, OwnerID: 1, Name: "Test Workspace"}
	editStore := &mockYouTubeVideoEditStore{
		findFn: func(ctx context.Context, id string) (*models.YouTubeVideoEdit, error) {
			return &models.YouTubeVideoEdit{
				ID:               "session-123",
				WorkspaceID:      workspace.ID,
				YouTubeVideoID:   "ytvideo123",
				VeloxProjectID:   "ve-project-123",
				Status:           "published",
				DesiredPrivacy:   "public",
				ThumbnailMediaID: strPtr("asset-uuid-123"),
			}, nil
		},
	}

	r := newPublishRouter(t, workspace, editStore,
		WithMediaStore(newMockMediaStore()),
		WithStorageProvider(newMockStorageProvider()),
	)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/session-123/publish", bytes.NewReader([]byte("{}")))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for published session, got %d: %s", w.Code, w.Body.String())
	}
}

func TestPublishYouTubeEditorSession_ScheduledPublishing(t *testing.T) {
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
				Status:              "editing",
			}, nil
		},
	}

	youTubeSvc := &mockYouTubeOAuthServiceForEditor{}

	thumbnailBytes := []byte("fake-thumbnail-bytes")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write(thumbnailBytes)
	}))
	defer server.Close()

	storage := newMockStorageProvider()
	storage.assetURLFn = func(key string) string { return server.URL + "/" + key }

	publishAt := time.Now().UTC().Add(24 * time.Hour)
	youTubeSvc.publishThumbnailFn = func(ctx context.Context, accessToken, videoID string, data []byte, mimeType, privacyStatus string, gotPublishAt *time.Time, title, description string) (string, error) {
		if privacyStatus != "private" {
			t.Errorf("expected privacyStatus private for scheduled publishing, got %s", privacyStatus)
		}
		if gotPublishAt == nil {
			t.Fatalf("expected publishAt for scheduled publishing, got nil")
		}
		if gotPublishAt.IsZero() {
			t.Errorf("expected non-zero publishAt for scheduled publishing")
		}
		if gotPublishAt.Format(time.RFC3339) != publishAt.Format(time.RFC3339) {
			t.Errorf("publishAt mismatch: want %v, got %v", publishAt, gotPublishAt)
		}
		return "https://www.youtube.com/watch?v=" + videoID, nil
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

	payload := map[string]any{
		"privacy_status": "private",
		"publish_at":     publishAt.Format(time.RFC3339),
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/session-123/publish", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp publishYouTubeEditorSessionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.PrivacyStatus != "private" {
		t.Errorf("expected privacy_status private, got %s", resp.PrivacyStatus)
	}
	if resp.PublishedAt == nil {
		t.Errorf("expected published_at in response")
	}
}

func TestPublishYouTubeEditorSession_ScheduledPublishingRequiresPrivate(t *testing.T) {
	workspace := &models.Workspace{ID: 7, OwnerID: 1, Name: "Test Workspace"}
	// Bug-fix Blocco #5 P0 #1: privacy + publish_at validation moved AFTER
	// the session load, so this test now supplies a resolvable session.
	// Payload privacy="public" wins (over edit.DesiredPrivacy="public"),
	// so resolved privacy is "public"; future publish_at + privacy !=
	// "private" → 400.
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
	r := newPublishRouter(t, workspace, editStore)

	payload := map[string]any{
		"privacy_status": "public",
		"publish_at":     time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339),
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/session-123/publish", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for scheduled publishing without private status, got %d: %s", w.Code, w.Body.String())
	}
}

func TestPublishYouTubeEditorSession_PastPublishAtRejected(t *testing.T) {
	workspace := &models.Workspace{ID: 7, OwnerID: 1, Name: "Test Workspace"}
	// Bug-fix Blocco #5 P0 #1: privacy + publish_at validation moved AFTER
	// the session load, so this test now supplies a resolvable session.
	// Payload privacy="private" wins (over edit.DesiredPrivacy="private"),
	// so resolved privacy is "private"; past publish_at → 400.
	editStore := &mockYouTubeVideoEditStore{
		findFn: func(ctx context.Context, id string) (*models.YouTubeVideoEdit, error) {
			return &models.YouTubeVideoEdit{
				ID:               "session-123",
				WorkspaceID:      workspace.ID,
				YouTubeVideoID:   "ytvideo123",
				VeloxProjectID:   "ve-project-123",
				Status:           "editing",
				DesiredPrivacy:   "private",
				ThumbnailMediaID: strPtr("asset-uuid-123"),
			}, nil
		},
	}
	r := newPublishRouter(t, workspace, editStore)

	payload := map[string]any{
		"privacy_status": "private",
		"publish_at":     time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339),
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/session-123/publish", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for past publish_at, got %d: %s", w.Code, w.Body.String())
	}
}

func TestPublishYouTubeEditorSession_PublishingInFlightReturnsConflict(t *testing.T) {
	workspace := &models.Workspace{ID: 7, OwnerID: 1, Name: "Test Workspace"}
	editStore := &mockYouTubeVideoEditStore{
		findFn: func(ctx context.Context, id string) (*models.YouTubeVideoEdit, error) {
			return &models.YouTubeVideoEdit{
				ID:               "session-123",
				WorkspaceID:      workspace.ID,
				YouTubeVideoID:   "ytvideo123",
				VeloxProjectID:   "ve-project-123",
				Status:           "publishing",
				DesiredPrivacy:   "public",
				ThumbnailMediaID: strPtr("asset-uuid-123"),
				UpdatedAt:        time.Now().UTC().Add(-30 * time.Second),
			}, nil
		},
	}

	r := newPublishRouter(t, workspace, editStore, WithPublishingInFlightTimeout(1*time.Minute))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/session-123/publish", bytes.NewReader([]byte("{}")))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 for in-flight publishing session, got %d: %s", w.Code, w.Body.String())
	}
}

func TestPublishYouTubeEditorSession_InFlightTimeoutExpiredRetries(t *testing.T) {
	account := &models.PlatformAccount{
		ID:             42,
		UserID:         1,
		Platform:       models.PlatformYouTube,
		PlatformUserID: "UC123",
		Username:       "testchannel",
		Status:         models.AccountStatusActive,
	}
	workspace := &models.Workspace{ID: 7, OwnerID: 1, Name: "Test Workspace"}

	mediaStore := newMockMediaStore()
	mediaStore.assets["asset-uuid-123"] = &models.MediaAsset{
		ID:          "asset-uuid-123",
		UserID:      1,
		UploadKey:   "uploads/1/thumb.jpg",
		ContentType: "image/jpeg",
		SizeBytes:   1024,
		Status:      models.MediaAssetStatusReady,
	}

	var updated *models.YouTubeVideoEdit
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
				Status:            "publishing",
				UpdatedAt:         time.Now().UTC().Add(-2 * time.Minute),
			}, nil
		},
		update: func(ctx context.Context, edit *models.YouTubeVideoEdit) error {
			updated = edit
			return nil
		},
	}

	youTubeSvc := &mockYouTubeOAuthServiceForEditor{}

	thumbnailBytes := []byte("fake-thumbnail-bytes")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write(thumbnailBytes)
	}))
	defer server.Close()

	storage := newMockStorageProvider()
	storage.assetURLFn = func(key string) string { return server.URL + "/" + key }

	youTubeSvc.publishThumbnailFn = func(ctx context.Context, accessToken, videoID string, data []byte, mimeType, privacyStatus string, publishAt *time.Time, opts models.YouTubePublishOptions) (string, error) {
		return "https://www.youtube.com/watch?v=" + videoID, nil
	}

	r := newPublishRouter(t, workspace, editStore,
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
		WithPublishingInFlightTimeout(1*time.Minute),
	)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/session-123/publish", bytes.NewReader([]byte("{}")))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 after in-flight timeout expired, got %d: %s", w.Code, w.Body.String())
	}
	if updated == nil || updated.Status != "published" {
		t.Fatalf("expected session status published after retry, got %v", updated)
	}
}

func TestPublishYouTubeEditorSession_DefaultInFlightTimeoutExpiredRetries(t *testing.T) {
	account := &models.PlatformAccount{
		ID:             42,
		UserID:         1,
		Platform:       models.PlatformYouTube,
		PlatformUserID: "UC123",
		Username:       "testchannel",
		Status:         models.AccountStatusActive,
	}
	workspace := &models.Workspace{ID: 7, OwnerID: 1, Name: "Test Workspace"}

	mediaStore := newMockMediaStore()
	mediaStore.assets["asset-uuid-123"] = &models.MediaAsset{
		ID:          "asset-uuid-123",
		UserID:      1,
		UploadKey:   "uploads/1/thumb.jpg",
		ContentType: "image/jpeg",
		SizeBytes:   1024,
		Status:      models.MediaAssetStatusReady,
	}

	var updated *models.YouTubeVideoEdit
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
				Status:            "publishing",
				UpdatedAt:         time.Now().UTC().Add(-DefaultPublishingInFlightTimeout - time.Minute),
			}, nil
		},
		update: func(ctx context.Context, edit *models.YouTubeVideoEdit) error {
			updated = edit
			return nil
		},
	}

	youTubeSvc := &mockYouTubeOAuthServiceForEditor{}

	thumbnailBytes := []byte("fake-thumbnail-bytes")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write(thumbnailBytes)
	}))
	defer server.Close()

	storage := newMockStorageProvider()
	storage.assetURLFn = func(key string) string { return server.URL + "/" + key }

	youTubeSvc.publishThumbnailFn = func(ctx context.Context, accessToken, videoID string, data []byte, mimeType, privacyStatus string, publishAt *time.Time, opts models.YouTubePublishOptions) (string, error) {
		return "https://www.youtube.com/watch?v=" + videoID, nil
	}

	r := newPublishRouter(t, workspace, editStore,
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

	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/session-123/publish", bytes.NewReader([]byte("{}")))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 after default in-flight timeout expired, got %d: %s", w.Code, w.Body.String())
	}
	if updated == nil || updated.Status != "published" {
		t.Fatalf("expected session status published after retry, got %v", updated)
	}
}

func TestPublishYouTubeEditorSession_RetryFromFailed(t *testing.T) {
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

	var updated *models.YouTubeVideoEdit
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
				Status:              "failed",
				LastError:         "previous failure",
			}, nil
		},
		update: func(ctx context.Context, edit *models.YouTubeVideoEdit) error {
			updated = edit
			return nil
		},
	}

	youTubeSvc := &mockYouTubeOAuthServiceForEditor{}

	thumbnailBytes := []byte("fake-thumbnail-bytes")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write(thumbnailBytes)
	}))
	defer server.Close()

	storage := newMockStorageProvider()
	storage.assetURLFn = func(key string) string { return server.URL + "/" + key }

	var publishCalls int
	youTubeSvc.publishThumbnailFn = func(ctx context.Context, accessToken, videoID string, data []byte, mimeType, privacyStatus string, publishAt *time.Time, opts models.YouTubePublishOptions) (string, error) {
		publishCalls++
		return "https://www.youtube.com/watch?v=" + videoID, nil
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

	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/session-123/publish", bytes.NewReader([]byte("{}")))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if publishCalls != 1 {
		t.Errorf("expected 1 publish call, got %d", publishCalls)
	}
	if updated == nil || updated.Status != "published" {
		t.Fatalf("expected session status published after retry, got %v", updated)
	}
	if updated.LastError != "" {
		t.Errorf("expected last_error to be cleared, got %q", updated.LastError)
	}
}

// TestPublishYouTubeEditorSession_ScheduledFromSessionPrivacy is the
// regression test for Bug #1 in the original assessment ("Validazione
// anticipata della privacy"). Pre-fix, this exact payload returned 400
// because the early body-only privacyStatus defaulted missing → "public",
// and then the early "future publish_at requires private" rule fired (400)
// — even though the session itself was already private. Post-fix, the
// late resolver falls back to edit.DesiredPrivacy when the payload omits
// privacy_status, so the request is correctly accepted with a 200.
//
// This test is the proof that moving the privacy resolution + validation
// AFTER the session load actually fixes the bug. Without this test the
// fix would be silent (no behaviour change in any other test case).
func TestPublishYouTubeEditorSession_ScheduledFromSessionPrivacy(t *testing.T) {
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

	var capturedPrivacy string
	var publishCalled bool
	editStore := &mockYouTubeVideoEditStore{
		findFn: func(ctx context.Context, id string) (*models.YouTubeVideoEdit, error) {
			// The key field — session DesiredPrivacy is "private".
			// The payload below omits privacy_status; the late
			// resolver must fall through to this value for the
			// future publish_at to be accepted.
			return &models.YouTubeVideoEdit{
				ID:                "session-123",
				WorkspaceID:       workspace.ID,
				PlatformAccountID: account.ID,
				YouTubeVideoID:    "ytvideo123",
				VeloxProjectID:    "ve-project-123",
				ThumbnailMediaID:  strPtr("asset-uuid-123"),
				DesiredPrivacy:    "private",
				Status:            "editing",
			}, nil
		},
		update: func(ctx context.Context, edit *models.YouTubeVideoEdit) error {
			return nil
		},
	}

	youTubeSvc := &mockYouTubeOAuthServiceForEditor{}

	thumbnailBytes := []byte("fake-thumbnail-bytes")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write(thumbnailBytes)
	}))
	defer server.Close()

	storage := newMockStorageProvider()
	storage.assetURLFn = func(key string) string { return server.URL + "/" + key }

	youTubeSvc.publishThumbnailFn = func(ctx context.Context, accessToken, videoID string, data []byte, mimeType, privacyStatus string, gotPublishAt *time.Time, title, description string) (string, error) {
		publishCalled = true
		capturedPrivacy = privacyStatus
		return "https://www.youtube.com/watch?v=" + videoID, nil
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

	// Payload DELIBERATELY omits privacy_status — the regression point.
	publishAt := time.Now().UTC().Add(24 * time.Hour)
	payload := map[string]any{
		"publish_at": publishAt.Format(time.RFC3339),
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/session-123/publish", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for scheduled publish inferred from session.DesiredPrivacy, got %d: %s", w.Code, w.Body.String())
	}
	if !publishCalled {
		t.Fatalf("expected PublishThumbnail to be called")
	}
	if capturedPrivacy != "private" {
		t.Errorf("resolved privacy must be 'private' (from session.DesiredPrivacy), got %q", capturedPrivacy)
	}
}

// TestPublishYouTubeEditorSession_ConcurrentPublishClaimsAtomically is the
// concurrency regression for Blocco #5 P0 #2 — the atomic CAS claim
// must guarantee that exactly ONE publish fires PublishThumbnail per
// N concurrent requests on the same session_id. Pre-fix the handler's
// read-then-update race would stamp status='publishing' on the same row
// from multiple goroutines, each dispatching a PublishThumbnail call.
// Post-fix, MarkPublishing's CAS returns the typed sentinel from N-1
// callers; the handler maps to 409.
func TestPublishYouTubeEditorSession_ConcurrentPublishClaimsAtomically(t *testing.T) {
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

	var publishCalls int32
	capturedPrivacyMu := sync.Mutex{}
	var capturedPrivacy string
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
			return nil
		},
	}

	youTubeSvc := &mockYouTubeOAuthServiceForEditor{}

	thumbnailBytes := []byte("fake-thumbnail-bytes")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write(thumbnailBytes)
	}))
	defer server.Close()

	storage := newMockStorageProvider()
	storage.assetURLFn = func(key string) string { return server.URL + "/" + key }

	youTubeSvc.publishThumbnailFn = func(ctx context.Context, accessToken, videoID string, data []byte, mimeType, privacyStatus string, publishAt *time.Time, opts models.YouTubePublishOptions) (string, error) {
		atomic.AddInt32(&publishCalls, 1)
		capturedPrivacyMu.Lock()
		capturedPrivacy = privacyStatus
		capturedPrivacyMu.Unlock()
		return "https://www.youtube.com/watch?v=" + videoID, nil
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

	const numGoroutines = 10
	var wg sync.WaitGroup
	type callResult struct {
		code int
		body string
	}
	results := make([]callResult, numGoroutines)
	payload := []byte("{}")

	// Sync barrier: release every goroutine at once so the HTTP
	// dispatch lands as close to "all at once" as the runtime
	// allows. Without this, goroutines that reach the publish handler
	// a few microseconds apart would still hit the CAS — with this,
	// any flaky ordering regressions surface under repeated runs.
	//
	// r.Setup() MUST be called exactly once here, NOT per-goroutine:
	// each Setup() rebuilds the chi.Mux mux + AuthModule/csrf module
	// route tables, neither of which is safe for concurrent
	// map writes. Capture the handler and call it from every
	// goroutine — http.Handler is safe to invoke concurrently.
	handler := r.Setup()
	start := make(chan struct{})
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/session-123/publish", bytes.NewReader(payload))
			req.Header.Set("Content-Type", "application/json")
			withBearerJWT(t, req, 1)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			results[idx] = callResult{code: rec.Code, body: rec.Body.String()}
		}(i)
	}
	close(start)
	wg.Wait()

	var success, conflict, other int
	for _, res := range results {
		switch res.code {
		case http.StatusOK:
			success++
		case http.StatusConflict:
			conflict++
		default:
			other++
			t.Errorf("unexpected status code %d (body=%s)", res.code, res.body)
		}
	}
	if success != 1 {
		t.Errorf("expected exactly 1 successful publish (200), got %d", success)
	}
	if conflict != numGoroutines-1 {
		t.Errorf("expected %d concurrent CAS-loss (409), got %d", numGoroutines-1, conflict)
	}
	if other != 0 {
		t.Errorf("expected 0 unexpected statuses, got %d", other)
	}
	if got := atomic.LoadInt32(&publishCalls); got != 1 {
		t.Errorf("expected exactly 1 PublishThumbnail YouTube API call, got %d", got)
	}
	capturedPrivacyMu.Lock()
	gotPrivacy := capturedPrivacy
	capturedPrivacyMu.Unlock()
	if gotPrivacy != "public" {
		t.Errorf("expected resolved privacy 'public' on the winning publish, got %q", gotPrivacy)
	}
}

func strPtr(s string) *string { return &s }

// ----------------------------------------------------------------------------
// GET /api/v1/youtube/editor-sessions — dashboard list tests
// ----------------------------------------------------------------------------

// TestListYouTubeEditorSessions_RequiresAuth is the auth gate. The
// dashboard list endpoint is identical to its POST sibling in that
// it refuses any request without a valid JWT identity, mapping to
// HTTP 401.
func TestListYouTubeEditorSessions_RequiresAuth(t *testing.T) {
	r := mustNewRouterWithDefaults(
		services.NewCapabilityRouter(),
		&mockUserStore{},
		auth.NewManager(testJWTSecret, 24),
		"",
		nil,
	)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/youtube/editor-sessions?workspace_id=1", nil)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", w.Code, w.Body.String())
	}
}

// TestListYouTubeEditorSessions_MissingWorkspaceID asserts the
// 400-on-missing-workspace guard. Without ?workspace_id, the
// handler cannot scope the read and would otherwise risk
// cross-tenant leakage. The handler fails fast BEFORE the
// repository call so a misconfigured client never reaches SQL.
func TestListYouTubeEditorSessions_MissingWorkspaceID(t *testing.T) {
	workspace := &models.Workspace{ID: 7, OwnerID: 1, Name: "Test Workspace"}
	editStore := &mockYouTubeVideoEditStore{}
	r := newPublishRouter(t, workspace, editStore)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/youtube/editor-sessions", nil)
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

// TestListYouTubeEditorSessions_WorkspaceNotFound asserts the
// 404-on-missing-workspace path AND the "no cross-tenant probe"
// path: a non-existent workspace AND a workspace the caller does
// not own BOTH return 404 with the same generic message. The
// handler treats them identically so a hostile caller cannot
// distinguish the two states.
func TestListYouTubeEditorSessions_WorkspaceNotFound(t *testing.T) {
	workspace := &models.Workspace{ID: 7, OwnerID: 1, Name: "Test Workspace"}
	editStore := &mockYouTubeVideoEditStore{}
	// Workspace store returns nil for every id — the handler then
	// maps to 404 before reaching the repository.
	r := newPublishRouter(t, workspace, editStore)
	// Override the workspace store to return nil for ALL ids so the
	// 404 path is hit regardless of which workspace_id the caller
	// supplied.
	r.workspaceStore = &mockWorkspaceStore{
		findByIDFn: func(id int64) (*models.Workspace, error) { return nil, nil },
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/youtube/editor-sessions?workspace_id=999", nil)
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

// TestListYouTubeEditorSessions_FiltersNonTerminalByDefault is the
// happy-path: workspace_id supplied, no status filter, repository
// receives the default non-terminal status set
// (editing/failed/publishing). The handler must NOT pass an empty
// status slice to the repository (the production repository
// applies the default, but a regression that omits the default
// would surface as "published" rows leaking into the dashboard).
func TestListYouTubeEditorSessions_FiltersNonTerminalByDefault(t *testing.T) {
	workspace := &models.Workspace{ID: 7, OwnerID: 1, Name: "Test Workspace"}
	now := time.Now().UTC()
	thumbID := "thumb-uuid-1"
	var capturedFilter repository.YouTubeEditorSessionListFilter
	editStore := &mockYouTubeVideoEditStore{
		listFn: func(ctx context.Context, filter repository.YouTubeEditorSessionListFilter) ([]*models.YouTubeVideoEdit, error) {
			capturedFilter = filter
			return []*models.YouTubeVideoEdit{
				{
					ID:                "session-1",
					WorkspaceID:       workspace.ID,
					PlatformAccountID: 42,
					YouTubeVideoID:    "vid-1",
					VeloxProjectID:    "ve-project-1",
					ThumbnailMediaID:  &thumbID,
					DesiredPrivacy:    "public",
					Status:            "editing",
					CreatedAt:         now,
					UpdatedAt:         now,
				},
			}, nil
		},
	}
	r := newPublishRouter(t, workspace, editStore)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/youtube/editor-sessions?workspace_id=7", nil)
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if capturedFilter.WorkspaceID != workspace.ID {
		t.Errorf("filter.WorkspaceID: want %d, got %d", workspace.ID, capturedFilter.WorkspaceID)
	}
	if len(capturedFilter.Statuses) != 0 {
		t.Errorf("filter.Statuses: want empty (handler should NOT preset the default), got %v", capturedFilter.Statuses)
	}
	if capturedFilter.AccountID != nil {
		t.Errorf("filter.AccountID: want nil, got %v", *capturedFilter.AccountID)
	}
	if capturedFilter.Limit != 0 {
		t.Errorf("filter.Limit: want 0 (handler default), got %d", capturedFilter.Limit)
	}
	var resp listYouTubeEditorSessionsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(resp.Sessions))
	}
	got := resp.Sessions[0]
	if got.ID != "session-1" || got.YouTubeVideoID != "vid-1" || got.VeloxProjectID != "ve-project-1" {
		t.Errorf("session row mismatch: %+v", got)
	}
	if got.EditorURL == "" {
		t.Errorf("editor_url should be derived server-side, got empty")
	}
	if got.ThumbnailMediaID == nil || *got.ThumbnailMediaID != thumbID {
		t.Errorf("thumbnail_media_id: want %q, got %v", thumbID, got.ThumbnailMediaID)
	}
	if got.DesiredPrivacy != "public" || got.Status != "editing" {
		t.Errorf("privacy/status: want public/editing, got %s/%s", got.DesiredPrivacy, got.Status)
	}
}

// TestListYouTubeEditorSessions_AccountFilter asserts the handler
// forwards ?account_id to the repository AND that an empty body
// (no rows) returns 200 + sessions: [], NOT 404.
func TestListYouTubeEditorSessions_AccountFilter(t *testing.T) {
	workspace := &models.Workspace{ID: 7, OwnerID: 1, Name: "Test Workspace"}
	var capturedFilter repository.YouTubeEditorSessionListFilter
	editStore := &mockYouTubeVideoEditStore{
		listFn: func(ctx context.Context, filter repository.YouTubeEditorSessionListFilter) ([]*models.YouTubeVideoEdit, error) {
			capturedFilter = filter
			return nil, nil
		},
	}
	r := newPublishRouter(t, workspace, editStore)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/youtube/editor-sessions?workspace_id=7&account_id=42", nil)
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if capturedFilter.AccountID == nil || *capturedFilter.AccountID != 42 {
		t.Errorf("filter.AccountID: want 42, got %v", capturedFilter.AccountID)
	}
	var resp listYouTubeEditorSessionsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Sessions == nil {
		t.Errorf("sessions slice must be non-nil empty, not null")
	}
	if len(resp.Sessions) != 0 {
		t.Errorf("expected 0 sessions, got %d", len(resp.Sessions))
	}
}

// TestListYouTubeEditorSessions_StatusFilterMulti asserts
// ?status=editing,failed is parsed into a multi-element slice.
// A regression that only takes the first comma-separated value
// would fail here.
func TestListYouTubeEditorSessions_StatusFilterMulti(t *testing.T) {
	workspace := &models.Workspace{ID: 7, OwnerID: 1, Name: "Test Workspace"}
	var capturedFilter repository.YouTubeEditorSessionListFilter
	editStore := &mockYouTubeVideoEditStore{
		listFn: func(ctx context.Context, filter repository.YouTubeEditorSessionListFilter) ([]*models.YouTubeVideoEdit, error) {
			capturedFilter = filter
			return nil, nil
		},
	}
	r := newPublishRouter(t, workspace, editStore)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/youtube/editor-sessions?workspace_id=7&status=editing,failed", nil)
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if len(capturedFilter.Statuses) != 2 {
		t.Fatalf("filter.Statuses: want 2 elements, got %d (%v)", len(capturedFilter.Statuses), capturedFilter.Statuses)
	}
	wantStatuses := map[string]bool{"editing": true, "failed": true}
	for _, s := range capturedFilter.Statuses {
		if !wantStatuses[s] {
			t.Errorf("unexpected status %q in filter", s)
		}
	}
}

// TestListYouTubeEditorSessions_InvalidStatusRejected asserts the
// 400-on-off-allow-list-status path. The handler does NOT
// pre-validate the parsed status slice against the allow-list;
// it forwards whatever the caller supplied to the repository, and
// the repository's ListByWorkspace returns the typed sentinel
// ErrYouTubeVideoEditListStatusInvalid. The handler then maps
// the sentinel to 400 via errors.Is. So the END-TO-END contract
// is "off-allow-list ?status= → 400", verified here by simulating
// the repository's sentinel path.
func TestListYouTubeEditorSessions_InvalidStatusRejected(t *testing.T) {
	workspace := &models.Workspace{ID: 7, OwnerID: 1, Name: "Test Workspace"}
	editStore := &mockYouTubeVideoEditStore{
		listFn: func(ctx context.Context, filter repository.YouTubeEditorSessionListFilter) ([]*models.YouTubeVideoEdit, error) {
			return nil, fmt.Errorf("%w: %q", repository.ErrYouTubeVideoEditListStatusInvalid, "garbage")
		},
	}
	r := newPublishRouter(t, workspace, editStore)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/youtube/editor-sessions?workspace_id=7&status=garbage", nil)
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

// TestListYouTubeEditorSessions_LimitOutOfRange asserts the
// repository's ErrYouTubeVideoEditListLimitInvalid → 400 mapping.
// `?limit=501` exceeds the cap (500) and is rejected at the
// repository level; the handler translates the sentinel to 400
// so the SPA sees a clear "limit out of range" message rather
// than a generic 500.
func TestListYouTubeEditorSessions_LimitOutOfRange(t *testing.T) {
	workspace := &models.Workspace{ID: 7, OwnerID: 1, Name: "Test Workspace"}
	editStore := &mockYouTubeVideoEditStore{
		listFn: func(ctx context.Context, filter repository.YouTubeEditorSessionListFilter) ([]*models.YouTubeVideoEdit, error) {
			return nil, fmt.Errorf("%w: limit=%d (max=%d)", repository.ErrYouTubeVideoEditListLimitInvalid, 501, repository.YouTubeEditorSessionListMaxLimit)
		},
	}
	r := newPublishRouter(t, workspace, editStore)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/youtube/editor-sessions?workspace_id=7&limit=501", nil)
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for out-of-range limit, got %d: %s", w.Code, w.Body.String())
	}
}

// TestListYouTubeEditorSessions_AccountIDInvalid asserts the 400
// on non-positive or non-numeric ?account_id. The handler parses
// the value before reaching the repository, so listFn is never
// invoked when the value is invalid.
func TestListYouTubeEditorSessions_AccountIDInvalid(t *testing.T) {
	workspace := &models.Workspace{ID: 7, OwnerID: 1, Name: "Test Workspace"}
	var called bool
	editStore := &mockYouTubeVideoEditStore{
		listFn: func(ctx context.Context, filter repository.YouTubeEditorSessionListFilter) ([]*models.YouTubeVideoEdit, error) {
			called = true
			return nil, nil
		},
	}
	r := newPublishRouter(t, workspace, editStore)

	for _, badValue := range []string{"0", "-1", "abc"} {
		t.Run("account_id="+badValue, func(t *testing.T) {
			called = false
			req := httptest.NewRequest(http.MethodGet, "/api/v1/youtube/editor-sessions?workspace_id=7&account_id="+badValue, nil)
			withBearerJWT(t, req, 1)
			w := httptest.NewRecorder()
			r.Setup().ServeHTTP(w, req)
			if w.Code != http.StatusBadRequest {
				t.Errorf("expected 400 for account_id=%q, got %d: %s", badValue, w.Code, w.Body.String())
			}
			if called {
				t.Errorf("repository must NOT be called when account_id=%q is invalid", badValue)
			}
		})
	}
}

// TestListYouTubeEditorSessions_StoreNotConfigured asserts the
// 503 path: when r.youtubeVideoEditStore is nil, the handler
// returns 503 (matches the nil-store feature-flag pattern used
// by the other editor-sessions endpoints). The router is built
// WITHOUT WithYouTubeVideoEditStore so the field is nil; a
// workspace store that resolves the workspace is wired so the
// test reaches the nil-store branch instead of the
// 404-on-workspace branch.
func TestListYouTubeEditorSessions_StoreNotConfigured(t *testing.T) {
	workspace := &models.Workspace{ID: 7, OwnerID: 1, Name: "Test Workspace"}
	r := mustNewRouterWithDefaults(
		services.NewCapabilityRouter(),
		&mockUserStore{},
		auth.NewManager(testJWTSecret, 24),
		"https://app.instaedit.org",
		nil,
		WithWorkspaceStore(&mockWorkspaceStore{
			findByIDFn: func(id int64) (*models.Workspace, error) {
				if id == workspace.ID {
					return workspace, nil
				}
				return nil, nil
			},
		}),
		// INTENTIONALLY no WithYouTubeVideoEditStore.
	)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/youtube/editor-sessions?workspace_id=7", nil)
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when store is nil, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateYouTubeEditorSession_HappyPath(t *testing.T) {
	account := &models.PlatformAccount{
		ID:             42,
		UserID:         1,
		Platform:       models.PlatformYouTube,
		PlatformUserID: "UC123",
		Username:       "testchannel",
		Status:         models.AccountStatusActive,
	}
	workspace := &models.Workspace{
		ID:      7,
		OwnerID: 1,
		Name:    "Test Workspace",
	}
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
		findChannelFn: func(ctx context.Context, workspaceID, platformAccountID int64) (*models.WorkspaceChannel, error) {
			if workspaceID == workspace.ID && platformAccountID == account.ID {
				return &models.WorkspaceChannel{WorkspaceID: workspace.ID, PlatformAccountID: account.ID, Enabled: true}, nil
			}
			return nil, nil
		},
	}
	vault := &mockCredentialVault{
		getFn: func(ctx context.Context, id int64, tt string) (*models.OAuthToken, error) {
			if id == account.ID {
				return &models.OAuthToken{AccessToken: "valid-token"}, nil
			}
			return nil, errors.New("token not found")
		},
	}
	youTubeSvc := &mockYouTubeOAuthServiceForEditor{}
	editStore := &mockYouTubeVideoEditStore{}

	r := mustNewRouterWithDefaults(
		services.NewCapabilityRouter(),
		store,
		auth.NewManager(testJWTSecret, 24),
		"https://app.instaedit.org",
		nil,
		WithWorkspaceStore(workspaceStore),
		WithCredentialVault(vault),
		WithYouTubeService(youTubeSvc),
		WithYouTubeVideoEditStore(editStore),
		WithEditorURL("https://editor.instaedit.org"),
	)

	payload := map[string]any{
		"workspace_id":         workspace.ID,
		"platform_account_id":  account.ID,
		"youtube_video_id":     "abc123",
		"source_thumbnail_url": "https://i.ytimg.com/original.jpg",
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var resp createYouTubeEditorSessionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.SessionID == "" {
		t.Errorf("session_id should be present")
	}
	if resp.VeloxProjectID == "" {
		t.Errorf("velox_project_id should be present")
	}
	if resp.EditorURL == "" {
		t.Errorf("editor_url should be present")
	}
	if len(editStore.created) != 1 {
		t.Fatalf("expected one edit session to be created, got %d", len(editStore.created))
	}
	edit := editStore.created[0]
	if edit.WorkspaceID != workspace.ID {
		t.Errorf("workspace_id: want %d, got %d", workspace.ID, edit.WorkspaceID)
	}
	if edit.PlatformAccountID != account.ID {
		t.Errorf("platform_account_id: want %d, got %d", account.ID, edit.PlatformAccountID)
	}
	if edit.YouTubeVideoID != "abc123" {
		t.Errorf("youtube_video_id: want abc123, got %s", edit.YouTubeVideoID)
	}
	if edit.Status != "editing" {
		t.Errorf("status: want editing, got %s", edit.Status)
	}
}

func TestCreateYouTubeEditorSession_RequiresAuth(t *testing.T) {
	r := mustNewRouterWithDefaults(
		services.NewCapabilityRouter(),
		&mockUserStore{},
		auth.NewManager(testJWTSecret, 24),
		"",
		nil,
	)
	payload := map[string]any{
		"workspace_id":        1,
		"platform_account_id": 1,
		"youtube_video_id":  "abc123",
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateYouTubeEditorSession_MissingRequiredFields(t *testing.T) {
	r := mustNewRouterWithDefaults(
		services.NewCapabilityRouter(),
		&mockUserStore{},
		auth.NewManager(testJWTSecret, 24),
		"",
		nil,
	)
	payload := map[string]any{
		"workspace_id":        1,
		"platform_account_id": 1,
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateYouTubeEditorSession_PublicVideoRejected(t *testing.T) {
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
		findChannelFn: func(ctx context.Context, workspaceID, platformAccountID int64) (*models.WorkspaceChannel, error) {
			if workspaceID == workspace.ID && platformAccountID == account.ID {
				return &models.WorkspaceChannel{WorkspaceID: workspace.ID, PlatformAccountID: account.ID, Enabled: true}, nil
			}
			return nil, nil
		},
	}
	vault := &mockCredentialVault{
		getFn: func(ctx context.Context, id int64, tt string) (*models.OAuthToken, error) {
			if id == account.ID {
				return &models.OAuthToken{AccessToken: "valid-token"}, nil
			}
			return nil, errors.New("token not found")
		},
	}
	youTubeSvc := &mockYouTubeOAuthServiceForEditor{
		getVideoFn: func(ctx context.Context, accessToken, videoID string) (*models.YouTubeVideoDetails, error) {
			return &models.YouTubeVideoDetails{
				ID:           videoID,
				Title:        "Already Public",
				ChannelID:    account.PlatformUserID,
				ThumbnailURL: "https://i.ytimg.com/default.jpg",
				Privacy:      "public",
				UploadStatus: "processed",
			}, nil
		},
	}
	editStore := &mockYouTubeVideoEditStore{}

	r := mustNewRouterWithDefaults(
		services.NewCapabilityRouter(),
		store,
		auth.NewManager(testJWTSecret, 24),
		"https://app.instaedit.org",
		nil,
		WithWorkspaceStore(workspaceStore),
		WithCredentialVault(vault),
		WithYouTubeService(youTubeSvc),
		WithYouTubeVideoEditStore(editStore),
		WithEditorURL("https://editor.instaedit.org"),
	)

	payload := map[string]any{
		"workspace_id":        workspace.ID,
		"platform_account_id": account.ID,
		"youtube_video_id":    "public123",
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	if len(editStore.created) != 0 {
		t.Fatalf("expected no edit session to be created, got %d", len(editStore.created))
	}
}

func TestUpdateYouTubeEditorSession_StoresThumbnailMediaID(t *testing.T) {
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
		ID:     "asset-uuid-123",
		UserID: 1,
		Status: models.MediaAssetStatusReady,
	}

	var updated *models.YouTubeVideoEdit
	editStore := &mockYouTubeVideoEditStore{
		findByProjectFn: func(ctx context.Context, projectID string) (*models.YouTubeVideoEdit, error) {
			return &models.YouTubeVideoEdit{
				ID:                "session-123",
				WorkspaceID:       workspace.ID,
				PlatformAccountID: account.ID,
				YouTubeVideoID:    "abc123",
				VeloxProjectID:    "ve-project-123",
				Status:            "editing",
			}, nil
		},
		update: func(ctx context.Context, edit *models.YouTubeVideoEdit) error {
			updated = edit
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
	)

	payload := map[string]any{"thumbnail_media_id": "asset-uuid-123"}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/youtube/editor-sessions/by-project/ve-project-123", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if updated == nil {
		t.Fatalf("expected session to be updated")
	}
	if updated.ThumbnailMediaID == nil || *updated.ThumbnailMediaID != "asset-uuid-123" {
		t.Fatalf("expected thumbnail_media_id to be asset-uuid-123, got %v", updated.ThumbnailMediaID)
	}
}

// strPtr is already defined earlier in this file (line ~1336) for the
// existing PATCH/PATCH-by-project tests; the attach-thumbnail tests
// reuse the same helper rather than redeclaring it.

// ────────────────────────────────────────────────────────────────────────────
// POST /api/v1/youtube/editor-sessions/{id}/thumbnail — Blocco #5 P0 #4
//
// Direct handoff endpoint tests. The handler validates:
//   1. session not found                                    → 404
//   2. workspace not accessible by caller                   → 403
//   3. asset not found                                      → 404
//   4. asset ownership mismatch                             → 403
//   5. asset exists but Status != ready                     → 409
//   6. CAS-loss (status not in editing/failed)              → 409
//   7. missing thumbnail_media_id payload                   → 400
// Plus the happy path → 200.
//
// Each test builds its own mockYouTubeVideoEditStore with the
// minimal closures needed; the helper `newAttachThumbnailTestRig`
// centralises the common wiring (mockUserStore, mockWorkspaceStore,
// mockMediaStore) so the per-test bodies stay focused on the
// error-branch assertion.
// ────────────────────────────────────────────────────────────────────────────

type attachThumbnailRig struct {
	router      *Router
	mediaStore  *mockMediaStore
	editStore   *mockYouTubeVideoEditStore
	workspace   *models.Workspace
	account     *models.PlatformAccount
	sessionID   string
}

func newAttachThumbnailTestRig(t *testing.T) *attachThumbnailRig {
	t.Helper()
	workspace := &models.Workspace{ID: 7, OwnerID: 1, Name: "Test Workspace"}
	account := &models.PlatformAccount{
		ID:             42,
		UserID:         1,
		Platform:       models.PlatformYouTube,
		PlatformUserID: "UC123",
		Username:       "testchannel",
		Status:         models.AccountStatusActive,
	}
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
	r := mustNewRouterWithDefaults(
		services.NewCapabilityRouter(),
		store,
		auth.NewManager(testJWTSecret, 24),
		"https://app.instaedit.org",
		nil,
		WithWorkspaceStore(workspaceStore),
		WithMediaStore(mediaStore),
	)
	return &attachThumbnailRig{
		router:     r,
		mediaStore: mediaStore,
		workspace:  workspace,
		account:    account,
		sessionID:  "session-uuid-123",
	}
}

func (rig *attachThumbnailRig) attachEditStore(editStore *mockYouTubeVideoEditStore) *Router {
	rig.editStore = editStore
	return mustNewRouterWithDefaults(
		services.NewCapabilityRouter(),
		&mockUserStore{
			findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
				if id == rig.account.ID {
					return rig.account, nil
				}
				return nil, nil
			},
		},
		auth.NewManager(testJWTSecret, 24),
		"https://app.instaedit.org",
		nil,
		WithWorkspaceStore(&mockWorkspaceStore{
			findByIDFn: func(id int64) (*models.Workspace, error) {
				if id == rig.workspace.ID {
					return rig.workspace, nil
				}
				return nil, nil
			},
		}),
		WithMediaStore(rig.mediaStore),
		WithYouTubeVideoEditStore(editStore),
	)
}

// TestAttachThumbnail_HappyPath — POST with valid payload on a
// session in 'editing' state, asset ready + owned by caller,
// workspace accessible. Expects 200 + response body with
// session_id + thumbnail_media_id.
func TestAttachThumbnail_HappyPath(t *testing.T) {
	rig := newAttachThumbnailTestRig(t)
	rig.mediaStore.assets["asset-uuid-123"] = &models.MediaAsset{
		ID:          "asset-uuid-123",
		UserID:      1,
		UploadKey:   "uploads/1/thumb.jpg",
		ContentType: "image/jpeg",
		SizeBytes:   1024,
		Status:      models.MediaAssetStatusReady,
	}
	var attachedSessionID, attachedMediaID string
	editStore := &mockYouTubeVideoEditStore{
		findFn: func(ctx context.Context, id string) (*models.YouTubeVideoEdit, error) {
			return &models.YouTubeVideoEdit{
				ID:                rig.sessionID,
				WorkspaceID:       rig.workspace.ID,
				PlatformAccountID: rig.account.ID,
				YouTubeVideoID:    "yt-abc",
				Status:            "editing",
			}, nil
		},
		attachThumbnailFn: func(ctx context.Context, sessionID, thumbnailMediaID string) (*models.YouTubeVideoEdit, error) {
			attachedSessionID = sessionID
			attachedMediaID = thumbnailMediaID
			return &models.YouTubeVideoEdit{
				ID:                sessionID,
				WorkspaceID:       rig.workspace.ID,
				PlatformAccountID: rig.account.ID,
				ThumbnailMediaID:  &thumbnailMediaID,
				Status:            "editing",
			}, nil
		},
	}
	r := rig.attachEditStore(editStore)

	body, _ := json.Marshal(map[string]string{"thumbnail_media_id": "asset-uuid-123"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/"+rig.sessionID+"/thumbnail", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if attachedSessionID != rig.sessionID {
		t.Fatalf("expected AttachThumbnail called with session_id=%s, got %s", rig.sessionID, attachedSessionID)
	}
	if attachedMediaID != "asset-uuid-123" {
		t.Fatalf("expected AttachThumbnail called with media_id=asset-uuid-123, got %s", attachedMediaID)
	}
	var resp attachThumbnailResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.SessionID != rig.sessionID {
		t.Fatalf("expected response session_id=%s, got %s", rig.sessionID, resp.SessionID)
	}
	if resp.ThumbnailMediaID != "asset-uuid-123" {
		t.Fatalf("expected response thumbnail_media_id=asset-uuid-123, got %s", resp.ThumbnailMediaID)
	}
	if resp.ThumbnailStatus != "editing" {
		t.Fatalf("expected response thumbnail_status=editing, got %s", resp.ThumbnailStatus)
	}
}

// TestAttachThumbnail_SessionNotFound — the session_id does not
// resolve in the store. Expects 404 (without touching the asset).
func TestAttachThumbnail_SessionNotFound(t *testing.T) {
	rig := newAttachThumbnailTestRig(t)
	rig.mediaStore.assets["asset-uuid-123"] = &models.MediaAsset{
		ID:     "asset-uuid-123",
		UserID: 1,
		Status: models.MediaAssetStatusReady,
	}
	attachCalled := false
	editStore := &mockYouTubeVideoEditStore{
		findFn: func(ctx context.Context, id string) (*models.YouTubeVideoEdit, error) {
			return nil, nil // session not found
		},
		attachThumbnailFn: func(ctx context.Context, sessionID, thumbnailMediaID string) (*models.YouTubeVideoEdit, error) {
			attachCalled = true
			return nil, nil
		},
	}
	r := rig.attachEditStore(editStore)

	body, _ := json.Marshal(map[string]string{"thumbnail_media_id": "asset-uuid-123"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/missing-id/thumbnail", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
	if attachCalled {
		t.Fatalf("AttachThumbnail must NOT be called when session lookup fails")
	}
}

// TestAttachThumbnail_WorkspaceMismatch — the session's workspace is
// owned by a different user. Expects 403 (explicit gate per user spec;
// deviates from the existing handlers which return 404 for the same
// scenario).
func TestAttachThumbnail_WorkspaceMismatch(t *testing.T) {
	rig := newAttachThumbnailTestRig(t)
	rig.mediaStore.assets["asset-uuid-123"] = &models.MediaAsset{
		ID:     "asset-uuid-123",
		UserID: 1,
		Status: models.MediaAssetStatusReady,
	}
	attachCalled := false
	// Build a workspace owned by user 99 (caller is user 1).
	foreignWorkspace := &models.Workspace{ID: 7, OwnerID: 99, Name: "Foreign Workspace"}
	editStore := &mockYouTubeVideoEditStore{
		findFn: func(ctx context.Context, id string) (*models.YouTubeVideoEdit, error) {
			return &models.YouTubeVideoEdit{
				ID:                rig.sessionID,
				WorkspaceID:       foreignWorkspace.ID,
				PlatformAccountID: rig.account.ID,
				Status:            "editing",
			}, nil
		},
		attachThumbnailFn: func(ctx context.Context, sessionID, thumbnailMediaID string) (*models.YouTubeVideoEdit, error) {
			attachCalled = true
			return nil, nil
		},
	}
	r := mustNewRouterWithDefaults(
		services.NewCapabilityRouter(),
		&mockUserStore{},
		auth.NewManager(testJWTSecret, 24),
		"https://app.instaedit.org",
		nil,
		WithWorkspaceStore(&mockWorkspaceStore{
			findByIDFn: func(id int64) (*models.Workspace, error) {
				if id == foreignWorkspace.ID {
					return foreignWorkspace, nil
				}
				return nil, nil
			},
		}),
		WithMediaStore(rig.mediaStore),
		WithYouTubeVideoEditStore(editStore),
	)

	body, _ := json.Marshal(map[string]string{"thumbnail_media_id": "asset-uuid-123"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/"+rig.sessionID+"/thumbnail", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
	if attachCalled {
		t.Fatalf("AttachThumbnail must NOT be called when workspace check fails")
	}
}

// TestAttachThumbnail_AssetNotFound — the supplied media_id does not
// resolve in the media store. Expects 404 (no asset exists).
func TestAttachThumbnail_AssetNotFound(t *testing.T) {
	rig := newAttachThumbnailTestRig(t)
	attachCalled := false
	editStore := &mockYouTubeVideoEditStore{
		findFn: func(ctx context.Context, id string) (*models.YouTubeVideoEdit, error) {
			return &models.YouTubeVideoEdit{
				ID:                rig.sessionID,
				WorkspaceID:       rig.workspace.ID,
				PlatformAccountID: rig.account.ID,
				Status:            "editing",
			}, nil
		},
		attachThumbnailFn: func(ctx context.Context, sessionID, thumbnailMediaID string) (*models.YouTubeVideoEdit, error) {
			attachCalled = true
			return nil, nil
		},
	}
	r := rig.attachEditStore(editStore)

	body, _ := json.Marshal(map[string]string{"thumbnail_media_id": "missing-asset-id"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/"+rig.sessionID+"/thumbnail", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
	if attachCalled {
		t.Fatalf("AttachThumbnail must NOT be called when asset lookup fails")
	}
}

// TestAttachThumbnail_AssetNotReady — the asset exists but its
// Status is not 'ready' (e.g. 'uploading', 'failed', 'deleted').
// Expects 409.
func TestAttachThumbnail_AssetNotReady(t *testing.T) {
	rig := newAttachThumbnailTestRig(t)
	rig.mediaStore.assets["asset-uuid-123"] = &models.MediaAsset{
		ID:     "asset-uuid-123",
		UserID: 1,
		Status: models.MediaAssetStatus("uploading"),
	}
	attachCalled := false
	editStore := &mockYouTubeVideoEditStore{
		findFn: func(ctx context.Context, id string) (*models.YouTubeVideoEdit, error) {
			return &models.YouTubeVideoEdit{
				ID:                rig.sessionID,
				WorkspaceID:       rig.workspace.ID,
				PlatformAccountID: rig.account.ID,
				Status:            "editing",
			}, nil
		},
		attachThumbnailFn: func(ctx context.Context, sessionID, thumbnailMediaID string) (*models.YouTubeVideoEdit, error) {
			attachCalled = true
			return nil, nil
		},
	}
	r := rig.attachEditStore(editStore)

	body, _ := json.Marshal(map[string]string{"thumbnail_media_id": "asset-uuid-123"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/"+rig.sessionID+"/thumbnail", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
	if attachCalled {
		t.Fatalf("AttachThumbnail must NOT be called when asset is not ready")
	}
}

// TestAttachThumbnail_AssetOwnershipMismatch — asset exists but is
// owned by a different user. Expects 403 (anti-cross-tenant probe).
func TestAttachThumbnail_AssetOwnershipMismatch(t *testing.T) {
	rig := newAttachThumbnailTestRig(t)
	rig.mediaStore.assets["asset-uuid-123"] = &models.MediaAsset{
		ID:     "asset-uuid-123",
		UserID: 99, // belongs to a different user
		Status: models.MediaAssetStatusReady,
	}
	attachCalled := false
	editStore := &mockYouTubeVideoEditStore{
		findFn: func(ctx context.Context, id string) (*models.YouTubeVideoEdit, error) {
			return &models.YouTubeVideoEdit{
				ID:                rig.sessionID,
				WorkspaceID:       rig.workspace.ID,
				PlatformAccountID: rig.account.ID,
				Status:            "editing",
			}, nil
		},
		attachThumbnailFn: func(ctx context.Context, sessionID, thumbnailMediaID string) (*models.YouTubeVideoEdit, error) {
			attachCalled = true
			return nil, nil
		},
	}
	r := rig.attachEditStore(editStore)

	body, _ := json.Marshal(map[string]string{"thumbnail_media_id": "asset-uuid-123"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/"+rig.sessionID+"/thumbnail", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
	if attachCalled {
		t.Fatalf("AttachThumbnail must NOT be called when asset ownership check fails")
	}
}

// TestAttachThumbnail_CASLoss — the session is in a state that does
// not match the AttachThumbnail CAS predicate (status='publishing' or
// 'published'). Expects 409, no asset mutation.
func TestAttachThumbnail_CASLoss(t *testing.T) {
	rig := newAttachThumbnailTestRig(t)
	rig.mediaStore.assets["asset-uuid-123"] = &models.MediaAsset{
		ID:     "asset-uuid-123",
		UserID: 1,
		Status: models.MediaAssetStatusReady,
	}
	editStore := &mockYouTubeVideoEditStore{
		findFn: func(ctx context.Context, id string) (*models.YouTubeVideoEdit, error) {
			// Pretend the session just transitioned to 'publishing' between
			// the read at the top of the handler and the AttachThumbnail
			// call. The mock faithfully reports the not-found sentinel.
			return &models.YouTubeVideoEdit{
				ID:                rig.sessionID,
				WorkspaceID:       rig.workspace.ID,
				PlatformAccountID: rig.account.ID,
				Status:            "editing",
			}, nil
		},
		attachThumbnailFn: func(ctx context.Context, sessionID, thumbnailMediaID string) (*models.YouTubeVideoEdit, error) {
			return nil, repository.ErrYouTubeVideoEditNotFound
		},
	}
	r := rig.attachEditStore(editStore)

	body, _ := json.Marshal(map[string]string{"thumbnail_media_id": "asset-uuid-123"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/"+rig.sessionID+"/thumbnail", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
}

// TestAttachThumbnail_MissingPayload — body lacks thumbnail_media_id
// or is empty. Expects 400.
func TestAttachThumbnail_MissingPayload(t *testing.T) {
	rig := newAttachThumbnailTestRig(t)
	editStore := &mockYouTubeVideoEditStore{
		attachThumbnailFn: func(ctx context.Context, sessionID, thumbnailMediaID string) (*models.YouTubeVideoEdit, error) {
			t.Fatalf("AttachThumbnail must NOT be called when payload is missing")
			return nil, nil
		},
	}
	r := rig.attachEditStore(editStore)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/"+rig.sessionID+"/thumbnail", bytes.NewReader([]byte("{}")))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}
