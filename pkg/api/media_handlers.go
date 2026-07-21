package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// MediaStore is the narrow contract for media-asset CRUD.
// Implemented by *repository.MediaAssetRepository in production;
// tests inject a small in-memory mock. Defined here (not in
// repository) so pkg/api has zero compile-time dependency on the
// internal repository package.
type MediaStore interface {
	Create(asset *models.MediaAsset) error
	FindByID(id string) (*models.MediaAsset, error)
	MarkReady(id, sha256 string, sizeBytes int64, contentType string) error
	MarkFailed(id, reason string) error
	// MarkFailedWithReason is the diagnose-friendly variant: passes
	// the underlying `cause` so the implementation can log it
	// alongside any persist failure. Use this in caller code where
	// you have a typed `err` and want to preserve its value across
	// the MarkFailed boundary.
	MarkFailedWithReason(id, reason string, cause error) error
}

// WithMediaStore injects the media-asset repository into the router.
// Mirrors the pattern of WithPostStore / WithStorageProvider.
func WithMediaStore(s MediaStore) RouterOption {
	return func(r *Router) { r.mediaStore = s }
}

// PresignMediaRequest is the body for POST /api/v1/media/presign.
// The client declares what it wants to upload; the server returns a
// presigned URL + an asset_id the client commits via /complete after
// the PUT succeeds.
type PresignMediaRequest struct {
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	SizeBytes   int64  `json:"size_bytes"`
	// SHA256 is optional but recommended: lets the client commit to
	// a hash at presign time. The /complete handler can then verify
	// the S3-returned hash against this expected value.
	SHA256 string `json:"sha256,omitempty"`
}

// PresignMediaResponse is the response for POST /api/v1/media/presign.
// The client uses upload_url directly to PUT the file; the asset_id
// is committed via POST /api/v1/media/{asset_id}/complete.
type PresignMediaResponse struct {
	AssetID       string            `json:"asset_id"`
	UploadURL     string            `json:"upload_url"`
	UploadMethod  string            `json:"upload_method"`
	UploadHeaders map[string]string `json:"upload_headers"`
	ExpiresAt     time.Time         `json:"expires_at"`
	ContentType   string            `json:"content_type"`
	MaxSizeBytes  int64             `json:"max_size_bytes"`
}

// mediaPresignTTL is how long the generated upload_url remains valid.
// Long enough for a slow mobile upload (~250 KB/s for a 200 MiB file =
// ~13 min), short enough that leaked URLs don't live forever.
const mediaPresignTTL = 15 * time.Minute

// mediaAssetLifetime is how long an asset is valid for use in posts
// after presign. After this expires, /complete returns 410 Gone and
// the publish flow rejects the asset. 24h is generous for an
// authoring flow (upload + post + schedule) without letting stale
// assets linger indefinitely.
const mediaAssetLifetime = 24 * time.Hour

