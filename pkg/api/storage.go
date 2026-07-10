package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// CreateUploadURLReq is the body schema for POST /api/v1/storage/upload-url.
// The client describes the file it wants to upload; the server responds
// with a presigned URL the client can PUT the file to.
type CreateUploadURLReq struct {
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	SizeBytes   int64  `json:"size_bytes"`
}

// allowedUploadContentTypes is the strict allowlist for content types the
// client may upload. Anything outside this set is rejected with 422 —
// prevents uploading `text/html` (XSS risk if the storage ever serves
// with HTML content-type), `application/x-msdownload` (malware), or any
// type that social platforms can't consume.
//
// Sets stay small and conservative; if a use case needs more, add them
// here AND configure the corresponding Content-Type on the bucket.
var allowedUploadContentTypes = map[string]bool{
	"image/jpeg":      true,
	"image/png":       true,
	"image/webp":      true,
	"video/mp4":       true,
	"video/quicktime": true,
}

// uploadURLTTL is how long the generated upload_url remains valid. Long
// enough for a slow upload on mobile (~250 KB/s), short enough that
// leaked URLs don't live forever in client logs.
const uploadURLTTL = 15 * time.Minute

// defaultMaxUploadBytes is the fallback cap when MaxUploadBytes isn't
// wired (Router.maxUploadBytes == 0).
const defaultMaxUploadBytes int64 = 200 * 1024 * 1024 // 200 MiB

// handleCreateUploadURL (POST /api/v1/storage/upload-url, protected)
// generates a presigned upload URL. Validation:
//   - storage configured    → 501 otherwise
//   - JWT user identity     → 401 if missing (via requireUserID)
//   - filename non-empty    → 422
//   - content_type in allowlist (image/jpeg, image/png, image/webp,
//     video/mp4, video/quicktime) → 422 otherwise
//   - size_bytes > 0        → 422
//   - size_bytes ≤ MaxUploadBytes (or default 200 MiB) → 422 otherwise
//   - malformed JSON        → 400
//
// Success returns the presigned UploadGrant (upload_url + media_url +
// expires_at) so the client can PUT the file then reference media_url
// as Post.MediaURL when calling POST /api/v1/posts.
func (r *Router) handleCreateUploadURL(w http.ResponseWriter, req *http.Request) {
	if r.storageProvider == nil {
		writeError(w, http.StatusNotImplemented, "storage not configured on this server")
		return
	}
	userID, ok := requireUserID(w, req, r)
	if !ok {
		return
	}

	var body CreateUploadURLReq
	req.Body = http.MaxBytesReader(w, req.Body, 1<<20) // 1 MB cap; far below storage cap
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	// Semantic validation (422 per codebase convention).
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
	grant, err := r.storageProvider.SignUpload(req.Context(), userID, key,
		body.ContentType, body.SizeBytes, uploadURLTTL)
	if err != nil {
		logAndError(w, "failed to sign upload", err,
			"user_id", userID, "provider", r.storageProvider.Provider())
		return
	}
	writeJSON(w, http.StatusOK, grant)
}
