// Package worker unit-tests for PublishWorker — the *driver* branch
// of the async-publishing pipeline (queued → publishing →
// published|failed). Reconciler tests now live in
// reconcile_worker_test.go (Taglio 5.x split).
//
// Shared test fixtures (mockUserStore, mockProvider, mockAsyncProvider,
// mockCredentialVault, newTestWorker, scheduledTarget,
// publishingTarget) are in mocks_test.go. This file owns mockPostStore
// (the driver-only repository mock) and the publishTarget /
// runOnce / computeProviderIdempotencyKey unit tests.
package worker

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
	"github.com/Marcuss-ops/InstaeditLogin/pkg/metrics"
)

// ------------------------------------------------------------------
// mockPostStore — driver-only repository mock. Distinct from
// mockReconcilePostStore (reconcile_worker_test.go) because the
// driver's surface (ListPending, ClaimQueuedTarget, FindByID,
// SetProviderIdempotencyKey) is different from the reconciler's
// (ListPublishing, UpdateStatus). The interface split
// (PublisherPostStore vs ReconcilePostStore) compiles-in the
// invariant that each goroutine only exercises its own surface.
// ------------------------------------------------------------------

// mockPostStore is a PublisherPostStore with configurable function
// fields and call counters. The counters let each test assert the
// exact ordering of repository calls (e.g. Claim must happen BEFORE
// FindByID).
//
// Taglio 4.7 LEVEL 2: added setKeyFn + setKeyCalls + setKeyIDs +
// setKeyVals for the SetProviderIdempotencyKey interface method.
// Default behaviour (no setKeyFn configured) is no-op return nil so
// tests that don't exercise the stamp path continue to pass.
//
// Taglio 5.x: dropped listPublishingFn + updatePublishStateFn +
// related counters. Those moved to mockReconcilePostStore
// (reconcile_worker_test.go) on the Reconciler's surface.
type mockPostStore struct {
	// Call counters — one per method, incremented on every invocation.
	// Tests assert on the relative ordering (e.g. claimCalls > 0 before
	// findByIDCalls is allowed) and the final counts.
	claimCalls       int
	findByIDCalls    int
	updateCalls      int
	listPendingCalls int
	setKeyCalls      int

	// Function fields — each test overrides only what it exercises.
	listPendingFn  func(before time.Time) ([]models.PostTarget, error)
	claimFn        func(id int64) (bool, error)
	findByIDFn     func(id int64) (*models.Post, error)
	updateStatusFn func(*models.PostTarget) error
	// setKeyFn lets a test simulate ErrProviderIdempotencyConflict
	// from the repository's SetProviderIdempotencyKey call. Default
	// (nil) returns nil — the worker's happy path.
	setKeyFn func(id int64, key string) error

	// Captured targets from UpdateStatus — lets tests inspect the
	// final status (published vs failed) and assert on the worker
	// writing the right terminal state. Stored as struct values
	// (not pointers) so later mutations to the caller's target
	// don't leak into the captured snapshot.
	updateTargets []models.PostTarget

	// Captured SetProviderIdempotencyKey calls — (id, key) pairs in
	// invocation order. Tests verify the deterministic SHA-256
	// prefix path produced the expected hex string for a given
	// (post, account) pair on the last attempt.
	setKeyIDs  []int64
	setKeyVals []string
}

func (m *mockPostStore) ListPending(before time.Time) ([]models.PostTarget, error) {
	m.listPendingCalls++
	if m.listPendingFn == nil {
		return nil, nil
	}
	return m.listPendingFn(before)
}

func (m *mockPostStore) FindByID(id int64) (*models.Post, error) {
	m.findByIDCalls++
	if m.findByIDFn == nil {
		return nil, errors.New("FindByID not implemented in this test")
	}
	return m.findByIDFn(id)
}

func (m *mockPostStore) ClaimQueuedTarget(id int64) (bool, error) {
	m.claimCalls++
	if m.claimFn == nil {
		return false, errors.New("ClaimQueuedTarget not implemented in this test")
	}
	return m.claimFn(id)
}

func (m *mockPostStore) UpdateStatus(target *models.PostTarget) error {
	m.updateCalls++
	// Snapshot the struct by value so later mutations to the
	// caller's target don't leak into the captured row. Pointers
	// inside the struct (e.g. *time.Time PublishedAt) still
	// alias, but the worker only writes PublishedAt once and the
	// test reads it at assertion time, so this is safe.
	m.updateTargets = append(m.updateTargets, *target)
	if m.updateStatusFn == nil {
		return nil
	}
	return m.updateStatusFn(target)
}

// SetProviderIdempotencyKey (Taglio 4.7 LEVEL 2) — captures the
// (id, key) tuple for assertion. Default behaviour is no-op so
// tests that don't exercise the stamp path continue to pass
// without configuring a setKeyFn.
func (m *mockPostStore) SetProviderIdempotencyKey(id int64, key string) error {
	m.setKeyCalls++
	m.setKeyIDs = append(m.setKeyIDs, id)
	m.setKeyVals = append(m.setKeyVals, key)
	if m.setKeyFn == nil {
		return nil
	}
	return m.setKeyFn(id, key)
}

// ------------------------------------------------------------------
// publishTarget tests (sync platforms — the pre-4.2 behaviour /
// driver surface)
// ------------------------------------------------------------------

// TestPublishTarget_HappyPath_ClaimThenPublishToPublished covers the
// verdict §10 success path: claim wins → load post → load account →
// refresh token → stamp provider_idempotency_key → publish → status
// transition to 'published'. The test also asserts the exact call
// ORDERING: claim MUST run before FindByID, FindByID MUST run before
// Publish, and the SetProviderIdempotencyKey MUST run between renew
// and Publish so retries reuse the same key.
func TestPublishTarget_HappyPath_ClaimThenPublishToPublished(t *testing.T) {
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
			return &models.PublishResult{PlatformMediaID: "media-456"}, nil
		},
	}
	vault := &mockCredentialVault{
		renewFn: func(ctx context.Context, accountID int64, tokenType string, refresh credentials.TokenRefresher) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "fresh-tok", TokenType: "bearer"}, nil
		},
	}
	w := newTestWorker(posts, users, "instagram", svc, vault)

	if err := w.publishTarget(context.Background(), scheduledTarget()); err != nil {
		t.Fatalf("publishTarget: %v", err)
	}

	// All four steps fired exactly once.
	if posts.claimCalls != 1 {
		t.Errorf("ClaimQueuedTarget calls: want 1, got %d", posts.claimCalls)
	}
	if posts.findByIDCalls != 1 {
		t.Errorf("FindByID calls: want 1, got %d", posts.findByIDCalls)
	}
	if vault.ensureCalls != 1 {
		t.Errorf("Renew calls: want 1, got %d (BEFORE Publish should have refreshed the OAuth token)", vault.ensureCalls)
	}
	// Taglio 4.7 LEVEL 2: after claim wins, the worker stamps the
	// deterministic provider_idempotency_key. This MUST happen once
	// BEFORE Publish so retries reuse the same key.
	if posts.setKeyCalls != 1 {
		t.Errorf("SetProviderIdempotencyKey calls: want 1 (stamp per-target idempotency key), got %d", posts.setKeyCalls)
	}
	// The stamped key must match the deterministic SHA-256 prefix of
	// "v1:100:10" (post_id:account_id).
	wantKey := computeProviderIdempotencyKey(100, 10)
	if len(posts.setKeyVals) != 1 || posts.setKeyVals[0] != wantKey {
		t.Errorf("SetProviderIdempotencyKey key: want %q (SHA-256 prefix of v1:100:10), got %v",
			wantKey, posts.setKeyVals)
	}
	if svc.publishCalls != 1 {
		t.Errorf("Publish calls: want 1, got %d", svc.publishCalls)
	}
	if posts.updateCalls != 1 {
		t.Errorf("UpdateStatus calls: want 1, got %d (transition publishing→published)", posts.updateCalls)
	}
	// Final state must be 'published' with the platform_media_id and a
	// non-nil published_at. UpdateStatus captures the target at the
	// moment of the call, so we inspect the captured slice.
	if len(posts.updateTargets) != 1 {
		t.Fatalf("UpdateStatus captures: want 1, got %d", len(posts.updateTargets))
	}
	final := posts.updateTargets[0]
	if final.Status != models.PostStatusPublished {
		t.Errorf("final status: want published, got %q", final.Status)
	}
	if final.PlatformPostID != "media-456" {
		t.Errorf("platform_post_id: want media-456, got %q", final.PlatformPostID)
	}
	if final.PublishedAt == nil {
		t.Error("published_at: want non-nil, got nil (worker must stamp publish time on success)")
	}
}

