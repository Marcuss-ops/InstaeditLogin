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

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------

// mockPostStore is a PublisherPostStore with configurable function fields
// and call counters. The counters let each test assert the exact ordering
// of repository calls (e.g. Claim must happen BEFORE FindByID).
//
// Taglio 4.2: added listPublishingFn, updatePublishStateFn, and counters
// for the new reconciler goroutine's data path.
type mockPostStore struct {
	// Call counters — one per method, incremented on every invocation.
	// Tests assert on the relative ordering (e.g. claimCalls > 0 before
	// findByIDCalls is allowed) and the final counts.
	claimCalls              int
	findByIDCalls           int
	updateCalls             int
	listPendingCalls        int
	listPublishingCalls     int
	updatePublishStateCalls int

	// Function fields — each test overrides only what it exercises.
	listPendingFn        func(before time.Time) ([]models.PostTarget, error)
	listPublishingFn     func() ([]models.PostTarget, error)
	claimFn              func(id int64) (bool, error)
	findByIDFn           func(id int64) (*models.Post, error)
	updateStatusFn       func(*models.PostTarget) error
	updatePublishStateFn func(id int64, providerState string) error

	// Captured targets from UpdateStatus — lets tests inspect the
	// final status (published vs failed) and assert on the worker
	// writing the right terminal state. Stored as struct values
	// (not pointers) so later mutations to the caller's target
	// don't leak into the captured snapshot.
	updateTargets []models.PostTarget

	// Captured UpdatePublishState calls — used by reconciler tests to
	// verify the worker is recording the platform's current state
	// on every poll.
	updatePublishStateIDs    []int64
	updatePublishStateValues []string
}

func (m *mockPostStore) ListPending(before time.Time) ([]models.PostTarget, error) {
	m.listPendingCalls++
	if m.listPendingFn == nil {
		return nil, nil
	}
	return m.listPendingFn(before)
}

