package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------

type mockLivestreamStore struct {
	createFn          func(ctx context.Context, ls *models.Livestream) error
	findByIDFn        func(ctx context.Context, id string) (*models.Livestream, error)
	listByWorkspaceFn func(ctx context.Context, workspaceID int64) ([]models.Livestream, error)
	updateFn          func(ctx context.Context, ls *models.Livestream) error
	deleteFn          func(ctx context.Context, id string) error
}

func (m *mockLivestreamStore) Create(ctx context.Context, ls *models.Livestream) error {
	if m.createFn == nil {
		return nil
	}
	return m.createFn(ctx, ls)
}
func (m *mockLivestreamStore) FindByID(ctx context.Context, id string) (*models.Livestream, error) {
	if m.findByIDFn != nil {
		return m.findByIDFn(ctx, id)
	}
	return nil, nil
}
func (m *mockLivestreamStore) ListByWorkspace(ctx context.Context, workspaceID int64) ([]models.Livestream, error) {
	if m.listByWorkspaceFn != nil {
		return m.listByWorkspaceFn(ctx, workspaceID)
	}
	return nil, nil
}
func (m *mockLivestreamStore) Update(ctx context.Context, ls *models.Livestream) error {
	if m.updateFn == nil {
		return nil
	}
	return m.updateFn(ctx, ls)
}
func (m *mockLivestreamStore) Delete(ctx context.Context, id string) error {
	if m.deleteFn == nil {
		return nil
	}
	return m.deleteFn(ctx, id)
}

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

const livestreamTestWorkspaceID = int64(7)

func livestreamTestAccount() *models.PlatformAccount {
	return &models.PlatformAccount{
		ID:             42,
		UserID:         1,
		Platform:       models.PlatformYouTube,
		PlatformUserID: "UC123",
		Username:       "testchannel",
		Status:         models.AccountStatusActive,
	}
}

func livestreamTestRouter(lsStore *mockLivestreamStore, account *models.PlatformAccount, ownerID int64) *Router {
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			if account != nil && id == account.ID {
				return account, nil
			}
			return nil, nil
		},
	}
	wsStore := &mockWorkspaceStore{
		findByIDFn: func(id int64) (*models.Workspace, error) {
			if id == livestreamTestWorkspaceID {
				return &models.Workspace{ID: livestreamTestWorkspaceID, OwnerID: ownerID, Name: "Test Workspace"}, nil
			}
			return nil, nil
		},
		findChannelFn: func(ctx context.Context, wsID, accountID int64) (*models.WorkspaceChannel, error) {
			if wsID == livestreamTestWorkspaceID && account != nil && accountID == account.ID {
				return &models.WorkspaceChannel{WorkspaceID: wsID, PlatformAccountID: accountID, Enabled: true}, nil
			}
			return nil, nil
		},
	}
	return mustNewRouterWithDefaults(
		services.NewCapabilityRouter(),
		store,
		auth.NewManager(testJWTSecret, 24),
		"",
		nil,
		WithWorkspaceStore(wsStore),
		WithLivestreamStore(lsStore),
	)
}

func validLivestreamPayload() map[string]any {
	return map[string]any{
		"workspace_id":        livestreamTestWorkspaceID,
		"platform_account_id": int64(42),
		"title":               "WWE News 24/7",
		"privacy_status":      "unlisted",
		"playback_mode":       "loop_continuous",
		"schedule_type":       "manual",
		"resolution":          "1080p30",
	}
}

func doLivestreamRequest(t *testing.T, r *Router, method, path string, payload any) *httptest.ResponseRecorder {
	t.Helper()
	var body *bytes.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal payload: %v", err)
		}
		body = bytes.NewReader(raw)
	} else {
		body = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, body)
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	return w
}

// ---------------------------------------------------------------------------
// POST /api/v1/livestreams
// ---------------------------------------------------------------------------