// TestPublishTarget_PrefersPageAccessToken verifies that when a
// TokenTypePageAccess token exists in the vault (Facebook Pages), the
// worker passes it to Publish() instead of the refreshed user token.
func TestPublishTarget_PrefersPageAccessToken(t *testing.T) {
	const pageAccessToken = "page-access-token-xyz"
	posts := &mockPostStore{
		claimFn:    func(id int64) (bool, error) { return true, nil },
		findByIDFn: func(id int64) (*models.Post, error) { return &models.Post{ID: 100, Caption: "x"}, nil },
	}
	users := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return &models.PlatformAccount{ID: 10, Platform: "facebook", PlatformUserID: "page-123"}, nil
		},
	}
	var publishedToken string
	svc := &mockProvider{
		baseMockProvider: baseMockProvider{platform: "facebook"},
		publishFn: func(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (*models.PublishResult, error) {
			publishedToken = accessToken
			return &models.PublishResult{PlatformMediaID: "fb-post-1"}, nil
		},
	}
	vault := &mockCredentialVault{
		renewFn: func(ctx context.Context, accountID int64, tokenType string, refresh credentials.TokenRefresher) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "user-token-refreshed", TokenType: models.TokenTypeLongLived}, nil
		},
		getFn: func(ctx context.Context, platformAccountID int64, tokenType string) (*models.OAuthToken, error) {
			if tokenType == models.TokenTypePageAccess {
				return &models.OAuthToken{AccessToken: pageAccessToken, TokenType: models.TokenTypePageAccess}, nil
			}
			return nil, errors.New("token not found")
		},
	}
	w := newTestWorkerWithoutThrottle(posts, users, "facebook", svc, vault)

	if err := w.publishTarget(context.Background(), scheduledTarget()); err != nil {
		t.Fatalf("publishTarget: %v", err)
	}
	if publishedToken != pageAccessToken {
		t.Errorf("Publish access_token: want page token %q, got %q", pageAccessToken, publishedToken)
	}
}

// TestPublishTarget_ForwardsIdempotencyKeyOnPayload is the dedicated
// Taglio 4.7 LEVEL 2 assertion that payload.IdempotencyKey is the
// deterministic key the worker computed + stamped onto the target
// BEFORE the Publish call. The capture is in mockProvider.capturedPayload.
func TestPublishTarget_ForwardsIdempotencyKeyOnPayload(t *testing.T) {
	posts := &mockPostStore{
		claimFn:    func(id int64) (bool, error) { return true, nil },
		findByIDFn: func(id int64) (*models.Post, error) { return &models.Post{ID: 100, Caption: "x"}, nil },
	}
	users := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return &models.PlatformAccount{ID: 10, Platform: "instagram", PlatformUserID: "fb-1"}, nil
		},
	}
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

	if err := w.publishTarget(context.Background(), scheduledTarget()); err != nil {
		t.Fatalf("publishTarget: %v", err)
	}
	if svc.capturedPayload == nil {
		t.Fatal("publishFn was never called — worker bug")
	}
	wantKey := computeProviderIdempotencyKey(100, 10)
	if svc.capturedPayload.IdempotencyKey != wantKey {
		t.Errorf("payload.IdempotencyKey: want %q (deterministic SHA-256 prefix of v1:100:10), got %q",
			wantKey, svc.capturedPayload.IdempotencyKey)
	}
}

// TestPublishTarget_AsyncPlatform_StatusStaysPublishing (Taglio 4.2):
// when the platform has the AsyncPublisher capability, the publish()
// call returns immediately with a publish_id and the worker must
// KEEP the target in status='publishing' (not transition to
// 'published'). The ReconcilerWorker goroutine will later drive the
// state machine. (Taglio 5.x: the goroutine is in its own Run loop
// now, not inside the driver's runOnce.)
func TestPublishTarget_AsyncPlatform_StatusStaysPublishing(t *testing.T) {
	posts := &mockPostStore{
		claimFn: func(id int64) (bool, error) { return true, nil },
		findByIDFn: func(id int64) (*models.Post, error) {
			return &models.Post{ID: 100, Caption: "x", MediaURL: "https://cdn.example.com/v.mp4"}, nil
		},
	}
	users := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return &models.PlatformAccount{ID: 10, Platform: "tiktok", PlatformUserID: "tt-1"}, nil
		},
	}
	// TikTok-style async provider: Publish() returns a publish_id
	// immediately (the platform will process async).
	svc := &mockAsyncProvider{
		baseMockProvider: baseMockProvider{platform: "tiktok"},
		publishFn: func(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (*models.PublishResult, error) {
			return &models.PublishResult{PlatformMediaID: "tiktok-publish-id-xyz"}, nil
		},
	}
	vault := &mockCredentialVault{
		renewFn: func(ctx context.Context, accountID int64, tokenType string, refresh credentials.TokenRefresher) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "t"}, nil
		},
	}
	w := newTestWorker(posts, users, "tiktok", svc, vault)

	if err := w.publishTarget(context.Background(), scheduledTarget()); err != nil {
		t.Fatalf("publishTarget (async): %v", err)
	}

	// Publish was called once.
	if svc.publishCalls != 1 {
		t.Errorf("Publish calls: want 1, got %d", svc.publishCalls)
	}
	// Taglio 4.7 LEVEL 2: the worker stamped the per-target
	// provider_idempotency_key BEFORE Publish. Required for retries
	// of the async platform to dedup at the platform's API level.
	if posts.setKeyCalls != 1 {
		t.Errorf("SetProviderIdempotencyKey calls: want 1, got %d (async must also stamp before publish)", posts.setKeyCalls)
	}
	if svc.capturedPayload == nil || svc.capturedPayload.IdempotencyKey == "" {
		t.Error("async publish must forward payload.IdempotencyKey (Taglio 4.7 LEVEL 2 invariant)")
	}
	// UpdateStatus was called once to record the publish_id.
	if posts.updateCalls != 1 {
		t.Fatalf("UpdateStatus calls: want 1 (record publish_id), got %d", posts.updateCalls)
	}
	final := posts.updateTargets[0]
	// CRITICAL: status MUST stay 'publishing' — the reconciler owns
	// the publishing → published|failed transition.
	if final.Status != models.PostStatusPublishing {
		t.Errorf("status: want publishing (async, reconciler owns terminal), got %q", final.Status)
	}
	// The publish_id from the Publish() result must land on
	// PlatformPostID for the reconciler to query.
	if final.PlatformPostID != "tiktok-publish-id-xyz" {
		t.Errorf("platform_post_id: want tiktok-publish-id-xyz, got %q", final.PlatformPostID)
	}
	// PublishedAt must NOT be set yet (the publish hasn't completed).
	if final.PublishedAt != nil {
		t.Error("published_at: want nil (publish not yet complete), got non-nil")
	}
	// No CheckPublishStatus / Reconcile calls happen in the publishTarget path.
	// Those are the ReconcilerWorker's job.
	if svc.checkStatusCalls != 0 {
		t.Errorf("CheckPublishStatus calls in publishTarget: want 0, got %d (only reconciler should call this)", svc.checkStatusCalls)
	}
	if svc.reconcileCalls != 0 {
		t.Errorf("Reconcile calls in publishTarget: want 0 (reconciler owns that path), got %d", svc.reconcileCalls)
	}
}