func (m *mockPostStore) ListPublishing() ([]models.PostTarget, error) {
	m.listPublishingCalls++
	if m.listPublishingFn == nil {
		return nil, nil
	}
	return m.listPublishingFn()
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

func (m *mockPostStore) UpdatePublishState(id int64, providerState string) error {
	m.updatePublishStateCalls++
	m.updatePublishStateIDs = append(m.updatePublishStateIDs, id)
	m.updatePublishStateValues = append(m.updatePublishStateValues, providerState)
	if m.updatePublishStateFn == nil {
		return nil
	}
	return m.updatePublishStateFn(id, providerState)
}

// mockUserStore is a PublisherUserStore with a configurable lookup.
type mockUserStore struct {
	findPlatformAccountFn func(id int64) (*models.PlatformAccount, error)
}

func (m *mockUserStore) FindPlatformAccountByID(id int64) (*models.PlatformAccount, error) {
	if m.findPlatformAccountFn == nil {
		return nil, errors.New("FindPlatformAccountByID not implemented in this test")
	}
	return m.findPlatformAccountFn(id)
}

// mockProvider is the SYNC-ONLY platform provider mock. It satisfies
// services.OAuthProvider (via the embedded base) and services.Publisher,
// but NOT services.AsyncPublisher. Use for tests that exercise
// sync-only platforms (Instagram, YouTube) where the publish() call
// completes the publish synchronously and there is no async state
// machine to drive.
//
// For TikTok-style async platforms, use mockAsyncProvider below.
type mockProvider struct {
	baseMockProvider
	publishFn func(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (*models.PublishResult, error)
	// Call counter — used to prove the loser branch of the claim
	// never reaches the platform API (no Publish call when claim=false).
	publishCalls int
}

// baseMockProvider holds the shared OAuthProvider methods. Embedded so
// mockProvider and mockAsyncProvider both satisfy services.OAuthProvider
// without duplicating the methods.
type baseMockProvider struct {
	platform string
}

func (b *baseMockProvider) Name() string { return b.platform }

func (m *mockProvider) GetLoginURL(state string) string {
	panic("GetLoginURL not used in worker tests")
}
func (m *mockProvider) HandleCallback(ctx context.Context, state, code string) (*models.PlatformProfile, *models.TokenData, error) {
	panic("HandleCallback not used in worker tests")
}
func (m *mockProvider) RefreshOAuthToken(ctx context.Context, refreshToken string) (*models.TokenData, error) {
	panic("RefreshOAuthToken not used in worker tests — wire via mockCredentialVault.renewFn if needed")
}
func (m *mockProvider) Publish(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (*models.PublishResult, error) {
	m.publishCalls++
	if m.publishFn == nil {
		return nil, errors.New("Publish not implemented in this test")
	}
	return m.publishFn(ctx, accessToken, platformUserID, payload)
}

// mockAsyncProvider (Taglio 4.2) satisfies services.AsyncPublisher in
// addition to Publisher. The router will register it under the
// AsyncPublisher capability; the reconciler goroutine will pick it up
// on every tick to drive the 4-step state machine.
//
// Use for tests that exercise async platforms (TikTok today). For
// sync platforms use mockProvider instead.
type mockAsyncProvider struct {
	baseMockProvider
	publishFn          func(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (*models.PublishResult, error)
	startPublishFn     func(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (string, string, error)
	checkStatusFn      func(ctx context.Context, accessToken, publishID string) (string, error)
	continuePublishFn  func(ctx context.Context, accessToken, publishID string) error
	reconcileFn        func(ctx context.Context, accessToken, publishID string) (*models.PublishResult, error)
	publishCalls       int
	startPublishCalls  int
	checkStatusCalls   int
	continueCalls      int
	reconcileCalls     int
}

func (m *mockAsyncProvider) GetLoginURL(state string) string {
	panic("GetLoginURL not used in worker tests")
}
func (m *mockAsyncProvider) HandleCallback(ctx context.Context, state, code string) (*models.PlatformProfile, *models.TokenData, error) {
	panic("HandleCallback not used in worker tests")
}
func (m *mockAsyncProvider) RefreshOAuthToken(ctx context.Context, refreshToken string) (*models.TokenData, error) {
	panic("RefreshOAuthToken not used in worker tests — wire via mockCredentialVault.renewFn if needed")
}
func (m *mockAsyncProvider) Publish(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (*models.PublishResult, error) {
	m.publishCalls++
	if m.publishFn == nil {
		return nil, errors.New("Publish not implemented in this test")
	}
	return m.publishFn(ctx, accessToken, platformUserID, payload)
}

// StartPublish (Taglio 4.2, async only) — default: derive publish_id
// from the configured publishFn (the real TikTok StartPublish returns a
// publish_id synchronously). Tests that need a specific publish_id can
// set startPublishFn directly.
func (m *mockAsyncProvider) StartPublish(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (string, string, error) {
	m.startPublishCalls++
	if m.startPublishFn != nil {
		return m.startPublishFn(ctx, accessToken, platformUserID, payload)
	}
	if m.publishFn != nil {
		res, err := m.publishFn(ctx, accessToken, platformUserID, payload)
		if err != nil {
			return "", "", err
		}
		return res.PlatformMediaID, "PROCESSING_UPLOAD", nil
	}
	return "default-publish-id", "PROCESSING_UPLOAD", nil
}

// CheckPublishStatus (Taglio 4.2, async only) — single GET, no polling.
func (m *mockAsyncProvider) CheckPublishStatus(ctx context.Context, accessToken, publishID string) (string, error) {
	m.checkStatusCalls++
	if m.checkStatusFn == nil {
		return "", errors.New("CheckPublishStatus not implemented in this test")
	}
	return m.checkStatusFn(ctx, accessToken, publishID)
}

// ContinuePublish (Taglio 4.2, async only) — PULL_FROM_URL no-op.
func (m *mockAsyncProvider) ContinuePublish(ctx context.Context, accessToken, publishID string) error {
	m.continueCalls++
	if m.continuePublishFn == nil {
		return nil // PULL_FROM_URL: no-op default
	}
	return m.continuePublishFn(ctx, accessToken, publishID)
}

// Reconcile (Taglio 4.2, async only) — terminal-state detector.
func (m *mockAsyncProvider) Reconcile(ctx context.Context, accessToken, publishID string) (*models.PublishResult, error) {
	m.reconcileCalls++
	if m.reconcileFn != nil {
		return m.reconcileFn(ctx, accessToken, publishID)
	}
	// Default: derive from CheckPublishStatus — matches the real
	// TikTokOAuthService.Reconcile which is a thin wrapper.
	state, err := m.CheckPublishStatus(ctx, accessToken, publishID)
	if err != nil {
		return nil, err
	}
	if state == "PUBLISH_COMPLETE" {
		return &models.PublishResult{PlatformMediaID: publishID}, nil
	}
	if state == "FAILED" {
		return nil, errors.New("tiktok publish failed: publish_id=" + publishID)
	}
	return nil, nil
}

// mockCredentialVault is a credentials.VaultAPI. The worker only calls
// Renew (via the vault field on PublishWorker), so Save / Get / Revoke /
// Rotate are stubbed (panic if accidentally called).
//
// Taglio 2.2: renamed from mockTokenService. The `renewFn` signature
// now takes a credentials.TokenRefresher (plain function) rather than
// a services.OAuthProvider — the vault has zero knowledge of per-platform
// types, so the worker adapts OAuthProvider.RefreshOAuthToken into a
// closure at the call site. The test never needs to call the refresher
// itself; it just returns a valid token.
type mockCredentialVault struct {
	renewFn     func(ctx context.Context, accountID int64, tokenType string, refresh credentials.TokenRefresher) (*models.OAuthToken, error)
	ensureCalls int
}

func (m *mockCredentialVault) Save(ctx context.Context, platformAccountID int64, tokenData *models.TokenData) error {
	panic("Save not used in worker tests")
}
func (m *mockCredentialVault) Get(ctx context.Context, platformAccountID int64, tokenType string) (*models.OAuthToken, error) {
	panic("Get not used in worker tests")
}
func (m *mockCredentialVault) Renew(ctx context.Context, accountID int64, tokenType string, refresh credentials.TokenRefresher) (*models.OAuthToken, error) {
	m.ensureCalls++
	if m.renewFn == nil {
		return nil, errors.New("Renew not implemented in this test")
	}
	return m.renewFn(ctx, accountID, tokenType, refresh)
}
func (m *mockCredentialVault) Revoke(ctx context.Context, platformAccountID int64) error {
	panic("Revoke not used in worker tests")
}
func (m *mockCredentialVault) Rotate(ctx context.Context, platformAccountID int64, tokenData *models.TokenData) error {
	return m.Save(ctx, platformAccountID, tokenData)
}

// newTestWorker builds a PublishWorker wired with the given mocks.
// interval is small (10ms) but irrelevant — the tests call publishTarget
// directly rather than driving the Run loop.
//
// The provider can be a mockProvider (sync) or a mockAsyncProvider
// (async) — the router registers whichever capability set the value
// structurally satisfies.
func newTestWorker(posts *mockPostStore, users *mockUserStore, name string, svc any, vault *mockCredentialVault) *PublishWorker {
	router := services.NewCapabilityRouter()
	router.Register(name, svc)
	return NewPublishWorker(
		posts,
		users,
		router,
		vault,
		10*time.Millisecond,
		nil, // inherit slog.Default()
	)
}

// helper — builds a scheduled target the worker can pick up.
func scheduledTarget() *models.PostTarget {
	return &models.PostTarget{
		ID:                200,
		PostID:            100,
		PlatformAccountID: 10,
		Status:            models.PostStatusScheduled,
	}
}

// helper — builds a publishing target the reconciler can pick up.
func publishingTarget() *models.PostTarget {
	return &models.PostTarget{
		ID:                300,
		PostID:            100,
		PlatformAccountID: 10,
		Status:            models.PostStatusPublishing,
		PlatformPostID:    "publish-id-abc",
	}
}

// ---------------------------------------------------------------------------
// publishTarget tests (sync platforms — the pre-4.2 behavior)
// ---------------------------------------------------------------------------

// TestPublishTarget_HappyPath_ClaimThenPublishToPublished covers the
// verdict §10 success path: claim wins → load post → load account →
// refresh token → publish → status transition to 'published'.
// The test also asserts the exact call ORDERING: claim MUST run before
// FindByID, FindByID MUST run before Publish.
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
		t.Errorf("ClaimScheduledTarget calls: want 1, got %d", posts.claimCalls)
	}
	if posts.findByIDCalls != 1 {
		t.Errorf("FindByID calls: want 1, got %d", posts.findByIDCalls)
	}
	if vault.ensureCalls != 1 {
		t.Errorf("Renew calls: want 1, got %d (BEFORE Publish should have refreshed the OAuth token)", vault.ensureCalls)
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

// TestPublishTarget_AsyncPlatform_StatusStaysPublishing (Taglio 4.2):
// when the platform has the AsyncPublisher capability, the publish()
// call returns immediately with a publish_id and the worker must
// KEEP the target in status='publishing' (not transition to 'published').
// The reconciler goroutine will later drive the state machine.
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
	// Those are the reconciler's job.
	if svc.checkStatusCalls != 0 {
		t.Errorf("CheckPublishStatus calls in publishTarget: want 0, got %d (only reconciler should call this)", svc.checkStatusCalls)
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
		t.Errorf("ClaimScheduledTarget calls: want 1, got %d", posts.claimCalls)
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
}

// TestPublishTarget_ClaimFiresBeforeFindByID asserts the claim-first
// ordering invariant using a call ordering tracker. A regression that
// reordered the two steps (e.g. "preload post then claim" to optimize
// for the loser path) would break the double-publish guarantee if
// the post load also had a side-effect (e.g. logging payload).
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
	w := newTestWorker(posts, users, "instagram", svc, vault)

	if err := w.publishTarget(context.Background(), scheduledTarget()); err != nil {
		t.Fatalf("publishTarget: %v", err)
	}
	want := []string{"claim", "findByID", "findAccount", "renew", "publish"}
	if len(order) != len(want) {
		t.Fatalf("call order: want %v, got %v", want, order)
	}
	for i, step := range want {
		if order[i] != step {
			t.Errorf("step[%d]: want %q, got %q (full order: %v)", i, step, order[i], order)
		}
	}
}

// TestPublishTarget_ClaimFiresBeforeAnySideEffectOnLoss combines the
// "no side effects on claim loss" + "ordering" guarantees into a
// single observable invariant: the FIRST repo call on every claim
// must be ClaimQueuedTarget. This is the simplest expression of
// the verdict §10 contract.
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
// target. It MUST NOT silently look like a claim-loss (which would
// swallow infrastructure errors and delay retry until the next tick).
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
}

// TestPublishTarget_PostNotFound_AfterClaim_MarksFailed covers the
// "vanished parent post" failure mode. The claim already won (so
// the row is in 'publishing' state), the worker MUST mark the
// target 'failed' so the next tick won't re-pick it. It must NOT
// silently skip (a silent skip would leave a row stuck in
// 'publishing' forever).
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
	// No platform API call — no post means nothing to publish.
	if svc.publishCalls != 0 {
		t.Errorf("Publish called despite vanished post: %d", svc.publishCalls)
	}
}

// TestPublishTarget_PlatformPublishError_MarksFailed covers the
// platform API failure path. The claim already won (so the row is
// in 'publishing' state); a platform error MUST transition the
// target to 'failed' with the error message, so the next tick
// doesn't re-pick it.
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
}

// TestPublishTarget_OneClaimWinner_OnlyWinnerPublishes is the
// end-to-end verdict §10 invariant: when two workers race the
// claim, EXACTLY ONE Publish call is observed. This is the
// double-publish-prevention guarantee in its strongest form — the
// atomic-claim's whole reason to exist.
//
// Note: this is a logic-level test (the mocks are in-memory, no
// real DB). The real atomicity is provided by the database's
// row-level locking on the UPDATE. This test verifies that the
// worker treats a "loser" mock-return as expected — and that the
// worker code does not bypass the claim. The SQL-level
// concurrency proof is the repository test
// (TestPostClaimQueuedTarget_Success/AlreadyClaimed) plus
// the WHERE-status='scheduled' guard in the actual UPDATE.
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
	// would also reach Publish). The whole point of the test is
	// that the mutex makes A win and B lose — and B's mocks use
	// counters (not t.Error) so a stray B call would still let
	// the test report publishHits correctly.
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
	// And exactly one claim won.
	if claimedBy != "A" && claimedBy != "B" {
		t.Errorf("claimedBy: want A or B, got %q", claimedBy)
	}
	// The losing worker's downstream methods must not have been
	// called. We assert on the call counters (not t.Error in the
	// mocks) so the assertion order doesn't matter and a test
	// failure in the counters is the single source of truth.
	// (One of A/B will be the loser depending on goroutine
	// scheduling; the loser's findByIDCalls + renewCalls +
	// publishCalls + updateCalls must all be 0.)
	winnerPosts, loserPosts := postsA, postsB
	winnerSvc, loserSvc := svcA, svcB
	winnerVault, loserVault := vaultA, vaultB
	if claimedBy == "B" {
		winnerPosts, loserPosts = postsB, postsA
		winnerSvc, loserSvc = svcB, svcA
		winnerVault, loserVault = vaultB, vaultA
	}
	_ = winnerPosts // winner's call counts are exercised by the happy-path test
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
}

// ---------------------------------------------------------------------------
// Taglio 4.2: reconciler tests (tickReconcile + reconcileTarget)
// ---------------------------------------------------------------------------

// TestReconcileTarget_PublishComplete_TransitionsToPublished covers the
// happy terminal state: CheckPublishStatus returns PUBLISH_COMPLETE,
// the reconciler must transition the target from 'publishing' to
// 'published' with a non-nil published_at.
func TestReconcileTarget_PublishComplete_TransitionsToPublished(t *testing.T) {
	posts := &mockPostStore{}
	users := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return &models.PlatformAccount{ID: 10, Platform: "tiktok", PlatformUserID: "tt-1"}, nil
		},
	}
	svc := &mockAsyncProvider{
		baseMockProvider: baseMockProvider{platform: "tiktok"},
		checkStatusFn: func(ctx context.Context, accessToken, publishID string) (string, error) {
			return "PUBLISH_COMPLETE", nil
		},
	}
	vault := &mockCredentialVault{
		renewFn: func(ctx context.Context, accountID int64, tokenType string, refresh credentials.TokenRefresher) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "t"}, nil
		},
	}
	w := newTestWorker(posts, users, "tiktok", svc, vault)

	reconciled, wasFailed, err := w.reconcileTarget(context.Background(), publishingTarget())
	if err != nil {
		t.Fatalf("reconcileTarget: %v", err)
	}
	if !reconciled {
		t.Error("reconciled: want true (PUBLISH_COMPLETE is a terminal transition), got false")
	}
	if wasFailed {
		t.Error("wasFailed: want false (PUBLISH_COMPLETE is a success, not failure)")
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
	// UpdatePublishState must have been called for observability.
	if posts.updatePublishStateCalls != 1 {
		t.Errorf("UpdatePublishState calls: want 1 (record terminal state), got %d", posts.updatePublishStateCalls)
	}
	if len(posts.updatePublishStateValues) != 1 || posts.updatePublishStateValues[0] != "PUBLISH_COMPLETE" {
		t.Errorf("UpdatePublishState values: want [PUBLISH_COMPLETE], got %v", posts.updatePublishStateValues)
	}
	// CheckPublishStatus called exactly once (no polling).
	if svc.checkStatusCalls != 1 {
		t.Errorf("CheckPublishStatus calls: want 1, got %d (NO polling — the whole point of Taglio 4.2)", svc.checkStatusCalls)
	}
}

