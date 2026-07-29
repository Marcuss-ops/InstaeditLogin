// Package api — delivery contract types and idempotency-key parser.
//
// Part of the split of deliveries_create.go (audit T3: versioned contract).
// Contains all request/response DTOs for POST /internal/v1/deliveries:
//
//   - VeloxDeliverContractRequest  — the spec §7.1 nested contract (new)
//   - VeloxDeliverArtifactRequest  — the legacy flat contract (deprecated)
//   - Response types for both paths + the 409 conflict body
//   - Idempotency-Key regex + parser
//
// The new contract carries an explicit contract_version discriminator
// ("velox-instaedit.v1"); the dispatcher checks this field instead of
// the old shape-detection heuristic (isVeloxContractRequest).
package api

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// ContractVersionV1 is the canonical discriminator for the spec §7.1
// nested contract. Present in VeloxDeliverContractRequest.ContractVersion;
// absent from legacy requests. The handler uses this to dispatch between
// the contract path and the legacy path — no auto-detection heuristics.
const ContractVersionV1 = "velox-instaedit.v1"

// ── Idempotency-Key ─────────────────────────────────────────────────

// VeloxContractIdempotencyKeyRegex matches the canonical Idempotency-Key
// format mandated by `docs/velox-instaedit-contract.md` §7.2:
//
//	velox-<job_id>-<artifact_id>-<platform>-<account|group>
var VeloxContractIdempotencyKeyRegex = regexp.MustCompile(`^velox-[a-z0-9_]+-[a-z0-9_]+-[a-z0-9_]+-[a-z0-9_]+$`)

// VeloxContractIdempotencyKeyParts is the typed decomposition.
type VeloxContractIdempotencyKeyParts struct {
	JobID          string
	ArtifactID     string
	Platform       string
	AccountOrGroup string
}

// ParseVeloxContractIdempotencyKey splits a canonical key into its 4 segments.
func ParseVeloxContractIdempotencyKey(s string) (VeloxContractIdempotencyKeyParts, bool) {
	if !VeloxContractIdempotencyKeyRegex.MatchString(s) {
		return VeloxContractIdempotencyKeyParts{}, false
	}
	parts := strings.Split(strings.TrimPrefix(s, "velox-"), "-")
	if len(parts) != 4 {
		return VeloxContractIdempotencyKeyParts{}, false
	}
	return VeloxContractIdempotencyKeyParts{
		JobID:          parts[0],
		ArtifactID:     parts[1],
		Platform:       parts[2],
		AccountOrGroup: parts[3],
	}, true
}

func accountOrGroupMatches(segment string, targetType string, expectedID int64) bool {
	wantStr := fmt.Sprintf("%d", expectedID)
	if segment == wantStr {
		return true
	}
	var prefix string
	switch targetType {
	case "channel":
		prefix = "account"
	case "group":
		prefix = "group"
	default:
		return false
	}
	if strings.HasPrefix(segment, prefix+"_") {
		return segment[len(prefix)+1:] == wantStr
	}
	if strings.HasPrefix(segment, prefix+":") {
		return segment[len(prefix)+1:] == wantStr
	}
	return false
}

// ── New contract request (spec §7.1) ────────────────────────────────

// VeloxDeliverContractRequest is the body shape mandated by
// `docs/velox-instaedit-contract.md` §7.1. The ContractVersion field
// is the EXPLICIT discriminator — the handler dispatches on this
// field, NOT on a field-presence heuristic.
type VeloxDeliverContractRequest struct {
	ContractVersion string                   `json:"contract_version"` // "velox-instaedit.v1"
	Source          VeloxContractSource      `json:"source"`
	Media           VeloxContractMedia       `json:"media"`
	Destination     VeloxContractDestination `json:"destination"`
	Publication     VeloxContractPublication `json:"publication"`
}

// hasContractVersion returns true when the request carries a recognised
// contract_version. This replaces the old isVeloxContractRequest heuristic.
func hasContractVersion(r *VeloxDeliverContractRequest) bool {
	return r != nil && r.ContractVersion == ContractVersionV1
}

type VeloxContractSource struct {
	System     string `json:"system"`
	JobID      string `json:"job_id"`
	TaskID     string `json:"task_id"`
	ArtifactID string `json:"artifact_id"`
}

type VeloxContractMedia struct {
	DownloadURL     string  `json:"download_url"`
	SHA256          string  `json:"sha256"`
	SizeBytes       int64   `json:"size_bytes"`
	MimeType        string  `json:"mime_type"`
	DurationSeconds float64 `json:"duration_seconds"`
}

type VeloxContractDestination struct {
	WorkspaceID       int64  `json:"workspace_id"`
	Platform          string `json:"platform"`
	TargetType        string `json:"target_type"`
	PlatformAccountID int64  `json:"platform_account_id,omitempty"`
	GroupID           int64  `json:"group_id,omitempty"`
}

type VeloxContractPublication struct {
	Title            string     `json:"title"`
	Description      string     `json:"description"`
	Tags             []string   `json:"tags,omitempty"`
	InitialPrivacy   string     `json:"initial_privacy"`
	FinalPrivacy     string     `json:"final_privacy"`
	RequireThumbnail bool       `json:"require_thumbnail"`
	PublishAt        *time.Time `json:"publish_at,omitempty"`
}

// ── New contract response ───────────────────────────────────────────

// VeloxDeliverContractResponse is the 202 body for the CONTRACT path.
type VeloxDeliverContractResponse struct {
	DeliveryID string `json:"delivery_id"`
	Status     string `json:"status"` // always "accepted"
	Duplicate  bool   `json:"duplicate"`
}

// ── Legacy contract (VeloxDeliverArtifactRequest, velox_types.go) ──

// VeloxDeliverArtifactRequest is declared in velox_types.go.
// VeloxDeliverArtifactResponse is declared in velox_types.go.
// VeloxDeliverArtifactConflictResponse is declared in velox_types.go.

// ── Synthesised ids ─────────────────────────────────────────────────

// synthesizeContractDeliveryID hashes (system:job:task) into a stable
// external_delivery_id.
func synthesizeContractDeliveryID(r *VeloxDeliverContractRequest) string {
	h := sha256.Sum256([]byte("delivery:" + r.Source.System + ":" + r.Source.JobID + ":" + r.Source.TaskID))
	return "velox-" + hex.EncodeToString(h[:8])
}

// synthesizeContractDestinationID hashes the destination triple into
// a stable external_destination_id.
func synthesizeContractDestinationID(r *VeloxContractDestination) string {
	var key string
	switch r.TargetType {
	case "channel":
		key = fmt.Sprintf("destination:channel:ws=%d|platform=%s|account=%d", r.WorkspaceID, r.Platform, r.PlatformAccountID)
	case "group":
		key = fmt.Sprintf("destination:group:ws=%d|platform=%s|group=%d", r.WorkspaceID, r.Platform, r.GroupID)
	default:
		key = fmt.Sprintf("destination:unknown:ws=%d|platform=%s", r.WorkspaceID, r.Platform)
	}
	h := sha256.Sum256([]byte(key))
	return "extdst-" + hex.EncodeToString(h[:8])
}