// handlePresignMedia (POST /api/v1/media/presign, protected) creates
// a media_assets row in `pending` state and returns a presigned S3
// PUT URL the client uses to upload directly. The PUT itself is
// handled by S3 (NOT by us) — there is no Taglio 3.2 endpoint that
// receives the file body.
//
// Validation (422 on semantic errors, 400 on malformed JSON):
//   - JWT user identity required
//   - content_type must be in the allowlist (image/jpeg, image/png,
//     image/webp, video/mp4, video/quicktime)
//   - size_bytes > 0
//   - size_bytes ≤ MaxUploadBytes (default 200 MiB)
//
// Success: 200 with {asset_id, upload_url, upload_method, upload_headers,
// expires_at, content_type, max_size_bytes}.
func (r *Router) handlePresignMedia(w http.ResponseWriter, req *http.Request) {
	if r.storageProvider == nil || r.mediaStore == nil {
		writeError(w, http.StatusNotImplemented, "media not configured on this server")
		return
	}
	userID, ok := requireUserID(w, req, r)
	if !ok {
		return
	}

	var body PresignMediaRequest
	req.Body = http.MaxBytesReader(w, req.Body, 1<<20) // 1 MB cap; far below storage cap
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	if body.Filename == "" {
		writeError(w, http.StatusUnprocessableEntity, "filename is required")
		return
	}
	if !allowedUploadContentTypes[body.ContentType] {
		writeError(w, http.StatusUnprocessableEntity,
			"content_type must be one of: image/jpeg, image/png, image/webp, video/mp4, video/quicktime")
		return
	}
	if body.SizeBytes <= 0 {
		writeError(w, http.StatusUnprocessableEntity, "size_bytes must be > 0")
		return
	}
	maxBytes := r.maxUploadBytes
	if maxBytes <= 0 {
		maxBytes = defaultMaxUploadBytes
	}
	if body.SizeBytes > maxBytes {
		writeError(w, http.StatusUnprocessableEntity,
			fmt.Sprintf("size_bytes exceeds limit (%d bytes max)", maxBytes))
		return
	}

	key := services.BuildUploadKey(userID, body.Filename)
	grant, err := r.storageProvider.SignUpload(
		req.Context(), userID, key,
		body.ContentType, body.SizeBytes, mediaPresignTTL,
	)
	if err != nil {
		logAndError(w, req, "failed to sign media upload", err, "user_id", userID)
		return
	}

	asset := &models.MediaAsset{
		UserID:      userID,
		UploadKey:   key,
		ContentType: body.ContentType,
		SizeBytes:   body.SizeBytes,
		SHA256:      body.SHA256,
		Status:      models.MediaAssetStatusPending,
		ExpiresAt:   time.Now().Add(mediaAssetLifetime),
	}
	if err := r.mediaStore.Create(asset); err != nil {
		logAndError(w, req, "failed to create media asset", err, "user_id", userID)
		return
	}

	writeJSON(w, http.StatusOK, PresignMediaResponse{
		AssetID:       asset.ID,
		UploadURL:     grant.UploadURL,
		UploadMethod:  http.MethodPut,
		UploadHeaders: map[string]string{"Content-Type": body.ContentType},
		ExpiresAt:     grant.ExpiresAt,
		ContentType:   body.ContentType,
		MaxSizeBytes:  maxBytes,
	})
}

// handleCompleteMedia (POST /api/v1/media/{id}/complete, protected)
// verifies the S3 upload by HEADing the object, then transitions
// the asset to `ready`. The body is optional — clients may include
// a sha256 to commit to a specific hash, but the server does not
// re-compute the S3 object hash (S3 doesn't expose it cheaply).
//
// Error cases:
//   - asset not found    → 404
//   - asset not owned    → 403 (don't leak existence to non-owners)
//   - already complete   → 200 idempotent return
//   - expired            → 410 Gone
//   - S3 object missing  → 400 (asset stays pending; client can retry)
//   - size mismatch      → 422 (asset transitions to failed)
//   - content-type mismatch → 422 (asset transitions to failed)
func (r *Router) handleCompleteMedia(w http.ResponseWriter, req *http.Request) {
	if r.storageProvider == nil || r.mediaStore == nil {
		writeError(w, http.StatusNotImplemented, "media not configured on this server")
		return
	}
	userID, ok := requireUserID(w, req, r)
	if !ok {
		return
	}
	id := chi.URLParam(req, "id")
	asset, err := r.mediaStore.FindByID(id)
	if err != nil {
		logAndError(w, req, "failed to find media asset", err, "asset_id", id)
		return
	}
	if asset == nil {
		writeError(w, http.StatusNotFound, "media asset not found")
		return
	}
	if asset.UserID != userID {
		// Don't leak existence: same response as not-found.
		writeError(w, http.StatusNotFound, "media asset not found")
		return
	}
	if asset.Status == models.MediaAssetStatusReady {
		// Idempotent: already complete. Return current record.
		writeJSON(w, http.StatusOK, asset)
		return
	}
	if asset.Status == models.MediaAssetStatusExpired || time.Now().After(asset.ExpiresAt) {
		writeError(w, http.StatusGone, "media asset expired; please re-upload")
		return
	}
	// Task 6/10 contract enforcement — reject empty SHA upfront
	// so the asset never reaches MarkReady with the legacy "no SHA
	// recorded" sentinel. The repo's MarkReady now also refuses
	// empty (ErrMediaAssetSHARequired), but rejecting here avoids
	// the S3 HEAD round-trip + content-type / size verification
	// work for a request that's guaranteed to fail at the persist
	// step. Clients MUST commit to the SHA at /presign time (the
	// PresignMediaRequest.sha256 field) so this branch is the
	// exception, not the rule. Future enhancement: support a
	// /complete body carrying a sha256 override for clients that
	// skipped the presign SHA but want to commit now (would also
	// need to handle the case where the SHA differs from the
	// presign-declared value — locked as followup).
	if asset.SHA256 == "" {
		_ = r.mediaStore.MarkFailed(id, "sha256 required: client must compute SHA-256 locally and pass it in the /presign body before /complete (Task 6/10 enforcement)")
		writeError(w, http.StatusBadRequest,
			"sha256 required: client must compute SHA-256 locally and pass it in the /presign body before /complete")
		return
	}

	// HEAD the S3 object to confirm the upload actually landed.
	contentType, sizeBytes, err := r.storageProvider.VerifyUpload(req.Context(), asset.UploadKey)
	if err != nil {
		_ = r.mediaStore.MarkFailed(id, err.Error())
		writeError(w, http.StatusBadRequest, "media upload verification failed: "+err.Error())
		return
	}
	if sizeBytes != asset.SizeBytes {
		reason := fmt.Sprintf("size mismatch: uploaded=%d expected=%d", sizeBytes, asset.SizeBytes)
		_ = r.mediaStore.MarkFailed(id, reason)
		writeError(w, http.StatusUnprocessableEntity, reason)
		return
	}
	if contentType != asset.ContentType {
		reason := fmt.Sprintf("content-type mismatch: uploaded=%q expected=%q", contentType, asset.ContentType)
		_ = r.mediaStore.MarkFailed(id, reason)
		writeError(w, http.StatusUnprocessableEntity, reason)
		return
	}
	if err := r.mediaStore.MarkReady(id, asset.SHA256, sizeBytes, contentType); err != nil {
		logAndError(w, req, "failed to mark media asset ready", err, "asset_id", id)
		return
	}
	// Re-fetch to return the updated record.
	updated, _ := r.mediaStore.FindByID(id)
	if updated == nil {
		updated = asset
	}
	writeJSON(w, http.StatusOK, updated)
}

