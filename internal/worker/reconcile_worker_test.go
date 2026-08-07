// Package worker unit-tests for ReconcileWorker — the async-publishing
// state machine (publishing → published|failed) extracted from
// PublishWorker in Taglio 5.x. This file covers the per-target
// reconcileTarget state machine via AsyncPublisher.Reconcile; the
// tickReconcile batch and the Run/RunOnce loops live in
// reconcile_worker_tick_test.go and reconcile_worker_run_test.go.
//
// All tests use the mocks in mocks_test.go (mockUserStore,
// mockAsyncProvider, mockProvider, mockCredentialVault) plus the
// mockReconcilePostStore defined here (bounded dirty-queue + publishing
// surface). The fact that the reconciler cannot accidentally call
// SetProviderIdempotencyKey / ClaimQueuedTarget is enforced at compile
// time by the ReconcilePostStore interface boundary.

package worker

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// ------------------------------------------------------------------
// mockReconcilePostStore — the narrow interface ReconcileWorker
// depends on. Distinct from mockPostStore (publish_worker_test.go)
// because the reconciler has different surface needs: only the
// read (ListPublishing) and the status mutation (UpdateStatus).
// The driver-side mutations (claim, find, stamp key) belong to the
// publish driver, not the reconciler.
// ------------------------------------------------------------------

type mockReconcilePostStore struct {
	mu sync.Mutex // guards counters + captured slices (read by test goroutine while written by worker goroutine)

	// Call counters — one per method, incremented on every invocation.
	listPublishingCalls  int
	claimPublishingCalls int
	updateCalls          int

	// Function fields — each test overrides only what it exercises.
	listPublishingFn      func() ([]models.PostTarget, error)
	listPublishingLimit   int
	listPublishingLimits  []int
	scheduleCalls         int
	scheduleIDs           []int64
	scheduleTimes         []time.Time
	scheduleAttempts      []int
	scheduleIncrement     []bool
	scheduleErrorCodes    []string
	scheduleErrorMessages []string
	scheduleFn            func(id int64, expectedAttempt int, next time.Time) error
	claimPublishingFn     func(id int64) (bool, error)
	updateStatusFn        func(*models.PostTarget) error
	dirtyPostIDsFn        func(limit int) ([]int64, error)
	repairDirtyPostFn     func(postID int64) error
	dirtyPostIDsCalls     int
	repairDirtyPostCalls  int
	dirtyPostIDsLimit     int
	repairedPostIDs       []int64

	// Captured targets from UpdateStatus — lets tests inspect the
	// final status (published vs failed) and assert on the worker
	// writing the right terminal state. Stored as struct values
	// (not pointers) so later mutations to the caller's target
	// don't leak into the captured snapshot.
	updateTargets []models.PostTarget
}

func (m *mockReconcilePostStore) ListPublishing(limit int) ([]models.PostTarget, error) {
	m.mu.Lock()
	m.listPublishingCalls++
	m.listPublishingLimit = limit
	m.listPublishingLimits = append(m.listPublishingLimits, limit)
	m.mu.Unlock()
	if m.listPublishingFn == nil {
		return nil, nil
	}
	return m.listPublishingFn()
}

func (m *mockReconcilePostStore) ClaimPublishingTarget(id int64, ownerID string, leaseTTL time.Duration) (bool, error) {
	m.mu.Lock()
	m.claimPublishingCalls++
	m.mu.Unlock()
	if m.claimPublishingFn == nil {
		return true, nil // default: claim always succeeds
	}
	return m.claimPublishingFn(id)
}

func (m *mockReconcilePostStore) HeartbeatReconcileTarget(id int64, ownerID string, leaseTTL time.Duration) error {
	return nil
}

func (m *mockReconcilePostStore) ReleaseReconcileTarget(id int64, ownerID string) error {
	return nil
}

func (m *mockReconcilePostStore) ScheduleNextReconcileWithLease(id int64, ownerID string, expectedAttempt int, next time.Time, incrementAttempt bool, errorCode, errorMessage string) error {
	m.mu.Lock()
	m.scheduleCalls++
	m.scheduleIDs = append(m.scheduleIDs, id)
	m.scheduleTimes = append(m.scheduleTimes, next)
	m.scheduleAttempts = append(m.scheduleAttempts, expectedAttempt)
	m.scheduleIncrement = append(m.scheduleIncrement, incrementAttempt)
	m.scheduleErrorCodes = append(m.scheduleErrorCodes, errorCode)
	m.scheduleErrorMessages = append(m.scheduleErrorMessages, errorMessage)
	m.mu.Unlock()
	if m.scheduleFn == nil {
		return nil
	}
	return m.scheduleFn(id, expectedAttempt, next)
}

