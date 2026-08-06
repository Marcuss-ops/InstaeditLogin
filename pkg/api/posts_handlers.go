package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
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

	// Phase 1: read body bytes + hash + decode + schema validation
	// (workspace_id, status, targets). The body bytes + hash are
	// reused by the idempotency gate (phase 3).
	body, bodyBytes, done := decodeCreatePostRequest(w, req)
	if done {
		return
	}
	hash := idempotencyHash(bodyBytes)

	// Phase 2: workspace lookup + ownership check. Runs BEFORE the
	// cache replay so a forged cross-tenant (workspace_id, key)
	// tuple cannot leak another tenant's resource.
	ws, done := r.lookupCreatePostWorkspace(w, req, userID, body.WorkspaceID)
	if done {
		return
	}

	// Phase 3: idempotency gate keyed on (ws.ID, idemKey) — replay /
	// conflict / continue.
	idemKey, done := r.applyCreatePostIdempotency(w, req, ws.ID, hash)
	if done {
		return
	}

	// Phase 4: canonical publish cursor (publish_at / scheduled_at
	// alias), PUBLISH_HORIZON_DAYS cap, status promotion.
	publishAt, status, done := r.resolveCreatePostSchedule(w, req, body)
	if done {
		return
	}

	// Phase 5 (terminal): media resolution, persist, idempotency
	// record, 201 response.
	r.createPostPersist(w, userID, ws, body, publishAt, status, idemKey, hash)
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
	if err != nil || wid <= 0 {
		writeError(w, http.StatusBadRequest, "invalid workspace id: "+err.Error())
		return
	}
	if req.URL.Query().Get("cursor") == "" && req.URL.Query().Get("limit") == "" {
		posts, listErr := r.postStore.ListByWorkspace(wid)
		if listErr != nil {
			code, msg := mapRepoError(listErr)
			writeError(w, code, "failed to list posts: "+msg)
			return
		}
		if posts == nil {
			posts = []models.Post{}
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"posts": posts})
		return
	}
	limit, rawCursor, err := parseListPageWithBounds(req.URL.Query(), 50, 100)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	cursorContext := "workspace_id=" + strconv.FormatInt(wid, 10)
	cursorTime, cursorID, cursorNull, err := decodeListCursorDetails(rawCursor, "posts", cursorContext)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if cursorNull && rawCursor != "" {
		writeError(w, http.StatusBadRequest, "invalid list cursor: post cursor timestamp is required")
		return
	}
	var posts []models.Post
	hasMore := false
	if paged, ok := r.postStore.(interface {
		ListByWorkspacePage(int64, *time.Time, int64, int) ([]models.Post, bool, error)
	}); ok {
		var afterTime *time.Time
		var afterID int64
		if rawCursor != "" {
			afterTime = &cursorTime
			afterID, err = strconv.ParseInt(cursorID, 10, 64)
			if err != nil || afterID <= 0 {
				writeError(w, http.StatusBadRequest, "invalid list cursor")
				return
			}
		}
		posts, hasMore, err = paged.ListByWorkspacePage(wid, afterTime, afterID, limit)
	} else {
		if rawCursor != "" {
			writeError(w, http.StatusNotImplemented, "cursor pagination is not supported by this post store")
			return
		}
		posts, err = r.postStore.ListByWorkspace(wid)
		if len(posts) > limit {
			hasMore = true
			posts = posts[:limit]
		}
	}
	if err != nil {
		code, msg := mapRepoError(err)
		writeError(w, code, "failed to list posts: "+msg)
		return
	}
	if posts == nil {
		posts = []models.Post{}
	}
	writeJSON(w, http.StatusOK, postListResponse(posts, hasMore, cursorContext))
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
		if req.URL.Query().Get("cursor") == "" && req.URL.Query().Get("limit") == "" {
			posts, listErr := r.postStore.ListByWorkspace(wid)
			if listErr != nil {
				writeError(w, http.StatusInternalServerError, "failed to list posts: "+listErr.Error())
				return
			}
			if posts == nil {
				posts = []models.Post{}
			}
			writeJSON(w, http.StatusOK, map[string]interface{}{"posts": posts})
			return
		}
		limit, rawCursor, pageErr := parseListPageWithBounds(req.URL.Query(), 50, 100)
		if pageErr != nil {
			writeError(w, http.StatusBadRequest, pageErr.Error())
			return
		}
		cursorContext := listCursorFilterContext(req.URL.Query(), "workspace_id")
		cursorTime, cursorID, cursorNull, pageErr := decodeListCursorDetails(rawCursor, "posts", cursorContext)
		if pageErr != nil {
			writeError(w, http.StatusBadRequest, pageErr.Error())
			return
		}
		if cursorNull && rawCursor != "" {
			writeError(w, http.StatusBadRequest, "invalid list cursor: post cursor timestamp is required")
			return
		}
		var posts []models.Post
		hasMore := false
		if paged, ok := r.postStore.(interface {
			ListByWorkspacePage(int64, *time.Time, int64, int) ([]models.Post, bool, error)
		}); ok {
			var afterTime *time.Time
			var afterID int64
			if rawCursor != "" {
				afterTime = &cursorTime
				afterID, pageErr = strconv.ParseInt(cursorID, 10, 64)
				if pageErr != nil || afterID <= 0 {
					writeError(w, http.StatusBadRequest, "invalid list cursor")
					return
				}
			}
			posts, hasMore, pageErr = paged.ListByWorkspacePage(wid, afterTime, afterID, limit)
		} else {
			if rawCursor != "" {
				writeError(w, http.StatusNotImplemented, "cursor pagination is not supported by this post store")
				return
			}
			posts, pageErr = r.postStore.ListByWorkspace(wid)
			if len(posts) > limit {
				hasMore = true
				posts = posts[:limit]
			}
		}
		if pageErr != nil {
			writeError(w, http.StatusInternalServerError, "failed to list posts: "+pageErr.Error())
			return
		}
		if posts == nil {
			posts = []models.Post{}
		}
		writeJSON(w, http.StatusOK, postListResponse(posts, hasMore, cursorContext))
		return
	}
	wss, err := r.workspaceStore.ListByOwner(userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list workspaces: "+err.Error())
		return
	}
	workspaceIDs := make([]int64, 0, len(wss))
	for _, ws := range wss {
		if ws.ID > 0 {
			workspaceIDs = append(workspaceIDs, ws.ID)
		}
	}
	sort.Slice(workspaceIDs, func(i, j int) bool { return workspaceIDs[i] < workspaceIDs[j] })
	if req.URL.Query().Get("cursor") == "" && req.URL.Query().Get("limit") == "" {
		all := make([]models.Post, 0)
		for _, ws := range wss {
			posts, listErr := r.postStore.ListByWorkspace(ws.ID)
			if listErr == nil {
				all = append(all, posts...)
			}
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"posts": all})
		return
	}
	limit, rawCursor, pageErr := parseListPageWithBounds(req.URL.Query(), 50, 100)
	if pageErr != nil {
		writeError(w, http.StatusBadRequest, pageErr.Error())
		return
	}
	cursorContext := "workspaces=" + joinInt64List(workspaceIDs)
	cursorTime, cursorID, cursorNull, pageErr := decodeListCursorDetails(rawCursor, "posts", cursorContext)
	if pageErr != nil {
		writeError(w, http.StatusBadRequest, pageErr.Error())
		return
	}
	if cursorNull && rawCursor != "" {
		writeError(w, http.StatusBadRequest, "invalid list cursor: post cursor timestamp is required")
		return
	}
	var all []models.Post
	hasMore := false
	if paged, ok := r.postStore.(interface {
		ListByWorkspacesPage([]int64, *time.Time, int64, int) ([]models.Post, bool, error)
	}); ok {
		var afterTime *time.Time
		var afterID int64
		if rawCursor != "" {
			afterTime = &cursorTime
			afterID, pageErr = strconv.ParseInt(cursorID, 10, 64)
			if pageErr != nil || afterID <= 0 {
				writeError(w, http.StatusBadRequest, "invalid list cursor")
				return
			}
		}
		all, hasMore, pageErr = paged.ListByWorkspacesPage(workspaceIDs, afterTime, afterID, limit)
	} else {
		if rawCursor != "" {
			writeError(w, http.StatusNotImplemented, "cursor pagination is not supported by this post store")
			return
		}
		for _, ws := range wss {
			posts, listErr := r.postStore.ListByWorkspace(ws.ID)
			if listErr == nil {
				all = append(all, posts...)
			}
		}
		if len(all) > limit {
			hasMore = true
			all = all[:limit]
		}
	}
	if pageErr != nil {
		writeError(w, http.StatusInternalServerError, "failed to list posts: "+pageErr.Error())
		return
	}
	if all == nil {
		all = []models.Post{}
	}
	writeJSON(w, http.StatusOK, postListResponse(all, hasMore, cursorContext))
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
