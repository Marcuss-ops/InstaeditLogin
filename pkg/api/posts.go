package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// CreatePostTargetReq is one element of the targets array in
// POST /api/v1/posts. platform_account_id is the FK to platform_accounts
// (which itself references users via user_id).
type CreatePostTargetReq struct {
	PlatformAccountID int64 `json:"platform_account_id"`
}

// CreatePostReq is the body schema for POST /api/v1/posts.
//
// Validation per user spec:
//   - workspace_id > 0 (required)
//   - targets has at least 1 element (required)
//   - each target.platform_account_id > 0
//
// Workspace ownership is enforced by looking up the workspace and
// matching its OwnerID against the authenticated user_id before
// invoking the postStore.Create atomic transaction.
type CreatePostReq struct {
	WorkspaceID int64                 `json:"workspace_id"`
	Title       string                `json:"title,omitempty"`
	Caption     string                `json:"caption,omitempty"`
	MediaURL    string                `json:"media_url,omitempty"`
	ScheduledAt string                `json:"scheduled_at,omitempty"` // RFC 3339; parsed after validation
	Targets     []CreatePostTargetReq `json:"targets"`
}

// CreatePostResp is the response shape for POST /api/v1/posts. Compact:
// the client submitted the request and primarily needs the auto-generated
// IDs (post.id + each target.id) plus the status (assigned by the repo).
// scheduled_at is reflected back so the client can confirm what was stored.
type CreatePostResp struct {
	ID          int64                  `json:"id"`
	WorkspaceID int64                  `json:"workspace_id"`
	Status      models.PostStatus      `json:"status"`
	CreatedAt   string                 `json:"created_at"` // RFC 3339
	Targets     []CreatePostTargetResp `json:"targets"`
}

// CreatePostTargetResp echoes each created target's auto-assigned id.
type CreatePostTargetResp struct {
	ID                int64             `json:"id"`
	PlatformAccountID int64             `json:"platform_account_id"`
	Status            models.PostStatus `json:"status"`
}

// handleCreatePost (POST /api/v1/posts, protected) creates a post and
// its initial fan-out of PostTargets inside a single atomic transaction
// (delegated to PostStore.Create).
//
// Body schema (see CreatePostReq). Validation:
//   - workspace_id > 0           → 422
//   - targets has len >= 1        → 422
//   - each target.platform > 0    → 422 (with index reported)
//   - workspace owned by caller   → 403
//   - malformed JSON              → 400
//   - missing repo (501)            handled by per-store nil check
//   - RFC 3339 scheduled_at       → 400 if unparseable
func (r *Router) handleCreatePost(w http.ResponseWriter, req *http.Request) {
	if r.postStore == nil || r.workspaceStore == nil {
		writeError(w, http.StatusNotImplemented, "posts not configured on this server")
		return
	}
	userID, ok := requireUserID(w, req, r)
	if !ok {
		return
	}

	var body CreatePostReq
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	// User-spec validation (semantic → 422)
	if body.WorkspaceID <= 0 {
		writeError(w, http.StatusUnprocessableEntity, "workspace_id is required and must be > 0")
		return
	}
	if len(body.Targets) == 0 {
		writeError(w, http.StatusUnprocessableEntity, "at least 1 target is required")
		return
	}
	for i, t := range body.Targets {
		if t.PlatformAccountID <= 0 {
			writeError(w, http.StatusUnprocessableEntity,
				"targets["+strconv.Itoa(i)+"].platform_account_id is required and must be > 0")
			return
		}
	}

	// scheduled_at parsed AFTER validation but BEFORE the repo insert.
	// We accept a string from JSON so a malformed timestamp can be
	// reported as 400 rather than a generic decode failure.
	var scheduledAt *time.Time
	if body.ScheduledAt != "" {
		t, err := time.Parse(time.RFC3339, body.ScheduledAt)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid scheduled_at: "+err.Error())
			return
		}
		scheduledAt = &t
	}

	// Tenant isolation: only the workspace owner can create posts in it.
	ws, err := r.workspaceStore.FindByID(body.WorkspaceID)
	if err != nil {
		logAndError(w, "failed to lookup workspace", err, "workspace_id", body.WorkspaceID)
		return
	}
	if ws == nil {
		// 404 (not 403) to avoid leaking existence of other users' workspaces.
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	if ws.OwnerID != userID {
		slog.Warn("post create: workspace cross-owner access denied",
			"user_id", userID, "workspace_id", body.WorkspaceID, "owner_id", ws.OwnerID)
		writeError(w, http.StatusForbidden, "workspace not owned by current user")
		return
	}

	// Build the domain entities; the repo.Create call will atomically
	// insert the post + each target in one tx.
	post := &models.Post{
		WorkspaceID: body.WorkspaceID,
		Title:       body.Title,
		Caption:     body.Caption,
		MediaURL:    body.MediaURL,
		Status:      models.PostStatusDraft,
	}
	if scheduledAt != nil {
		post.ScheduledAt = scheduledAt
		post.Status = models.PostStatusScheduled
	}
	targets := make([]*models.PostTarget, len(body.Targets))
	for i, t := range body.Targets {
		targets[i] = &models.PostTarget{
			PlatformAccountID: t.PlatformAccountID,
			Status:            models.PostStatusScheduled,
		}
	}

	if err := r.postStore.Create(post, targets); err != nil {
		logAndError(w, "failed to create post", err,
			"user_id", userID, "workspace_id", body.WorkspaceID)
		return
	}

	// Echo back the assigned IDs (post.id, target.id) so the caller can
	// poll status / link to the future worker fan-out.
	targetResponses := make([]CreatePostTargetResp, len(targets))
	for i, t := range targets {
		targetResponses[i] = CreatePostTargetResp{
			ID:                t.ID,
			PlatformAccountID: t.PlatformAccountID,
			Status:            t.Status,
		}
	}
	writeJSON(w, http.StatusCreated, CreatePostResp{
		ID:          post.ID,
		WorkspaceID: post.WorkspaceID,
		Status:      post.Status,
		CreatedAt:   post.CreatedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"),
		Targets:     targetResponses,
	})
}

