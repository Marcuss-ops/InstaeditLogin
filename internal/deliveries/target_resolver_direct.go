package deliveries

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
)

// resolveChannelTarget validates one channel target. Returns the
// resolved entry on success, or an error result on failure.
func (r *TargetResolver) resolveChannelTarget(ctx context.Context, req ResolveRequest) (*ResolveResult, error) {
	t := req.Target

	// Step 1 — resolve platform_account_id from the discriminator.
	accountID, err := r.resolveAccountIDForChannel(ctx, req.WorkspaceID, req.Platform, t)
	if err != nil {
		return &ResolveResult{
			Valid:           false,
			ErrorCode:       ErrCodeTargetNotAvailable,
			Message:         err.Error(),
			ResolvedTargets: []ResolvedTargetEntry{},
		}, nil
	}
	if accountID == 0 {
		return &ResolveResult{
			Valid:           false,
			ErrorCode:       ErrCodeTargetNotAvailable,
			Message:         "target channel is not bound to this workspace",
			ResolvedTargets: []ResolvedTargetEntry{},
		}, nil
	}

	// Step 2 — workspace binding check.
	binding, err := r.deps.WorkspaceStore.FindChannel(ctx, req.WorkspaceID, accountID)
	if err != nil {
		slog.Error("target resolver: workspace channel lookup failed",
			"workspace_id", req.WorkspaceID, "platform_account_id", accountID, "err", err)
		return nil, fmt.Errorf("workspace channel lookup failed: %w", err)
	}
	if binding == nil {
		return &ResolveResult{
			Valid:           false,
			ErrorCode:       ErrCodeTargetNotAvailable,
			Message:         "platform_account is not bound to this workspace",
			ResolvedTargets: []ResolvedTargetEntry{},
		}, nil
	}

	// Step 3 — platform_account lookup + eligibility.
	pa, err := r.deps.UserStore.FindPlatformAccountByID(accountID)
	if err != nil {
		slog.Error("target resolver: platform_account lookup failed",
			"platform_account_id", accountID, "err", err)
		// A store failure is INFRA, not a domain outcome: it must map
		// to 500 at the boundary, not to a domain "blocked_auth" 422
		// that tells the user to re-authorize a healthy channel.
		// Only a nil row (missing account) is a domain result.
		return nil, fmt.Errorf("target resolver: platform_account lookup failed: %w", err)
	}
	if pa == nil {
		return &ResolveResult{
			Valid:           false,
			ErrorCode:       ErrCodeTargetNotAvailable,
			Message:         "platform_account row not found",
			ResolvedTargets: []ResolvedTargetEntry{},
		}, nil
	}
	if pa.Platform != req.Platform {
		return &ResolveResult{
			Valid:           false,
			ErrorCode:       ErrCodeTargetNotAvailable,
			Message:         "platform_account is not registered for platform " + req.Platform,
			ResolvedTargets: []ResolvedTargetEntry{},
		}, nil
	}

	// ChannelID cross-check (catches OAuth-grant-switched-channel).
	if t.ChannelID != "" && pa.PlatformUserID != t.ChannelID {
		entry := makeResolvedEntry(pa, binding, ErrCodeBlockedAuth)
		return &ResolveResult{
			Valid:           false,
			ErrorCode:       ErrCodeBlockedAuth,
			Message:         "OAuth grant does not match expected channel_id (channel was transferred/reassigned)",
			ResolvedTargets: []ResolvedTargetEntry{entry},
		}, nil
	}

	// Shared eligibility check.
	entry, eligibility := checkAccountEligibility(pa, binding)
	if !eligibility.Valid {
		return &ResolveResult{
			Valid:           false,
			ErrorCode:       eligibility.ErrorCode,
			Message:         eligibility.Message,
			ResolvedTargets: []ResolvedTargetEntry{entry},
		}, nil
	}

	return &ResolveResult{
		Valid:           true,
		DestinationID:   "instaedit_" + req.Platform,
		Platform:        req.Platform,
		ResolvedTargets: []ResolvedTargetEntry{entry},
	}, nil
}

// resolveAccountIDForChannel bridges the discriminator variants
// (PlatformAccountID direct OR ChannelID via workspace channel list).
func (r *TargetResolver) resolveAccountIDForChannel(
	ctx context.Context,
	workspaceID int64,
	platform string,
	t TargetDescriptor,
) (int64, error) {
	if t.PlatformAccountID != 0 {
		return t.PlatformAccountID, nil
	}
	// ChannelID path: walk workspace channels.
	channels, err := r.deps.WorkspaceStore.ListChannels(ctx, workspaceID)
	if err != nil {
		return 0, errors.New("workspace channels list failed")
	}
	for _, ch := range channels {
		pa, err := r.deps.UserStore.FindPlatformAccountByID(ch.PlatformAccountID)
		if err != nil || pa == nil {
			continue
		}
		if pa.Platform != platform {
			continue
		}
		if t.ChannelID != "" && pa.PlatformUserID == t.ChannelID {
			return ch.PlatformAccountID, nil
		}
		if t.ChannelName != "" && strings.EqualFold(strings.TrimSpace(pa.Username), strings.TrimSpace(t.ChannelName)) {
			return ch.PlatformAccountID, nil
		}
	}
	return 0, nil
}
