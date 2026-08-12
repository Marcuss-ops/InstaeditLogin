package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

type coverLibraryTestStore struct {
	covers    []models.CoverLibraryItem
	templates []models.CoverTemplate
	versions  []models.CoverTemplateVersion
	created   *models.CoverTemplateVersion
}

func (s *coverLibraryTestStore) ListCoverLibrary(context.Context, int64, string, int) ([]models.CoverLibraryItem, error) {
	return s.covers, nil
}
func (s *coverLibraryTestStore) ListCoverTemplates(context.Context, int64, string, string) ([]models.CoverTemplate, error) {
	return s.templates, nil
}
func (s *coverLibraryTestStore) ListCoverTemplateVersions(context.Context, int64, int64) ([]models.CoverTemplateVersion, error) {
	return s.versions, nil
}
func (s *coverLibraryTestStore) CreateCoverTemplate(context.Context, *models.CoverTemplate, *models.CoverTemplateVersion) error {
	return nil
}
func (s *coverLibraryTestStore) CreateCoverTemplateVersion(_ context.Context, _ int64, version *models.CoverTemplateVersion) error {
	version.ID = 22
	version.VersionNumber = 2
	version.CreatedAt = time.Now().UTC()
	s.created = version
	return nil
}
func (s *coverLibraryTestStore) ArchiveCoverTemplate(context.Context, int64, int64) error { return nil }

func coverLibraryRouter(t *testing.T, store *coverLibraryTestStore) *Router {
	t.Helper()
	return newTestRouter(&mockProvider{platform: models.PlatformYouTube}, &mockUserStore{}, "",
		WithWorkspaceStore(&mockWorkspaceStore{findByIDFn: func(id int64) (*models.Workspace, error) {
			return &models.Workspace{ID: id, OwnerID: 1}, nil
		}}),
		WithCoverLibraryStore(store),
	)
}

func TestCoverLibrary_ListIsWorkspaceScopedAndReturnsItems(t *testing.T) {
	store := &coverLibraryTestStore{covers: []models.CoverLibraryItem{{ExportID: "export-1", WorkspaceID: 7, ProjectName: "Boxing v3", MediaID: "media-1", SHA256: "abc123"}}}
	r := coverLibraryRouter(t, store)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/cover-library?workspace_id=7", nil)
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var response coverLibraryListResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if len(response.Items) != 1 || response.Items[0].WorkspaceID != 7 || response.Items[0].SHA256 != "abc123" {
		t.Fatalf("unexpected cover library response: %+v", response.Items)
	}
}

func TestTemplateLibrary_CreateVersionUsesWorkspaceAndSetsImmutableVersion(t *testing.T) {
	store := &coverLibraryTestStore{}
	r := coverLibraryRouter(t, store)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/template-library/11/versions?workspace_id=7", strings.NewReader(`{"editor_project_id":"editor-project-v2","slots":{"title":true}}`))
	withBearerJWT(t, req, 1)
	w := httptest.NewRecorder()
	r.Setup().ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d: %s", w.Code, w.Body.String())
	}
	if store.created == nil || store.created.TemplateID != 11 || store.created.VersionNumber != 2 || store.created.EditorProjectID != "editor-project-v2" {
		t.Fatalf("unexpected created version: %+v", store.created)
	}
}
