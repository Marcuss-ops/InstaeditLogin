package api

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// StorageProvider abstracts presigned-URL minting so the API layer stays
// storage-agnostic. Production wires services.NewSupabaseProvider or
// services.NewS3Provider (see /api/v1/storage/upload-url handler); tests
// inject a stub implementing the same interface.
//
// The signature mirrors services.StorageProvider exactly so a real
// implementation can be passed through WithStorageProvider without
// adapter wrappers.
type StorageProvider interface {
	Provider() string
	SignUpload(ctx context.Context, userID int64, key, contentType string, sizeBytes int64, ttl time.Duration) (*services.UploadGrant, error)
}

// RouterOption mutates the Router at construction time so callers can
// inject optional dependencies without breaking the NewRouter signature.
// Functional-options pattern: each new persistence layer (workspaces,
// posts, future rate limiters, etc.) gets its own WithXxxStore opt.
type RouterOption func(*Router)

// WithWorkspaceStore injects the workspace persistence layer. Without it,
// the workspace handlers return 501 Not Implemented — useful for partial
// rollouts where the workspace domain isn't wired yet.
func WithWorkspaceStore(repo WorkspaceStore) RouterOption {
	return func(r *Router) {
		r.workspaceRepo = repo
	}
}

// WithPostStore injects the post + post_targets persistence layer.
// Without it, the post handlers return 501 Not Implemented.
func WithPostStore(repo PostStore) RouterOption {
	return func(r *Router) {
		r.postRepo = repo
	}
}

// WithStorageProvider injects the presigned-URL minting layer.
// Without it, /api/v1/storage/upload-url returns 501 Not Implemented.
func WithStorageProvider(p StorageProvider) RouterOption {
	return func(r *Router) {
		r.storageProvider = p
	}
}

// WithMaxUploadBytes caps the per-file size the API will accept at
// /api/v1/storage/upload-url. When unset, the handler falls back to
// defaultMaxUploadBytes (200 MiB) — see /api/v1/storage/upload-url.
func WithMaxUploadBytes(n int64) RouterOption {
	return func(r *Router) {
		r.maxUploadBytes = n
	}
}

// parsePathIDAsInt64 extracts a numeric path parameter (e.g. `{id}`) and
// writes a 400 Bad Request if it's missing or not a positive int64.
// Returns (id, true) on success; the caller should bail if ok is false —
// the response is already written.
func parsePathIDAsInt64(w http.ResponseWriter, req *http.Request, paramName string) (int64, bool) {
	s := req.PathValue(paramName)
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		writeError(w, http.StatusBadRequest, "invalid "+paramName+": "+s)
		return 0, false
	}
	return n, true
}

// requireUserID resolves the caller's user_id from JWT context (or the
// fallback in non-strict mode) and writes 401 if absent. Returns
// (userID, true) on success.
func requireUserID(w http.ResponseWriter, req *http.Request, r *Router) (int64, bool) {
	userID := resolveUserID(req, 0, r.strictAuth)
	if userID == 0 {
		writeError(w, http.StatusUnauthorized, "missing user identity")
		return 0, false
	}
	return userID, true
}

// logAndError logs err with structured context and writes a 500 JSON
// response. Centralizes the slog+writeError pair so handlers stay short.
func logAndError(w http.ResponseWriter, msg string, err error, kv ...any) {
	slog.Error(msg, append([]any{"error", err}, kv...)...)
	writeError(w, http.StatusInternalServerError, msg+": "+err.Error())
}
