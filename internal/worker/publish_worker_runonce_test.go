package worker

import (
	"context"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// ------------------------------------------------------------------
// runOnce test — Taglio 5.x: runOnce calls tick() ONLY. Reconcile is
// its own goroutine now (ReconcileWorker.Run).
// ------------------------------------------------------------------

// TestRunOnce_TickOnly asserts the new runOnce() body: it should
// call tick() — and ONLY tick(). The reconciler is no longer
// invoked from this goroutine.
//
// Positive assertion:
//   - ListPending called once
//   - Reconciler methods (CheckPublishStatus, Reconcile) NEVER
//     reached from runOnce on the driver goroutine — the claim
//     was taken on a sync platform so no async-publish side.
func TestRunOnce_TickOnly(t *testing.T) {
	posts := &mockPostStore{
		listPendingFn: func(before time.Time) ([]models.PostTarget, error) {
			return []models.PostTarget{
				{ID: 1, PostID: 100, PlatformAccountID: 10, Status: models.PostStatusScheduled},
			}, nil
		},
		findByIDFn: func(id int64) (*models.Post, error) {
			return &models.Post{ID: 100, Caption: "x"}, nil
		},
		claimFn:  func(id int64) (bool, error) { return true, nil },
		setKeyFn: func(id int64, key string) error { return nil },
	}
	users := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return &models.PlatformAccount{ID: 10, Platform: "instagram", PlatformUserID: "fb-1"}, nil
		},
	}
	// Sync platform — Publish happens inline, no async branch.
	svc := &mockProvider{
		baseMockProvider: baseMockProvider{platform: "instagram"},
		publishFn: func(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (*models.PublishResult, error) {
			return &models.PublishResult{PlatformMediaID: "media-1"}, nil
		},
	}
	vault := &mockCredentialVault{
		renewFn: func(ctx context.Context, accountID int64, tokenType string, refresh credentials.TokenRefresher) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "t"}, nil
		},
	}
	w := newTestWorker(posts, users, "instagram", svc, vault)

	w.runOnce(context.Background())

	// ListPending called once (tick ran).
	if posts.listPendingCalls != 1 {
		t.Errorf("ListPending calls: want 1 (tick ran), got %d", posts.listPendingCalls)
	}
	if svc.publishCalls != 1 {
		t.Errorf("Publish calls: want 1 (sync path), got %d", svc.publishCalls)
	}
	if posts.updateCalls != 1 {
		t.Errorf("UpdateStatus calls: want 1 (publishing→published), got %d", posts.updateCalls)
	}
	if posts.updateTargets[0].Status != models.PostStatusPublished {
		t.Errorf("final status: want published, got %q", posts.updateTargets[0].Status)
	}
	// Reconciler NEVER reached from the driver's goroutine — the
	// mockAsyncProvider would never be wired here (the test uses
	// mockProvider for sync). For the async-platform negative
	// assertion, run an async-platform variant below.
}

// TestRunOnce_TickOnly_AsyncPlatform_NoReconcile asserts the new
// shape on an ASYNC platform: the driver publishes (returning a
// publish_id, status stays 'publishing'). The driver must NOT
// invoke Reconcile — that's the ReconcilerWorker's Run-loop job now.
//
// This is the regression guard against re-introducing the old
// runOnce which called tickReconcile (.ListPublishing + .Reconcile).
func TestRunOnce_TickOnly_AsyncPlatform_NoReconcile(t *testing.T) {
	posts := &mockPostStore{
		listPendingFn: func(before time.Time) ([]models.PostTarget, error) {
			return []models.PostTarget{
				{ID: 1, PostID: 100, PlatformAccountID: 10, Status: models.PostStatusScheduled},
			}, nil
		},
		findByIDFn: func(id int64) (*models.Post, error) {
			return &models.Post{ID: 100, Caption: "x", MediaURL: "https://cdn.example.com/v.mp4"}, nil
		},
		claimFn:  func(id int64) (bool, error) { return true, nil },
		setKeyFn: func(id int64, key string) error { return nil },
	}
	users := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return &models.PlatformAccount{ID: 10, Platform: "tiktok", PlatformUserID: "tt-1"}, nil
		},
	}
	// TikTok-style async provider.
	svc := &mockAsyncProvider{
		baseMockProvider: baseMockProvider{platform: "tiktok"},
		publishFn: func(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (*models.PublishResult, error) {
			return &models.PublishResult{PlatformMediaID: "tiktok-publish-id"}, nil
		},
	}
	vault := &mockCredentialVault{
		renewFn: func(ctx context.Context, accountID int64, tokenType string, refresh credentials.TokenRefresher) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "t"}, nil
		},
	}
	w := newTestWorker(posts, users, "tiktok", svc, vault)

	w.runOnce(context.Background())

	// Driver tick fired.
	if posts.listPendingCalls != 1 {
		t.Errorf("ListPending calls: want 1, got %d", posts.listPendingCalls)
	}
	if svc.publishCalls != 1 {
		t.Errorf("Publish calls: want 1, got %d", svc.publishCalls)
	}
	// Drive left the row in 'publishing' with the publish_id stamped.
	if posts.updateTargets[0].Status != models.PostStatusPublishing {
		t.Errorf("final status: want publishing (async, reconciler owns terminal), got %q", posts.updateTargets[0].Status)
	}
	if posts.updateTargets[0].PlatformPostID != "tiktok-publish-id" {
		t.Errorf("platform_post_id: want tiktok-publish-id, got %q", posts.updateTargets[0].PlatformPostID)
	}
	// Reconciler NEVER reached from the driver's runOnce. Loop
	// back over the assertion: the async mock's Reconcile,
	// CheckPublishStatus, StartPublish counters must all be 0 here.
	if svc.reconcileCalls != 0 {
		t.Errorf("Reconcile calls in driver runOnce: want 0 (reconciler owns Reconcile), got %d", svc.reconcileCalls)
	}
	if svc.checkStatusCalls != 0 {
		t.Errorf("CheckPublishStatus calls in driver runOnce: want 0 (reconciler owns status-check path), got %d", svc.checkStatusCalls)
	}
	if svc.startPublishCalls != 0 {
		t.Errorf("StartPublish calls in driver runOnce: want 0 (per-platform timing — driver uses Publish via the canonical path), got %d", svc.startPublishCalls)
	}
}
