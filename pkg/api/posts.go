package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// crypto/sha256 was previously imported here for the inline hash
// calculation in handleCreatePost; the responsibility moved to
// pkg/api/idempotency.go's idempotencyHash helper, so the import
// is no longer needed in this file. The "strings" import IS still
// needed (see uses of strings.TrimSpace and strings.Contains below)
// — only the crypto/sha256 reference is gone.

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
	PlatformAccountID int64 `json:"platform_account_id"`
}

// CreatePostRequest is the universal JSON body for POST /api/v1/posts.
//
// P1#4 — scheduled_at was split into ingest_after + publish_at. The
// canonical field name on the wire is publish_at (the user-facing
// "what time should this fire" cursor). scheduled_at remains on the
// wire as a one-minor-version alias so existing SPA / mobile / curl
// clients continue to work without a coordinated deploy. Server-side
// helper resolvePublishAt applies the alias precedence:
//
//	publish_at set (non-nil) AND scheduled_at set → publish_at wins
//	publish_at set (non-nil) AND scheduled_at nil → publish_at wins
//	publish_at nil          AND scheduled_at set → scheduled_at becomes publish_at
//	publish_at nil          AND scheduled_at nil → nil (legacy single-file flow)
//
// ingest_after is server-computed (DEFAULT NOW() at the SQL level);
// clients do NOT pass it. Future ingress-time controls (e.g.
// INGEST_LEAD_TIME_MINUTES env) live here, not in the wire shape.
type CreatePostRequest struct {
	WorkspaceID int64             `json:"workspace_id"`
	Content     CreatePostContent `json:"content"`
	// scheduled_at is the legacy alias. New callers should send
	// publish_at; both keys are accepted, publish_at wins if both
	// are set. The struct pair is preserved for one minor version;
	// P1#5 removes scheduled_at from the wire.
	ScheduledAt *time.Time `json:"scheduled_at,omitempty"`
	// publish_at is the canonical user-facing cursor.
	PublishAt *time.Time         `json:"publish_at,omitempty"`
	Status    models.PostStatus  `json:"status,omitempty"`
	Targets   []CreatePostTarget `json:"targets"`
}

// ResolvePublishAt returns the canonical publish_at cursor for the
// request, falling back to the scheduled_at alias when publish_at is
// not supplied. Centralised here so every handler applies identical
// precedence rules.
func (r CreatePostRequest) ResolvePublishAt() *time.Time {
	if r.PublishAt != nil {
		return r.PublishAt
	}
	return r.ScheduledAt
}

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

// publishAtJSON returns both scheduled_at and publish_at keys for the
// outgoing JSON so legacy SPA clients continue to render the calendar
// until they migrate to the new canonical key.
func publishAtJSON(publishAt *time.Time) map[string]interface{} {
	out := map[string]interface{}{
		"publish_at": publishAt,
	}
	if publishAt != nil {
		// Mirror as scheduled_at for back-compat.
		t := *publishAt
		out["scheduled_at"] = &t
	} else {
		out["scheduled_at"] = nil
	}
	return out
}

