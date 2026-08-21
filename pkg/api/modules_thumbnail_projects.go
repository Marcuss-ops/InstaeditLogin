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
	protected        func(http.HandlerFunc) http.HandlerFunc
	machineProtected func(string, http.HandlerFunc) http.HandlerFunc
	create           http.HandlerFunc
	list             http.HandlerFunc
	get              http.HandlerFunc
	update           http.HandlerFunc
	snapshot         http.HandlerFunc
	revisions        http.HandlerFunc
	revision         http.HandlerFunc
	restore          http.HandlerFunc
	archive          http.HandlerFunc
	delete           http.HandlerFunc
	render           http.HandlerFunc
	getExport        http.HandlerFunc
	addAsset         http.HandlerFunc
	listAssets       http.HandlerFunc
	deleteAsset      http.HandlerFunc
	createAssignment http.HandlerFunc
	listAssignments  http.HandlerFunc
	resolveMedia     http.HandlerFunc
	createBridge     http.HandlerFunc
	getBridge        http.HandlerFunc
	deleteBridge     http.HandlerFunc
}

func NewThumbnailProjectsModule(protected func(http.HandlerFunc) http.HandlerFunc, machineProtected func(string, http.HandlerFunc) http.HandlerFunc, create, list, get, update, snapshot, revisions, revision, restore, archive, delete, render, getExport, addAsset, listAssets, deleteAsset, createAssignment, listAssignments, resolveMedia, createBridge, getBridge, deleteBridge http.HandlerFunc) RouteModule {
	return &ThumbnailProjectsModule{protected: protected, machineProtected: machineProtected, create: create, list: list, get: get, update: update, snapshot: snapshot, revisions: revisions, revision: revision, restore: restore, archive: archive, delete: delete, render: render, getExport: getExport, addAsset: addAsset, listAssets: listAssets, deleteAsset: deleteAsset, createAssignment: createAssignment, listAssignments: listAssignments, resolveMedia: resolveMedia, createBridge: createBridge, getBridge: getBridge, deleteBridge: deleteBridge}
}

var _ RouteModule = (*ThumbnailProjectsModule)(nil)

func (m *ThumbnailProjectsModule) Register(mux chi.Router) {
	machine := m.machineProtected
	if machine == nil {
		machine = func(_ string, h http.HandlerFunc) http.HandlerFunc { return m.protected(h) }
	}
	write := machine
	read := machine
	mux.Method(http.MethodPost, "/api/v1/thumbnail-projects", write("write", m.create))
	mux.Method(http.MethodGet, "/api/v1/thumbnail-projects", read("read", m.list))
	mux.Method(http.MethodGet, "/api/v1/thumbnail-projects/{id}", read("read", m.get))
	mux.Method(http.MethodPatch, "/api/v1/thumbnail-projects/{id}", write("write", m.update))
	mux.Method(http.MethodPut, "/api/v1/thumbnail-projects/{id}/snapshot", write("write", m.snapshot))
	mux.Method(http.MethodGet, "/api/v1/thumbnail-projects/{id}/revisions", read("read", m.revisions))
	mux.Method(http.MethodGet, "/api/v1/thumbnail-projects/{id}/revisions/{revision_id}", read("read", m.revision))
	mux.Method(http.MethodPost, "/api/v1/thumbnail-projects/{id}/restore/{revision_id}", write("write", m.restore))
	mux.Method(http.MethodPost, "/api/v1/thumbnail-projects/{id}/archive", write("write", m.archive))
	mux.Method(http.MethodDelete, "/api/v1/thumbnail-projects/{id}", write("write", m.delete))
	// Canonical render + export surface. POST render rasterizes the
	// persisted snapshot through the deterministic renderer and stores
	// the file via the Media Library; GET export reads a workspace-
	// scoped export row by id.
	mux.Method(http.MethodPost, "/api/v1/thumbnail-projects/{id}/render", write("write", m.render))
	mux.Method(http.MethodGet, "/api/v1/thumbnail-exports/{export_id}", read("read", m.getExport))
	// Project asset links (media library rows referenced by the canvas).
	// POST links a ready media asset with a typed role; GET lists the
	// links; DELETE removes one (project, media_id, role) triple. All
	// workspace-scoped via query parameters + ownership checks.
	mux.Method(http.MethodPost, "/api/v1/thumbnail-projects/{id}/assets", write("write", m.addAsset))
	mux.Method(http.MethodGet, "/api/v1/thumbnail-projects/{id}/assets", read("read", m.listAssets))
	mux.Method(http.MethodDelete, "/api/v1/thumbnail-projects/{id}/assets/{media_id}", write("write", m.deleteAsset))
	// Optional YouTube destination for an existing ready export. The
	// export exists before any assignment and the original project is
	// never modified; the workspace guard is enforced handler-side.
	mux.Method(http.MethodPost, "/api/v1/thumbnail-exports/{export_id}/assignments", write("write", m.createAssignment))
	// GET project assignments — workspace-scoped destination list used
	// by the Copertine library to compute the "Collegate" filter.
	mux.Method(http.MethodGet, "/api/v1/thumbnail-projects/{id}/assignments", read("read", m.listAssignments))
	// Media resolver: resolves the snapshot's media_id references to
	// short-lived presigned GET URLs, workspace-scoped. This is the
	// server-authoritative source for the editor canvas — local blobs
	// are never authoritative.
	mux.Method(http.MethodPost, "/api/v1/thumbnail-projects/{id}/media/resolve", read("read", m.resolveMedia))
	// Minimal InstaEdit-owned bridge to the separate Velox editor. The
	// bridge stores only an opaque Velox handle plus optional concrete
	// channel context; it never exposes groups or channel catalogs.
	mux.Method(http.MethodPost, "/api/v1/thumbnail-projects/{id}/velox-bridge", write("write", m.createBridge))
	mux.Method(http.MethodGet, "/api/v1/thumbnail-projects/{id}/velox-bridge", read("read", m.getBridge))
	mux.Method(http.MethodDelete, "/api/v1/thumbnail-projects/{id}/velox-bridge", write("write", m.deleteBridge))
}
