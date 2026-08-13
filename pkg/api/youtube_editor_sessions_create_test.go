package api

// Create handler tests.
import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
	"net/http"
	"net/http/httptest"
	"testing"
)

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

	var capturedSession *models.YouTubeVideoEdit
	editStore.findOrCreateFn = func(ctx context.Context, workspaceID, platformAccountID int64, youtubeVideoID, sessionIDHint, projectIDHint string) (*models.YouTubeVideoEdit, error) {
		capturedSession = &models.YouTubeVideoEdit{
			ID:                sessionIDHint,
			WorkspaceID:       workspaceID,
			PlatformAccountID: platformAccountID,
			YouTubeVideoID:    youtubeVideoID,
			VeloxProjectID:    projectIDHint,
			Status:            "editing",
		}
		editStore.created = append(editStore.created, capturedSession)
		return capturedSession, nil
	}

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
	// The session contract carries the authoritative video projection
	// (videos.list) — InstaEditor's initial document.
	if resp.YouTubeVideoID != "abc123" {
		t.Errorf("youtube_video_id: want abc123, got %q", resp.YouTubeVideoID)
	}
	if resp.Title != "Test Video" {
		t.Errorf("title: want Test Video, got %q", resp.Title)
	}
	if resp.ThumbnailURL != "https://i.ytimg.com/default.jpg" {
		t.Errorf("thumbnail_url: got %q", resp.ThumbnailURL)
	}
	if resp.PrivacyStatus != "private" {
		t.Errorf("privacy_status: want private, got %q", resp.PrivacyStatus)
	}
	if resp.Source != "youtube" {
		t.Errorf("source: want youtube, got %q", resp.Source)
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

// TestCreateYouTubeEditorSession_PersistsCategory verifies the
// authoritative snippet categoryId from videos.list is persisted on the
// session row (extended contract) when YouTube returns one.
func TestCreateYouTubeEditorSession_PersistsCategory(t *testing.T) {
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
				Title:        "Test Video",
				ChannelID:    account.PlatformUserID,
				ThumbnailURL: "https://i.ytimg.com/default.jpg",
				CategoryID:   "24",
				Privacy:      "private",
				UploadStatus: "processed",
			}, nil
		},
	}
	editStore := &mockYouTubeVideoEditStore{}
	var persisted *models.YouTubeVideoEdit
	editStore.findOrCreateFn = func(ctx context.Context, workspaceID, platformAccountID int64, youtubeVideoID, sessionIDHint, projectIDHint string) (*models.YouTubeVideoEdit, error) {
		persisted = &models.YouTubeVideoEdit{
			ID:                sessionIDHint,
			WorkspaceID:       workspaceID,
			PlatformAccountID: platformAccountID,
			YouTubeVideoID:    youtubeVideoID,
			VeloxProjectID:    projectIDHint,
			Status:            "editing",
		}
		editStore.created = append(editStore.created, persisted)
		return persisted, nil
	}

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
		"youtube_video_id":    "abc123",
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
	if persisted == nil {
		t.Fatal("expected the persisted session row to be captured")
	}
	if persisted.CategoryID != "24" {
		t.Fatalf("expected category_id=24 persisted on the session row, got %q", persisted.CategoryID)
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
		"youtube_video_id":    "abc123",
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
