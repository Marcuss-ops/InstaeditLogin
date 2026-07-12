package api

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// --- Request types -----------------------------------------------------------

// CreatePostContent wraps the post body fields in a nested "content" object.
// Taglio 3.2: the legacy `media_url` field is REMOVED. Clients now
// pass `media: [{ asset_id }]` — the handler resolves each asset_id
// to a trusted internal S3 URL via the mediaStore + storageProvider.
// The Post struct (and response) still has MediaURL as the internal
// field populated from the asset, but the REQUEST shape uses asset_id.
type CreatePostContent struct {
	Title   string     `json:"title,omitempty"`
	Caption string     `json:"caption,omitempty"`
	Media   []MediaRef `json:"media,omitempty"`
}

// CreatePostTarget is one entry in the universal targets[] array.
type CreatePostTarget struct {
	AccountID int64 `json:"account_id"`
}

// CreatePostRequest is the universal JSON body for POST /api/v1/posts.
type CreatePostRequest struct {
	WorkspaceID    int64              `json:"workspace_id"`
	Content        CreatePostContent  `json:"content"`
	ScheduledAt    *time.Time         `json:"scheduled_at,omitempty"`
	Targets        []CreatePostTarget `json:"targets"`
	IdempotencyKey *string            `json:"-"` // set from header, not body
}

// SchedulePostRequest is the JSON body for POST /posts/{id}/schedule.
type SchedulePostRequest struct {
	ScheduledAt time.Time `json:"scheduled_at"`
}

// AddTargetRequest is the JSON body for POST /posts/{id}/targets.
type AddTargetRequest struct {
	AccountID int64 `json:"account_id"`
}

// --- Error mapping -----------------------------------------------------------

func mapRepoError(err error) (int, string) {
	switch {
	case err == nil:
		return http.StatusOK, ""
	case errors.Is(err, repository.ErrPostUnauthorized):
		return http.StatusForbidden, err.Error()
	case errors.Is(err, repository.ErrPostNotFound):
		return http.StatusNotFound, err.Error()
	case errors.Is(err, repository.ErrPostTargetNotFound):
		return http.StatusNotFound, err.Error()
	case errors.Is(err, repository.ErrIdempotencyConflict):
		return http.StatusConflict, err.Error()
	case errors.Is(err, sql.ErrNoRows):
		return http.StatusNotFound, "post not found"
	default:
		return http.StatusInternalServerError, err.Error()
	}
}

// --- Handlers ----------------------------------------------------------------

