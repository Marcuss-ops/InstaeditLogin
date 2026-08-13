package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// PublishingModuleDeps is the narrow set of dependencies the
// publishing module needs to mount its routes.
type PublishingModuleDeps struct {
	RateLimitSvc         *services.RateLimitService
	Protected            func(http.HandlerFunc) http.HandlerFunc
	CreatePost           http.HandlerFunc
	ListPosts            http.HandlerFunc
	ListPostsByWorkspace http.HandlerFunc
	GetPost              http.HandlerFunc
	PatchPost            http.HandlerFunc
	DeletePost           http.HandlerFunc
	PublishPost          http.HandlerFunc
	SchedulePost         http.HandlerFunc
	CancelPost           http.HandlerFunc
	RetryPost            http.HandlerFunc
	GetPostTargets       http.HandlerFunc
	// GetPostTarget (Taglio 5.1 step 2) serves the polling
	// single-target GET /api/v1/post-targets/{id}. Same handler
	// path resolution, same workspace isolation, distinct URL
	// from the per-post fan-out endpoint.
	GetPostTarget        http.HandlerFunc
	AddPostTarget        http.HandlerFunc
	RetryTarget          http.HandlerFunc
	UploadCounts         http.HandlerFunc
	ListUploads          http.HandlerFunc
	ListUploadsByAccount http.HandlerFunc
	UploadsBatchByFolder http.HandlerFunc
	RescheduleUpload     http.HandlerFunc
	EditScheduledUpload  http.HandlerFunc
	CancelUpload         http.HandlerFunc
}

// PublishingModule mounts post, post-target and upload-job routes.
type PublishingModule struct {
	deps PublishingModuleDeps
}

func NewPublishingModule(deps PublishingModuleDeps) RouteModule {
	return &PublishingModule{deps: deps}
}

// Compile-time assertion: PublishingModule implements RouteModule.
var _ RouteModule = (*PublishingModule)(nil)

func (m *PublishingModule) Register(mux chi.Router) {
	mux.Route("/api/v1/posts", func(sr chi.Router) {
		if m.deps.RateLimitSvc != nil {
			sr.Use(WorkspacePostLimit(m.deps.RateLimitSvc))
		}
		sr.Post("/", m.deps.Protected(m.deps.CreatePost))
		sr.Get("/", m.deps.Protected(m.deps.ListPosts))
		sr.Get("/workspace/{wid}", m.deps.Protected(m.deps.ListPostsByWorkspace))
		sr.Get("/{id}", m.deps.Protected(m.deps.GetPost))
		sr.Patch("/{id}", m.deps.Protected(m.deps.PatchPost))
		sr.Delete("/{id}", m.deps.Protected(m.deps.DeletePost))
		sr.Post("/{id}/publish", m.deps.Protected(m.deps.PublishPost))
		sr.Post("/{id}/schedule", m.deps.Protected(m.deps.SchedulePost))
		sr.Post("/{id}/cancel", m.deps.Protected(m.deps.CancelPost))
		sr.Post("/{id}/retry", m.deps.Protected(m.deps.RetryPost))
		sr.Get("/{id}/targets", m.deps.Protected(m.deps.GetPostTargets))
		sr.Post("/{id}/targets", m.deps.Protected(m.deps.AddPostTarget))
	})
	mux.Route("/api/v1/post-targets", func(sr chi.Router) {
		// Taglio 5.1 step 2: single-target GET for the polling
		// frontend. Same /post-targets route group, hyphenated per
		// existing convention. The handler applies workspace
		// isolation in Go so we don't depend on a SQL JOIN for the
		// IDOR guard.
		sr.Get("/{id}", m.deps.Protected(m.deps.GetPostTarget))
		sr.Post("/{id}/retry", m.deps.Protected(m.deps.RetryTarget))
	})
	mux.Route("/api/v1/uploads", func(sr chi.Router) {
		sr.Get("/counts", m.deps.Protected(m.deps.UploadCounts))
		sr.Get("/", m.deps.Protected(m.deps.ListUploads))
		sr.Get("/by-account", m.deps.Protected(m.deps.ListUploadsByAccount))
		sr.Post("/batch/by-folder", m.deps.Protected(m.deps.UploadsBatchByFolder))
		sr.Patch("/{id}/reschedule", m.deps.Protected(m.deps.RescheduleUpload))
		sr.Patch("/{id}/metadata", m.deps.Protected(m.deps.EditScheduledUpload))
		sr.Delete("/{id}", m.deps.Protected(m.deps.CancelUpload))
	})
}
