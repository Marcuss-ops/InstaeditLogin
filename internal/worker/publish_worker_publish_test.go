package worker

import (
	"context"
	"errors"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

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
		t.Errorf("ClaimQueuedTargetWithLease calls: want 1, got %d", posts.claimCalls)
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