func (m *mockReconcilePostStore) UpdateReconcileStatusWithLease(target *models.PostTarget, ownerID string) error {
	m.mu.Lock()
	m.updateCalls++
	m.updateTargets = append(m.updateTargets, *target)
	m.mu.Unlock()
	if m.updateStatusFn == nil {
		return nil
	}
	return m.updateStatusFn(target)
}

func (m *mockReconcilePostStore) ListDirtyAggregatePostIDs(limit int) ([]int64, error) {
	m.mu.Lock()
	m.dirtyPostIDsCalls++
	m.dirtyPostIDsLimit = limit
	m.mu.Unlock()
	if m.dirtyPostIDsFn == nil {
		return nil, nil
	}
	return m.dirtyPostIDsFn(limit)
}

func (m *mockReconcilePostStore) RepairDirtyAggregatePost(postID int64) error {
	m.mu.Lock()
	m.repairDirtyPostCalls++
	m.repairedPostIDs = append(m.repairedPostIDs, postID)
	m.mu.Unlock()
	if m.repairDirtyPostFn == nil {
		return nil
	}
	return m.repairDirtyPostFn(postID)
}

// ------------------------------------------------------------------
// reconcileTarget tests
// ------------------------------------------------------------------

// TestReconcileTarget_PublishComplete_TransitionsToPublished covers
// the happy terminal state: Reconcile returns (*PublishResult, nil)
// corresponding to PUBLISH_COMPLETE upstream, the reconciler must
// transition the target from 'publishing' to 'published' with a
// non-nil published_at. The mock's CheckPublishStatus must NOT be
// reached — Reconcile is the new entrypoint and wraps it.
func TestReconcileTarget_PublishComplete_TransitionsToPublished(t *testing.T) {
	posts := &mockReconcilePostStore{}
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
	w := newTestReconcileWorker(posts, users, "tiktok", svc, vault)

	reconciled, wasFailed, err := w.reconcileTarget(context.Background(), publishingTarget())
	if err != nil {
		t.Fatalf("reconcileTarget: %v", err)
	}
	if !reconciled {
		t.Error("reconciled: want true (success result is a terminal transition), got false")
	}
	if wasFailed {
		t.Error("wasFailed: want false (success, not failure)")
	}
	// FASE 1.1: claim must fire exactly once before any downstream work.
	if posts.claimPublishingCalls != 1 {
		t.Errorf("ClaimPublishingTarget calls: want 1, got %d", posts.claimPublishingCalls)
	}
	// UpdateStatus should have been called once with status=published.
	if posts.updateCalls != 1 {
		t.Errorf("UpdateStatus calls: want 1, got %d", posts.updateCalls)
	}
	final := posts.updateTargets[0]
	if final.Status != models.PostStatusPublished {
		t.Errorf("final status: want published, got %q", final.Status)
	}
	if final.PublishedAt == nil {
		t.Error("published_at: want non-nil, got nil (reconciler must stamp publish time on success)")
	}
	if final.PlatformPostID != "publish-id-abc" {
		t.Errorf("platform_post_id: want publish-id-abc (carried over), got %q", final.PlatformPostID)
	}
	// provider_state must be stamped on the terminal transition.
	if final.ProviderState != "PUBLISH_COMPLETE" {
		t.Errorf("provider_state: want PUBLISH_COMPLETE, got %q", final.ProviderState)
	}
	// Reconcile called exactly once. CheckPublishStatus MUST NOT be reached
	// (Reconcile is the new entrypoint; the old in-flight string path is
	// gone from reconcileTarget).
	if svc.reconcileCalls != 1 {
		t.Errorf("Reconcile calls: want 1, got %d", svc.reconcileCalls)
	}
	if svc.checkStatusCalls != 0 {
		t.Errorf("CheckPublishStatus calls: want 0 (worker no longer calls it directly), got %d", svc.checkStatusCalls)
	}
}

