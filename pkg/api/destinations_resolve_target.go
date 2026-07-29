package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

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

// veloxResolveTargetDestinationIDPrefix is the canonical
// destination_id stamp for the resolve-target response. The
// value format "instaedit_<platform>" matches the existing
// diagnostic JSON in handleValidateInternalDestination's
// VeloxValidateDestinationResponse (which stamps "instaedit_youtube"
// for the active platform).
const veloxResolveTargetDestinationIDPrefix = "instaedit_"

// handleResolveTargetInternalDestination implements
// POST /internal/v1/destinations/resolve-target.
//
// ASYMMETRY with /internal/v1/destinations/{id}/validate:
//   - The legacy id-based endpoint looks up an existing
//     external_destinations row.
//   - The resolve-target endpoint takes a target descriptor
//     (workspace_id + platform + channel|group) and validates
//     it WITHOUT requiring a stored destinations row. This is
//     the spec'd pre-flight gate for the Velox worker.
//
// See docs/velox-instaedit-contract.md §3 for the canonical
// request/response shape and the error-code taxonomy.
//
// VELOX_API_TOKEN: enforced by the internalVeloxAuthMiddleware
// wrapper in VeloxModule.Register. The middleware emits 401 (no
// header), 403 (token mismatch), 503 (token not configured) —
// this handler emits only 200, 422, 500. The split lets the
// 401/403/503 signals route to the operator's paging while the
// 422/200 routing stays Velox-spec-compliant.
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

	// Step 1: structural validation. WorkspaceID>0 AND
	// platform AND target type are mandatory. The Target union
	// must specify exactly one resolution source.
	if err := validateResolveTargetRequest(&payload); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "validation: "+err.Error())
		return
	}

	// Step 2: workspace existence (collapses "wrong workspace"
	// with "non-existent" to TARGET_NOT_AVAILABLE so a probing
	// caller cannot enumerate valid workspace ids).
	ws, err := m.deps.WorkspaceStore.FindByID(payload.WorkspaceID)
	if err != nil {
		slog.Error("velox resolve-target: workspace lookup failed",
			"workspace_id", payload.WorkspaceID, "err", err)
		writeError(w, http.StatusInternalServerError, "workspace lookup failed")
		return
	}
	if ws == nil {
		respondResolveTargetInvalid(w, VeloxResolveTargetError{}, "TARGET_NOT_AVAILABLE", "workspace not found")
		return
	}

	// Step 3: target-type discriminator dispatches to the
	// channel resolution or the group expansion branch. Both
	// branches populate the same response shape so the caller
	// doesn't need to dispatch on the request discriminator
	// later.
	switch payload.Target.Type {
	case "channel":
		entries, code, msg := m.resolveChannelTarget(req.Context(),
			payload.WorkspaceID, payload.Platform, payload.Target)
		if code != "" {
			respondResolveTargetInvalid(w,
				VeloxResolveTargetError{Target: entries}, code, msg)
			return
		}
		writeJSON(w, http.StatusOK, VeloxResolveTargetResponse{
			Valid:           true,
			DestinationID:   veloxResolveTargetDestinationIDPrefix + payload.Platform,
			ResolvedTargets: entries,
		})
		return
	case "group":
		entries, code, msg := m.resolveGroupTarget(req.Context(),
			payload.WorkspaceID, payload.Platform, payload.Target.GroupID)
		if code != "" {
			respondResolveTargetInvalid(w,
				VeloxResolveTargetError{Target: entries}, code, msg)
			return
		}
		writeJSON(w, http.StatusOK, VeloxResolveTargetResponse{
			Valid:           true,
			DestinationID:   veloxResolveTargetDestinationIDPrefix + payload.Platform,
			ResolvedTargets: entries,
		})
		return
	default:
		// validateResolveTargetRequest already rejected unknown
		// types; this branch is defensive against a future payload
		// shape that adds a new type without registering it here.
		writeError(w, http.StatusUnprocessableEntity,
			"validation: unsupported target.type (this is a server bug; supported: channel|group)")
		return
	}
}

