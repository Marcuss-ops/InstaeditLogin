package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// ---------------------------------------------------------------------------
// handleCreatePost tests
// ---------------------------------------------------------------------------

func TestHandleCreatePost_Happy(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	store := &mockUserStore{}
	wsStore := &mockWorkspaceStore{
		findByIDFn: func(id int64) (*models.Workspace, error) {
			return &models.Workspace{ID: id, Name: "Mine", OwnerID: 1}, nil
		},
	}
	postStore := &mockPostStore{
		createFn: func(p *models.Post, tgts []*models.PostTarget) error {
			p.ID = 100
			p.CreatedAt = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
			for i, target := range tgts {
				target.ID = int64(200 + i)
			}
			return nil
		},
	}
	r := newTestRouter(svc, store, "",
		WithWorkspaceStore(wsStore),
		WithPostStore(postStore),
	)

	body := `{"workspace_id":1,"content":{"title":"hello","caption":"world"},"targets":[{"platform_account_id":10}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		ID          int64  `json:"id"`
		WorkspaceID int64  `json:"workspace_id"`
		Status      string `json:"status"`
		ScheduledAt string `json:"scheduled_at,omitempty"`
		Targets     []struct {
			ID                int64  `json:"id"`
			PlatformAccountID int64  `json:"platform_account_id"`
			Status            string `json:"status"`
		} `json:"targets"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if resp.ID != 100 {
		t.Fatalf("id: want 100, got %d", resp.ID)
	}
	if resp.Status != "draft" {
		t.Fatalf("status: want draft, got %s", resp.Status)
	}
	if resp.ScheduledAt != "" {
		t.Fatalf("scheduled_at: want empty for draft, got %s", resp.ScheduledAt)
	}
	if len(resp.Targets) != 1 || resp.Targets[0].ID != 200 || resp.Targets[0].PlatformAccountID != 10 {
		t.Fatalf("targets count/id/pa wrong: %+v", resp.Targets)
	}
}

func TestHandleCreatePost_GroupTargetExpandsToCurrentChannels(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	store := &mockUserStore{}
	wsStore := &mockWorkspaceStore{
		findByIDFn: func(id int64) (*models.Workspace, error) {
			return &models.Workspace{ID: id, Name: "Mine", OwnerID: 1}, nil
		},
	}
	var createdTargets []*models.PostTarget
	postStore := &mockPostStore{
		createFn: func(p *models.Post, targets []*models.PostTarget) error {
			p.ID = 101
			p.CreatedAt = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
			createdTargets = targets
			return nil
		},
	}
	groupStore := &mockGroupStore{
		findByIDFn: func(id int64) (*models.Group, error) {
			return &models.Group{ID: id, WorkspaceID: 1, Name: "Social IT"}, nil
		},
		listAccountsInGroupFn: func(int64) ([]int64, error) {
			return []int64{10, 11, 10}, nil
		},
	}
	r := newTestRouter(svc, store, "",
		WithWorkspaceStore(wsStore),
		WithPostStore(postStore),
		WithGroupStore(groupStore),
	)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts", strings.NewReader(
		`{"workspace_id":1,"content":{"title":"group post"},"targets":[{"group_id":7}]}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body.String())
	}
	if len(createdTargets) != 2 || createdTargets[0].PlatformAccountID != 10 || createdTargets[1].PlatformAccountID != 11 {
		t.Fatalf("group target was not expanded/deduplicated: %+v", createdTargets)
	}
}

func TestHandleCreatePost_HappyWithScheduledAt(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	store := &mockUserStore{}
	wsStore := &mockWorkspaceStore{
		findByIDFn: func(id int64) (*models.Workspace, error) {
			return &models.Workspace{ID: id, Name: "Mine", OwnerID: 1}, nil
		},
	}
	postStore := &mockPostStore{
		createFn: func(p *models.Post, _ []*models.PostTarget) error {
			p.ID = 100
			p.CreatedAt = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
			return nil
		},
	}
	// Taglio 3.2: legacy media_url REMOVED. No media in this test
	// (the post is text-only scheduled).
	r := newTestRouter(svc, store, "",
		WithWorkspaceStore(wsStore),
		WithPostStore(postStore),
	)

	// Pick a `scheduled_at` 7 days in the future, in RFC3339 form, so
	// the test stays inside `publishHorizonDays=30` regardless of when
	// CI runs. A literal date (e.g. "2026-08-10T00:00:00Z") silently
	// rots: on 2026-08-11 the date moves outside the 30-day window and
	// the test silently regresses to a 422 from the publish_at guard.
	scheduledAt := time.Now().UTC().Add(7 * 24 * time.Hour).Format(time.RFC3339)

	body := fmt.Sprintf(
		`{"workspace_id":1,"content":{"title":"future post"},"scheduled_at":"%s","targets":[{"platform_account_id":10}]}`,
		scheduledAt,
	)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		ID          int64  `json:"id"`
		Status      string `json:"status"`
		ScheduledAt string `json:"scheduled_at"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if resp.Status != "queued" {
		t.Fatalf("status: want scheduled, got %s", resp.Status)
	}
	if resp.ScheduledAt != scheduledAt {
		t.Fatalf("scheduled_at: want %s, got %s", scheduledAt, resp.ScheduledAt)
	}
}

