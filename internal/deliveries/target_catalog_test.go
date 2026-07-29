package deliveries

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

type catalogWorkspaceStore struct {
	workspace *models.Workspace
	channels  []models.WorkspaceChannel
}

func (s *catalogWorkspaceStore) FindByID(id int64) (*models.Workspace, error) {
	if s.workspace == nil || s.workspace.ID != id {
		return nil, nil
	}
	return s.workspace, nil
}

func (s *catalogWorkspaceStore) FindChannel(_ context.Context, workspaceID, platformAccountID int64) (*models.WorkspaceChannel, error) {
	for i := range s.channels {
		channel := s.channels[i]
		if channel.WorkspaceID == workspaceID && channel.PlatformAccountID == platformAccountID {
			return &channel, nil
		}
	}
	return nil, nil
}

func (s *catalogWorkspaceStore) ListChannels(_ context.Context, workspaceID int64) ([]models.WorkspaceChannel, error) {
	out := make([]models.WorkspaceChannel, 0, len(s.channels))
	for _, channel := range s.channels {
		if channel.WorkspaceID == workspaceID {
			out = append(out, channel)
		}
	}
	return out, nil
}

type catalogUserStore struct {
	accounts map[int64]*models.PlatformAccount
}

func (s *catalogUserStore) FindPlatformAccountByID(id int64) (*models.PlatformAccount, error) {
	return s.accounts[id], nil
}

type catalogDestinationStore struct {
	destinations []models.ExternalDestination
}

func (s *catalogDestinationStore) GetByID(_ context.Context, id string) (*models.ExternalDestination, error) {
	for i := range s.destinations {
		if s.destinations[i].ID == id {
			return &s.destinations[i], nil
		}
	}
	return nil, nil
}

func (s *catalogDestinationStore) ListByWorkspace(_ context.Context, workspaceID int64, enabledOnly bool) ([]models.ExternalDestination, error) {
	out := make([]models.ExternalDestination, 0, len(s.destinations))
	for _, destination := range s.destinations {
		if destination.WorkspaceID != workspaceID || enabledOnly && !destination.Enabled {
			continue
		}
		out = append(out, destination)
	}
	return out, nil
}

func TestListWorkspaceTargetsUsesCanonicalEligibility(t *testing.T) {
	resolver := NewTargetResolver(TargetResolverDeps{
		DestinationStore: &catalogDestinationStore{destinations: []models.ExternalDestination{
			{ID: "extdst_ready", SourceSystem: "velox", WorkspaceID: 12, PlatformAccountID: 10, Enabled: true},
			{ID: "extdst_disabled", SourceSystem: "velox", WorkspaceID: 12, PlatformAccountID: 20, Enabled: true},
			{ID: "extdst_reauth", SourceSystem: "velox", WorkspaceID: 12, PlatformAccountID: 30, Enabled: true},
		}},
		WorkspaceStore: &catalogWorkspaceStore{
			workspace: &models.Workspace{ID: 12, Name: "Editorial"},
			channels: []models.WorkspaceChannel{
				{WorkspaceID: 12, PlatformAccountID: 20, Enabled: false},
				{WorkspaceID: 12, PlatformAccountID: 10, Enabled: true},
				{WorkspaceID: 12, PlatformAccountID: 30, Enabled: true},
			},
		},
		UserStore: &catalogUserStore{accounts: map[int64]*models.PlatformAccount{
			10: {ID: 10, Platform: models.PlatformYouTube, PlatformUserID: "UC10", Username: "Ready", Status: models.AccountStatusActive},
			20: {ID: 20, Platform: models.PlatformYouTube, PlatformUserID: "UC20", Username: "Disabled", Status: models.AccountStatusActive},
			30: {ID: 30, Platform: models.PlatformYouTube, PlatformUserID: "UC30", Username: "Reauth", Status: models.AccountStatusReauthRequired},
		}},
	})

	entries, err := resolver.ListWorkspaceTargets(context.Background(), 12, models.PlatformYouTube)
	if err != nil {
		t.Fatalf("ListWorkspaceTargets returned error: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
	if entries[0].PlatformAccountID != 10 || entries[1].PlatformAccountID != 20 || entries[2].PlatformAccountID != 30 {
		t.Fatalf("catalog is not deterministically sorted: %#v", entries)
	}
	if entries[0].TargetErrorCode != "" || entries[0].ExternalDestinationID != "extdst_ready" {
		t.Fatalf("eligible channel binding invalid: %#v", entries[0])
	}
	if entries[1].TargetErrorCode != ErrCodeTargetNotAvailable {
		t.Fatalf("disabled channel code = %q", entries[1].TargetErrorCode)
	}
	if entries[2].TargetErrorCode != ErrCodeBlockedAuth {
		t.Fatalf("reauth channel code = %q", entries[2].TargetErrorCode)
	}
}

func TestListWorkspaceTargetsBlocksMissingDestination(t *testing.T) {
	resolver := NewTargetResolver(TargetResolverDeps{
		DestinationStore: &catalogDestinationStore{},
		WorkspaceStore: &catalogWorkspaceStore{
			workspace: &models.Workspace{ID: 12},
			channels:  []models.WorkspaceChannel{{WorkspaceID: 12, PlatformAccountID: 10, Enabled: true}},
		},
		UserStore: &catalogUserStore{accounts: map[int64]*models.PlatformAccount{
			10: {ID: 10, Platform: models.PlatformYouTube, PlatformUserID: "UC10", Status: models.AccountStatusActive},
		}},
	})

	entries, err := resolver.ListWorkspaceTargets(context.Background(), 12, models.PlatformYouTube)
	if err != nil {
		t.Fatalf("ListWorkspaceTargets returned error: %v", err)
	}
	if len(entries) != 1 || entries[0].TargetErrorCode != ErrCodeTargetNotAvailable || entries[0].ExternalDestinationID != "" {
		t.Fatalf("missing destination was not blocked: %#v", entries)
	}
}

func TestCapabilitiesForTarget(t *testing.T) {
	ready := ResolvedTargetEntry{
		Platform: models.PlatformYouTube,
		Status:   models.AccountStatusActive,
		Enabled:  true,
	}
	capabilities := CapabilitiesForTarget(ready)
	if !capabilities.UploadVideo || !capabilities.SetThumbnail || !capabilities.Publish || !capabilities.Schedule {
		t.Fatalf("eligible YouTube capabilities incomplete: %#v", capabilities)
	}

	ready.TargetErrorCode = ErrCodeBlockedAuth
	blocked := CapabilitiesForTarget(ready)
	if blocked.UploadVideo || blocked.SetThumbnail || blocked.Publish || blocked.Schedule {
		t.Fatalf("blocked target exposed capabilities: %#v", blocked)
	}
}
