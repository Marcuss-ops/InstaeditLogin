package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

func newMetadataPatchRouter(t *testing.T, updateFn func(ctx context.Context, accessToken, videoID, expectedChannelID string, patch models.YouTubeMetadataPatch) (*models.YouTubeMetadataResult, error), vaultGetFn func(ctx context.Context, id int64, tt string) (*models.OAuthToken, error)) (*Router, *mockYouTubeOAuthServiceForEditor) {
	t.Helper()
	workspace := &models.Workspace{ID: 7, OwnerID: 1}
	ytAccount := &models.PlatformAccount{
		ID:             42,
		UserID:         1,
		Platform:       models.PlatformYouTube,
		PlatformUserID: "UC123",
		Username:       "testchannel",
		Status:         models.AccountStatusActive,
	}
	group := &models.Group{ID: 3, WorkspaceID: workspace.ID, Name: "Marketing"}

	groupStore := &mockGroupStore{
		findByIDFn: func(id int64) (*models.Group, error) {
			if id == group.ID {
				return group, nil
			}
			return nil, nil
		},
		listAccountsInGroupFn: func(groupID int64) ([]int64, error) {
			if groupID == group.ID {
				return []int64{ytAccount.ID}, nil
			}
			return nil, nil
		},
	}
	userStore := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			if id == ytAccount.ID {
				return ytAccount, nil
			}
			return nil, nil
		},
	}
	editStore := &mockYouTubeVideoEditStore{}
	ytSvc := &mockYouTubeOAuthServiceForEditor{
		updateVideoMetadataFn: updateFn,
	}
	vault := &mockCredentialVault{getFn: vaultGetFn}

	r := newGroupVideosRouter(t, workspace, groupStore, userStore, editStore, ytSvc, vault)
	return r, ytSvc
}

func patchVideoRequest(t *testing.T, r *Router, groupID int, videoID, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/api/v1/groups/%d/youtube/videos/%s", groupID, videoID), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	return w
}

func TestPatchGroupYouTubeVideoMetadata_HappyPath(t *testing.T) {
	var gotToken, gotVideoID, gotChannel string
	var gotPatch models.YouTubeMetadataPatch
	updateFn := func(ctx context.Context, accessToken, videoID, expectedChannelID string, patch models.YouTubeMetadataPatch) (*models.YouTubeMetadataResult, error) {
		gotToken = accessToken
		gotVideoID = videoID
		gotChannel = expectedChannelID
		gotPatch = patch
		return &models.YouTubeMetadataResult{
			VideoID:     videoID,
			Title:       *patch.Title,
			Description: *patch.Description,
			CategoryID:  *patch.CategoryID,
		}, nil
	}
	r, _ := newMetadataPatchRouter(t, updateFn, func(ctx context.Context, id int64, tt string) (*models.OAuthToken, error) {
		return &models.OAuthToken{AccessToken: "valid-token"}, nil
	})

	w := patchVideoRequest(t, r, 3, "VID123", `{"platform_account_id": 42, "title": "Nuovo titolo", "description": "Nuova descrizione", "category_id": "24"}`)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if gotToken != "valid-token" {
		t.Errorf("token: want valid-token, got %q", gotToken)
	}
	if gotVideoID != "VID123" {
		t.Errorf("video id: want VID123, got %q", gotVideoID)
	}
	// expectedChannelID gates the update to the account's channel.
	if gotChannel != "UC123" {
		t.Errorf("expected channel: want UC123, got %q", gotChannel)
	}
	if gotPatch.Title == nil || *gotPatch.Title != "Nuovo titolo" {
		t.Errorf("patch title: want Nuovo titolo, got %v", gotPatch.Title)
	}
	if gotPatch.Description == nil || *gotPatch.Description != "Nuova descrizione" {
		t.Errorf("patch description: want Nuova descrizione, got %v", gotPatch.Description)
	}
	if gotPatch.CategoryID == nil || *gotPatch.CategoryID != "24" {
		t.Errorf("patch category: want 24, got %v", gotPatch.CategoryID)
	}

	var resp groupYouTubeVideoMetadataResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.YoutubeVideoID != "VID123" || resp.Title != "Nuovo titolo" || resp.Description != "Nuova descrizione" || resp.CategoryID != "24" {
		t.Errorf("response: got %+v", resp)
	}
}

