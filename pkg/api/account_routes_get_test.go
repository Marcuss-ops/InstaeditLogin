package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

func TestHandleGetAccount_Happy(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	owner := ownedAccountFixture(1, "instagram")
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			if id != 21 {
				t.Errorf("handler called FindPlatformAccountByID with id=%d, want 21 (path param)", id)
			}
			return owner, nil
		},
	}
	r := newTestRouter(svc, store, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/21", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		ID             int64     `json:"id"`
		Platform       string    `json:"platform"`
		PlatformUserID string    `json:"platform_user_id"`
		Username       string    `json:"username"`
		Status         string    `json:"status"`
		CreatedAt      time.Time `json:"created_at"`
		UserID         int64     `json:"user_id,omitempty"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if resp.ID != 21 || resp.Platform != "instagram" || resp.Username != "alice_instagram" {
		t.Errorf("response shape mismatch: %+v", resp)
	}
	if resp.UserID != 0 {
		t.Errorf("internal user_id leaked: %d", resp.UserID)
	}
}

// TestHandleGetAccount_NotFound_404 covers both the genuine-not-found
// and the cross-tenant cases under one roof (the loadOwnAccountByID
// helper collapses them by design — 404 prevents existence leaks).
func TestHandleGetAccount_NotFound_404(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return nil, nil // genuine not-found
		},
	}
	r := newTestRouter(svc, store, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/999", nil)
	w := httptest.NewRecorder()
	// JWT for user 1, but no row exists for id=999.
	jwt := issueTestJWT(t, 1)
	req.Header.Set("Authorization", "Bearer "+jwt)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404 (account not found), got %d: %s", w.Code, w.Body.String())
	}
}

// TestHandleGetAccount_CrossTenant_404 is the workspace-isolation
// canary: an account owned by user 999 MUST NOT be returned when the
// caller is user 1. The 404 (not 403) is critical — 403 would confirm
// to a probe that the id exists but is cross-tenant, leaking the
// existence of accounts in other user boundaries.
func TestHandleGetAccount_CrossTenant_404(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	crossTenant := ownedAccountFixture(999, "instagram")
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return crossTenant, nil // exists, but owned by user 999
		},
	}
	r := newTestRouter(svc, store, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/21", nil)
	w := httptest.NewRecorder()
	// Caller is user 1.
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant probe MUST return 404 (not 403), got %d: %s", w.Code, w.Body.String())
	}
	// Defence-in-depth: response body must NOT echo the cross-tenant
	// owner's id. Plain "account not found" string is the only safe form.
	if strings.Contains(w.Body.String(), "999") {
		t.Errorf("response leaks owned_by user id in body: %s", w.Body.String())
	}
}

// TestHandleGetAccount_NoSession_401 proves r.protected rejects the
// request before the handler runs. The handler's own nil-identity 401
// is defence-in-depth (loadOwnAccountByID returns 401 on nil identity)
// but the route-level middleware is the primary gate.
func TestHandleGetAccount_NoSession_401(t *testing.T) {
	svc := &mockProvider{platform: "instagram"}
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			t.Errorf("FindPlatformAccountByUser MUST NOT be called without a session (data leak risk); got id=%d", id)
			return nil, nil
		},
	}
	r := newTestRouter(svc, store, "")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/21", nil)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req) // NO JWT — session-less probe

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("no-session /accounts/21: want 401, got %d: %s", w.Code, w.Body.String())
	}
}

// TestHandleDisconnectAccount_Happy_204 verifies: 204 No Content + the
// shared-grant check ran (count == 0 → last channel on the grant) +
// vault.Revoke was called + account row was updated to
// status='disconnected' + auditLogStore fired (when present).
func TestHandleGetAccount_WithSnapshot_ResourceIncluded(t *testing.T) {
	svc := &mockProvider{platform: "youtube"}
	owner := ownedAccountFixture(1, "youtube")
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return owner, nil
		},
	}
	snap := &repository.AccountResourceSnapshot{
		PlatformAccountID: 21,
		ResourceType:      "channel",
		Profile: map[string]any{
			"external_id":  "UCtest123",
			"display_name": "Test Channel",
			"handle":       "@test",
			"avatar_url":   "https://example.com/avatar.jpg",
		},
		Statistics: map[string]any{
			"subscribers": map[string]any{
				"label":         "Subscribers",
				"value":         float64(125000),
				"display_value": "125.0K",
			},
		},
		FetchedAt: time.Now(),
	}
	snapStore := &mockSnapshotStore{
		getFn: func(id int64) (*repository.AccountResourceSnapshot, error) {
			return snap, nil
		},
	}
	r := newTestRouter(svc, store, "", WithSnapshotStore(snapStore))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/21", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		ID       int64  `json:"id"`
		Platform string `json:"platform"`
		Resource *struct {
			ResourceType string `json:"resource_type"`
			DisplayName  string `json:"display_name"`
		} `json:"resource"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Resource == nil {
		t.Fatal("resource field should be present when snapshot exists")
	}
	if resp.Resource.ResourceType != "channel" {
		t.Errorf("resource.resource_type: want channel, got %q", resp.Resource.ResourceType)
	}
	if resp.Resource.DisplayName != "Test Channel" {
		t.Errorf("resource.display_name: want Test Channel, got %q", resp.Resource.DisplayName)
	}
}

// TestHandleGetAccount_NoSnapshot_NoResource proves that when no snapshot
// exists, the response omits the resource field.
func TestHandleGetAccount_NoSnapshot_NoResource(t *testing.T) {
	svc := &mockProvider{platform: "youtube"}
	owner := ownedAccountFixture(1, "youtube")
	store := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return owner, nil
		},
	}
	snapStore := &mockSnapshotStore{
		getFn: func(id int64) (*repository.AccountResourceSnapshot, error) {
			return nil, nil
		},
	}
	r := newTestRouter(svc, store, "", WithSnapshotStore(snapStore))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/accounts/21", nil)
	w := httptest.NewRecorder()
	withBearerJWT(t, req, 1)
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Resource *struct{} `json:"resource"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Resource != nil {
		t.Error("resource field should be nil/absent when no snapshot exists")
	}
}
