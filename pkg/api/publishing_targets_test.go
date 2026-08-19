package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

func TestHandleListPublishingTargets_ReturnsYouTubeChannelsAndGroups(t *testing.T) {
	const (
		userID      = int64(1)
		workspaceID = int64(7)
	)

	userStore := &mockUserStore{
		listFilteredYouTubeAccountsFn: func(gotUserID int64, gotWorkspaceID *int64, _, _, _ string) ([]*models.PlatformAccount, error) {
			if gotUserID != userID || gotWorkspaceID == nil || *gotWorkspaceID != workspaceID {
				t.Fatalf("unexpected account scope: user=%d workspace=%v", gotUserID, gotWorkspaceID)
			}
			return []*models.PlatformAccount{
				{ID: 101, Platform: "youtube", PlatformUserID: "UC-it", Username: "Boxe IT", Status: models.AccountStatusActive},
				{ID: 102, Platform: "youtube", PlatformUserID: "UC-en", Username: "Boxe EN", Status: models.AccountStatusActive},
			}, nil
		},
	}
	r := mustNewRouterWithDefaults(
		services.NewCapabilityRouter(), userStore, auth.NewManager(testJWTSecret, 24),
		"https://app.instaedit.org", nil,
		WithWorkspaceStore(&mockWorkspaceStore{
			findByIDFn: func(id int64) (*models.Workspace, error) {
				return &models.Workspace{ID: id, OwnerID: userID}, nil
			},
			listChannelsFn: func(context.Context, int64) ([]models.WorkspaceChannel, error) {
				return []models.WorkspaceChannel{
					{WorkspaceID: workspaceID, PlatformAccountID: 101, Enabled: true},
					{WorkspaceID: workspaceID, PlatformAccountID: 102, Enabled: true},
				}, nil
			},
		}),
		WithGroupStore(&mockGroupStore{
			listByWorkspaceWithAccountsFn: func(int64) ([]models.GroupWithAccounts, error) {
				return []models.GroupWithAccounts{{
					Group:      models.Group{ID: 55, WorkspaceID: workspaceID, Name: "Boxe"},
					AccountIDs: []int64{101, 102},
				}}, nil
			},
		}),
	)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/publishing/targets", nil)
	withBearerJWTForWorkspace(t, req, userID, workspaceID)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("catalog endpoint: want 200, got %d: %s", w.Code, w.Body.String())
	}
	var response struct {
		WorkspaceID int64 `json:"workspace_id"`
		Channels    []struct {
			PlatformAccountID int64  `json:"platform_account_id"`
			ChannelID         string `json:"channel_id"`
		} `json:"channels"`
		Groups []struct {
			GroupID           int64   `json:"group_id"`
			ChannelAccountIDs []int64 `json:"channel_account_ids"`
		} `json:"groups"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode catalog: %v", err)
	}
	if response.WorkspaceID != workspaceID || len(response.Channels) != 2 || len(response.Groups) != 1 {
		t.Fatalf("unexpected catalog: %+v", response)
	}
	if response.Groups[0].GroupID != 55 || len(response.Groups[0].ChannelAccountIDs) != 2 {
		t.Fatalf("unexpected Boxe group: %+v", response.Groups[0])
	}
}
