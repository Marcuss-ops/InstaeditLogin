package worker

import (
	"context"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

type fakePublishMediaResolver struct {
	calls    int
	gotPost  *models.Post
	freshURL string
}

func (r *fakePublishMediaResolver) ResolveForUpload(_ context.Context, post *models.Post, _ time.Duration) (string, error) {
	r.calls++
	r.gotPost = post
	return r.freshURL, nil
}

// TestPublishTarget_ScheduledPostReplacesExpiredPersistedURL proves the
// production driver path (claim -> load -> publishTarget -> executePublish)
// never forwards the expired compatibility URL to the publisher.
func TestPublishTarget_ScheduledPostReplacesExpiredPersistedURL(t *testing.T) {
	posts := &mockPostStore{
		claimFn: func(int64) (bool, error) { return true, nil },
		findByIDFn: func(int64) (*models.Post, error) {
			publishAt := time.Now().Add(-time.Minute)
			return &models.Post{
				ID: 101, WorkspaceID: 7,
				MediaURL:  "https://storage.example/stale-get?signature=expired",
				PublishAt: &publishAt,
			}, nil
		},
	}
	users := &mockUserStore{findPlatformAccountFn: func(int64) (*models.PlatformAccount, error) {
		return &models.PlatformAccount{ID: 10, Platform: "instagram", PlatformUserID: "account-10"}, nil
	}}
	resolver := &fakePublishMediaResolver{freshURL: "https://storage.example/fresh-get?signature=new"}
	var publishedURL string
	provider := &mockProvider{
		baseMockProvider: baseMockProvider{platform: "instagram"},
		publishFn: func(_ context.Context, _, _ string, payload models.PublishPayload) (*models.PublishResult, error) {
			publishedURL = payload.VideoURL
			return &models.PublishResult{PlatformMediaID: "remote-201"}, nil
		},
	}
	vault := &mockCredentialVault{renewFn: func(context.Context, int64, string, credentials.TokenRefresher) (*models.OAuthToken, error) {
		return &models.OAuthToken{AccessToken: "access-token"}, nil
	}}
	w := newTestWorkerWithoutThrottle(posts, users, "instagram", provider, vault)
	w.resolver = resolver

	if err := w.publishTarget(context.Background(), scheduledTarget()); err != nil {
		t.Fatalf("publishTarget: %v", err)
	}
	if resolver.calls != 1 {
		t.Fatalf("resolver calls = %d, want 1", resolver.calls)
	}
	if publishedURL != resolver.freshURL {
		t.Fatalf("publisher VideoURL = %q, want fresh URL %q", publishedURL, resolver.freshURL)
	}
	if publishedURL == "https://storage.example/stale-get?signature=expired" {
		t.Fatal("publishTarget forwarded the stale persisted presigned URL")
	}
}

// TestExecutePublish_ScheduledPostReplacesExpiredPersistedURL proves the
// scheduled-post regression: the post keeps its old presigned URL in the
// compatibility/cache column, but the publisher receives a newly-resolved
// URL immediately before the API call.
func TestExecutePublish_ScheduledPostReplacesExpiredPersistedURL(t *testing.T) {
	posts := &mockPostStore{}
	users := &mockUserStore{}
	resolver := &fakePublishMediaResolver{freshURL: "https://storage.example/fresh-get?signature=new"}
	w := newTestWorkerWithoutThrottle(posts, users, "instagram", &mockProvider{
		baseMockProvider: baseMockProvider{platform: "instagram"},
	}, &mockCredentialVault{})
	w.resolver = resolver

	publishAt := time.Now().Add(-time.Minute)
	post := &models.Post{
		ID:          101,
		WorkspaceID: 7,
		MediaURL:    "https://storage.example/stale-get?signature=expired",
		PublishAt:   &publishAt,
	}
	account := &models.PlatformAccount{ID: 10, Platform: "instagram", PlatformUserID: "account-10"}
	target := &models.PostTarget{ID: 201, PostID: post.ID, PlatformAccountID: account.ID}
	oauthToken := &models.OAuthToken{AccessToken: "access-token"}

	var publishedURL string
	publisher := &mockProvider{
		baseMockProvider: baseMockProvider{platform: "instagram"},
		publishFn: func(_ context.Context, _, _ string, payload models.PublishPayload) (*models.PublishResult, error) {
			publishedURL = payload.VideoURL
			return &models.PublishResult{PlatformMediaID: "remote-201"}, nil
		},
	}

	if err := w.executePublish(context.Background(), target, account, post, oauthToken, models.PublishPayload{
		VideoURL: "https://storage.example/stale-get?signature=expired",
	}, publisher); err != nil {
		t.Fatalf("executePublish: %v", err)
	}
	if resolver.calls != 1 {
		t.Fatalf("resolver calls = %d, want 1", resolver.calls)
	}
	if resolver.gotPost != post {
		t.Fatal("resolver did not receive the scheduled post")
	}
	if publishedURL != resolver.freshURL {
		t.Fatalf("publisher VideoURL = %q, want fresh URL %q", publishedURL, resolver.freshURL)
	}
	if publishedURL == post.MediaURL {
		t.Fatal("publisher received the stale persisted presigned URL")
	}
}