// TestReconcileTarget_Failed_TransitionsToFailed covers the failure
// terminal state: CheckPublishStatus returns FAILED, the reconciler
// must transition to 'failed' with the error message.
func TestReconcileTarget_Failed_TransitionsToFailed(t *testing.T) {
	posts := &mockPostStore{}
	users := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return &models.PlatformAccount{ID: 10, Platform: "tiktok", PlatformUserID: "tt-1"}, nil
		},
	}
	svc := &mockAsyncProvider{
		baseMockProvider: baseMockProvider{platform: "tiktok"},
		checkStatusFn: func(ctx context.Context, accessToken, publishID string) (string, error) {
			return "FAILED", nil
		},
	}
	vault := &mockCredentialVault{
		renewFn: func(ctx context.Context, accountID int64, tokenType string, refresh credentials.TokenRefresher) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "t"}, nil
		},
	}
	w := newTestWorker(posts, users, "tiktok", svc, vault)

	reconciled, wasFailed, err := w.reconcileTarget(context.Background(), publishingTarget())
	if err != nil {
		t.Fatalf("reconcileTarget: %v", err)
	}
	if !reconciled || !wasFailed {
		t.Errorf("reconciled=%v wasFailed=%v: want (true, true)", reconciled, wasFailed)
	}
	// UpdateStatus should have been called once with status=failed.
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
	// provider_state was still recorded (for observability).
	if posts.updatePublishStateCalls != 1 {
		t.Errorf("UpdatePublishState calls: want 1 (record terminal state), got %d", posts.updatePublishStateCalls)
	}
}

