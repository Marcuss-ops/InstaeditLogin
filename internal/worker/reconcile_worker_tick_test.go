package worker

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// ------------------------------------------------------------------
// tickReconcile tests
// ------------------------------------------------------------------

// TestTickReconcile_IteratesAllPublishingTargets covers the
// tickReconcile body: it should call ListPublishing, then iterate
// every returned target through reconcileTarget (which delegates to
// Reconcile). Reconcile returning (nil, nil) on every target = all
// in-flight.
func TestTickReconcile_UsesBoundedPollingLimit(t *testing.T) {
	posts := &mockReconcilePostStore{}
	users := &mockUserStore{}
	w := newTestReconcileWorker(posts, users, "tiktok", &mockAsyncProvider{baseMockProvider: baseMockProvider{platform: "tiktok"}}, &mockCredentialVault{})

	_, _, err := w.tickReconcile(context.Background())
	if err != nil {
		t.Fatalf("tickReconcile: %v", err)
	}
	if posts.listPublishingLimit != reconcilePollingBatchSize {
		t.Fatalf("ListPublishing limit = %d, want %d", posts.listPublishingLimit, reconcilePollingBatchSize)
	}
}

func TestTickReconcile_BoundedBatchThroughput(t *testing.T) {
	const batch = reconcilePollingBatchSize
	posts := &mockReconcilePostStore{listPublishingFn: func() ([]models.PostTarget, error) {
		// The repository contract already bounds this slice to `batch`; the
		// test verifies the worker consumes one bounded batch and never grows
		// local work beyond the configured throughput budget.
		targets := make([]models.PostTarget, batch)
		for i := range targets {
			targets[i] = models.PostTarget{
				ID:                int64(i + 1),
				PostID:            int64(i + 1),
				PlatformAccountID: 10,
				Status:            models.PostStatusPublishing,
				PlatformPostID:    "publish-" + strconv.Itoa(i+1),
			}
		}
		return targets, nil
	}}
	users := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return &models.PlatformAccount{ID: 10, Platform: "tiktok", PlatformUserID: "tt-1"}, nil
		},
	}
	provider := &mockAsyncProvider{
		baseMockProvider: baseMockProvider{platform: "tiktok"},
		reconcileFn: func(context.Context, string, string) (*models.PublishResult, error) {
			return nil, nil
		},
	}
	vault := &mockCredentialVault{
		renewFn: func(context.Context, int64, string, credentials.TokenRefresher) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "token"}, nil
		},
	}
	w := newTestReconcileWorker(posts, users, "tiktok", provider, vault)

	if _, _, err := w.tickReconcile(context.Background()); err != nil {
		t.Fatalf("tickReconcile: %v", err)
	}
	if posts.listPublishingLimit != batch {
		t.Fatalf("ListPublishing limit = %d, want %d", posts.listPublishingLimit, batch)
	}
	if provider.reconcileCalls != batch {
		t.Fatalf("provider calls = %d, want exactly one bounded batch (%d)", provider.reconcileCalls, batch)
	}
}

func TestReconcileWorker_AdaptiveBackoffCapsAtMaximum(t *testing.T) {
	posts := &mockReconcilePostStore{}
	w := &ReconcileWorker{postRepo: posts}
	target := publishingTarget()
	target.ReconcileAttempt = len(reconcileBackoffSchedule) + 20
	expectedAttempt := target.ReconcileAttempt

	before := time.Now()
	if _, _, err := w.scheduleInFlight(target); err != nil {
		t.Fatalf("scheduleInFlight: %v", err)
	}
	if posts.scheduleCalls != 1 {
		t.Fatalf("schedule calls = %d, want 1", posts.scheduleCalls)
	}
	if posts.scheduleAttempts[0] != expectedAttempt {
		t.Fatalf("expected attempt = %d, got %d", expectedAttempt, posts.scheduleAttempts[0])
	}
	if target.ReconcileAttempt != expectedAttempt {
		t.Fatalf("in-flight scheduling consumed transient attempt: got %d, want %d", target.ReconcileAttempt, expectedAttempt)
	}
	delta := posts.scheduleTimes[0].Sub(before)
	if delta < reconcileBackoffCap-500*time.Millisecond || delta > reconcileBackoffCap+500*time.Millisecond {
		t.Fatalf("adaptive backoff = %v, want approximately cap %v", delta, reconcileBackoffCap)
	}
}

