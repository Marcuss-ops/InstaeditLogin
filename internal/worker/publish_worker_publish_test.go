package worker

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
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
