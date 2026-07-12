// Package worker shared test fixtures for publish_worker_test.go and
// reconcile_worker_test.go. Both _test.go files compile into the
// same package worker test binary, so the mocks defined here are
// available to both. Mock structs that have structurally-different
// surface per goroutine stay in their respective test files:
//
//   - mockPostStore         (publish_worker_test.go) — driver-only
//     surfaces (ListPending, ClaimQueuedTarget, FindByID,
//     SetProviderIdempotencyKey).
//   - mockReconcilePostStore (reconcile_worker_test.go) —
//     reconciler-only surfaces (ListPublishing, UpdatePublishState).
//
// The split is intentional: a regression that introduces a "reconciler
// calls SetProviderIdempotencyKey" bug would fail to compile on the
// reconciler side; a "driver calls UpdatePublishState" bug would not
// compile on the driver side. The interface split ALSO compiles-in
// this invariant.
package worker

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// ------------------------------------------------------------------
// mockUserStore — shared between PublishWorker + ReconcileWorker.
// Both depend on the identical lookup (resolver for orphan-account
// detection on the publishing / reconciling paths), so one mock
// serves both signatures. ReconcileWorker's PublisherUserStore alias
// (declared in reconcile_worker.go) uses the same type machinery.
// ------------------------------------------------------------------

// mockUserStore is a PublisherUserStore with a configurable lookup.
// Pass into NewPublishWorker / NewReconcileWorker via the userRepo
// argument; both share the same FindPlatformAccountByID contract.
type mockUserStore struct {
	findPlatformAccountFn func(id int64) (*models.PlatformAccount, error)
}

func (m *mockUserStore) FindPlatformAccountByID(id int64) (*models.PlatformAccount, error) {
	if m.findPlatformAccountFn == nil {
		return nil, errors.New("FindPlatformAccountByID not implemented in this test")
	}
	return m.findPlatformAccountFn(id)
}

// ------------------------------------------------------------------
// Platform provider mocks — shared between driver + reconciler tests.
// ------------------------------------------------------------------

// baseMockProvider holds the shared OAuthProvider methods. Embedded
// so mockProvider and mockAsyncProvider both satisfy
// services.OAuthProvider without duplicating the methods.
type baseMockProvider struct {
	platform string
}

func (b *baseMockProvider) Name() string { return b.platform }

// mockProvider is the SYNC-ONLY platform provider mock. Satisfies
// services.OAuthProvider (via the embedded base) and
// services.Publisher, but NOT services.AsyncPublisher. Use for
// tests that exercise sync-only platforms (Instagram, YouTube)
// where the publish() call completes the publish synchronously
// and there is no async state machine to drive.
//
// For TikTok-style async platforms, use mockAsyncProvider below.
type mockProvider struct {
	mu sync.Mutex
	baseMockProvider
	publishFn func(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (*models.PublishResult, error)
	// publishCalls counter — used to prove the loser branch of the
	// claim never reaches the platform API (no Publish call when
	// claim=false).
	publishCalls int
	// capturedPayload holds the last Publish() payload the worker
	// forwarded. Tests assert on fields like payload.IdempotencyKey
	// (Taglio 4.7 LEVEL 2 stamp-and-forward invariant).
	capturedPayload *models.PublishPayload
}

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
	m.mu.Lock()
	m.publishCalls++
	m.capturedPayload = &payload
	m.mu.Unlock()
	if m.publishFn == nil {
		return nil, errors.New("Publish not implemented in this test")
	}
	return m.publishFn(ctx, accessToken, platformUserID, payload)
}

