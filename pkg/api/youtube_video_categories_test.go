package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// newVideoCategoriesRouter wires the handler's dependencies: the
// caller's workspace (ListByOwner → workspace 7), one group with one
// ACTIVE YouTube account (42), and the vault + service under test.
// Pass non-nil stores to override the happy-path defaults.
func newVideoCategoriesRouter(
	t *testing.T,
	listFn func(ctx context.Context, accessToken, regionCode string) ([]services.YouTubeVideoCategory, error),
	vaultGetFn func(ctx context.Context, id int64, tt string) (*models.OAuthToken, error),
	workspaceStore *mockWorkspaceStore,
	groupStore *mockGroupStore,
	userStore *mockUserStore,
) *Router {
	t.Helper()
	ytAccount := &models.PlatformAccount{
		ID:             42,
		UserID:         1,
		Platform:       models.PlatformYouTube,
		PlatformUserID: "UC123",
		Username:       "testchannel",
		Status:         models.AccountStatusActive,
	}
	if workspaceStore == nil {
		workspaceStore = &mockWorkspaceStore{
			listByOwnerFn: func(ownerID int64) ([]models.Workspace, error) {
				return []models.Workspace{{ID: 7, OwnerID: ownerID}}, nil
			},
		}
	}
	if groupStore == nil {
		groupStore = &mockGroupStore{
			listByWorkspaceFn: func(workspaceID int64) ([]models.Group, error) {
				return []models.Group{{ID: 3, WorkspaceID: workspaceID, Name: "Marketing"}}, nil
			},
			listAccountsInGroupFn: func(groupID int64) ([]int64, error) {
				return []int64{ytAccount.ID}, nil
			},
		}
	}
	if userStore == nil {
		userStore = &mockUserStore{
			findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
				if id == ytAccount.ID {
					return ytAccount, nil
				}
				return nil, nil
			},
		}
	}
	ytSvc := &mockYouTubeOAuthServiceForEditor{listVideoCategoriesFn: listFn}
	vault := &mockCredentialVault{getFn: vaultGetFn}
	return mustNewRouterWithDefaults(
		services.NewCapabilityRouter(),
		userStore,
		auth.NewManager(testJWTSecret, 24),
		"https://app.instaedit.org",
		nil,
		WithWorkspaceStore(workspaceStore),
		WithGroupStore(groupStore),
		WithYouTubeService(ytSvc),
		WithCredentialVault(vault),
	)
}

func videoCategoriesRequest(t *testing.T, r *Router, query string, withJWT bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/youtube/video-categories"+query, nil)
	if withJWT {
		withBearerJWT(t, req, 1)
	}
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	return w
}

