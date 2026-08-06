package api

import (
	"strconv"
	"strings"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

func joinInt64List(values []int64) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, strconv.FormatInt(value, 10))
	}
	return strings.Join(parts, ",")
}

func postListResponse(posts []models.Post, hasMore bool, context string) paginatedPostListResponse {
	items := make([]postListItem, 0, len(posts))
	for _, post := range posts {
		items = append(items, postListItemFromModel(post))
	}
	response := paginatedPostListResponse{Posts: items, HasMore: hasMore}
	if hasMore && len(posts) > 0 {
		last := posts[len(posts)-1]
		response.NextCursor = encodeListCursorForContext("posts", context, last.CreatedAt, strconv.FormatInt(last.ID, 10))
	}
	return response
}

func groupListResponse(groups []models.Group, hasMore bool, context string) paginatedGroupListResponse {
	items := make([]groupListItem, 0, len(groups))
	for _, group := range groups {
		items = append(items, groupListItemFromModel(group))
	}
	response := paginatedGroupListResponse{Groups: items, HasMore: hasMore}
	if hasMore && len(groups) > 0 {
		last := groups[len(groups)-1]
		response.NextCursor = encodeListCursorForContext("groups", context, last.CreatedAt, strconv.FormatInt(last.ID, 10))
	}
	return response
}
