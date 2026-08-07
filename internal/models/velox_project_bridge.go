package models

import (
	"fmt"
	"strings"
	"time"
)

// ProjectBridgeContractVersion is the normative wire contract for the
// one-way InstaEdit → Velox project reference. It must not be reused for
// publishing/catalog contracts.
const ProjectBridgeContractVersion = "instaedit.velox.project-bridge.v1"

// VeloxProjectBridge is the minimal, InstaEdit-owned relation between an
// application project and an opaque editor project. It intentionally carries
// no group membership, channel catalog, or editor-internal data (timeline,
// layers, render state, revisions). Those stay in the editor system.
type VeloxProjectBridge struct {

	// ContractVersion is emitted at the API boundary; it is not a persisted
	// domain field and does not create a second ownership model.
	ContractVersion string `json:"contract_version,omitempty"`

	ProjectID         string  `json:"project_id"`
	VeloxProjectID    string  `json:"velox_project_id"`
	WorkspaceID       int64   `json:"workspace_id"`
	Platform          string  `json:"platform,omitempty"`
	PlatformAccountID *int64  `json:"platform_account_id,omitempty"`
	ChannelID         *string `json:"channel_id,omitempty"`
	VideoID           *string `json:"video_id,omitempty"`
	Language          *string `json:"language,omitempty"`
	// EditorProvider names the editor backend the velox_project_id belongs
	// to ("velox" today). It exists so the bridge can be re-targeted to a
	// different editor without changing InstaEdit's project model.
	EditorProvider string `json:"editor_provider,omitempty"`
	// EditorStatus and LastEditorSyncAt are the only allowed operational
	// metadata. They are not a competing project lifecycle: editor-internal
	// status (render/job state) remains editor-owned.
	EditorStatus     string     `json:"editor_status,omitempty"`
	LastEditorSyncAt *time.Time `json:"last_editor_sync_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
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
	// The bridge is provider-declarative: it records which editor backend
	// owns the external project id. "velox" is the current implementation.
	if strings.TrimSpace(b.EditorProvider) == "" {
		b.EditorProvider = "velox"
	}
	b.EditorProvider = strings.TrimSpace(b.EditorProvider)
	b.EditorStatus = strings.TrimSpace(b.EditorStatus)
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
