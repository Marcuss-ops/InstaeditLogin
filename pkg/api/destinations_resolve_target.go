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

// VeloxResolveTargetRequest is the body for the service-to-service
// POST /internal/v1/destinations/resolve-target endpoint.
//
// The handler validates a target DESCRIPTOR (workspace_id + platform
// + channel-or-group) WITHOUT requiring the operator to have
// already stored an externalDestinations row. This is the
// pre-flight gate that the Velox worker calls BEFORE
// POST /internal/v1/deliveries: it surfaces "this channel/group
// is unusable" failures with structured error codes (TARGET_NOT_AVAILABLE,
// GROUP_EMPTY, BLOCKED_AUTH) instead of fail-silently at upload
// time. The companion endpoints in the contract doc (§3) are:
//
//   - POST /internal/v1/destinations/{id}/validate               (legacy / id-based)
//   - POST /internal/v1/destinations/resolve-target              (this handler, body-based)
//
// The /resolve-target path disambiguates from the legacy
// /destinations/{id}/validate by refusing to look up a stored
// destination row. The body shape mirrors the publishing spec
// §2.3 (the destination block the deliveries POST later sends).
type VeloxResolveTargetRequest struct {
	WorkspaceID int64 `json:"workspace_id"`

	// Platform is the canonical social platform string (matches
	// the canonical constants in internal/models/user.go).
	// Today the handler supports "youtube" — the only platform
	// with workspace channel bindings, group expansion, and
	// thumbnail-editing flows. Other platforms return 422.
	Platform string `json:"platform"`

	// Target carries the resolution descriptor. The Type field
	// picks the discriminator between channel and group.
	Target VeloxResolveTargetPayload `json:"target"`
}

// VeloxResolveTargetPayload is the union of channel/group
// descriptors. Exactly one of {PlatformAccountID, GroupID, ChannelID}
// must be non-empty per the type-shot contract; the handler
// rejects payloads that set a mismatched combination.
type VeloxResolveTargetPayload struct {
	Type string `json:"type"`

	// PlatformAccountID is the FK into platform_accounts (the
	// canonical InstaEdit route). Present when Type = "channel" and
	// the caller knows the InstaEdit primary key.
	PlatformAccountID int64 `json:"platform_account_id,omitempty"`

	// ChannelID is the provider's native channel id (e.g.
	// "UCxxxxxxxx" for YouTube). Accepted as an ALTERNATIVE to
	// PlatformAccountID when the caller's source-of-truth is the
	// YouTube channel id (common for cross-posting tools that
	// originate on YouTube's side).
	ChannelID string `json:"channel_id,omitempty"`

	// GroupID expands to N platform_account_ids via
	// GroupStore.ListAccountsInGroup. Group-empty → GROUP_EMPTY.
	GroupID int64 `json:"group_id,omitempty"`
}

// VeloxResolveTargetResponse is the wire shape returned by the
// handler. Two top-level branches:
//
//	valid=true  (HTTP 200):  ResolvedTargets fully populated;
//	                         ErrorCode/Message absent.
//	valid=false (HTTP 422):  ResolvedTargets populated iff the
//	                         failure occurred AFTER per-target
//	                         expansion (a partial-failure group
//	                         surfaces the failing member rows so
//	                         the operator UI can highlight which
//	                         channels need reauth); ErrorCode +
//	                         Message present.
//
// ErrorCode taxonomy:
//   - TARGET_NOT_AVAILABLE  — channel not bound to the workspace,
//     disabled, or belongs to a different workspace; OR group
//     belongs to a different workspace.
//   - GROUP_EMPTY           — group has zero accounts attached.
//   - BLOCKED_AUTH          — at least one account has
//     status='reauth_required' or reauth_required_at IS NOT NULL.
type VeloxResolveTargetResponse struct {
	Valid           bool                       `json:"valid"`
	DestinationID   string                     `json:"destination_id,omitempty"`
	ResolvedTargets []VeloxResolvedTargetEntry `json:"resolved_targets"`
	ErrorCode       string                     `json:"error_code,omitempty"`
	Message         string                     `json:"message,omitempty"`
}

// VeloxResolvedTargetEntry is one row in the resolved_targets
// array. Per-target status mirrors the underlying
// platform_account.status enum ("active", "reauth_required",
// etc.) so the operator dashboard can highlight individual
// failures inside a partial-failure group expansion.
type VeloxResolvedTargetEntry struct {
	PlatformAccountID int64  `json:"platform_account_id"`
	Platform          string `json:"platform"`
	// ChannelID is the provider-native id (YouTube: "UCxxxx").
	// Always populated from platform_account.PlatformUserID; the
	// caller can compare against the channel_id they sent in the
	// request to verify the binding round-trip.
	ChannelID   string `json:"channel_id"`
	ChannelName string `json:"channel_name,omitempty"`
	// Status mirrors platform_account.status. Always set; never
	// empty (a missing row never makes it into resolved_targets).
	Status string `json:"status"`
	// Enabled mirrors workspace_channels.enabled. False means the
	// operator has soft-disabled the channel in this workspace
	// (per-workspace mute); the channel row still exists in the
	// workspace but the worker pool must skip it.
	Enabled bool `json:"enabled"`
	// Per-target error_code when VALID=false AND the failure
	// scoped to this single row. Absent on a valid row.
	TargetErrorCode string `json:"target_error_code,omitempty"`
}

