package api

import (
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// postListItem is the deliberately small projection used by list endpoints.
// Full post metadata and target fan-out remain detail reads.
type postListItem struct {
	ID             int64             `json:"id"`
	WorkspaceID    int64             `json:"workspace_id"`
	Title          string            `json:"title,omitempty"`
	Caption        string            `json:"caption,omitempty"`
	MediaURL       string            `json:"media_url,omitempty"`
	MediaAssetID   *string           `json:"media_asset_id,omitempty"`
	PrivacyLevel   string            `json:"privacy_level,omitempty"`
	DefaultPrivacy string            `json:"default_privacy_level,omitempty"`
	IngestAfter    time.Time         `json:"ingest_after"`
	PublishAt      *time.Time        `json:"publish_at,omitempty"`
	Status         models.PostStatus `json:"status"`
	UploadJobID    *int64            `json:"upload_job_id,omitempty"`
	CreatedAt      time.Time         `json:"created_at"`
}

func postListItemFromModel(post models.Post) postListItem {
	return postListItem{
		ID:             post.ID,
		WorkspaceID:    post.WorkspaceID,
		Title:          post.Title,
		Caption:        post.Caption,
		MediaURL:       post.MediaURL,
		MediaAssetID:   post.MediaAssetID,
		PrivacyLevel:   post.PrivacyLevel,
		DefaultPrivacy: post.DefaultPrivacyLevel,
		IngestAfter:    post.IngestAfter,
		PublishAt:      post.PublishAt,
		Status:         post.Status,
		UploadJobID:    post.UploadJobID,
		CreatedAt:      post.CreatedAt,
	}
}

type paginatedPostListResponse struct {
	Posts      []postListItem `json:"posts"`
	NextCursor string         `json:"next_cursor,omitempty"`
	HasMore    bool           `json:"has_more"`
}

// groupListItem excludes the aggregate account membership and any future
// internal columns. Memberships are returned by the aggregate endpoint.
type groupListItem struct {
	ID            int64     `json:"id"`
	WorkspaceID   int64     `json:"workspace_id"`
	ParentGroupID *int64    `json:"parent_group_id,omitempty"`
	Name          string    `json:"name"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func groupListItemFromModel(group models.Group) groupListItem {
	return groupListItem{
		ID:            group.ID,
		WorkspaceID:   group.WorkspaceID,
		ParentGroupID: group.ParentGroupID,
		Name:          group.Name,
		CreatedAt:     group.CreatedAt,
		UpdatedAt:     group.UpdatedAt,
	}
}

type paginatedGroupListResponse struct {
	Groups     []groupListItem `json:"groups"`
	NextCursor string          `json:"next_cursor,omitempty"`
	HasMore    bool            `json:"has_more"`
}
