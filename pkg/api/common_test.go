package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// TestAccountContent_Paginates proves that cursor and limit query params
// are forwarded to the provider and the next_cursor is returned to the client.
func TestAccountContent_Paginates(t *testing.T) {
	var gotCursor string
	var gotLimit int
	var gotPrivacy string
	svc := &mockDetailProvider{
		mockProvider: mockProvider{platform: "youtube"},
		contentFn: func(ctx context.Context, accessToken, platformUserID string, cursor string, limit int, privacy string) (*models.AccountContentPage, error) {
			gotCursor = cursor
			gotLimit = limit
			gotPrivacy = privacy
			return &models.AccountContentPage{
				Items: []models.AccountContentItem{
					{ExternalID: "vid1", Title: "Video One"},
					{ExternalID: "vid2", Title: "Video Two"},
				},
				NextCursor: "page-2-token",
			}, nil
		},
	}
	owner := ownedAccountFixture(1, "youtube")
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return owner, nil
		},
	}
	vault := &mockCredentialVault{
		getFn: func(ctx context.Context, id int64, tt string) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "test-token"}, nil
		},
	}
	r := newTestRouter(svc, store, "", WithCredentialVault(vault))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/21/content?cursor=page-1-token&limit=5", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("content: want 200, got %d: %s", w.Code, w.Body.String())
	}
	if gotCursor != "page-1-token" {
		t.Errorf("cursor forwarded: want page-1-token, got %q", gotCursor)
	}
	if gotLimit != 5 {
		t.Errorf("limit forwarded: want 5, got %d", gotLimit)
	}
	if gotPrivacy != "" {
		t.Errorf("privacy forwarded: want empty, got %q", gotPrivacy)
	}

	var resp struct {
		Items []struct {
			ExternalID string `json:"external_id"`
		} `json:"items"`
		NextCursor string `json:"next_cursor"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode content response: %v", err)
	}
	if len(resp.Items) != 2 {
		t.Fatalf("items: want 2, got %d", len(resp.Items))
	}
	if resp.Items[0].ExternalID != "vid1" {
		t.Errorf("first item: want vid1, got %q", resp.Items[0].ExternalID)
	}
	if resp.NextCursor != "page-2-token" {
		t.Errorf("next_cursor: want page-2-token, got %q", resp.NextCursor)
	}
}
