package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// handleCreatePost's body is decomposed into phase helpers below
// (pattern: handleCallback / handleUploadsBatchByFolder / group
// videos). Each phase returns done=true when it already wrote a
// response (error OR terminal branch); the handler chains them and
// only reaches createPostPersist when every gate passed.

// decodeCreatePostRequest is phase 1 of handleCreatePost: read the
// body bytes once + compute the idempotency hash, then decode + run
// the schema validation (workspace_id, status, targets). done=true
// means a 4xx was already written.
func decodeCreatePostRequest(w http.ResponseWriter, req *http.Request) (CreatePostRequest, []byte, bool) {
	// Read body bytes once + compute hash. Rewinds req.Body so any
	// downstream json.NewDecoder(req.Body) sees the same payload.
	bodyBytes, bodyErr := idempotencyReadBody(w, req)
	if bodyErr != nil {
		writeRequestBodyError(w, bodyErr)
		return CreatePostRequest{}, nil, true
	}

	// Decode the body. We use json.Unmarshal on the bytes slice
	// (vs json.NewDecoder(req.Body)) because we already have the
	// bytes — Unmarshal doesn't read from req.Body so rewind
	// concerns are moot.
	var body CreatePostRequest
	if err := json.Unmarshal(bodyBytes, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return CreatePostRequest{}, nil, true
	}
	if body.WorkspaceID == 0 {
		writeError(w, http.StatusUnprocessableEntity, "workspace_id is required")
		return CreatePostRequest{}, nil, true
	}
	if body.Status != "" && !body.Status.IsValid() {
		writeError(w, http.StatusBadRequest, "status must be one of: draft, queued")
		return CreatePostRequest{}, nil, true
	}
	if len(body.Targets) == 0 {
		writeError(w, http.StatusUnprocessableEntity, "at least one target is required")
		return CreatePostRequest{}, nil, true
	}
	for i, t := range body.Targets {
		if t.PlatformAccountID == 0 {
			writeError(w, http.StatusUnprocessableEntity,
				fmt.Sprintf("targets[%d].platform_account_id is required", i))
			return CreatePostRequest{}, nil, true
		}
	}
	return body, bodyBytes, false
}

// lookupCreatePostWorkspace is phase 2 of handleCreatePost: resolve
// the workspace by body.WorkspaceID + check ws.OwnerID == userID.
// This MUST run before the idempotency cache replay: an attacker
// could forge a request with another tenant's workspace_id in the
// body and a guessed key — without the ownership check, the cache
// would leak that tenant's resource. The ownership check makes the
// (workspace_id, key) cache tuple safe to use.
func (r *Router) lookupCreatePostWorkspace(w http.ResponseWriter, req *http.Request, userID, workspaceID int64) (*models.Workspace, bool) {
	ws, err := r.workspaceStore.FindByID(workspaceID)
	if err != nil {
		code, msg := mapWorkspaceError(err)
		writeError(w, code, "workspace lookup: "+msg)
		return nil, true
	}
	if ws == nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return nil, true
	}
	if ws.OwnerID != userID {
		writeError(w, http.StatusForbidden, "workspace not owned by this user")
		return nil, true
	}
	return ws, false
}

// applyCreatePostIdempotency is phase 3 of handleCreatePost: the
// idempotency lookup keyed on (ws.ID, idemKey) + the branch
// (replay / conflict / continue). done=true means the replay or the
// 409 was already written.
func (r *Router) applyCreatePostIdempotency(w http.ResponseWriter, req *http.Request, wsID int64, hash []byte) (idemKey string, done bool) {
	// Workspace ownership verified (phase 2). NOW do the idempotency
	// lookup keyed on (ws.ID, idemKey). Cross-tenant cache hit is
	// impossible because the (workspace, key) tuple is unique.
	idemKey = strings.TrimSpace(req.Header.Get("Idempotency-Key"))
	idemOutcome, idemRec, idemErr := idempotencyLookup(r, wsID, idemKey, hash, "post")
	if idemErr != nil {
		// 400 on "key too long" is a client-side contract
		// violation. Everything else (DB errors) is server-side.
		if strings.Contains(idemErr.Error(), "exceeds") {
			writeError(w, http.StatusBadRequest, idemErr.Error())
			return idemKey, true
		}
		writeError(w, http.StatusInternalServerError, "idempotency lookup: "+idemErr.Error())
		return idemKey, true
	}
	switch idemOutcome {
	case idempotencyConflict:
		writeError(w, http.StatusConflict, "idempotency_key_conflict")
		return idemKey, true
	case idempotencyReplay:
		if replayErr := replayIdempotentResource(r, w, idemRec, idemRec.ResponseStatus); replayErr != nil {
			writeError(w, http.StatusInternalServerError, "idempotency replay: "+replayErr.Error())
		}
		return idemKey, true
	case idempotencyContinue:
		// Fall through to the rest of the handler.
	}
	return idemKey, false
}

