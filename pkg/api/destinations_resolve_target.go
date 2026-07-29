package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/Marcuss-ops/InstaeditLogin/internal/deliveries"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// VeloxResolveTargetRequest is the canonical service-to-service target
// discovery and validation request. target.type supports:
//   - channel: validate one platform account or provider-native channel id;
//   - group: validate and expand one saved group;
//   - catalog: list every workspace channel together with posting capability.
type VeloxResolveTargetRequest struct {
	WorkspaceID int64                     `json:"workspace_id"`
	Platform    string                    `json:"platform"`
	Target      VeloxResolveTargetPayload `json:"target"`
}

// VeloxResolveTargetPayload is the discriminator payload. catalog intentionally
// carries no identifier: workspace_id + platform define the discovery scope.
type VeloxResolveTargetPayload struct {
	Type              string `json:"type"`
	PlatformAccountID int64  `json:"platform_account_id,omitempty"`
	ChannelID         string `json:"channel_id,omitempty"`
	GroupID           int64  `json:"group_id,omitempty"`
}

// VeloxResolveTargetResponse is shared by validation and catalog discovery.
// A catalog request returns valid=true when discovery completed, even when
// individual channels have can_post=false; row-level fields explain the block.
type VeloxResolveTargetResponse struct {
	Valid           bool                       `json:"valid"`
	DestinationID   string                     `json:"destination_id,omitempty"`
	ResolvedTargets []VeloxResolvedTargetEntry `json:"resolved_targets"`
	ErrorCode       string                     `json:"error_code,omitempty"`
	Message         string                     `json:"message,omitempty"`
}

// VeloxResolvedTargetEntry is the stable channel row returned to Velox and job
// senders. Stable IDs are authoritative; channel_name is display-only.
type VeloxResolvedTargetEntry struct {
	PlatformAccountID int64                             `json:"platform_account_id"`
	Platform          string                            `json:"platform"`
	ChannelID         string                            `json:"channel_id"`
	ChannelName       string                            `json:"channel_name,omitempty"`
	Status            string                            `json:"status"`
	Enabled           bool                              `json:"enabled"`
	CanPost           bool                              `json:"can_post"`
	BlockReason       string                            `json:"block_reason,omitempty"`
	Capabilities      deliveries.PublishingCapabilities `json:"capabilities"`
	TargetErrorCode   string                            `json:"target_error_code,omitempty"`
}

// handleResolveTargetInternalDestination implements
// POST /internal/v1/destinations/resolve-target.
//
// It is deliberately the only target-discovery boundary. channel/group
// validation and catalog listing both use the same TargetResolver and shared
// eligibility gate, preventing UI discovery from disagreeing with delivery.
func (m *VeloxModule) handleResolveTargetInternalDestination(w http.ResponseWriter, req *http.Request) {
	if m.deps.WorkspaceStore == nil {
		writeError(w, http.StatusInternalServerError, "workspace store not configured")
		return
	}
	if m.deps.UserStore == nil {
		writeError(w, http.StatusInternalServerError, "user store not configured")
		return
	}
	if m.deps.GroupStore == nil {
		writeError(w, http.StatusNotImplemented, "resolve-target requires GroupStore (velox module wiring)")
		return
	}

	var payload VeloxResolveTargetRequest
	decoder := json.NewDecoder(req.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		slog.Warn("velox resolve-target: invalid JSON", "err", err, "remote_addr", req.RemoteAddr)
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if err := validateResolveTargetRequest(&payload); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation: "+err.Error())
		return
	}

	if payload.Target.Type == "catalog" {
		entries, err := m.resolver().ListWorkspaceTargets(req.Context(), payload.WorkspaceID, payload.Platform)
		if err != nil {
			slog.Error("velox target catalog: resolver failed", "workspace_id", payload.WorkspaceID, "err", err)
			writeError(w, http.StatusInternalServerError, "target catalog resolution failed")
			return
		}
		writeJSON(w, http.StatusOK, VeloxResolveTargetResponse{
			Valid:           true,
			DestinationID:   "instaedit_" + payload.Platform,
			ResolvedTargets: convertResolvedEntries(entries),
		})
		return
	}

	result, err := m.resolver().Resolve(req.Context(), deliveries.ResolveRequest{
		WorkspaceID: payload.WorkspaceID,
		Platform:    payload.Platform,
		Target: deliveries.TargetDescriptor{
			Type:              payload.Target.Type,
			PlatformAccountID: payload.Target.PlatformAccountID,
			ChannelID:         payload.Target.ChannelID,
			GroupID:           payload.Target.GroupID,
		},
	})
	if err != nil {
		slog.Error("velox resolve-target: resolver failed", "workspace_id", payload.WorkspaceID, "err", err)
		writeError(w, http.StatusInternalServerError, "target resolution failed")
		return
	}
	if !result.Valid {
		writeJSON(w, http.StatusUnprocessableEntity, VeloxResolveTargetResponse{
			Valid:           false,
			ResolvedTargets: convertResolvedEntries(result.ResolvedTargets),
			ErrorCode:       result.ErrorCode,
			Message:         result.Message,
		})
		return
	}

	writeJSON(w, http.StatusOK, VeloxResolveTargetResponse{
		Valid:           true,
		DestinationID:   result.DestinationID,
		ResolvedTargets: convertResolvedEntries(result.ResolvedTargets),
	})
}