// TestReconcileTarget_InFlight_LeavesStatusUnchanged covers the
// in-flight case: CheckPublishStatus returns PROCESSING_UPLOAD (or
// PENDING_PUBLISH / IN_REVIEW), the reconciler must LEAVE the
// status as 'publishing' and just record the current state in
// provider_state. The next tick will check again.
func TestReconcileTarget_InFlight_LeavesStatusUnchanged(t *testing.T) {
	posts := &mockPostStore{}
	users := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return &models.PlatformAccount{ID: 10, Platform: "tiktok", PlatformUserID: "tt-1"}, nil
		},
	}
	svc := &mockAsyncProvider{
		baseMockProvider: baseMockProvider{platform: "tiktok"},
		checkStatusFn: func(ctx context.Context, accessToken, publishID string) (string, error) {
			return "PROCESSING_UPLOAD", nil
		},
	}
	vault := &mockCredentialVault{
		renewFn: func(ctx context.Context, accountID int64, tokenType string, refresh credentials.TokenRefresher) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "t"}, nil
		},
	}
	w := newTestWorker(posts, users, "tiktok", svc, vault)

	reconciled, wasFailed, err := w.reconcileTarget(context.Background(), publishingTarget())
	if err != nil {
		t.Fatalf("reconcileTarget: %v", err)
	}
	if reconciled || wasFailed {
		t.Errorf("reconciled=%v wasFailed=%v: want (false, false) for in-flight", reconciled, wasFailed)
	}
	// CRITICAL: no UpdateStatus call — the row is still in-flight, the
	// reconciler must not mutate status.
	if posts.updateCalls != 0 {
		t.Errorf("UpdateStatus calls: want 0 (in-flight, no transition), got %d", posts.updateCalls)
	}
	// But UpdatePublishState IS called — for observability.
	if posts.updatePublishStateCalls != 1 {
		t.Errorf("UpdatePublishState calls: want 1 (record in-flight state), got %d", posts.updatePublishStateCalls)
	}
	if len(posts.updatePublishStateValues) != 1 || posts.updatePublishStateValues[0] != "PROCESSING_UPLOAD" {
		t.Errorf("UpdatePublishState values: want [PROCESSING_UPLOAD], got %v", posts.updatePublishStateValues)
	}
}

