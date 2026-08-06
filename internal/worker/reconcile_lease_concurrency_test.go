package worker

import (
	"context"
	"sync"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

func TestReconcileTarget_ThreeWorkersOnlyOneProviderCall(t *testing.T) {
	posts := &mockReconcilePostStore{}
	var claimMu sync.Mutex
	claimed := false
	posts.claimPublishingFn = func(id int64) (bool, error) {
		claimMu.Lock()
		defer claimMu.Unlock()
		if claimed {
			return false, nil
		}
		claimed = true
		return true, nil
	}

	users := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return &models.PlatformAccount{ID: 10, Platform: "tiktok", PlatformUserID: "tt-1"}, nil
		},
	}
	provider := &mockAsyncProvider{
		baseMockProvider: baseMockProvider{platform: "tiktok"},
		reconcileFn: func(ctx context.Context, accessToken, publishID string) (*models.PublishResult, error) {
			return &models.PublishResult{PlatformMediaID: publishID}, nil
		},
	}
	vault := &mockCredentialVault{
		renewFn: func(ctx context.Context, accountID int64, tokenType string, refresh credentials.TokenRefresher) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "access-token"}, nil
		},
	}

	workers := make([]*ReconcileWorker, 3)
	for i, workerID := range []string{"worker-a", "worker-b", "worker-c"} {
		workers[i] = newTestReconcileWorker(posts, users, "tiktok", provider, vault)
		workers[i].workerID = workerID
	}

	start := make(chan struct{})
	results := make(chan struct {
		reconciled bool
		err        error
	}, len(workers))
	var wg sync.WaitGroup
	for _, worker := range workers {
		worker := worker
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			reconciled, _, err := worker.reconcileTarget(context.Background(), publishingTarget())
			results <- struct {
				reconciled bool
				err        error
			}{reconciled, err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	winners := 0
	for result := range results {
		if result.err != nil {
			t.Fatalf("concurrent reconcile: %v", result.err)
		}
		if result.reconciled {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("reconcile winners = %d, want exactly 1", winners)
	}
	provider.mu.Lock()
	providerCalls := provider.reconcileCalls
	provider.mu.Unlock()
	if providerCalls != 1 {
		t.Fatalf("provider reconcile calls = %d, want exactly 1", providerCalls)
	}
}