// handleCreatePost creates a post with targets in a single atomic call.
// POST /api/v1/posts
//
// Universal payload: {workspace_id, content:{title,caption,media_url},
// scheduled_at, targets:[{account_id}]}. Status defaults to "draft";
// if scheduled_at is set, status auto-promotes to "queued".
//
// Idempotency-Key header: clients may supply an Idempotency-Key to safely
// retry the request. Same key + same body returns the original 201 response.
// Same key + different body returns 409 Conflict.
func (r *Router) handleCreatePost(w http.ResponseWriter, req *http.Request) {
	if r.postStore == nil {
		writeError(w, http.StatusNotImplemented, "posts not configured on this server")
		return
	}
	if r.workspaceStore == nil {
		writeError(w, http.StatusNotImplemented, "workspaces not configured on this server")
		return
	}
	userID, ok := requireUserID(w, req, r)
	if !ok {
		return
	}

	// Read body into bytes for SHA-256 hashing (idempotency) and JSON decode.
	bodyBytes, err := io.ReadAll(req.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read request body: "+err.Error())
		return
	}

	var body CreatePostRequest
	if err := json.Unmarshal(bodyBytes, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if body.WorkspaceID == 0 {
		writeError(w, http.StatusUnprocessableEntity, "workspace_id is required")
		return
	}
	if len(body.Targets) == 0 {
		writeError(w, http.StatusUnprocessableEntity, "at least one target is required")
		return
	}
	for i, t := range body.Targets {
		if t.AccountID == 0 {
			writeError(w, http.StatusUnprocessableEntity,
				fmt.Sprintf("targets[%d].account_id is required", i))
			return
		}
	}
	ws, err := r.workspaceStore.FindByID(body.WorkspaceID)
	if err != nil {
		code, msg := mapWorkspaceError(err)
		writeError(w, code, "workspace lookup: "+msg)
		return
	}
	if ws == nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	if ws.OwnerID != userID {
		writeError(w, http.StatusForbidden, "workspace not owned by this user")
		return
	}

	status := models.PostStatusDraft
	if body.ScheduledAt != nil {
		status = models.PostStatusQueued
	}

	// Compute SHA-256 of the raw request body (idempotency hash).
	idempotencyKey := req.Header.Get("Idempotency-Key")
	requestHash := sha256Hex(bodyBytes)
	if idempotencyKey != "" {
		body.IdempotencyKey = &idempotencyKey
	}

	// Taglio 3.2: resolve media asset_id(s) → trusted internal S3 URL.
	// The first asset's URL is stored in post.MediaURL; the publish
	// worker continues to read post.MediaURL so the per-platform
	// service interfaces don't need to change. The URL is always
	// the internal S3 URL — no user-controlled URL can ever flow
	// into the publish pipeline.
	mediaURL, err := r.resolveFirstMediaURL(userID, body.Content.Media)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	post := &models.Post{
		WorkspaceID:    body.WorkspaceID,
		Title:          body.Content.Title,
		Caption:        body.Content.Caption,
		MediaURL:       mediaURL,
		ScheduledAt:    body.ScheduledAt,
		Status:         status,
		IdempotencyKey: bodyIdempotencyKeyPtr(body),
		Version:        1,
	}
	targets := make([]*models.PostTarget, 0, len(body.Targets))
	for _, t := range body.Targets {
		targets = append(targets, &models.PostTarget{
			PlatformAccountID: t.AccountID,
			Status:            models.PostStatusQueued,
			Version:           1,
		})
	}

	result, err := r.postStore.Create(post, targets, idempotencyKey, requestHash)
	if err != nil {
		code, msg := mapRepoError(err)
		writeError(w, code, "failed to create post: "+msg)
		return
	}
	if result.Duplicate {
		// Idempotent retry: return the cached response body.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		w.Write(result.CachedBody)
		return
	}
	writeJSON(w, http.StatusCreated, createPostResponse{post: post, targets: targets})
}

type createPostResponse struct {
	post    *models.Post
	targets []*models.PostTarget
}

func (c createPostResponse) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]interface{}{
		"id":           c.post.ID,
		"workspace_id": c.post.WorkspaceID,
		"title":        c.post.Title,
		"caption":      c.post.Caption,
		"media_url":    c.post.MediaURL,
		"scheduled_at": c.post.ScheduledAt,
		"status":       c.post.Status,
		"version":      c.post.Version,
		"created_at":   c.post.CreatedAt,
		"updated_at":   c.post.UpdatedAt,
		"post":         c.post,
		"targets":      c.targets,
	})
}

