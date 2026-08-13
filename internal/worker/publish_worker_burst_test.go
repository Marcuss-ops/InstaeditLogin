package worker

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// TestTick_ParallelBurst_SaturatesPoolAndProcessesAll verifies the
// burst behaviour: N targets scheduled at the same minute are drained
// by a bounded worker pool (publishConcurrency), NOT a sequential
// for-loop. The test blocks every publishFn until the pool is
// saturated, then releases — if tick() serialized the batch it could
// never reach more than 1 in-flight publish; if the pool bound is
// broken it would exceed it.
func TestTick_ParallelBurst_SaturatesPoolAndProcessesAll(t *testing.T) {
	const targetCount = 8
	const poolSize = 4

	var inFlight, maxInFlight atomic.Int32
	started := make(chan struct{}, targetCount)
	release := make(chan struct{})

	posts := &mockPostStore{
		listPendingFn: func(before time.Time) ([]models.PostTarget, error) {
			targets := make([]models.PostTarget, targetCount)
			for i := range targets {
				targets[i] = models.PostTarget{ID: int64(100 + i), PostID: 1000, PlatformAccountID: 10, Status: models.PostStatusScheduled}
			}
			return targets, nil
		},
		findByIDFn: func(id int64) (*models.Post, error) {
			return &models.Post{ID: 1000, Caption: "burst"}, nil
		},
		claimFn:  func(id int64) (bool, error) { return true, nil },
		setKeyFn: func(id int64, key string) error { return nil },
	}
	users := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return &models.PlatformAccount{ID: 10, Platform: "instagram", PlatformUserID: "burst-1"}, nil
		},
	}
	svc := &mockProvider{
		baseMockProvider: baseMockProvider{platform: "instagram"},
		publishFn: func(_ context.Context, _, _ string, _ models.PublishPayload) (*models.PublishResult, error) {
			cur := inFlight.Add(1)
			for {
				peak := maxInFlight.Load()
				if cur <= peak || maxInFlight.CompareAndSwap(peak, cur) {
					break
				}
			}
			started <- struct{}{}
			<-release // block until the test releases the whole batch
			inFlight.Add(-1)
			return &models.PublishResult{PlatformMediaID: "burst-media"}, nil
		},
	}
	vault := &mockCredentialVault{
		renewFn: func(ctx context.Context, accountID int64, tokenType string, refresh credentials.TokenRefresher) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "t"}, nil
		},
	}
	w := newTestWorkerWithoutThrottle(posts, users, "instagram", svc, vault)
	w.SetPublishConcurrency(poolSize)

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _, _, _ = w.tick(context.Background())
	}()

	// Wait until the pool is saturated: poolSize publishes in flight.
	for i := 0; i < poolSize; i++ {
		select {
		case <-started:
		case <-done:
			t.Fatal("tick finished before the pool saturated — targets serialized")
		}
	}
	if got := maxInFlight.Load(); got > poolSize {
		t.Fatalf("max concurrent publishes=%d, want <= %d (pool bound violated)", got, poolSize)
	}
	if got := maxInFlight.Load(); got < 2 {
		t.Fatalf("max concurrent publishes=%d, want >= 2 (batch was processed sequentially)", got)
	}

	// Release the batch; the remaining targets drain through the pool.
	close(release)
	for i := poolSize; i < targetCount; i++ {
		select {
		case <-started:
		case <-done:
			t.Fatal("tick finished before all targets started publishing")
		}
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("tick did not return after releasing the batch")
	}

	if got := svc.publishCalls; got != targetCount {
		t.Errorf("publish calls=%d, want %d", got, targetCount)
	}
	if got := posts.updateCalls; got != targetCount {
		t.Errorf("UpdateStatus calls=%d, want %d (every target terminal-updated)", got, targetCount)
	}
	if got := posts.listPendingCalls; got != 1 {
		t.Errorf("ListPending calls=%d, want 1", got)
	}
}

// TestTick_ConcurrencyOne_SequentialFastPath pins the legacy behaviour:
// SetPublishConcurrency(1) restores the sequential drain, still
// processing every target exactly once.
func TestTick_ConcurrencyOne_SequentialFastPath(t *testing.T) {
	const targetCount = 3

	posts := &mockPostStore{
		listPendingFn: func(before time.Time) ([]models.PostTarget, error) {
			targets := make([]models.PostTarget, targetCount)
			for i := range targets {
				targets[i] = models.PostTarget{ID: int64(200 + i), PostID: 2000, PlatformAccountID: 10, Status: models.PostStatusScheduled}
			}
			return targets, nil
		},
		findByIDFn: func(id int64) (*models.Post, error) {
			return &models.Post{ID: 2000, Caption: "seq"}, nil
		},
		claimFn:  func(id int64) (bool, error) { return true, nil },
		setKeyFn: func(id int64, key string) error { return nil },
	}
	users := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return &models.PlatformAccount{ID: 10, Platform: "instagram", PlatformUserID: "seq-1"}, nil
		},
	}
	svc := &mockProvider{
		baseMockProvider: baseMockProvider{platform: "instagram"},
		publishFn: func(_ context.Context, _, _ string, _ models.PublishPayload) (*models.PublishResult, error) {
			return &models.PublishResult{PlatformMediaID: "seq-media"}, nil
		},
	}
	vault := &mockCredentialVault{
		renewFn: func(ctx context.Context, accountID int64, tokenType string, refresh credentials.TokenRefresher) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "t"}, nil
		},
	}
	w := newTestWorkerWithoutThrottle(posts, users, "instagram", svc, vault)
	w.SetPublishConcurrency(1)

	processed, succeeded, failed, err := w.tick(context.Background())
	if err != nil {
		t.Fatalf("tick: %v", err)
	}
	if processed != targetCount || succeeded != targetCount || failed != 0 {
		t.Errorf("tick counts: processed=%d succeeded=%d failed=%d, want %d/%d/0", processed, succeeded, failed, targetCount, targetCount)
	}
	if svc.publishCalls != targetCount {
		t.Errorf("publish calls=%d, want %d", svc.publishCalls, targetCount)
	}
}