func TestPatchGroupYouTubeVideoMetadata_PrivacyChange(t *testing.T) {
	var gotPatch models.YouTubeMetadataPatch
	updateFn := func(ctx context.Context, accessToken, videoID, expectedChannelID string, patch models.YouTubeMetadataPatch) (*models.YouTubeMetadataResult, error) {
		gotPatch = patch
		return &models.YouTubeMetadataResult{
			VideoID:       videoID,
			Title:         "T",
			Description:   "D",
			CategoryID:    "22",
			PrivacyStatus: "public",
		}, nil
	}
	r, _ := newMetadataPatchRouter(t, updateFn, func(ctx context.Context, id int64, tt string) (*models.OAuthToken, error) {
		return &models.OAuthToken{AccessToken: "valid-token"}, nil
	})

	w := patchVideoRequest(t, r, 3, "VID123", `{"platform_account_id": 42, "title": "T", "privacy_status": "public"}`)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if gotPatch.PrivacyStatus == nil || *gotPatch.PrivacyStatus != "public" {
		t.Errorf("patch privacy: want public, got %v", gotPatch.PrivacyStatus)
	}
	var resp groupYouTubeVideoMetadataResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.PrivacyStatus != "public" {
		t.Errorf("response privacy: want public, got %q", resp.PrivacyStatus)
	}
}

func TestPatchGroupYouTubeVideoMetadata_Validation(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"invalid group id", "invalid", "group_id path parameter"},
		{"missing platform_account_id", `{"title": "T"}`, "platform_account_id"},
		{"empty patch", `{"platform_account_id": 42}`, "at least one"},
		{"empty title", `{"platform_account_id": 42, "title": "   "}`, "title cannot be empty"},
		{"unknown category", `{"platform_account_id": 42, "category_id": "9999"}`, "known YouTube category"},
		{"invalid privacy", `{"platform_account_id": 42, "privacy_status": "bogus"}`, "privacy_status must be"},
		{"malformed json", `{`, "invalid JSON body"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, _ := newMetadataPatchRouter(t, nil, func(ctx context.Context, id int64, tt string) (*models.OAuthToken, error) {
				t.Errorf("vault.Renew must not be reached on %s", tc.name)
				return nil, errors.New("unexpected")
			})
			groupID := 3
			urlPath := fmt.Sprintf("/api/v1/groups/%d/youtube/videos/VID123", groupID)
			if tc.name == "invalid group id" {
				urlPath = "/api/v1/groups/abc/youtube/videos/VID123"
			}
			req := httptest.NewRequest(http.MethodPatch, urlPath, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			withBearerJWT(t, req, 1)
			w := httptest.NewRecorder()
			r.Setup().ServeHTTP(w, req)

			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), tc.want) {
				t.Errorf("body %q does not mention %q", w.Body.String(), tc.want)
			}
		})
	}
}

func TestPatchGroupYouTubeVideoMetadata_AuthRequired(t *testing.T) {
	r, _ := newMetadataPatchRouter(t, nil, nil)
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/groups/3/youtube/videos/VID123", strings.NewReader(`{"platform_account_id": 42, "title": "T"}`))
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", w.Code, w.Body.String())
	}
}

