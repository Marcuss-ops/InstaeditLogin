package worker

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// ------------------------------------------------------------------
// publishTarget tests — rate-limit requeue path (OPEN GAP closure,
// ARCHITECTURE.md §Rate limiting (d)). When the FINAL platform
// publish call answers 429/Retry-After, the worker must NOT take
// the terminal markFailed path: it requeues the target via
// MarkRateLimitedRetry with next_attempt_at = NOW() + Retry-After.
// ------------------------------------------------------------------

// rateLimitTestFixture wires the standard happy-path mocks with a
// publisher that fails with the supplied error. Mirrors the fixture
// shape used across publish_worker_publish_test.go.
func rateLimitTestFixture(pubErr error) (*mockPostStore, *mockProvider, *PublishWorker) {
	posts := &mockPostStore{
		claimFn: func(id int64) (bool, error) { return true, nil },
		findByIDFn: func(id int64) (*models.Post, error) {
			return &models.Post{
				ID:          100,
				WorkspaceID: 1,
				Title:       "Hello",
				Caption:     "World",
				MediaURL:    "https://cdn.example.com/video.mp4",
				Status:      models.PostStatusScheduled,
			}, nil
		},
	}
	users := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return &models.PlatformAccount{
				ID:             10,
				UserID:         1,
				Platform:       "instagram",
				PlatformUserID: "fb-123",
			}, nil
		},
	}
	svc := &mockProvider{
		baseMockProvider: baseMockProvider{platform: "instagram"},
		publishFn: func(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (*models.PublishResult, error) {
			return nil, pubErr
		},
	}
	vault := &mockCredentialVault{
		renewFn: func(ctx context.Context, accountID int64, tokenType string, refresh credentials.TokenRefresher) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "fresh-tok", TokenType: "bearer"}, nil
		},
	}
	w := newTestWorker(posts, users, "instagram", svc, vault)
	return posts, svc, w
}

// TestPublishTarget_RateLimitError_RequeuesWithRetryAfter covers the
// canonical *services.RateLimitError shape: the platform said "come
// back in 90s", so the worker must reschedule via MarkRateLimitedRetry
// with next_attempt_at ≈ NOW()+90s and must NOT write a terminal
// status through UpdateStatus.
func TestPublishTarget_RateLimitError_RequeuesWithRetryAfter(t *testing.T) {
	const retryAfter = 90 * time.Second
	posts, svc, w := rateLimitTestFixture(&services.RateLimitError{RetryAfter: retryAfter})

	before := time.Now()
	if err := w.publishTarget(context.Background(), scheduledTarget()); err != nil {
		t.Fatalf("publishTarget: want nil (rescheduled rate-limit is not a tick error), got %v", err)
	}
	after := time.Now()

	if svc.publishCalls != 1 {
		t.Errorf("Publish calls: want 1, got %d", svc.publishCalls)
	}
	if posts.markRateLimitedRetryCalls != 1 {
		t.Fatalf("MarkRateLimitedRetry calls: want 1, got %d", posts.markRateLimitedRetryCalls)
	}
	// The scheduled timestamp must be NOW()+RetryAfter within the
	// test's own wall-clock window.
	got := posts.markRateLimitedRetryAts[0]
	if got.Before(before.Add(retryAfter)) || got.After(after.Add(retryAfter)) {
		t.Errorf("next_attempt_at: want within [%v, %v], got %v",
			before.Add(retryAfter), after.Add(retryAfter), got)
	}
	if !strings.Contains(posts.markRateLimitedRetryErrs[0], "rate limited") {
		t.Errorf("persisted error: want rate-limit prose, got %q", posts.markRateLimitedRetryErrs[0])
	}
	// The terminal path must NOT fire: no UpdateStatus write
	// (markFailed would have captured a 'failed' target).
	if posts.updateCalls != 0 {
		t.Errorf("UpdateStatus calls: want 0 (no terminal transition on rate-limit), got %d", posts.updateCalls)
	}
}

// TestPublishTarget_RateLimitProviderError_RequeuesWithRetryAfter
// covers the canonical *services.ProviderError shape (SPRINT 5.1
// contract) with Code == rate_limited: the worker must detect it via
// IsRateLimitError and use its RetryAfter hint.
func TestPublishTarget_RateLimitProviderError_RequeuesWithRetryAfter(t *testing.T) {
	const retryAfter = 2 * time.Minute
	pubErr := &services.ProviderError{
		Code:        services.ErrorCodeRateLimited,
		Platform:    "instagram",
		Retryable:   true,
		RetryAfter:  retryAfter,
		SafeMessage: "platform throttled the publish call",
		StatusCode:  429,
	}
	posts, _, w := rateLimitTestFixture(pubErr)

	before := time.Now()
	if err := w.publishTarget(context.Background(), scheduledTarget()); err != nil {
		t.Fatalf("publishTarget: want nil, got %v", err)
	}
	after := time.Now()

	if posts.markRateLimitedRetryCalls != 1 {
		t.Fatalf("MarkRateLimitedRetry calls: want 1, got %d", posts.markRateLimitedRetryCalls)
	}
	got := posts.markRateLimitedRetryAts[0]
	if got.Before(before.Add(retryAfter)) || got.After(after.Add(retryAfter)) {
		t.Errorf("next_attempt_at: want within [%v, %v], got %v",
			before.Add(retryAfter), after.Add(retryAfter), got)
	}
	if posts.updateCalls != 0 {
		t.Errorf("UpdateStatus calls: want 0, got %d", posts.updateCalls)
	}
}

