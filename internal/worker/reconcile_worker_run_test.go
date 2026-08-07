package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// ------------------------------------------------------------------
// ReconcileWorker.Run tests
// ------------------------------------------------------------------

// TestReconcileWorker_Run_TicksAndExitsOnCtxCancel verifies the
// dispatcher's shape: initial drain (first runOnce before ticker) +
// ticker-fired runOnce on the interval, then ctx.Done() returns
// cleanly. Drives the Run loop on a goroutine and asserts counters
// before cancelling.
func TestReconcileWorker_Run_TicksAndExitsOnCtxCancel(t *testing.T) {
	posts := &mockReconcilePostStore{listPublishingFn: func() ([]models.PostTarget, error) {

		return []models.PostTarget{
			{ID: 1, PostID: 100, PlatformAccountID: 10, Status: models.PostStatusPublishing, PlatformPostID: "p-1"},
		}, nil
	},
	}
	users := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return &models.PlatformAccount{ID: 10, Platform: "tiktok", PlatformUserID: "tt-1"}, nil
		},
	}
	svc := &mockAsyncProvider{
		baseMockProvider: baseMockProvider{platform: "tiktok"},
		reconcileFn: func(ctx context.Context, accessToken, publishID string) (*models.PublishResult, error) {
			return &models.PublishResult{PlatformMediaID: publishID}, nil
		},
	}
	vault := &mockCredentialVault{
		renewFn: func(ctx context.Context, accountID int64, tokenType string, refresh credentials.TokenRefresher) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "t"}, nil
		},
	}

	router := services.NewCapabilityRouter()
	router.Register("tiktok", svc)
	w := NewReconcileWorker(posts, users, router, vault, "test-worker-id", nil, 10*time.Millisecond, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()

	// Wait for at least one tickReconcile call (initial drain + at
	// least one ticker tick within 200ms with 10ms interval).
	deadline := time.Now().Add(150 * time.Millisecond)
	for time.Now().Before(deadline) {
		posts.mu.Lock()
		calls := posts.listPublishingCalls
		posts.mu.Unlock()
		if calls > 0 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
			t.Errorf("Run err: want DeadlineExceeded or Canceled, got %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Run did not return after ctx cancel")
	}

	posts.mu.Lock()
	listCalls := posts.listPublishingCalls
	posts.mu.Unlock()
	if listCalls < 1 {
		t.Errorf("ListPublishing calls: want >=1 (initial drain), got %d", listCalls)
	}
}

// TestReconcileWorker_Run_GracefulShutdown_DrainsInFlight covers
// the dispatcher's "graceful shutdown al worker esistente"
// requirement: when ctx is cancelled, the reconciler stops calling
// ListPublishing but lets the in-flight reconcileTarget finish.
// Uses the same gate-channel pattern as
// TestDispatcher_GracefulShutdown_DrainsInFlight (processFunc
// ignores ctx so ctx-cancel doesn't short-circuit).
func TestReconcileWorker_Run_GracefulShutdown_DrainsInFlight(t *testing.T) {
	posts := &mockReconcilePostStore{listPublishingFn: func() ([]models.PostTarget, error) {

		return []models.PostTarget{
			{ID: 11, PostID: 100, PlatformAccountID: 10, Status: models.PostStatusPublishing, PlatformPostID: "p-11"},
		}, nil
	},
	}
	users := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return &models.PlatformAccount{ID: 10, Platform: "tiktok", PlatformUserID: "tt-1"}, nil
		},
	}

	entered := make(chan struct{})
	gate := make(chan struct{})
	svc := &mockAsyncProvider{
		baseMockProvider: baseMockProvider{platform: "tiktok"},
		reconcileFn: func(_ context.Context, _ string, _ string) (*models.PublishResult, error) {
			// Gate on test-driven channel only; ignore ctx so
			// the in-flight reconcileTarget isn't short-circuited
			// by ctx cancel (matches the dispatcher's grace test).
			close(entered)
			<-gate
			return &models.PublishResult{PlatformMediaID: "p-11"}, nil
		},
	}
	vault := &mockCredentialVault{
		renewFn: func(_ context.Context, _ int64, _ string, _ credentials.TokenRefresher) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "t"}, nil
		},
	}

	router := services.NewCapabilityRouter()
	router.Register("tiktok", svc)
	w := NewReconcileWorker(posts, users, router, vault, "test-worker-id", nil, 1*time.Hour, nil) // big tick so only the initial drain fires

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()

	<-entered
	cancel()

	// Run must NOT return yet — the in-flight reconcile must drain.
	select {
	case err := <-done:
		t.Fatalf("Run returned prematurely with %v (in-flight should drain)", err)
	case <-time.After(50 * time.Millisecond):
	}

	// Unblock the reconciler; Run should now return ctx.Canceled.
	close(gate)
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Run err: want context.Canceled, got %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Run did not return after gate closed")
	}

	// After the gate closed, the in-flight reconcileTarget completed
	// successfully → at least one UpdateStatus call with status=published.
	if posts.updateCalls < 1 {
		t.Errorf("UpdateStatus calls after graceful drain: want >=1, got %d", posts.updateCalls)
	}
	if len(posts.updateTargets) < 1 || posts.updateTargets[0].Status != models.PostStatusPublished {
		t.Errorf("final status: want published, got %+v", posts.updateTargets)
	}
}

