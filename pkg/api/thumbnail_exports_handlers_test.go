package api

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/thumbnailrender"
)

// stubRoundTripper answers every request with the configured status and
// body so handler tests never hit the network for the server-side PUT
// to the (mock) presigned upload URL or the media-asset GET.
type stubRoundTripper struct {
	status int
	body   []byte
}

func (s stubRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: s.status,
		Status:     http.StatusText(s.status),
		Header:     http.Header{},
		Body:       io.NopCloser(bytes.NewReader(s.body)),
		Request:    req,
	}, nil
}

// encodeTestPNG builds a solid-color PNG asset for the media-store
// fixtures used by the image-object render tests.
func encodeTestPNG(t *testing.T, w, h int, r, g, b uint8) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{R: r, G: g, B: b, A: 255}), image.Point{}, draw.Src)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode test png: %v", err)
	}
	return buf.Bytes()
}

func thumbnailRenderRouter(t *testing.T, store *thumbnailProjectTestStore, media MediaStore, storage StorageProvider, ws *mockWorkspaceStore, transport http.RoundTripper) *Router {
	t.Helper()
	if transport == nil {
		transport = stubRoundTripper{status: http.StatusOK}
	}
	return newTestRouter(&mockProvider{platform: models.PlatformYouTube}, &mockUserStore{}, "",
		WithWorkspaceStore(ws),
		WithThumbnailProjectStore(store),
		WithMediaStore(media),
		WithStorageProvider(storage),
		WithThumbnailDownloadClient(&http.Client{Transport: transport}),
	)
}

func defaultRenderProject() *models.ThumbnailProject {
	rev := "rev-1"
	return &models.ThumbnailProject{
		ID: "thumbproj_test", WorkspaceID: 7, CreatedBy: 1,
		Name: "Cover", CanvasWidth: 320, CanvasHeight: 180,
		Status: models.ThumbnailProjectStatusDraft, Version: 2, CurrentRevisionID: &rev,
	}
}

func defaultRenderRevision() *models.ThumbnailProjectRevision {
	return &models.ThumbnailProjectRevision{
		ID: "rev-1", ProjectID: "thumbproj_test", RevisionNumber: 1, SchemaVersion: 1,
		SnapshotJSON:    json.RawMessage(`{"canvas":{"width":320,"height":180,"background":"#30305a"},"objects":[{"id":"t1","type":"text","text":"HELLO","x":10,"y":10,"font_size":48,"fill":"#ffffff"},{"id":"r1","type":"rect","x":40,"y":60,"width":120,"height":60,"fill":"#ff0000"}]}`),
		RendererVersion: "renderer-1",
	}
}

func workspaceOwnerStore(ownerID int64) *mockWorkspaceStore {
	return &mockWorkspaceStore{findByIDFn: func(id int64) (*models.Workspace, error) {
		return &models.Workspace{ID: id, OwnerID: ownerID}, nil
	}}
}

func TestThumbnailRender_HappyPath_CreatesReadyExport(t *testing.T) {
	store := &thumbnailProjectTestStore{project: defaultRenderProject(), revision: defaultRenderRevision()}
	media := newMockMediaStore()
	r := thumbnailRenderRouter(t, store, media, newMockStorageProvider(), workspaceOwnerStore(1), nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/thumbnail-projects/thumbproj_test/render?workspace_id=7", bytes.NewBufferString(`{}`))
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body.String())
	}
	var export models.ThumbnailExport
	if err := json.NewDecoder(w.Body).Decode(&export); err != nil {
		t.Fatalf("decode export: %v", err)
	}
	if export.Status != models.ThumbnailProjectExportStatusReady {
		t.Fatalf("export status: %q", export.Status)
	}
	if export.ID == "" || export.MediaID == "" || export.RevisionID != "rev-1" {
		t.Fatalf("export fields: %+v", export)
	}
	if export.Width != 320 || export.Height != 180 {
		t.Fatalf("export dims: %dx%d", export.Width, export.Height)
	}
	if export.FileSize <= 0 || len(export.SHA256) != 32 {
		t.Fatalf("export file_size=%d sha256=%d bytes", export.FileSize, len(export.SHA256))
	}
	if export.RendererVersion != thumbnailrender.RendererVersion {
		t.Fatalf("renderer_version: %q, want %q", export.RendererVersion, thumbnailrender.RendererVersion)
	}
	if store.lastExportStatus != models.ThumbnailProjectExportStatusReady {
		t.Fatalf("final status transition: %q", store.lastExportStatus)
	}
	if store.lastExportProfile == "" {
		t.Fatal("render profile must be persisted for idempotency")
	}
	// The rendered media asset must exist and be ready in the media store.
	if len(media.assets) != 1 {
		t.Fatalf("media assets: want 1, got %d", len(media.assets))
	}
	for id, asset := range media.assets {
		if asset.Status != models.MediaAssetStatusReady {
			t.Fatalf("asset %s status: %q", id, asset.Status)
		}
		if asset.SHA256 == "" || asset.ContentType != models.ThumbnailProjectExportContentTypePNG {
			t.Fatalf("asset %s sha/ct: %q %q", id, asset.SHA256, asset.ContentType)
		}
	}
}