// handleListWorkspacePosts (GET /api/v1/workspaces/{id}/posts, protected)
// lists every post in a workspace the caller owns. 403 if not owned.
// Posts are NOT joined with their targets here — call ListByPost on the
// returned ID for fan-out inspection.
func (r *Router) handleListWorkspacePosts(w http.ResponseWriter, req *http.Request) {
	if r.postStore == nil || r.workspaceStore == nil {
		writeError(w, http.StatusNotImplemented, "posts not configured on this server")
		return
	}
	userID, ok := requireUserID(w, req, r)
	if !ok {
		return
	}

	id, ok := parsePathIDAsInt64(w, req, "id")
	if !ok {
		return
	}

	ws, err := r.workspaceStore.FindByID(id)
	if err != nil {
		logAndError(w, "failed to find workspace", err, "id", id)
		return
	}
	if ws == nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	if ws.OwnerID != userID {
		slog.Warn("posts list: workspace cross-owner access denied",
			"user_id", userID, "workspace_id", id, "owner_id", ws.OwnerID)
		writeError(w, http.StatusForbidden, "workspace not owned by current user")
		return
	}

	posts, err := r.postStore.ListByWorkspace(id)
	if err != nil {
		logAndError(w, "failed to list posts", err, "workspace_id", id)
		return
	}
	if posts == nil {
		posts = []models.Post{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"posts": posts})
}

// handleGetPost (GET /api/v1/posts/{id}, protected) fetches a single post.
// Cross-owner access is reported as 404 (not 403) by design — same
// security rationale as handleGetWorkspace.
func (r *Router) handleGetPost(w http.ResponseWriter, req *http.Request) {
	if r.postStore == nil || r.workspaceStore == nil {
		writeError(w, http.StatusNotImplemented, "posts not configured on this server")
		return
	}
	userID, ok := requireUserID(w, req, r)
	if !ok {
		return
	}

	id, ok := parsePathIDAsInt64(w, req, "id")
	if !ok {
		return
	}

	post, err := r.postStore.FindByID(id)
	if err != nil {
		logAndError(w, "failed to find post", err, "id", id)
		return
	}
	if post == nil {
		writeError(w, http.StatusNotFound, "post not found")
		return
	}
	// Tenant isolation: the post's workspace's owner must match caller.
	ws, err := r.workspaceStore.FindByID(post.WorkspaceID)
	if err != nil {
		logAndError(w, "failed to lookup workspace for post", err, "workspace_id", post.WorkspaceID)
		return
	}
	if ws == nil || ws.OwnerID != userID {
		slog.Warn("post get: cross-owner access denied",
			"user_id", userID, "post_id", id, "workspace_id", post.WorkspaceID)
		writeError(w, http.StatusNotFound, "post not found")
		return
	}
	writeJSON(w, http.StatusOK, post)
}