// TestPublishTarget_PayloadIdempotencyKeyCarriesAcrossRetries is
// the Taglio 4.7 LEVEL 2 deterministic-key invariant: the SAME
// (post_id, platform_account_id) tuple MUST produce the SAME key
// on every publishTarget call. The mock here bypasses the
// SetProviderIdempotencyKey stamp by pre-setting
// target.ProviderIdempotencyKey so the "already stamped" branch
// runs and we can observe the reuse path.
func TestPublishTarget_PayloadIdempotencyKeyCarriesAcrossRetries(t *testing.T) {
	wantKey := computeProviderIdempotencyKey(100, 10)
	posts := &mockPostStore{
		claimFn: func(id int64) (bool, error) { return true, nil },
		findByIDFn: func(id int64) (*models.Post, error) {
			return &models.Post{ID: 100, Caption: "x", MediaURL: "https://cdn.example.com/v.mp4"}, nil
		},
		// EnsureProviderIdempotencyKey must NOT be reached — the
		// target already has a stamped key. If it IS reached, the
		// assertion fails because the worker would re-stamp and the
		// SetKeyFn (configured to capture + error) would trip.
		setKeyFn: func(id int64, key string) error {
			t.Errorf("SetProviderIdempotencyKey should NOT be called when target already has a key; got id=%d key=%q", id, key)
			return nil
		},
	}
	users := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return &models.PlatformAccount{ID: 10, Platform: "instagram", PlatformUserID: "fb-1"}, nil
		},
	}
	svc := &mockProvider{
		baseMockProvider: baseMockProvider{platform: "instagram"},
		publishFn: func(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (*models.PublishResult, error) {
			return &models.PublishResult{PlatformMediaID: "media-retry"}, nil
		},
	}
	vault := &mockCredentialVault{
		renewFn: func(ctx context.Context, accountID int64, tokenType string, refresh credentials.TokenRefresher) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "t"}, nil
		},
	}
	w := newTestWorker(posts, users, "instagram", svc, vault)

	// Build a target with the deterministic key PRE-stamped so the
	// worker reuses it instead of computing a new one. This is the
	// retry path: ticker picks up the same target again on the
	// second attempt.
	pre := scheduledTarget()
	pre.ProviderIdempotencyKey = &wantKey

	if err := w.publishTarget(context.Background(), pre); err != nil {
		t.Fatalf("publishTarget (retry): %v", err)
	}
	if posts.setKeyCalls != 0 {
		t.Errorf("SetProviderIdempotencyKey calls: want 0 (retry reuses pre-stamped key), got %d", posts.setKeyCalls)
	}
	// Publish must still carry the same key on the payload.
	if svc.capturedPayload == nil || svc.capturedPayload.IdempotencyKey != wantKey {
		t.Errorf("payload.IdempotencyKey: want %q (reused from pre-stamped target), got %+v",
			wantKey, svc.capturedPayload)
	}
}

// TestPublishTarget_SetKeyConflict_PromotesToFailed covers the
// ErrProviderIdempotencyConflict path: the worker MUST promote the
// target to status='failed' (not leave it in 'publishing' anymore)
// so the row drops out of BOTH the driver's tick filter AND the
// ReconcilerWorker's tickReconcile filter. Leaving the row in
// 'publishing' would be a permanent infinite polling loop (no
// other worker can re-claim it because verdict-§10 owned the row).
//
// The setKeyFn injects a fake ErrProviderIdempotencyConflict-shaped
// error to avoid importing the real repository package.
func TestPublishTarget_SetKeyConflict_PromotesToFailed(t *testing.T) {
	posts := &mockPostStore{
		claimFn: func(id int64) (bool, error) { return true, nil },
		findByIDFn: func(id int64) (*models.Post, error) {
			return &models.Post{ID: 100, Caption: "x"}, nil
		},
		setKeyFn: func(id int64, key string) error {
			// Wrap with %w so errors.Is(err, repository.ErrProviderIdempotencyConflict)
			// matches the real sentinel inside the worker's promote-to-failed
			// branch.
			return fmt.Errorf("%w: account already has a target with this key",
				repository.ErrProviderIdempotencyConflict)
		},
	}
	users := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return &models.PlatformAccount{ID: 10, Platform: "instagram", PlatformUserID: "fb-1"}, nil
		},
	}
	svc := &mockProvider{
		baseMockProvider: baseMockProvider{platform: "instagram"},
		publishFn: func(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (*models.PublishResult, error) {
			t.Error("Publish MUST NOT be called when SetProviderIdempotencyKey conflicts (conflict is the worker's exit signal)")
			return nil, nil
		},
	}
	vault := &mockCredentialVault{
		renewFn: func(ctx context.Context, accountID int64, tokenType string, refresh credentials.TokenRefresher) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "t"}, nil
		},
	}
	w := newTestWorker(posts, users, "instagram", svc, vault)

	err := w.publishTarget(context.Background(), scheduledTarget())
	if err == nil {
		t.Fatal("expected conflict error to surface, got nil — the tick counter wouldn't increment without it")
	}
	// ALSO assert the sentinel propagates through the worker's outer
	// fmt.Errorf wrapping — the production pq.Error path dispatches
	// repository.ErrProviderIdempotencyConflict upstream via the same
	// chain and the tick counter's errors.Is check depends on it.
	if !errors.Is(err, repository.ErrProviderIdempotencyConflict) {
		t.Errorf("err chain must wrap repository.ErrProviderIdempotencyConflict for the tick/errors.Is dispatcher, got %v", err)
	}
	if svc.publishCalls != 0 {
		t.Errorf("Publish calls under conflict: want 0, got %d", svc.publishCalls)
	}
	// CRITICAL: on conflict, the worker MUST call UpdateStatus with
	// status='failed' so the row drops out of both 'publishing' filter
	// sets (driver's tick + ReconcilerWorker's tickReconcile).
	if posts.updateCalls != 1 {
		t.Errorf("UpdateStatus calls under conflict: want 1 (promote-to-failed), got %d", posts.updateCalls)
	}
	if len(posts.updateTargets) != 1 {
		t.Fatalf("UpdateStatus captures under conflict: want 1, got %d", len(posts.updateTargets))
	}
	final := posts.updateTargets[0]
	if final.Status != models.PostStatusFailed {
		t.Errorf("final status: want failed (promote-to-failed on conflict), got %q", final.Status)
	}
	if !strings.Contains(final.ErrorMessage, "provider idempotency key conflict") {
		t.Errorf("ErrorMessage should explain the conflict: %q", final.ErrorMessage)
	}
}

// TestPublishTarget_ClaimLoss_SkipsWithoutPublish is the verdict §10
// double-publish-prevention test: when ClaimQueuedTarget returns
// false (another worker already won the race), the worker MUST skip
// the target without loading the post, refreshing the token, or
// calling Publish. Any of those side-effects on a claim-loss would
// risk a second publish for the same target.
func TestPublishTarget_ClaimLoss_SkipsWithoutPublish(t *testing.T) {
	posts := &mockPostStore{
		claimFn: func(id int64) (bool, error) { return false, nil },
		// If the worker incorrectly continues after claim-loss, these
		// functions will be invoked and the assertions below will
		// catch the side effect.
		findByIDFn: func(id int64) (*models.Post, error) {
			t.Error("FindByID called despite claim loss (claim-loss branch must short-circuit BEFORE post load)")
			return nil, nil
		},
	}
	users := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			t.Error("FindPlatformAccountByID called despite claim loss")
			return nil, nil
		},
	}
	svc := &mockProvider{
		baseMockProvider: baseMockProvider{platform: "instagram"},
		publishFn: func(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (*models.PublishResult, error) {
			t.Error("Publish called despite claim loss (verdict §10 — this is the double-publish the claim was supposed to prevent)")
			return nil, nil
		},
	}
	vault := &mockCredentialVault{
		renewFn: func(ctx context.Context, accountID int64, tokenType string, refresh credentials.TokenRefresher) (*models.OAuthToken, error) {
			t.Error("Renew called despite claim loss")
			return nil, nil
		},
	}
	w := newTestWorker(posts, users, "instagram", svc, vault)

	// Claim-loss is NOT an error from publishTarget's perspective —
	// it's a normal skip. The worker just logs and returns nil.
	if err := w.publishTarget(context.Background(), scheduledTarget()); err != nil {
		t.Fatalf("publishTarget: claim-loss should be nil (it's a skip, not a failure), got %v", err)
	}

	// Hard call counts: claim fires once, NOTHING ELSE does.
	if posts.claimCalls != 1 {
		t.Errorf("ClaimQueuedTarget calls: want 1, got %d", posts.claimCalls)
	}
	if posts.findByIDCalls != 0 {
		t.Errorf("FindByID calls: want 0, got %d (claim-loss must short-circuit)", posts.findByIDCalls)
	}
	if vault.ensureCalls != 0 {
		t.Errorf("Renew calls: want 0, got %d (claim-loss must short-circuit)", vault.ensureCalls)
	}
	if svc.publishCalls != 0 {
		t.Errorf("Publish calls: want 0, got %d (CRITICAL: this is the double-publish path)", svc.publishCalls)
	}
	if posts.updateCalls != 0 {
		t.Errorf("UpdateStatus calls: want 0, got %d (claim-loss must NOT mutate status — another worker owns the row)", posts.updateCalls)
	}
	if posts.setKeyCalls != 0 {
		t.Errorf("SetProviderIdempotencyKey calls: want 0 (claim-loss must short-circuit), got %d", posts.setKeyCalls)
	}
}

