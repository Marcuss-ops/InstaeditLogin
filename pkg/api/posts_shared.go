package api

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// parsePostListPage centralizes cursor validation shared by the workspace
// and authenticated-user post list handlers. It returns the exact values
// expected by the PostStore page methods; callers remain responsible for
// mapping errors to their endpoint-specific HTTP response.
func parsePostListPage(q url.Values, cursorContext string) (limit int, afterTime *time.Time, afterID int64, hasCursor bool, err error) {
	limit, rawCursor, err := parseListPageWithBounds(q, 50, 100)
	if err != nil {
		return 0, nil, 0, false, err
	}
	cursorTime, cursorID, cursorNull, err := decodeListCursorDetails(rawCursor, "posts", cursorContext)
	if err != nil {
		return 0, nil, 0, false, err
	}
	if cursorNull && rawCursor != "" {
		return 0, nil, 0, false, fmt.Errorf("invalid list cursor: post cursor timestamp is required")
	}
	if rawCursor == "" {
		return limit, nil, 0, false, nil
	}
	afterID, err = strconv.ParseInt(cursorID, 10, 64)
	if err != nil || afterID <= 0 {
		return 0, nil, 0, false, fmt.Errorf("invalid list cursor")
	}
	return limit, &cursorTime, afterID, true, nil
}

// postWorkspaceOwnedByUser is the shared workspace-isolation predicate for
// post detail/update handlers. Any workspace lookup failure is deliberately
// treated as not-owned so callers preserve their existing 404 contract.
func (r *Router) postWorkspaceOwnedByUser(post *models.Post, userID int64) bool {
	if r == nil || r.workspaceStore == nil || post == nil {
		return false
	}
	workspace, err := r.workspaceStore.FindByID(post.WorkspaceID)
	return err == nil && workspace != nil && workspace.OwnerID == userID
}

// postIDFromURL keeps route-id parsing consistent for shared post handlers
// without changing the existing chi route parameter contract.
func postIDFromURL(req *http.Request) (int64, error) {
	return strconv.ParseInt(chi.URLParam(req, "id"), 10, 64)
}
