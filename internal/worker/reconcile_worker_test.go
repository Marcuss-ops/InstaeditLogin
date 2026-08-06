// Package worker unit-tests for ReconcileWorker — the async-publishing
// state machine (publishing → published|failed) extracted from
// PublishWorker in Taglio 5.x. Tests this file:
//
//   - reconcileTarget  — per-target state-machine via AsyncPublisher.Reconcile
//   - tickReconcile    — list-and-reconcile batch + counter increments
//   - ReconcileWorker.Run — initial-drain + ticker + ctx-cancellable shape
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
	"strings"
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
	listPublishingFn     func() ([]models.PostTarget, error)
	listPublishingLimit  int
	listPublishingLimits []int
	scheduleCalls        int
	scheduleIDs          []int64
	scheduleTimes        []time.Time
	scheduleAttempts     []int
	scheduleFn           func(id int64, expectedAttempt int, next time.Time) error
	claimPublishingFn    func(id int64) (bool, error)
	updateStatusFn       func(*models.PostTarget) error
	dirtyPostIDsFn       func(limit int) ([]int64, error)
	repairDirtyPostFn    func(postID int64) error
	dirtyPostIDsCalls    int
	repairDirtyPostCalls int
	dirtyPostIDsLimit    int
	repairedPostIDs      []int64

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

func (m *mockReconcilePostStore) ScheduleNextReconcileWithLease(id int64, ownerID string, expectedAttempt int, next time.Time) error {
	m.mu.Lock()
	m.scheduleCalls++
	m.scheduleIDs = append(m.scheduleIDs, id)
	m.scheduleTimes = append(m.scheduleTimes, next)
	m.scheduleAttempts = append(m.scheduleAttempts, expectedAttempt)
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

// TestReconcileTarget_Failed_TransitionsToFailed covers the failure
// terminal state: Reconcile returns (nil, err) for FAILED-state AND
// for transient 5xx (both collapse to terminal per the interface
// contract). The reconciler must transition to 'failed' with the
// error message.
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
			return nil, errors.New("publish failed: tiktok returned status FAILED")
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

// TestReconcileTarget_TransientError_TerminalFailure covers the
// post-refactor behavioural change: under Reconcile's contract,
// ANY error from Reconcile — including transient 5xx — is terminal.
// The platform impl collapses both FAILED-state and transient errors
// into (nil, err); the worker treats that as a 'failed' transition.
// (Pre-refactor: transient errors left the target alone for next
// tick. The reviewer's documented choice was to trust Reconcile's
// contract; per-target retry is the outbox dispatcher's job at the
// platform-decoupled level.)
func TestReconcileTarget_TransientError_TerminalFailure(t *testing.T) {
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
		t.Fatalf("reconcileTarget: %v (reconciler surface error must NOT propagate as tick error)", err)
	}
	if !reconciled || !wasFailed {
		t.Errorf("reconciled=%v wasFailed=%v: want (true, true) — transient errors are terminal under Reconcile's contract", reconciled, wasFailed)
	}
	if posts.claimPublishingCalls != 1 {
		t.Errorf("ClaimPublishingTarget calls: want 1 (claim always fires first), got %d", posts.claimPublishingCalls)
	}
	if posts.updateCalls != 1 {
		t.Errorf("UpdateStatus calls: want 1 (transition publishing→failed), got %d", posts.updateCalls)
	}
	final := posts.updateTargets[0]
	if final.Status != models.PostStatusFailed {
		t.Errorf("final status: want failed, got %q", final.Status)
	}
	if !strings.Contains(final.ErrorMessage, "502") {
		t.Errorf("ErrorMessage should propagate the platform error: %q", final.ErrorMessage)
	}
	if final.ProviderState != "FAILED" {
		t.Errorf("provider_state: want FAILED, got %q", final.ProviderState)
	}
}

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
