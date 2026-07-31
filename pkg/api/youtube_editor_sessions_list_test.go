package api

// List handler tests.
import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

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