// TestReconcileTarget_Failed_TransitionsToFailed covers an explicit
// provider FAILED state. The terminal marker is distinct from transport
// errors so an authoritative provider failure is still final.
func TestReconcileTarget_Failed_TransitionsToFailed(t *testing.T) {
	posts := &mockReconcilePostStore{}
	users := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return &models.PlatformAccount{ID: 10, Platform: "tiktok", PlatformUserID: "tt-1"}, nil
		},
	}
	svc := &mockAsyncProvider{
		baseMockProvider: baseMockProvider{platform: "tiktok"},
		reconcileFn: func(ctx context.Context, accessToken, publishID string) (*models.PublishResult, error) {
			return nil, services.NewTerminalPublishError("FAILED", errors.New("publish failed: tiktok returned status FAILED"))
		},
	}
	vault := &mockCredentialVault{
		renewFn: func(ctx context.Context, accountID int64, tokenType string, refresh credentials.TokenRefresher) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "t"}, nil
		},
	}
	w := newTestReconcileWorker(posts, users, "tiktok", svc, vault)

	reconciled, wasFailed, err := w.reconcileTarget(context.Background(), publishingTarget())
	if err != nil {
		t.Fatalf("reconcileTarget: %v", err)
	}
	if !reconciled || !wasFailed {
		t.Errorf("reconciled=%v wasFailed=%v: want (true, true)", reconciled, wasFailed)
	}
	if posts.claimPublishingCalls != 1 {
		t.Errorf("ClaimPublishingTarget calls: want 1, got %d", posts.claimPublishingCalls)
	}
	if posts.updateCalls != 1 {
		t.Errorf("UpdateStatus calls: want 1, got %d", posts.updateCalls)
	}
	final := posts.updateTargets[0]
	if final.Status != models.PostStatusFailed {
		t.Errorf("final status: want failed, got %q", final.Status)
	}
	if final.ErrorMessage == "" {
		t.Error("ErrorMessage should be populated with the failure reason for debugging")
	}
	if final.PublishedAt != nil {
		t.Error("PublishedAt should remain nil on failure")
	}
	// provider_state must be stamped on the terminal transition.
	if final.ProviderState != "FAILED" {
		t.Errorf("provider_state: want FAILED, got %q", final.ProviderState)
	}
	if svc.reconcileCalls != 1 {
		t.Errorf("Reconcile calls: want 1, got %d", svc.reconcileCalls)
	}
}

// TestReconcileTarget_InFlight_LeavesStatusUnchanged covers the
// in-flight case: Reconcile returns (nil, nil) — the platform's
// PublishID is still in PROCESSING_UPLOAD/PENDING_PUBLISH/IN_REVIEW.
// The reconciler MUST leave status='publishing' and try again next
// tick. provider_state is intentionally NOT written on in-flight
// (no state string is exposed through Reconcile's contract; the
// column becomes a terminal-state log rather than a per-tick snapshot).
func TestReconcileTarget_InFlight_LeavesStatusUnchanged(t *testing.T) {
	posts := &mockReconcilePostStore{}
	users := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return &models.PlatformAccount{ID: 10, Platform: "tiktok", PlatformUserID: "tt-1"}, nil
		},
	}
	svc := &mockAsyncProvider{
		baseMockProvider: baseMockProvider{platform: "tiktok"},
		reconcileFn: func(ctx context.Context, accessToken, publishID string) (*models.PublishResult, error) {
			return nil, nil // in-flight
		},
	}
	vault := &mockCredentialVault{
		renewFn: func(ctx context.Context, accountID int64, tokenType string, refresh credentials.TokenRefresher) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "t"}, nil
		},
	}
	w := newTestReconcileWorker(posts, users, "tiktok", svc, vault)

	reconciled, wasFailed, err := w.reconcileTarget(context.Background(), publishingTarget())
	if err != nil {
		t.Fatalf("reconcileTarget: %v", err)
	}
	if reconciled || wasFailed {
		t.Errorf("reconciled=%v wasFailed=%v: want (false, false) for in-flight", reconciled, wasFailed)
	}
	if posts.claimPublishingCalls != 1 {
		t.Errorf("ClaimPublishingTarget calls: want 1 (claim always fires first), got %d", posts.claimPublishingCalls)
	}
	if posts.updateCalls != 0 {
		t.Errorf("UpdateStatus calls: want 0 (in-flight, no transition), got %d", posts.updateCalls)
	}
	if svc.reconcileCalls != 1 {
		t.Errorf("Reconcile calls: want 1, got %d", svc.reconcileCalls)
	}
}

