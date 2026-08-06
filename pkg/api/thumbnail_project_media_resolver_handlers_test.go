package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

const (
	resolveMediaA = "00000000-0000-4000-8000-000000000001"
	resolveMediaB = "00000000-0000-4000-8000-000000000002"
)

func resolveMediaStore() *mockMediaStore {
	store := newMockMediaStore()
	now := time.Now()
	store.assets[resolveMediaA] = &models.MediaAsset{
		ID: resolveMediaA, UserID: 1, UploadKey: "uploads/1/a.jpg",
		ContentType: "image/jpeg", SizeBytes: 2048,
		Status: models.MediaAssetStatusReady, ExpiresAt: now.Add(24 * time.Hour),
		CreatedAt: now,
	}
	store.assets[resolveMediaB] = &models.MediaAsset{
		ID: resolveMediaB, UserID: 1, UploadKey: "uploads/1/b.png",
		ContentType: "image/png", SizeBytes: 4096,
		Status: models.MediaAssetStatusReady, ExpiresAt: now.Add(24 * time.Hour),
		CreatedAt: now,
	}
	store.visibleInWorkspace = map[int64][]int64{7: {1}}
	return store
}

func resolveMediaRouter(t *testing.T, store *thumbnailProjectTestStore, media MediaStore, ws *mockWorkspaceStore) *Router {
	t.Helper()
	return thumbnailRenderRouter(t, store, media, newMockStorageProvider(), ws, nil)
}

func TestThumbnailProjects_ResolveMediaRequiresWorkspaceQuery(t *testing.T) {
	r := resolveMediaRouter(t, &thumbnailProjectTestStore{}, newMockMediaStore(), workspaceOwnerStore(1))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/thumbnail-projects/thumbproj_test/media/resolve", bytes.NewBufferString(`{"media_ids":["`+resolveMediaA+`"]}`))
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for missing workspace_id, got %d", w.Code)
	}
}

func TestThumbnailProjects_ResolveMediaCrossWorkspaceIs404(t *testing.T) {
	r := resolveMediaRouter(t, &thumbnailProjectTestStore{}, resolveMediaStore(), workspaceOwnerStore(99))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/thumbnail-projects/thumbproj_test/media/resolve?workspace_id=7", bytes.NewBufferString(`{"media_ids":["`+resolveMediaA+`"]}`))
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404 for cross-workspace resolve, got %d", w.Code)
	}
}

func TestThumbnailProjects_ResolveMediaMintsSignedURLs(t *testing.T) {
	media := resolveMediaStore()
	r := resolveMediaRouter(t, &thumbnailProjectTestStore{}, media, workspaceOwnerStore(1))
	body := `{"media_ids":["` + resolveMediaA + `","` + resolveMediaB + `"]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/thumbnail-projects/thumbproj_test/media/resolve?workspace_id=7", bytes.NewBufferString(body))
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var response thumbnailMediaResolveResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if len(response.Items) != 2 {
		t.Fatalf("want 2 resolved items, got %d: %+v", len(response.Items), response.Items)
	}
	for _, item := range response.Items {
		if item.URL == "" || !bytes.Contains([]byte(item.URL), []byte("X-Amz-Signature=mock")) {
			t.Fatalf("expected presigned URL, got %q", item.URL)
		}
		if item.ContentType == "" || item.SizeBytes <= 0 {
			t.Fatalf("missing metadata: %+v", item)
		}
	}
}

func TestThumbnailProjects_ResolveMediaOmitsForeignAsset(t *testing.T) {
	media := resolveMediaStore()
	// A third asset owned by another user (not a workspace member):
	// must never resolve, even when requested.
	media.assets["00000000-0000-4000-8000-000000000099"] = &models.MediaAsset{
		ID: "00000000-0000-4000-8000-000000000099", UserID: 99, UploadKey: "uploads/99/x.jpg",
		ContentType: "image/jpeg", SizeBytes: 512,
		Status: models.MediaAssetStatusReady, ExpiresAt: time.Now().Add(24 * time.Hour),
	}
	body := `{"media_ids":["` + resolveMediaA + `","00000000-0000-4000-8000-000000000099"]}`
	r := resolveMediaRouter(t, &thumbnailProjectTestStore{}, media, workspaceOwnerStore(1))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/thumbnail-projects/thumbproj_test/media/resolve?workspace_id=7", bytes.NewBufferString(body))
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var response thumbnailMediaResolveResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if len(response.Items) != 1 || response.Items[0].MediaID != resolveMediaA {
		t.Fatalf("foreign asset must be omitted, got %+v", response.Items)
	}
}

func TestThumbnailProjects_ResolveMediaRejectsNonUUID(t *testing.T) {
	r := resolveMediaRouter(t, &thumbnailProjectTestStore{}, newMockMediaStore(), workspaceOwnerStore(1))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/thumbnail-projects/thumbproj_test/media/resolve?workspace_id=7", bytes.NewBufferString(`{"media_ids":["not-a-uuid"]}`))
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422 for non-UUID media id, got %d", w.Code)
	}
}

func TestThumbnailProjects_ResolveMediaEmptyIDsIs400(t *testing.T) {
	r := resolveMediaRouter(t, &thumbnailProjectTestStore{}, newMockMediaStore(), workspaceOwnerStore(1))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/thumbnail-projects/thumbproj_test/media/resolve?workspace_id=7", bytes.NewBufferString(`{"media_ids":[]}`))
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for empty media_ids, got %d", w.Code)
	}
}

func TestThumbnailProjects_ResolveMediaCachesTemporaryURLs(t *testing.T) {
	media := resolveMediaStore()
	storage := newMockStorageProvider()
	r := thumbnailRenderRouter(t, &thumbnailProjectTestStore{}, media, storage, workspaceOwnerStore(1), nil)
	body := `{"media_ids":["` + resolveMediaA + `"]}`
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/thumbnail-projects/thumbproj_test/media/resolve?workspace_id=7", bytes.NewBufferString(body))
		withBearerJWT(t, req, 1)
		w := httptest.NewRecorder()
		r.Setup().ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("request %d: want 200, got %d: %s", i, w.Code, w.Body.String())
		}
	}
	if storage.getObjectCalls != 1 {
		t.Fatalf("signed URL calls: got %d, want 1 due to short cache", storage.getObjectCalls)
	}
}

func TestThumbnailProjects_ResolveMediaDeduplicatesInput(t *testing.T) {
	media := resolveMediaStore()
	r := resolveMediaRouter(t, &thumbnailProjectTestStore{}, media, workspaceOwnerStore(1))
	body := `{"media_ids":["` + resolveMediaA + `","` + resolveMediaA + `"]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/thumbnail-projects/thumbproj_test/media/resolve?workspace_id=7", bytes.NewBufferString(body))
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var response thumbnailMediaResolveResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if len(response.Items) != 1 {
		t.Fatalf("duplicate ids must resolve to a single item, got %d", len(response.Items))
	}
}
