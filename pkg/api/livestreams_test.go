package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
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
	return livestreamTestRouterWithVault(lsStore, account, ownerID, &mockCredentialVault{
		getFn: func(ctx context.Context, platformAccountID int64, tokenType string) (*models.OAuthToken, error) {
			if account != nil && platformAccountID == account.ID {
				return &models.OAuthToken{TokenType: tokenType, Scopes: []string{"https://www.googleapis.com/auth/youtube.force-ssl"}}, nil
			}
			return nil, nil
		},
	})
}

// livestreamTestRouterWithVault wires a custom vault so the scope guard
// in handleCreateLivestream is exercised (and so tests can simulate a
// grant without the live scope, or a missing/expired grant).
func livestreamTestRouterWithVault(lsStore *mockLivestreamStore, account *models.PlatformAccount, ownerID int64, vault credentials.VaultAPI) *Router {
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
		nil, WithWorkspaceStore(wsStore),
		WithLivestreamStore(lsStore),
		WithCredentialVault(vault),
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
// Shared command policy
// ---------------------------------------------------------------------------

func TestApplyLivestreamFields_CreateAndPatchSharePolicy(t *testing.T) {
	payload := validLivestreamPayload()
	payload["title"] = "  Shared title  "
	payload["category"] = "24"
	payload["language"] = "it"
	payload["latency_preference"] = "low"
	payload["auto_start"] = true

	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	var create createLivestreamRequest
	if err := json.Unmarshal(raw, &create); err != nil {
		t.Fatalf("decode create payload: %v", err)
	}
	created := &models.Livestream{
		ScheduleType:      models.LivestreamScheduleManual,
		Resolution:        models.LivestreamResolution1080p,
		FrameRate:         models.LivestreamFrameRate,
		AutoRestart:       true,
		LatencyPreference: models.LivestreamLatencyNormal,
	}
	if err := applyLivestreamFields(created, livestreamCreateFields(create)); err != nil {
		t.Fatalf("apply create fields: %v", err)
	}
	if created.Title != "Shared title" || created.Category != "24" || created.Language != "it" || !created.AutoStart {
		t.Fatalf("create policy did not normalize fields: %+v", created)
	}

	title := "  Shared title  "
	patch := patchLivestreamRequest{Title: &title}
	updated := &models.Livestream{Title: "old", ScheduleType: models.LivestreamScheduleManual}
	if err := applyLivestreamFields(updated, livestreamPatchFields(patch)); err != nil {
		t.Fatalf("apply patch fields: %v", err)
	}
	if updated.Title != created.Title {
		t.Fatalf("create and patch normalization diverged: create=%q patch=%q", created.Title, updated.Title)
	}
	if updated.Category != "" || updated.AutoStart {
		t.Fatalf("patch changed fields that were not supplied: %+v", updated)
	}
}

func TestApplyLivestreamFields_RejectsScheduledWithoutStart(t *testing.T) {
	schedule := models.LivestreamScheduleScheduled
	ls := &models.Livestream{ScheduleType: models.LivestreamScheduleManual}
	if err := applyLivestreamFields(ls, livestreamFieldInput{ScheduleType: &schedule}); err == nil || !strings.Contains(err.Error(), "scheduled_start_at") {
		t.Fatalf("expected scheduled start invariant error, got %v", err)
	}
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
	// Wizard step-2 metadata defaults: no category, no language, no
	// cover, not made for kids, DVR/auto-start/auto-stop off, latency
	// normal.
	if captured.Category != "" || captured.Language != "" || captured.ThumbnailMediaID != nil {
		t.Errorf("metadata defaults not applied: %+v", captured)
	}
	if captured.MadeForKids || captured.DVREnabled || captured.AutoStart || captured.AutoStop {
		t.Errorf("metadata booleans should default false: %+v", captured)
	}
	if captured.LatencyPreference != models.LivestreamLatencyNormal {
		t.Errorf("latency default: want normal, got %q", captured.LatencyPreference)
	}

	var resp livestreamResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.ID != captured.ID || resp.ActualState != "draft" {
		t.Errorf("response mismatch: %+v", resp)
	}
}

func TestCreateLivestream_MetadataFieldsPersisted(t *testing.T) {
	var captured *models.Livestream
	lsStore := &mockLivestreamStore{
		createFn: func(ctx context.Context, ls *models.Livestream) error {
			captured = ls
			return nil
		},
	}
	r := livestreamTestRouter(lsStore, livestreamTestAccount(), 1)

	thumb := "3fa85f64-5717-4562-b3fc-2c963f66afa6"
	payload := validLivestreamPayload()
	payload["category"] = "24"
	payload["made_for_kids"] = true
	payload["language"] = "it"
	payload["thumbnail_media_id"] = thumb
	payload["dvr_enabled"] = true
	payload["auto_start"] = true
	payload["auto_stop"] = false
	payload["latency_preference"] = "low"

	w := doLivestreamRequest(t, r, http.MethodPost, "/api/v1/livestreams", payload)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	if captured == nil {
		t.Fatal("livestream was not persisted")
	}
	if captured.Category != "24" {
		t.Errorf("category: got %q", captured.Category)
	}
	if !captured.MadeForKids {
		t.Error("made_for_kids should be true")
	}
	if captured.Language != "it" {
		t.Errorf("language: got %q", captured.Language)
	}
	if captured.ThumbnailMediaID == nil || *captured.ThumbnailMediaID != thumb {
		t.Errorf("thumbnail_media_id: got %v", captured.ThumbnailMediaID)
	}
	if !captured.DVREnabled || !captured.AutoStart || captured.AutoStop {
		t.Errorf("contentDetails booleans: %+v", captured)
	}
	if captured.LatencyPreference != models.LivestreamLatencyLow {
		t.Errorf("latency: got %q", captured.LatencyPreference)
	}

	// The response echoes the same metadata.
	var resp livestreamResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Category != "24" || resp.Language != "it" || resp.LatencyPreference != "low" {
		t.Errorf("response metadata mismatch: %+v", resp)
	}
	if resp.ThumbnailMediaID == nil || *resp.ThumbnailMediaID != thumb {
		t.Errorf("response thumbnail_media_id: got %v", resp.ThumbnailMediaID)
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
		{name: "bad category", mutate: func(p map[string]any) { p["category"] = "9999" }, wantMsg: "category"},
		{name: "bad language", mutate: func(p map[string]any) { p["language"] = "not a lang" }, wantMsg: "language"},
		{name: "bad latency", mutate: func(p map[string]any) { p["latency_preference"] = "ultra" }, wantMsg: "latency_preference"},
		{name: "thumbnail too long", mutate: func(p map[string]any) { p["thumbnail_media_id"] = strings.Repeat("a", 80) }, wantMsg: "thumbnail_media_id"},
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

func TestCreateLivestream_RejectsMissingLiveScope(t *testing.T) {
	account := livestreamTestAccount()
	vault := &mockCredentialVault{
		getFn: func(ctx context.Context, platformAccountID int64, tokenType string) (*models.OAuthToken, error) {
			if platformAccountID == account.ID {
				// Grant exists but carries no YouTube live scope.
				return &models.OAuthToken{TokenType: tokenType, Scopes: []string{"https://www.googleapis.com/auth/youtube.readonly"}}, nil
			}
			return nil, nil
		},
	}
	r := livestreamTestRouterWithVault(&mockLivestreamStore{}, account, 1, vault)

	w := doLivestreamRequest(t, r, http.MethodPost, "/api/v1/livestreams", validLivestreamPayload())
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "live streaming") {
		t.Errorf("body should mention live streaming, got %s", w.Body.String())
	}
}

func TestCreateLivestream_RejectsUnavailableGrant(t *testing.T) {
	account := livestreamTestAccount()
	vault := &mockCredentialVault{
		getFn: func(ctx context.Context, platformAccountID int64, tokenType string) (*models.OAuthToken, error) {
			return nil, errors.New("token expired")
		},
	}
	r := livestreamTestRouterWithVault(&mockLivestreamStore{}, account, 1, vault)

	w := doLivestreamRequest(t, r, http.MethodPost, "/api/v1/livestreams", validLivestreamPayload())
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "grant") {
		t.Errorf("body should mention the grant, got %s", w.Body.String())
	}
}

func TestLivestreamRoutes_Contract(t *testing.T) {
	r := livestreamTestRouter(&mockLivestreamStore{}, livestreamTestAccount(), 1)
	patterns := make(map[string]map[string]bool)
	r.Setup()
	for _, route := range r.mux.Routes() {
		if patterns[route.Pattern] == nil {
			patterns[route.Pattern] = make(map[string]bool)
		}
		for method := range route.Handlers {
			patterns[route.Pattern][method] = true
		}
	}
	want := map[string]string{
		"/api/v1/livestreams":          "GET,POST",
		"/api/v1/livestreams/channels": "GET",
		"/api/v1/livestreams/{id}":     "DELETE,GET,PATCH",
	}
	for pattern, methods := range want {
		for _, method := range strings.Split(methods, ",") {
			if !patterns[pattern][method] {
				t.Errorf("missing livestream route %s %s", method, pattern)
			}
		}
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
	// Wizard step-2 metadata round-trips through the list envelope.
	if resp.Items[0].Category != "24" || resp.Items[0].Language != "it" {
		t.Errorf("metadata fields missing from list response: %+v", resp.Items[0])
	}
	if resp.Items[0].ThumbnailMediaID == nil || *resp.Items[0].ThumbnailMediaID != "thumb-123" {
		t.Errorf("thumbnail_media_id missing from list response: %+v", resp.Items[0])
	}
	if !resp.Items[0].DVREnabled || !resp.Items[0].AutoStop || resp.Items[0].AutoStart {
		t.Errorf("contentDetails booleans missing from list response: %+v", resp.Items[0])
	}
	if resp.Items[0].LatencyPreference != models.LivestreamLatencyLow {
		t.Errorf("latency_preference missing from list response: %+v", resp.Items[0])
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
		Category:          "24",
		MadeForKids:       false,
		Language:          "it",
		ThumbnailMediaID:  strPtr("thumb-123"),
		DVREnabled:        true,
		AutoStart:         false,
		AutoStop:          true,
		LatencyPreference: models.LivestreamLatencyLow,
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

func TestListLivestreamChannels_HappyPath(t *testing.T) {
	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	lastValidated := now.Add(-2 * time.Minute)

	validYT := livestreamTestAccount() // id 42
	validYT.Username = "WWE Insider Italia"
	validYT.LastValidatedAt = &lastValidated
	scopeLessYT := &models.PlatformAccount{ID: 43, UserID: 1, Platform: models.PlatformYouTube, PlatformUserID: "UC-scopeless", Username: "Old Channel", Status: models.AccountStatusActive}
	instagram := &models.PlatformAccount{ID: 44, UserID: 1, Platform: models.PlatformInstagram, PlatformUserID: "ig-1", Username: "IG", Status: models.AccountStatusActive}

	accounts := map[int64]*models.PlatformAccount{
		42: validYT, 43: scopeLessYT, 44: instagram,
	}
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return accounts[id], nil
		},
	}
	wsStore := &mockWorkspaceStore{
		findByIDFn: func(id int64) (*models.Workspace, error) {
			return &models.Workspace{ID: livestreamTestWorkspaceID, OwnerID: 1, Name: "Test Workspace"}, nil
		},
		listChannelsFn: func(ctx context.Context, workspaceID int64) ([]models.WorkspaceChannel, error) {
			return []models.WorkspaceChannel{
				{WorkspaceID: workspaceID, PlatformAccountID: 42, Enabled: true},
				{WorkspaceID: workspaceID, PlatformAccountID: 43, Enabled: true},
				{WorkspaceID: workspaceID, PlatformAccountID: 44, Enabled: true},
			}, nil
		},
	}
	vault := &mockCredentialVault{
		getFn: func(ctx context.Context, platformAccountID int64, tokenType string) (*models.OAuthToken, error) {
			switch platformAccountID {
			case 42:
				return &models.OAuthToken{TokenType: tokenType, Scopes: []string{"https://www.googleapis.com/auth/youtube.force-ssl"}}, nil
			case 43:
				return &models.OAuthToken{TokenType: tokenType, Scopes: []string{"https://www.googleapis.com/auth/youtube.readonly"}}, nil
			default:
				return nil, nil
			}
		},
	}
	lsStore := &mockLivestreamStore{
		listByWorkspaceFn: func(ctx context.Context, workspaceID int64) ([]models.Livestream, error) {
			return []models.Livestream{
				{ID: "ls_live1", WorkspaceID: workspaceID, PlatformAccountID: 42, ActualState: models.LivestreamStateLive},
				{ID: "ls_live2", WorkspaceID: workspaceID, PlatformAccountID: 42, ActualState: models.LivestreamStateLive},
				{ID: "ls_sched", WorkspaceID: workspaceID, PlatformAccountID: 42, ActualState: models.LivestreamStateScheduled},
			}, nil
		},
	}
	r := mustNewRouterWithDefaults(
		services.NewCapabilityRouter(),
		store,
		auth.NewManager(testJWTSecret, 24),
		"",
		nil, WithWorkspaceStore(wsStore),
		WithLivestreamStore(lsStore),
		WithCredentialVault(vault),
	)

	w := doLivestreamRequest(t, r, http.MethodGet, "/api/v1/livestreams/channels?workspace_id=7", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp listLivestreamChannelsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	// The Instagram account is filtered out; both YouTube channels stay
	// visible so the UI can explain why the scope-less one is blocked.
	if len(resp.Channels) != 2 {
		t.Fatalf("channels: want 2 (non-YouTube filtered), got %+v", resp.Channels)
	}
	byID := map[int64]livestreamChannelResponse{}
	for _, c := range resp.Channels {
		byID[c.PlatformAccountID] = c
	}
	first := byID[42]
	if !first.OAuthReady || !first.LiveEnabled {
		t.Errorf("account 42: want oauth_ready+live_enabled, got %+v", first)
	}
	if first.ActiveLives != 2 {
		t.Errorf("account 42 active_lives: want 2 (scheduled excluded), got %d", first.ActiveLives)
	}
	if first.LastVerifiedAt == nil || !first.LastVerifiedAt.Equal(lastValidated) {
		t.Errorf("account 42 last_verified_at: want %v, got %v", lastValidated, first.LastVerifiedAt)
	}
	if first.Username != "WWE Insider Italia" {
		t.Errorf("account 42 username: got %q", first.Username)
	}
	second := byID[43]
	if !second.OAuthReady || second.LiveEnabled {
		t.Errorf("account 43: want oauth_ready but live_enabled=false, got %+v", second)
	}
}

func TestListLivestreamChannels_RequiresWorkspaceID(t *testing.T) {
	r := livestreamTestRouter(&mockLivestreamStore{}, livestreamTestAccount(), 1)
	w := doLivestreamRequest(t, r, http.MethodGet, "/api/v1/livestreams/channels", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestListLivestreamChannels_WorkspaceNotOwned(t *testing.T) {
	r := livestreamTestRouter(&mockLivestreamStore{}, livestreamTestAccount(), 2)
	w := doLivestreamRequest(t, r, http.MethodGet, "/api/v1/livestreams/channels?workspace_id=7", nil)
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

func TestPatchLivestream_MetadataFields(t *testing.T) {
	ls := livestreamFixtureResponse()
	var updated *models.Livestream
	lsStore := &mockLivestreamStore{
		findByIDFn: func(ctx context.Context, id string) (*models.Livestream, error) {
			return ls, nil
		},
		updateFn: func(ctx context.Context, row *models.Livestream) error {
			updated = row
			return nil
		},
	}
	r := livestreamTestRouter(lsStore, livestreamTestAccount(), 1)

	w := doLivestreamRequest(t, r, http.MethodPatch, "/api/v1/livestreams/"+ls.ID, map[string]any{
		"category":           "20",
		"made_for_kids":      true,
		"language":           "en",
		"dvr_enabled":        false,
		"auto_start":         true,
		"auto_stop":          false,
		"latency_preference": "ultraLow",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if updated == nil {
		t.Fatal("updated row is nil")
	}
	if updated.Category != "20" || updated.Language != "en" || updated.LatencyPreference != "ultraLow" {
		t.Errorf("patched metadata wrong: %+v", updated)
	}
	if !updated.MadeForKids || !updated.AutoStart || updated.AutoStop || updated.DVREnabled {
		t.Errorf("patched booleans wrong: %+v", updated)
	}
	// Untouched thumbnail survives.
	if updated.ThumbnailMediaID == nil || *updated.ThumbnailMediaID != "thumb-123" {
		t.Errorf("untouched thumbnail should survive: %+v", updated)
	}
}

func TestPatchLivestream_ClearsThumbnail(t *testing.T) {
	ls := livestreamFixtureResponse()
	var updated *models.Livestream
	lsStore := &mockLivestreamStore{
		findByIDFn: func(ctx context.Context, id string) (*models.Livestream, error) {
			return ls, nil
		},
		updateFn: func(ctx context.Context, row *models.Livestream) error {
			updated = row
			return nil
		},
	}
	r := livestreamTestRouter(lsStore, livestreamTestAccount(), 1)

	// Empty string clears the cover (same semantics as scheduled_start_at).
	w := doLivestreamRequest(t, r, http.MethodPatch, "/api/v1/livestreams/"+ls.ID, map[string]any{
		"thumbnail_media_id": "",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if updated.ThumbnailMediaID != nil {
		t.Errorf("thumbnail_media_id should be cleared, got %v", updated.ThumbnailMediaID)
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