func TestThumbnailRender_IsIdempotentForSameRevisionAndProfile(t *testing.T) {
	store := &thumbnailProjectTestStore{project: defaultRenderProject(), revision: defaultRenderRevision()}
	media := newMockMediaStore()
	storage := newMockStorageProvider()
	r := thumbnailRenderRouter(t, store, media, storage, workspaceOwnerStore(1), nil)

	render := func() (int, models.ThumbnailExport) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/thumbnail-projects/thumbproj_test/render?workspace_id=7", bytes.NewBufferString(`{}`))
		withBearerJWT(t, req, 1)
		w := httptest.NewRecorder()
		r.Setup().ServeHTTP(w, req)
		var export models.ThumbnailExport
		if err := json.NewDecoder(w.Body).Decode(&export); err != nil {
			t.Fatalf("decode export: %v", err)
		}
		return w.Code, export
	}

	firstCode, first := render()
	if firstCode != http.StatusCreated || first.Status != models.ThumbnailProjectExportStatusReady {
		t.Fatalf("first render: status=%d export=%+v", firstCode, first)
	}
	if len(media.assets) != 1 {
		t.Fatalf("first render created %d media assets, want 1", len(media.assets))
	}
	secondCode, second := render()
	if secondCode != http.StatusOK {
		t.Fatalf("idempotent retry: status=%d export=%+v", secondCode, second)
	}
	if second.ID != first.ID || second.MediaID != first.MediaID || string(second.SHA256) != string(first.SHA256) {
		t.Fatalf("retry returned a different artifact: first=%+v second=%+v", first, second)
	}
	if len(media.assets) != 1 {
		t.Fatalf("idempotent retry created duplicate media assets: %d", len(media.assets))
	}
}

func TestThumbnailRender_ImageObjectResolvedFromMediaStore(t *testing.T) {
	// Snapshot references a 4x4 red PNG asset owned by the caller.
	redPNG := encodeTestPNG(t, 4, 4, 255, 0, 0)
	media := newMockMediaStore()
	media.assets["11111111-1111-4111-8111-111111111111"] = &models.MediaAsset{
		ID: "11111111-1111-4111-8111-111111111111", UserID: 1,
		UploadKey: "uploads/1/img.png", ContentType: "image/png",
		SizeBytes: int64(len(redPNG)), Status: models.MediaAssetStatusReady,
		ExpiresAt: time.Now().Add(time.Hour),
	}
	media.visibleInWorkspace = map[int64][]int64{7: {1}}
	store := &thumbnailProjectTestStore{
		project: defaultRenderProject(),
		revision: &models.ThumbnailProjectRevision{
			ID: "rev-1", ProjectID: "thumbproj_test", RevisionNumber: 1, SchemaVersion: 1,
			SnapshotJSON:    json.RawMessage(`{"canvas":{"width":64,"height":64,"background":"#000"},"objects":[{"id":"i1","type":"image","media_id":"11111111-1111-4111-8111-111111111111","x":8,"y":8,"width":32,"height":32}]}`),
			RendererVersion: "renderer-1",
		},
	}
	// The GET for the media bytes must return the PNG body; the upload
	// PUT also goes through the same transport.
	r := thumbnailRenderRouter(t, store, media, newMockStorageProvider(), workspaceOwnerStore(1),
		stubRoundTripper{status: http.StatusOK, body: redPNG})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/thumbnail-projects/thumbproj_test/render?workspace_id=7", bytes.NewBufferString(`{}`))
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body.String())
	}
}

