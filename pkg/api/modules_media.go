package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// MediaModuleDeps is the narrow set of dependencies the media module
// needs to mount its routes.
type MediaModuleDeps struct {
	RateLimitSvc       *services.RateLimitService
	Protected          func(http.HandlerFunc) http.HandlerFunc
	PresignMedia       http.HandlerFunc
	DriveImport        http.HandlerFunc
	DriveImportAsync   http.HandlerFunc
	DriveBatchImport   http.HandlerFunc
	DriveBatchImportV2 http.HandlerFunc
	DriveBatchV2Status http.HandlerFunc
	DriveBatchStatus   http.HandlerFunc
	CompleteMedia      http.HandlerFunc
	// ListMedia backs GET /api/v1/media — the reduced Media Library list.
	ListMedia http.HandlerFunc
	// GetMedia backs GET /api/v1/media/{id} — detail + on-demand preview.
	GetMedia        http.HandlerFunc
	ListDriveAssets http.HandlerFunc
	GetDriveAsset   http.HandlerFunc
}

// MediaModule mounts the presigned-upload and Drive-import routes.
type MediaModule struct {
	deps MediaModuleDeps
}

func NewMediaModule(deps MediaModuleDeps) RouteModule {
	return &MediaModule{deps: deps}
}

// Compile-time assertion: MediaModule implements RouteModule.
var _ RouteModule = (*MediaModule)(nil)

func (m *MediaModule) Register(mux chi.Router) {
	var mediaPresignMw []func(http.Handler) http.Handler
	if m.deps.RateLimitSvc != nil {
		mediaPresignMw = append(mediaPresignMw, MediaPresignLimit(m.deps.RateLimitSvc))
	}
	mux.Method(http.MethodPost, "/api/v1/media/presign", chain(m.deps.Protected(m.deps.PresignMedia), mediaPresignMw...))
	mux.Method(http.MethodPost, "/api/v1/media/import/drive", m.deps.Protected(m.deps.DriveImport))
	mux.Method(http.MethodPost, "/api/v1/media/import/drive/async", m.deps.Protected(m.deps.DriveImportAsync))
	mux.Method(http.MethodPost, "/api/v1/media/import/drive/folder", m.deps.Protected(m.deps.DriveBatchImport))
	mux.Method(http.MethodPost, "/api/v1/media/import/drive/folder/async", m.deps.Protected(m.deps.DriveBatchImportV2))
	mux.Method(http.MethodGet, "/api/v1/media/import/drive/folder/async/{id}", m.deps.Protected(m.deps.DriveBatchV2Status))
	mux.Method(http.MethodGet, "/api/v1/media/import/drive/batch/status", m.deps.Protected(m.deps.DriveBatchStatus))
	mux.Method(http.MethodPost, "/api/v1/media/{id}/complete", m.deps.Protected(m.deps.CompleteMedia))
	mux.Method(http.MethodGet, "/api/v1/media", m.deps.Protected(m.deps.ListMedia))
	mux.Method(http.MethodGet, "/api/v1/media/{id}", m.deps.Protected(m.deps.GetMedia))
	mux.Method(http.MethodGet, "/api/v1/drive/assets", m.deps.Protected(m.deps.ListDriveAssets))
	mux.Method(http.MethodGet, "/api/v1/drive/assets/{id}/content", m.deps.Protected(m.deps.GetDriveAsset))
}