// TestPublishTarget_ClaimFiresBeforeFindByID asserts the
// claim-first ordering invariant using a call ordering tracker. A
// regression that reordered the two steps would break the
// double-publish guarantee if the post load also had a side-effect
// (e.g. logging payload).
func TestPublishTarget_ClaimFiresBeforeFindByID(t *testing.T) {
	var order []string
	posts := &mockPostStore{
		claimFn: func(id int64) (bool, error) {
			order = append(order, "claim")
			return true, nil
		},
		findByIDFn: func(id int64) (*models.Post, error) {
			order = append(order, "findByID")
			return &models.Post{ID: 100, Caption: "x"}, nil
		},
	}
	users := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			order = append(order, "findAccount")
			return &models.PlatformAccount{ID: 10, Platform: "instagram", PlatformUserID: "fb-1"}, nil
		},
	}
	svc := &mockProvider{
		baseMockProvider: baseMockProvider{platform: "instagram"},
		publishFn: func(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (*models.PublishResult, error) {
			order = append(order, "publish")
			return &models.PublishResult{PlatformMediaID: "ok"}, nil
		},
	}
	vault := &mockCredentialVault{
		renewFn: func(ctx context.Context, accountID int64, tokenType string, refresh credentials.TokenRefresher) (*models.OAuthToken, error) {
			order = append(order, "renew")
			return &models.OAuthToken{AccessToken: "t"}, nil
		},
	}
	// Capture the SetKey call so the ordering tracker includes it.
	posts.setKeyFn = func(id int64, key string) error {
		order = append(order, "setKey")
		return nil
	}
	w := newTestWorker(posts, users, "instagram", svc, vault)

	if err := w.publishTarget(context.Background(), scheduledTarget()); err != nil {
		t.Fatalf("publishTarget: %v", err)
	}
	// Taglio 4.7 LEVEL 2: SetKey inserted BETWEEN renew and publish
	// so retries reuse the same key. Ordering invariants unchanged
	// for the prior steps.
	want := []string{"claim", "findByID", "findAccount", "renew", "setKey", "publish"}
	if len(order) != len(want) {
		t.Fatalf("call order: want %v, got %v", want, order)
	}
	for i, step := range want {
		if order[i] != step {
			t.Errorf("step[%d]: want %q, got %q (full order: %v)", i, step, order[i], order)
		}
	}
}

// TestPublishTarget_ClaimFiresBeforeAnySideEffectOnLoss combines
// the "no side effects on claim loss" + "ordering" guarantees into
// a single observable invariant: the FIRST repo call on every
// claim must be ClaimQueuedTarget. This is the simplest
// expression of the verdict §10 contract.
func TestPublishTarget_ClaimFiresBeforeAnySideEffectOnLoss(t *testing.T) {
	var order []string
	posts := &mockPostStore{
		claimFn: func(id int64) (bool, error) {
			order = append(order, "claim")
			return false, nil // lost
		},
		findByIDFn: func(id int64) (*models.Post, error) {
			order = append(order, "findByID")
			return nil, nil
		},
	}
	users := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			order = append(order, "findAccount")
			return nil, nil
		},
	}
	svc := &mockProvider{
		baseMockProvider: baseMockProvider{platform: "instagram"},
		publishFn: func(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (*models.PublishResult, error) {
			order = append(order, "publish")
			return nil, nil
		},
	}
	vault := &mockCredentialVault{
		renewFn: func(ctx context.Context, accountID int64, tokenType string, refresh credentials.TokenRefresher) (*models.OAuthToken, error) {
			order = append(order, "renew")
			return nil, nil
		},
	}
	w := newTestWorker(posts, users, "instagram", svc, vault)

	if err := w.publishTarget(context.Background(), scheduledTarget()); err != nil {
		t.Fatalf("publishTarget: %v", err)
	}
	if len(order) != 1 || order[0] != "claim" {
		t.Errorf("on claim-loss, only ClaimQueuedTarget should run; got order=%v", order)
	}
}

// TestPublishTarget_ClaimError_Propagates covers the path where
// ClaimQueuedTarget itself returns an error (DB unreachable, etc.).
// The error must surface so the tick can log + continue to the next
// target.
func TestPublishTarget_ClaimError_Propagates(t *testing.T) {
	posts := &mockPostStore{
		claimFn: func(id int64) (bool, error) {
			return false, errors.New("connection lost")
		},
	}
	users := &mockUserStore{}
	svc := &mockProvider{baseMockProvider: baseMockProvider{platform: "instagram"}}
	vault := &mockCredentialVault{}
	w := newTestWorker(posts, users, "instagram", svc, vault)

	err := w.publishTarget(context.Background(), scheduledTarget())
	if err == nil {
		t.Fatal("expected claim error to propagate, got nil (claim DB errors must surface so the tick can log/continue)")
	}
	// No downstream calls on claim error.
	if svc.publishCalls != 0 {
		t.Errorf("Publish called despite claim error: %d", svc.publishCalls)
	}
	if posts.updateCalls != 0 {
		t.Errorf("UpdateStatus called despite claim error: %d", posts.updateCalls)
	}
	if posts.setKeyCalls != 0 {
		t.Errorf("SetProviderIdempotencyKey called despite claim error: %d", posts.setKeyCalls)
	}
}

// TestPublishTarget_PostNotFound_AfterClaim_MarksFailed covers the
// "vanished parent post" failure mode. The claim already won (so
// the row is in 'publishing' state), the worker MUST mark the
// target 'failed' so the next tick won't re-pick it.
func TestPublishTarget_PostNotFound_AfterClaim_MarksFailed(t *testing.T) {
	posts := &mockPostStore{
		claimFn:    func(id int64) (bool, error) { return true, nil },
		findByIDFn: func(id int64) (*models.Post, error) { return nil, nil }, // vanished
	}
	users := &mockUserStore{}
	svc := &mockProvider{baseMockProvider: baseMockProvider{platform: "instagram"}}
	vault := &mockCredentialVault{}
	w := newTestWorker(posts, users, "instagram", svc, vault)

	if err := w.publishTarget(context.Background(), scheduledTarget()); err == nil {
		t.Fatal("expected error from vanished post, got nil")
	}
	if posts.updateCalls != 1 {
		t.Errorf("UpdateStatus calls: want 1 (must mark 'failed' so the row isn't re-picked), got %d", posts.updateCalls)
	}
	if len(posts.updateTargets) != 1 || posts.updateTargets[0].Status != models.PostStatusFailed {
		t.Errorf("final status: want failed, got %+v", posts.updateTargets)
	}
	if posts.updateTargets[0].ErrorMessage == "" {
		t.Error("ErrorMessage should be populated on the failed transition (for debugging)")
	}
	if svc.publishCalls != 0 {
		t.Errorf("Publish called despite vanished post: %d", svc.publishCalls)
	}
	if posts.setKeyCalls != 0 {
		t.Errorf("SetProviderIdempotencyKey called despite vanished post: %d", posts.setKeyCalls)
	}
}

// TestPublishTarget_PlatformPublishError_MarksFailed covers the
// platform API failure path. The claim already won (so the row is
// in 'publishing' state); a platform error MUST transition the
// target to 'failed' with the error message.
func TestPublishTarget_PlatformPublishError_MarksFailed(t *testing.T) {
	posts := &mockPostStore{
		claimFn: func(id int64) (bool, error) { return true, nil },
		findByIDFn: func(id int64) (*models.Post, error) {
			return &models.Post{ID: 100, Caption: "x"}, nil
		},
	}
	users := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return &models.PlatformAccount{ID: 10, Platform: "instagram", PlatformUserID: "fb-1"}, nil
		},
	}
	svc := &mockProvider{
		baseMockProvider: baseMockProvider{platform: "instagram"},
		publishFn: func(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (*models.PublishResult, error) {
			return nil, errors.New("500 internal error from meta")
		},
	}
	vault := &mockCredentialVault{
		renewFn: func(ctx context.Context, accountID int64, tokenType string, refresh credentials.TokenRefresher) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "t"}, nil
		},
	}
	w := newTestWorker(posts, users, "instagram", svc, vault)

	if err := w.publishTarget(context.Background(), scheduledTarget()); err == nil {
		t.Fatal("expected error from platform publish failure, got nil")
	}
	if posts.updateCalls != 1 {
		t.Errorf("UpdateStatus calls: want 1, got %d", posts.updateCalls)
	}
	final := posts.updateTargets[0]
	if final.Status != models.PostStatusFailed {
		t.Errorf("final status: want failed, got %q", final.Status)
	}
	if final.ErrorMessage == "" {
		t.Error("ErrorMessage should be populated with the platform error for debugging")
	}
	if final.PublishedAt != nil {
		t.Error("PublishedAt should remain nil on failure (a failed target has no published_at)")
	}
	if posts.setKeyCalls != 1 {
		t.Errorf("SetProviderIdempotencyKey calls: want 1, got %d", posts.setKeyCalls)
	}
}

