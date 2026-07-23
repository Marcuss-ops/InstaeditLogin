package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// =============================================================================
// P0#4 — workspace_channels handler tests
// =============================================================================
//
// These tests mirror the style of pkg/api/workspaces_test.go +
// pkg/api/groups.go's test coverage. They cover:
//   - Happy path on POST/GET/PATCH/DELETE.
//   - Cross-tenant ownership guard returns 404 (not 403) — existence-leak
//     avoidance (mirrors handleGetWorkspace policy).
//   - Bad path params return 400.
//   - Missing body fields return 400.
//   - Update with no fields returns 400.
//   - Detach of a non-existent binding returns 404.
//   - No JWT returns 401 (the protected middleware short-circuit).
//
// The mock workspace store is configured per-test with function fields
// (attachChFn, listChannelsFn, etc.) added in P0#4. The cross-tenant
// case uses findByIDFn returning a workspace owned by a DIFFERENT
// user so requireWorkspaceOwnership rejects with 404.
// =============================================================================

// workspaceChannelsRouter builds a minimal Router wired with the
// supplied mockWorkspaceStore and the auth manager from routes_test.go.
// Re-uses the JWT helpers from the existing test files.
func workspaceChannelsRouter(store *mockWorkspaceStore) *Router {
	return NewRouter(
		nil, // capRouter — unused by channel handlers
		&mockUserStore{},
		auth.NewManager(testJWTSecret, 24),
		"",
		nil,
		WithWorkspaceStore(store), WithOneTimeCodeStore(NewInMemoryOneTimeCodeStore(60*time.Second)))
}

// TestHandleAttachWorkspaceChannel_HappyPath verifies the basic POST
// success: the binding is created (or upserted) and the response body
// contains the persisted row.
func TestHandleAttachWorkspaceChannel_HappyPath(t *testing.T) {
	store := &mockWorkspaceStore{
		findByIDFn: func(id int64) (*models.Workspace, error) {
			return &models.Workspace{ID: id, OwnerID: 1}, nil
		},
		attachChFn: func(ctx context.Context, wsID, accID int64, groupName string) (*models.WorkspaceChannel, error) {
			return &models.WorkspaceChannel{
				WorkspaceID:       wsID,
				PlatformAccountID: accID,
				GroupName:         groupName,
				Enabled:           true,
			}, nil
		},
	}
	r := workspaceChannelsRouter(store)

	body := `{"platform_account_id": 42, "group_name": "editorial"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/1/channels", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("want 201 Created, got %d: %s", w.Code, w.Body.String())
	}
	var got models.WorkspaceChannel
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.WorkspaceID != 1 || got.PlatformAccountID != 42 || got.GroupName != "editorial" {
		t.Errorf("unexpected response: %+v", got)
	}
	if !got.Enabled {
		t.Errorf("enabled: want true on first insert, got false")
	}
}

// TestHandleAttachWorkspaceChannel_CrossTenant_404 ensures the
// requireWorkspaceOwnership helper rejects with 404 (not 403) when
// the caller is not the workspace owner. Existence-leak avoidance.
func TestHandleAttachWorkspaceChannel_CrossTenant_404(t *testing.T) {
	store := &mockWorkspaceStore{
		findByIDFn: func(id int64) (*models.Workspace, error) {
			// Owner 999, NOT caller 1.
			return &models.Workspace{ID: id, OwnerID: 999}, nil
		},
		attachChFn: func(ctx context.Context, wsID, accID int64, groupName string) (*models.WorkspaceChannel, error) {
			t.Errorf("AttachChannel MUST NOT be called when the workspace is foreign-owned; got wsID=%d accID=%d", wsID, accID)
			return nil, nil
		},
	}
	r := workspaceChannelsRouter(store)

	body := `{"platform_account_id": 42}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/1/channels", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("want 404 (cross-owner existence-leak avoidance), got %d: %s", w.Code, w.Body.String())
	}
}

