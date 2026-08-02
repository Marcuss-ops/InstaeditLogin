package worker

import (
	"context"
	"errors"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

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