// TestPublishTarget_OneClaimWinner_OnlyWinnerPublishes is the
// end-to-end verdict §10 invariant: when two workers race the
// claim, EXACTLY ONE Publish call is observed.
func TestPublishTarget_OneClaimWinner_OnlyWinnerPublishes(t *testing.T) {
	t.Parallel()

	// Use a real (in-process) mutex to deterministically simulate
	// the DB's row-level locking. The first worker to acquire the
	// mutex sets claimedBy; the second sees it and returns
	// claimed=false. This matches the semantics of `UPDATE ...
	// WHERE status='queued'` under contention.
	var (
		mu          sync.Mutex
		claimedBy   string // "A", "B", or "" (none)
		publishHits int
		publishMu   sync.Mutex
	)
	recordPublish := func() {
		publishMu.Lock()
		publishHits++
		publishMu.Unlock()
	}

	// Worker A: claims on first acquire, proceeds through the full
	// happy path (FindByID → Renew → Publish).
	postsA := &mockPostStore{
		claimFn: func(id int64) (bool, error) {
			mu.Lock()
			defer mu.Unlock()
			if claimedBy != "" {
				return false, nil // already claimed
			}
			claimedBy = "A"
			return true, nil
		},
		findByIDFn: func(id int64) (*models.Post, error) {
			return &models.Post{ID: 100, Caption: "x"}, nil
		},
	}
	usersA := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return &models.PlatformAccount{ID: 10, Platform: "instagram", PlatformUserID: "fb-1"}, nil
		},
	}
	svcA := &mockProvider{
		baseMockProvider: baseMockProvider{platform: "instagram"},
		publishFn: func(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (*models.PublishResult, error) {
			recordPublish()
			return &models.PublishResult{PlatformMediaID: "media-A"}, nil
		},
	}
	vaultA := &mockCredentialVault{
		renewFn: func(ctx context.Context, accountID int64, tokenType string, refresh credentials.TokenRefresher) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "t"}, nil
		},
	}
	wA := newTestWorker(postsA, usersA, "instagram", svcA, vaultA)

	// Worker B: identical happy path wiring (if B ever won, B
	// would also reach Publish).
	postsB := &mockPostStore{
		claimFn: func(id int64) (bool, error) {
			mu.Lock()
			defer mu.Unlock()
			if claimedBy != "" {
				return false, nil
			}
			claimedBy = "B"
			return true, nil
		},
		findByIDFn: func(id int64) (*models.Post, error) {
			return &models.Post{ID: 100, Caption: "x"}, nil
		},
	}
	usersB := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return &models.PlatformAccount{ID: 10, Platform: "instagram", PlatformUserID: "fb-1"}, nil
		},
	}
	svcB := &mockProvider{
		baseMockProvider: baseMockProvider{platform: "instagram"},
		publishFn: func(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (*models.PublishResult, error) {
			recordPublish()
			return &models.PublishResult{PlatformMediaID: "media-B"}, nil
		},
	}
	vaultB := &mockCredentialVault{
		renewFn: func(ctx context.Context, accountID int64, tokenType string, refresh credentials.TokenRefresher) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "t"}, nil
		},
	}
	wB := newTestWorker(postsB, usersB, "instagram", svcB, vaultB)

	// Race the two workers on the same target.
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _ = wA.publishTarget(context.Background(), scheduledTarget()) }()
	go func() { defer wg.Done(); _ = wB.publishTarget(context.Background(), scheduledTarget()) }()
	wg.Wait()

	// The verdict: exactly one Publish happened, not two.
	if publishHits != 1 {
		t.Errorf("Publish calls: want 1 (verdict §10: only the claim winner may publish), got %d", publishHits)
	}
	if claimedBy != "A" && claimedBy != "B" {
		t.Errorf("claimedBy: want A or B, got %q", claimedBy)
	}
	winnerPosts, loserPosts := postsA, postsB
	winnerSvc, loserSvc := svcA, svcB
	winnerVault, loserVault := vaultA, vaultB
	if claimedBy == "B" {
		winnerPosts, loserPosts = postsB, postsA
		winnerSvc, loserSvc = svcB, svcA
		winnerVault, loserVault = vaultB, vaultA
	}
	_ = winnerPosts
	_ = winnerSvc
	_ = winnerVault
	if loserPosts.findByIDCalls != 0 {
		t.Errorf("loser FindByID calls: want 0, got %d (claim-loss must short-circuit BEFORE post load)", loserPosts.findByIDCalls)
	}
	if loserVault.ensureCalls != 0 {
		t.Errorf("loser Renew calls: want 0, got %d", loserVault.ensureCalls)
	}
	if loserSvc.publishCalls != 0 {
		t.Errorf("loser Publish calls: want 0, got %d (CRITICAL: this is the double-publish path)", loserSvc.publishCalls)
	}
	if loserPosts.updateCalls != 0 {
		t.Errorf("loser UpdateStatus calls: want 0, got %d (claim-loss must NOT mutate status — winner owns the row)", loserPosts.updateCalls)
	}
	if loserPosts.setKeyCalls != 0 {
		t.Errorf("loser SetProviderIdempotencyKey calls: want 0, got %d (claim-loss must short-circuit)", loserPosts.setKeyCalls)
	}
}

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

// ------------------------------------------------------------------
// computeProviderIdempotencyKey unit tests
// ------------------------------------------------------------------

// TestComputeProviderIdempotencyKey_Deterministic covers the
// Taglio 4.7 LEVEL 2 invariant: same (post_id, platform_account_id)
// → same hex prefix, every time. Retries reuse the same key.
func TestComputeProviderIdempotencyKey_Deterministic(t *testing.T) {
	k1 := computeProviderIdempotencyKey(100, 10)
	k2 := computeProviderIdempotencyKey(100, 10)
	if k1 != k2 {
		t.Errorf("not deterministic: %q vs %q", k1, k2)
	}
	if len(k1) != providerIdempotencyKeyLen {
		t.Errorf("len: want %d, got %d (%q)", providerIdempotencyKeyLen, len(k1), k1)
	}
}

// TestComputeProviderIdempotencyKey_DifferentInputs covers the
// security invariant: different (post_id, platform_account_id)
// tuples yield DIFFERENT keys (otherwise cross-account collisions
// would slip past the partial UNIQUE INDEX).
func TestComputeProviderIdempotencyKey_DifferentInputs(t *testing.T) {
	postA := computeProviderIdempotencyKey(100, 10)
	postB := computeProviderIdempotencyKey(101, 10) // different post
	acctA := computeProviderIdempotencyKey(100, 10)
	acctB := computeProviderIdempotencyKey(100, 11) // different account
	if postA == postB {
		t.Errorf("different post_ids collided: %q == %q", postA, postB)
	}
	if acctA == acctB {
		t.Errorf("different platform_account_ids collided: %q == %q", acctA, acctB)
	}
	if postA != acctA {
		t.Error("(100, 10) should be self-consistent")
	}
}

// ------------------------------------------------------------------
// P1 — YouTube privacy_level precedence cascade tests
// (migration 053 + internal/worker/publish_worker.go)
// The cascade is:
//
//   payload override (post.PrivacyLevel)        [highest]
//   > post.DefaultPrivacyLevel                  [middle]
//   > "unlisted"                                [YouTube fallback]
//   > "PUBLIC_TO_EVERYONE"                      [other platforms]
//
// The boundary allowlist (public|unlisted|private) is enforced at
// youtube_oauth.go::ValidateContent → validateYouTubePrivacyLevel.
// These tests verify the worker produces the correct intermediate
// PublishPayload.PrivacyLevel value; the allowlist test that rejects
// an invalid value lives in services/youtube_oauth_test.go.
// ------------------------------------------------------------------

