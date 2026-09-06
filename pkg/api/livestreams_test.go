package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

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
			return nil, credentials.ErrTokenExpired
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