func TestThumbnailRender_ImageFromAnotherUserIsRejected(t *testing.T) {
	media := newMockMediaStore()
	media.assets["11111111-1111-4111-8111-111111111111"] = &models.MediaAsset{
		ID: "11111111-1111-4111-8111-111111111111", UserID: 999, // foreign owner
		UploadKey: "uploads/999/img.png", ContentType: "image/png",
		SizeBytes: 8, Status: models.MediaAssetStatusReady, ExpiresAt: time.Now().Add(time.Hour),
	}
	// User 999 is neither the workspace owner nor a member → blocked.
	media.visibleInWorkspace = map[int64][]int64{7: {1}}
	store := &thumbnailProjectTestStore{
		project: defaultRenderProject(),
		revision: &models.ThumbnailProjectRevision{
			ID: "rev-1", ProjectID: "thumbproj_test", RevisionNumber: 1, SchemaVersion: 1,
			SnapshotJSON:    json.RawMessage(`{"canvas":{"width":64,"height":64},"objects":[{"id":"i1","type":"image","media_id":"11111111-1111-4111-8111-111111111111","width":32,"height":32}]}`),
			RendererVersion: "renderer-1",
		},
	}
	r := thumbnailRenderRouter(t, store, media, newMockStorageProvider(), workspaceOwnerStore(1), nil)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/thumbnail-projects/thumbproj_test/render?workspace_id=7", bytes.NewBufferString(`{}`))
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422 for foreign media, got %d: %s", w.Code, w.Body.String())
	}
}

func TestThumbnailRender_MemberSharedImageRenders(t *testing.T) {
	// A workspace member (user 2) uploaded the image; the workspace
	// allowlist includes the member, so the render must succeed — the
	// same membership semantics as the media resolver. Without this,
	// the editor could display an image the renderer refuses.
	redPNG := encodeTestPNG(t, 4, 4, 0, 255, 0)
	media := newMockMediaStore()
	media.assets["11111111-1111-4111-8111-111111111111"] = &models.MediaAsset{
		ID: "11111111-1111-4111-8111-111111111111", UserID: 2,
		UploadKey: "uploads/2/img.png", ContentType: "image/png",
		SizeBytes: int64(len(redPNG)), Status: models.MediaAssetStatusReady,
		ExpiresAt: time.Now().Add(time.Hour),
	}
	media.visibleInWorkspace = map[int64][]int64{7: {1, 2}}
	store := &thumbnailProjectTestStore{
		project: defaultRenderProject(),
		revision: &models.ThumbnailProjectRevision{
			ID: "rev-1", ProjectID: "thumbproj_test", RevisionNumber: 1, SchemaVersion: 1,
			SnapshotJSON:    json.RawMessage(`{"canvas":{"width":64,"height":64,"background":"#000"},"objects":[{"id":"i1","type":"image","media_id":"11111111-1111-4111-8111-111111111111","x":8,"y":8,"width":32,"height":32}]}`),
			RendererVersion: "renderer-1",
		},
	}
	r := thumbnailRenderRouter(t, store, media, newMockStorageProvider(), workspaceOwnerStore(1),
		stubRoundTripper{status: http.StatusOK, body: redPNG})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/thumbnail-projects/thumbproj_test/render?workspace_id=7", bytes.NewBufferString(`{}`))
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("want 201 for member-shared media, got %d: %s", w.Code, w.Body.String())
	}
}

func TestThumbnailRender_NoSavedSnapshot_422(t *testing.T) {
	// Project without any current revision (no snapshot saved yet).
	project := defaultRenderProject()
	project.CurrentRevisionID = nil
	store := &thumbnailProjectTestStore{project: project}
	r := thumbnailRenderRouter(t, store, newMockMediaStore(), newMockStorageProvider(), workspaceOwnerStore(1), nil)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/thumbnail-projects/thumbproj_test/render?workspace_id=7", bytes.NewBufferString(`{}`))
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d: %s", w.Code, w.Body.String())
	}
}