// TestPublishTarget_PrivacyLevel_PostOverrideWins confirms the highest
// precedence term: post.PrivacyLevel (set by the post-update endpoint)
// wins over post.DefaultPrivacyLevel and over the YouTube "unlisted"
// fallback. The expected PublishPayload.PrivacyLevel on the captured
// call is the post's override.
func TestPublishTarget_PrivacyLevel_PostOverrideWins(t *testing.T) {
	posts := &mockPostStore{
		claimFn: func(id int64) (bool, error) { return true, nil },
		findByIDFn: func(id int64) (*models.Post, error) {
			return &models.Post{
				ID:                 100,
				Caption:            "x",
				MediaURL:           "https://cdn.example.com/video.mp4",
				PrivacyLevel:       "private", // highest term
				DefaultPrivacyLevel: "unlisted", // middle term
			}, nil
		},
	}
	users := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return &models.PlatformAccount{ID: 10, Platform: "youtube", PlatformUserID: "UC-chan"}, nil
		},
	}
	svc := &mockProvider{
		baseMockProvider: baseMockProvider{platform: "youtube"},
		publishFn: func(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (*models.PublishResult, error) {
			return &models.PublishResult{PlatformMediaID: "yt-id"}, nil
		},
	}
	vault := &mockCredentialVault{
		renewFn: func(ctx context.Context, accountID int64, tokenType string, refresh credentials.TokenRefresher) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "t"}, nil
		},
	}
	w := newTestWorker(posts, users, "youtube", svc, vault)

	if err := w.publishTarget(context.Background(), scheduledTarget()); err != nil {
		t.Fatalf("publishTarget: %v", err)
	}
	if svc.capturedPayload == nil {
		t.Fatal("Publish never called — worker bug")
	}
	if svc.capturedPayload.PrivacyLevel != "private" {
		t.Errorf("payload.PrivacyLevel: want \"private\" (post.PrivacyLevel wins), got %q",
			svc.capturedPayload.PrivacyLevel)
	}
}

// TestPublishTarget_PrivacyLevel_PostDefaultWinsOverFallback confirms
// the middle term: post.DefaultPrivacyLevel (inherited from
// upload_job → import_batch) is preferred over the YouTube "unlisted"
// fallback when post.PrivacyLevel is empty.
func TestPublishTarget_PrivacyLevel_PostDefaultWinsOverFallback(t *testing.T) {
	posts := &mockPostStore{
		claimFn: func(id int64) (bool, error) { return true, nil },
		findByIDFn: func(id int64) (*models.Post, error) {
			return &models.Post{
				ID:          100,
				Caption:     "x",
				MediaURL:    "https://cdn.example.com/video.mp4",
				// PrivacyLevel empty → falls through to DefaultPrivacyLevel
				DefaultPrivacyLevel: "public",
			}, nil
		},
	}
	users := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return &models.PlatformAccount{ID: 10, Platform: "youtube", PlatformUserID: "UC-chan"}, nil
		},
	}
	svc := &mockProvider{
		baseMockProvider: baseMockProvider{platform: "youtube"},
		publishFn: func(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (*models.PublishResult, error) {
			return &models.PublishResult{PlatformMediaID: "yt-id"}, nil
		},
	}
	vault := &mockCredentialVault{
		renewFn: func(ctx context.Context, accountID int64, tokenType string, refresh credentials.TokenRefresher) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "t"}, nil
		},
	}
	w := newTestWorker(posts, users, "youtube", svc, vault)

	if err := w.publishTarget(context.Background(), scheduledTarget()); err != nil {
		t.Fatalf("publishTarget: %v", err)
	}
	if svc.capturedPayload.PrivacyLevel != "public" {
		t.Errorf("payload.PrivacyLevel: want \"public\" (post.DefaultPrivacyLevel wins over fallback), got %q",
			svc.capturedPayload.PrivacyLevel)
	}
}

// TestPublishTarget_PrivacyLevel_YouTubeFallbackIsUnlisted confirms
// the bottom term: when both post-side fields are empty AND the
// platform is YouTube, the worker falls back to "unlisted" (NOT
// "private" as in the legacy hardcoded default).
func TestPublishTarget_PrivacyLevel_YouTubeFallbackIsUnlisted(t *testing.T) {
	posts := &mockPostStore{
		claimFn: func(id int64) (bool, error) { return true, nil },
		findByIDFn: func(id int64) (*models.Post, error) {
			return &models.Post{
				ID:          100,
				Caption:     "x",
				MediaURL:    "https://cdn.example.com/video.mp4",
				// Both privacy fields empty — must reach the platform fallback
			}, nil
		},
	}
	users := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return &models.PlatformAccount{ID: 10, Platform: "youtube", PlatformUserID: "UC-chan"}, nil
		},
	}
	svc := &mockProvider{
		baseMockProvider: baseMockProvider{platform: "youtube"},
		publishFn: func(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (*models.PublishResult, error) {
			return &models.PublishResult{PlatformMediaID: "yt-id"}, nil
		},
	}
	vault := &mockCredentialVault{
		renewFn: func(ctx context.Context, accountID int64, tokenType string, refresh credentials.TokenRefresher) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "t"}, nil
		},
	}
	w := newTestWorker(posts, users, "youtube", svc, vault)

	if err := w.publishTarget(context.Background(), scheduledTarget()); err != nil {
		t.Fatalf("publishTarget: %v", err)
	}
	if svc.capturedPayload.PrivacyLevel != "unlisted" {
		t.Errorf("payload.PrivacyLevel: want \"unlisted\" (YouTube fallback replaces legacy \"private\"), got %q",
			svc.capturedPayload.PrivacyLevel)
	}
}

// TestPublishTarget_PrivacyLevel_NonYouTubeKeepsPublicToEveryone
// confirms the worker does NOT stamp the YouTube-specific "unlisted"
// fallback on non-YouTube platforms; Instagram / TikTok etc. keep
// their historical "PUBLIC_TO_EVERYONE" default when both post-side
// fields are empty.
func TestPublishTarget_PrivacyLevel_NonYouTubeKeepsPublicToEveryone(t *testing.T) {
	posts := &mockPostStore{
		claimFn: func(id int64) (bool, error) { return true, nil },
		findByIDFn: func(id int64) (*models.Post, error) {
			return &models.Post{
				ID:                 100,
				Caption:            "ig-caption",
				MediaURL:           "https://cdn.example.com/ig.mp4",
				Status:             models.PostStatusScheduled,
				// Both privacy fields empty
			}, nil
		},
	}
	users := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return &models.PlatformAccount{ID: 10, Platform: "instagram", PlatformUserID: "ig-1"}, nil
		},
	}
	svc := &mockProvider{
		baseMockProvider: baseMockProvider{platform: "instagram"},
		publishFn: func(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (*models.PublishResult, error) {
			return &models.PublishResult{PlatformMediaID: "ig-id"}, nil
		},
	}
	vault := &mockCredentialVault{
		renewFn: func(ctx context.Context, accountID int64, tokenType string, refresh credentials.TokenRefresher) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "t"}, nil
		},
	}
	w := newTestWorker(posts, users, "instagram", svc, vault)

	if err := w.publishTarget(context.Background(), scheduledTarget()); err != nil {
		t.Fatalf("publishTarget: %v", err)
	}
	if svc.capturedPayload.PrivacyLevel != "PUBLIC_TO_EVERYONE" {
		t.Errorf("payload.PrivacyLevel: want \"PUBLIC_TO_EVERYONE\" (non-YouTube keeps generic default), got %q",
			svc.capturedPayload.PrivacyLevel)
	}
}

// ------------------------------------------------------------------
// P0#3 — server-side YouTube pre-upload channel binding check
// ------------------------------------------------------------------

