package models

import "time"

// AccountDetails holds the remote resource representation of a connected
// platform account (channel, page, profile). Returned by
// AccountDetailsProvider.GetAccountDetails. The struct is
// platform-agnostic: each provider fills only the fields it supports;
// omitted fields are omitted from JSON.
type AccountDetails struct {
	ResourceType string          `json:"resource_type"`
	ExternalID   string          `json:"external_id"`
	DisplayName  string          `json:"display_name"`
	Handle       string          `json:"handle,omitempty"`
	Description  string          `json:"description,omitempty"`
	AvatarURL    string          `json:"avatar_url,omitempty"`
	BannerURL    string          `json:"banner_url,omitempty"`
	PublicURL    string          `json:"public_url,omitempty"`
	Metrics      []AccountMetric `json:"metrics"`
	Properties   map[string]any  `json:"properties,omitempty"`
	FetchedAt    time.Time       `json:"fetched_at"`
}

// AccountMetric is a single numeric metric for an account (subscribers,
// views, video count, etc.). DisplayValue is the human-readable
// formatting ("125K", "18M").
type AccountMetric struct {
	Key          string `json:"key"`
	Label        string `json:"label"`
	Value        int64  `json:"value"`
	DisplayValue string `json:"display_value"`
}

// AccountContentPage holds a paginated list of content items (videos,
// posts, etc.) for a platform account.
type AccountContentPage struct {
	Items      []AccountContentItem `json:"items"`
	NextCursor string               `json:"next_cursor,omitempty"`
}

// AccountContentItem represents a single content item (video, post)
// belonging to a platform account.
type AccountContentItem struct {
	ExternalID   string          `json:"external_id"`
	Title        string          `json:"title,omitempty"`
	Description  string          `json:"description,omitempty"`
	ThumbnailURL string          `json:"thumbnail_url,omitempty"`
	PublicURL    string          `json:"public_url,omitempty"`
	Privacy      string          `json:"privacy,omitempty"`
	Status       string          `json:"status,omitempty"`
	PublishedAt  *time.Time      `json:"published_at,omitempty"`
	Duration     string          `json:"duration,omitempty"`
	Metrics      []AccountMetric `json:"metrics"`
	Properties   map[string]any  `json:"properties,omitempty"`
}
