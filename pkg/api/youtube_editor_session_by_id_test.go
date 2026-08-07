package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/auth"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// fakeAuthIdentity is the minimal auth.Identity implementation the
// GET handler tests use. auth.IdentityFromContext reads UserID()
// from the context-bound identity; we only need that + a stable
// UserID getter for the workspace-ownership check.
type fakeAuthIdentity struct {
	uid int64
}

func (f *fakeAuthIdentity) UserID() int64               { return f.uid }
func (f *fakeAuthIdentity) IsAdmin() bool               { return false }
func (f *fakeAuthIdentity) IsAPIKey() bool              { return false }
func (f *fakeAuthIdentity) KeyID() int64                { return 0 }
func (f *fakeAuthIdentity) WorkspaceID() int64          { return 0 }
func (f *fakeAuthIdentity) SessionID() int64            { return 0 }
func (f *fakeAuthIdentity) Permissions() []string       { return nil }
func (f *fakeAuthIdentity) HasPermission(_ string) bool { return false }

// withIdentity returns a child context carrying the supplied
// identity. The handler reads it via auth.IdentityFromContext;
// auth.WithIdentity is the production setter that stores it
// under the canonical context key.
func withIdentity(ctx context.Context, id auth.Identity) context.Context {
	return auth.WithIdentity(ctx, id)
}

// fakeWorkspaceStoreForSessionGet is the minimal in-memory
// WorkspaceStore fake used by the GET-by-id handler tests.
// Embeds the production WorkspaceStore interface (other methods
// nil-receiver-safe → calling them panics loudly) + overrides
// ONLY the method the handler actually invokes (FindByID).
// The other WorkspaceStore methods (Create, ListByOwner, Delete,
// AttachChannel, ListChannels, UpdateChannel, DetachChannel,
// FindChannel) panic-on-call — those signals surface unused
// invocations to the test author.
type fakeWorkspaceStoreForSessionGet struct {
	WorkspaceStore
	rows    map[int64]*models.Workspace
	binding *models.WorkspaceChannel
}

func newFakeWorkspaceStoreForSessionGet() *fakeWorkspaceStoreForSessionGet {
	return &fakeWorkspaceStoreForSessionGet{rows: map[int64]*models.Workspace{}}
}

func (f *fakeWorkspaceStoreForSessionGet) FindByID(id int64) (*models.Workspace, error) {
	if f.rows == nil {
		return nil, nil
	}
	return f.rows[id], nil
}

// FindChannel + ListChannels are required to satisfy the production
// WorkspaceStore interface which
// (*VeloxModule).CreateThumbnailSessionForDelivery calls in its
// defense-in-depth (workspace, account) binding check.
//
// Without FindChannel, a method call promotes to the nil embedded
// interface and SIGSEGVs. The HappyPath / CrossTenant tests must
// set f.binding to a non-nil enabled WorkspaceChannel BEFORE
// calling seedGetTestRow — otherwise the production helper rejects
// the (workspace, account) pair with ErrEditorSessionChannelUnlinked
// (mapped to 404 in production now). ListChannels returns empty
// because the GET-by-id handler never exercises channel_id-resolution.
func (f *fakeWorkspaceStoreForSessionGet) FindChannel(_ context.Context, _ int64, _ int64) (*models.WorkspaceChannel, error) {
	return f.binding, nil
}

func (f *fakeWorkspaceStoreForSessionGet) ListChannels(_ context.Context, _ int64) ([]models.WorkspaceChannel, error) {
	return nil, nil
}

// Compile-time guarantee that fakeWorkspaceStoreForSessionGet
// satisfies the production WorkspaceStore interface. Same guard as
// fakeE2EWorkspace + workspaceStoreAdapter in this package.
var _ WorkspaceStore = (*fakeWorkspaceStoreForSessionGet)(nil)