// TestReconcileTarget_SyncPlatform_LeavesAlone covers the case
// where the platform doesn't have the AsyncPublisher capability
// (e.g. Instagram, YouTube — they complete their publish in the
// original tick's publishTarget() call, no polling needed).
// The reconciler must not touch these targets.
func TestReconcileTarget_SyncPlatform_LeavesAlone(t *testing.T) {
	posts := &mockPostStore{}
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
	w := newTestWorker(posts, users, "instagram", svc, vault)

	reconciled, wasFailed, err := w.reconcileTarget(context.Background(), publishingTarget())
	if err != nil {
		t.Fatalf("reconcileTarget: %v", err)
	}
	if reconciled || wasFailed {
		t.Errorf("reconciled=%v wasFailed=%v: want (false, false) for sync platform", reconciled, wasFailed)
	}
	// No DB writes for sync platforms.
	if posts.updateCalls != 0 {
		t.Errorf("UpdateStatus calls: want 0 (sync platform, no transition), got %d", posts.updateCalls)
	}
	if posts.updatePublishStateCalls != 0 {
		t.Errorf("UpdatePublishState calls: want 0 (sync platform, no polling), got %d", posts.updatePublishStateCalls)
	}
	// No platform API calls.
	if svc.publishCalls != 0 {
		t.Errorf("Publish calls: want 0 (sync platform, no polling), got %d", svc.publishCalls)
	}
	// No token refresh either (the sync-platform short-circuit happens
	// before the vault.Renew call).
	if vault.ensureCalls != 0 {
		t.Errorf("Renew calls: want 0 (sync platform, no token refresh), got %d", vault.ensureCalls)
	}
}

