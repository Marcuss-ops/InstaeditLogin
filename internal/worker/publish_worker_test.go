package worker

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------

// mockPostStore is a PublisherPostStore with configurable function fields
// and call counters. The counters let each test assert the exact ordering
// of repository calls (e.g. Claim must happen BEFORE FindByID).
type mockPostStore struct {
	// Call counters — one per method, incremented on every invocation.
	// Tests assert on the relative ordering (e.g. claimCalls > 0 before
	// findByIDCalls is allowed) and the final counts.
	claimCalls    int
	findByIDCalls int
	updateCalls   int

	// Function fields — each test overrides only what it exercises.
	claimFn        func(id int64) (bool, error)
	findByIDFn     func(id int64) (*models.Post, error)
	updateStatusFn func(*models.PostTarget) error

	// Captured targets from UpdateStatus — lets tests inspect the
	// final status (published vs failed) and assert on the worker
	// writing the right terminal state. Stored as struct values
	// (not pointers) so later mutations to the caller's target
	// don't leak into the captured snapshot.
	updateTargets []models.PostTarget
}

func (m *mockPostStore) ListPending(before time.Time) ([]models.PostTarget, error) {
	// Not exercised by publishTarget tests; the worker drives it
	// from tick() but publishTarget itself only takes a single
	// target. Return an empty slice to keep the interface satisfied.
	return nil, nil
}
func (m *mockPostStore) FindByID(id int64) (*models.Post, error) {
	m.findByIDCalls++
	if m.findByIDFn == nil {
		return nil, errors.New("FindByID not implemented in this test")
	}
	return m.findByIDFn(id)
}
func (m *mockPostStore) ClaimScheduledTarget(id int64) (bool, error) {
	m.claimCalls++
	if m.claimFn == nil {
		return false, errors.New("ClaimScheduledTarget not implemented in this test")
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

// mockPlatformService is a services.PlatformService with configurable
// function fields. Only the methods exercised by publishTarget
// (EnsureFreshToken, Publish) need concrete wiring; the rest panic
// to make accidental use obvious during test maintenance.
type mockPlatformService struct {
	platform      string
	ensureFreshFn func(ctx context.Context, accountID int64, tokenType string, refresh services.TokenRefresher) (*models.OAuthToken, error)
	publishFn     func(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (*models.PublishResult, error)

	// Call counters — used to prove the loser branch of the claim
	// never reaches the platform API (no Publish call when claim=false).
	ensureFreshCalls int
	publishCalls     int
}

func (m *mockPlatformService) GetPlatform() string { return m.platform }
func (m *mockPlatformService) GetLoginURL(state string) string {
	panic("GetLoginURL not used in worker tests")
}
func (m *mockPlatformService) HandleCallback(ctx context.Context, state, code string) (*models.PlatformProfile, *models.TokenData, error) {
	panic("HandleCallback not used in worker tests")
}
func (m *mockPlatformService) RefreshOAuthToken(ctx context.Context, refreshToken string) (*models.TokenData, error) {
	panic("RefreshOAuthToken not used in worker tests")
}
func (m *mockPlatformService) Publish(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (*models.PublishResult, error) {
	m.publishCalls++
	if m.publishFn == nil {
		return nil, errors.New("Publish not implemented in this test")
	}
	return m.publishFn(ctx, accessToken, platformUserID, payload)
}
func (m *mockPlatformService) SaveEncryptedToken(platformAccountID int64, tokenData *models.TokenData) error {
	panic("SaveEncryptedToken not used in worker tests")
}
func (m *mockPlatformService) GetDecryptedToken(platformAccountID int64, tokenType string) (*models.OAuthToken, error) {
	panic("GetDecryptedToken not used in worker tests")
}
func (m *mockPlatformService) EnsureFreshToken(ctx context.Context, accountID int64, tokenType string, refresh services.TokenRefresher) (*models.OAuthToken, error) {
	m.ensureFreshCalls++
	if m.ensureFreshFn == nil {
		return nil, errors.New("EnsureFreshToken not implemented in this test")
	}
	return m.ensureFreshFn(ctx, accountID, tokenType, refresh)
}

// newTestWorker builds a PublishWorker wired with the given mocks.
// interval is small (10ms) but irrelevant — the tests call publishTarget
// directly rather than driving the Run loop.
func newTestWorker(posts *mockPostStore, users *mockUserStore, svc *mockPlatformService) *PublishWorker {
	return NewPublishWorker(
		posts,
		users,
		map[string]services.PlatformService{"meta": svc},
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

// ---------------------------------------------------------------------------
// publishTarget tests
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
				Platform:       "meta",
				PlatformUserID: "fb-123",
			}, nil
		},
	}
	svc := &mockPlatformService{
		platform: "meta",
		ensureFreshFn: func(ctx context.Context, accountID int64, tokenType string, refresh services.TokenRefresher) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "fresh-tok", TokenType: "bearer"}, nil
		},
		publishFn: func(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (*models.PublishResult, error) {
			return &models.PublishResult{PlatformMediaID: "media-456"}, nil
		},
	}
	w := newTestWorker(posts, users, svc)

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
	if svc.ensureFreshCalls != 1 {
		t.Errorf("EnsureFreshToken calls: want 1, got %d (BEFORE Publish should have refreshed the OAuth token)", svc.ensureFreshCalls)
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

// TestPublishTarget_ClaimLoss_SkipsWithoutPublish is the verdict §10
// double-publish-prevention test: when ClaimScheduledTarget returns
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
	svc := &mockPlatformService{
		platform: "meta",
		ensureFreshFn: func(ctx context.Context, accountID int64, tokenType string, refresh services.TokenRefresher) (*models.OAuthToken, error) {
			t.Error("EnsureFreshToken called despite claim loss")
			return nil, nil
		},
		publishFn: func(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (*models.PublishResult, error) {
			t.Error("Publish called despite claim loss (verdict §10 — this is the double-publish the claim was supposed to prevent)")
			return nil, nil
		},
	}
	w := newTestWorker(posts, users, svc)

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
	if svc.ensureFreshCalls != 0 {
		t.Errorf("EnsureFreshToken calls: want 0, got %d (claim-loss must short-circuit)", svc.ensureFreshCalls)
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
			return &models.PlatformAccount{ID: 10, Platform: "meta", PlatformUserID: "fb-1"}, nil
		},
	}
	svc := &mockPlatformService{
		platform: "meta",
		ensureFreshFn: func(ctx context.Context, accountID int64, tokenType string, refresh services.TokenRefresher) (*models.OAuthToken, error) {
			order = append(order, "ensureFresh")
			return &models.OAuthToken{AccessToken: "t"}, nil
		},
		publishFn: func(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (*models.PublishResult, error) {
			order = append(order, "publish")
			return &models.PublishResult{PlatformMediaID: "ok"}, nil
		},
	}
	w := newTestWorker(posts, users, svc)

	if err := w.publishTarget(context.Background(), scheduledTarget()); err != nil {
		t.Fatalf("publishTarget: %v", err)
	}
	want := []string{"claim", "findByID", "findAccount", "ensureFresh", "publish"}
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
// must be ClaimScheduledTarget. This is the simplest expression of
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
	svc := &mockPlatformService{
		platform: "meta",
		ensureFreshFn: func(ctx context.Context, accountID int64, tokenType string, refresh services.TokenRefresher) (*models.OAuthToken, error) {
			order = append(order, "ensureFresh")
			return nil, nil
		},
		publishFn: func(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (*models.PublishResult, error) {
			order = append(order, "publish")
			return nil, nil
		},
	}
	w := newTestWorker(posts, users, svc)

	if err := w.publishTarget(context.Background(), scheduledTarget()); err != nil {
		t.Fatalf("publishTarget: %v", err)
	}
	if len(order) != 1 || order[0] != "claim" {
		t.Errorf("on claim-loss, only ClaimScheduledTarget should run; got order=%v", order)
	}
}

// TestPublishTarget_ClaimError_Propagates covers the path where
// ClaimScheduledTarget itself returns an error (DB unreachable, etc.).
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
	svc := &mockPlatformService{platform: "meta"}
	w := newTestWorker(posts, users, svc)

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
	svc := &mockPlatformService{platform: "meta"}
	w := newTestWorker(posts, users, svc)

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
			return &models.PlatformAccount{ID: 10, Platform: "meta", PlatformUserID: "fb-1"}, nil
		},
	}
	svc := &mockPlatformService{
		platform: "meta",
		ensureFreshFn: func(ctx context.Context, accountID int64, tokenType string, refresh services.TokenRefresher) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "t"}, nil
		},
		publishFn: func(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (*models.PublishResult, error) {
			return nil, errors.New("500 internal error from meta")
		},
	}
	w := newTestWorker(posts, users, svc)

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
// (TestPostClaimScheduledTarget_Success/AlreadyClaimed) plus
// the WHERE-status='scheduled' guard in the actual UPDATE.
func TestPublishTarget_OneClaimWinner_OnlyWinnerPublishes(t *testing.T) {
	t.Parallel()

	// Use a real (in-process) mutex to deterministically simulate
	// the DB's row-level locking. The first worker to acquire the
	// mutex sets claimedBy; the second sees it and returns
	// claimed=false. This matches the semantics of `UPDATE ...
	// WHERE status='scheduled'` under contention.
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
	// happy path (FindByID → EnsureFreshToken → Publish).
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
			return &models.PlatformAccount{ID: 10, Platform: "meta", PlatformUserID: "fb-1"}, nil
		},
	}
	svcA := &mockPlatformService{
		platform: "meta",
		ensureFreshFn: func(ctx context.Context, accountID int64, tokenType string, refresh services.TokenRefresher) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "t"}, nil
		},
		publishFn: func(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (*models.PublishResult, error) {
			recordPublish()
			return &models.PublishResult{PlatformMediaID: "media-A"}, nil
		},
	}
	wA := newTestWorker(postsA, usersA, svcA)

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
			return &models.PlatformAccount{ID: 10, Platform: "meta", PlatformUserID: "fb-1"}, nil
		},
	}
	svcB := &mockPlatformService{
		platform: "meta",
		ensureFreshFn: func(ctx context.Context, accountID int64, tokenType string, refresh services.TokenRefresher) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "t"}, nil
		},
		publishFn: func(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (*models.PublishResult, error) {
			recordPublish()
			return &models.PublishResult{PlatformMediaID: "media-B"}, nil
		},
	}
	wB := newTestWorker(postsB, usersB, svcB)

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
	// scheduling; the loser's findByIDCalls + ensureFreshCalls +
	// publishCalls + updateCalls must all be 0.)
	winnerPosts, loserPosts := postsA, postsB
	winnerSvc, loserSvc := svcA, svcB
	if claimedBy == "B" {
		winnerPosts, loserPosts = postsB, postsA
		winnerSvc, loserSvc = svcB, svcA
	}
	_ = winnerPosts // winner's call counts are exercised by the happy-path test
	_ = winnerSvc
	if loserPosts.findByIDCalls != 0 {
		t.Errorf("loser FindByID calls: want 0, got %d (claim-loss must short-circuit BEFORE post load)", loserPosts.findByIDCalls)
	}
	if loserSvc.ensureFreshCalls != 0 {
		t.Errorf("loser EnsureFreshToken calls: want 0, got %d", loserSvc.ensureFreshCalls)
	}
	if loserSvc.publishCalls != 0 {
		t.Errorf("loser Publish calls: want 0, got %d (CRITICAL: this is the double-publish path)", loserSvc.publishCalls)
	}
	if loserPosts.updateCalls != 0 {
		t.Errorf("loser UpdateStatus calls: want 0, got %d (claim-loss must NOT mutate status — winner owns the row)", loserPosts.updateCalls)
	}
}
