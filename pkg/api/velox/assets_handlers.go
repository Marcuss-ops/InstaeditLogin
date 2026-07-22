package velox

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// getAsset implements GET /api/v1/velox/assets/{id}.
//
// Returns artifact metadata (sha256, size, mime_type, download_url)
// for a Velox-produced asset. The download_url is the Velox-internal
// presigned URL; the BFF returns it verbatim so the browser can fetch
// the artifact directly from Velox's CDN edge (not through the BFF).
//
// Verifies the asset belongs to the session's workspace before
// returning. Mismatch → 404 (no existence leak).
func (b *bff) getAsset(w http.ResponseWriter, req *http.Request) {
	wsID, userID, ok := b.requireIdentity(w, req)
	if !ok {
		return
	}
	assetID := chi.URLParam(req, "id")
	if assetID == "" {
		writeError(w, http.StatusBadRequest, "asset id required")
		return
	}
	asset, err := b.deps.Client.GetAsset(req.Context(), wsID, userID, assetID)
	if err != nil {
		slog.Error("velox bff: get asset failed", "asset_id", assetID, "err", err)
		mapClientError(w, err)
		return
	}
	if !verifyOwnership(w, asset.WorkspaceID, wsID) {
		return
	}
	writeJSON(w, http.StatusOK, asset)
}
