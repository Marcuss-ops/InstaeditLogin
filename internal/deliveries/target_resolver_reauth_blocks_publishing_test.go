package deliveries

import (
	"context"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// TestTargetResolver_ReauthRequired_BlocksPublishing pins the catalog-side
// behavior the Velox → InstaeditLogin publishing flow depends on: when a
// channel transitions to reauth_required (or is disabled / revoked /
// disconnected), the next ListWorkspaceTargets call MUST surface:
//
//   - target_error_code = "BLOCKED_AUTH"           (reauth) or "TARGET_NOT_AVAILABLE" (disabled/revoked)
//   - enabled              = false                 (binding row disabled)
//   - can_post surface     = all-zero capabilities (Velox socialclient maps these to can_post=false)
//
// A regression in checkAccountEligibility or CapabilitiesForTarget would
// silently let Velox route a job to a channel that the operator has
// disabled or whose OAuth grant has been revoked. The Velox handler at
// publishing_targets.go:131 reads target.CanPost to drive
// delivery_destinations.enabled — if the InstaEdit catalog returns
// can_post=true for a reauth row, Velox will keep the destination enabled
// and the dispatcher will fail at upload time.
//
// The 4 conditions from the publish-ready happy-path test are inverted:
// the channel is no longer publishable, and the catalog response must
// reflect that. This test exercises all three rejection sources
// (reauth_required enum, reauth_required_at timestamp, disabled binding,
// revoked/disconnected enum) so a future refactor that drops any one
// of them surfaces here.
func TestTargetResolver_ReauthRequired_BlocksPublishing(t *testing.T) {
	const (
		workspaceID       int64 = 42
		platformAccountID int64 = 381
		extDestinationID        = "extdst_01JABC_reauth"
		channelID               = "UC_reauth_42"
		channelUsername         = "Reauth Channel"
	)

	// Fixture builder: returns a fresh resolver with the given platform_account
	// and binding state so each sub-test is isolated.
	newReauthFixture := func(t *testing.T, pa *models.PlatformAccount, bindingEnabled bool) *TargetResolver {
		t.Helper()
		return NewTargetResolver(TargetResolverDeps{
			DestinationStore: &catalogDestinationStore{destinations: []models.ExternalDestination{{
				ID:                extDestinationID,
				SourceSystem:      "velox",
				WorkspaceID:       workspaceID,
				PlatformAccountID: platformAccountID,
				Enabled:           true, // cond 4 still healthy
			}}},
			WorkspaceStore: &catalogWorkspaceStore{
				workspace: &models.Workspace{ID: workspaceID, Name: "Editorial"},
				channels: []models.WorkspaceChannel{{
					WorkspaceID:      workspaceID,
					PlatformAccountID: platformAccountID,
					Enabled:          bindingEnabled,
				}},
			},
			UserStore: &catalogUserStore{accounts: map[int64]*models.PlatformAccount{
				platformAccountID: pa,
			}},
		})
	}

	activeAccount := &models.PlatformAccount{
		ID:               platformAccountID,
		Platform:         models.PlatformYouTube,
		PlatformUserID:   channelID,
		Username:         channelUsername,
		Status:           models.AccountStatusActive,
		ReauthRequiredAt: nil,
	}

	cases := []struct {
		name              string
		pa                *models.PlatformAccount
		bindingEnabled    bool
		wantErrorCode     string
		wantEnabled       bool // catalog.Enabled (binding-level)
		wantCapabilities  bool // any of upload_video / set_thumbnail / publish / schedule
	}{
		{
			name:             "reauth_required_via_status_enum_blocks_publishing",
			pa:               clonePlatformAccount(activeAccount, func(p *models.PlatformAccount) { p.Status = models.AccountStatusReauthRequired }),
			bindingEnabled:   true,
			wantErrorCode:    ErrCodeBlockedAuth,
			wantEnabled:      true, // binding still enabled; the BLOCK comes from status
			wantCapabilities: false,
		},
		{
			name:             "reauth_required_via_timestamp_blocks_publishing",
			pa:               clonePlatformAccount(activeAccount, func(p *models.PlatformAccount) {
				now := time.Now().UTC()
				p.ReauthRequiredAt = &now
				// status stays 'active' but the dual-signal gate flips on the timestamp
			}),
			bindingEnabled:   true,
			wantErrorCode:    ErrCodeBlockedAuth,
			wantEnabled:      true,
			wantCapabilities: false,
		},
		{
			name:             "revoked_status_blocks_publishing",
			pa:               clonePlatformAccount(activeAccount, func(p *models.PlatformAccount) { p.Status = models.AccountStatusRevoked }),
			bindingEnabled:   true,
			wantErrorCode:    ErrCodeTargetNotAvailable,
			wantEnabled:      true,
			wantCapabilities: false,
		},
		{
			name:             "disconnected_status_blocks_publishing",
			pa:               clonePlatformAccount(activeAccount, func(p *models.PlatformAccount) { p.Status = models.AccountStatusDisconnected }),
			bindingEnabled:   true,
			wantErrorCode:    ErrCodeTargetNotAvailable,
			wantEnabled:      true,
			wantCapabilities: false,
		},
		{
			name:             "disabled_binding_blocks_publishing",
			pa:               activeAccount,
			bindingEnabled:   false, // operator flipped the workspace binding off
			wantErrorCode:    ErrCodeTargetNotAvailable,
			wantEnabled:      false, // binding row's Enabled field reflects this
			wantCapabilities: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resolver := newReauthFixture(t, tc.pa, tc.bindingEnabled)
			ctx := context.Background()

			catalog, err := resolver.ListWorkspaceTargets(ctx, workspaceID, models.PlatformYouTube)
			if err != nil {
				t.Fatalf("ListWorkspaceTargets returned error: %v", err)
			}
			if len(catalog) != 1 {
				t.Fatalf("catalog: want 1 entry; got %d", len(catalog))
			}
			entry := catalog[0]
			if entry.PlatformAccountID != platformAccountID {
				t.Fatalf("catalog.PlatformAccountID: want %d; got %d", platformAccountID, entry.PlatformAccountID)
			}

			// Pin the catalog verdict: the catalog response MUST carry the
			// error code so Velox's socialclient can map it to can_post=false
			// via the CapabilitiesForTarget gate below.
			if entry.TargetErrorCode != tc.wantErrorCode {
				t.Fatalf("catalog.TargetErrorCode: want %q; got %q (Velox depends on this exact verdict to set can_post=false)",
					tc.wantErrorCode, entry.TargetErrorCode)
			}

			// Pin the binding-level Enabled flag (separate from can_post).
			// The Velox handler reads target.CanPost from the socialclient
			// response, which is derived from capabilities — see the next
			// assertion — but the binding-level Enabled is what
			// ListWorkspaceTargets stamps on the catalog entry.
			if entry.Enabled != tc.wantEnabled {
				t.Fatalf("catalog.Enabled: want %v; got %v", tc.wantEnabled, entry.Enabled)
			}

			// Pin the capabilities surface: any non-empty capability would mean
			// the Velox socialclient maps the entry to can_post=true and the
			// publisher routes a job to a disabled channel.
			caps := CapabilitiesForTarget(entry.ResolvedTargetEntry)
			hasAnyCap := caps.UploadVideo || caps.SetThumbnail || caps.Publish || caps.Schedule
			if hasAnyCap != tc.wantCapabilities {
				t.Fatalf("CapabilitiesForTarget: want any-cap=%v (so Velox can_post maps to false); got upload_video=%v set_thumbnail=%v publish=%v schedule=%v",
					tc.wantCapabilities, caps.UploadVideo, caps.SetThumbnail, caps.Publish, caps.Schedule)
			}
		})
	}
}

// clonePlatformAccount returns a deep-enough copy of pa (only the
// fields the resolver reads) with the given mutator applied. This keeps
// the per-sub-test fixtures isolated — without the copy, each sub-test
// would mutate the shared activeAccount fixture and bleed into the next.
func clonePlatformAccount(pa *models.PlatformAccount, mutate func(*models.PlatformAccount)) *models.PlatformAccount {
	out := *pa // shallow copy of value-type fields
	if pa.ReauthRequiredAt != nil {
		ts := *pa.ReauthRequiredAt
		out.ReauthRequiredAt = &ts
	}
	if mutate != nil {
		mutate(&out)
	}
	return &out
}