// TestReconcileTarget_OrphanAccount_MarksFailed covers the
// "platform_account disappeared" failure mode: FindPlatformAccountByID
// returns (nil, nil). The reconciler must mark the target 'failed'
// so it doesn't loop forever.
func TestReconcileTarget_OrphanAccount_MarksFailed(t *testing.T) {
	posts := &mockPostStore{}
	users := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return nil, nil // vanished
		},
	}
	svc := &mockAsyncProvider{baseMockProvider: baseMockProvider{platform: "tiktok"}}
	vault := &mockCredentialVault{}
	w := newTestWorker(posts, users, "tiktok", svc, vault)

	reconciled, wasFailed, err := w.reconcileTarget(context.Background(), publishingTarget())
	if err != nil {
		t.Fatalf("reconcileTarget: %v", err)
	}
	if !reconciled || !wasFailed {
		t.Errorf("reconciled=%v wasFailed=%v: want (true, true) for orphan account", reconciled, wasFailed)
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
}

// TestReconcileTarget_CheckStatusError_LeavesAlone covers the
// transient-error case: CheckPublishStatus returns a 5xx. The
// reconciler must leave the target as-is so the next tick retries
// — failing a target on a transient 5xx would be too aggressive
// (TikTok's SLO is loose).
func TestReconcileTarget_CheckStatusError_LeavesAlone(t *testing.T) {
	posts := &mockPostStore{}
	users := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return &models.PlatformAccount{ID: 10, Platform: "tiktok", PlatformUserID: "tt-1"}, nil
		},
	}
	svc := &mockAsyncProvider{
		baseMockProvider: baseMockProvider{platform: "tiktok"},
		checkStatusFn: func(ctx context.Context, accessToken, publishID string) (string, error) {
			return "", errors.New("502 bad gateway from tiktok")
		},
	}
	vault := &mockCredentialVault{
		renewFn: func(ctx context.Context, accountID int64, tokenType string, refresh credentials.TokenRefresher) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "t"}, nil
		},
	}
	w := newTestWorker(posts, users, "tiktok", svc, vault)

	reconciled, wasFailed, err := w.reconcileTarget(context.Background(), publishingTarget())
	if err != nil {
		t.Fatalf("reconcileTarget: %v (transient errors must NOT propagate as tick errors)", err)
	}
	if reconciled || wasFailed {
		t.Errorf("reconciled=%v wasFailed=%v: want (false, false) for transient error", reconciled, wasFailed)
	}
	// No DB writes — we leave the target alone to retry next tick.
	if posts.updateCalls != 0 {
		t.Errorf("UpdateStatus calls: want 0 (transient error, retry next tick), got %d", posts.updateCalls)
	}
	if posts.updatePublishStateCalls != 0 {
		t.Errorf("UpdatePublishState calls: want 0 (no state to record), got %d", posts.updatePublishStateCalls)
	}
}