func TestThumbnailRender_InvalidContentType_422(t *testing.T) {
	store := &thumbnailProjectTestStore{project: defaultRenderProject(), revision: defaultRenderRevision()}
	r := thumbnailRenderRouter(t, store, newMockMediaStore(), newMockStorageProvider(), workspaceOwnerStore(1), nil)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/thumbnail-projects/thumbproj_test/render?workspace_id=7", bytes.NewBufferString(`{"content_type":"image/gif"}`))
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d: %s", w.Code, w.Body.String())
	}
}

func TestThumbnailRender_UploadFailure_502(t *testing.T) {
	store := &thumbnailProjectTestStore{project: defaultRenderProject(), revision: defaultRenderRevision()}
	media := newMockMediaStore()
	r := thumbnailRenderRouter(t, store, media, newMockStorageProvider(), workspaceOwnerStore(1),
		stubRoundTripper{status: http.StatusServiceUnavailable})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/thumbnail-projects/thumbproj_test/render?workspace_id=7", bytes.NewBufferString(`{}`))
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("want 502, got %d: %s", w.Code, w.Body.String())
	}
	// The media asset must be marked failed, never ready.
	for _, asset := range media.assets {
		if asset.Status == models.MediaAssetStatusReady {
			t.Fatal("asset must not be ready after a failed upload")
		}
	}
}

func TestThumbnailRender_CrossWorkspace_404(t *testing.T) {
	store := &thumbnailProjectTestStore{project: defaultRenderProject()}
	r := thumbnailRenderRouter(t, store, newMockMediaStore(), newMockStorageProvider(), workspaceOwnerStore(99), nil)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/thumbnail-projects/thumbproj_test/render?workspace_id=7", bytes.NewBufferString(`{}`))
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404 for cross-workspace render, got %d", w.Code)
	}
}

func TestThumbnailExport_GetHappy(t *testing.T) {
	store := &thumbnailProjectTestStore{export: &models.ThumbnailExport{
		ID: "thumbexp_1", ProjectID: "thumbproj_test", RevisionID: "rev-1",
		MediaID: "11111111-1111-4111-8111-111111111111", ContentType: "image/png",
		Width: 320, Height: 180, FileSize: 12, SHA256: make([]byte, 32),
		RendererVersion: "renderer-1", Status: models.ThumbnailProjectExportStatusReady,
	}}
	r := thumbnailRenderRouter(t, store, newMockMediaStore(), newMockStorageProvider(), workspaceOwnerStore(1), nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/thumbnail-exports/thumbexp_1?workspace_id=7", nil)
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var export models.ThumbnailExport
	if err := json.NewDecoder(w.Body).Decode(&export); err != nil {
		t.Fatal(err)
	}
	if export.ID != "thumbexp_1" || export.Status != models.ThumbnailProjectExportStatusReady {
		t.Fatalf("export: %+v", export)
	}
}

func TestThumbnailExport_GetNotFound(t *testing.T) {
	store := &thumbnailProjectTestStore{findExportErr: repository.ErrThumbnailExportNotFound}
	r := thumbnailRenderRouter(t, store, newMockMediaStore(), newMockStorageProvider(), workspaceOwnerStore(1), nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/thumbnail-exports/missing?workspace_id=7", nil)
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}

func TestThumbnailRender_RequiresStorageConfigured(t *testing.T) {
	store := &thumbnailProjectTestStore{project: defaultRenderProject(), revision: defaultRenderRevision()}
	// No WithMediaStore / WithStorageProvider → 501.
	r := newTestRouter(&mockProvider{platform: models.PlatformYouTube}, &mockUserStore{}, "",
		WithWorkspaceStore(workspaceOwnerStore(1)), WithThumbnailProjectStore(store))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/thumbnail-projects/thumbproj_test/render?workspace_id=7", bytes.NewBufferString(`{}`))
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("want 501, got %d", w.Code)
	}
}
