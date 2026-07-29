package deliveries

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// ListWorkspaceTargets returns every channel bound to a workspace for the
// requested platform. It deliberately reuses checkAccountEligibility so the
// catalog, resolve-target and saved-destination validation cannot drift.
//
// The returned slice includes blocked channels as well as publishable ones.
// Callers inspect TargetErrorCode to decide whether a channel can currently be
// selected. Results are sorted by platform_account_id for deterministic UIs,
// payload generation and smoke tests.
func (r *TargetResolver) ListWorkspaceTargets(ctx context.Context, workspaceID int64, platform string) ([]ResolvedTargetEntry, error) {
	if workspaceID <= 0 {
		return nil, errors.New("target catalog: workspace_id must be positive")
	}
	if platform == "" {
		return nil, errors.New("target catalog: platform is required")
	}
	if r == nil {
		return nil, errors.New("target catalog: resolver is nil")
	}
	if r.deps.WorkspaceStore == nil {
		return nil, errors.New("target catalog: WorkspaceStore not wired")
	}
	if r.deps.UserStore == nil {
		return nil, errors.New("target catalog: UserStore not wired")
	}

	workspace, err := r.deps.WorkspaceStore.FindByID(workspaceID)
	if err != nil {
		return nil, fmt.Errorf("target catalog: workspace lookup failed: %w", err)
	}
	if workspace == nil {
		return []ResolvedTargetEntry{}, nil
	}

	bindings, err := r.deps.WorkspaceStore.ListChannels(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("target catalog: workspace channel list failed: %w", err)
	}

	entries := make([]ResolvedTargetEntry, 0, len(bindings))
	for i := range bindings {
		binding := bindings[i]
		account, accountErr := r.deps.UserStore.FindPlatformAccountByID(binding.PlatformAccountID)
		if accountErr != nil {
			return nil, fmt.Errorf("target catalog: platform_account %d lookup failed: %w", binding.PlatformAccountID, accountErr)
		}
		if account == nil {
			entries = append(entries, ResolvedTargetEntry{
				PlatformAccountID: binding.PlatformAccountID,
				Platform:          platform,
				Status:            "missing",
				Enabled:           binding.Enabled,
				TargetErrorCode:   ErrCodeTargetNotAvailable,
			})
			continue
		}
		if account.Platform != platform {
			continue
		}

		entry, eligibility := checkAccountEligibility(account, &binding)
		if !eligibility.Valid {
			entry.TargetErrorCode = eligibility.ErrorCode
		}
		entries = append(entries, entry)
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].PlatformAccountID < entries[j].PlatformAccountID
	})
	return entries, nil
}

// PublishingCapabilities is intentionally small and platform-neutral. New
// capabilities must be added here and resolved centrally rather than being
// recomputed independently by API handlers or clients.
type PublishingCapabilities struct {
	UploadVideo  bool `json:"upload_video"`
	SetThumbnail bool `json:"set_thumbnail"`
	Publish      bool `json:"publish"`
	Schedule     bool `json:"schedule"`
}

// CapabilitiesForTarget derives the publishing surface from the same resolved
// target row used by delivery validation. Today YouTube supports the complete
// private-upload -> thumbnail -> publish/schedule flow. A blocked target has no
// active capability, even if the provider would normally support it.
func CapabilitiesForTarget(entry ResolvedTargetEntry) PublishingCapabilities {
	eligible := entry.TargetErrorCode == "" && entry.Enabled && entry.Status == models.AccountStatusActive
	if !eligible {
		return PublishingCapabilities{}
	}
	if entry.Platform == models.PlatformYouTube {
		return PublishingCapabilities{
			UploadVideo:  true,
			SetThumbnail: true,
			Publish:      true,
			Schedule:     true,
		}
	}
	return PublishingCapabilities{}
}
