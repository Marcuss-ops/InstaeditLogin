package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// ThumbnailProjectsModule mounts project CRUD, immutable snapshot, revision,
// lifecycle routes, the canonical render/export endpoints, and the project
// asset-link endpoints. The handlers themselves perform JWT identity and
// workspace ownership checks; no YouTube/provider dependency is captured
// here.
type ThumbnailProjectsModule struct {
	protected   func(http.HandlerFunc) http.HandlerFunc
	create      http.HandlerFunc
	list        http.HandlerFunc
	get         http.HandlerFunc
	update      http.HandlerFunc
	snapshot    http.HandlerFunc
	revisions   http.HandlerFunc
	revision    http.HandlerFunc
	restore     http.HandlerFunc
	archive     http.HandlerFunc
	delete      http.HandlerFunc
	render      http.HandlerFunc
	getExport   http.HandlerFunc
	addAsset    http.HandlerFunc
	listAssets  http.HandlerFunc
	deleteAsset http.HandlerFunc
}

func NewThumbnailProjectsModule(protected func(http.HandlerFunc) http.HandlerFunc, create, list, get, update, snapshot, revisions, revision, restore, archive, delete, render, getExport, addAsset, listAssets, deleteAsset http.HandlerFunc) RouteModule {
	return &ThumbnailProjectsModule{protected: protected, create: create, list: list, get: get, update: update, snapshot: snapshot, revisions: revisions, revision: revision, restore: restore, archive: archive, delete: delete, render: render, getExport: getExport, addAsset: addAsset, listAssets: listAssets, deleteAsset: deleteAsset}
}

var _ RouteModule = (*ThumbnailProjectsModule)(nil)

func (m *ThumbnailProjectsModule) Register(mux chi.Router) {
	mux.Method(http.MethodPost, "/api/v1/thumbnail-projects", m.protected(m.create))
	mux.Method(http.MethodGet, "/api/v1/thumbnail-projects", m.protected(m.list))
	mux.Method(http.MethodGet, "/api/v1/thumbnail-projects/{id}", m.protected(m.get))
	mux.Method(http.MethodPatch, "/api/v1/thumbnail-projects/{id}", m.protected(m.update))
	mux.Method(http.MethodPut, "/api/v1/thumbnail-projects/{id}/snapshot", m.protected(m.snapshot))
	mux.Method(http.MethodGet, "/api/v1/thumbnail-projects/{id}/revisions", m.protected(m.revisions))
	mux.Method(http.MethodGet, "/api/v1/thumbnail-projects/{id}/revisions/{revision_id}", m.protected(m.revision))
	mux.Method(http.MethodPost, "/api/v1/thumbnail-projects/{id}/restore/{revision_id}", m.protected(m.restore))
	mux.Method(http.MethodPost, "/api/v1/thumbnail-projects/{id}/archive", m.protected(m.archive))
	mux.Method(http.MethodDelete, "/api/v1/thumbnail-projects/{id}", m.protected(m.delete))
	// Canonical render + export surface. POST render rasterizes the
	// persisted snapshot through the deterministic renderer and stores
	// the file via the Media Library; GET export reads a workspace-
	// scoped export row by id.
	mux.Method(http.MethodPost, "/api/v1/thumbnail-projects/{id}/render", m.protected(m.render))
	mux.Method(http.MethodGet, "/api/v1/thumbnail-exports/{export_id}", m.protected(m.getExport))
	// Project asset links (media library rows referenced by the canvas).
	// POST links a ready media asset with a typed role; GET lists the
	// links; DELETE removes one (project, media_id, role) triple. All
	// workspace-scoped via query parameters + ownership checks.
	mux.Method(http.MethodPost, "/api/v1/thumbnail-projects/{id}/assets", m.protected(m.addAsset))
	mux.Method(http.MethodGet, "/api/v1/thumbnail-projects/{id}/assets", m.protected(m.listAssets))
	mux.Method(http.MethodDelete, "/api/v1/thumbnail-projects/{id}/assets/{media_id}", m.protected(m.deleteAsset))
}
