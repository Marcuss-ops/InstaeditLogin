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
//     reconciler-only surfaces (ListPublishing, UpdateStatus).
//
// The split is intentional: a regression that introduces a "reconciler
// calls SetProviderIdempotencyKey" bug would fail to compile on the
// reconciler side; a "driver calls UpdateStatus" bug would not
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
//
// Taglio P0#3: added MarkReauthRequiredFn + a counter so tests can
// assert on whether the worker flagged the platform_account
// reauth_required after a YouTube channel binding mismatch. The
// counter is increment-protected because some tests drive multiple
// targets concurrently.
type mockUserStore struct {
	mu                      sync.Mutex
	findPlatformAccountFn   func(id int64) (*models.PlatformAccount, error)
	markReauthRequiredFn    func(ctx context.Context, id int64, code, message string) error
	markReauthRequiredCalls int
	lastMarkReauthCode      string
	lastMarkReauthMessage   string
	lastMarkReauthAccountID int64
}

func (m *mockUserStore) FindPlatformAccountByID(id int64) (*models.PlatformAccount, error) {
	if m.findPlatformAccountFn == nil {
		return nil, errors.New("FindPlatformAccountByID not implemented in this test")
	}
	return m.findPlatformAccountFn(id)
}

// MarkReauthRequired (P0#3 server-side YouTube channel binding
// check) delegates to the configured function. Tests assert on
// either the returned error, the recorded call count, or the
// (code, message) passed by the worker. The default (no fn
// configured) succeeds silently so existing tests that don't
// exercise the channel-binding check keep their prior assertion
// surface — the new interface method compiles in cleanly without
// disrupting the publisher flow.
func (m *mockUserStore) MarkReauthRequired(ctx context.Context, id int64, code, message string) error {
	m.mu.Lock()
	m.markReauthRequiredCalls++
	m.lastMarkReauthCode = code
	m.lastMarkReauthMessage = message
	m.lastMarkReauthAccountID = id
	m.mu.Unlock()
	if m.markReauthRequiredFn == nil {
		return nil
	}
	return m.markReauthRequiredFn(ctx, id, code, message)
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
//
// Taglio P0#3: added an OPTIONAL validateChannelBindingFn +
// ValidateChannelBinding method so the same struct doubles as a
// services.YouTubeChannelBinder. Tests that exercise the YouTube
// pre-upload channel binding check set the fn; tests that don't
// (Instagram, Twitter, etc.) leave it nil and ValidateChannelBinding
// returns nil — the existing publish path proceeds unaffected.
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
	// validateChannelBindingFn (P0#3) — when non-nil, the worker's
	// YouTube pre-upload binding check delegates to this function.
	// When nil (default), ValidateChannelBinding returns nil and the
	// publish path proceeds as if the check passed.
	validateChannelBindingFn func(ctx context.Context, accessToken, expectedChannelID string) error
	// validateChannelBindingCalls — mu-protected counter for tests
	// asserting the check was invoked exactly once per target.
	validateChannelBindingCalls int
	// canaryUploadFn (Task 7/10) — when non-nil, CanaryUpload returns
	// whatever this fn returns. When nil (default), returns nil +
	// services.ErrYouTubeCanaryRejected so the worker's
	// SetCanonicalCanaryUploader(nil) defensive path is exercised.
	canaryUploadFn func(ctx context.Context, accessToken, expectedChannelID string) (*services.CanaryUploadResult, error)
	// canaryUploadCalls — mu-protected counter so tests can assert the
	// pre-flight was invoked exactly N times.
	canaryUploadCalls int
	// capturedAccessToken / capturedExpectedChannelID record the
	// inputs to ValidateChannelBinding so tests can assert the
	// worker forwarded the post-renew access_token + the platform
	// account's platform_user_id (not stale values).
	capturedAccessToken     string
	capturedExpectedChannel string
}

func (m *mockProvider) GetLoginURL(state string) string {
	panic("GetLoginURL not used in worker tests")
}
func (m *mockProvider) GetLoginURLWithOptions(state string, _ services.OAuthLoginOptions) string {
	panic("GetLoginURLWithOptions not used in worker tests")
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

// ValidateChannelBinding (P0#3) implements services.YouTubeChannelBinder
// when the test has set validateChannelBindingFn. When the fn is nil
// (default for non-YouTube tests), returns nil so the worker's
// publish path proceeds unchanged. Tests that exercise the mismatch /
// transient path set the fn accordingly.
//
// captures accessToken + expectedChannelID so tests can assert the
// worker forwarded the post-renew token (NOT a stale pre-renew
// value) and the platform_account.platform_user_id (NOT a hard-coded
// placeholder).
func (m *mockProvider) ValidateChannelBinding(ctx context.Context, accessToken, expectedChannelID string) error {
	m.mu.Lock()
	m.validateChannelBindingCalls++
	m.capturedAccessToken = accessToken
	m.capturedExpectedChannel = expectedChannelID
	m.mu.Unlock()
	if m.validateChannelBindingFn == nil {
		return nil
	}
	return m.validateChannelBindingFn(ctx, accessToken, expectedChannelID)
}

// CanaryUpload (Task 7/10) implements services.YouTubeCanaryUploader.
// When canaryUploadFn is nil (default), returns nil +
// services.ErrYouTubeCanaryRejected — matches the production
// shape when the canary capability is absent.
func (m *mockProvider) CanaryUpload(ctx context.Context, accessToken, expectedChannelID string) (*services.CanaryUploadResult, error) {
	m.mu.Lock()
	m.canaryUploadCalls++
	m.mu.Unlock()
	if m.canaryUploadFn == nil {
		return nil, services.ErrYouTubeCanaryRejected
	}
	return m.canaryUploadFn(ctx, accessToken, expectedChannelID)
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
func (m *mockAsyncProvider) GetLoginURLWithOptions(state string, _ services.OAuthLoginOptions) string {
	panic("GetLoginURLWithOptions not used in worker tests")
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

// mockCredentialVault is a credentials.VaultAPI. The worker calls
// Renew (via the vault field on PublishWorker/ReconcileWorker) and,
// for page-scoped providers, Get to retrieve a TokenTypePageAccess.
// Save / Revoke / Rotate are stubbed (panic if accidentally called).
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
	getFn       func(ctx context.Context, platformAccountID int64, tokenType string) (*models.OAuthToken, error)
	ensureCalls int
}

func (m *mockCredentialVault) Save(ctx context.Context, platformAccountID int64, tokenData *models.TokenData) error {
	panic("Save not used in worker tests")
}
func (m *mockCredentialVault) Get(ctx context.Context, platformAccountID int64, tokenType string) (*models.OAuthToken, error) {
	if m.getFn != nil {
		return m.getFn(ctx, platformAccountID, tokenType)
	}
	// Default: behave like a real vault that has no page token stored.
	// The publish worker will fall back to the refreshed user token.
	return nil, errors.New("token not found")
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
		"test-worker-id",
		nil, // no MemoryLimiter needed in unit tests
		10*time.Millisecond,
		nil, // inherit slog.Default()
	)
}

// newTestWorkerWithoutThrottle is like newTestWorker but with the
// platform throttle disabled (nil). Use this in tests that call
// publishTarget multiple times to avoid blocking on Wait().
func newTestWorkerWithoutThrottle(posts PublisherPostStore, users *mockUserStore, name string, svc any, vault *mockCredentialVault) *PublishWorker {
	w := newTestWorker(posts, users, name, svc, vault)
	w.throttle = nil
	return w
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
		"test-worker-id",
		nil, // no MemoryLimiter needed in unit tests
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
