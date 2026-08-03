package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
	"github.com/Marcuss-ops/InstaeditLogin/internal/thumbnailrender"
)

// renderUploadTTL is how long the presigned PUT URL for the rendered
// file stays valid. The file is tiny (a thumbnail) and uploaded
// server-side immediately, so a short window suffices.
const renderUploadTTL = 10 * time.Minute

// renderMediaFetchTTL is how long the presigned GET URL used to fetch
// referenced media assets stays valid while rendering image objects.
const renderMediaFetchTTL = 5 * time.Minute

// renderExportAssetTTL is the lifetime of the media_assets row created
// for a rendered export. Render exports are persistent artifacts (DoD:
// "Export persistente e verificabile"), so they deliberately do NOT
// use the publish-horizon formula that governs scheduled uploads.
const renderExportAssetTTL = 365 * 24 * time.Hour

// renderS3UploadTimeout bounds the server-side PUT of the rendered
// bytes to MinIO/S3.
const renderS3UploadTimeout = 2 * time.Minute

// maxRenderImageAssetBytes caps how much of a referenced media asset
// the render path will download for an image object (32 MiB).
const maxRenderImageAssetBytes = 32 << 20

// Render errors that distinguish client-fixable problems (bad snapshot
// or unresolvable media reference) from infrastructure failures.
var (
	errRenderMediaNotFound = errors.New("thumbnail render: media asset not found")
	errRenderMediaNotReady = errors.New("thumbnail render: media asset is not ready")
	errRenderMediaExpired  = errors.New("thumbnail render: media asset is expired")
	errRenderMediaFetch    = errors.New("thumbnail render: failed to fetch media asset bytes")
)

// thumbnailRenderRequest is the body for
// POST /api/v1/thumbnail-projects/{id}/render. Every field is optional:
// the renderer derives the revision, content type and dimensions from
// the project state when omitted.
type thumbnailRenderRequest struct {
	// RevisionID pins the render to a specific revision. When empty the
	// project's current_revision_id is used.
	RevisionID string `json:"revision_id,omitempty"`
	// ContentType defaults to image/png; image/jpeg is also supported.
	ContentType string `json:"content_type,omitempty"`
	// Width/Height override the project canvas dimensions.
	Width  int `json:"width,omitempty"`
	Height int `json:"height,omitempty"`
}

