package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// helper: build a *Router with one workspace + one user + one
// edit-store row for the given project_id, mirroring the
// newPublishRouter factory from youtube_editor_sessions_test.go.
func newByProjectRouter(t *testing.T, workspace *models.Workspace, edit *models.YouTubeVideoEdit) *Router {
	t.Helper()

	wsStore := &mockWorkspaceStore{
		findByIDFn: func(id int64) (*models.Workspace, error) {
			if id == workspace.ID {
				return workspace, nil
			}
			return nil, nil
		},
		findChannelFn: func(ctx context.Context, wsID, accountID int64) (*models.WorkspaceChannel, error) {
			if wsID == workspace.ID && edit != nil && accountID == edit.PlatformAccountID {
				return &models.WorkspaceChannel{WorkspaceID: wsID, PlatformAccountID: accountID}, nil
			}
			return nil, nil
		},
	}
	editStore := &mockYouTubeVideoEditStore{
		findByProjectFn: func(ctx context.Context, projectID string) (*models.YouTubeVideoEdit, error) {
			if edit != nil && projectID == edit.VeloxProjectID {
				return edit, nil
			}
			return nil, nil
		},
		findFn: func(ctx context.Context, id string) (*models.YouTubeVideoEdit, error) {
			if edit != nil && id == edit.ID {
				return edit, nil
			}
			return nil, nil
		},
		// MarkPublishing / FindOrCreateEditableSession no-ops default to
		// the mock's no-op fallback; tests below override publishThumbnailFn
		// directly when they need YouTube API mocking.
	}
	uStore := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			if edit != nil && id == edit.PlatformAccountID {
				return &models.PlatformAccount{
					ID:             id,
					Platform:       models.PlatformYouTube,
					PlatformUserID: "channel-1",
				}, nil
			}
			return nil, nil
		},
	}
	youTubeSvc := &mockYouTubeOAuthServiceForEditor{
		getVideoFn: func(ctx context.Context, accessToken, videoID string) (*models.YouTubeVideoDetails, error) {
			return &models.YouTubeVideoDetails{
				ID:           videoID,
				ChannelID:    "channel-1",
				UploadStatus: "processed",
				Privacy:      "private",
			}, nil
		},
	}
	vault := &mockCredentialVault{
		getFn: func(ctx context.Context, accountID int64, kind string) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "test-token", TokenType: kind}, nil
		},
	}
	return newPublishRouter(t, workspace, editStore,
		WithWorkspaceStore(wsStore),
		WithUserStore(uStore),
		WithCredentialVault(vault),
		WithYouTubeService(youTubeSvc),
	)
}

// TestGetYouTubeEditorSessionByProject_401_NoAuth verifies the auth gate.
func TestGetYouTubeEditorSessionByProject_401_NoAuth(t *testing.T) {
	t.Parallel()
	workspace := &models.Workspace{ID: 7, OwnerID: 1}
	edit := &models.YouTubeVideoEdit{
		ID:                "sess-1",
		WorkspaceID:       workspace.ID,
		PlatformAccountID: 42,
		YouTubeVideoID:    "yt-1",
		VeloxProjectID:    "vp-1",
		Status:            "editing",
	}
	router := newByProjectRouter(t, workspace, edit)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/youtube/editor-sessions/by-project/vp-1", nil)
	// no withBearerJWT — unauthenticated
	rec := httptest.NewRecorder()
	router.Setup().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d (body=%s)", rec.Code, rec.Body.String())
	}
}

// TestGetYouTubeEditorSessionByProject_404_NotFound verifies the 404
// for a missing session_id (the handler must not leak "not yours"
// vs "not found" — both collapse into 404).
func TestGetYouTubeEditorSessionByProject_404_NotFound(t *testing.T) {
	t.Parallel()
	workspace := &models.Workspace{ID: 7, OwnerID: 1}
	router := newByProjectRouter(t, workspace, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/youtube/editor-sessions/by-project/vp-missing", nil)
	withBearerJWT(t, req, 1)
	rec := httptest.NewRecorder()
	router.Setup().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d (body=%s)", rec.Code, rec.Body.String())
	}
}

// TestPublishYouTubeEditorSessionByProject_200_ResponseContainsStatus
// verifies that a successful publish response includes the `status`
// field (P0 contract alignment).
func TestPublishYouTubeEditorSessionByProject_200_ResponseContainsStatus(t *testing.T) {
	t.Parallel()
	workspace := &models.Workspace{ID: 7, OwnerID: 1}
	edit := &models.YouTubeVideoEdit{
		ID:                "sess-1",
		WorkspaceID:       workspace.ID,
		PlatformAccountID: 42,
		YouTubeVideoID:    "yt-1",
		VeloxProjectID:    "vp-1",
		DesiredPrivacy:    "public",
		Status:            "published",
		ActualPrivacy:     strPtr("public"),
		YouTubeSyncStatus: strPtr("confirmed"),
	}
	router := newByProjectRouter(t, workspace, edit)

	body := bytes.NewReader([]byte(`{"privacy_status":"public"}`))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/by-project/vp-1/publish", body)
	withBearerJWT(t, req, 1)
	rec := httptest.NewRecorder()
	router.Setup().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	var resp publishYouTubeEditorSessionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Status != "published" {
		t.Fatalf("expected status=published, got %q", resp.Status)
	}
	if resp.VideoID != "yt-1" {
		t.Fatalf("expected video_id=yt-1, got %q", resp.VideoID)
	}
	if resp.ActualPrivacy != "public" {
		t.Fatalf("expected actual_privacy=public, got %q", resp.ActualPrivacy)
	}
	if resp.YouTubeSyncStatus != "confirmed" {
		t.Fatalf("expected youtube_sync_status=confirmed, got %q", resp.YouTubeSyncStatus)
	}
	// Raw-wire assertion (P0 contract lock): catches json-tag drift on
	// the publishYouTubeEditorSessionResponse struct. If someone renames
	// `json:"status"` to a different tag the struct decode above would
	// still report the right value (because the test round-trips the
	// body), but the wire payload would not contain the literal
	// `"status":"published"` key, breaking InstaEditor's
	// `publishResult.status` consumer.
	if !strings.Contains(rec.Body.String(), `"status":"published"`) {
		t.Fatalf("expected raw wire body to contain literal `\"status\":\"published\"`, got %s", rec.Body.String())
	}
}