// TestTickReconcile_IteratesAllPublishingTargets covers the tickReconcile
// goroutine: it should call ListPublishing, then iterate every returned
// target through reconcileTarget. The call counts and counters let us
// verify the iteration.
func TestTickReconcile_IteratesAllPublishingTargets(t *testing.T) {
	posts := &mockPostStore{
		listPublishingFn: func() ([]models.PostTarget, error) {
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
	// Each CheckPublishStatus returns PROCESSING_UPLOAD so reconcileTarget
	// leaves the target alone. We only care about the iteration here.
	svc := &mockAsyncProvider{
		baseMockProvider: baseMockProvider{platform: "tiktok"},
		checkStatusFn: func(ctx context.Context, accessToken, publishID string) (string, error) {
			return "PROCESSING_UPLOAD", nil
		},
	}
	vault := &mockCredentialVault{
		renewFn: func(ctx context.Context, accountID int64, tokenType string, refresh credentials.TokenRefresher) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "t"}, nil
		},
	}
	w := newTestWorker(posts, users, "tiktok", svc, vault)

	reconciled, failed, err := w.tickReconcile(context.Background())
	if err != nil {
		t.Fatalf("tickReconcile: %v", err)
	}
	// All 3 targets are in-flight → none reconciled, none failed.
	if reconciled != 0 {
		t.Errorf("reconciled: want 0 (all in-flight), got %d", reconciled)
	}
	if failed != 0 {
		t.Errorf("failed: want 0 (all in-flight), got %d", failed)
	}
	// ListPublishing called once.
	if posts.listPublishingCalls != 1 {
		t.Errorf("ListPublishing calls: want 1, got %d", posts.listPublishingCalls)
	}
	// CheckPublishStatus called 3 times — once per target.
	if svc.checkStatusCalls != 3 {
		t.Errorf("CheckPublishStatus calls: want 3 (one per target), got %d", svc.checkStatusCalls)
	}
	// UpdatePublishState called 3 times — one per target, for observability.
	if posts.updatePublishStateCalls != 3 {
		t.Errorf("UpdatePublishState calls: want 3 (one per target), got %d", posts.updatePublishStateCalls)
	}
	// UpdateStatus NOT called — none of them transitioned.
	if posts.updateCalls != 0 {
		t.Errorf("UpdateStatus calls: want 0 (all in-flight), got %d", posts.updateCalls)
	}
}