func TestCreateLivestream_HappyPath(t *testing.T) {
	var captured *models.Livestream
	lsStore := &mockLivestreamStore{
		createFn: func(ctx context.Context, ls *models.Livestream) error {
			captured = ls
			return nil
		},
	}
	r := livestreamTestRouter(lsStore, livestreamTestAccount(), 1)

	w := doLivestreamRequest(t, r, http.MethodPost, "/api/v1/livestreams", validLivestreamPayload())
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	if captured == nil {
		t.Fatal("livestream was not persisted")
	}
	if !strings.HasPrefix(captured.ID, "ls_") {
		t.Errorf("id should be prefixed with ls_, got %q", captured.ID)
	}
	if captured.WorkspaceID != livestreamTestWorkspaceID || captured.PlatformAccountID != 42 {
		t.Errorf("ownership fields wrong: %+v", captured)
	}
	if captured.DesiredState != models.LivestreamStateDraft || captured.ActualState != models.LivestreamStateDraft {
		t.Errorf("states should start as draft, got desired=%s actual=%s", captured.DesiredState, captured.ActualState)
	}
	if captured.Title != "WWE News 24/7" {
		t.Errorf("title: got %q", captured.Title)
	}
	if captured.FrameRate != 30 || captured.Resolution != "1080p30" || !captured.AutoRestart {
		t.Errorf("defaults not applied: %+v", captured)
	}

	var resp livestreamResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.ID != captured.ID || resp.ActualState != "draft" {
		t.Errorf("response mismatch: %+v", resp)
	}
}

func TestCreateLivestream_ScheduledRequiresStartAt(t *testing.T) {
	lsStore := &mockLivestreamStore{}
	r := livestreamTestRouter(lsStore, livestreamTestAccount(), 1)
	payload := validLivestreamPayload()
	payload["schedule_type"] = "scheduled"

	w := doLivestreamRequest(t, r, http.MethodPost, "/api/v1/livestreams", payload)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateLivestream_ValidationErrors(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(p map[string]any)
		wantMsg string
	}{
		{name: "missing title", mutate: func(p map[string]any) { delete(p, "title") }, wantMsg: "title is required"},
		{name: "blank title", mutate: func(p map[string]any) { p["title"] = "   " }, wantMsg: "title is required"},
		{name: "bad privacy", mutate: func(p map[string]any) { p["privacy_status"] = "secret" }, wantMsg: "privacy_status"},
		{name: "bad playback", mutate: func(p map[string]any) { p["playback_mode"] = "shuffle" }, wantMsg: "playback_mode"},
		{name: "bad schedule", mutate: func(p map[string]any) { p["schedule_type"] = "whenever" }, wantMsg: "schedule_type"},
		{name: "bad resolution", mutate: func(p map[string]any) { p["resolution"] = "4k60" }, wantMsg: "resolution"},
		{name: "bad frame rate", mutate: func(p map[string]any) { p["frame_rate"] = 60 }, wantMsg: "frame_rate"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := livestreamTestRouter(&mockLivestreamStore{}, livestreamTestAccount(), 1)
			payload := validLivestreamPayload()
			tc.mutate(payload)
			w := doLivestreamRequest(t, r, http.MethodPost, "/api/v1/livestreams", payload)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), tc.wantMsg) {
				t.Errorf("body should mention %q, got %s", tc.wantMsg, w.Body.String())
			}
		})
	}
}

func TestCreateLivestream_AccountNotYouTube(t *testing.T) {
	account := livestreamTestAccount()
	account.Platform = models.PlatformInstagram
	r := livestreamTestRouter(&mockLivestreamStore{}, account, 1)

	w := doLivestreamRequest(t, r, http.MethodPost, "/api/v1/livestreams", validLivestreamPayload())
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateLivestream_AccountNotLinked(t *testing.T) {
	// The account exists and is YouTube, but the workspace_channels
	// join returns nothing → the channel is not linked to the workspace.
	account := livestreamTestAccount()
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return account, nil
		},
	}
	wsStore := &mockWorkspaceStore{
		findByIDFn: func(id int64) (*models.Workspace, error) {
			return &models.Workspace{ID: livestreamTestWorkspaceID, OwnerID: 1, Name: "Test Workspace"}, nil
		},
		findChannelFn: func(ctx context.Context, wsID, accountID int64) (*models.WorkspaceChannel, error) {
			return nil, nil
		},
	}
	r := mustNewRouterWithDefaults(
		services.NewCapabilityRouter(),
		store,
		auth.NewManager(testJWTSecret, 24),
		"",
		nil,
		WithWorkspaceStore(wsStore),
		WithLivestreamStore(&mockLivestreamStore{}),
	)

	w := doLivestreamRequest(t, r, http.MethodPost, "/api/v1/livestreams", validLivestreamPayload())
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "not linked") {
		t.Errorf("body should mention the channel linkage, got %s", w.Body.String())
	}
}

