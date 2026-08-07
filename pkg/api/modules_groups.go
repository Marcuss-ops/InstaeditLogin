package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// GroupsHandlers groups the HTTP handler functions used by the
// groups module. Keeping them in a nested struct keeps
// GroupsModuleDeps readable (mirrors the AuthHandlers pattern used
// by the auth module).
type GroupsHandlers struct {
	ListGroups          http.HandlerFunc
	ListGroupsWithAccounts http.HandlerFunc
	CreateGroup         http.HandlerFunc
	GetGroup            http.HandlerFunc
	UpdateGroup         http.HandlerFunc
	DeleteGroup         http.HandlerFunc
	ListGroupAccounts   http.HandlerFunc
	SetGroupAccounts    http.HandlerFunc
	UpdateGroupSettings http.HandlerFunc
	RemoveGroupAccount  http.HandlerFunc
}

// GroupsModuleDeps is the narrow set of dependencies the groups
// module needs to mount its routes. The module owns ONLY the
// hierarchical-groups bounded context (/api/v1/groups/*); it does not
// need auth middleware, billing, sessions or any other Router
// feature.
type GroupsModuleDeps struct {
	// GroupStore backs every /api/v1/groups handler (TAGLIO X.Y).
	// Optional — mirrors the WorkspaceStore / PostStore nil-guard
	// pattern: when nil, the module registers NO routes (every
	// /api/v1/groups/* endpoint then 404s at the chi level, exactly
	// like the pre-module behaviour). Wired in
	// internal/bootstrap/app.go via api.WithGroupStore(repo).
	GroupStore GroupStore
	// Protected is the JWT/session auth wrapper applied to every
	// groups route.
	Protected func(http.HandlerFunc) http.HandlerFunc
	// Handlers are the concrete handler functions, injected by the
	// Router in Setup().
	Handlers GroupsHandlers
}

// GroupsModule mounts the hierarchical-groups routes. Extracted from
// AuthModule (which previously owned /api/v1/groups alongside the
// identity surfaces) so the groups bounded context owns its own
// dependencies and route table.
type GroupsModule struct {
	deps GroupsModuleDeps
}

func NewGroupsModule(deps GroupsModuleDeps) RouteModule {
	return &GroupsModule{deps: deps}
}

// Compile-time assertion: GroupsModule implements RouteModule.
var _ RouteModule = (*GroupsModule)(nil)

func (m *GroupsModule) Register(mux chi.Router) {
	if m.deps.GroupStore == nil {
		return
	}
	mux.Route("/api/v1/groups", func(sr chi.Router) {
		sr.Get("/", m.deps.Protected(m.deps.Handlers.ListGroups))
		sr.Get("/aggregate", m.deps.Protected(m.deps.Handlers.ListGroupsWithAccounts))
		sr.Post("/", m.deps.Protected(m.deps.Handlers.CreateGroup))
		sr.Get("/{id:[0-9]+}", m.deps.Protected(m.deps.Handlers.GetGroup))
		sr.Patch("/{id:[0-9]+}", m.deps.Protected(m.deps.Handlers.UpdateGroup))
		sr.Delete("/{id:[0-9]+}", m.deps.Protected(m.deps.Handlers.DeleteGroup))
		sr.Get("/{id:[0-9]+}/accounts", m.deps.Protected(m.deps.Handlers.ListGroupAccounts))
		sr.Put("/{id:[0-9]+}/accounts", m.deps.Protected(m.deps.Handlers.SetGroupAccounts))
		sr.Delete("/{id:[0-9]+}/accounts/{accountId:[0-9]+}", m.deps.Protected(m.deps.Handlers.RemoveGroupAccount))
		sr.Patch("/{id:[0-9]+}/settings", m.deps.Protected(m.deps.Handlers.UpdateGroupSettings))
	})
}
