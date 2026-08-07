package api

// Shared test doubles and setup helpers.
import (
	"context"
	"errors"
	"fmt"
	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
	"io"
	"sync"
	"testing"
	"time"
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
	// simulatedStatus tracks the current lifecycle status across
	// MarkPublishing / MarkPublishedWithActualPrivacy calls so the
	// two CAS simulators stay in sync without relying on findFn
	// returning a mutated row (findFn creates fresh structs each call).
	simulatedStatus   string
	attachThumbnailFn func(ctx context.Context, sessionID, thumbnailMediaID string) (*models.YouTubeVideoEdit, error)
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
	// saveDraftFn (P2 Dark Editor auto-save) is the CAS simulator
	// for draft persistence. Default returns nil (success).
	saveDraftFn func(ctx context.Context, id string, title string, description string, tags []string, defaultLanguage string, defaultAudioLanguage string, translations map[string]models.YouTubeTranslation, desiredPrivacy string, publishAt *time.Time, draftUpdatedAt time.Time) error
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
		m.simulatedStatus = "publishing"
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
	// Use simulatedStatus (set by MarkPublishing) instead of
	// re-calling findFn, which always returns a fresh "editing" row.
	if m.simulatedStatus != "publishing" {
		if m.findFn == nil {
			return nil, fmt.Errorf("markPublishedWithActualPrivacy fallback: no findFn")
		}
		row, err := m.findFn(ctx, id)
		if err != nil || row == nil {
			return nil, fmt.Errorf("markPublishedWithActualPrivacy fallback: find returned nil: %w", err)
		}
		return nil, fmt.Errorf("%w: simulated CAS-loss (status=%s)", repository.ErrYouTubeVideoEditNotFound, row.Status)
	}
	m.simulatedStatus = "published"
	if m.findFn == nil {
		return nil, errors.New("markPublishedWithActualPrivacy fallback: no findFn to bootstrap published row")
	}
	row, err := m.findFn(ctx, id)
	if err != nil || row == nil {
		return nil, fmt.Errorf("markPublishedWithActualPrivacy fallback: find returned nil: %w", err)
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

// SaveDraft (P2 — Dark Editor auto-save) routes to saveDraftFn when
// supplied; default returns nil (success).
func (m *mockYouTubeVideoEditStore) SaveDraft(ctx context.Context, id string, title string, description string, tags []string, defaultLanguage string, defaultAudioLanguage string, translations map[string]models.YouTubeTranslation, desiredPrivacy string, publishAt *time.Time, draftUpdatedAt time.Time) error {
	if m.saveDraftFn != nil {
		return m.saveDraftFn(ctx, id, title, description, tags, defaultLanguage, defaultAudioLanguage, translations, desiredPrivacy, publishAt, draftUpdatedAt)
	}
	return nil
}

// mockYouTubeOAuthServiceForEditor implements the subset of
// YouTubeOAuthService needed by the editor session handler.
type mockYouTubeOAuthServiceForEditor struct {
	getVideoFn            func(ctx context.Context, accessToken, videoID string) (*models.YouTubeVideoDetails, error)
	publishThumbnailFn    func(ctx context.Context, accessToken, videoID string, thumbnailData []byte, mimeType, privacyStatus string, publishAt *time.Time, opts models.YouTubePublishOptions) (string, error)
	upsertLocalizationsFn func(ctx context.Context, accessToken, videoID, lang string, tr models.YouTubeTranslation) error
	listEditableVideosFn  func(ctx context.Context, accessToken, channelID, pageToken string) (*services.YouTubeVideoPage, error)
	// validateChannelBindingFn (P0 — batch cross-channel guard) lets
	// tests simulate the grant→channel binding probe. The default is
	// fail-loud ("not implemented") so any test that exercises a path
	// reaching ValidateChannelBinding must explicitly opt in — a
	// silent nil success could mask guard gaps in future tests.
	validateChannelBindingFn func(ctx context.Context, accessToken, expectedChannelID string) error
}

func (m *mockYouTubeOAuthServiceForEditor) RefreshOAuthToken(ctx context.Context, refreshToken string) (*models.TokenData, error) {
	return nil, errors.New("not implemented")
}

func (m *mockYouTubeOAuthServiceForEditor) GetTokenInfo(ctx context.Context, accessToken string) (*services.YouTubeTokenInfo, error) {
	return nil, errors.New("not implemented")
}

func (m *mockYouTubeOAuthServiceForEditor) ValidateChannelBinding(ctx context.Context, accessToken, expectedChannelID string) error {
	if m.validateChannelBindingFn != nil {
		return m.validateChannelBindingFn(ctx, accessToken, expectedChannelID)
	}
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
			WithEditorURL("https://editor.instaedit.test"),
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

func strPtr(s string) *string { return &s }

type attachThumbnailRig struct {
	router     *Router
	mediaStore *mockMediaStore
	editStore  *mockYouTubeVideoEditStore
	workspace  *models.Workspace
	account    *models.PlatformAccount
	sessionID  string
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
