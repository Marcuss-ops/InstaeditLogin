package deliveries

import (
	"context"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// TestTargetResolver_PublishReady_AllFourConditionsMet is the canonical
// publish-readiness happy-path pin for the four operator-facing
// conditions a YouTube channel must satisfy before Velox can route
// a delivery through it. It exercises the unified TargetResolver
// (resolveChannelTarget + ListWorkspaceTargets + CapabilitiesForTarget)
// against a single fixture where ALL four conditions hold:
//
//  1. WORKSPACE BINDING ENABLED — workspace_channels row exists with
//     the (workspace_id, platform_account_id) tuple and enabled=true.
//  2. PLATFORM ACCOUNT ACTIVE — platform_accounts.status='active'
//     AND reauth_required_at IS NULL. The resolver-side check is the
//     canonical gate (checkAccountEligibility step 1+3 in
//     target_resolver.go).
//  3. OAUTH VALID — at the resolver boundary, "OAuth valid" is
//     carried indirectly via platform_accounts.status='active' (the
//     status enum is updated by OAuth callbacks:
//     internal/services/youtube_validate.go flips to reauth_required
//     on invalid_grant, so an active account is the resolver-side
//     proxy for "OAuth not revoked / not expired"). The actual
//     token-freshness check (oauth_connections.expires_at > NOW() +
//     granted_scopes verification) is enforced at the WORKER
//     boundary — internal/services/youtube_validate.go +
//     internal/worker/publish_worker.go — NOT at the resolver. This
//     test pins the resolver contract; the worker contract is
//     covered by internal/services/youtube_oauth_validate_test.go
//     and internal/worker/publish_worker_retry_test.go.
//  4. EXTERNAL DESTINATION ENABLED — external_destinations row
//     exists with source_system='velox', enabled=true and linked to
//     the same platform_account via the
//     (workspace_id, platform_account_id) tuple. The catalog path
//     (ListWorkspaceTargets) joins this row and stamps
//     ExternalDestinationID on the catalog entry.
//
// A future regression that drifts ANY of the resolver-side checks
// (binding.enabled / status active / reauth null +
// external_destination enabled + velox source) breaks this test.
//
// Cross-reference: this test does NOT verify OAuth scope/path fine
// details (gdrive readonly, channel-manager scope) — see
// internal/services/youtube_oauth_validate_test.go for those.
func TestTargetResolver_PublishReady_AllFourConditionsMet(t *testing.T) {
	const (
		workspaceID        int64 = 42
		platformAccountID  int64 = 381
		extDestinationID         = "extdst_01JABC_publish_ready"
		directTargetDestID       = "instaedit_youtube"
		channelID                = "UC_publish_ready_42"
		channelUsername          = "Publish Ready Channel"
	)

	// Fixture: workspace + workspace_channel binding (cond 1 enabled),
	// platform_account (cond 2 active + reauth_required_at nil),
	// external_destination (cond 4 velox + enabled + linked).
	resolver := NewTargetResolver(TargetResolverDeps{
		DestinationStore: &catalogDestinationStore{destinations: []models.ExternalDestination{
			{
				ID:                extDestinationID,
				SourceSystem:      "velox",
				WorkspaceID:       workspaceID,
				PlatformAccountID: platformAccountID,
				Enabled:           true,
			},
		}},
		WorkspaceStore: &catalogWorkspaceStore{
			workspace: &models.Workspace{ID: workspaceID, Name: "Editorial"},
			channels: []models.WorkspaceChannel{
				{
					WorkspaceID:       workspaceID,
					PlatformAccountID: platformAccountID,
					Enabled:           true,
				},
			},
		},
		UserStore: &catalogUserStore{accounts: map[int64]*models.PlatformAccount{
			platformAccountID: {
				ID:               platformAccountID,
				Platform:         models.PlatformYouTube,
				PlatformUserID:   channelID,
				Username:         channelUsername,
				Status:           models.AccountStatusActive,
				ReauthRequiredAt: nil,
			},
		}},
	})

	ctx := context.Background()

	// --- SavedDestination path (id-based validate) ---------------------
	got, err := resolver.Resolve(ctx, ResolveRequest{
		DestID: extDestinationID,
	})
	if err != nil {
		t.Fatalf("Resolve(extdst) returned error: %v", err)
	}
	if !got.Valid {
		t.Fatalf("publish-ready channel should be Valid=true; got Valid=false (ErrorCode=%q, Message=%q)", got.ErrorCode, got.Message)
	}
	if got.ErrorCode != "" {
		t.Fatalf("publish-ready channel ErrorCode must be empty; got %q", got.ErrorCode)
	}
	if got.DestinationID != extDestinationID {
		t.Fatalf("DestinationID: want %q; got %q", extDestinationID, got.DestinationID)
	}
	if got.Platform != models.PlatformYouTube {
		t.Fatalf("Platform: want %q; got %q", models.PlatformYouTube, got.Platform)
	}
	if len(got.ResolvedTargets) != 1 {
		t.Fatalf("ResolvedTargets: want 1 entry; got %d", len(got.ResolvedTargets))
	}
	entry := got.ResolvedTargets[0]
	if entry.PlatformAccountID != platformAccountID {
		t.Fatalf("entry.PlatformAccountID: want %d; got %d", platformAccountID, entry.PlatformAccountID)
	}
	if entry.Status != models.AccountStatusActive {
		t.Fatalf("entry.Status: want %q; got %q", models.AccountStatusActive, entry.Status)
	}
	if !entry.Enabled {
		t.Fatalf("entry.Enabled: want true (binding row had Enabled=true); got false")
	}
	if entry.TargetErrorCode != "" {
		t.Fatalf("entry.TargetErrorCode: want empty for publish-ready; got %q", entry.TargetErrorCode)
	}

	// --- DirectTarget path (body-based resolve-target) ----------------
	dir, err := resolver.Resolve(ctx, ResolveRequest{
		WorkspaceID: workspaceID,
		Platform:    models.PlatformYouTube,
		Target: TargetDescriptor{
			Type:              "channel",
			PlatformAccountID: platformAccountID,
		},
	})
	if err != nil {
		t.Fatalf("Resolve(direct) returned error: %v", err)
	}
	if !dir.Valid {
		t.Fatalf("DirectTarget publish-ready should be Valid=true; got Valid=false (ErrorCode=%q, Message=%q)", dir.ErrorCode, dir.Message)
	}
	if dir.DestinationID != directTargetDestID {
		t.Fatalf("DirectTarget DestinationID: want %q; got %q", directTargetDestID, dir.DestinationID)
	}
	if dir.ErrorCode != "" {
		t.Fatalf("DirectTarget ErrorCode must be empty for publish-ready; got %q", dir.ErrorCode)
	}

	// --- ListWorkspaceTargets (catalog must surface ExternalDestinationID) ----
	catalog, err := resolver.ListWorkspaceTargets(ctx, workspaceID, models.PlatformYouTube)
	if err != nil {
		t.Fatalf("ListWorkspaceTargets returned error: %v", err)
	}
	if len(catalog) != 1 {
		t.Fatalf("catalog: want 1 entry; got %d", len(catalog))
	}
	catalogEntry := catalog[0]
	if catalogEntry.PlatformAccountID != platformAccountID {
		t.Fatalf("catalog.PlatformAccountID: want %d; got %d", platformAccountID, catalogEntry.PlatformAccountID)
	}
	if catalogEntry.ExternalDestinationID != extDestinationID {
		t.Fatalf("catalog.ExternalDestinationID: want %q; got %q (cond 4 want destination enabled + linked)", extDestinationID, catalogEntry.ExternalDestinationID)
	}
	if catalogEntry.TargetErrorCode != "" {
		t.Fatalf("catalog.TargetErrorCode must be empty for publish-ready; got %q (cond 4 violation)", catalogEntry.TargetErrorCode)
	}
	if !catalogEntry.Enabled {
		t.Fatalf("catalog.Enabled: want true (cond 1 violation — binding row must have Enabled=true)")
	}
	if catalogEntry.Status != models.AccountStatusActive {
		t.Fatalf("catalog.Status: want %q (cond 2 violation); got %q", models.AccountStatusActive, catalogEntry.Status)
	}

	// --- CapabilitiesForTarget (cond 4 + YouTube surface) -------------
	caps := CapabilitiesForTarget(catalogEntry.ResolvedTargetEntry)
	if !caps.UploadVideo {
		t.Fatalf("publish-ready YouTube channel must surface upload_video capability; got %#v", caps)
	}
	if !caps.SetThumbnail {
		t.Fatalf("publish-ready YouTube channel must surface set_thumbnail capability; got %#v", caps)
	}
	if !caps.Publish {
		t.Fatalf("publish-ready YouTube channel must surface publish capability; got %#v", caps)
	}
	if !caps.Schedule {
		t.Fatalf("publish-ready YouTube channel must surface schedule capability; got %#v", caps)
	}
}

// TestTargetResolver_PublishReady_BlockedByEachCondition inverts each
// of the four happy-path conditions SEPARATELY and asserts (a) the
// respective ErrorCode/TargetErrorCode plus (b) ALL four YouTube
// capability flags are blocked (upload_video, set_thumbnail, publish,
// schedule). The Velox socialclient maps
//
//	capabilities.UploadVideo=false  →  can_post=false
//
// which flows into delivery_destinations.enabled and BLOCKS the
// publisher from routing a job to the channel. A regression that lets
// any one subcase slip a non-blocked verdict past would let Velox
// silently drain a job to a disabled channel.
//
// Pins the error-code taxonomy the Velox handler (publishing_targets.go)
// depends on:
//   - BLOCKED_AUTH          for reauth (oauth grant revoked / token expired)
//   - TARGET_NOT_AVAILABLE  for binding disabled / external destination
//     disabled / external destination source_system
//     != "velox"
//
// The two paths share checkAccountEligibility so this test exercises
// BOTH the SavedDestination path (per-target entry) AND the
// ListWorkspaceTargets catalog path (channel-level visibility).
func TestTargetResolver_PublishReady_BlockedByEachCondition(t *testing.T) {
	const (
		workspaceID       int64 = 42
		platformAccountID int64 = 381
		channelID               = "UC_blocked_42"
		channelUsername         = "Blocked Channel"
	)

	// Healthy baseline — each subcase flips exactly ONE condition.
	activeAccount := &models.PlatformAccount{
		ID:               platformAccountID,
		Platform:         models.PlatformYouTube,
		PlatformUserID:   channelID,
		Username:         channelUsername,
		Status:           models.AccountStatusActive,
		ReauthRequiredAt: nil,
	}

	cases := []struct {
		name             string
		bindingEnabled   bool                    // cond 1: workspace↔account binding
		pa               *models.PlatformAccount // cond 2/3: account state
		destEnabled      bool                    // cond 4a: external_destination.enabled
		destSourceSystem string                  // cond 4b: external_destination.source_system
		wantErrorCode    string                  // EXPECTED catalog.TargetErrorCode
	}{
		{
			// (a) workspace binding DISABLED — flipping cond 1 only.
			// binding.Enabled=false trips checkAccountEligibility step 4,
			// stamps entry.TargetErrorCode=TARGET_NOT_AVAILABLE; the
			// external_destination row stays healthy so we know the block
			// is truly from the binding, not the destination.
			name:             "blocked_by_disabled_workspace_binding",
			bindingEnabled:   false,
			pa:               activeAccount,
			destEnabled:      true,
			destSourceSystem: "velox",
			wantErrorCode:    ErrCodeTargetNotAvailable,
		},
		{
			// (b) platform_account reauth_required. We use the timestamp
			// path because it's the dual-signal gate documented in
			// checkAccountEligibility step 1 — status enum matches the
			// same verdict. The user's spec framed reauth as a single
			// condition; both sub-paths emit BLOCKED_AUTH so this single
			// subcase pins it.
			name:           "blocked_by_reauth_required",
			bindingEnabled: true,
			pa: clonePlatformAccount(activeAccount, func(p *models.PlatformAccount) {
				now := time.Now().UTC()
				p.ReauthRequiredAt = &now
			}),
			destEnabled:      true,
			destSourceSystem: "velox",
			wantErrorCode:    ErrCodeBlockedAuth,
		},
		{
			// (c) external_destination DISABLED — flipping cond 4 (Velox-
			// side row enabled flag). The workspace binding + account are
			// healthy, but ListWorkspaceTargets excludes the row at
			// target_catalog.go line 73 (destination.Enabled=false) so
			// ExternalDestinationID is empty and entry.TargetErrorCode
			// gets stamped TARGET_NOT_AVAILABLE at line 138.
			name:             "blocked_by_disabled_external_destination",
			bindingEnabled:   true,
			pa:               activeAccount,
			destEnabled:      false,
			destSourceSystem: "velox",
			wantErrorCode:    ErrCodeTargetNotAvailable,
		},
		{
			// (d) external_destination with source_system != "velox".
			// Mirrors (c)'s mechanism at the same target_catalog.go line 73
			// guard — a destination row exists but is filtered out by the
			// source_system check. This guards against a future
			// multi-source extension (e.g. dropbox joining the same code
			// path) leaking foreign destinations into the Velox
			// publishing_targets response.
			name:             "blocked_by_foreign_source_system",
			bindingEnabled:   true,
			pa:               activeAccount,
			destEnabled:      true,
			destSourceSystem: "dropbox",
			wantErrorCode:    ErrCodeTargetNotAvailable,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resolver := NewTargetResolver(TargetResolverDeps{
				DestinationStore: &catalogDestinationStore{destinations: []models.ExternalDestination{{
					ID:                "extdst_01J_" + tc.name,
					SourceSystem:      tc.destSourceSystem,
					WorkspaceID:       workspaceID,
					PlatformAccountID: platformAccountID,
					Enabled:           tc.destEnabled,
				}}},
				WorkspaceStore: &catalogWorkspaceStore{
					workspace: &models.Workspace{ID: workspaceID, Name: "Editorial"},
					channels: []models.WorkspaceChannel{{
						WorkspaceID:       workspaceID,
						PlatformAccountID: platformAccountID,
						Enabled:           tc.bindingEnabled,
					}},
				},
				UserStore: &catalogUserStore{accounts: map[int64]*models.PlatformAccount{
					platformAccountID: tc.pa,
				}},
			})

			ctx := context.Background()

			// --- Catalog path (Velox /publishing/targets consumer) -----
			catalog, err := resolver.ListWorkspaceTargets(ctx, workspaceID, models.PlatformYouTube)
			if err != nil {
				t.Fatalf("ListWorkspaceTargets returned error: %v", err)
			}
			if len(catalog) != 1 {
				t.Fatalf("catalog: want 1 entry; got %d", len(catalog))
			}
			entry := catalog[0]

			// (i) The respective TargetErrorCode pinned.
			if entry.TargetErrorCode != tc.wantErrorCode {
				t.Fatalf("catalog.TargetErrorCode: want %q; got %q (Velox socialclient gates can_post on this exact verdict)",
					tc.wantErrorCode, entry.TargetErrorCode)
			}

			// (ii) For (c) and (d) — when the destination row is rejected,
			// the catalog entry's ExternalDestinationID surfaced to Velox
			// must be empty (the row was excluded at target_catalog.go:73).
			if !tc.destEnabled || tc.destSourceSystem != "velox" {
				if entry.ExternalDestinationID != "" {
					t.Fatalf("catalog.ExternalDestinationID: want empty (foreign/disabled destination); got %q",
						entry.ExternalDestinationID)
				}
			}

			// (iii) CapabilitiesForTarget — assert ALL four capability
			// flags blocked. Any non-blocked flag would let Velox
			// mistakenly advertise can_post=true.
			caps := CapabilitiesForTarget(entry.ResolvedTargetEntry)
			if caps.UploadVideo {
				t.Fatalf("capabilities.UploadVideo: want false (publisher blocks); got true")
			}
			if caps.SetThumbnail {
				t.Fatalf("capabilities.SetThumbnail: want false (publisher blocks); got true")
			}
			if caps.Publish {
				t.Fatalf("capabilities.Publish: want false (publisher blocks); got true")
			}
			if caps.Schedule {
				t.Fatalf("capabilities.Schedule: want false (publisher blocks); got true")
			}

			// (iv) SavedDestination path (id-based validate) — when the
			// destination row is loaded by `dest.Enabled && dest != nil`,
			// the eligibility gate fires for (a)(b)(c). Only (d) skips
			// because SavedDestination does NOT consult `dest.SourceSystem`
			// (target_resolver.go:236 only checks dest.Enabled, not the
			// source-system guard that lives in target_catalog.go:73).
			if tc.destSourceSystem == "velox" {
				savedDestID := "extdst_01J_" + tc.name
				got, err := resolver.Resolve(ctx, ResolveRequest{DestID: savedDestID})
				if err != nil {
					t.Fatalf("Resolve(SavedDestination) returned error: %v", err)
				}
				if got.Valid {
					t.Fatalf("SavedDestination should be Valid=false for blocker %q; got Valid=true (ErrorCode=%q)",
						tc.name, got.ErrorCode)
				}
				if got.ErrorCode != tc.wantErrorCode {
					t.Fatalf("ResolveResult.ErrorCode: want %q; got %q",
						tc.wantErrorCode, got.ErrorCode)
				}
			}

			// (v) DirectTarget path (body-based resolve-target) — the
			// endpoint the user spec explicitly exercises. Must emit the
			// same ErrorCode as the catalog verdict so the wire contract
			// stays unified.
			dir, err := resolver.Resolve(ctx, ResolveRequest{
				WorkspaceID: workspaceID,
				Platform:    models.PlatformYouTube,
				Target: TargetDescriptor{
					Type:              "channel",
					PlatformAccountID: platformAccountID,
				},
			})
			if err != nil {
				t.Fatalf("Resolve(DirectTarget) returned error: %v", err)
			}
			// For (c) and (d) ExternalDestinationID-empty / foreign
			// source_system, DirectTarget goes through the eligibility
			// gate (workspace + binding + account checks) which does NOT
			// consult external_destinations — so those subcases are
			// permitted to surface Valid=true (DirectTarget doesn't
			// see the destination row). The destination-row check is
			// only wired in ListWorkspaceTargets at target_catalog.go:73.
			// Conversely, (a)(b) must trip DirectTarget because they
			// flip the binding or the account state (which
			// checkAccountEligibility reads directly).
			switch tc.name {
			case "blocked_by_disabled_external_destination", "blocked_by_foreign_source_system":
				if !dir.Valid {
					t.Fatalf("DirectTarget for %q should be Valid=true (DirectTarget doesn't see the destination); got Valid=false (ErrorCode=%q, Message=%q)",
						tc.name, dir.ErrorCode, dir.Message)
				}
			default:
				if dir.Valid {
					t.Fatalf("DirectTarget should be Valid=false for blocker %q; got Valid=true (ErrorCode=%q)",
						tc.name, dir.ErrorCode)
				}
				if dir.ErrorCode != tc.wantErrorCode {
					t.Fatalf("DirectTarget.ErrorCode: want %q; got %q",
						tc.wantErrorCode, dir.ErrorCode)
				}
			}
		})
	}
}
