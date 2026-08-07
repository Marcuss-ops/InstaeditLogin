package models

import (
	"fmt"
	"strings"
	"time"
)

// ProjectBridgeContractVersion is the normative wire contract for the
// one-way InstaEdit → editor project reference. It must not be reused for
// publishing or catalog contracts.
const ProjectBridgeContractVersion = "instaedit.velox.project-bridge.v1"

// VeloxProjectBridge is the minimal, InstaEdit-owned relation between an
// application project and an opaque editor project. workspace_id is retained
// solely as the tenant boundary. It intentionally carries no group, channel,
// video, platform, or editor-internal data.
type VeloxProjectBridge struct {
	ContractVersion string `json:"contract_version,omitempty"`

	ProjectID         string `json:"project_id"`
	ExternalProjectID string `json:"external_project_id"`
	WorkspaceID       int64  `json:"workspace_id"`

	// EditorProvider identifies the editor backend owning VeloxProjectID.
	EditorProvider string `json:"editor_provider,omitempty"`
	// EditorStatus and LastEditorSyncAt are optional coarse operational
	// metadata only. Timeline, layers, revisions, and render state remain
	// owned by the editor system.
	EditorStatus     string     `json:"editor_status,omitempty"`
	LastEditorSyncAt *time.Time `json:"last_editor_sync_at,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// NormalizeAndValidate canonicalizes a bridge before persistence. It never
// accepts or derives group/channel/video context: those are owned by the
// application records that authorize the project operation.
func (b *VeloxProjectBridge) NormalizeAndValidate() error {
	if b == nil {
		return fmt.Errorf("velox project bridge is required")
	}
	b.ProjectID = strings.TrimSpace(b.ProjectID)
	b.ExternalProjectID = strings.TrimSpace(b.ExternalProjectID)
	b.EditorProvider = strings.TrimSpace(b.EditorProvider)
	b.EditorStatus = strings.TrimSpace(b.EditorStatus)
	if b.ProjectID == "" || b.ExternalProjectID == "" {
		return fmt.Errorf("project_id and external_project_id are required")
	}
	if b.WorkspaceID <= 0 {
		return fmt.Errorf("workspace_id must be positive")
	}
	if b.EditorProvider == "" {
		b.EditorProvider = "velox"
	}
	return nil
}