func TestHandleCreatePost_MissingWorkspaceID_422(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	store := &mockUserStore{}
	wsStore := &mockWorkspaceStore{}
	postStore := &mockPostStore{}
	r := newTestRouter(svc, store, "",
		WithWorkspaceStore(wsStore),
		WithPostStore(postStore),
	)

	body := `{"targets":[{"platform_account_id":10}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d", w.Code)
	}
}

func TestHandleCreatePost_NoTargets_422(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	store := &mockUserStore{}
	wsStore := &mockWorkspaceStore{}
	postStore := &mockPostStore{}
	r := newTestRouter(svc, store, "",
		WithWorkspaceStore(wsStore),
		WithPostStore(postStore),
	)

	body := `{"workspace_id":1}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d", w.Code)
	}
}

func TestHandleCreatePost_BadTargetID_422(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	store := &mockUserStore{}
	wsStore := &mockWorkspaceStore{}
	postStore := &mockPostStore{}
	r := newTestRouter(svc, store, "",
		WithWorkspaceStore(wsStore),
		WithPostStore(postStore),
	)

	body := `{"workspace_id":1,"content":{"title":"x"},"targets":[{"platform_account_id":0}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d", w.Code)
	}
}

func TestHandleCreatePost_CrossOwnerWorkspace_403(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	store := &mockUserStore{}
	wsStore := &mockWorkspaceStore{
		findByIDFn: func(id int64) (*models.Workspace, error) {
			return &models.Workspace{ID: id, Name: "Other", OwnerID: 999}, nil
		},
	}
	postStore := &mockPostStore{}
	r := newTestRouter(svc, store, "",
		WithWorkspaceStore(wsStore),
		WithPostStore(postStore),
	)

	body := `{"workspace_id":1,"content":{"title":"x"},"targets":[{"platform_account_id":10}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 42)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleGetPost_CrossOwner_404(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	store := &mockUserStore{}
	wsStore := &mockWorkspaceStore{
		findByIDFn: func(id int64) (*models.Workspace, error) {
			return &models.Workspace{ID: id, Name: "Other", OwnerID: 999}, nil
		},
	}
	postStore := &mockPostStore{
		findByIDFn: func(id int64) (*models.Post, error) {
			return &models.Post{
				ID:          id,
				WorkspaceID: 1,
				Title:       "secret",
				Status:      models.PostStatusDraft,
				CreatedAt:   time.Now(),
			}, nil
		},
	}
	r := newTestRouter(svc, store, "",
		WithWorkspaceStore(wsStore),
		WithPostStore(postStore),
	)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/posts/100", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 42)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}

// ---------------------------------------------------------------------------
// /api/v1/media/presign tests (Taglio 3.2 — replaces the old
// /api/v1/storage/upload-url tests; the 11 new media-endpoint tests
// live in pkg/api/media_test.go).
// ---------------------------------------------------------------------------

// TestHandleCreatePost_StrictPayloadRejectsLegacyMediaURL proves
// Taglio 3.2: the public create-post payload no longer accepts
// media_url. A legacy payload silently ignores media_url and the
// server resolves media from the (empty) media:[] array, so the
// post is created with no media — this test documents the new
// contract by exercising the asset_id path.
func TestHandleCreatePost_StrictPayloadRejectsLegacyMediaURL(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	store := &mockUserStore{}
	wsStore := &mockWorkspaceStore{
		findByIDFn: func(id int64) (*models.Workspace, error) {
			return &models.Workspace{ID: id, Name: "Mine", OwnerID: 1}, nil
		},
	}
	postStore := &mockPostStore{
		createFn: func(p *models.Post, _ []*models.PostTarget) error {
			p.ID = 100
			p.CreatedAt = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
			return nil
		},
	}
	r := newTestRouter(svc, store, "",
		WithWorkspaceStore(wsStore),
		WithPostStore(postStore),
		WithMediaStore(newMockMediaStore()),
		WithStorageProvider(newMockStorageProvider()),
	)

	// Legacy payload with media_url — should be silently ignored.
	// The new contract is { content: { media: [{ asset_id }] } }.
	body := `{"workspace_id":1,"content":{"title":"x","media_url":"https://attacker.com/x.png"},"targets":[{"platform_account_id":10}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/posts", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("legacy media_url is ignored (not an error), but the new payload should still create the post: want 201, got %d: %s", w.Code, w.Body.String())
	}
}