// TestReconcileTarget_SyncPlatform_LeavesAlone covers the case
// where the platform doesn't have the AsyncPublisher capability
// (e.g. Instagram — it completes its publish in the driver's
// publishTarget() call, no polling needed). The reconciler must
// not touch these targets.
func TestReconcileTarget_SyncPlatform_LeavesAlone(t *testing.T) {
	posts := &mockReconcilePostStore{}
	users := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return &models.PlatformAccount{ID: 10, Platform: "instagram", PlatformUserID: "fb-1"}, nil
		},
	}
	// Instagram mockProvider has NO AsyncPublisher methods, so the
	// router.AsyncPublisher lookup returns (nil, false) and the
	// reconciler should no-op.
	svc := &mockProvider{
		baseMockProvider: baseMockProvider{platform: "instagram"},
	}
	vault := &mockCredentialVault{}
	w := newTestReconcileWorker(posts, users, "instagram", svc, vault)

	reconciled, wasFailed, err := w.reconcileTarget(context.Background(), publishingTarget())
	if err != nil {
		t.Fatalf("reconcileTarget: %v", err)
	}
	if reconciled || wasFailed {
		t.Errorf("reconciled=%v wasFailed=%v: want (false, false) for sync platform", reconciled, wasFailed)
	}
	if posts.claimPublishingCalls != 1 {
		t.Errorf("ClaimPublishingTarget calls: want 1 (claim always fires first), got %d", posts.claimPublishingCalls)
	}
	if posts.updateCalls != 0 {
		t.Errorf("UpdateStatus calls: want 0 (sync platform, no transition), got %d", posts.updateCalls)
	}
	if svc.publishCalls != 0 {
		t.Errorf("Publish calls: want 0 (sync platform, no polling), got %d", svc.publishCalls)
	}
	if vault.ensureCalls != 0 {
		t.Errorf("Renew calls: want 0 (sync platform, no token refresh), got %d", vault.ensureCalls)
	}
}

// TestReconcileTarget_OrphanAccount_MarksFailed covers the
// "platform_account disappeared" failure mode:
// FindPlatformAccountByID returns (nil, nil). The reconciler must
// mark the target 'failed' so it doesn't loop forever.
func TestReconcileTarget_OrphanAccount_MarksFailed(t *testing.T) {
	posts := &mockReconcilePostStore{}
	users := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return nil, nil // vanished
		},
	}
	svc := &mockAsyncProvider{baseMockProvider: baseMockProvider{platform: "tiktok"}}
	vault := &mockCredentialVault{}
	w := newTestReconcileWorker(posts, users, "tiktok", svc, vault)

	reconciled, wasFailed, err := w.reconcileTarget(context.Background(), publishingTarget())
	if err != nil {
		t.Fatalf("reconcileTarget: %v", err)
	}
	if !reconciled || !wasFailed {
		t.Errorf("reconciled=%v wasFailed=%v: want (true, true) for orphan account", reconciled, wasFailed)
	}
	if posts.claimPublishingCalls != 1 {
		t.Errorf("ClaimPublishingTarget calls: want 1 (claim always fires first), got %d", posts.claimPublishingCalls)
	}
	if posts.updateCalls != 1 {
		t.Errorf("UpdateStatus calls: want 1, got %d", posts.updateCalls)
	}
	final := posts.updateTargets[0]
	if final.Status != models.PostStatusFailed {
		t.Errorf("final status: want failed, got %q", final.Status)
	}
	if final.ErrorMessage == "" {
		t.Error("ErrorMessage should explain why the target was failed (orphan account)")
	}
	if final.ProviderState != "FAILED" {
		t.Errorf("provider_state: want FAILED, got %q", final.ProviderState)
	}
}