// AddTargetRequest is the JSON body for POST /posts/{id}/targets.
type AddTargetRequest struct {
	PlatformAccountID int64 `json:"platform_account_id"`
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
// Universal payload: {workspace_id, content:{title,caption,media},
// scheduled_at, targets:[{account_id}]}. Status defaults to "draft";
// if scheduled_at is set, status auto-promotes to "queued".
//
// Idempotency (level 1, migration 021): when the request carries an
// Idempotency-Key header, the handler consults the idempotency_records
// cache keyed on (workspace_id, idempotency_key):
//
//   - hit + same payload hash → replay: re-fetch the post and
//     return it with the original 201 status.
//   - hit + different payload hash OR different resource_type →
//     409 idempotency_key_conflict.
//   - miss → handler runs normally; on success, an idempotency
//     record is inserted for future replays.
//
// Order of operations (security-relevant):
//
//  1. Read body bytes + hash them (idempotency_read_body).
//  2. Unmarshal + validate body schema (workspace_id, status, targets).
//  3. Look up workspace by body.WorkspaceID + check ws.OwnerID == userID.
//     This MUST run before the cache replay: an attacker could
//     forge a request with another tenant's workspace_id in the body
//     and a guessed key — without the ownership check, the cache
//     would leak that tenant's resource. The ownership check makes
//     the (workspace_id, key) cache tuple safe to use.
//  4. Cache lookup keyed on (ws.ID, idemKey, hash).
//  5. Branch: replay / conflict / continue.
//  6. If continue, run the rest of the handler (mediaURL resolution,
//     PostRepository.Create, insert idempotency_record, write JSON).
//
// Taglio 3.2: the legacy `media_url` field on content is REMOVED.
// Clients pass `media: [{ asset_id }]` — the handler resolves each
// asset_id to a trusted internal S3 URL via the mediaStore +
// storageProvider. The first asset's URL is stored in post.MediaURL
// so the publish worker can continue to use the existing flow.
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

	// Read body bytes once + compute hash. Rewinds req.Body so any
	// downstream json.NewDecoder(req.Body) sees the same payload.
	bodyBytes, bodyErr := idempotencyReadBody(req)
	if bodyErr != nil {
		writeError(w, http.StatusBadRequest, "request body unreadable: "+bodyErr.Error())
		return
	}
	hash := idempotencyHash(bodyBytes)

	// Decode the body. We use json.Unmarshal on the bytes slice
	// (vs json.NewDecoder(req.Body)) because we already have the
	// bytes — Unmarshal doesn't read from req.Body so rewind
	// concerns are moot.
	var body CreatePostRequest
	if err := json.Unmarshal(bodyBytes, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	if body.WorkspaceID == 0 {
		writeError(w, http.StatusUnprocessableEntity, "workspace_id is required")
		return
	}
	if body.Status != "" && !body.Status.IsValid() {
		writeError(w, http.StatusBadRequest, "status must be one of: draft, queued")
		return
	}
	if len(body.Targets) == 0 {
		writeError(w, http.StatusUnprocessableEntity, "at least one target is required")
		return
	}
	for i, t := range body.Targets {
		if t.PlatformAccountID == 0 {
			writeError(w, http.StatusUnprocessableEntity,
				fmt.Sprintf("targets[%d].platform_account_id is required", i))
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

	// Workspace ownership verified. NOW do the idempotency lookup
	// keyed on (ws.ID, idemKey). Cross-tenant cache hit is
	// impossible because the (workspace, key) tuple is unique.
	idemKey := strings.TrimSpace(req.Header.Get("Idempotency-Key"))
	idemOutcome, idemRec, idemErr := idempotencyLookup(r, ws.ID, idemKey, hash, "post")
	if idemErr != nil {
		// 400 on "key too long" is a client-side contract
		// violation. Everything else (DB errors) is server-side.
		if strings.Contains(idemErr.Error(), "exceeds") {
			writeError(w, http.StatusBadRequest, idemErr.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "idempotency lookup: "+idemErr.Error())
		return
	}
	switch idemOutcome {
	case idempotencyConflict:
		writeError(w, http.StatusConflict, "idempotency_key_conflict")
		return
	case idempotencyReplay:
		if replayErr := replayIdempotentResource(r, w, idemRec, idemRec.ResponseStatus); replayErr != nil {
			writeError(w, http.StatusInternalServerError, "idempotency replay: "+replayErr.Error())
		}
		return
	case idempotencyContinue:
		// Fall through to the rest of the handler.
	}

	// P1#4 — resolve the canonical publish cursor via the alias
	// helper (publish_at wins; scheduled_at falls back).
	publishAt := body.ResolvePublishAt()
	status := models.PostStatusDraft
	if body.Status != "" {
		status = body.Status
	} else if publishAt != nil {
		status = models.PostStatusQueued
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
//
// Taglio 3b: the legacy `media_url` field is REMOVED from PATCH.
// Clients pass `media: [{ asset_id }]` — the handler resolves each
// asset_id to a trusted internal S3 URL. User-controlled URLs can
// no longer be injected via PATCH.
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
	ws, err := r.workspaceStore.FindByID(existing.WorkspaceID)
	if err != nil || ws == nil || ws.OwnerID != userID {
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
} // handleGetPostTargets lists all targets for a post.
// GET /api/v1/posts/{id}/targets
func (r *Router) handleGetPostTargets(w http.ResponseWriter, req *http.Request) {
	if r.postStore == nil {
		writeError(w, http.StatusNotImplemented, "posts not configured on this server")
		return
	}
	// Taglio 3.x: wire ListByPost into PostStore, then read chi.URLParam(req, "id").
	// Until then, return empty targets without consuming the post id param.
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
