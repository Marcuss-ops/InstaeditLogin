package models

import (
	"encoding/json"
	"time"
)

// ExternalDestination is the on-disk representation of one Velox→InstaEdit
// opaque destination pointer (migration 054_external_destinations.sql).
//
// Server-side, each row resolves extdst_01J... → (workspace, platform
// account, channel, OAuth token, publish defaults). The upstream system
// (Velox, future Dropbox/S3-drop) only ever receives the opaque id;
// credentials NEVER cross the boundary.
//
// PRIMARY KEY id is TEXT (ULID-style with `extdst_` prefix per the
// integration spec) generated application-side — the schema does
// NOT auto-generate. The handler layer enforces the prefix on
// writes; the DB only stores the byte payload.
//
// JSON-tagged to match the published REST shape of
// POST /api/v1/integrations/velox/destinations + GET on the
// corresponding read endpoint. Field names are camelCase to mirror
// the spec's "external_destination_id" rather than snake_case.
type ExternalDestination struct {
	ID                string          `json:"id"`                  // extdst_01J...
	SourceSystem      string          `json:"source_system"`       // "velox" today
	WorkspaceID       int64           `json:"workspace_id"`
	PlatformAccountID int64           `json:"platform_account_id"`
	Enabled           bool            `json:"enabled"`             // operator toggle
	DefaultMetadata   json.RawMessage `json:"default_metadata"`    // JSONB byte slice (round-trip)
	CreatedAt         time.Time       `json:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at"`
}

// DefaultMetadataAsMap decodes DefaultMetadata into a map. Returns
// nil + nil when the column was unset (`{}`::jsonb → empty map).
// The publish_worker calls this when applying per-tenant defaults;
// the result is a key/value map the worker can JSON_EXTRACT individual
// fields from without re-parsing.
//
// Mirrors the import_batch repo's `RawString`-style helper. NOT
// received/serialised over the wire — purely application-side.
func (e *ExternalDestination) DefaultMetadataAsMap() (map[string]any, error) {
	if len(e.DefaultMetadata) == 0 {
		return nil, nil
	}
	var out map[string]any
	if err := json.Unmarshal(e.DefaultMetadata, &out); err != nil {
		return nil, err
	}
	return out, nil
}