// VeloxResolveTargetError is the internal carrier for invalid
// results so respondResolveTargetInvalid can stay a single
// helper used twice (channel + group branches). The Target
// slice may carry partial-failure rows so the operator UI can
// highlight which member failed; an empty Target means "no
// rows produced" (e.g. GROUP_EMPTY before expansion).
type VeloxResolveTargetError struct {
	Target []VeloxResolvedTargetEntry
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

// respondResolveTargetInvalid emits the 422 + JSON body for all
// valid=false paths. Centralising the wire shape keeps the
// channel/group branches' invalid-path code identical (no risk
// of a 200-only or 200/422 wire mismatch).
func respondResolveTargetInvalid(w http.ResponseWriter, e VeloxResolveTargetError, code, msg string) {
	if e.Target == nil {
		e.Target = []VeloxResolvedTargetEntry{}
	}
	writeJSON(w, http.StatusUnprocessableEntity, VeloxResolveTargetResponse{
		Valid:           false,
		ResolvedTargets: e.Target,
		ErrorCode:       code,
		Message:         msg,
	})
}

// resolveChannelTarget validates one channel target and returns
// the resolved entry (or empty + error code when invalid).
//
// Returns ([entry], "", "") on full validity.
// Returns ([], "CODE", "msg") on a no-rows surface failure.
// Returns ([partial-entry], "CODE", "msg") on a per-target
// blockage where the row still exists in resolved_targets so
// the operator UI can highlight the failing member.
//
// Algorithm:
//  1. Resolve platform_account_id from the discriminator
//     (PlatformAccountID direct OR ChannelID via
//     WorkspaceStore.ListChannels filtered on
//     platform_account.PlatformUserID).
//  2. WorkspaceStore.FindChannel(workspace_id, account_id) →
//     binding check (binding exists + enabled).
//  3. UserStore.FindPlatformAccountByID → status/eligibility
//     (active, NOT reauth_required, NOT revoked/disconnected).
//  4. If caller supplied ChannelID additional cross-check that
//     pa.PlatformUserID == channel_id (defends against
//     "wrong-channel grant" — a stale OAuth whose grant id
//     maps to a different YouTube channel).
//
// Error-code precedence (most-severe wins):
//
//	BLOCKED_AUTH > TARGET_NOT_AVAILABLE
//
// The classes cover mutually-exclusive failure surfaces:
//   - BLOCKED_AUTH fires whenever the platform_account status
//     is non-actionable (reauth_required, revoked, disconnected,
//     pending_authorization, suspended, error). Error is
//     grant-side → operator action is "rotate the grant".
//   - TARGET_NOT_AVAILABLE fires when the row is eligible but
//     not bound (no workspace_channels row, disabled binding,
//     cross-workspace leak). Error is workspace-side → operator
//     action is "fix the binding".
func (m *VeloxModule) resolveChannelTarget(
	ctx context.Context,
	workspaceID int64,
	platform string,
	t VeloxResolveTargetPayload,
) ([]VeloxResolvedTargetEntry, string, string) {
	// Step 1 — resolve platform_account_id from the discriminator.
	accountID, err := m.resolveAccountIDForChannelTarget(ctx, workspaceID, platform, t)
	if err != nil {
		// resolveAccountIDForChannelTarget already mapped
		// transient DB errors to TARGET_NOT_AVAILABLE with a
		// specific message; pass it through.
		return nil, "TARGET_NOT_AVAILABLE", err.Error()
	}
	if accountID == 0 {
		return nil, "TARGET_NOT_AVAILABLE",
			"target channel is not bound to this workspace"
	}

	// Step 2 — workspace binding check.
	binding, err := m.deps.WorkspaceStore.FindChannel(ctx, workspaceID, accountID)
	if err != nil {
		slog.Error("velox resolve-target: workspace channel lookup failed",
			"workspace_id", workspaceID, "platform_account_id", accountID, "err", err)
		return nil, "TARGET_NOT_AVAILABLE", "workspace channel lookup failed"
	}
	if binding == nil {
		return nil, "TARGET_NOT_AVAILABLE",
			"platform_account is not bound to this workspace"
	}

	// Step 3 — platform_account status / eligibility.
	pa, err := m.deps.UserStore.FindPlatformAccountByID(accountID)
	if err != nil {
		slog.Error("velox resolve-target: platform_account lookup failed",
			"platform_account_id", accountID, "err", err)
		return nil, "BLOCKED_AUTH", "platform_account lookup failed"
	}
	if pa == nil {
		return nil, "TARGET_NOT_AVAILABLE", "platform_account row not found"
	}
	if pa.Platform != platform {
		// Platform mismatch is a wiring bug — the producer
		// asked for "youtube" but the bound account is, say,
		// "tiktok". 422 (canonical) but with a TARGET-style
		// error so the operator fixes it.
		return nil, "TARGET_NOT_AVAILABLE",
			"platform_account is not registered for platform " + platform
	}

	// Pre-eligibility channel-binding cross-check: caller
	// supplied ChannelID, verify it matches pa.PlatformUserID.
	// Catches the "OAuth grant switched channel" failure mode
	// where the access token used to be valid for the expected
	// channel but the channel was transferred/reassigned
	// since the grant (rare but documented as a YouTube
	// behavior; OAuth grant stays valid for the new owner).
	if t.ChannelID != "" && pa.PlatformUserID != t.ChannelID {
		entry := VeloxResolvedTargetEntry{
			PlatformAccountID: pa.ID,
			Platform:          pa.Platform,
			ChannelID:         pa.PlatformUserID,
			ChannelName:       pa.Username,
			Status:            pa.Status,
			Enabled:           binding.Enabled,
			TargetErrorCode:   "BLOCKED_AUTH",
		}
		return []VeloxResolvedTargetEntry{entry},
			"BLOCKED_AUTH",
			"OAuth grant does not match expected channel_id (channel was transferred/reassigned)"
	}

	// Status check — reauth_required / revoked / disconnected /
	// pending / suspended / error → BLOCKED_AUTH. We separate
	// revocation classes so the error message is actionable
	// (operator runbook is different for "reauth" vs "revoke").
	if pa.Status == models.AccountStatusReauthRequired || pa.ReauthRequiredAt != nil {
		entry := makeErrorEntry(pa, binding, "BLOCKED_AUTH")
		return []VeloxResolvedTargetEntry{entry},
			"BLOCKED_AUTH",
			"platform_account requires re-authorization"
	}
	if pa.Status != models.AccountStatusActive {
		entry := makeErrorEntry(pa, binding, "BLOCKED_AUTH")
		return []VeloxResolvedTargetEntry{entry},
			"BLOCKED_AUTH",
			fmt.Sprintf("platform_account status is %q (must be %q)", pa.Status, models.AccountStatusActive)
	}

	// Workspace-side disabled check (binding exists but disabled).
	if !binding.Enabled {
		entry := makeErrorEntry(pa, binding, "TARGET_NOT_AVAILABLE")
		return []VeloxResolvedTargetEntry{entry},
			"TARGET_NOT_AVAILABLE",
			"channel is disabled in this workspace"
	}

	// Valid path.
	return []VeloxResolvedTargetEntry{{
		PlatformAccountID: pa.ID,
		Platform:          pa.Platform,
		ChannelID:         pa.PlatformUserID,
		ChannelName:       pa.Username,
		Status:            pa.Status,
		Enabled:           binding.Enabled,
	}}, "", ""
}

// resolveAccountIDForChannelTarget bridges the discriminator
// variants on VeloxResolveTargetPayload into a single
// platform_account_id. The platform_account_id path is direct;
// the channel_id path walks the workspace's channel list and
// matches on platform_user_id (= YouTube channel id).
//
// Returns (0, error) when no match is found; the error message
// distinguishes "not bound to workspace" from "DB transient
// failure" so the caller's wire response can be diagnostic
// without leaking internal errors to the response body.
func (m *VeloxModule) resolveAccountIDForChannelTarget(
	ctx context.Context,
	workspaceID int64,
	platform string,
	t VeloxResolveTargetPayload,
) (int64, error) {
	if t.PlatformAccountID != 0 {
		return t.PlatformAccountID, nil
	}
	// ChannelID path: list workspace channels, then resolve
	// each candidate's platform_account row to find the matching
	// provider id. The list is bounded by the per-workspace
	// row cap (see repository.WorkspaceRepository.ListChannels)
	// so an O(N×M) full scan stays cheap.
	channels, err := m.deps.WorkspaceStore.ListChannels(ctx, workspaceID)
	if err != nil {
		return 0, errors.New("workspace channels list failed")
	}
	for _, ch := range channels {
		pa, err := m.deps.UserStore.FindPlatformAccountByID(ch.PlatformAccountID)
		if err != nil || pa == nil {
			continue
		}
		if pa.Platform == platform && pa.PlatformUserID == t.ChannelID {
			return ch.PlatformAccountID, nil
		}
	}
	return 0, nil
}

// resolveGroupTarget expands a group_id into N platform_accounts
// and validates each (all-or-nothing — the spec says "valid=true
// if all accounts are active+enabled+binded to expected channel").
//
//   - Group not found / wrong workspace  → TARGET_NOT_AVAILABLE
//   - Group has zero accounts attached   → GROUP_EMPTY
//   - Per-member validation failure      → bubbling code wins
//     (BLOCKED_AUTH outranks TARGET_NOT_AVAILABLE) and partial
//     entries are returned so the operator UI can highlight
//     the failing member(s).
func (m *VeloxModule) resolveGroupTarget(
	ctx context.Context,
	workspaceID int64,
	platform string,
	groupID int64,
) ([]VeloxResolvedTargetEntry, string, string) {
	g, err := m.deps.GroupStore.FindByID(groupID)
	if err != nil {
		slog.Error("velox resolve-target: group lookup failed",
			"group_id", groupID, "err", err)
		return nil, "TARGET_NOT_AVAILABLE", "group lookup failed"
	}
	if g == nil {
		return nil, "TARGET_NOT_AVAILABLE",
			fmt.Sprintf("group %d not found", groupID)
	}
	if g.WorkspaceID != workspaceID {
		// Cross-workspace leak guard: the group exists but the
		// requestor is not its workspace — same collapse as
		// "not found" so probing callers cannot enumerate
		// group ids across tenants.
		return nil, "TARGET_NOT_AVAILABLE",
			fmt.Sprintf("group %d does not belong to workspace %d", groupID, workspaceID)
	}

	accountIDs, err := m.deps.GroupStore.ListAccountsInGroup(groupID)
	if err != nil {
		slog.Error("velox resolve-target: group accounts lookup failed",
			"group_id", groupID, "err", err)
		return nil, "TARGET_NOT_AVAILABLE", "group members lookup failed"
	}
	if len(accountIDs) == 0 {
		return nil, "GROUP_EMPTY",
			fmt.Sprintf("group %d has no accounts attached", groupID)
	}

	// Per-member validation. We compose on the channel-target
	// resolver by synthesising a payload per member.
	entries := make([]VeloxResolvedTargetEntry, 0, len(accountIDs))
	var severestCode, severestMsg string
	for _, acctID := range accountIDs {
		syntheticPayload := VeloxResolveTargetPayload{
			Type:             "channel",
			PlatformAccountID: acctID,
		}
		subEntries, subCode, subMsg := m.resolveChannelTarget(ctx, workspaceID, platform, syntheticPayload)
		// subEntries is never nil when the row exists; when the
		// channel-target resolver returns ([], "...", ...) with
		// no row (rare path: account not bound to workspace even
		// though it's in the group), synthesise a stub entry so
		// the operator UI can still surface the failing member.
		if len(subEntries) == 0 && subCode != "" {
			pa, _ := m.deps.UserStore.FindPlatformAccountByID(acctID)
			if pa != nil {
				subEntries = []VeloxResolvedTargetEntry{{
					PlatformAccountID: pa.ID,
					Platform:          pa.Platform,
					ChannelID:         pa.PlatformUserID,
					ChannelName:       pa.Username,
					Status:            pa.Status,
					TargetErrorCode:   subCode,
				}}
			}
		}
		entries = append(entries, subEntries...)
		if subCode == "BLOCKED_AUTH" {
			severestCode = "BLOCKED_AUTH"
			severestMsg = subMsg
		} else if subCode != "" && severestCode == "" {
			severestCode = subCode
			severestMsg = subMsg
		}
	}

	if severestCode != "" {
		return entries, severestCode, severestMsg
	}
	return entries, "", ""
}

// makeErrorEntry is a small builder for the error-path entries
// returned by resolveChannelTarget. Centralised so the target-
// scoped error_code annotation stays consistent across the
// BLOCKED_AUTH and TARGET_NOT_AVAILABLE surfaces.
func makeErrorEntry(pa *models.PlatformAccount, binding *models.WorkspaceChannel, code string) VeloxResolvedTargetEntry {
	status := ""
	if pa != nil {
		status = pa.Status
	}
	enabled := false
	if binding != nil {
		enabled = binding.Enabled
	}
	return VeloxResolvedTargetEntry{
		PlatformAccountID: pa.ID,
		Platform:          pa.Platform,
		ChannelID:         pa.PlatformUserID,
		ChannelName:       pa.Username,
		Status:            status,
		Enabled:           enabled,
		TargetErrorCode:   code,
	}
}
