package deliveries

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// TargetCatalogDestinationStore is the optional read surface required by the
// discovery endpoint. It remains separate from TargetDestinationStore so
// existing resolve-target tests and adapters that only need GetByID do not grow
// an unrelated dependency.
type TargetCatalogDestinationStore interface {
	ListByWorkspace(ctx context.Context, workspaceID int64, enabledOnly bool) ([]models.ExternalDestination, error)
}

// CatalogTargetEntry joins one resolved channel with the opaque destination ID
// Velox must use for delivery. The social destination remains authoritative;
// names, platform_account_id and channel_id are discovery/display data only.
type CatalogTargetEntry struct {
	ResolvedTargetEntry
	ExternalDestinationID string `json:"external_destination_id,omitempty"`
}

// ListWorkspaceTargets returns every channel bound to a workspace for the
// requested platform and joins it to an enabled Velox external destination.
// It reuses checkAccountEligibility so catalog discovery, resolve-target and
// saved-destination validation cannot drift.
func (r *TargetResolver) ListWorkspaceTargets(ctx context.Context, workspaceID int64, platform string) ([]CatalogTargetEntry, error) {
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
		return []CatalogTargetEntry{}, nil
	}

	bindings, err := r.deps.WorkspaceStore.ListChannels(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("target catalog: workspace channel list failed: %w", err)
	}

	// Build one deterministic opaque destination per platform account. Only
	// enabled source_system=velox rows are eligible. When duplicates exist, the
	// lexicographically smallest ID wins so retries and UIs are stable.
	destinationByAccount := map[int64]string{}
	if catalogStore, ok := r.deps.DestinationStore.(TargetCatalogDestinationStore); ok {
		destinations, listErr := catalogStore.ListByWorkspace(ctx, workspaceID, true)
		if listErr != nil {
			return nil, fmt.Errorf("target catalog: external destination list failed: %w", listErr)
		}
		sort.Slice(destinations, func(i, j int) bool { return destinations[i].ID < destinations[j].ID })
		for _, destination := range destinations {
			if !destination.Enabled || destination.SourceSystem != "velox" || destination.PlatformAccountID <= 0 {
				continue
			}
			if _, exists := destinationByAccount[destination.PlatformAccountID]; !exists {
				destinationByAccount[destination.PlatformAccountID] = destination.ID
			}
		}
	}

	entries := make([]CatalogTargetEntry, 0, len(bindings))
	for i := range bindings {
		binding := bindings[i]
		account, accountErr := r.deps.UserStore.FindPlatformAccountByID(binding.PlatformAccountID)
		if accountErr != nil {
			return nil, fmt.Errorf("target catalog: platform_account %d lookup failed: %w", binding.PlatformAccountID, accountErr)
		}
		if account == nil {
			entries = append(entries, CatalogTargetEntry{ResolvedTargetEntry: ResolvedTargetEntry{
				PlatformAccountID: binding.PlatformAccountID,
				Platform:          platform,
				Status:            "missing",
				Enabled:           binding.Enabled,
				TargetErrorCode:   ErrCodeTargetNotAvailable,
			}})
			continue
		}
		if account.Platform != platform {
			continue
		}

		entry, eligibility := checkAccountEligibility(account, &binding)
		if !eligibility.Valid {
			entry.TargetErrorCode = eligibility.ErrorCode
		}
		externalDestinationID := destinationByAccount[account.ID]
		if eligibility.Valid && externalDestinationID == "" {
			entry.TargetErrorCode = ErrCodeTargetNotAvailable
		}
		entries = append(entries, CatalogTargetEntry{
			ResolvedTargetEntry:   entry,
			ExternalDestinationID: externalDestinationID,
		})
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
