package models

import (
	"fmt"
	"strings"
	"time"
)

// VeloxProjectBridge is the minimal, InstaEdit-owned relation between an
// application project and an opaque editor project. It intentionally carries
// no group membership or channel catalog data.
type VeloxProjectBridge struct {
	ProjectID         string    `json:"project_id"`
	VeloxProjectID    string    `json:"velox_project_id"`
	WorkspaceID       int64     `json:"workspace_id"`
	Platform          string    `json:"platform,omitempty"`
	PlatformAccountID *int64    `json:"platform_account_id,omitempty"`
	ChannelID         *string   `json:"channel_id,omitempty"`
	VideoID           *string   `json:"video_id,omitempty"`
	Language          *string   `json:"language,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// NormalizeAndValidate canonicalizes a bridge before persistence. A channel
// context is all-or-nothing at the account boundary: without an InstaEdit
// platform account, no provider context may be persisted.
func (b *VeloxProjectBridge) NormalizeAndValidate() error {
	if b == nil {
		return fmt.Errorf("velox project bridge is required")
	}
	b.ProjectID = strings.TrimSpace(b.ProjectID)
	b.VeloxProjectID = strings.TrimSpace(b.VeloxProjectID)
	b.Platform = strings.ToLower(strings.TrimSpace(b.Platform))
	if b.ProjectID == "" || b.VeloxProjectID == "" {
		return fmt.Errorf("project_id and velox_project_id are required")
	}
	if b.WorkspaceID <= 0 {
		return fmt.Errorf("workspace_id must be positive")
	}
	if b.PlatformAccountID == nil {
		if b.Platform != "" || b.ChannelID != nil || b.VideoID != nil || b.Language != nil {
			return fmt.Errorf("channel context requires platform_account_id")
		}
		return nil
	}
	if *b.PlatformAccountID <= 0 {
		return fmt.Errorf("platform_account_id must be positive")
	}
	if b.Platform == "" {
		return fmt.Errorf("platform is required when channel context is present")
	}
	for name, value := range map[string]**string{
		"channel_id": &b.ChannelID,
		"video_id":   &b.VideoID,
		"language":   &b.Language,
	} {
		if *value == nil {
			continue
		}
		trimmed := strings.TrimSpace(**value)
		if trimmed == "" {
			return fmt.Errorf("%s cannot be empty", name)
		}
		*value = &trimmed
	}
	return nil
}