// handleRenderThumbnailProject implements the canonical render path:
//
//	persisted snapshot → deterministic PNG/JPEG → media_assets ready
//	(MinIO via the shared Media Library storage) → thumbnail_exports
//	with SHA-256, dimensions, file_size and renderer_version → project
//	latest_export_id + preview_media_id updated.
//
// It requires no YouTube channel, video, or OAuth connection. The
// requesting user must have access to the workspace; referenced media
// assets must be owned by the same user (cross-workspace images are
// rejected).
func (r *Router) handleRenderThumbnailProject(w http.ResponseWriter, req *http.Request) {
	if r.thumbnailProjectStore == nil {
		writeError(w, http.StatusNotImplemented, "thumbnail projects not configured on this server")
		return
	}
	if r.mediaStore == nil || r.storageProvider == nil {
		writeError(w, http.StatusNotImplemented, "media storage not configured on this server")
		return
	}
	workspaceID, ok := parseThumbnailWorkspaceQuery(w, req)
	if !ok {
		return
	}
	userID, ok := r.thumbnailProjectWorkspace(w, req, workspaceID)
	if !ok {
		return
	}
	projectID, ok := parseThumbnailProjectID(w, req)
	if !ok {
		return
	}
	var body thumbnailRenderRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 1<<20)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid thumbnail render body")
		return
	}

	contentType := strings.ToLower(strings.TrimSpace(body.ContentType))
	if contentType == "" {
		contentType = models.ThumbnailProjectExportContentTypePNG
	}
	if contentType != models.ThumbnailProjectExportContentTypePNG &&
		contentType != models.ThumbnailProjectExportContentTypeJPEG {
		writeError(w, http.StatusUnprocessableEntity, "content_type must be image/png or image/jpeg")
		return
	}

	project, err := r.thumbnailProjectStore.FindByID(req.Context(), workspaceID, projectID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "find thumbnail project: "+err.Error())
		return
	}
	if project == nil || project.Status == models.ThumbnailProjectStatusDeleted {
		writeError(w, http.StatusNotFound, "thumbnail project not found")
		return
	}

	revisionID := strings.TrimSpace(body.RevisionID)
	if revisionID == "" {
		if project.CurrentRevisionID == nil || *project.CurrentRevisionID == "" {
			writeError(w, http.StatusUnprocessableEntity, "project has no saved snapshot; save a snapshot before rendering")
			return
		}
		revisionID = *project.CurrentRevisionID
	}
	revision, err := r.thumbnailProjectStore.FindRevision(req.Context(), workspaceID, projectID, revisionID)
	if err != nil || revision == nil {
		mapThumbnailRevisionError(w, err)
		return
	}

	width, height := project.CanvasWidth, project.CanvasHeight
	if body.Width > 0 {
		width = body.Width
	}
	if body.Height > 0 {
		height = body.Height
	}
	if width <= 0 || height <= 0 || width > 16384 || height > 16384 {
		writeError(w, http.StatusUnprocessableEntity, "invalid render dimensions")
		return
	}

	scene, err := thumbnailrender.Parse(revision.SnapshotJSON, width, height)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	// Resolve every media asset referenced by image objects BEFORE
	// rendering so a missing/foreign/unready asset fails fast with a
	// clear 422 instead of surfacing mid-render. Cross-workspace media
	// is rejected (DoD: "Cross-workspace bloccato").
	mediaBytes := make(map[string][]byte, len(scene.Objects))
	for i := range scene.Objects {
		o := &scene.Objects[i]
		if o.Type != "image" || !o.Visible {
			continue
		}
		data, rerr := r.fetchRenderMediaBytes(req.Context(), userID, o.MediaID)
		if rerr != nil {
			writeRenderMediaError(w, rerr)
			return
		}
		mediaBytes[o.MediaID] = data
	}
	resolve := func(_ context.Context, mediaID string) ([]byte, error) {
		data, ok := mediaBytes[mediaID]
		if !ok {
			return nil, thumbnailrender.ErrMediaNotFound
		}
		return data, nil
	}

	rendered, err := scene.Render(req.Context(), contentType, resolve)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	// --- Persist the rendered file through the shared Media Library ---
	shaSum := sha256.Sum256(rendered)
	shaHex := hex.EncodeToString(shaSum[:])
	sizeBytes := int64(len(rendered))
	ext := "png"
	if contentType == models.ThumbnailProjectExportContentTypeJPEG {
		ext = "jpeg"
	}
	key := services.BuildUploadKey(userID, "thumbnail_export."+ext)
	asset := &models.MediaAsset{
		UserID:      userID,
		UploadKey:   key,
		Bucket:      storageBucket(r.storageProvider),
		ContentType: contentType,
		SizeBytes:   sizeBytes,
		SHA256:      shaHex,
		Status:      models.MediaAssetStatusPending,
		ExpiresAt:   time.Now().Add(renderExportAssetTTL),
	}
	if err := r.mediaStore.Create(asset); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create media asset: "+err.Error())
		return
	}

	grant, err := r.storageProvider.SignUpload(req.Context(), userID, key, contentType, sizeBytes, renderUploadTTL)
	if err != nil {
		safeMarkFailed(req.Context(), slog.Default(), r.mediaStore, asset.ID, err.Error(), err)
		writeError(w, http.StatusInternalServerError, "failed to sign render upload: "+err.Error())
		return
	}

	uploadReq, err := http.NewRequestWithContext(req.Context(), http.MethodPut, grant.UploadURL, bytes.NewReader(rendered))
	if err != nil {
		safeMarkFailed(req.Context(), slog.Default(), r.mediaStore, asset.ID, err.Error(), err)
		writeError(w, http.StatusInternalServerError, "failed to build render upload request: "+err.Error())
		return
	}
	uploadReq.Header.Set("Content-Type", contentType)
	uploadReq.ContentLength = sizeBytes
	uploadClient := r.thumbnailDownloadClient
	if uploadClient == nil {
		uploadClient = &http.Client{Timeout: renderS3UploadTimeout}
	}
	uploadResp, err := uploadClient.Do(uploadReq)
	if err != nil {
		safeMarkFailed(req.Context(), slog.Default(), r.mediaStore, asset.ID, err.Error(), err)
		writeError(w, http.StatusBadGateway, "failed to upload render to storage: "+err.Error())
		return
	}
	uploadResp.Body.Close()
	if uploadResp.StatusCode >= 300 {
		reason := fmt.Sprintf("render storage upload returned %d", uploadResp.StatusCode)
		safeMarkFailed(req.Context(), slog.Default(), r.mediaStore, asset.ID, reason, errors.New(reason))
		writeError(w, http.StatusBadGateway, reason)
		return
	}
	if err := r.mediaStore.MarkReady(asset.ID, shaHex, sizeBytes, contentType); err != nil {
		safeMarkFailed(req.Context(), slog.Default(), r.mediaStore, asset.ID, err.Error(), err)
		writeError(w, http.StatusInternalServerError, "failed to mark render asset ready: "+err.Error())
		return
	}

	// --- Two-phase export lifecycle: rendering → ready ---
	export := &models.ThumbnailExport{
		ProjectID:       projectID,
		RevisionID:      revisionID,
		MediaID:         asset.ID,
		ContentType:     contentType,
		Width:           width,
		Height:          height,
		FileSize:        sizeBytes,
		SHA256:          shaSum[:],
		RendererVersion: thumbnailrender.RendererVersion,
		Status:          models.ThumbnailProjectExportStatusRendering,
	}
	if err := r.thumbnailProjectStore.CreateExport(req.Context(), workspaceID, export); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if err := r.thumbnailProjectStore.UpdateExportStatus(req.Context(), workspaceID, export.ID,
		models.ThumbnailProjectExportStatusReady, "", shaSum[:], sizeBytes, thumbnailrender.RendererVersion); err != nil {
		writeError(w, http.StatusInternalServerError, "export rendered but failed to finalize: "+err.Error())
		return
	}
	export.Status = models.ThumbnailProjectExportStatusReady
	writeJSON(w, http.StatusCreated, export)
}

