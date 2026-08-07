package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

func TestVeloxProjectBridge_EndToEndSourceOfTruthAuthorizationIdempotencyAndRedirect(t *testing.T) {
	store := &thumbnailProjectTestStore{
		project: &models.ThumbnailProject{
			ID: "thumbproj_contract", WorkspaceID: 7, CreatedBy: 1,
			Status: models.ThumbnailProjectStatusDraft,
		},
	}
	workspaceStore := &mockWorkspaceStore{findByIDFn: func(id int64) (*models.Workspace, error) {
		if id != 7 {
			return &models.Workspace{ID: id, OwnerID: 99}, nil
		}
		return &models.Workspace{ID: id, OwnerID: 1}, nil
	}}
	r := newTestRouter(
		&mockProvider{platform: models.PlatformYouTube}, &mockUserStore{}, "",
		WithWorkspaceStore(workspaceStore),
		WithThumbnailProjectStore(store),
		WithEditorURL("https://instaeditor.example.test/app"),
	)
	h := r.Setup()
	body := `{"workspace_id":7,"velox_project_id":"vx_contract_1"}`

	// The InstaEdit-owned project and workspace are the authorization and
	// source-of-truth gates. The first request creates only the bridge.
	w, req := bridgeRequest(t, http.MethodPost, "/api/v1/thumbnail-projects/thumbproj_contract/velox-bridge", body)
	h.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: want 201, got %d: %s", w.Code, w.Body.String())
	}
	var created veloxProjectBridgeResponse
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.Bridge.ProjectID != "thumbproj_contract" || created.Bridge.WorkspaceID != 7 || created.Bridge.VeloxProjectID != "vx_contract_1" {
		t.Fatalf("bridge is not InstaEdit-scoped: %+v", created.Bridge)
	}
	parsedURL, err := url.Parse(created.EditorURL)
	if err != nil {
		t.Fatalf("parse editor_url: %v", err)
	}
	if parsedURL.Host != "instaeditor.example.test" || parsedURL.Path != "/app/editor/vx_contract_1" {
		t.Fatalf("redirect does not target the separate editor SPA: %q", created.EditorURL)
	}
	if store.bridge == nil || store.bridge.VeloxProjectID != "vx_contract_1" {
		t.Fatalf("InstaEdit store did not receive the opaque bridge: %+v", store.bridge)
	}
	if store.createBridgeCalls != 1 {
		t.Fatalf("first request should persist exactly once, got %d create calls", store.createBridgeCalls)
	}

	// Replaying the same request returns the same authoritative bridge and
	// URL instead of creating a second relation.
	w, req = bridgeRequest(t, http.MethodPost, "/api/v1/thumbnail-projects/thumbproj_contract/velox-bridge", body)
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("equivalent replay: want 200, got %d: %s", w.Code, w.Body.String())
	}
	var replayed veloxProjectBridgeResponse
	if err := json.Unmarshal(w.Body.Bytes(), &replayed); err != nil {
		t.Fatalf("decode replay response: %v", err)
	}
	if replayed.Bridge.ProjectID != created.Bridge.ProjectID || replayed.Bridge.VeloxProjectID != created.Bridge.VeloxProjectID || replayed.EditorURL != created.EditorURL {
		t.Fatalf("replay changed the authoritative bridge: created=%+v replayed=%+v", created, replayed)
	}
	if store.createBridgeCalls != 1 {
		t.Fatalf("equivalent replay must not persist twice, got %d create calls", store.createBridgeCalls)
	}

	// A different Velox handle cannot overwrite the InstaEdit-owned relation.
	w, req = bridgeRequest(t, http.MethodPost, "/api/v1/thumbnail-projects/thumbproj_contract/velox-bridge", `{"workspace_id":7,"velox_project_id":"vx_other"}`)
	h.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("changed replay: want 409, got %d: %s", w.Code, w.Body.String())
	}

	// A caller without authorization cannot use the bridge as a discovery
	// endpoint, and a different workspace cannot see the project.
	unauthenticated := httptest.NewRequest(http.MethodPost, "/api/v1/thumbnail-projects/thumbproj_contract/velox-bridge", bytes.NewBufferString(body))
	unauthenticatedW := httptest.NewRecorder()
	h.ServeHTTP(unauthenticatedW, unauthenticated)
	if unauthenticatedW.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated create: want 401, got %d", unauthenticatedW.Code)
	}
	w, req = bridgeRequest(t, http.MethodPost, "/api/v1/thumbnail-projects/thumbproj_contract/velox-bridge", `{"workspace_id":8,"velox_project_id":"vx_probe"}`)
	h.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("cross-workspace create: want 404, got %d", w.Code)
	}
	if store.createBridgeCalls != 1 {
		t.Fatalf("cross-workspace probe must not persist, got %d create calls", store.createBridgeCalls)
	}
}

func TestVeloxProjectBridge_ContractHasOnlyMinimalContextAndSeparateSPA(t *testing.T) {
	requestType := reflect.TypeOf(createVeloxProjectBridgeRequest{})
	forbiddenFields := map[string]bool{
		"GroupID": true, "GroupIDs": true, "ChannelIDs": true,
		"MemberIDs": true, "WorkspaceSnapshot": true, "UserGroups": true,
	}
	for i := 0; i < requestType.NumField(); i++ {
		if forbiddenFields[requestType.Field(i).Name] {
			t.Fatalf("bridge request contains duplicated ownership field %q", requestType.Field(i).Name)
		}
	}
	for _, field := range []string{"WorkspaceID", "VeloxProjectID", "Platform", "PlatformAccountID", "ChannelID", "VideoID", "Language"} {
		if _, ok := requestType.FieldByName(field); !ok {
			t.Fatalf("minimal bridge request lost field %q", field)
		}
	}

	root := projectRoot(t)
	migration := readContractFile(t, root, "internal", "database", "migrations", "112_velox_project_bridges.sql")
	handler := readContractFile(t, root, "pkg", "api", "velox_project_bridge_handlers.go")
	module := readContractFile(t, root, "pkg", "api", "modules_thumbnail_projects.go")
	for name, content := range map[string]string{"bridge migration": migration, "bridge handler": handler, "thumbnail module": module} {
		lower := strings.ToLower(content)
		for _, forbidden := range []string{
			"group_id", "channel_ids", "member_ids", "velox_workspace",
			"dblink", "postgres_fdw", "attach database", "create database",
			"iframe",
		} {
			if strings.Contains(lower, forbidden) {
				t.Fatalf("%s violates bridge boundary with %q", name, forbidden)
			}
		}
	}
	if strings.Contains(strings.ToLower(handler), "database/sql") || strings.Contains(strings.ToLower(handler), "sql.open") {
		t.Fatal("bridge handler must use the InstaEdit store boundary, not open a shared database")
	}
	if strings.Contains(strings.ToLower(handler), "sync") || strings.Contains(strings.ToLower(module), "sync") {
		t.Fatal("bridge boundary must not implement bidirectional synchronization")
	}
	for _, localTable := range []string{"thumbnail_projects", "workspace_channels", "platform_accounts", "workspaces"} {
		if !strings.Contains(strings.ToLower(migration), localTable) {
			t.Fatalf("bridge migration no longer declares expected InstaEdit-local relation %q", localTable)
		}
	}
}

func projectRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller unavailable")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
}

func readContractFile(t *testing.T, root string, parts ...string) string {
	t.Helper()
	path := filepath.Join(append([]string{root}, parts...)...)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read contract file %s: %v", path, err)
	}
	return string(data)
}