// TestTickReconcile_EmptyList_NoOp covers the "nothing to do" path.
func TestTickReconcile_EmptyList_NoOp(t *testing.T) {
	posts := &mockPostStore{
		listPublishingFn: func() ([]models.PostTarget, error) {
			return nil, nil
		},
	}
	users := &mockUserStore{}
	svc := &mockAsyncProvider{baseMockProvider: baseMockProvider{platform: "tiktok"}}
	vault := &mockCredentialVault{}
	w := newTestWorker(posts, users, "tiktok", svc, vault)

	reconciled, failed, err := w.tickReconcile(context.Background())
	if err != nil {
		t.Fatalf("tickReconcile: %v", err)
	}
	if reconciled != 0 || failed != 0 {
		t.Errorf("counters: want (0, 0), got (%d, %d)", reconciled, failed)
	}
	if svc.checkStatusCalls != 0 {
		t.Errorf("CheckPublishStatus calls: want 0 (empty list), got %d", svc.checkStatusCalls)
	}
}

// TestTickReconcile_ListError_Propagates covers the "DB unreachable"
// path. tickReconcile must surface the error so the caller can log it.
func TestTickReconcile_ListError_Propagates(t *testing.T) {
	posts := &mockPostStore{
		listPublishingFn: func() ([]models.PostTarget, error) {
			return nil, errors.New("db down")
		},
	}
	users := &mockUserStore{}
	svc := &mockAsyncProvider{baseMockProvider: baseMockProvider{platform: "tiktok"}}
	vault := &mockCredentialVault{}
	w := newTestWorker(posts, users, "tiktok", svc, vault)

	_, _, err := w.tickReconcile(context.Background())
	if err == nil {
		t.Fatal("expected list error to propagate, got nil")
	}
}

// TestRunOnce_BothTicksAndReconcile covers the new runOnce() method:
// it should call BOTH tick() AND tickReconcile() in sequence on
// every interval.
func TestRunOnce_BothTicksAndReconcile(t *testing.T) {
	posts := &mockPostStore{
		listPendingFn: func(before time.Time) ([]models.PostTarget, error) {
			return nil, nil // nothing to publish
		},
		listPublishingFn: func() ([]models.PostTarget, error) {
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
		checkStatusFn: func(ctx context.Context, accessToken, publishID string) (string, error) {
			return "PUBLISH_COMPLETE", nil
		},
	}
	vault := &mockCredentialVault{
		renewFn: func(ctx context.Context, accountID int64, tokenType string, refresh credentials.TokenRefresher) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "t"}, nil
		},
	}
	w := newTestWorker(posts, users, "tiktok", svc, vault)

	w.runOnce(context.Background())

	// tick + reconcile both ran.
	if posts.listPendingCalls != 1 {
		t.Errorf("ListPending calls: want 1 (tick ran), got %d", posts.listPendingCalls)
	}
	if posts.listPublishingCalls != 1 {
		t.Errorf("ListPublishing calls: want 1 (reconcile ran), got %d", posts.listPublishingCalls)
	}
	// The publishing target was reconciled and transitioned to published.
	if posts.updateCalls != 1 {
		t.Errorf("UpdateStatus calls: want 1 (publishing→published), got %d", posts.updateCalls)
	}
	if len(posts.updateTargets) != 1 || posts.updateTargets[0].Status != models.PostStatusPublished {
		t.Errorf("final status: want published, got %+v", posts.updateTargets)
	}
}