// runGetEditorSessionByID wires a Router with the supplied fakes
// + the test JWT-equivalent identity, then issues GET against the
// new endpoint. Centralised so per-test setup stays minimal.
//
// Mounts the handler DIRECTLY on a bare chi mux (no JWT middleware
// — identity is injected via context).
func runGetEditorSessionByID(
	t *testing.T,
	yteStore *fakeYouTubeVideoEditStore,
	ws *fakeWorkspaceStoreForSessionGet,
	identity auth.Identity,
	pathParam string,
) *httptest.ResponseRecorder {
	t.Helper()
	r := &Router{
		youtubeVideoEditStore: yteStore,
		workspaceStore:        ws,
		editorURL:             "https://editor.instaedit.test",
	}
	mux := chi.NewRouter()
	mux.Method(http.MethodGet, "/api/v1/youtube/editor-sessions/{id}", http.HandlerFunc(r.handleGetYouTubeEditorSessionByID))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/youtube/editor-sessions/"+pathParam, nil)
	if identity != nil {
		req = req.WithContext(withIdentity(req.Context(), identity))
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

// seedGetTestRow writes a fresh row into the supplied store using
// the production helper (so the DTO round-trip exercises the same
// ID + VeloxProjectID format the auto-provisioner mints).
func seedGetTestRow(t *testing.T, store *fakeYouTubeVideoEditStore, ws WorkspaceStore, users UserStore, videoID string) *models.YouTubeVideoEdit {
	t.Helper()
	vm := NewVeloxModule(VeloxModuleDeps{
		WorkspaceStore:        ws,
		UserStore:             users,
		YouTubeVideoEditStore: store,
	}).(*VeloxModule)
	row, _, err := vm.CreateThumbnailSessionForDelivery(context.Background(), CreateThumbnailSessionInput{
		WorkspaceID:       12,
		PlatformAccountID: 381,
		YouTubeVideoID:    videoID,
		VideoTitle:        "My Video",
		FinalPrivacy:      "public",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	return row
}

// TestGetYouTubeEditorSessionByID_HappyPath verifies the happy
// path: row present + workspace owned → 200 + the canonical DTO
// shape returned by toYouTubeEditorSessionDetail.
func TestGetYouTubeEditorSessionByID_HappyPath(t *testing.T) {
	store := newFakeYouTubeVideoEditStore()
	ws := newFakeWorkspaceStoreForSessionGet()
	ws.rows[12] = &models.Workspace{ID: 12, OwnerID: 42, Name: "ws"}
	// Seed the (workspace, account) binding so the real
	// CreateThumbnailSessionForDelivery defense-in-depth check passes.
	ws.binding = &models.WorkspaceChannel{WorkspaceID: 12, PlatformAccountID: 381, Enabled: true}
	users := &fakeE2EUser{accounts: map[int64]*models.PlatformAccount{
		381: {ID: 381, Platform: "youtube", Status: "active"},
	}}
	row := seedGetTestRow(t, store, ws, users, "vid-happy")

	w := runGetEditorSessionByID(t, store, ws, &fakeAuthIdentity{uid: 42}, row.ID)
	if w.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d body=%s", w.Code, w.Body.String())
	}
	var dto youTubeEditorSessionDetail
	if err := json.Unmarshal(w.Body.Bytes(), &dto); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, w.Body.String())
	}
	if dto.ID != row.ID {
		t.Errorf("ID: want %q, got %q", row.ID, dto.ID)
	}
	if dto.WorkspaceID != 12 {
		t.Errorf("WorkspaceID: want 12, got %d", dto.WorkspaceID)
	}
	if dto.PlatformAccountID != 381 {
		t.Errorf("PlatformAccountID: want 381, got %d", dto.PlatformAccountID)
	}
	if dto.YouTubeVideoID != "vid-happy" {
		t.Errorf("YouTubeVideoID: want vid-happy, got %q", dto.YouTubeVideoID)
	}
	if !hasStringPrefix(dto.VeloxProjectID, "ve_") {
		t.Errorf("VeloxProjectID: want ve_ prefix, got %q", dto.VeloxProjectID)
	}
	// The launcher URL must be present and point at the session's own
	// project handle (the InstaEditor SPA redirect target).
	wantEditorURL := "https://editor.instaedit.test/editor/" + row.VeloxProjectID
	if dto.EditorURL != wantEditorURL {
		t.Errorf("EditorURL: want %q, got %q", wantEditorURL, dto.EditorURL)
	}
	if dto.DesiredPrivacy != "public" {
		t.Errorf("DesiredPrivacy: want public, got %q", dto.DesiredPrivacy)
	}
	if dto.Status != "editing" {
		t.Errorf("Status: want editing, got %q", dto.Status)
	}
	if dto.DraftTitle == nil || *dto.DraftTitle != "My Video" {
		t.Errorf("DraftTitle: want the auto-provisioner hint, got %v", dto.DraftTitle)
	}
}

// TestGetYouTubeEditorSessionByID_NotFound verifies the handler
// returns 404 when the row doesn't exist.
func TestGetYouTubeEditorSessionByID_NotFound(t *testing.T) {
	store := newFakeYouTubeVideoEditStore()
	ws := newFakeWorkspaceStoreForSessionGet()
	ws.rows[12] = &models.Workspace{ID: 12, OwnerID: 42, Name: "ws"}
	w := runGetEditorSessionByID(t, store, ws, &fakeAuthIdentity{uid: 42}, "ytedit_does-not-exist")
	if w.Code != http.StatusNotFound {
		t.Errorf("status: want 404, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestGetYouTubeEditorSessionByID_CrossTenant verifies the
// workspace-ownership check returns 404 for a row in a workspace
// the caller does NOT own. Defends against cross-tenant probes.
func TestGetYouTubeEditorSessionByID_CrossTenant(t *testing.T) {
	store := newFakeYouTubeVideoEditStore()
	ws := newFakeWorkspaceStoreForSessionGet()
	ws.rows[12] = &models.Workspace{ID: 12, OwnerID: 42, Name: "ws"} // owned by 42
	ws.binding = &models.WorkspaceChannel{WorkspaceID: 12, PlatformAccountID: 381, Enabled: true}
	users := &fakeE2EUser{accounts: map[int64]*models.PlatformAccount{
		381: {ID: 381, Platform: "youtube", Status: "active"},
	}}
	row := seedGetTestRow(t, store, ws, users, "vid-foreign")

	// Caller is user 9999 — NOT owner.
	w := runGetEditorSessionByID(t, store, ws, &fakeAuthIdentity{uid: 9999}, row.ID)
	if w.Code != http.StatusNotFound {
		t.Errorf("cross-tenant probe: want 404 (defence-in-depth), got %d body=%s", w.Code, w.Body.String())
	}
}

// TestGetYouTubeEditorSessionByID_MissingIdentity verifies the
// handler returns 401 when no JWT identity is on the context.
func TestGetYouTubeEditorSessionByID_MissingIdentity(t *testing.T) {
	store := newFakeYouTubeVideoEditStore()
	ws := newFakeWorkspaceStoreForSessionGet()
	w := runGetEditorSessionByID(t, store, ws, nil, "any-id")
	if w.Code != http.StatusUnauthorized {
		t.Errorf("missing identity: want 401, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestGetYouTubeEditorSessionByID_StoreUnconfigured verifies the
// handler returns 503 when the youtubeVideoEditStore dep is nil.
func TestGetYouTubeEditorSessionByID_StoreUnconfigured(t *testing.T) {
	r := &Router{
		youtubeVideoEditStore: nil,
		workspaceStore:        newFakeWorkspaceStoreForSessionGet(),
	}
	mux := chi.NewRouter()
	mux.Method(http.MethodGet, "/api/v1/youtube/editor-sessions/{id}", http.HandlerFunc(r.handleGetYouTubeEditorSessionByID))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/youtube/editor-sessions/any-id", nil)
	req = req.WithContext(withIdentity(req.Context(), &fakeAuthIdentity{uid: 42}))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("nil store: want 503, got %d body=%s", w.Code, w.Body.String())
	}
}

// hasStringPrefix is a tiny strings.HasPrefix wrapper so the test
// file doesn't need to import strings directly.
func hasStringPrefix(s, prefix string) bool {
	if len(s) < len(prefix) {
		return false
	}
	return s[:len(prefix)] == prefix
}
