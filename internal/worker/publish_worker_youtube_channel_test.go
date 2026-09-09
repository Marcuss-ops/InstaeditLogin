package worker

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

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

// TestPublishTarget_PrivacyLevel asserts the precedence cascade for
// payload.PrivacyLevel:
//
//	post.PrivacyLevel > post.DefaultPrivacyLevel > platform fallback
//
// For YouTube the fallback is "unlisted"; for other platforms it is
// "PUBLIC_TO_EVERYONE".
func TestPublishTarget_PrivacyLevel(t *testing.T) {
	cases := []struct {
		name        string
		platform    string
		post        models.Post
		wantPrivacy string
	}{
		{
			name:        "PostOverrideWins",
			platform:    "youtube",
			post:        models.Post{ID: 100, Caption: "x", MediaURL: "https://cdn.example.com/video.mp4", PrivacyLevel: "private", DefaultPrivacyLevel: "unlisted"},
			wantPrivacy: "private",
		},
		{
			name:        "PostDefaultWinsOverFallback",
			platform:    "youtube",
			post:        models.Post{ID: 100, Caption: "x", MediaURL: "https://cdn.example.com/video.mp4", DefaultPrivacyLevel: "public"},
			wantPrivacy: "public",
		},
		{
			name:        "YouTubeFallbackIsUnlisted",
			platform:    "youtube",
			post:        models.Post{ID: 100, Caption: "x", MediaURL: "https://cdn.example.com/video.mp4"},
			wantPrivacy: "unlisted",
		},
		{
			name:        "NonYouTubeKeepsPublicToEveryone",
			platform:    "instagram",
			post:        models.Post{ID: 100, Caption: "ig-caption", MediaURL: "https://cdn.example.com/ig.mp4", Status: models.PostStatusScheduled},
			wantPrivacy: "PUBLIC_TO_EVERYONE",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := tc.post
			posts := &mockPostStore{
				claimFn:    func(id int64) (bool, error) { return true, nil },
				findByIDFn: func(id int64) (*models.Post, error) { return &p, nil },
			}
			users := &mockUserStore{
				findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
					return &models.PlatformAccount{ID: 10, Platform: tc.platform, PlatformUserID: "u"}, nil
				},
			}
			var capturedPrivacy string
			svc := &mockProvider{
				baseMockProvider: baseMockProvider{platform: tc.platform},
				publishFn: func(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (*models.PublishResult, error) {
					capturedPrivacy = payload.PrivacyLevel
					return &models.PublishResult{PlatformMediaID: "m"}, nil
				},
			}
			vault := &mockCredentialVault{
				renewFn: func(ctx context.Context, accountID int64, tokenType string, refresh credentials.TokenRefresher) (*models.OAuthToken, error) {
					return &models.OAuthToken{AccessToken: "t"}, nil
				},
			}
			w := newTestWorker(posts, users, tc.platform, svc, vault)
			if err := w.publishTarget(context.Background(), scheduledTarget()); err != nil {
				t.Fatalf("publishTarget: %v", err)
			}
			if capturedPrivacy != tc.wantPrivacy {
				t.Errorf("payload.PrivacyLevel: want %q, got %q", tc.wantPrivacy, capturedPrivacy)
			}
		})
	}
}

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
				ID:       100,
				Caption:  "yt-caption",
				Title:    "yt-title",
				MediaURL: "https://cdn.example.com/yt-video.mp4",
				Status:   models.PostStatusScheduled,
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

func TestPublishTarget_YouTube_ReauthRequiredBlocksBeforeRenew(t *testing.T) {
	posts := &mockPostStore{
		claimFn: func(id int64) (bool, error) { return true, nil },
		findByIDFn: func(id int64) (*models.Post, error) {
			return &models.Post{ID: 100, Caption: "blocked", MediaURL: "https://cdn.example.com/video.mp4"}, nil
		},
	}
	users := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return &models.PlatformAccount{
				ID:             10,
				Platform:       models.PlatformYouTube,
				PlatformUserID: "UCreauth",
				Status:         models.AccountStatusReauthRequired,
			}, nil
		},
	}
	svc := &mockProvider{
		baseMockProvider: baseMockProvider{platform: models.PlatformYouTube},
		publishFn: func(context.Context, string, string, models.PublishPayload) (*models.PublishResult, error) {
			t.Fatal("Publish must not run for a reauth_required account")
			return nil, nil
		},
	}
	vault := &mockCredentialVault{
		renewFn: func(context.Context, int64, string, credentials.TokenRefresher) (*models.OAuthToken, error) {
			t.Fatal("Renew must not run for a reauth_required account")
			return nil, nil
		},
	}
	w := newTestWorker(posts, users, models.PlatformYouTube, svc, vault)

	if err := w.publishTarget(context.Background(), scheduledTarget()); err == nil {
		t.Fatal("publishTarget must return a blocked-auth error")
	}
	if vault.ensureCalls != 0 {
		t.Errorf("Renew calls: want 0, got %d", vault.ensureCalls)
	}
	if svc.publishCalls != 0 {
		t.Errorf("Publish calls: want 0, got %d", svc.publishCalls)
	}
	if posts.updateCalls != 1 || posts.updateTargets[0].Status != models.PostStatusBlockedAuth {
		t.Fatalf("target transition: want one blocked_auth update, got calls=%d targets=%v", posts.updateCalls, posts.updateTargets)
	}
}