// resolveCreatePostSchedule is phase 4 of handleCreatePost: resolve
// the canonical publish cursor (publish_at wins; scheduled_at falls
// back), enforce the PUBLISH_HORIZON_DAYS cap, and promote the
// status (explicit status wins; publish_at non-nil → queued;
// otherwise draft).
//
// The 5-second minimum-future floor and the past-date rejection are
// NOT applied here — handleCreatePost's callers include the
// "publish immediately" flow (publishAt == nil) which must pass; the
// producer-side validation in handleRescheduleUpload owns the floors
// for the reschedule path.
func (r *Router) resolveCreatePostSchedule(w http.ResponseWriter, req *http.Request, body CreatePostRequest) (*time.Time, models.PostStatus, bool) {
	// P1#4 — resolve the canonical publish cursor via the alias
	// helper (publish_at wins; scheduled_at falls back).
	publishAt := body.ResolvePublishAt()
	// Blocco #3 P0 — horizon enforcement on POST /api/v1/posts.
	// The producer-side heuristic in drive_batch_v2_handlers already
	// caps the worst-case projected batch horizon; this single-post
	// endpoint applies the SAME env-driven cap from the user-facing
	// schedule view so a manual "/posts now + calendar" planning
	// can never park a post past PUBLISH_HORIZON_DAYS.
	if publishAt != nil {
		maxHorizon := time.Now().Add(time.Duration(r.publishHorizonDays()) * 24 * time.Hour)
		if publishAt.After(maxHorizon) {
			writeError(w, http.StatusUnprocessableEntity,
				fmt.Sprintf("publish_at must be within %d days from now (PUBLISH_HORIZON_DAYS)", r.publishHorizonDays()),
			)
			return nil, "", true
		}
	}
	status := models.PostStatusDraft
	if body.Status != "" {
		status = body.Status
	} else if publishAt != nil {
		status = models.PostStatusQueued
	}
	return publishAt, status, false
}

// createPostPersist is phase 5 (terminal) of handleCreatePost:
// resolve the media asset_id(s) → trusted internal S3 URL, build the
// post + targets, persist via postStore.Create, insert the
// idempotency record, and write the 201 response.
func (r *Router) createPostPersist(
	w http.ResponseWriter,
	userID int64,
	ws *models.Workspace,
	body CreatePostRequest,
	publishAt *time.Time,
	status models.PostStatus,
	idemKey string,
	hash []byte,
) {
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
		WorkspaceID: body.WorkspaceID,
		Title:       body.Content.Title,
		Caption:     body.Content.Caption,
		MediaURL:    mediaURL,
		// P1#4 — ingest_after is server-side DEFAULT NOW() at SQL
		// level; we leave zero-value here so the SQL DEFAULT fires.
		// publish_at comes from the body's canonical-or-alias cursor.
		PublishAt: publishAt,
		Status:    status,
	}
	targets := make([]*models.PostTarget, 0, len(body.Targets))
	for _, t := range body.Targets {
		targets = append(targets, &models.PostTarget{
			PlatformAccountID: t.PlatformAccountID,
			Status:            models.PostStatusQueued,
		})
	}

	if err := r.postStore.Create(post, targets); err != nil {
		code, msg := mapRepoError(err)
		writeError(w, code, "failed to create post: "+msg)
		return
	}
	// Idempotency-Key post-create write (level 1, migration 021).
	// Only fires when the request carried the header AND we fell
	// through to the handler (i.e. no cached hit). Best-effort:
	// the cache is operator UX, not part of the API contract.
	insertIdempotentRecord(r, ws.ID, idemKey, "post", post.ID, hash, http.StatusCreated)
	writeJSON(w, http.StatusCreated, createPostResponse{post: post, targets: targets})
}

type createPostResponse struct {
	post    *models.Post
	targets []*models.PostTarget
}

func (c createPostResponse) MarshalJSON() ([]byte, error) {
	// P1#4 — emit BOTH publish_at (canonical) AND scheduled_at
	// (legacy alias) on the wire so legacy SPA clients continue to
	// render the calendar until they migrate. The post pointer also
	// serialises since the marshaler is on the wrapper struct.
	base := publishAtJSON(c.post.PublishAt)
	base["id"] = c.post.ID
	base["workspace_id"] = c.post.WorkspaceID
	base["title"] = c.post.Title
	base["caption"] = c.post.Caption
	base["media_url"] = c.post.MediaURL
	base["status"] = c.post.Status
	base["version"] = c.post.Version
	base["created_at"] = c.post.CreatedAt
	base["updated_at"] = c.post.UpdatedAt
	base["post"] = c.post
	base["targets"] = c.targets
	return json.Marshal(base)
}