// handleResolveTargetInternalDestination implements
// POST /internal/v1/destinations/resolve-target.
//
// Thin adapter: delegates to the unified TargetResolver
// (internal/deliveries/target_resolver.go). The handler keeps
// JSON decode/validation + wire response mapping; the resolver
// owns all persistence-layer checks (workspace, account, binding,
// eligibility, group expansion).
//
// See docs/velox-instaedit-contract.md §3 for the canonical
// request/response shape and the error-code taxonomy.
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
		writeError(w, http.StatusNotImplemented,
			"resolve-target requires GroupStore (velox module wiring)")
		return
	}

	var payload VeloxResolveTargetRequest
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		slog.Warn("velox resolve-target: invalid JSON",
			"err", err, "remote_addr", req.RemoteAddr)
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	// Step 1: structural validation (unchanged).
	if err := validateResolveTargetRequest(&payload); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation: "+err.Error())
		return
	}

	// Step 2: delegate to the unified TargetResolver (DirectTarget path).
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
		slog.Error("velox resolve-target: resolver failed",
			"workspace_id", payload.WorkspaceID, "err", err)
		writeError(w, http.StatusInternalServerError, "target resolution failed")
		return
	}

	// Step 3: map resolver result to wire response.
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

// convertResolvedEntries maps the resolver's internal
// deliveries.ResolvedTargetEntry to the wire-visible
// VeloxResolvedTargetEntry. Fields are 1:1; the conversion
// exists because the resolver lives in internal/deliveries
// (Pattern 0 typed port) and the handler layer owns its own
// JSON-annotated DTOs.
func convertResolvedEntries(in []deliveries.ResolvedTargetEntry) []VeloxResolvedTargetEntry {
	if len(in) == 0 {
		return []VeloxResolvedTargetEntry{}
	}
	out := make([]VeloxResolvedTargetEntry, len(in))
	for i, e := range in {
		out[i] = VeloxResolvedTargetEntry{
			PlatformAccountID: e.PlatformAccountID,
			Platform:          e.Platform,
			ChannelID:         e.ChannelID,
			ChannelName:       e.ChannelName,
			Status:            e.Status,
			Enabled:           e.Enabled,
			TargetErrorCode:   e.TargetErrorCode,
		}
	}
	return out
}

// validateResolveTargetRequest enforces the structural rules
// of the request body. All malformed-shape rejections are 422
// (validation); only the persistence-layer 404s travel as
// TARGET_NOT_AVAILABLE.
func validateResolveTargetRequest(p *VeloxResolveTargetRequest) error {
	if p.WorkspaceID <= 0 {
		return errors.New("workspace_id must be a positive integer")
	}
	if p.Platform == "" {
		return errors.New("platform must be a non-empty string (e.g. \"youtube\")")
	}
	// Only "youtube" is wired for resolve-target today; other
	// platforms return a clean 422 here so the caller knows
	// the operator hasn't extended the surface yet. A future
	// Taglio lifts this into a CapabilityRouter check once
	// the resolve surface is replicated for TikTok/LinkedIn.
	if p.Platform != models.PlatformYouTube {
		return fmt.Errorf("platform %q is not supported by resolve-target (today: %q)",
			p.Platform, models.PlatformYouTube)
	}
	switch p.Target.Type {
	case "channel":
		// Resolution source: platform_account_id (canonical InstaEdit FK)
		// OR channel_id (provider-native id, e.g. YouTube UCxxxxxx).
		// Exactly one of the two MUST be supplied (else we have
		// nothing to resolve against).
		if p.Target.PlatformAccountID == 0 && p.Target.ChannelID == "" {
			return errors.New("target.type=channel requires platform_account_id OR channel_id")
		}
		// Both fields supplied is ALLOWED: platform_account_id is
		// authoritative for the binding lookup, channel_id is
		// used as a post-resolution OAuth-grant cross-check
		// (catches the "grant switched channel" failure mode,
		// BLOCKED_AUTH). The cross-check is non-trivial and
		// surfaces a leakage risk only when explicitly
		// requested, so the caller opts in by sending both.
		// The OR clause above (account_id != 0 || channel_id != "")
		// is equivalent to "we already passed the first check" —
		// at least one resolution source is non-empty by this
		// point. The GroupID rejection is therefore unconditional.
		if p.Target.GroupID != 0 {
			return errors.New("target.type=channel cannot specify group_id (use target.type=group instead)")
		}
	case "group":
		if p.Target.GroupID <= 0 {
			return errors.New("target.type=group requires group_id > 0")
		}
		if p.Target.PlatformAccountID != 0 || p.Target.ChannelID != "" {
			return errors.New("target.type=group cannot specify platform_account_id or channel_id (use target.type=channel instead)")
		}
	default:
		return fmt.Errorf("target.type %q is not supported (today: \"channel\" or \"group\")", p.Target.Type)
	}
	return nil
}