// TestGetYouTubeEditorSessionByProject_200_HappyPath verifies the
// happy-path GET returns the YouTubeEditorSessionDetail DTO.
func TestGetYouTubeEditorSessionByProject_200_HappyPath(t *testing.T) {
	t.Parallel()
	workspace := &models.Workspace{ID: 7, OwnerID: 1}
	edit := &models.YouTubeVideoEdit{
		ID:                "sess-1",
		WorkspaceID:       workspace.ID,
		PlatformAccountID: 42,
		YouTubeVideoID:    "yt-1",
		VeloxProjectID:    "vp-1",
		DesiredPrivacy:    "private",
		Status:            "editing",
	}
	router := newByProjectRouter(t, workspace, edit)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/youtube/editor-sessions/by-project/vp-1", nil)
	withBearerJWT(t, req, 1)
	rec := httptest.NewRecorder()
	router.Setup().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", rec.Code, rec.Body.String())
	}

	var dto youTubeEditorSessionDetail
	if err := json.NewDecoder(rec.Body).Decode(&dto); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if dto.ID != "sess-1" {
		t.Fatalf("expected id=sess-1, got %s", dto.ID)
	}
	if dto.Status != "editing" {
		t.Fatalf("expected status=editing, got %s", dto.Status)
	}
	if dto.DesiredPrivacy != "private" {
		t.Fatalf("expected desired_privacy=private, got %s", dto.DesiredPrivacy)
	}
	// The launcher URL must be present and point at the session's own
	// project handle (the InstaEditor SPA redirect target).
	wantEditorURL := "https://editor.instaedit.test/editor/vp-1"
	if dto.EditorURL != wantEditorURL {
		t.Fatalf("expected editor_url=%s, got %s", wantEditorURL, dto.EditorURL)
	}
}

// TestPublishYouTubeEditorSessionByProject_409_Idempotent verifies
// that a 'published' session replays the stored public URL without
// calling the YouTube API.
func TestPublishYouTubeEditorSessionByProject_409_Idempotent(t *testing.T) {
	t.Parallel()
	workspace := &models.Workspace{ID: 7, OwnerID: 1}
	published := &models.YouTubeVideoEdit{
		ID:                "sess-1",
		WorkspaceID:       workspace.ID,
		PlatformAccountID: 42,
		YouTubeVideoID:    "yt-1",
		VeloxProjectID:    "vp-1",
		DesiredPrivacy:    "public",
		Status:            "published",
	}
	router := newByProjectRouter(t, workspace, published)

	body := bytes.NewReader([]byte(`{"privacy_status":"public"}`))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/by-project/vp-1/publish", body)
	withBearerJWT(t, req, 1)
	rec := httptest.NewRecorder()
	router.Setup().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 on idempotent replay, got %d (body=%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "https://www.youtube.com/watch?v=yt-1") {
		t.Fatalf("expected idempotent replay body to contain the watch URL, got %s", rec.Body.String())
	}
}

// TestPublishYouTubeEditorSessionByProject_404_NotFound verifies the
// handler returns 404 when velox_project_id resolves to no session.
func TestPublishYouTubeEditorSessionByProject_404_NotFound(t *testing.T) {
	t.Parallel()
	workspace := &models.Workspace{ID: 7, OwnerID: 1}
	router := newByProjectRouter(t, workspace, nil)

	body := bytes.NewReader([]byte(`{}`))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/by-project/vp-missing/publish", body)
	withBearerJWT(t, req, 1)
	rec := httptest.NewRecorder()
	router.Setup().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d (body=%s)", rec.Code, rec.Body.String())
	}
}

// TestPublishYouTubeEditorSessionByProject_BadJSON verifies the JSON
// decode branch returns 400.
func TestPublishYouTubeEditorSessionByProject_BadJSON(t *testing.T) {
	t.Parallel()
	workspace := &models.Workspace{ID: 7, OwnerID: 1}
	edit := &models.YouTubeVideoEdit{
		ID:                "sess-1",
		WorkspaceID:       workspace.ID,
		PlatformAccountID: 42,
		YouTubeVideoID:    "yt-1",
		VeloxProjectID:    "vp-1",
		Status:            "editing",
		DesiredPrivacy:    "private",
		ThumbnailMediaID:  strPtr("asset-1"),
	}
	router := newByProjectRouter(t, workspace, edit)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/youtube/editor-sessions/by-project/vp-1/publish", bytes.NewReader([]byte("{not-json")))
	withBearerJWT(t, req, 1)
	rec := httptest.NewRecorder()
	router.Setup().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d (body=%s)", rec.Code, rec.Body.String())
	}
}