// fetchRenderMediaBytes downloads the bytes of a ready, non-expired
// media asset owned by userID via a fresh presigned GET URL.
func (r *Router) fetchRenderMediaBytes(ctx context.Context, userID int64, mediaID string) ([]byte, error) {
	asset, err := r.mediaStore.FindByID(mediaID)
	if err != nil {
		return nil, fmt.Errorf("%w: lookup %s: %v", errRenderMediaFetch, mediaID, err)
	}
	if asset == nil {
		return nil, fmt.Errorf("%w: %s", errRenderMediaNotFound, mediaID)
	}
	if asset.UserID != userID {
		// Do not leak existence across users/workspaces.
		return nil, fmt.Errorf("%w: %s", errRenderMediaNotFound, mediaID)
	}
	if asset.Status != models.MediaAssetStatusReady {
		return nil, fmt.Errorf("%w: %s (status=%s)", errRenderMediaNotReady, mediaID, asset.Status)
	}
	if time.Now().After(asset.ExpiresAt) {
		return nil, fmt.Errorf("%w: %s", errRenderMediaExpired, mediaID)
	}
	url, err := r.storageProvider.GetObject(ctx, asset.UploadKey, renderMediaFetchTTL)
	if err != nil {
		return nil, fmt.Errorf("%w: sign %s: %v", errRenderMediaFetch, mediaID, err)
	}
	client := r.thumbnailDownloadClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("%w: get %s: %v", errRenderMediaFetch, mediaID, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: %s returned %d", errRenderMediaFetch, mediaID, resp.StatusCode)
	}
	if resp.ContentLength > maxRenderImageAssetBytes {
		return nil, fmt.Errorf("%w: %s exceeds %d bytes", errRenderMediaFetch, mediaID, maxRenderImageAssetBytes)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxRenderImageAssetBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: read %s: %v", errRenderMediaFetch, mediaID, err)
	}
	if len(body) > maxRenderImageAssetBytes {
		return nil, fmt.Errorf("%w: %s exceeds %d bytes", errRenderMediaFetch, mediaID, maxRenderImageAssetBytes)
	}
	return body, nil
}

// writeRenderMediaError maps the render media sentinel errors to HTTP
// responses. All of them are client-fixable (bad snapshot reference),
// so they map to 422; the message stays generic to avoid leaking
// whether a foreign asset exists.
func writeRenderMediaError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errRenderMediaNotFound),
		errors.Is(err, errRenderMediaNotReady),
		errors.Is(err, errRenderMediaExpired),
		errors.Is(err, errRenderMediaFetch):
		writeError(w, http.StatusUnprocessableEntity, "thumbnail render: referenced media asset is unavailable")
	default:
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	}
}

// handleGetThumbnailExport returns a workspace-scoped rendered export
// (GET /api/v1/thumbnail-exports/{export_id}).
func (r *Router) handleGetThumbnailExport(w http.ResponseWriter, req *http.Request) {
	if r.thumbnailProjectStore == nil {
		writeError(w, http.StatusNotImplemented, "thumbnail projects not configured on this server")
		return
	}
	workspaceID, ok := parseThumbnailWorkspaceQuery(w, req)
	if !ok {
		return
	}
	if _, ok := r.thumbnailProjectWorkspace(w, req, workspaceID); !ok {
		return
	}
	exportID := strings.TrimSpace(chi.URLParam(req, "export_id"))
	if exportID == "" {
		writeError(w, http.StatusBadRequest, "thumbnail export id is required")
		return
	}
	export, err := r.thumbnailProjectStore.FindExport(req.Context(), workspaceID, exportID)
	if err != nil {
		if errors.Is(err, repository.ErrThumbnailExportNotFound) {
			writeError(w, http.StatusNotFound, "thumbnail export not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "find thumbnail export: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, export)
}
