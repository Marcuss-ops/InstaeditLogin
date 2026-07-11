package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// CreatePostRequest is the JSON body for POST /posts/.
type CreatePostRequest struct {
	WorkspaceID int64                `json:"workspace_id"`
	Title       string               `json:"title,omitempty"`
	Caption     string               `json:"caption,omitempty"`
	MediaURL    string               `json:"media_url,omitempty"`
	ScheduledAt *time.Time           `json:"scheduled_at,omitempty"`
	Status      models.PostStatus    `json:"status,omitempty"`
	Targets     []PostTargetRequest  `json:"targets,omitempty"`
}

// PostTargetRequest is one row in CreatePostRequest.Targets.
type PostTargetRequest struct {
	PlatformAccountID int64             `json:"platform_account_id"`
	Status            models.PostStatus `json:"status,omitempty"`
}

// SchedulePostRequest is the JSON body for POST /posts/{id}/schedule.
type SchedulePostRequest struct {
	ScheduledAt time.Time `json:"scheduled_at"`
}

// AddTargetRequest is the JSON body for POST /posts/{id}/targets.
type AddTargetRequest struct {
	PlatformAccountID int64             `json:"platform_account_id"`
	Status            models.PostStatus `json:"status,omitempty"`
}

// mapRepoError translates a repository sentinel into the corresponding HTTP
// status. Per the current API contract: ErrPostUnauthorized -> 403 (the
// operator has chosen to surface "exists but not yours" for admin debugging;
// a future hardening pass could switch this to 404 to prevent
// workspace-existence leaks across tenants — see errors.go doc). All other
// sentinels and sql.ErrNoRows map to 404. Unknown errors fall through to 500.
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
	case errors.Is(err, sql.ErrNoRows):
		return http.StatusNotFound, "post not found"
	default:
		return http.StatusInternalServerError, err.Error()
	}
}

// handleCreatePost creates a post (and any initial targets) within a workspace.
// Status defaults to "draft" if the caller doesn't specify one.
func (r *Router) handleCreatePost(w http.ResponseWriter, req *http.Request) {
	userID := resolveUserID(req, 0, r.strictAuth)
	if userID == 0 {
		writeError(w, http.StatusUnauthorized, "user identity required")
		return
	}
	var body CreatePostRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if body.WorkspaceID == 0 {
		writeError(w, http.StatusBadRequest, "workspace_id is required")
		return
	}
	if body.Status == "" {
		body.Status = models.PostStatusDraft
	}
	if !body.Status.IsValid() {
		writeError(w, http.StatusBadRequest, "status must be one of: draft, scheduled, publishing, published, failed")
		return
	}
	post := &models.Post{
		WorkspaceID: body.WorkspaceID,
		Title:       body.Title,
		Caption:     body.Caption,
		MediaURL:    body.MediaURL,
		ScheduledAt: body.ScheduledAt,
		Status:      body.Status,
	}
	targets := make([]*models.PostTarget, 0, len(body.Targets))
	for _, t := range body.Targets {
		status := t.Status
		if status == "" {
			status = models.PostStatusScheduled
		}
		targets = append(targets, &models.PostTarget{PlatformAccountID: t.PlatformAccountID, Status: status})
	}
	if err := r.postRepo.Create(post, targets); err != nil {
		status, msg := mapRepoError(err)
		writeError(w, status, "failed to create post: "+msg)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]interface{}{"post": post, "targets": targets})
}

// handleAddTarget appends a post_target to an existing post.
// Pre-checks the post exists so ErrPostTargetNotFound is the only sentinel
// the handler can produce on success path.
func (r *Router) handleAddTarget(w http.ResponseWriter, req *http.Request) {
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
	if body.PlatformAccountID == 0 {
		writeError(w, http.StatusBadRequest, "platform_account_id is required")
		return
	}
	existing, err := r.postRepo.FindByID(id)
	if err != nil || existing == nil {
		status, msg := mapRepoError(err)
		if status == http.StatusOK {
			status = http.StatusNotFound
			msg = "post not found"
		}
		writeError(w, status, msg)
		return
	}
	status := body.Status
	if status == "" {
		status = models.PostStatusScheduled
	}
	target := &models.PostTarget{PostID: id, PlatformAccountID: body.PlatformAccountID, Status: status}
	if err := r.postRepo.Save(target); err != nil {
		status2, msg := mapRepoError(err)
		writeError(w, status2, "failed to add target: "+msg)
		return
	}
	writeJSON(w, http.StatusCreated, target)
}

// handleSchedulePost sets Status=scheduled and ScheduledAt on an existing post.
// Reads the existing post first to populate WorkspaceID (tenant-isolation
// predicate) so the repo's UPDATE statement matches the row.
func (r *Router) handleSchedulePost(w http.ResponseWriter, req *http.Request) {
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
	existing, err := r.postRepo.FindByID(id)
	if err != nil || existing == nil {
		status, msg := mapRepoError(err)
		if status == http.StatusOK {
			status = http.StatusNotFound
			msg = "post not found"
		}
		writeError(w, status, msg)
		return
	}
	post := &models.Post{
		ID:          id,
		WorkspaceID: existing.WorkspaceID,
		Title:       existing.Title,
		Caption:     existing.Caption,
		MediaURL:    existing.MediaURL,
		ScheduledAt: &body.ScheduledAt,
		Status:      models.PostStatusScheduled,
	}
	if err := r.postRepo.Update(post); err != nil {
		status, msg := mapRepoError(err)
		writeError(w, status, "failed to schedule: "+msg)
		return
	}
	writeJSON(w, http.StatusOK, post)
}

// handleGetPost fetches a post by id (without its targets).
func (r *Router) handleGetPost(w http.ResponseWriter, req *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(req, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid post id: "+err.Error())
		return
	}
	p, err := r.postRepo.FindByID(id)
	if err != nil {
		status, msg := mapRepoError(err)
		writeError(w, status, "failed to get post: "+msg)
		return
	}
	if p == nil {
		writeError(w, http.StatusNotFound, "post not found")
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// handleListByWorkspace lists posts in a given workspace.
func (r *Router) handleListByWorkspace(w http.ResponseWriter, req *http.Request) {
	wid, err := strconv.ParseInt(chi.URLParam(req, "wid"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id: "+err.Error())
		return
	}
	posts, err := r.postRepo.ListByWorkspace(wid)
	if err != nil {
		status, msg := mapRepoError(err)
		writeError(w, status, "failed to list posts: "+msg)
		return
	}
	if posts == nil {
		posts = []models.Post{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"posts": posts})
}
