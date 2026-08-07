package api

// Post mutation handlers.

import (
	"encoding/json"

	"net/http"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

func (r *Router) handlePatchPost(w http.ResponseWriter, req *http.Request) {
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
	id, err := postIDFromURL(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid post id: "+err.Error())
		return
	}
	existing, err := r.postStore.FindByID(id)
	if err != nil || existing == nil {
		writeError(w, http.StatusNotFound, "post not found")
		return
	}
	if !r.postWorkspaceOwnedByUser(existing, userID) {
		writeError(w, http.StatusNotFound, "post not found")
		return
	}
	var body struct {
		Title   string     `json:"title,omitempty"`
		Caption string     `json:"caption,omitempty"`
		Media   []MediaRef `json:"media,omitempty"`
		Status  string     `json:"status,omitempty"`
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
		// P1#4 — preserve the existing publish_at (was scheduled_at).
		PublishAt: existing.PublishAt,
		Status:    existing.Status,
		Version:   existing.Version,
	}
	if body.Title != "" {
		post.Title = body.Title
	}
	if body.Caption != "" {
		post.Caption = body.Caption
	}
	if len(body.Media) > 0 {
		mediaURL, err := r.resolveFirstMediaURL(userID, body.Media)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		post.MediaURL = mediaURL
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

func (r *Router) handleDeletePost(w http.ResponseWriter, req *http.Request) {
	if r.postStore == nil {
		writeError(w, http.StatusNotImplemented, "posts not configured on this server")
		return
	}
	id, err := postIDFromURL(req)
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
