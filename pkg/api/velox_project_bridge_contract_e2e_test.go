package api

import (
	"bytes"
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
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
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
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
	editorService := &fakeEditorService{}
	editorService.createProjectFn = func(_ context.Context, req services.CreateEditorProjectRequest) (*services.EditorProject, error) {
		if store.bridge != nil {
			if store.bridge.ProjectID != req.ApplicationProjectID || store.bridge.WorkspaceID != req.WorkspaceID {
				return nil, services.ErrEditorProjectNotFound
			}
			if store.bridge.ExternalProjectID != req.ExternalProjectID {
				return nil, services.ErrEditorProjectConflict
			}
			return &services.EditorProject{ApplicationProjectID: req.ApplicationProjectID, WorkspaceID: req.WorkspaceID, ExternalProjectID: store.bridge.ExternalProjectID, Created: false}, nil
		}
		externalID := req.ExternalProjectID
		if externalID == "" {
			externalID = "vx_contract_1"
		}
		store.bridge = &models.VeloxProjectBridge{ProjectID: req.ApplicationProjectID, WorkspaceID: req.WorkspaceID, ExternalProjectID: externalID, EditorProvider: "velox"}
		return &services.EditorProject{ApplicationProjectID: req.ApplicationProjectID, WorkspaceID: req.WorkspaceID, ExternalProjectID: externalID, Created: true}, nil
	}

	r := newTestRouter(
		&mockProvider{platform: models.PlatformYouTube}, &mockUserStore{}, "",
		WithWorkspaceStore(workspaceStore),
		WithThumbnailProjectStore(store),
		WithEditorURL("https://instaeditor.example.test/app"),
		WithEditorService(editorService),
	)
	h := r.Setup()
	body := `{"contract_version":"instaedit.velox.project-bridge.v1","workspace_id":7}`
	replayBody := `{"contract_version":"instaedit.velox.project-bridge.v1","workspace_id":7,"external_project_id":"vx_contract_1"}`

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
	if created.ContractVersion != models.ProjectBridgeContractVersion {
		t.Fatalf("unexpected bridge contract version: %q", created.ContractVersion)
	}
	if created.Bridge.ProjectID != "thumbproj_contract" || created.Bridge.WorkspaceID != 7 || created.Bridge.ExternalProjectID != "vx_contract_1" {
		t.Fatalf("bridge is not InstaEdit-scoped: %+v", created.Bridge)
	}
	if created.Bridge.EditorProvider != "velox" {
		t.Fatalf("bridge editor_provider must default to the current editor backend: %q", created.Bridge.EditorProvider)
	}
	parsedURL, err := url.Parse(created.EditorURL)
	if err != nil {
		t.Fatalf("parse editor_url: %v", err)
	}
	if parsedURL.Host != "instaeditor.example.test" || parsedURL.Path != "/app/editor/vx_contract_1" {
		t.Fatalf("redirect does not target the separate editor SPA: %q", created.EditorURL)
	}
	if store.bridge == nil || store.bridge.ExternalProjectID != "vx_contract_1" {
		t.Fatalf("InstaEdit store did not receive the opaque bridge: %+v", store.bridge)
	}
	if len(editorService.calls) != 1 || store.createBridgeCalls != 0 {
		t.Fatalf("first request must use EditorService exactly once and bypass legacy persistence: service_calls=%d store_calls=%d", len(editorService.calls), store.createBridgeCalls)
	}

	// Replaying the same request returns the same authoritative bridge and
	// URL instead of creating a second relation.
	w, req = bridgeRequest(t, http.MethodPost, "/api/v1/thumbnail-projects/thumbproj_contract/velox-bridge", replayBody)
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("equivalent replay: want 200, got %d: %s", w.Code, w.Body.String())
	}
	var replayed veloxProjectBridgeResponse
	if err := json.Unmarshal(w.Body.Bytes(), &replayed); err != nil {
		t.Fatalf("decode replay response: %v", err)
	}
	if replayed.Bridge.ProjectID != created.Bridge.ProjectID || replayed.Bridge.ExternalProjectID != created.Bridge.ExternalProjectID || replayed.EditorURL != created.EditorURL {
		t.Fatalf("replay changed the authoritative bridge: created=%+v replayed=%+v", created, replayed)
	}
	if len(editorService.calls) != 2 || store.createBridgeCalls != 0 {
		t.Fatalf("equivalent replay must use service and bypass legacy persistence: service_calls=%d store_calls=%d", len(editorService.calls), store.createBridgeCalls)
	}

	// A different Velox handle cannot overwrite the InstaEdit-owned relation.
	w, req = bridgeRequest(t, http.MethodPost, "/api/v1/thumbnail-projects/thumbproj_contract/velox-bridge", `{"contract_version":"instaedit.velox.project-bridge.v1","workspace_id":7,"external_project_id":"vx_other"}`)
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
	serviceCallsBeforeCrossWorkspace := len(editorService.calls)
	w, req = bridgeRequest(t, http.MethodPost, "/api/v1/thumbnail-projects/thumbproj_contract/velox-bridge", `{"contract_version":"instaedit.velox.project-bridge.v1","workspace_id":8,"external_project_id":"vx_probe"}`)
	h.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("cross-workspace create: want 404, got %d", w.Code)
	}
	if len(editorService.calls) != serviceCallsBeforeCrossWorkspace || store.createBridgeCalls != 0 {
		t.Fatalf("cross-workspace probe must not call the editor service or persist through legacy store: service_calls=%d before=%d store_calls=%d", len(editorService.calls), serviceCallsBeforeCrossWorkspace, store.createBridgeCalls)
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
	for _, field := range []string{"WorkspaceID", "ExternalProjectID"} {
		if _, ok := requestType.FieldByName(field); !ok {
			t.Fatalf("minimal bridge request lost field %q", field)
		}
	}
	bridgeType := reflect.TypeOf(models.VeloxProjectBridge{})
	for _, field := range []string{"Platform", "PlatformAccountID", "ChannelID", "VideoID", "Language", "GroupID", "GroupIDs", "MemberIDs"} {
		if _, ok := bridgeType.FieldByName(field); ok {
			t.Fatalf("minimal bridge model contains forbidden field %q", field)
		}
	}

	root := projectRoot(t)
	migration := readContractFile(t, root, "internal", "database", "migrations", "112_velox_project_bridges.sql")
	metadataMigration := readContractFile(t, root, "internal", "database", "migrations", "115_velox_project_bridge_editor_metadata.sql")
	minimalMigration := readContractFile(t, root, "internal", "database", "migrations", "116_velox_project_bridge_minimal.sql")
	handler := readContractFile(t, root, "pkg", "api", "velox_project_bridge_handlers.go")
	module := readContractFile(t, root, "pkg", "api", "modules_thumbnail_projects.go")
	for name, content := range map[string]string{"bridge migration": migration, "bridge metadata migration": metadataMigration, "bridge minimal migration": minimalMigration, "bridge handler": handler, "thumbnail module": module} {
		lower := strings.ToLower(stripSQLComments(content))
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
	// The bridge must never become a replica of the editor. Editor-internal
	// representations (timeline, layers, scenes, assets, revisions, render
	// state) are forbidden in the bridge schema.
	for _, replicaColumn := range []string{
		"timeline", "layers", "scenes", "render_state", "revisions",
		"keyframes", "canvas", "editor_snapshot",
	} {
		if strings.Contains(strings.ToLower(migration), replicaColumn) || strings.Contains(strings.ToLower(metadataMigration), replicaColumn) {
			t.Fatalf("bridge migration replicates editor-internal column %q", replicaColumn)
		}
	}
	// The bridge records its editor backend explicitly.
	for _, required := range []string{"editor_provider", "editor_status", "last_editor_sync_at"} {
		if !strings.Contains(strings.ToLower(metadataMigration), required) {
			t.Fatalf("bridge metadata migration lost required column %q", required)
		}
	}
	for _, removed := range []string{"platform", "platform_account_id", "channel_id", "video_id", "language"} {
		if !strings.Contains(strings.ToLower(minimalMigration), "drop column if exists "+removed) {
			t.Fatalf("minimal bridge migration does not remove legacy column %q", removed)
		}
	}
	assertBoundaryGoFile(t, filepath.Join(root, "pkg", "api", "velox_project_bridge_handlers.go"), map[string]bool{
		"database/sql":            false,
		"github.com/lib/pq":       false,
		"github.com/jackc/pgx/v5": false,
	}, []string{"handleCreateVeloxProjectBridge", "handleGetVeloxProjectBridge", "handleDeleteVeloxProjectBridge"})
	assertBoundaryGoFile(t, filepath.Join(root, "pkg", "api", "modules_thumbnail_projects.go"), map[string]bool{
		"database/sql":            false,
		"github.com/lib/pq":       false,
		"github.com/jackc/pgx/v5": false,
	}, []string{"Register"})
	for _, forbidden := range []string{"CREATE DATABASE", "dblink", "postgres_fdw", "CREATE TRIGGER", "sync_groups", "sync_channels", "bidirectional"} {
		if strings.Contains(strings.ToLower(migration), strings.ToLower(forbidden)) || strings.Contains(strings.ToLower(handler), strings.ToLower(forbidden)) || strings.Contains(strings.ToLower(module), strings.ToLower(forbidden)) {
			t.Fatalf("bridge boundary contains forbidden shared/synchronization construct %q", forbidden)
		}
	}
	for _, localTable := range []string{"thumbnail_projects", "workspaces"} {
		if !strings.Contains(strings.ToLower(migration), localTable) {
			t.Fatalf("bridge migration no longer declares expected InstaEdit-local relation %q", localTable)
		}
	}
}

func stripSQLComments(content string) string {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if idx := strings.Index(line, "--"); idx >= 0 {
			lines[i] = line[:idx]
		}
	}
	return strings.Join(lines, "\n")
}

func assertBoundaryGoFile(t *testing.T, path string, allowedImports map[string]bool, requiredFunctions []string) {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse boundary file %s: %v", path, err)
	}
	imports := make(map[string]bool, len(file.Imports))
	for _, spec := range file.Imports {
		name := strings.Trim(spec.Path.Value, "\"")
		imports[name] = true
		if allowed, known := allowedImports[name]; known && !allowed {
			t.Fatalf("boundary file %s imports forbidden database driver %q", path, name)
		}
	}
	for name, allowed := range allowedImports {
		if allowed && !imports[name] {
			t.Fatalf("boundary file %s lost required import %q", path, name)
		}
	}
	functions := make(map[string]bool)
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok {
			functions[fn.Name.Name] = true
		}
	}
	for _, name := range requiredFunctions {
		if !functions[name] {
			t.Fatalf("boundary file %s lost required function %q", path, name)
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