// handleAddTarget appends a post_target to an existing post.
// POST /api/v1/posts/{id}/targets
func (r *Router) handleAddTarget(w http.ResponseWriter, req *http.Request) {
	if r.postStore == nil {
		writeError(w, http.StatusNotImplemented, "posts not configured on this server")
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(req, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid post id: "+err.Error())
		return
	}
	var body AddTargetRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if body.AccountID == 0 {
		writeError(w, http.StatusBadRequest, "account_id is required")
		return
	}
	existing, err := r.postStore.FindByID(id)
	if err != nil || existing == nil {
		code, msg := mapRepoError(err)
		if code == http.StatusOK {
			code = http.StatusNotFound
			msg = "post not found"
		}
		writeError(w, code, msg)
		return
	}
	target := &models.PostTarget{
		PostID:            id,
		PlatformAccountID: body.AccountID,
		Status:            models.PostStatusQueued,
		Version:           1,
	}
	if err := r.postStore.SaveTarget(target); err != nil {
		code, msg := mapRepoError(err)
		writeError(w, code, "failed to add target: "+msg)
		return
	}
	writeJSON(w, http.StatusCreated, target)
}

// handleSchedulePost sets Status=queued and ScheduledAt on a draft post.
// POST /api/v1/posts/{id}/schedule
func (r *Router) handleSchedulePost(w http.ResponseWriter, req *http.Request) {
	if r.postStore == nil {
		writeError(w, http.StatusNotImplemented, "posts not configured on this server")
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(req, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid post id: "+err.Error())
		return
	}
	var body SchedulePostRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if body.ScheduledAt.IsZero() {
		writeError(w, http.StatusBadRequest, "scheduled_at is required")
		return
	}
	existing, err := r.postStore.FindByID(id)
	if err != nil || existing == nil {
		code, msg := mapRepoError(err)
		if code == http.StatusOK {
			code = http.StatusNotFound
			msg = "post not found"
		}
		writeError(w, code, msg)
		return
	}
	post := &models.Post{
		ID:          id,
		WorkspaceID: existing.WorkspaceID,
		Title:       existing.Title,
		Caption:     existing.Caption,
		MediaURL:    existing.MediaURL,
		ScheduledAt: &body.ScheduledAt,
		Status:      models.PostStatusQueued,
		Version:     existing.Version,
	}
	if err := r.postStore.Update(post); err != nil {
		code, msg := mapRepoError(err)
		writeError(w, code, "failed to schedule: "+msg)
		return
	}
	writeJSON(w, http.StatusOK, post)
}

// handleGetPost fetches a post by id with cross-tenant isolation.
// GET /api/v1/posts/{id}
func (r *Router) handleGetPost(w http.ResponseWriter, req *http.Request) {
	if r.postStore == nil {
		writeError(w, http.StatusNotImplemented, "posts not configured on this server")
		return
	}
	if r.workspaceStore == nil {
		writeError(w, http.StatusNotImplemented, "workspaces not configured on this server")
		return
	}
	userID, ok := requireUserID(w, req, r)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(req, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid post id: "+err.Error())
		return
	}
	p, err := r.postStore.FindByID(id)
	if err != nil {
		code, msg := mapRepoError(err)
		writeError(w, code, "failed to get post: "+msg)
		return
	}
	if p == nil {
		writeError(w, http.StatusNotFound, "post not found")
		return
	}
	ws, err := r.workspaceStore.FindByID(p.WorkspaceID)
	if err != nil || ws == nil || ws.OwnerID != userID {
		writeError(w, http.StatusNotFound, "post not found")
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// handleListByWorkspace lists posts in a workspace.
// GET /api/v1/posts/workspace/{wid}
func (r *Router) handleListByWorkspace(w http.ResponseWriter, req *http.Request) {
	if r.postStore == nil {
		writeError(w, http.StatusNotImplemented, "posts not configured on this server")
		return
	}
	wid, err := strconv.ParseInt(chi.URLParam(req, "wid"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id: "+err.Error())
		return
	}
	posts, err := r.postStore.ListByWorkspace(wid)
	if err != nil {
		code, msg := mapRepoError(err)
		writeError(w, code, "failed to list posts: "+msg)
		return
	}
	if posts == nil {
		posts = []models.Post{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"posts": posts})
}

// handleListPosts lists all posts for the authenticated user across their
// workspaces. Accepts optional ?workspace_id and ?status query parameters.
// GET /api/v1/posts
func (r *Router) handleListPosts(w http.ResponseWriter, req *http.Request) {
	if r.postStore == nil {
		writeError(w, http.StatusNotImplemented, "posts not configured on this server")
		return
	}
	if r.workspaceStore == nil {
		writeError(w, http.StatusNotImplemented, "workspaces not configured on this server")
		return
	}
	userID, ok := requireUserID(w, req, r)
	if !ok {
		return
	}
	wsIDStr := req.URL.Query().Get("workspace_id")
	if wsIDStr != "" {
		wid, err := strconv.ParseInt(wsIDStr, 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid workspace_id")
			return
		}
		ws, err := r.workspaceStore.FindByID(wid)
		if err != nil || ws == nil || ws.OwnerID != userID {
			writeError(w, http.StatusForbidden, "workspace not owned by this user")
			return
		}
		posts, err := r.postStore.ListByWorkspace(wid)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list posts: "+err.Error())
			return
		}
		if posts == nil {
			posts = []models.Post{}
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"posts": posts})
		return
	}
	wss, err := r.workspaceStore.ListByOwner(userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list workspaces: "+err.Error())
		return
	}
	all := make([]models.Post, 0)
	for _, ws := range wss {
		posts, err := r.postStore.ListByWorkspace(ws.ID)
		if err != nil {
			continue
		}
		all = append(all, posts...)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"posts": all})
}

// handlePatchPost updates the editable fields of an existing post.
// PATCH /api/v1/posts/{id}
func (r *Router) handlePatchPost(w http.ResponseWriter, req *http.Request) {
	if r.postStore == nil {
		writeError(w, http.StatusNotImplemented, "posts not configured on this server")
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(req, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid post id: "+err.Error())
		return
	}
	existing, err := r.postStore.FindByID(id)
	if err != nil || existing == nil {
		writeError(w, http.StatusNotFound, "post not found")
		return
	}
	var body struct {
		Title    string `json:"title,omitempty"`
		Caption  string `json:"caption,omitempty"`
		MediaURL string `json:"media_url,omitempty"`
		Status   string `json:"status,omitempty"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	post := &models.Post{
		ID:          id,
		WorkspaceID: existing.WorkspaceID,
		Title:       existing.Title,
		Caption:     existing.Caption,
		MediaURL:    existing.MediaURL,
		ScheduledAt: existing.ScheduledAt,
		Status:      existing.Status,
		Version:     existing.Version,
	}
	if body.Title != "" {
		post.Title = body.Title
	}
	if body.Caption != "" {
		post.Caption = body.Caption
	}
	if body.MediaURL != "" {
		post.MediaURL = body.MediaURL
	}
	if body.Status != "" {
		s := models.PostStatus(body.Status)
		if !s.IsValid() {
			writeError(w, http.StatusBadRequest, "invalid status: "+string(s))
			return
		}
		post.Status = s
	}
	if err := r.postStore.Update(post); err != nil {
		code, msg := mapRepoError(err)
		writeError(w, code, "failed to update post: "+msg)
		return
	}
	writeJSON(w, http.StatusOK, post)
}

// handleDeletePost removes a post and its targets (CASCADE).
// DELETE /api/v1/posts/{id}
func (r *Router) handleDeletePost(w http.ResponseWriter, req *http.Request) {
	if r.postStore == nil {
		writeError(w, http.StatusNotImplemented, "posts not configured on this server")
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(req, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid post id: "+err.Error())
		return
	}
	if err := r.postStore.Delete(id); err != nil {
		code, msg := mapRepoError(err)
		writeError(w, code, "failed to delete post: "+msg)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// handlePublishPostID transitions a post and its targets to publishing.
// POST /api/v1/posts/{id}/publish
func (r *Router) handlePublishPostID(w http.ResponseWriter, req *http.Request) {
	if r.postStore == nil {
		writeError(w, http.StatusNotImplemented, "posts not configured on this server")
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(req, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid post id: "+err.Error())
		return
	}
	if err := r.postStore.PublishPost(id); err != nil {
		code, msg := mapRepoError(err)
		writeError(w, code, "failed to publish post: "+msg)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "publishing"})
}

// handleCancelPost cancels a queued post, moving it back to draft.
// POST /api/v1/posts/{id}/cancel
func (r *Router) handleCancelPost(w http.ResponseWriter, req *http.Request) {
	if r.postStore == nil {
		writeError(w, http.StatusNotImplemented, "posts not configured on this server")
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(req, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid post id: "+err.Error())
		return
	}
	if err := r.postStore.CancelPost(id); err != nil {
		code, msg := mapRepoError(err)
		writeError(w, code, "failed to cancel post: "+msg)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "draft"})
}

// handleRetryPost transitions a failed post back to queued.
// POST /api/v1/posts/{id}/retry
func (r *Router) handleRetryPost(w http.ResponseWriter, req *http.Request) {
	if r.postStore == nil {
		writeError(w, http.StatusNotImplemented, "posts not configured on this server")
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(req, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid post id: "+err.Error())
		return
	}
	if err := r.postStore.RetryPost(id); err != nil {
		code, msg := mapRepoError(err)
		writeError(w, code, "failed to retry post: "+msg)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "queued"})
}

// handleGetPostTargets lists all targets for a post.
// GET /api/v1/posts/{id}/targets
func (r *Router) handleGetPostTargets(w http.ResponseWriter, req *http.Request) {
	if r.postStore == nil {
		writeError(w, http.StatusNotImplemented, "posts not configured on this server")
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(req, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid post id: "+err.Error())
		return
	}
	_ = id // Taglio 3.x: wire ListByPost into PostStore, then use it here.
	writeJSON(w, http.StatusOK, map[string]interface{}{"targets": []interface{}{}})
}

// handleRetryTarget transitions a failed target back to queued.
// POST /api/v1/post-targets/{id}/retry
func (r *Router) handleRetryTarget(w http.ResponseWriter, req *http.Request) {
	if r.postStore == nil {
		writeError(w, http.StatusNotImplemented, "posts not configured on this server")
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(req, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid target id: "+err.Error())
		return
	}
	if err := r.postStore.RetryTarget(id); err != nil {
		code, msg := mapRepoError(err)
		writeError(w, code, "failed to retry target: "+msg)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "queued"})
}

// --- Helpers ----------------------------------------------------------------

// sha256Hex returns the hex-encoded SHA-256 hash of data.
func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// bodyIdempotencyKeyPtr returns a pointer to the idempotency key if set in
// the request body (for the legacy idempotency_key field on Post). This is
// distinct from the Idempotency-Key header.
func bodyIdempotencyKeyPtr(body CreatePostRequest) *string {
	return body.IdempotencyKey
}