// TestReconcileTarget_ClaimLoss_SkipsWithoutSideEffects covers the
// FASE 1.1 claim-loss path: when ClaimPublishingTarget returns false
// (another reconciler replica already claimed the row via SKIP LOCKED),
// the reconciler MUST skip the target without loading the account,
// refreshing the token, or calling Reconcile.
func TestReconcileTarget_ClaimLoss_SkipsWithoutSideEffects(t *testing.T) {
	posts := &mockReconcilePostStore{
		claimPublishingFn: func(id int64) (bool, error) {
			return false, nil // another reconciler already claimed
		},
	}
	users := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			t.Error("FindPlatformAccountByID called despite claim loss")
			return nil, nil
		},
	}
	svc := &mockAsyncProvider{baseMockProvider: baseMockProvider{platform: "tiktok"}}
	vault := &mockCredentialVault{
		renewFn: func(ctx context.Context, accountID int64, tokenType string, refresh credentials.TokenRefresher) (*models.OAuthToken, error) {
			t.Error("Renew called despite claim loss")
			return nil, nil
		},
	}
	w := newTestReconcileWorker(posts, users, "tiktok", svc, vault)

	reconciled, wasFailed, err := w.reconcileTarget(context.Background(), publishingTarget())
	if err != nil {
		t.Fatalf("reconcileTarget: claim-loss should be nil (skip, not failure), got %v", err)
	}
	if reconciled || wasFailed {
		t.Errorf("reconciled=%v wasFailed=%v: want (false, false) on claim loss", reconciled, wasFailed)
	}
	if posts.claimPublishingCalls != 1 {
		t.Errorf("ClaimPublishingTarget calls: want 1, got %d", posts.claimPublishingCalls)
	}
	// No downstream calls.
	if posts.updateCalls != 0 {
		t.Errorf("UpdateStatus calls: want 0 (claim-loss skips), got %d", posts.updateCalls)
	}
	if svc.reconcileCalls != 0 {
		t.Errorf("Reconcile calls: want 0 (claim-loss skips), got %d", svc.reconcileCalls)
	}
	if vault.ensureCalls != 0 {
		t.Errorf("Renew calls: want 0 (claim-loss skips), got %d", vault.ensureCalls)
	}
}

// TestReconcileTarget_TransientError_SchedulesRetry covers a plain
// provider transport error (the shape used by timeout/reset paths).
// It must leave the target publishing and advance the lease-protected
// reconcile schedule rather than marking the target failed.
func TestReconcileTarget_TransientError_SchedulesRetry(t *testing.T) {
	posts := &mockReconcilePostStore{}
	users := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return &models.PlatformAccount{ID: 10, Platform: "tiktok", PlatformUserID: "tt-1"}, nil
		},
	}
	svc := &mockAsyncProvider{
		baseMockProvider: baseMockProvider{platform: "tiktok"},
		reconcileFn: func(ctx context.Context, accessToken, publishID string) (*models.PublishResult, error) {
			return nil, errors.New("502 bad gateway from tiktok")
		},
	}
	vault := &mockCredentialVault{
		renewFn: func(ctx context.Context, accountID int64, tokenType string, refresh credentials.TokenRefresher) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "t"}, nil
		},
	}
	w := newTestReconcileWorker(posts, users, "tiktok", svc, vault)

	reconciled, wasFailed, err := w.reconcileTarget(context.Background(), publishingTarget())
	if err != nil {
		t.Fatalf("reconcileTarget: %v", err)
	}
	if reconciled || wasFailed {
		t.Errorf("reconciled=%v wasFailed=%v: want (false, false) — transient errors must be retried", reconciled, wasFailed)
	}
	if posts.claimPublishingCalls != 1 {
		t.Errorf("ClaimPublishingTarget calls: want 1, got %d", posts.claimPublishingCalls)
	}
	if posts.updateCalls != 0 {
		t.Errorf("UpdateStatus calls: want 0 (transient retry keeps publishing), got %d", posts.updateCalls)
	}
	if posts.scheduleCalls != 1 {
		t.Errorf("ScheduleNextReconcile calls: want 1, got %d", posts.scheduleCalls)
	}
	if posts.scheduleAttempts[0] != 0 {
		t.Errorf("scheduled attempt: want 0, got %d", posts.scheduleAttempts[0])
	}
	if posts.scheduleTimes[0].Before(time.Now().Add(4 * time.Second)) {
		t.Errorf("transient retry scheduled too early: %v", posts.scheduleTimes[0])
	}
}