// TestPublishTarget_YouTube_ChannelMatch_PublishesNormally verifies
// the happy path: when the YouTube channel binding check returns nil,
// the worker proceeds through Publish → target.Status='published',
// and the platform_account is NOT flagged reauth_required.
//
// Assertions cover the side effects the contract guarantees:
//   - validateChannelBindingCalls==1 (check ran exactly once)
//   - capturedAccessToken is the post-renew token (NOT stale)
//   - capturedExpectedChannel is the platform_account.platform_user_id
//   - publishCalls==1 (the platform publish proceeds)
//   - markReauthRequiredCalls==0 (no false positive on match)
func TestPublishTarget_YouTube_ChannelMatch_PublishesNormally(t *testing.T) {
	posts := &mockPostStore{
		claimFn: func(id int64) (bool, error) { return true, nil },
		findByIDFn: func(id int64) (*models.Post, error) {
			return &models.Post{
				ID:          100,
				Caption:     "yt-caption",
				Title:       "yt-title",
				MediaURL:    "https://cdn.example.com/yt-video.mp4",
				Status:      models.PostStatusScheduled,
			}, nil
		},
	}
	users := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return &models.PlatformAccount{
				ID:             10,
				Platform:       "youtube",
				PlatformUserID: "UCexpectedYtChan",
			}, nil
		},
	}
	svc := &mockProvider{
		baseMockProvider: baseMockProvider{platform: "youtube"},
		publishFn: func(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (*models.PublishResult, error) {
			return &models.PublishResult{PlatformMediaID: "yt-video-id-1"}, nil
		},
		// P0#3: the binding check returns nil — the grant IS bound
		// to the expected channel. publish proceeds.
		validateChannelBindingFn: func(ctx context.Context, accessToken, expectedChannelID string) error {
			return nil
		},
	}
	vault := &mockCredentialVault{
		renewFn: func(ctx context.Context, accountID int64, tokenType string, refresh credentials.TokenRefresher) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "fresh-yt-bearer", TokenType: "bearer"}, nil
		},
	}
	w := newTestWorker(posts, users, "youtube", svc, vault)

	if err := w.publishTarget(context.Background(), scheduledTarget()); err != nil {
		t.Fatalf("publishTarget on match: %v", err)
	}

	// 1. The binding check ran exactly once.
	if svc.validateChannelBindingCalls != 1 {
		t.Errorf("ValidateChannelBinding calls: want 1, got %d", svc.validateChannelBindingCalls)
	}
	// 2. The worker forwarded the post-renew access token (NOT a
	//    stale value).
	if svc.capturedAccessToken != "fresh-yt-bearer" {
		t.Errorf("captured access token: want fresh-yt-bearer (post-renew input), got %q", svc.capturedAccessToken)
	}
	// 3. The worker forwarded the platform_account.platform_user_id.
	if svc.capturedExpectedChannel != "UCexpectedYtChan" {
		t.Errorf("captured expected channel: want UCexpectedYtChan (platform_account.platform_user_id), got %q", svc.capturedExpectedChannel)
	}
	// 4. Publish proceeded.
	if svc.publishCalls != 1 {
		t.Errorf("Publish calls: want 1 (match path), got %d", svc.publishCalls)
	}
	// 5. NO reauth flagging on match path (this is the test the channel
	// binding check exists to guard against — a false-positive reauth
	// flag on a healthy match would lock the operator out).
	if users.markReauthRequiredCalls != 0 {
		t.Errorf("MarkReauthRequired calls on match: want 0 (no false-positive reauth flag), got %d", users.markReauthRequiredCalls)
	}
	// 6. Target transitioned to published (happy path).
	if posts.updateCalls != 1 {
		t.Fatalf("UpdateStatus calls: want 1, got %d", posts.updateCalls)
	}
	if posts.updateTargets[0].Status != models.PostStatusPublished {
		t.Errorf("final status: want published, got %q", posts.updateTargets[0].Status)
	}
	if posts.updateTargets[0].PlatformPostID != "yt-video-id-1" {
		t.Errorf("platform_post_id: want yt-video-id-1, got %q", posts.updateTargets[0].PlatformPostID)
	}
}

// TestPublishTarget_YouTube_ChannelMismatch_FlagsReauthAndFailsTarget
// verifies the mismatch path: the binding check returns
// ErrYouTubeChannelMismatch (wrapped). The worker must:
//   - call MarkReauthRequired with code="youtube_channel_mismatch"
//     so the operator's dashboard prompts a reconnect.
//   - NOT publish (upload on the wrong channel is the bug we are
//     guarding against).
//   - transition the post_target to 'failed' with a descriptive
//     ErrorMessage so the operator sees WHY the upload was refused.
func TestPublishTarget_YouTube_ChannelMismatch_FlagsReauthAndFailsTarget(t *testing.T) {
	posts := &mockPostStore{
		claimFn: func(id int64) (bool, error) { return true, nil },
		findByIDFn: func(id int64) (*models.Post, error) {
			return &models.Post{
				ID:       200,
				Caption:  "mismatched yt caption",
				Title:    "yt-title",
				MediaURL: "https://cdn.example.com/y.mp4",
			}, nil
		},
	}
	var markCode, markMsg string
	var markID int64
	users := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return &models.PlatformAccount{
				ID:             11,
				Platform:       "youtube",
				PlatformUserID: "UCexpectedChan",
			}, nil
		},
		markReauthRequiredFn: func(ctx context.Context, id int64, code, message string) error {
			markID = id
			markCode = code
			markMsg = message
			return nil
		},
	}
	svc := &mockProvider{
		baseMockProvider: baseMockProvider{platform: "youtube"},
		publishFn: func(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (*models.PublishResult, error) {
			// P0#3: Publish MUST NOT be reached on channel mismatch.
			// Reaching Publish would silently upload to the wrong
			// channel — exactly the bug this guard is preventing.
			t.Error("Publish called despite channel mismatch (this is the silent-wrong-channel bug we are guarding against)")
			return nil, errors.New("unreachable")
		},
		// Mismatch: grant lists UCwrongChan, expected UCexpectedChan.
		validateChannelBindingFn: func(ctx context.Context, accessToken, expectedChannelID string) error {
			return fmt.Errorf("%w: expected %q, grant bound to %q",
				services.ErrYouTubeChannelMismatch, expectedChannelID, "UCwrongChan")
		},
	}
	vault := &mockCredentialVault{
		renewFn: func(ctx context.Context, accountID int64, tokenType string, refresh credentials.TokenRefresher) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "any-bearer"}, nil
		},
	}
	w := newTestWorker(posts, users, "youtube", svc, vault)

	err := w.publishTarget(context.Background(), scheduledTarget())
	if err == nil {
		t.Fatal("expected error on channel mismatch, got nil")
	}

	// 1. The binding check ran exactly once.
	if svc.validateChannelBindingCalls != 1 {
		t.Errorf("ValidateChannelBinding calls: want 1, got %d", svc.validateChannelBindingCalls)
	}
	// 2. Publish was NOT called (CRITICAL — no silent wrong-channel upload).
	if svc.publishCalls != 0 {
		t.Fatalf("Publish calls on mismatch: want 0, got %d (wrong-channel upload)", svc.publishCalls)
	}
	// 3. MarkReauthRequired was called with the right code + message.
	if users.markReauthRequiredCalls != 1 {
		t.Errorf("MarkReauthRequired calls: want 1 (flag platform_account for reauth), got %d", users.markReauthRequiredCalls)
	}
	if markID != 11 {
		t.Errorf("MarkReauthRequired account id: want 11, got %d", markID)
	}
	if markCode != "youtube_channel_mismatch" {
		t.Errorf("MarkReauthRequired code: want youtube_channel_mismatch, got %q", markCode)
	}
	if !strings.Contains(markMsg, "UCexpectedChan") {
		t.Errorf("MarkReauthRequired message should include expected channel id, got %q", markMsg)
	}
	if !strings.Contains(markMsg, "UCwrongChan") {
		t.Errorf("MarkReauthRequired message should include actual channel id (operator visibility), got %q", markMsg)
	}
	// 4. Post_target transitioned to failed with a descriptive message.
	if posts.updateCalls != 1 {
		t.Fatalf("UpdateStatus calls: want 1, got %d", posts.updateCalls)
	}
	if posts.updateTargets[0].Status != models.PostStatusFailed {
		t.Errorf("final status: want failed, got %q", posts.updateTargets[0].Status)
	}
	if !strings.Contains(posts.updateTargets[0].ErrorMessage, "youtube channel binding") {
		t.Errorf("ErrorMessage should mention the binding check, got %q", posts.updateTargets[0].ErrorMessage)
	}
	// 5. The idempotency key stamp happened BEFORE the mismatch check
	//    would have produced an error... actually no: the stamp comes
	//    AFTER the binding check in our placement. So on mismatch we
	//    expect setKeyCalls==0 (no key stamped on a failed publish).
	if posts.setKeyCalls != 0 {
		t.Errorf("SetProviderIdempotencyKey calls on mismatch: want 0 (no key stamped for failed publishes), got %d", posts.setKeyCalls)
	}
	_ = err
}