func TestHandleListYouTubeVideoCategories_HappyPath(t *testing.T) {
	var gotToken, gotRegion string
	listFn := func(ctx context.Context, accessToken, regionCode string) ([]services.YouTubeVideoCategory, error) {
		gotToken = accessToken
		gotRegion = regionCode
		return []services.YouTubeVideoCategory{
			{ID: "24", Label: "Intrattenimento"},
			{ID: "17", Label: "Sport"},
		}, nil
	}
	r := newVideoCategoriesRouter(t, listFn, func(ctx context.Context, id int64, tt string) (*models.OAuthToken, error) {
		return &models.OAuthToken{AccessToken: "valid-token"}, nil
	}, nil, nil, nil)

	w := videoCategoriesRequest(t, r, "?region_code=IT", true)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if gotToken != "valid-token" {
		t.Errorf("token: want valid-token, got %q", gotToken)
	}
	if gotRegion != "IT" {
		t.Errorf("region: want IT, got %q", gotRegion)
	}
	var resp struct {
		Categories []services.YouTubeVideoCategory `json:"categories"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Categories) != 2 || resp.Categories[0].ID != "24" || resp.Categories[0].Label != "Intrattenimento" {
		t.Errorf("categories: got %+v", resp.Categories)
	}
}

func TestHandleListYouTubeVideoCategories_RegionOptional(t *testing.T) {
	var gotRegion string
	listFn := func(ctx context.Context, accessToken, regionCode string) ([]services.YouTubeVideoCategory, error) {
		gotRegion = regionCode
		return []services.YouTubeVideoCategory{}, nil
	}
	r := newVideoCategoriesRouter(t, listFn, func(ctx context.Context, id int64, tt string) (*models.OAuthToken, error) {
		return &models.OAuthToken{AccessToken: "valid-token"}, nil
	}, nil, nil, nil)

	w := videoCategoriesRequest(t, r, "", true)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if gotRegion != "" {
		t.Errorf("region: want empty (global default), got %q", gotRegion)
	}
	// The categories array must be present, never null.
	if !json.Valid(w.Body.Bytes()) || !strings.Contains(w.Body.String(), `"categories":[]`) {
		t.Errorf("body: want categories:[]; got %s", w.Body.String())
	}
}

func TestHandleListYouTubeVideoCategories_Validation(t *testing.T) {
	cases := []string{"?region_code=ITL", "?region_code=I", "?region_code=1T"}
	for _, query := range cases {
		t.Run(query, func(t *testing.T) {
			r := newVideoCategoriesRouter(t, func(ctx context.Context, accessToken, regionCode string) ([]services.YouTubeVideoCategory, error) {
				t.Error("service must not be reached for an invalid region_code")
				return nil, nil
			}, func(ctx context.Context, id int64, tt string) (*models.OAuthToken, error) {
				t.Error("vault must not be reached for an invalid region_code")
				return nil, nil
			}, nil, nil, nil)

			w := videoCategoriesRequest(t, r, query, true)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), "region_code") {
				t.Errorf("body %q does not mention region_code", w.Body.String())
			}
		})
	}
}

func TestHandleListYouTubeVideoCategories_AuthRequired(t *testing.T) {
	r := newVideoCategoriesRouter(t, nil, nil, nil, nil, nil)
	w := videoCategoriesRequest(t, r, "?region_code=IT", false)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleListYouTubeVideoCategories_NoAccount404(t *testing.T) {
	workspaceStore := &mockWorkspaceStore{
		listByOwnerFn: func(ownerID int64) ([]models.Workspace, error) {
			return nil, nil
		},
	}
	r := newVideoCategoriesRouter(t, func(ctx context.Context, accessToken, regionCode string) ([]services.YouTubeVideoCategory, error) {
		t.Error("service must not be reached without an account")
		return nil, nil
	}, func(ctx context.Context, id int64, tt string) (*models.OAuthToken, error) {
		t.Error("vault must not be reached without an account")
		return nil, nil
	}, workspaceStore, nil, nil)

	w := videoCategoriesRequest(t, r, "", true)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "nessun account") {
		t.Errorf("body %q does not mention the missing account", w.Body.String())
	}
}

func TestHandleListYouTubeVideoCategories_PicksActiveYouTubeAccount(t *testing.T) {
	// Account 1 is disabled (skipped); account 42 is the active one.
	groupStore := &mockGroupStore{
		listByWorkspaceFn: func(workspaceID int64) ([]models.Group, error) {
			return []models.Group{{ID: 3, WorkspaceID: workspaceID}}, nil
		},
		listAccountsInGroupFn: func(groupID int64) ([]int64, error) {
			return []int64{1, 42}, nil
		},
	}
	userStore := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			if id == 1 {
				return &models.PlatformAccount{ID: 1, UserID: 1, Platform: models.PlatformYouTube, Status: models.AccountStatusExpired}, nil
			}
			return &models.PlatformAccount{ID: 42, UserID: 1, Platform: models.PlatformYouTube, Status: models.AccountStatusActive}, nil
		},
	}
	var renewedAccount int64
	var renewedType string
	r := newVideoCategoriesRouter(t,
		func(ctx context.Context, accessToken, regionCode string) ([]services.YouTubeVideoCategory, error) {
			return []services.YouTubeVideoCategory{}, nil
		},
		func(ctx context.Context, id int64, tt string) (*models.OAuthToken, error) {
			renewedAccount = id
			renewedType = tt
			return &models.OAuthToken{AccessToken: "valid-token"}, nil
		},
		nil, groupStore, userStore,
	)

	w := videoCategoriesRequest(t, r, "", true)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if renewedAccount != 42 {
		t.Errorf("renew account: want 42 (active), got %d", renewedAccount)
	}
	if renewedType != models.TokenTypeBearer {
		t.Errorf("renew type: want %s, got %s", models.TokenTypeBearer, renewedType)
	}
}

func TestHandleListYouTubeVideoCategories_ListWorkspacesError500(t *testing.T) {
	workspaceStore := &mockWorkspaceStore{
		listByOwnerFn: func(ownerID int64) ([]models.Workspace, error) {
			return nil, errors.New("boom")
		},
	}
	r := newVideoCategoriesRouter(t, nil, nil, workspaceStore, nil, nil)

	w := videoCategoriesRequest(t, r, "", true)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleListYouTubeVideoCategories_TokenError502(t *testing.T) {
	r := newVideoCategoriesRouter(t,
		func(ctx context.Context, accessToken, regionCode string) ([]services.YouTubeVideoCategory, error) {
			t.Error("service must not be reached when the token renew fails")
			return nil, nil
		},
		func(ctx context.Context, id int64, tt string) (*models.OAuthToken, error) {
			return nil, errors.New("boom")
		},
		nil, nil, nil,
	)

	w := videoCategoriesRequest(t, r, "", true)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleListYouTubeVideoCategories_Upstream502(t *testing.T) {
	r := newVideoCategoriesRouter(t,
		func(ctx context.Context, accessToken, regionCode string) ([]services.YouTubeVideoCategory, error) {
			return nil, &services.YouTubeAPIError{StatusCode: http.StatusInternalServerError, Category: "server_error", Message: "boom"}
		},
		func(ctx context.Context, id int64, tt string) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "valid-token"}, nil
		},
		nil, nil, nil,
	)

	w := videoCategoriesRequest(t, r, "", true)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandleListYouTubeVideoCategories_RateLimit429(t *testing.T) {
	r := newVideoCategoriesRouter(t,
		func(ctx context.Context, accessToken, regionCode string) ([]services.YouTubeVideoCategory, error) {
			return nil, &services.YouTubeAPIError{StatusCode: http.StatusTooManyRequests, Category: "rate_limit", Message: "rate"}
		},
		func(ctx context.Context, id int64, tt string) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "valid-token"}, nil
		},
		nil, nil, nil,
	)

	w := videoCategoriesRequest(t, r, "", true)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d: %s", w.Code, w.Body.String())
	}
}
