package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// AddTargetRequest is the JSON body for POST /posts/{id}/targets.
type AddTargetRequest struct {
	PlatformAccountID int64 `json:"platform_account_id"`
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
	if body.PlatformAccountID == 0 {
		writeError(w, http.StatusBadRequest, "platform_account_id is required")
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
		PlatformAccountID: body.PlatformAccountID,
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

// handleGetPostTargets lists all targets for a post.
// GET /api/v1/posts/{id}/targets
//
// Taglio 5.1 step 2 — wired postStore.ListByPost() so the historical
// empty-array stub now returns the actual fan-out. Reads the post id
// from chi.URLParam; returns 400 on a non-integer id; returns
// {"targets": []} (NOT null) when the post has no targets so the
// JSON shape is stable for client-side iteration.
//
// Workspace isolation: the historical stub returned the empty array
// regardless of caller identity — masking a potential IDOR leak
// rather than fixing it. Step 2 fixed the leak by mirroring the same
// Target → Post → Workspace ownership guard handleGetPost uses:
// 404 on cross-owner (NOT 403, NOT 410) so the response leaks no
// information about workspace existence. TestHandleGetPostTargets_CrossOwner_404
// pins this contract.
func (r *Router) handleGetPostTargets(w http.ResponseWriter, req *http.Request) {
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
	// Workspace-isolation guard (Taglio 5.1 step 2 ship-blocker
	// fix): resolve the parent post → workspace, reject on
	// cross-owner. Same profile as handleGetPost / handleGetSinglePostTarget.
	post, postErr := r.postStore.FindByID(id)
	if postErr != nil || post == nil {
		writeError(w, http.StatusNotFound, "post not found")
		return
	}
	ws, wsErr := r.workspaceStore.FindByID(post.WorkspaceID)
	if wsErr != nil || ws == nil || ws.OwnerID != userID {
		writeError(w, http.StatusNotFound, "post not found")
		return
	}
	targets, err := r.postStore.ListByPost(id)
	if err != nil {
		code, msg := mapRepoError(err)
		writeError(w, code, "failed to list post_targets: "+msg)
		return
	}
	if targets == nil {
		targets = []models.PostTarget{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"targets": targets})
}

// postTargetDetailResponse is the JSON envelope for GET /api/v1/post-targets/{id}.
// It inlines every models.PostTarget field plus the post-side
// privacy context the polling frontend needs (privacy /
// privacy_status / made_for_kids).
//
// Explicit field selection (NOT embedding the struct) so the
// response field names are de-coupled from the model's JSON tags:
// next_retry_at is more semantically clear than next_attempt_at for
// "when will the platform retry" — the test harness asserts on
// next_retry_at per task spec. attempt_count and error_message
// remain stable under the model's JSON tags so the polling consumer
// doesn't have to learn a renamed schema just to render errors.
//
// Field grouping is documented inline. Adding a new column on
// post_targets MUST also appear here AND in qSelectTargetByID's
// Scan arity in post_repo.go::FindTargetByID.
type postTargetDetailResponse struct {
	target       *models.PostTarget
	privacyLevel string
	madeForKids  bool
}

func (r postTargetDetailResponse) MarshalJSON() ([]byte, error) {
	// Call site (handleGetSinglePostTarget) 404s before constructing
	// a response with a nil target, so dereference is safe. If a
	// future refactor regresses that guard, the panic on t.* fields
	// below surfaces the bug loudly through the recovery middleware
	// — preferable to silently emitting "null".
	t := r.target
	return json.Marshal(map[string]any{
		// target identity
		"id":                  t.ID,
		"post_id":             t.PostID,
		"platform_account_id": t.PlatformAccountID,
		// lifecycle
		"status":        t.Status,
		"attempt_count": t.AttemptCount,
		"next_retry_at": t.NextAttemptAt, // named for clarity vs Go field NextAttemptAt
		// terminal-state diagnostics
		"error_message": t.ErrorMessage,
		"published_at":  t.PublishedAt,
		"completed_at":  t.CompletedAt,
		// platform-facing ids
		"platform_post_id":         t.PlatformPostID,
		"remote_post_id":           t.RemotePostID,
		"remote_post_url":          t.RemotePostURL,
		"provider_state":           t.ProviderState,
		"container_id":             t.ContainerID,
		"provider_idempotency_key": t.ProviderIdempotencyKey,
		"last_error_code":          t.LastErrorCode,
		// audit
		"version":    t.Version,
		"created_at": t.CreatedAt,
		"updated_at": t.UpdatedAt,
		// post-side privacy context (Taglio 5.1 step 2)
		// Both fields mirror post.PrivacyLevel until/unless we
		// add a per-target override column. made_for_kids is
		// parsed best-effort from post.metadata.made_for_kids.
		"privacy":        r.privacyLevel,
		"privacy_status": r.privacyLevel,
		"made_for_kids":  r.madeForKids,
	})
}

// handleGetSinglePostTarget returns a single post_target with status
// + retry metadata + post-side privacy context.
// GET /api/v1/post-targets/{id}
//
// Taglio 5.1 step 2 polling endpoint. The accept flow is:
//
//	JSON 200 {"id":..,"status":..,"attempt_count":..,"next_retry_at":..,
//	          "error_message":..,"privacy":..,"privacy_status":..,
//	          "made_for_kids":.., ...}     ← postTargetDetailResponse
//	404 ↪ target id doesn't exist OR cross-tenant probe (workspace
//	     not owned by the caller). Same shape as a missing row so the
//	     response leaks no information about workspace existence.
//	400 ↪ invalid id (non-integer).
//	401 ↪ missing identity.
//	500 ↪ unexpected repo error.
//
// Workspace isolation: after FindTargetByID, the handler resolves
// Target → Post → Workspace and rejects when ws.OwnerID != userID.
// This closes the OWASP IDOR surface even though the SQL read itself
// is workspace-agnostic (intentional per FindTargetByID's contract).
func (r *Router) handleGetSinglePostTarget(w http.ResponseWriter, req *http.Request) {
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
		writeError(w, http.StatusBadRequest, "invalid target id: "+err.Error())
		return
	}
	target, err := r.postStore.FindTargetByID(id)
	if err != nil {
		code, msg := mapRepoError(err)
		writeError(w, code, "failed to get post_target: "+msg)
		return
	}
	if target == nil {
		// Don't leak existence via ErrPostTargetNotFound vs row-absent split —
		// both shapes collapse to 404 post_target not found so a probing
		// adversary cannot distinguish "id is invalid" from "id is valid
		// but you don't own the workspace".
		writeError(w, http.StatusNotFound, "post_target not found")
		return
	}
	// Load the parent post for workspace-isolation context AND
	// privacy fields. Both round-trips intentional: the SQL layer
	// (FindTargetByID) is workspace-agnostic on purpose so we keep
	// the IDOR check in Go where it's auditable.
	post, postErr := r.postStore.FindByID(target.PostID)
	if postErr != nil || post == nil {
		writeError(w, http.StatusNotFound, "post_target not found")
		return
	}
	ws, wsErr := r.workspaceStore.FindByID(post.WorkspaceID)
	if wsErr != nil || ws == nil || ws.OwnerID != userID {
		writeError(w, http.StatusNotFound, "post_target not found")
		return
	}
	// Parse made_for_kids from Post.Metadata (best-effort: malformed
	// JSON → false). The field is per-post (not per-target) because
	// the publish pipeline stamps it once at create-time via
	// POST /api/v1/posts body's settings.youtube.made_for_kids.
	madeForKids := false
	if len(post.Metadata) > 0 {
		var meta struct {
			MadeForKids bool `json:"made_for_kids"`
		}
		if jsonErr := json.Unmarshal(post.Metadata, &meta); jsonErr == nil {
			madeForKids = meta.MadeForKids
		}
	}
	writeJSON(w, http.StatusOK, postTargetDetailResponse{
		target:       target,
		privacyLevel: post.PrivacyLevel,
		madeForKids:  madeForKids,
	})
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