// TestPublishTarget_RateLimitError_ZeroHint_UsesDefaultBackoff covers
// the documented RetryAfter=0 contract ("a zero RetryAfter is a
// programming error in the provider; the worker falls back to the
// default backoff"): the reschedule must land at NOW()+
// defaultRateLimitBackoff instead of NOW() (which would busy-loop the
// driver against a still-closed window).
func TestPublishTarget_RateLimitError_ZeroHint_UsesDefaultBackoff(t *testing.T) {
	posts, _, w := rateLimitTestFixture(&services.RateLimitError{RetryAfter: 0})

	before := time.Now()
	if err := w.publishTarget(context.Background(), scheduledTarget()); err != nil {
		t.Fatalf("publishTarget: want nil, got %v", err)
	}
	after := time.Now()

	if posts.markRateLimitedRetryCalls != 1 {
		t.Fatalf("MarkRateLimitedRetry calls: want 1, got %d", posts.markRateLimitedRetryCalls)
	}
	got := posts.markRateLimitedRetryAts[0]
	if got.Before(before.Add(defaultRateLimitBackoff)) || got.After(after.Add(defaultRateLimitBackoff)) {
		t.Errorf("next_attempt_at: want NOW()+%v (default backoff), got %v (now=%v)",
			defaultRateLimitBackoff, got, before)
	}
}

// TestPublishTarget_RateLimitError_InMemoryTargetMirrorsRequeue
// asserts the in-memory target mirrors the DB-side requeue (status
// back to queued, attempt_count bumped, RATE_LIMITED code) so any
// downstream reader of the struct in the same tick sees the stamped
// values — the same mirroring convention used for
// ProviderIdempotencyKey in publishTarget.
func TestPublishTarget_RateLimitError_InMemoryTargetMirrorsRequeue(t *testing.T) {
	_, _, w := rateLimitTestFixture(&services.RateLimitError{RetryAfter: time.Minute})

	target := scheduledTarget()
	if err := w.publishTarget(context.Background(), target); err != nil {
		t.Fatalf("publishTarget: want nil, got %v", err)
	}

	if target.Status != models.PostStatusQueued {
		t.Errorf("in-memory status: want queued, got %q", target.Status)
	}
	if target.AttemptCount != 1 {
		t.Errorf("in-memory attempt_count: want 1, got %d", target.AttemptCount)
	}
	if target.NextAttemptAt == nil {
		t.Error("in-memory next_attempt_at: want non-nil, got nil")
	}
	if target.LastErrorCode != "RATE_LIMITED" {
		t.Errorf("in-memory last_error_code: want RATE_LIMITED, got %q", target.LastErrorCode)
	}
}

// TestPublishTarget_RateLimitError_RescheduleDBFailure_SurfacesError
// covers the degraded path: the platform said 429 AND the requeue
// UPDATE failed. The worker must surface an error (so the tick error
// counter increments and the operator sees the stall) that carries
// BOTH failure reasons.
func TestPublishTarget_RateLimitError_RescheduleDBFailure_SurfacesError(t *testing.T) {
	posts, _, w := rateLimitTestFixture(&services.RateLimitError{RetryAfter: time.Minute})
	posts.markRateLimitedRetryFn = func(id int64, nextAttemptAt time.Time, lastError string) error {
		return errors.New("db connection lost")
	}

	err := w.publishTarget(context.Background(), scheduledTarget())
	if err == nil {
		t.Fatal("publishTarget: want error when the rate-limit reschedule fails, got nil")
	}
	if !strings.Contains(err.Error(), "db connection lost") {
		t.Errorf("error: want the reschedule failure surfaced, got %v", err)
	}
	if !strings.Contains(err.Error(), "rate limited") {
		t.Errorf("error: want the underlying rate-limit cause preserved, got %v", err)
	}
}

// TestPublishTarget_NonRateLimitError_StillMarksFailed pins the
// existing terminal contract: a generic publish error must keep
// routing through markFailed (status='failed' via UpdateStatus), NOT
// the new requeue path. Guards against over-matching in the
// IsRateLimitError branch.
func TestPublishTarget_NonRateLimitError_StillMarksFailed(t *testing.T) {
	posts, _, w := rateLimitTestFixture(errors.New("platform exploded"))

	err := w.publishTarget(context.Background(), scheduledTarget())
	if err == nil {
		t.Fatal("publishTarget: want error on terminal publish failure, got nil")
	}
	if posts.markRateLimitedRetryCalls != 0 {
		t.Errorf("MarkRateLimitedRetry calls: want 0 for a non-rate-limit error, got %d", posts.markRateLimitedRetryCalls)
	}
	if posts.updateCalls != 1 {
		t.Fatalf("UpdateStatus calls: want 1 (terminal markFailed), got %d", posts.updateCalls)
	}
	if got := posts.updateTargets[0].Status; got != models.PostStatusFailed {
		t.Errorf("final status: want failed, got %q", got)
	}
}