func TestReconcileWorker_SchedulesAdaptiveBackoff(t *testing.T) {
	posts := &mockReconcilePostStore{}
	users := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return &models.PlatformAccount{ID: 10, Platform: "tiktok", PlatformUserID: "tt-1"}, nil
		},
	}
	provider := &mockAsyncProvider{
		baseMockProvider: baseMockProvider{platform: "tiktok"},
		reconcileFn:      func(context.Context, string, string) (*models.PublishResult, error) { return nil, nil },
	}
	vault := &mockCredentialVault{
		renewFn: func(context.Context, int64, string, credentials.TokenRefresher) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "token"}, nil
		},
	}
	w := newTestReconcileWorker(posts, users, "tiktok", provider, vault)
	target := publishingTarget()

	for i, want := range reconcileBackoffSchedule {
		target.ReconcileAttempt = i
		before := time.Now().Add(want - 500*time.Millisecond)
		_, _, err := w.reconcileTarget(context.Background(), target)
		if err != nil {
			t.Fatalf("reconcile attempt %d: %v", i, err)
		}
		if posts.scheduleCalls != i+1 {
			t.Fatalf("schedule calls after attempt %d = %d, want %d", i, posts.scheduleCalls, i+1)
		}
		got := posts.scheduleTimes[i]
		if got.Before(before) {
			t.Fatalf("backoff %d scheduled too early: got %v, lower bound %v", i, got, before)
		}
		if posts.scheduleAttempts[i] != i {
			t.Fatalf("CAS expected attempt at step %d = %d, want %d", i, posts.scheduleAttempts[i], i)
		}
		if posts.scheduleIncrement[i] {
			t.Fatalf("in-flight scheduling incremented attempt at step %d", i)
		}
	}
	if posts.scheduleTimes[len(posts.scheduleTimes)-1].Sub(time.Now()) > reconcileBackoffCap+time.Second {
		t.Fatalf("backoff exceeded cap: %v", posts.scheduleTimes[len(posts.scheduleTimes)-1])
	}
}

func TestTickReconcile_IteratesAllPublishingTargets(t *testing.T) {
	posts := &mockReconcilePostStore{listPublishingFn: func() ([]models.PostTarget, error) {

		return []models.PostTarget{
			{ID: 1, PostID: 100, PlatformAccountID: 10, Status: models.PostStatusPublishing, PlatformPostID: "p-1"},
			{ID: 2, PostID: 100, PlatformAccountID: 10, Status: models.PostStatusPublishing, PlatformPostID: "p-2"},
			{ID: 3, PostID: 100, PlatformAccountID: 10, Status: models.PostStatusPublishing, PlatformPostID: "p-3"},
		}, nil
	},
	}
	users := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return &models.PlatformAccount{ID: 10, Platform: "tiktok", PlatformUserID: "tt-1"}, nil
		},
	}
	// Reconcile returns (nil, nil) for in-flight on every call.
	svc := &mockAsyncProvider{
		baseMockProvider: baseMockProvider{platform: "tiktok"},
		reconcileFn: func(ctx context.Context, accessToken, publishID string) (*models.PublishResult, error) {
			return nil, nil
		},
	}
	vault := &mockCredentialVault{
		renewFn: func(ctx context.Context, accountID int64, tokenType string, refresh credentials.TokenRefresher) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "t"}, nil
		},
	}
	w := newTestReconcileWorker(posts, users, "tiktok", svc, vault)

	reconciled, failed, err := w.tickReconcile(context.Background())
	if err != nil {
		t.Fatalf("tickReconcile: %v", err)
	}
	if reconciled != 0 {
		t.Errorf("reconciled: want 0 (all in-flight), got %d", reconciled)
	}
	if failed != 0 {
		t.Errorf("failed: want 0 (all in-flight), got %d", failed)
	}
	if posts.listPublishingCalls != 1 {
		t.Errorf("ListPublishing calls: want 1, got %d", posts.listPublishingCalls)
	}
	if svc.reconcileCalls != 3 {
		t.Errorf("Reconcile calls: want 3 (one per target), got %d", svc.reconcileCalls)
	}
	if posts.updateCalls != 0 {
		t.Errorf("UpdateStatus calls: want 0 (all in-flight), got %d", posts.updateCalls)
	}
}

// TestTickReconcile_EmptyList_NoOp covers the "nothing to do" path.
func TestTickReconcile_EmptyList_NoOp(t *testing.T) {
	posts := &mockReconcilePostStore{listPublishingFn: func() ([]models.PostTarget, error) {

		return nil, nil
	},
	}
	users := &mockUserStore{}
	svc := &mockAsyncProvider{baseMockProvider: baseMockProvider{platform: "tiktok"}}
	vault := &mockCredentialVault{}
	w := newTestReconcileWorker(posts, users, "tiktok", svc, vault)

	reconciled, failed, err := w.tickReconcile(context.Background())
	if err != nil {
		t.Fatalf("tickReconcile: %v", err)
	}
	if reconciled != 0 || failed != 0 {
		t.Errorf("counters: want (0, 0), got (%d, %d)", reconciled, failed)
	}
	if svc.reconcileCalls != 0 {
		t.Errorf("Reconcile calls: want 0 (empty list), got %d", svc.reconcileCalls)
	}
}

// TestTickReconcile_ListError_Propagates covers the "DB unreachable"
// path. tickReconcile must surface the error so the caller can log it.
func TestTickReconcile_ListError_Propagates(t *testing.T) {
	posts := &mockReconcilePostStore{listPublishingFn: func() ([]models.PostTarget, error) {

		return nil, errors.New("db down")
	},
	}
	users := &mockUserStore{}
	svc := &mockAsyncProvider{baseMockProvider: baseMockProvider{platform: "tiktok"}}
	vault := &mockCredentialVault{}
	w := newTestReconcileWorker(posts, users, "tiktok", svc, vault)

	_, _, err := w.tickReconcile(context.Background())
	if err == nil {
		t.Fatal("expected list error to propagate, got nil")
	}
}
