package bootstrap

import (
	"context"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/worker"
	"github.com/Marcuss-ops/InstaeditLogin/pkg/api"
)

// assetsAdapter bridges *repository.MediaAssetRepository to the
// resolver's services.MediaAssetStore interface (which takes a ctx
// first arg). The repo's FindByID is a sync lookup that doesn't
// take a ctx; the adapter accepts the ctx to keep the resolver API
// future-proof (when the repo upgrades to ctx-aware queries the
// adapter can forward ctx without changing the wiring site).
// (P3 — migration 080 followup; touch only RunWorkers + this type.)
type assetsAdapter struct {
	repo *repository.MediaAssetRepository
}

func (a assetsAdapter) FindForPost(ctx context.Context, workspaceID int64, assetID, bucket, key string) (*models.MediaAsset, error) {
	return a.repo.FindForPost(ctx, workspaceID, assetID, bucket, key)
}

// FindByUploadKey is the bridge for the resolver's legacy URL fallback.
// The repository applies the same workspace ownership predicate as the
// canonical asset-id path, so legacy media_url rows cannot bypass tenant
// isolation.
func (a assetsAdapter) FindByUploadKey(ctx context.Context, workspaceID int64, key string) (*models.MediaAsset, error) {
	return a.repo.FindByUploadKey(ctx, workspaceID, key)
}

// routerEditorSessionAdapter bridges worker.EditorSessionCreatorInput
// (internal/worker) → api.CreateEditorSessionInput (pkg/api). The two
// structs are deliberately different so worker (which pkg/api must
// not import) doesn't cycle back through pkg/api. Adapter pattern
// keeps the bridge in one place; the reconciler goroutine calls
// routerEditorSessionAdapter.CreateEditorSession(...) which hands
// off to Router.CreateEditorSession (the shared per-target 1:1
// helper that mints fresh uuids and validates workspace + account +
// channel + token + video-state invariants).
//
// Compile-time assertion in pkg/api/youtube_editor_sessions.go
// confirms *api.Router satisfies the predicate
// "CreateEditorSession(context.Context, CreateEditorSessionInput)
// (*models.YouTubeVideoEdit, error)". This adapter enforces field-
// by-field struct identity at runtime via Go's struct-literal type
// checking.
type routerEditorSessionAdapter struct {
	router *api.Router
}

// CreateEditorSession forwards to Router.CreateEditorSession. All
// sentinel errors propagate untouched so the reconciler can branch
// on errors.Is for retry/skip decisions (see writeEditorSessionError
// for the HTTP-side mapping).
func (a *routerEditorSessionAdapter) CreateEditorSession(ctx context.Context, in worker.EditorSessionCreatorInput) (*models.YouTubeVideoEdit, error) {
	edit, _, err := a.router.CreateEditorSession(ctx, api.CreateEditorSessionInput{
		WorkspaceID:        in.WorkspaceID,
		PlatformAccountID:  in.PlatformAccountID,
		YouTubeVideoID:     in.YouTubeVideoID,
		SourceThumbnailURL: in.SourceThumbnailURL,
	})
	return edit, err
}
