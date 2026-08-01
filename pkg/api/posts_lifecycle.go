package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// SchedulePostRequest is the JSON body for POST /posts/{id}/schedule.
// P1#4 — publish_at is canonical; scheduled_at is the legacy alias
// kept for one-minor-version back-compat.
type SchedulePostRequest struct {
	// publish_at is canonical.
	PublishAt *time.Time `json:"publish_at,omitempty"`
	// scheduled_at is the legacy alias. If both are set, publish_at
	// wins (consistent with CreatePostRequest.ResolvePublishAt).
	ScheduledAt time.Time `json:"scheduled_at"`
}

// ResolvePublishAt applies the same precedence rules as
// CreatePostRequest. Both fields can't be nil because the handler
// returns 400 when both are nil; this helper just picks one when both
// are set.
func (r SchedulePostRequest) ResolvePublishAt() time.Time {
	if r.PublishAt != nil && !r.PublishAt.IsZero() {
		return *r.PublishAt
	}
	return r.ScheduledAt
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
	// P1#4 — canonical publish_at wins; scheduled_at falls back.
	publishAt := body.ResolvePublishAt()
	if publishAt.IsZero() {
		writeError(w, http.StatusBadRequest, "publish_at (or scheduled_at alias) is required")
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
		PublishAt:   &publishAt,
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