func TestPatchGroupYouTubeVideoMetadata_AccountNotInGroup404(t *testing.T) {
	r, _ := newMetadataPatchRouter(t, nil, nil)
	w := patchVideoRequest(t, r, 3, "VID123", `{"platform_account_id": 999, "title": "T"}`)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestPatchGroupYouTubeVideoMetadata_VideoNotFound404(t *testing.T) {
	updateFn := func(ctx context.Context, accessToken, videoID, expectedChannelID string, patch models.YouTubeMetadataPatch) (*models.YouTubeMetadataResult, error) {
		return nil, fmt.Errorf("%w: video_id=%s", services.ErrYouTubeVideoNotFound, videoID)
	}
	r, _ := newMetadataPatchRouter(t, updateFn, func(ctx context.Context, id int64, tt string) (*models.OAuthToken, error) {
		return &models.OAuthToken{AccessToken: "tok"}, nil
	})
	w := patchVideoRequest(t, r, 3, "VID-MISSING", `{"platform_account_id": 42, "title": "T"}`)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestPatchGroupYouTubeVideoMetadata_Forbidden403(t *testing.T) {
	updateFn := func(ctx context.Context, accessToken, videoID, expectedChannelID string, patch models.YouTubeMetadataPatch) (*models.YouTubeMetadataResult, error) {
		return nil, &services.YouTubeAPIError{StatusCode: http.StatusForbidden, Category: "auth", Message: "not owned"}
	}
	r, _ := newMetadataPatchRouter(t, updateFn, func(ctx context.Context, id int64, tt string) (*models.OAuthToken, error) {
		return &models.OAuthToken{AccessToken: "tok"}, nil
	})
	w := patchVideoRequest(t, r, 3, "VID123", `{"platform_account_id": 42, "title": "T"}`)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestPatchGroupYouTubeVideoMetadata_TokenError502(t *testing.T) {
	r, _ := newMetadataPatchRouter(t, nil, func(ctx context.Context, id int64, tt string) (*models.OAuthToken, error) {
		return nil, errors.New("no valid token")
	})
	w := patchVideoRequest(t, r, 3, "VID123", `{"platform_account_id": 42, "title": "T"}`)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d: %s", w.Code, w.Body.String())
	}
}

func TestPatchGroupYouTubeVideoMetadata_Upstream502(t *testing.T) {
	updateFn := func(ctx context.Context, accessToken, videoID, expectedChannelID string, patch models.YouTubeMetadataPatch) (*models.YouTubeMetadataResult, error) {
		return nil, &services.YouTubeAPIError{StatusCode: 0, Category: "network", Message: "boom"}
	}
	r, _ := newMetadataPatchRouter(t, updateFn, func(ctx context.Context, id int64, tt string) (*models.OAuthToken, error) {
		return &models.OAuthToken{AccessToken: "tok"}, nil
	})
	w := patchVideoRequest(t, r, 3, "VID123", `{"platform_account_id": 42, "title": "T"}`)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d: %s", w.Code, w.Body.String())
	}
}

func TestPatchGroupYouTubeVideoMetadata_CrossTenant404(t *testing.T) {
	workspace := &models.Workspace{ID: 7, OwnerID: 99}
	group := &models.Group{ID: 3, WorkspaceID: workspace.ID, Name: "Marketing"}
	groupStore := &mockGroupStore{
		findByIDFn: func(id int64) (*models.Group, error) {
			if id == group.ID {
				return group, nil
			}
			return nil, nil
		},
	}
	ytSvc := &mockYouTubeOAuthServiceForEditor{
		updateVideoMetadataFn: func(ctx context.Context, accessToken, videoID, expectedChannelID string, patch models.YouTubeMetadataPatch) (*models.YouTubeMetadataResult, error) {
			t.Errorf("UpdateVideoMetadata must NOT be called on cross-tenant 404")
			return nil, nil
		},
	}
	editStore := &mockYouTubeVideoEditStore{}
	vault := &mockCredentialVault{}
	r := newGroupVideosRouter(t, workspace, groupStore, nil, editStore, ytSvc, vault)

	w := patchVideoRequest(t, r, 3, "VID123", `{"platform_account_id": 42, "title": "T"}`)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}