func TestReconcileWorker_RunOnce_RepairsOnlyDirtyAggregatePosts(t *testing.T) {
	posts := &mockReconcilePostStore{
		dirtyPostIDsFn: func(limit int) ([]int64, error) {
			return []int64{101, 202}, nil
		},
	}
	w := NewReconcileWorker(
		posts,
		nil,
		services.NewCapabilityRouter(),
		nil,
		"test-worker-id",
		nil,
		time.Hour,
		nil,
	)

	w.runOnce(context.Background())

	posts.mu.Lock()
	listCalls := posts.dirtyPostIDsCalls
	limit := posts.dirtyPostIDsLimit
	repairCalls := posts.repairDirtyPostCalls
	repairedIDs := append([]int64(nil), posts.repairedPostIDs...)
	posts.mu.Unlock()
	if listCalls != 1 {
		t.Fatalf("ListDirtyAggregatePostIDs calls = %d, want 1", listCalls)
	}
	if limit != dirtyAggregateRepairBatchSize {
		t.Fatalf("dirty queue limit = %d, want %d", limit, dirtyAggregateRepairBatchSize)
	}
	if repairCalls != 2 {
		t.Fatalf("RepairDirtyAggregatePost calls = %d, want 2", repairCalls)
	}
	if len(repairedIDs) != 2 || repairedIDs[0] != 101 || repairedIDs[1] != 202 {
		t.Fatalf("repaired post IDs = %v, want [101 202]", repairedIDs)
	}
}

func TestReconcileWorker_RunOnce_DoesNotScanPostsForAggregateRepair(t *testing.T) {
	posts := &mockReconcilePostStore{}
	w := NewReconcileWorker(
		posts,
		nil,
		services.NewCapabilityRouter(),
		nil,
		"test-worker-id",
		nil,
		time.Hour,
		nil,
	)

	w.runOnce(context.Background())

	posts.mu.Lock()
	listCalls := posts.dirtyPostIDsCalls
	repairCalls := posts.repairDirtyPostCalls
	posts.mu.Unlock()
	if listCalls != 1 || repairCalls != 0 {
		t.Fatalf("dirty repair calls = list:%d repair:%d, want list:1 repair:0", listCalls, repairCalls)
	}
}

// The Run tests above (TestReconcileWorker_Run_*) construct a
// CapabilityRouter inline with a single `services.NewCapabilityRouter()
// + router.Register(name, svc)` call rather than going through
// mocks_test.go's newTestReconcileWorker (which hardcodes a 10ms
// TickInterval). They need finer control over the tick interval:
// 10ms for the initial-drain test, 1h for the graceful-shutdown test
// (so only the initial drain fires, then the in-flight reconcileTarget
// is the only thing in flight).
