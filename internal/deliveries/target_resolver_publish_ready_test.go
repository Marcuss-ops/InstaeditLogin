package deliveries

import (
	"context"
	"testing"

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
		workspaceID         int64 = 42
		platformAccountID   int64 = 381
		extDestinationID          = "extdst_01JABC_publish_ready"
		directTargetDestID        = "instaedit_youtube"
		channelID                 = "UC_publish_ready_42"
		channelUsername           = "Publish Ready Channel"
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
					WorkspaceID:      workspaceID,
					PlatformAccountID: platformAccountID,
					Enabled:          true,
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
			Type:             "channel",
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