// TestPublishTarget_YouTube_ChannelCheck_Transient_FailsTargetWithoutFlaggingReauth
// verifies the transient-error path: the binding check returns a
// plain error (NOT wrapping ErrYouTubeChannelMismatch). The worker
// MUST treat this as transient:
//   - DO NOT flag reauth_required (network blips / 5xx do not mean
//     the grant is dead; flagging would lock the operator out).
//   - DO transition the target to 'failed' so the row drops out of
//     the tick filter (impossible to retry on the same tick without
//     claim re-acquisition across processes — i.e. on the next
//     process restart / tick).
//
// In production, transient failures would be handled with
// decorator-jitter backoff (Taglio ~ future). Today the tick's
// per-target error counter increments and the error is logged; the
// next scheduler pass CAN retry if the platform_account was not
// flagged (which is what we want for transient cases).
func TestPublishTarget_YouTube_ChannelCheck_Transient_FailsTargetWithoutFlaggingReauth(t *testing.T) {
	posts := &mockPostStore{
		claimFn: func(id int64) (bool, error) { return true, nil },
		findByIDFn: func(id int64) (*models.Post, error) {
			return &models.Post{ID: 300, Caption: "x", MediaURL: "https://cdn.example.com/v.mp4"}, nil
		},
	}
	users := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return &models.PlatformAccount{
				ID:             12,
				Platform:       "youtube",
				PlatformUserID: "UCtransientChan",
			}, nil
		},
		// CRITICAL: the worker's transient branch must NOT call this
		// function. We configure a marker to detect any false
		// positive: if the worker calls MarkReauthRequired, the
		// platform_account will be wrongly flagged as dead.
		markReauthRequiredFn: func(ctx context.Context, id int64, code, message string) error {
			t.Errorf("MarkReauthRequired MUST NOT be called on transient binding-check failure (would lock the operator out); got id=%d code=%q", id, code)
			return nil
		},
	}
	svc := &mockProvider{
		baseMockProvider: baseMockProvider{platform: "youtube"},
		publishFn: func(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (*models.PublishResult, error) {
			t.Error("Publish called despite transient binding-check failure")
			return nil, errors.New("unreachable")
		},
		// Transient: 5xx from channels.list. PLAIN error — does NOT
		// wrap ErrYouTubeChannelMismatch. The worker must detect via
		// errors.Is(err, ErrYouTubeChannelMismatch) == false.
		validateChannelBindingFn: func(ctx context.Context, accessToken, expectedChannelID string) error {
			return errors.New("youtube channel binding: channels.list returned 503: service unavailable")
		},
	}
	vault := &mockCredentialVault{
		renewFn: func(ctx context.Context, accountID int64, tokenType string, refresh credentials.TokenRefresher) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "any"}, nil
		},
	}
	w := newTestWorker(posts, users, "youtube", svc, vault)

	err := w.publishTarget(context.Background(), scheduledTarget())
	if err == nil {
		t.Fatal("expected transient error to propagate so the tick counter increments, got nil")
	}
	// The error must NOT wrap ErrYouTubeChannelMismatch (otherwise the
	// worker's errors.Is would route it to the reauth branch).
	if errors.Is(err, services.ErrYouTubeChannelMismatch) {
		t.Errorf("transient error must NOT wrap ErrYouTubeChannelMismatch (would misroute to reauth branch), got %v", err)
	}
	// Validate the structure (counter & status).
	if svc.validateChannelBindingCalls != 1 {
		t.Errorf("ValidateChannelBinding calls: want 1, got %d", svc.validateChannelBindingCalls)
	}
	if svc.publishCalls != 0 {
		t.Errorf("Publish calls on transient: want 0, got %d", svc.publishCalls)
	}
	// markReauthRequiredFn is configured to fail via t.Errorf on any
	// call. The counter assertion is therefore unnecessary — the
	// configured fn already aborts the test if MarkReauthRequired
	// was reached.
	if posts.updateCalls != 1 {
		t.Fatalf("UpdateStatus calls: want 1 (mark target failed), got %d", posts.updateCalls)
	}
	if posts.updateTargets[0].Status != models.PostStatusFailed {
		t.Errorf("final status: want failed, got %q", posts.updateTargets[0].Status)
	}
	if !strings.Contains(posts.updateTargets[0].ErrorMessage, "channel binding") {
		t.Errorf("ErrorMessage should mention the binding check, got %q", posts.updateTargets[0].ErrorMessage)
	}
	_ = err
}

// TestPublishTarget_YouTube_ChannelBindingMismatch_IncrementsMetric (P0 #2)
// is the table-driven coverage of the
// youtube_publish_channel_mismatch_total counter. The metric MUST
// increment ONLY on the ErrYouTubeChannelMismatch branch (which
// ALSO calls MarkReauthRequired); the match branch and the
// transient 5xx branch MUST NOT increment because no reauth flag
// is written on those paths (drift up = Google silently re-bound
// the OAuth grant to a different Brand Account).
//
// Delta-based assertion (read before + read after) instead of
// Reset() so other parallel-sibling metric tests that share the
// global CounterVec don't get wiped between cases. The
// (provider="youtube") label is the only series this test reads;
// sibling tests use other labels.
func TestPublishTarget_YouTube_ChannelBindingMismatch_IncrementsMetric(t *testing.T) {
	cases := []struct {
		name                  string
		bindResultErr         error
		wantMetricDelta       float64
		wantMarkReauthCalls   int
	}{
		{
			name:                "match_does_not_increment",
			bindResultErr:       nil,
			wantMetricDelta:     0,
			wantMarkReauthCalls: 0,
		},
		{
			name: "mismatch_increments_by_one",
			bindResultErr: fmt.Errorf("%w: %q is not in channels.list(mine=true) result",
				services.ErrYouTubeChannelMismatch, "UCexpectedChanID"),
			wantMetricDelta:     1,
			wantMarkReauthCalls: 1,
		},
		{
			name: "transient_does_not_increment",
			// 503 from channels.list — MISMATCH PATH MUST NOT FIRE
			// because this is wrapped plainly (no ErrYouTubeChannelMismatch
			// in the chain). Mirrors the existing
			// TestPublishTarget_YouTube_ChannelCheck_Transient_ path.
			bindResultErr:       errors.New("youtube channel binding: channels.list returned 503: upstream"),
			wantMetricDelta:     0,
			wantMarkReauthCalls: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := testutil.ToFloat64(metrics.YouTubePublishChannelMismatch.WithLabelValues("youtube"))

			posts := &mockPostStore{
				claimFn: func(id int64) (bool, error) { return true, nil },
				findByIDFn: func(id int64) (*models.Post, error) {
					return &models.Post{ID: 100, Caption: "x", MediaURL: "https://cdn.example.com/v.mp4"}, nil
				},
			}
			users := &mockUserStore{
				findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
					return &models.PlatformAccount{
						ID:             11,
						Platform:       models.PlatformYouTube,
						PlatformUserID: "UCexpectedChanID",
					}, nil
				},
				markReauthRequiredFn: func(ctx context.Context, id int64, code, message string) error {
					if tc.wantMarkReauthCalls == 0 {
						t.Errorf("MarkReauthRequired MUST NOT be called when bindResultErr is non-mismatch (%v)", tc.bindResultErr)
					}
					return nil
				},
			}
			svc := &mockProvider{
				baseMockProvider: baseMockProvider{platform: "youtube"},
				publishFn: func(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (*models.PublishResult, error) {
					// For match sub-case Publish is expected to be
					// called. For mismatch/transient the
					// channel-binding branch short-circuits BEFORE
					// Publish and the existing tests
					// (TestPublishTarget_YouTube_ChannelMismatch_FlagsReauthAndFailsTarget_
					// + TestPublishTarget_YouTube_ChannelCheck_Transient_)
					// assert that. Returning a non-nil result here
					// keeps the sync publishing path (which reads
					// result.PlatformMediaID) free of nil-deref in
					// the match happy path.
					return &models.PublishResult{PlatformMediaID: "yt-test-media"}, nil
				},
				validateChannelBindingFn: func(ctx context.Context, accessToken, expectedChannelID string) error {
					return tc.bindResultErr
				},
			}
			vault := &mockCredentialVault{
				renewFn: func(ctx context.Context, accountID int64, tokenType string, refresh credentials.TokenRefresher) (*models.OAuthToken, error) {
					return &models.OAuthToken{AccessToken: "t"}, nil
				},
			}
			w := newTestWorker(posts, users, "youtube", svc, vault)

			_ = w.publishTarget(context.Background(), scheduledTarget())

			after := testutil.ToFloat64(metrics.YouTubePublishChannelMismatch.WithLabelValues("youtube"))
			delta := after - before
			if delta != tc.wantMetricDelta {
				t.Errorf("youtube_publish_channel_mismatch_total{youtube} delta: want %v, got %v", tc.wantMetricDelta, delta)
			}
			if users.markReauthRequiredCalls != tc.wantMarkReauthCalls {
				t.Errorf("MarkReauthRequired calls: want %d, got %d (must match metric increment: the two fire together)", tc.wantMarkReauthCalls, users.markReauthRequiredCalls)
			}
		})
	}
}
