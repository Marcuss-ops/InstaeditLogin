package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// CreatePostRequest is the JSON body for POST /posts/.
type CreatePostRequest struct {
	WorkspaceID int64               `json:"workspace_id"`
	Title       string              `json:"title,omitempty"`
	Caption     string              `json:"caption,omitempty"`
	MediaURL    string              `json:"media_url,omitempty"`
	ScheduledAt *time.Time          `json:"scheduled_at,omitempty"`
	Status      models.PostStatus   `json:"status,omitempty"`
	Targets     []PostTargetRequest `json:"targets,omitempty"`
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

// mapRepoError translates a repository sentinel into the corresponding
// HTTP status. Per the current API contract:
//
//   - ErrPostUnauthorized -> 403 (the operator has chosen to surface
//     "exists but not yours" for admin debugging; a future hardening
//     pass could switch this to 404 to prevent workspace-existence leaks
//     across tenants — see errors.go doc).
//   - ErrPostNotFound, ErrPostTargetNotFound, sql.ErrNoRows -> 404.
//   - Unknown errors fall through to 500.
//
// TestMapRepoError_AllSentinalMappings in posts_test.go LITERALLY locks
// these mappings down — any change here must update that test in lockstep.
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

// handleCreatePost creates a post (and any initial targets) within a
// workspace. Status defaults to "draft" if the caller doesn't specify
// one, OR to "scheduled" if a scheduled_at is provided. 501 if
// WithPostStore / WithWorkspaceStore were not wired.
//
// Validation order is intentional (most upstream failure first, so the
// client sees the most specific reason):
//  1. JSON decode (400 on malformed body)
//  2. workspace_id present (422 on missing)
//  3. status valid if explicitly set (400 on bogus — early exit so we
//     don't waste a workspace lookup)
//  4. workspace lookup (404 if not found)
//  5. workspace ownership (403 if caller is not the owner)
//  6. at least one target (422 on empty)
//  7. each target has a platform_account_id (422 on zero)
//  8. auto-promote status: scheduled_at != nil && status == "" → "scheduled"
//  9. default status: "draft" if still empty
//
// 10. Create (404 on ErrPostUnauthorized per the cross-tenant contract)
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
	var body CreatePostRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if body.WorkspaceID == 0 {
		writeError(w, http.StatusUnprocessableEntity, "workspace_id is required")
		return
	}
	if body.Status != "" && !body.Status.IsValid() {
		writeError(w, http.StatusBadRequest, "status must be one of: draft, scheduled, publishing, published, failed")
		return
	}
	ws, err := r.workspaceStore.FindByID(body.WorkspaceID)
	if err != nil {
		status, msg := mapWorkspaceError(err)
		writeError(w, status, "workspace lookup: "+msg)
		return
	}
	if ws == nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	if ws.OwnerID != userID {
		// 403 (not 404) per the current API contract — the operator has
		// chosen to surface "exists but not yours" for admin debugging.
		writeError(w, http.StatusForbidden, "workspace not owned by this user")
		return
	}
	if len(body.Targets) == 0 {
		writeError(w, http.StatusUnprocessableEntity, "at least one target is required")
		return
	}
	for i, t := range body.Targets {
		if t.PlatformAccountID == 0 {
			writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("targets[%d].platform_account_id is required", i))
			return
		}
	}
	if body.ScheduledAt != nil && body.Status == "" {
		body.Status = models.PostStatusScheduled
	}
	if body.Status == "" {
		body.Status = models.PostStatusDraft
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
	if err := r.postStore.Create(post, targets); err != nil {
		status, msg := mapRepoError(err)
		writeError(w, status, "failed to create post: "+msg)
		return
	}
	// Response shape: the post fields are at the top level (so a flat
	// decoder like {id, workspace_id, status, targets} works) AND nested
	// under a "post" key (so the older nested decoder {post, targets}
	// also works). JSON encoding handles the duplication cleanly.
	writeJSON(w, http.StatusCreated, createPostResponse{post: post, targets: targets})
}

// createPostResponse serialises a Post + its initial targets. The Post
// fields are promoted to the top level (id, workspace_id, status, etc.)
// AND duplicated under a "post" key. This dual shape lets two existing
// test decoders (one flat, one nested) share a single response payload
// without one of them going stale.
type createPostResponse struct {
	post    *models.Post
	targets []*models.PostTarget
}

// MarshalJSON renders the Post fields at the top level (matches the
// flat-decode contract in routes_test.go) and under a "post" key
// (matches the nested-decode contract in posts_test.go).
func (c createPostResponse) MarshalJSON() ([]byte, error) {
	flat := map[string]interface{}{
		"id":           c.post.ID,
		"workspace_id": c.post.WorkspaceID,
		"title":        c.post.Title,
		"caption":      c.post.Caption,
		"media_url":    c.post.MediaURL,
		"scheduled_at": c.post.ScheduledAt,
		"status":       c.post.Status,
		"created_at":   c.post.CreatedAt,
		"post":         c.post,
		"targets":      c.targets,
	}
	return json.Marshal(flat)
}

// handleAddTarget appends a post_target to an existing post. Pre-checks
// the post exists so ErrPostTargetNotFound is the only sentinel the handler
// can produce on success path. 501 if WithPostStore was not wired.
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
	if body.PlatformAccountID == 0 {
		writeError(w, http.StatusBadRequest, "platform_account_id is required")
		return
	}
	existing, err := r.postStore.FindByID(id)
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
	if err := r.postStore.Save(target); err != nil {
		status2, msg := mapRepoError(err)
		writeError(w, status2, "failed to add target: "+msg)
		return
	}
	writeJSON(w, http.StatusCreated, target)
}

// handleSchedulePost sets Status=scheduled and ScheduledAt on an existing
// post. Reads the existing post first to populate WorkspaceID (tenant-
// isolation predicate) so the repo's UPDATE statement matches the row.
// 501 if WithPostStore was not wired.
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
	if err := r.postStore.Update(post); err != nil {
		status, msg := mapRepoError(err)
		writeError(w, status, "failed to schedule: "+msg)
		return
	}
	writeJSON(w, http.StatusOK, post)
}

// handleGetPost fetches a post by id (without its targets) and enforces
// cross-tenant isolation: the caller's user_id must match the post's
// workspace's owner_id, otherwise we return 404 (existence-leak
// avoidance, matching the contract for handleGetWorkspace).
// 501 if WithPostStore / WithWorkspaceStore were not wired.
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
		status, msg := mapRepoError(err)
		writeError(w, status, "failed to get post: "+msg)
		return
	}
	if p == nil {
		writeError(w, http.StatusNotFound, "post not found")
		return
	}
	ws, err := r.workspaceStore.FindByID(p.WorkspaceID)
	if err != nil || ws == nil || ws.OwnerID != userID {
		// 404 (not 403) to prevent workspace-existence leaks across tenants.
		writeError(w, http.StatusNotFound, "post not found")
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// handleListByWorkspace lists posts in a given workspace. 501 if
// WithPostStore was not wired.
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
		status, msg := mapRepoError(err)
		writeError(w, status, "failed to list posts: "+msg)
		return
	}
	if posts == nil {
		posts = []models.Post{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"posts": posts})
}
