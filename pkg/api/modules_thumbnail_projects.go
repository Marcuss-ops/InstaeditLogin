package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// ThumbnailProjectsModule mounts project CRUD, immutable snapshot, revision,
// and lifecycle routes. The handlers themselves perform JWT identity and
// workspace ownership checks; no YouTube/provider dependency is captured here.
type ThumbnailProjectsModule struct {
	protected func(http.HandlerFunc) http.HandlerFunc
	create    http.HandlerFunc
	list      http.HandlerFunc
	get       http.HandlerFunc
	update    http.HandlerFunc
	snapshot  http.HandlerFunc
	revisions http.HandlerFunc
	revision  http.HandlerFunc
	restore   http.HandlerFunc
	archive   http.HandlerFunc
	delete    http.HandlerFunc
}

func NewThumbnailProjectsModule(protected func(http.HandlerFunc) http.HandlerFunc, create, list, get, update, snapshot, revisions, revision, restore, archive, delete http.HandlerFunc) RouteModule {
	return &ThumbnailProjectsModule{protected: protected, create: create, list: list, get: get, update: update, snapshot: snapshot, revisions: revisions, revision: revision, restore: restore, archive: archive, delete: delete}
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
}
