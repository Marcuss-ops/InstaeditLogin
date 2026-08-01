package deliveries

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// and validates each.
func (r *TargetResolver) resolveGroupTarget(ctx context.Context, req ResolveRequest) (*ResolveResult, error) {
	if r.deps.GroupStore == nil {
		return nil, errors.New("target resolver: GroupStore not wired")
	}

	groupID := req.Target.GroupID

	g, err := r.deps.GroupStore.FindByID(groupID)
	if err != nil {
		slog.Error("target resolver: group lookup failed", "group_id", groupID, "err", err)
		return &ResolveResult{
			Valid:           false,
			ErrorCode:       ErrCodeTargetNotAvailable,
			Message:         "group lookup failed",
			ResolvedTargets: []ResolvedTargetEntry{},
		}, nil
	}
	if g == nil {
		return &ResolveResult{
			Valid:           false,
			ErrorCode:       ErrCodeTargetNotAvailable,
			Message:         fmt.Sprintf("group %d not found", groupID),
			ResolvedTargets: []ResolvedTargetEntry{},
		}, nil
	}
	if g.WorkspaceID != req.WorkspaceID {
		return &ResolveResult{
			Valid:           false,
			ErrorCode:       ErrCodeTargetNotAvailable,
			Message:         fmt.Sprintf("group %d does not belong to workspace %d", groupID, req.WorkspaceID),
			ResolvedTargets: []ResolvedTargetEntry{},
		}, nil
	}

	accountIDs, err := r.deps.GroupStore.ListAccountsInGroup(groupID)
	if err != nil {
		slog.Error("target resolver: group accounts lookup failed", "group_id", groupID, "err", err)
		return &ResolveResult{
			Valid:           false,
			ErrorCode:       ErrCodeTargetNotAvailable,
			Message:         "group members lookup failed",
			ResolvedTargets: []ResolvedTargetEntry{},
		}, nil
	}
	if len(accountIDs) == 0 {
		return &ResolveResult{
			Valid:           false,
			ErrorCode:       ErrCodeGroupEmpty,
			Message:         fmt.Sprintf("group %d has no accounts attached", groupID),
			ResolvedTargets: []ResolvedTargetEntry{},
		}, nil
	}

	// Per-member validation via the channel-target resolver.
	entries := make([]ResolvedTargetEntry, 0, len(accountIDs))
	var severestCode, severestMsg string
	for _, acctID := range accountIDs {
		subReq := ResolveRequest{
			WorkspaceID: req.WorkspaceID,
			Platform:    req.Platform,
			Target: TargetDescriptor{
				Type:              "channel",
				PlatformAccountID: acctID,
			},
		}
		subResult, subErr := r.resolveChannelTarget(ctx, subReq)
		if subErr != nil {
			return nil, subErr
		}
		if len(subResult.ResolvedTargets) == 0 && !subResult.Valid {
			// Synthesise a stub entry for visibility.
			pa, _ := r.deps.UserStore.FindPlatformAccountByID(acctID)
			if pa != nil {
				entries = append(entries, ResolvedTargetEntry{
					PlatformAccountID: pa.ID,
					Platform:          pa.Platform,
					ChannelID:         pa.PlatformUserID,
					ChannelName:       pa.Username,
					Status:            pa.Status,
					TargetErrorCode:   subResult.ErrorCode,
				})
			}
		} else {
			entries = append(entries, subResult.ResolvedTargets...)
		}
		// Bubble the severest code.
		if subResult.ErrorCode == ErrCodeBlockedAuth {
			severestCode = ErrCodeBlockedAuth
			severestMsg = subResult.Message
		} else if subResult.ErrorCode != "" && severestCode == "" {
			severestCode = subResult.ErrorCode
			severestMsg = subResult.Message
		}
	}

	if severestCode != "" {
		return &ResolveResult{
			Valid:           false,
			ErrorCode:       severestCode,
			Message:         severestMsg,
			ResolvedTargets: entries,
		}, nil
	}
	return &ResolveResult{
		Valid:           true,
		DestinationID:   "instaedit_" + req.Platform,
		Platform:        req.Platform,
		ResolvedTargets: entries,
	}, nil
}

// eligibilityOutcome is the internal carrier for the shared
// account-eligibility check result.
type eligibilityOutcome struct {
	Valid     bool
	ErrorCode string
	Message   string
}

// checkAccountEligibility is the SINGLE canonical eligibility gate
// shared by both the SavedDestination AND DirectTarget paths.
//
// Checks performed (in order, most severe first):
//  1. Reauth required (status enum OR reauth_required_at timestamp).
//  2. Revoked / disconnected (explicit operator termination).
//  3. Status must be "active".
//  4. Binding disabled (workspace-side soft-disable).
//
// Returns the ResolvedTargetEntry on both valid and invalid paths
// so the caller always has a row to surface in resolved_targets.
func checkAccountEligibility(pa *models.PlatformAccount, binding *models.WorkspaceChannel) (ResolvedTargetEntry, eligibilityOutcome) {
	entry := makeResolvedEntry(pa, binding, "")

	// 1. Reauth required (dual signal — status enum OR timestamp).
	if pa.Status == models.AccountStatusReauthRequired || pa.ReauthRequiredAt != nil {
		entry.TargetErrorCode = ErrCodeBlockedAuth
		return entry, eligibilityOutcome{
			Valid:     false,
			ErrorCode: ErrCodeBlockedAuth,
			Message:   "platform_account requires re-authorization",
		}
	}

	// 2. Revoked / disconnected — explicit operator termination.
	if pa.Status == models.AccountStatusRevoked || pa.Status == models.AccountStatusDisconnected {
		entry.TargetErrorCode = ErrCodeTargetNotAvailable
		return entry, eligibilityOutcome{
			Valid:     false,
			ErrorCode: ErrCodeTargetNotAvailable,
			Message:   fmt.Sprintf("platform_account status is %q (terminated)", pa.Status),
		}
	}

	// 3. Must be active.
	if pa.Status != models.AccountStatusActive {
		entry.TargetErrorCode = ErrCodeBlockedAuth
		return entry, eligibilityOutcome{
			Valid:     false,
			ErrorCode: ErrCodeBlockedAuth,
			Message:   fmt.Sprintf("platform_account status is %q (must be %q)", pa.Status, models.AccountStatusActive),
		}
	}

	// 4. Workspace-side disabled check.
	if binding != nil && !binding.Enabled {
		entry.TargetErrorCode = ErrCodeTargetNotAvailable
		return entry, eligibilityOutcome{
			Valid:     false,
			ErrorCode: ErrCodeTargetNotAvailable,
			Message:   "channel is disabled in this workspace",
		}
	}

	return entry, eligibilityOutcome{Valid: true}
}

// makeResolvedEntry is a small builder for ResolvedTargetEntry.
func makeResolvedEntry(pa *models.PlatformAccount, binding *models.WorkspaceChannel, code string) ResolvedTargetEntry {
	status := ""
	if pa != nil {
		status = pa.Status
	}
	enabled := false
	if binding != nil {
		enabled = binding.Enabled
	}
	return ResolvedTargetEntry{
		PlatformAccountID: pa.ID,
		Platform:          pa.Platform,
		ChannelID:         pa.PlatformUserID,
		ChannelName:       pa.Username,
		Status:            status,
		Enabled:           enabled,
		TargetErrorCode:   code,
	}
}