// mockAsyncProvider (Taglio 4.2) satisfies services.AsyncPublisher
// in addition to Publisher. The router will register it under the
// AsyncPublisher capability; the reconciler goroutine will pick it
// up on every tick to drive the 4-step state machine.
//
// Use for tests that exercise async platforms (TikTok today). For
// sync platforms use mockProvider instead.
type mockAsyncProvider struct {
	mu sync.Mutex
	baseMockProvider
	publishFn         func(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (*models.PublishResult, error)
	startPublishFn    func(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (string, string, error)
	checkStatusFn     func(ctx context.Context, accessToken, publishID string) (string, error)
	continuePublishFn func(ctx context.Context, accessToken, publishID string) error
	reconcileFn       func(ctx context.Context, accessToken, publishID string) (*models.PublishResult, error)
	publishCalls      int
	startPublishCalls int
	checkStatusCalls  int
	continueCalls     int
	reconcileCalls    int
	capturedPayload   *models.PublishPayload
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
	m.mu.Lock()
	m.publishCalls++
	m.capturedPayload = &payload
	m.mu.Unlock()
	if m.publishFn == nil {
		return nil, errors.New("Publish not implemented in this test")
	}
	return m.publishFn(ctx, accessToken, platformUserID, payload)
}

// StartPublish (Taglio 4.2, async only) — default: derive publish_id
// from the configured publishFn (the real TikTok StartPublish returns
// a publish_id synchronously). Tests that need a specific publish_id
// can set startPublishFn directly.
func (m *mockAsyncProvider) StartPublish(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (string, string, error) {
	m.mu.Lock()
	m.startPublishCalls++
	m.mu.Unlock()
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
	m.mu.Lock()
	m.checkStatusCalls++
	m.mu.Unlock()
	if m.checkStatusFn == nil {
		return "", errors.New("CheckPublishStatus not implemented in this test")
	}
	return m.checkStatusFn(ctx, accessToken, publishID)
}

// ContinuePublish (Taglio 4.2, async only) — PULL_FROM_URL no-op.
func (m *mockAsyncProvider) ContinuePublish(ctx context.Context, accessToken, publishID string) error {
	m.mu.Lock()
	m.continueCalls++
	m.mu.Unlock()
	if m.continuePublishFn != nil {
		return m.continuePublishFn(ctx, accessToken, publishID)
	}
	return nil // PULL_FROM_URL: no-op default
}

// Reconcile (Taglio 4.2, async only) — terminal-state detector.
func (m *mockAsyncProvider) Reconcile(ctx context.Context, accessToken, publishID string) (*models.PublishResult, error) {
	m.mu.Lock()
	m.reconcileCalls++
	m.mu.Unlock()
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

// ------------------------------------------------------------------
// mockCredentialVault — shared between driver + reconciler.
// ------------------------------------------------------------------

// mockCredentialVault is a credentials.VaultAPI. The worker only
// calls Renew (via the vault field on PublishWorker/ReconcileWorker),
// so Save / Get / Revoke / Rotate are stubbed (panic if accidentally
// called).
//
// Taglio 2.2: renamed from mockTokenService. The `renewFn` signature
// now takes a credentials.TokenRefresher (plain function) rather than
// a services.OAuthProvider — the vault has zero knowledge of
// per-platform types, so the worker adapts
// OAuthProvider.RefreshOAuthToken into a closure at the call site.
// The test never needs to call the refresher itself; it just returns
// a valid token.
type mockCredentialVault struct {
	mu          sync.Mutex
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
	m.mu.Lock()
	m.ensureCalls++
	m.mu.Unlock()
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

// ------------------------------------------------------------------
// Constructor helpers — one per goroutine type.
// ------------------------------------------------------------------

// newTestWorker builds a PublishWorker wired with the given mocks.
// interval is small (10ms) but irrelevant — the tests call
// publishTarget / runOnce directly rather than driving the Run loop.
//
// The provider can be a mockProvider (sync) or a mockAsyncProvider
// (async) — the router registers whichever capability set the value
// structurally satisfies.
func newTestWorker(posts PublisherPostStore, users *mockUserStore, name string, svc any, vault *mockCredentialVault) *PublishWorker {
	_ = posts // type-narrowing hint for the Reader; not used here
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

// newTestReconcileWorker builds a ReconcileWorker wired with the
// given mocks. interval is small (10ms) but irrelevant — tests call
// tickReconcile / reconcileTarget directly, or drive Run on a
// goroutine terminated by cancel.
func newTestReconcileWorker(posts ReconcilePostStore, users *mockUserStore, name string, svc any, vault *mockCredentialVault) *ReconcileWorker {
	router := services.NewCapabilityRouter()
	router.Register(name, svc)
	return NewReconcileWorker(
		posts,
		users,
		router,
		vault,
		10*time.Millisecond,
		nil, // inherit slog.Default()
	)
}

// ------------------------------------------------------------------
// Target builders.
// ------------------------------------------------------------------

// scheduledTarget — builds a scheduled target the driver can pick
// up via ListPending. Reconciler tests don't use this; included in
// the shared fixtures for consistency with the existing tests.
func scheduledTarget() *models.PostTarget {
	return &models.PostTarget{
		ID:                200,
		PostID:            100,
		PlatformAccountID: 10,
		Status:            models.PostStatusScheduled,
	}
}

// publishingTarget — builds a publishing target the reconciler can
// pick up via ListPublishing. The PlatformPostID is the (fake)
// publish_id the reconciler passes to Reconcile.
func publishingTarget() *models.PostTarget {
	return &models.PostTarget{
		ID:                300,
		PostID:            100,
		PlatformAccountID: 10,
		Status:            models.PostStatusPublishing,
		PlatformPostID:    "publish-id-abc",
	}
}

// Compile-time assertion that the mockUserStore here satisfies the
// runtime interface used by the production wiring. Caught at
// go vet time, not at runtime. ReconcileUserStore is a type alias
// of PublisherUserStore (declared in reconcile_worker.go), so a
// single assertion on PublisherUserStore is sufficient — adding a
// second `_ ReconcileUserStore = ...` would test the identical
// underlying type and produce a redundant identity check.
var _ PublisherUserStore = (*mockUserStore)(nil)
