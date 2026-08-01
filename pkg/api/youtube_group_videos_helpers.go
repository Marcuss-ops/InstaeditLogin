package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// collectDescendantGroups returns the immediate + transitive children
// of rootGroupID (rootGroupID itself is NOT included — the caller
// already appends it). Cycle protection: a `visited` set prevents an
// accidental parent_group_id loop (defence-in-depth on top of the
// GroupRepository's wouldCreateCycle check at write time).
//
// Algorithm: BFS from rootGroupID over a parent-group-by-id index
// built in O(N) over the workspace's group list. Memory: O(N) index
// + O(depth) queue.
func collectDescendantGroups(all []models.Group, rootGroupID int64) []*models.Group {
	byParent := make(map[int64][]int64)
	byID := make(map[int64]models.Group, len(all))
	for _, g := range all {
		byID[g.ID] = g
		if g.ParentGroupID != nil {
			byParent[*g.ParentGroupID] = append(byParent[*g.ParentGroupID], g.ID)
		}
	}
	visited := map[int64]bool{rootGroupID: true}
	queue := []int64{rootGroupID}
	out := make([]*models.Group, 0)
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, child := range byParent[cur] {
			if visited[child] {
				continue
			}
			visited[child] = true
			if g, ok := byID[child]; ok {
				out = append(out, &g)
			}
			queue = append(queue, child)
		}
	}
	return out
}

func parseGroupVideosPagination(req *http.Request, cfg YouTubeGroupVideosConfig) (int, int, error) {
	q := req.URL.Query()
	offset := 0
	if raw := strings.TrimSpace(q.Get("offset")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 {
			return 0, 0, fmt.Errorf("offset must be a non-negative integer")
		}
		offset = value
	}
	limit := cfg.DefaultPageSize
	if raw := strings.TrimSpace(q.Get("limit")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value <= 0 {
			return 0, 0, fmt.Errorf("limit must be a positive integer")
		}
		limit = value
	}
	if limit > cfg.MaxVideos {
		limit = cfg.MaxVideos
	}
	return offset, limit, nil
}

func parseGroupVideosRecency(req *http.Request) (int, error) {
	days := strings.TrimSpace(req.URL.Query().Get("days"))
	if days == "" {
		return 90, nil
	}
	value, err := strconv.Atoi(days)
	if err != nil || (value != 7 && value != 14 && value != 28 && value != 90) {
		return 0, fmt.Errorf("days must be one of 7, 14, 28, or 90")
	}
	return value, nil
}

func filterRecentYouTubeVideos(items []models.YouTubeVideoDetails, days int) []models.YouTubeVideoDetails {
	cutoff := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	filtered := make([]models.YouTubeVideoDetails, 0, len(items))
	for _, item := range items {
		if item.PublishedAt == nil || !item.PublishedAt.Before(cutoff) {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func isInvalidYouTubeTokenError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{"invalid_grant", "status 401", "token expired", "token revoked"} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}