// --- Request types for the new publish payload (Taglio 3.2) ---

// MediaRef is a reference to a verified media asset. The public
// publish payload uses asset_id (NOT media_url) so the only URL the
// platform API ever receives is the trusted internal S3 URL built
// from asset_id by the handler.
type MediaRef struct {
	AssetID string `json:"asset_id"`
}

// resolveMediaURLs looks up the assets behind the MediaRef slice and
// returns a list of internal S3 URLs (in the same order). Returns
// an error if any asset is missing, not owned by userID, not
// `ready`, or expired. This is the single chokepoint where a
// user-supplied asset_id is converted to a trusted URL — the only
// URL the platform API ever sees.
func (r *Router) resolveMediaURLs(_ context.Context, userID int64, refs []MediaRef) ([]string, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	if r.mediaStore == nil || r.storageProvider == nil {
		return nil, fmt.Errorf("media not configured on this server")
	}
	urls := make([]string, 0, len(refs))
	for _, ref := range refs {
		asset, err := r.mediaStore.FindByID(ref.AssetID)
		if err != nil {
			return nil, fmt.Errorf("lookup asset %s: %w", ref.AssetID, err)
		}
		if asset == nil {
			return nil, fmt.Errorf("media asset %s not found", ref.AssetID)
		}
		if asset.UserID != userID {
			return nil, fmt.Errorf("media asset %s not owned by this user", ref.AssetID)
		}
		if asset.Status != models.MediaAssetStatusReady {
			return nil, fmt.Errorf("media asset %s is not ready (status=%s)", ref.AssetID, asset.Status)
		}
		if time.Now().After(asset.ExpiresAt) {
			return nil, fmt.Errorf("media asset %s expired", ref.AssetID)
		}
		urls = append(urls, r.storageProvider.AssetURL(asset.UploadKey))
	}
	return urls, nil
}

// resolveFirstMediaURL is the single-asset variant used by
// handleCreatePost. Returns the first media URL (or "" when the
// request has no media). The Post struct's MediaURL is populated
// from this so the publish worker can continue to use the existing
// post.MediaURL → PublishPayload.{ImageURL,VideoURL} flow without
// per-platform service changes.
func (r *Router) resolveFirstMediaURL(userID int64, refs []MediaRef) (string, error) {
	urls, err := r.resolveMediaURLs(context.Background(), userID, refs)
	if err != nil {
		return "", err
	}
	if len(urls) == 0 {
		return "", nil
	}
	return urls[0], nil
}
