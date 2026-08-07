package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

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
