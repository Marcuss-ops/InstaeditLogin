package worker

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

func reconcilePolicyFixture(t *testing.T, reconcileFn func(context.Context, string, string) (*models.PublishResult, error)) (*mockReconcilePostStore, *ReconcileWorker) {
	t.Helper()
	posts := &mockReconcilePostStore{}
	users := &mockUserStore{findPlatformAccountFn: func(int64) (*models.PlatformAccount, error) {
		return &models.PlatformAccount{ID: 10, Platform: models.PlatformTikTok, PlatformUserID: "tt-1"}, nil
	}}
	provider := &mockAsyncProvider{
		baseMockProvider: baseMockProvider{platform: models.PlatformTikTok},
		reconcileFn:      reconcileFn,
	}
	vault := &mockCredentialVault{renewFn: func(context.Context, int64, string, credentials.TokenRefresher) (*models.OAuthToken, error) {
		return &models.OAuthToken{AccessToken: "token"}, nil
	}}
	return posts, newTestReconcileWorker(posts, users, models.PlatformTikTok, provider, vault)
}

func TestReconcilePolicy_RateLimitHonorsRetryAfter(t *testing.T) {
	const retryAfter = 37 * time.Second
	posts, worker := reconcilePolicyFixture(t, func(context.Context, string, string) (*models.PublishResult, error) {
		return nil, &services.ProviderError{Code: services.ErrorCodeRateLimited, Retryable: true, RetryAfter: retryAfter}
	})

	_, failed, err := worker.reconcileTarget(context.Background(), publishingTarget())
	if err != nil || failed {
		t.Fatalf("rate limit: err=%v failed=%v; want retry", err, failed)
	}
	if posts.updateCalls != 0 || posts.scheduleCalls != 1 {
		t.Fatalf("update=%d schedule=%d; want update=0 schedule=1", posts.updateCalls, posts.scheduleCalls)
	}
	if posts.scheduleIncrement[0] || posts.scheduleErrorCodes[0] != "RATE_LIMITED" {
		t.Fatalf("rate-limit diagnostics: increment=%v code=%q; want false/RATE_LIMITED", posts.scheduleIncrement[0], posts.scheduleErrorCodes[0])
	}
	delta := time.Until(posts.scheduleTimes[0])
	if delta < retryAfter-500*time.Millisecond || delta > retryAfter+500*time.Millisecond {
		t.Fatalf("delay=%v; want approximately %v", delta, retryAfter)
	}
}

func TestReconcilePolicy_RateLimitCodeOverridesFalseRetryableFlag(t *testing.T) {
	posts, worker := reconcilePolicyFixture(t, func(context.Context, string, string) (*models.PublishResult, error) {
		return nil, &services.ProviderError{Code: services.ErrorCodeRateLimited, Retryable: false, RetryAfter: 37 * time.Second}
	})

	reconciled, failed, err := worker.reconcileTarget(context.Background(), publishingTarget())
	if err != nil || reconciled || failed {
		t.Fatalf("rate limit with false Retryable: reconciled=%v failed=%v err=%v; want retry", reconciled, failed, err)
	}
	if posts.updateCalls != 0 || posts.scheduleCalls != 1 || posts.scheduleIncrement[0] {
		t.Fatalf("rate-limit classification: update=%d schedule=%d increment=%v; want 0/1/false", posts.updateCalls, posts.scheduleCalls, posts.scheduleIncrement[0])
	}
}

func TestReconcilePolicy_RateLimitDoesNotExhaustTransientBudget(t *testing.T) {
	const retryAfter = 37 * time.Second
	posts, worker := reconcilePolicyFixture(t, func(context.Context, string, string) (*models.PublishResult, error) {
		return nil, &services.ProviderError{Code: services.ErrorCodeRateLimited, Retryable: true, RetryAfter: retryAfter}
	})
	target := publishingTarget()
	target.ReconcileAttempt = reconcileMaxAttempts - 1

	reconciled, failed, err := worker.reconcileTarget(context.Background(), target)
	if err != nil || reconciled || failed {
		t.Fatalf("rate limit at budget: reconciled=%v failed=%v err=%v; want retry", reconciled, failed, err)
	}
	if posts.updateCalls != 0 || posts.scheduleCalls != 1 {
		t.Fatalf("update=%d schedule=%d; want update=0 schedule=1", posts.updateCalls, posts.scheduleCalls)
	}
	if posts.scheduleIncrement[0] || posts.scheduleErrorCodes[0] != "RATE_LIMITED" {
		t.Fatalf("rate-limit budget: increment=%v code=%q; want false/RATE_LIMITED", posts.scheduleIncrement[0], posts.scheduleErrorCodes[0])
	}
	if target.ReconcileAttempt != reconcileMaxAttempts-1 {
		t.Fatalf("rate limit consumed transient budget: got attempt %d, want %d", target.ReconcileAttempt, reconcileMaxAttempts-1)
	}
}