func convertResolvedEntries(in []deliveries.ResolvedTargetEntry) []VeloxResolvedTargetEntry {
	if len(in) == 0 {
		return []VeloxResolvedTargetEntry{}
	}
	out := make([]VeloxResolvedTargetEntry, len(in))
	for i, entry := range in {
		capabilities := deliveries.CapabilitiesForTarget(entry)
		out[i] = VeloxResolvedTargetEntry{
			PlatformAccountID: entry.PlatformAccountID,
			Platform:          entry.Platform,
			ChannelID:         entry.ChannelID,
			ChannelName:       entry.ChannelName,
			Status:            entry.Status,
			Enabled:           entry.Enabled,
			CanPost:           capabilities.UploadVideo && capabilities.Publish,
			BlockReason:       resolvedTargetBlockReason(entry),
			Capabilities:      capabilities,
			TargetErrorCode:   entry.TargetErrorCode,
		}
	}
	return out
}

func resolvedTargetBlockReason(entry deliveries.ResolvedTargetEntry) string {
	switch entry.TargetErrorCode {
	case deliveries.ErrCodeBlockedAuth:
		return "channel authentication requires attention"
	case deliveries.ErrCodeTargetNotAvailable:
		if !entry.Enabled {
			return "channel is disabled in this workspace"
		}
		return "channel is not available for publishing"
	default:
		return ""
	}
}

func validateResolveTargetRequest(payload *VeloxResolveTargetRequest) error {
	if payload == nil {
		return errors.New("request body is required")
	}
	if payload.WorkspaceID <= 0 {
		return errors.New("workspace_id must be a positive integer")
	}
	if payload.Platform == "" {
		return errors.New("platform must be a non-empty string (e.g. \"youtube\")")
	}
	if payload.Platform != models.PlatformYouTube {
		return fmt.Errorf("platform %q is not supported by resolve-target (today: %q)", payload.Platform, models.PlatformYouTube)
	}

	target := payload.Target
	switch target.Type {
	case "channel":
		if target.PlatformAccountID == 0 && target.ChannelID == "" {
			return errors.New("target.type=channel requires platform_account_id OR channel_id")
		}
		if target.GroupID != 0 {
			return errors.New("target.type=channel cannot specify group_id")
		}
	case "group":
		if target.GroupID <= 0 {
			return errors.New("target.type=group requires group_id > 0")
		}
		if target.PlatformAccountID != 0 || target.ChannelID != "" {
			return errors.New("target.type=group cannot specify platform_account_id or channel_id")
		}
	case "catalog":
		if target.PlatformAccountID != 0 || target.ChannelID != "" || target.GroupID != 0 {
			return errors.New("target.type=catalog cannot specify channel or group identifiers")
		}
	default:
		return fmt.Errorf("target.type %q is not supported (today: channel, group or catalog)", target.Type)
	}
	return nil
}
