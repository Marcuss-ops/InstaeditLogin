package api

// Read-only post handlers.

import (
	"net/http"

	"sort"

	"strconv"

	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

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
	id, err := postIDFromURL(req)
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
	if !r.postWorkspaceOwnedByUser(p, userID) {
		writeError(w, http.StatusNotFound, "post not found")
		return
	}
	writeJSON(w, http.StatusOK, p)
}

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
	cursorContext := "workspace_id=" + strconv.FormatInt(wid, 10)
	limit, afterTime, afterID, hasCursor, err := parsePostListPage(req.URL.Query(), cursorContext)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var posts []models.Post
	hasMore := false
	if paged, ok := r.postStore.(interface {
		ListByWorkspacePage(int64, *time.Time, int64, int) ([]models.Post, bool, error)
	}); ok {
		posts, hasMore, err = paged.ListByWorkspacePage(wid, afterTime, afterID, limit)
	} else {
		if hasCursor {
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
		cursorContext := listCursorFilterContext(req.URL.Query(), "workspace_id")
		limit, afterTime, afterID, hasCursor, pageErr := parsePostListPage(req.URL.Query(), cursorContext)
		if pageErr != nil {
			writeError(w, http.StatusBadRequest, pageErr.Error())
			return
		}
		var posts []models.Post
		hasMore := false
		if paged, ok := r.postStore.(interface {
			ListByWorkspacePage(int64, *time.Time, int64, int) ([]models.Post, bool, error)
		}); ok {
			posts, hasMore, pageErr = paged.ListByWorkspacePage(wid, afterTime, afterID, limit)
		} else {
			if hasCursor {
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
	cursorContext := "workspaces=" + joinInt64List(workspaceIDs)
	limit, afterTime, afterID, hasCursor, pageErr := parsePostListPage(req.URL.Query(), cursorContext)
	if pageErr != nil {
		writeError(w, http.StatusBadRequest, pageErr.Error())
		return
	}
	var all []models.Post
	hasMore := false
	if paged, ok := r.postStore.(interface {
		ListByWorkspacesPage([]int64, *time.Time, int64, int) ([]models.Post, bool, error)
	}); ok {
		all, hasMore, pageErr = paged.ListByWorkspacesPage(workspaceIDs, afterTime, afterID, limit)
	} else {
		if hasCursor {
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