func TestCreateLivestream_AccountInactive(t *testing.T) {
	account := livestreamTestAccount()
	account.Status = models.AccountStatusReauthRequired
	r := livestreamTestRouter(&mockLivestreamStore{}, account, 1)

	w := doLivestreamRequest(t, r, http.MethodPost, "/api/v1/livestreams", validLivestreamPayload())
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateLivestream_WorkspaceNotOwned(t *testing.T) {
	// Workspace owned by user 2; the JWT belongs to user 1.
	r := livestreamTestRouter(&mockLivestreamStore{}, livestreamTestAccount(), 2)

	w := doLivestreamRequest(t, r, http.MethodPost, "/api/v1/livestreams", validLivestreamPayload())
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestCreateLivestream_RequiresAuth(t *testing.T) {
	r := livestreamTestRouter(&mockLivestreamStore{}, livestreamTestAccount(), 1)
	raw, _ := json.Marshal(validLivestreamPayload())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/livestreams", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// GET /api/v1/livestreams
// ---------------------------------------------------------------------------

func TestListLivestreams_HappyPath(t *testing.T) {
	ls := livestreamFixtureResponse()
	lsStore := &mockLivestreamStore{
		listByWorkspaceFn: func(ctx context.Context, workspaceID int64) ([]models.Livestream, error) {
			if workspaceID != livestreamTestWorkspaceID {
				t.Errorf("list workspace = %d", workspaceID)
			}
			return []models.Livestream{*ls}, nil
		},
	}
	r := livestreamTestRouter(lsStore, livestreamTestAccount(), 1)

	w := doLivestreamRequest(t, r, http.MethodGet, "/api/v1/livestreams?workspace_id=7", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp listLivestreamsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Items) != 1 || resp.Items[0].ID != ls.ID || resp.Items[0].ActualState != models.LivestreamStateLive {
		t.Fatalf("unexpected items: %+v", resp.Items)
	}
	// The control-center page renders the channel name on every live
	// card; it is resolved from the platform account by the handler.
	if resp.Items[0].ChannelName != "testchannel" {
		t.Errorf("channel_name: want testchannel (account username), got %q", resp.Items[0].ChannelName)
	}
}

func livestreamFixtureResponse() *models.Livestream {
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	return &models.Livestream{
		ID:                "ls_live1",
		WorkspaceID:       livestreamTestWorkspaceID,
		PlatformAccountID: 42,
		CreatedBy:         1,
		Title:             "WWE News 24/7",
		PrivacyStatus:     models.LivestreamPrivacyUnlisted,
		PlaybackMode:      models.LivestreamPlaybackLoopContinuous,
		ScheduleType:      models.LivestreamScheduleManual,
		DesiredState:      models.LivestreamStateLive,
		ActualState:       models.LivestreamStateLive,
		Resolution:        models.LivestreamResolution1080p,
		FrameRate:         30,
		AutoRestart:       true,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
}

func TestListLivestreams_RequiresWorkspaceID(t *testing.T) {
	r := livestreamTestRouter(&mockLivestreamStore{}, livestreamTestAccount(), 1)
	w := doLivestreamRequest(t, r, http.MethodGet, "/api/v1/livestreams", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestListLivestreams_WorkspaceNotOwned(t *testing.T) {
	r := livestreamTestRouter(&mockLivestreamStore{}, livestreamTestAccount(), 2)
	w := doLivestreamRequest(t, r, http.MethodGet, "/api/v1/livestreams?workspace_id=7", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestListLivestreams_EmptyList(t *testing.T) {
	lsStore := &mockLivestreamStore{
		listByWorkspaceFn: func(ctx context.Context, workspaceID int64) ([]models.Livestream, error) {
			return nil, nil
		},
	}
	r := livestreamTestRouter(lsStore, livestreamTestAccount(), 1)

	w := doLivestreamRequest(t, r, http.MethodGet, "/api/v1/livestreams?workspace_id=7", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	// The sidebar badge hook relies on a non-null `items` array.
	if !strings.Contains(w.Body.String(), `"items":[]`) {
		t.Errorf("empty list should serialize as items:[], got %s", w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// GET /api/v1/livestreams/{id}
// ---------------------------------------------------------------------------

func TestGetLivestream_HappyPath(t *testing.T) {
	ls := livestreamFixtureResponse()
	lsStore := &mockLivestreamStore{
		findByIDFn: func(ctx context.Context, id string) (*models.Livestream, error) {
			if id == ls.ID {
				return ls, nil
			}
			return nil, nil
		},
	}
	r := livestreamTestRouter(lsStore, livestreamTestAccount(), 1)

	w := doLivestreamRequest(t, r, http.MethodGet, "/api/v1/livestreams/"+ls.ID, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp livestreamResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.ID != ls.ID {
		t.Errorf("id: got %q", resp.ID)
	}
}

func TestGetLivestream_NotFound(t *testing.T) {
	r := livestreamTestRouter(&mockLivestreamStore{}, livestreamTestAccount(), 1)
	w := doLivestreamRequest(t, r, http.MethodGet, "/api/v1/livestreams/ls_missing", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// PATCH /api/v1/livestreams/{id}
// ---------------------------------------------------------------------------

func TestPatchLivestream_HappyPath(t *testing.T) {
	ls := livestreamFixtureResponse()
	var updated *models.Livestream
	lsStore := &mockLivestreamStore{
		findByIDFn: func(ctx context.Context, id string) (*models.Livestream, error) {
			if id == ls.ID {
				return ls, nil
			}
			return nil, nil
		},
		updateFn: func(ctx context.Context, row *models.Livestream) error {
			updated = row
			return nil
		},
	}
	r := livestreamTestRouter(lsStore, livestreamTestAccount(), 1)

	w := doLivestreamRequest(t, r, http.MethodPatch, "/api/v1/livestreams/"+ls.ID, map[string]any{
		"title":          "WWE News 24/7 — Nuovo",
		"privacy_status": "private",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if updated == nil || updated.Title != "WWE News 24/7 — Nuovo" || updated.PrivacyStatus != "private" {
		t.Fatalf("updated row wrong: %+v", updated)
	}
	if updated.PlaybackMode != models.LivestreamPlaybackLoopContinuous {
		t.Errorf("untouched field should survive: %+v", updated)
	}
}

func TestPatchLivestream_RejectsWorkerOwnedState(t *testing.T) {
	ls := livestreamFixtureResponse()
	lsStore := &mockLivestreamStore{
		findByIDFn: func(ctx context.Context, id string) (*models.Livestream, error) {
			return ls, nil
		},
	}
	r := livestreamTestRouter(lsStore, livestreamTestAccount(), 1)

	w := doLivestreamRequest(t, r, http.MethodPatch, "/api/v1/livestreams/"+ls.ID, map[string]any{
		"desired_state": "live",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestPatchLivestream_NotFound(t *testing.T) {
	r := livestreamTestRouter(&mockLivestreamStore{}, livestreamTestAccount(), 1)
	w := doLivestreamRequest(t, r, http.MethodPatch, "/api/v1/livestreams/ls_missing", map[string]any{
		"title": "x",
	})
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// DELETE /api/v1/livestreams/{id}
// ---------------------------------------------------------------------------

func TestDeleteLivestream_HappyPath(t *testing.T) {
	ls := livestreamFixtureResponse()
	deleted := false
	lsStore := &mockLivestreamStore{
		findByIDFn: func(ctx context.Context, id string) (*models.Livestream, error) {
			if id == ls.ID {
				return ls, nil
			}
			return nil, nil
		},
		deleteFn: func(ctx context.Context, id string) error {
			deleted = true
			return nil
		},
	}
	r := livestreamTestRouter(lsStore, livestreamTestAccount(), 1)

	w := doLivestreamRequest(t, r, http.MethodDelete, "/api/v1/livestreams/"+ls.ID, nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", w.Code, w.Body.String())
	}
	if !deleted {
		t.Error("delete was not called")
	}
}

func TestDeleteLivestream_NotFound(t *testing.T) {
	r := livestreamTestRouter(&mockLivestreamStore{}, livestreamTestAccount(), 1)
	w := doLivestreamRequest(t, r, http.MethodDelete, "/api/v1/livestreams/ls_missing", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}