func TestReconcilePolicy_PermanentErrorFailsImmediately(t *testing.T) {
	posts, worker := reconcilePolicyFixture(t, func(context.Context, string, string) (*models.PublishResult, error) {
		return nil, services.NewPermanentPublishError(errors.New("channel mismatch"))
	})

	reconciled, failed, err := worker.reconcileTarget(context.Background(), publishingTarget())
	if err != nil || !reconciled || !failed {
		t.Fatalf("permanent: reconciled=%v failed=%v err=%v; want terminal failure", reconciled, failed, err)
	}
	if posts.scheduleCalls != 0 || posts.updateCalls != 1 || posts.updateTargets[0].Status != models.PostStatusFailed {
		t.Fatalf("schedule=%d update=%d status=%q; want schedule=0 update=1 failed", posts.scheduleCalls, posts.updateCalls, posts.updateTargets[0].Status)
	}
}

func TestReconcilePolicy_ExplicitTerminalStateFailsImmediately(t *testing.T) {
	posts, worker := reconcilePolicyFixture(t, func(context.Context, string, string) (*models.PublishResult, error) {
		return nil, services.NewTerminalPublishError("FAILED", errors.New("provider says FAILED"))
	})

	reconciled, failed, err := worker.reconcileTarget(context.Background(), publishingTarget())
	if err != nil || !reconciled || !failed {
		t.Fatalf("terminal: reconciled=%v failed=%v err=%v; want terminal failure", reconciled, failed, err)
	}
	if posts.scheduleCalls != 0 || posts.updateCalls != 1 {
		t.Fatalf("schedule=%d update=%d; want schedule=0 update=1", posts.scheduleCalls, posts.updateCalls)
	}
}

func TestReconcilePolicy_TimeoutAndNetworkResetRetry(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{name: "timeout", err: context.DeadlineExceeded},
		{name: "network reset", err: fmt.Errorf("status request: %w", errors.New("connection reset by peer"))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			posts, worker := reconcilePolicyFixture(t, func(context.Context, string, string) (*models.PublishResult, error) {
				return nil, tc.err
			})
			reconciled, failed, err := worker.reconcileTarget(context.Background(), publishingTarget())
			if err != nil || reconciled || failed {
				t.Fatalf("reconciled=%v failed=%v err=%v; want retry without terminal transition", reconciled, failed, err)
			}
			if posts.scheduleCalls != 1 || posts.updateCalls != 0 {
				t.Fatalf("schedule=%d update=%d; want schedule=1 update=0", posts.scheduleCalls, posts.updateCalls)
			}
			if !posts.scheduleIncrement[0] || posts.scheduleErrorCodes[0] != "RECONCILE_TRANSIENT" {
				t.Fatalf("transient diagnostics: increment=%v code=%q; want true/RECONCILE_TRANSIENT", posts.scheduleIncrement[0], posts.scheduleErrorCodes[0])
			}
		})
	}
}

func TestReconcilePolicy_TransientRetryBudgetMovesToDLQ(t *testing.T) {
	posts, worker := reconcilePolicyFixture(t, func(context.Context, string, string) (*models.PublishResult, error) {
		return nil, errors.New("503 service unavailable")
	})
	target := publishingTarget()
	target.ReconcileAttempt = reconcileMaxAttempts - 1

	reconciled, failed, err := worker.reconcileTarget(context.Background(), target)
	if err != nil || !reconciled || !failed {
		t.Fatalf("budget: reconciled=%v failed=%v err=%v; want DLQ transition", reconciled, failed, err)
	}
	if posts.scheduleCalls != 0 || posts.updateCalls != 1 {
		t.Fatalf("schedule=%d update=%d; want schedule=0 update=1", posts.scheduleCalls, posts.updateCalls)
	}
	final := posts.updateTargets[0]
	if final.Status != models.PostStatusDLQ || final.LastErrorCode != "RECONCILE_RETRY_EXHAUSTED" {
		t.Fatalf("status=%q code=%q; want dlq/RECONCILE_RETRY_EXHAUSTED", final.Status, final.LastErrorCode)
	}
}