// TestHandleAttachWorkspaceChannel_MissingPlatformAccountID verifies
// the body-validation guard: platform_account_id <= 0 returns 400
// before any SQL touches the database.
func TestHandleAttachWorkspaceChannel_MissingPlatformAccountID(t *testing.T) {
	store := &mockWorkspaceStore{
		findByIDFn: func(id int64) (*models.Workspace, error) {
			return &models.Workspace{ID: id, OwnerID: 1}, nil
		},
		attachChFn: func(ctx context.Context, wsID, accID int64, groupName string) (*models.WorkspaceChannel, error) {
			t.Errorf("AttachChannel MUST NOT be called with missing platform_account_id; got accID=%d", accID)
			return nil, nil
		},
	}
	r := workspaceChannelsRouter(store)

	body := `{"group_name": "x"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces/1/channels", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400 BadRequest, got %d: %s", w.Code, w.Body.String())
	}
}

// TestHandleListWorkspaceChannels_HappyPath verifies the GET path
// returns a JSON envelope {"channels": [...]} with the rows from
// the workspace.
func TestHandleListWorkspaceChannels_HappyPath(t *testing.T) {
	store := &mockWorkspaceStore{
		findByIDFn: func(id int64) (*models.Workspace, error) {
			return &models.Workspace{ID: id, OwnerID: 1}, nil
		},
		listChannelsFn: func(ctx context.Context, wsID int64) ([]models.WorkspaceChannel, error) {
			return []models.WorkspaceChannel{
				{WorkspaceID: wsID, PlatformAccountID: 11, GroupName: "a", Enabled: true},
				{WorkspaceID: wsID, PlatformAccountID: 22, GroupName: "b", Enabled: false},
			}, nil
		},
	}
	r := workspaceChannelsRouter(store)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/1/channels", nil)
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var envelope struct {
		Channels []models.WorkspaceChannel `json:"channels"`
	}
	if err := json.NewDecoder(w.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(envelope.Channels) != 2 {
		t.Errorf("want 2 channels, got %d", len(envelope.Channels))
	}
}

// TestHandleListWorkspaceChannels_EmptyArrayReturnsEmptyList verifies
// that an empty workspace returns "channels":[] rather than
// "channels":null — JSON-encode nil slice as empty.
func TestHandleListWorkspaceChannels_EmptyArrayReturnsEmptyList(t *testing.T) {
	store := &mockWorkspaceStore{
		findByIDFn: func(id int64) (*models.Workspace, error) {
			return &models.Workspace{ID: id, OwnerID: 1}, nil
		},
		listChannelsFn: func(ctx context.Context, wsID int64) ([]models.WorkspaceChannel, error) {
			return nil, nil // simulates "no rows"
		},
	}
	r := workspaceChannelsRouter(store)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/workspaces/1/channels", nil)
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if !bytes.Contains(w.Body.Bytes(), []byte(`"channels":[]`)) {
		t.Errorf("empty workspace must return channels:[], got %s", w.Body.String())
	}
}

// TestHandleUpdateWorkspaceChannel_HappyPath verifies that a PATCH
// with both fields updates the binding and returns the merged row.
func TestHandleUpdateWorkspaceChannel_HappyPath(t *testing.T) {
	var capturedGroup *string
	var capturedEnabled *bool
	store := &mockWorkspaceStore{
		findByIDFn: func(id int64) (*models.Workspace, error) {
			return &models.Workspace{ID: id, OwnerID: 1}, nil
		},
		updateChFn: func(ctx context.Context, wsID, accID int64, groupName *string, enabled *bool) error {
			capturedGroup = groupName
			capturedEnabled = enabled
			return nil
		},
		// PK-indexed read-back: return the merged row.
		findChannelFn: func(ctx context.Context, wsID, accID int64) (*models.WorkspaceChannel, error) {
			return &models.WorkspaceChannel{
				WorkspaceID:       wsID,
				PlatformAccountID: accID,
				GroupName:         "new-group",
				Enabled:           false,
			}, nil
		},
	}
	r := workspaceChannelsRouter(store)

	body := `{"group_name": "new-group", "enabled": false}`
	req := httptest.NewRequest(http.MethodPatch, "/api/v1/workspaces/1/channels/11", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	if capturedGroup == nil || *capturedGroup != "new-group" {
		t.Errorf("groupName: want pointer-to-new-group, got %v", capturedGroup)
	}
	if capturedEnabled == nil || *capturedEnabled != false {
		t.Errorf("enabled: want pointer-to-false, got %v", capturedEnabled)
	}
	var got models.WorkspaceChannel
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.GroupName != "new-group" || got.Enabled {
		t.Errorf("read-back mismatch: %+v", got)
	}
}

// TestHandleUpdateWorkspaceChannel_NoFields_400 ensures an empty
// PATCH body (no group_name AND no enabled) returns 400 — there
// must be at least one field to update. Prevents a no-op write.
func TestHandleUpdateWorkspaceChannel_NoFields_400(t *testing.T) {
	store := &mockWorkspaceStore{
		findByIDFn: func(id int64) (*models.Workspace, error) {
			return &models.Workspace{ID: id, OwnerID: 1}, nil
		},
		updateChFn: func(ctx context.Context, wsID, accID int64, groupName *string, enabled *bool) error {
			t.Errorf("UpdateChannel MUST NOT be called when both fields are absent")
			return nil
		},
	}
	r := workspaceChannelsRouter(store)

	req := httptest.NewRequest(http.MethodPatch, "/api/v1/workspaces/1/channels/11", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400 BadRequest (no fields), got %d: %s", w.Code, w.Body.String())
	}
}

// TestHandleDetachWorkspaceChannel_HappyPath verifies DELETE returns
// 204 No Content when the binding is removed.
func TestHandleDetachWorkspaceChannel_HappyPath(t *testing.T) {
	var calledWith struct {
		wsID, accID int64
	}
	store := &mockWorkspaceStore{
		findByIDFn: func(id int64) (*models.Workspace, error) {
			return &models.Workspace{ID: id, OwnerID: 1}, nil
		},
		detachChFn: func(ctx context.Context, wsID, accID int64) error {
			calledWith.wsID = wsID
			calledWith.accID = accID
			return nil
		},
	}
	r := workspaceChannelsRouter(store)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/workspaces/1/channels/42", nil)
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("want 204 No Content, got %d: %s", w.Code, w.Body.String())
	}
	if calledWith.wsID != 1 || calledWith.accID != 42 {
		t.Errorf("detach called with wsID=%d accID=%d, want 1,42", calledWith.wsID, calledWith.accID)
	}
}

// TestHandleDetachWorkspaceChannel_MissingBinding_404 verifies that
// DELETE of a non-existent binding returns 404, not 204. The repo
// returns ErrWorkspaceNotFound when RowsAffected==0; the handler
// maps that to 404.
func TestHandleDetachWorkspaceChannel_MissingBinding_404(t *testing.T) {
	store := &mockWorkspaceStore{
		findByIDFn: func(id int64) (*models.Workspace, error) {
			return &models.Workspace{ID: id, OwnerID: 1}, nil
		},
		detachChFn: func(ctx context.Context, wsID, accID int64) error {
			return repository.ErrWorkspaceNotFound
		},
	}
	r := workspaceChannelsRouter(store)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/workspaces/1/channels/42", nil)
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d: %s", w.Code, w.Body.String())
	}
}

// TestHandleWorkspaceChannel_BadPathID_400 verifies that non-numeric
// {id} or {accountId} segments return 400 before any handler runs.
//
// Two flavours:
//   - bad workspace id → FindByID must NEVER be called (the path
//     parser writes 400 before reaching the handler body).
//   - bad accountId with good workspace id → FindByID IS called and
//     must return a valid workspace (so the ownership check passes);
//     the bad accountId parse must surface as 400 from the handler.
func TestHandleWorkspaceChannel_BadPathID_400(t *testing.T) {
	cases := []struct {
		name         string
		method, path string
		body         string
		findByIDFn   func(id int64) (*models.Workspace, error)
	}{
		{
			name:   "POST bad workspace id",
			method: http.MethodPost, path: "/api/v1/workspaces/not-a-number/channels",
			body: `{"platform_account_id": 42}`,
			findByIDFn: func(id int64) (*models.Workspace, error) {
				t.Errorf("FindByID MUST NOT be called with bad workspace id; got id=%d", id)
				return nil, nil
			},
		},
		{
			name:   "GET bad workspace id",
			method: http.MethodGet, path: "/api/v1/workspaces/abc/channels",
			body: "",
			findByIDFn: func(id int64) (*models.Workspace, error) {
				t.Errorf("FindByID MUST NOT be called with bad workspace id; got id=%d", id)
				return nil, nil
			},
		},
		{
			name:   "PATCH bad account id",
			method: http.MethodPatch, path: "/api/v1/workspaces/1/channels/abc",
			body: `{"enabled": false}`,
			findByIDFn: func(id int64) (*models.Workspace, error) {
				return &models.Workspace{ID: id, OwnerID: 1}, nil
			},
		},
		{
			name:   "DELETE bad account id",
			method: http.MethodDelete, path: "/api/v1/workspaces/1/channels/xyz",
			body: "",
			findByIDFn: func(id int64) (*models.Workspace, error) {
				return &models.Workspace{ID: id, OwnerID: 1}, nil
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			store := &mockWorkspaceStore{findByIDFn: c.findByIDFn}
			r := workspaceChannelsRouter(store)

			var rdr io.Reader
			if c.body != "" {
				rdr = bytes.NewReader([]byte(c.body))
			}
			req := httptest.NewRequest(c.method, c.path, rdr)
			if c.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			withBearerJWT(t, req, 1)
			w := httptest.NewRecorder()
			r.Setup().ServeHTTP(w, req)
			if w.Code != http.StatusBadRequest {
				t.Errorf("%s %s: want 400 (bad path id), got %d: %s", c.method, c.path, w.Code, w.Body.String())
			}
		})
	}
}

// TestHandleWorkspaceChannel_NoJWT_401 verifies the auth middleware
// short-circuits all four endpoints when no JWT is supplied.
func TestHandleWorkspaceChannel_NoJWT_401(t *testing.T) {
	store := &mockWorkspaceStore{
		findByIDFn: func(id int64) (*models.Workspace, error) {
			t.Errorf("FindByID MUST NOT be called when no JWT is present")
			return nil, nil
		},
	}
	r := workspaceChannelsRouter(store)

	cases := []struct {
		method, path string
		body         string
	}{
		{http.MethodPost, "/api/v1/workspaces/1/channels", `{"platform_account_id":42}`},
		{http.MethodGet, "/api/v1/workspaces/1/channels", ""},
		{http.MethodPatch, "/api/v1/workspaces/1/channels/11", `{"enabled":false}`},
		{http.MethodDelete, "/api/v1/workspaces/1/channels/42", ""},
	}
	for _, c := range cases {
		var rdr io.Reader
		if c.body != "" {
			rdr = bytes.NewReader([]byte(c.body))
		} else {
			rdr = nil
		}
		req := httptest.NewRequest(c.method, c.path, rdr)
		if c.body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		// No withBearerJWT — short-circuit on 401.
		w := httptest.NewRecorder()
		r.Setup().ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s no-jwt: want 401, got %d: %s", c.method, c.path, w.Code, w.Body.String())
		}
	}
}

// Ensure mockUserStore satisfies the interface — keeps the test
// file self-contained against future signature changes.
var _ UserStore = (*mockUserStore)(nil)
