package worker

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// TestPublishTarget_YouTube_NativePublishAt_SkipsVideosUpdate validates
// the migration-126 native-scheduling fast path: when the Phase-1
// videos.insert carried status.publishAt (recorded on
// youtube_target_publications.native_publish_at), YouTube itself owns
// the private→public transition — so at publish_at the worker must NOT
// call UpdateVideoPrivacy (videos.update): that call would be redundant
// AND burns ~50 units from the 2026 "general" quota bucket per
// scheduled public video. The worker only stamps the target published.
//
// Assertions:
//  1. publisher.Publish NOT called (bypass preserved).
//  2. UpdateVideoPrivacy NOT called (native fast path — the whole point).
//  3. The post_target transitioned to status=published with the
//     composite PlatformPostID (channelID:videoID) + PublishedAt set.
//  4. The youtube_target_publications row got MarkPublished exactly once.
func TestPublishTarget_YouTube_NativePublishAt_SkipsVideosUpdate(t *testing.T) {
	const (
		phase1VideoID          = "PHASE1_VID_NATIVE"
		phase1YTPubRowID int64 = 8888
	)

	publishAt := time.Now().UTC().Add(1 * time.Hour)

	posts := &mockPostStore{
		claimFn: func(id int64) (bool, error) { return true, nil },
		findByIDFn: func(id int64) (*models.Post, error) {
			return &models.Post{
				ID:           100,
				Caption:      "yt-caption",
				Title:        "yt-title",
				MediaURL:     "https://cdn.example.com/yt-video.mp4",
				PrivacyLevel: "public",
				PublishAt:    &publishAt,
				Status:       models.PostStatusScheduled,
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
			t.Fatalf("FIX VIOLATION: publisher.Publish was called for a native-publishAt Phase-1 row (video_id=%q)", phase1VideoID)
			return nil, nil // unreachable; t.Fatalf already killed the test
		},
		validateChannelBindingFn: func(ctx context.Context, accessToken, expectedChannelID string) error {
			return nil
		},
		updateVideoPrivacyFn: func(ctx context.Context, accessToken, videoID, privacyStatus string, pa *time.Time, title, description string) error {
			t.Fatalf("FIX VIOLATION: UpdateVideoPrivacy (videos.update) was called for a native-publishAt row (video_id=%q); YouTube owns the transition and the ~50-unit call must be skipped", phase1VideoID)
			return nil // unreachable; t.Fatalf already killed the test
		},
	}
	vault := &mockCredentialVault{
		renewFn: func(ctx context.Context, accountID int64, tokenType string, refresh credentials.TokenRefresher) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "fresh-bearer"}, nil
		},
	}
	ytPubLookup := &mockYouTubeTargetPublicationLookup{
		findByPostTargetIDFn: func(ctx context.Context, postTargetID int64) (*models.YouTubeTargetPublication, error) {
			vid := phase1VideoID
			return &models.YouTubeTargetPublication{
				ID:                  phase1YTPubRowID,
				PostTargetID:        postTargetID,
				YouTubeUploadStatus: "youtube_uploaded",
				YouTubeVideoID:      &vid,
				// Migration 126: the Phase-1 videos.insert carried
				// status.publishAt → YouTube owns the transition.
				NativePublishAt: &publishAt,
			}, nil
		},
	}
	w := newTestWorker(posts, users, "youtube", svc, vault)
	w.SetYouTubeTargetPublicationStore(ytPubLookup)

	if err := w.publishTarget(context.Background(), scheduledTarget()); err != nil {
		t.Fatalf("publishTarget: %v", err)
	}

	// (1) Neither the fresh videos.insert nor the videos.update may fire.
	if svc.publishCalls != 0 {
		t.Errorf("publishCalls: want 0 (Phase-2 bypass must skip publisher.Publish), got %d", svc.publishCalls)
	}
	if svc.updateVideoPrivacyCalls != 0 {
		t.Errorf("updateVideoPrivacyCalls: want 0 (native publishAt must skip the ~50-unit videos.update), got %d", svc.updateVideoPrivacyCalls)
	}

	// (2) The target is stamped published with the composite id + timestamp.
	if len(posts.updateTargets) == 0 {
		t.Fatalf("UpdateStatus was never called; expected at least one call after publishTarget")
	}
	finalTarget := posts.updateTargets[len(posts.updateTargets)-1]
	if finalTarget.Status != models.PostStatusPublished {
		t.Errorf("final target.Status: want published, got %q", finalTarget.Status)
	}
	if finalTarget.PlatformPostID != "UCexpectedYtChan:"+phase1VideoID {
		t.Errorf("final target.PlatformPostID: want %q, got %q", "UCexpectedYtChan:"+phase1VideoID, finalTarget.PlatformPostID)
	}
	if finalTarget.PublishedAt == nil {
		t.Errorf("final target.PublishedAt: want non-nil, got nil")
	}

	// (3) The yt_pub row is stamped published exactly once.
	if ytPubLookup.markPublishedCalls != 1 {
		t.Errorf("markPublishedCalls: want 1, got %d", ytPubLookup.markPublishedCalls)
	}
	// (4) Control/recovery: the settlement VERIFIED the video's actual
	// privacy via videos.list before stamping (1 unit, general bucket)
	// instead of trusting the DB stamp blindly. The mock's default
	// GetYouTubeVideo returns "public", so the stamp is legitimate.
	if svc.getYouTubeVideoCalls != 1 {
		t.Errorf("getYouTubeVideoCalls: want 1 (native settlement must verify via videos.list), got %d", svc.getYouTubeVideoCalls)
	}
}

// TestPublishTarget_YouTube_NativePublishAt_StillPrivate_GraceRequeue
// covers the grace-window control path: the video is still private
// shortly after publish_at (YouTube is still processing its own
// transition) — the worker must NOT stamp published and must NOT burn
// a 50-unit videos.update; it requeues the target with backoff so the
// next tick re-verifies.
func TestPublishTarget_YouTube_NativePublishAt_StillPrivate_GraceRequeue(t *testing.T) {
	const phase1VideoID = "PHASE1_VID_GRACE"

	// publishAt 5 minutes ago — inside the 10-minute grace window.
	publishAt := time.Now().UTC().Add(-5 * time.Minute)

	posts := &mockPostStore{
		claimFn: func(id int64) (bool, error) { return true, nil },
		findByIDFn: func(id int64) (*models.Post, error) {
			return &models.Post{
				ID: 100, Caption: "yt-caption", Title: "yt-title",
				PrivacyLevel: "public", PublishAt: &publishAt, Status: models.PostStatusScheduled,
			}, nil
		},
	}
	users := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return &models.PlatformAccount{ID: 10, Platform: "youtube", PlatformUserID: "UCexpectedYtChan"}, nil
		},
	}
	svc := &mockProvider{
		baseMockProvider: baseMockProvider{platform: "youtube"},
		validateChannelBindingFn: func(ctx context.Context, accessToken, expectedChannelID string) error {
			return nil
		},
		updateVideoPrivacyFn: func(ctx context.Context, accessToken, videoID, privacyStatus string, pa *time.Time, title, description string) error {
			t.Fatalf("FIX VIOLATION: videos.update must NOT fire inside the grace window (video still private; YouTube is processing)")
			return nil
		},
		getYouTubeVideoFn: func(ctx context.Context, accessToken, videoID string) (*models.YouTubeVideoDetails, error) {
			return &models.YouTubeVideoDetails{ID: videoID, Privacy: "private"}, nil
		},
	}
	vault := &mockCredentialVault{
		renewFn: func(ctx context.Context, accountID int64, tokenType string, refresh credentials.TokenRefresher) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "fresh-bearer"}, nil
		},
	}
	ytPubLookup := &mockYouTubeTargetPublicationLookup{
		findByPostTargetIDFn: func(ctx context.Context, postTargetID int64) (*models.YouTubeTargetPublication, error) {
			vid := phase1VideoID
			return &models.YouTubeTargetPublication{
				ID: 8888, PostTargetID: postTargetID,
				YouTubeUploadStatus: "youtube_uploaded",
				YouTubeVideoID:      &vid,
				NativePublishAt:     &publishAt,
			}, nil
		},
	}
	w := newTestWorker(posts, users, "youtube", svc, vault)
	w.SetYouTubeTargetPublicationStore(ytPubLookup)

	if err := w.publishTarget(context.Background(), scheduledTarget()); err == nil {
		t.Fatal("publishTarget: want error (grace-window requeue), got nil")
	}
	// Not stamped published; no videos.update; verification ran once.
	if len(posts.updateTargets) == 0 || posts.updateTargets[len(posts.updateTargets)-1].Status == models.PostStatusPublished {
		t.Errorf("target must NOT be stamped published inside the grace window; last status=%v", posts.updateTargets)
	}
	if svc.updateVideoPrivacyCalls != 0 {
		t.Errorf("updateVideoPrivacyCalls: want 0 inside grace window, got %d", svc.updateVideoPrivacyCalls)
	}
	if svc.getYouTubeVideoCalls != 1 {
		t.Errorf("getYouTubeVideoCalls: want 1, got %d", svc.getYouTubeVideoCalls)
	}
}

// TestPublishTarget_YouTube_NativePublishAt_StillPrivate_PastGrace_Recovery
// covers the recovery path: the video is still private PAST the grace
// window (YouTube missed the scheduled transition) — the worker forces
// the transition via videos.update (one ~50-unit call, the exception
// path) and stamps published.
func TestPublishTarget_YouTube_NativePublishAt_StillPrivate_PastGrace_Recovery(t *testing.T) {
	const phase1VideoID = "PHASE1_VID_RECOVERY"

	// publishAt 30 minutes ago — well past the 10-minute grace window.
	publishAt := time.Now().UTC().Add(-30 * time.Minute)

	posts := &mockPostStore{
		claimFn: func(id int64) (bool, error) { return true, nil },
		findByIDFn: func(id int64) (*models.Post, error) {
			return &models.Post{
				ID: 100, Caption: "yt-caption", Title: "yt-title",
				PrivacyLevel: "public", PublishAt: &publishAt, Status: models.PostStatusScheduled,
			}, nil
		},
	}
	users := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return &models.PlatformAccount{ID: 10, Platform: "youtube", PlatformUserID: "UCexpectedYtChan"}, nil
		},
	}
	recoveryCalls := 0
	svc := &mockProvider{
		baseMockProvider: baseMockProvider{platform: "youtube"},
		validateChannelBindingFn: func(ctx context.Context, accessToken, expectedChannelID string) error {
			return nil
		},
		updateVideoPrivacyFn: func(ctx context.Context, accessToken, videoID, privacyStatus string, pa *time.Time, title, description string) error {
			recoveryCalls++
			if privacyStatus != "public" {
				t.Errorf("recovery videos.update privacyStatus: want public, got %q", privacyStatus)
			}
			if pa != nil {
				t.Errorf("recovery videos.update publishAt: want nil (force public now), got %v", pa)
			}
			return nil
		},
		getYouTubeVideoFn: func(ctx context.Context, accessToken, videoID string) (*models.YouTubeVideoDetails, error) {
			return &models.YouTubeVideoDetails{ID: videoID, Privacy: "private"}, nil
		},
	}
	vault := &mockCredentialVault{
		renewFn: func(ctx context.Context, accountID int64, tokenType string, refresh credentials.TokenRefresher) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "fresh-bearer"}, nil
		},
	}
	ytPubLookup := &mockYouTubeTargetPublicationLookup{
		findByPostTargetIDFn: func(ctx context.Context, postTargetID int64) (*models.YouTubeTargetPublication, error) {
			vid := phase1VideoID
			return &models.YouTubeTargetPublication{
				ID: 8888, PostTargetID: postTargetID,
				YouTubeUploadStatus: "youtube_uploaded",
				YouTubeVideoID:      &vid,
				NativePublishAt:     &publishAt,
			}, nil
		},
	}
	w := newTestWorker(posts, users, "youtube", svc, vault)
	w.SetYouTubeTargetPublicationStore(ytPubLookup)

	if err := w.publishTarget(context.Background(), scheduledTarget()); err != nil {
		t.Fatalf("publishTarget: %v", err)
	}
	if recoveryCalls != 1 {
		t.Errorf("recovery videos.update calls: want 1, got %d", recoveryCalls)
	}
	if len(posts.updateTargets) == 0 || posts.updateTargets[len(posts.updateTargets)-1].Status != models.PostStatusPublished {
		t.Errorf("target must be stamped published after recovery; last status=%v", posts.updateTargets)
	}
	if ytPubLookup.markPublishedCalls != 1 {
		t.Errorf("markPublishedCalls: want 1, got %d", ytPubLookup.markPublishedCalls)
	}
}

// TestPublishTarget_YouTube_NativePublishAt_VideoOrphaned_FallsThrough
// covers the orphan recovery for native-scheduled rows: videos.list
// returns 404 (user deleted the Phase-1 video / takedown) — the
// worker clears the stale yt_pub row (ClearYouTubeUpload) and falls
// through to a fresh publisher.Publish so the channel gets a video.
func TestPublishTarget_YouTube_NativePublishAt_VideoOrphaned_FallsThrough(t *testing.T) {
	const phase1VideoID = "PHASE1_VID_ORPHAN"

	publishAt := time.Now().UTC().Add(-30 * time.Minute)

	posts := &mockPostStore{
		claimFn: func(id int64) (bool, error) { return true, nil },
		findByIDFn: func(id int64) (*models.Post, error) {
			return &models.Post{
				ID: 100, Caption: "yt-caption", Title: "yt-title",
				PrivacyLevel: "public", PublishAt: &publishAt, Status: models.PostStatusScheduled,
			}, nil
		},
	}
	users := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return &models.PlatformAccount{ID: 10, Platform: "youtube", PlatformUserID: "UCexpectedYtChan"}, nil
		},
	}
	svc := &mockProvider{
		baseMockProvider: baseMockProvider{platform: "youtube"},
		validateChannelBindingFn: func(ctx context.Context, accessToken, expectedChannelID string) error {
			return nil
		},
		publishFn: func(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (*models.PublishResult, error) {
			return &models.PublishResult{PlatformMediaID: "fresh-video-id"}, nil
		},
		getYouTubeVideoFn: func(ctx context.Context, accessToken, videoID string) (*models.YouTubeVideoDetails, error) {
			return nil, fmt.Errorf("%w: video_id=%s", services.ErrYouTubeVideoNotFound, videoID)
		},
	}
	vault := &mockCredentialVault{
		renewFn: func(ctx context.Context, accountID int64, tokenType string, refresh credentials.TokenRefresher) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "fresh-bearer"}, nil
		},
	}
	ytPubLookup := &mockYouTubeTargetPublicationLookup{
		findByPostTargetIDFn: func(ctx context.Context, postTargetID int64) (*models.YouTubeTargetPublication, error) {
			vid := phase1VideoID
			return &models.YouTubeTargetPublication{
				ID: 8888, PostTargetID: postTargetID,
				YouTubeUploadStatus: "youtube_uploaded",
				YouTubeVideoID:      &vid,
				NativePublishAt:     &publishAt,
			}, nil
		},
	}
	w := newTestWorker(posts, users, "youtube", svc, vault)
	w.SetYouTubeTargetPublicationStore(ytPubLookup)

	if err := w.publishTarget(context.Background(), scheduledTarget()); err != nil {
		t.Fatalf("publishTarget: %v", err)
	}
	if ytPubLookup.clearYouTubeUploadCalls != 1 {
		t.Errorf("clearYouTubeUploadCalls: want 1 (orphan recovery must clear the stale yt_pub row), got %d", ytPubLookup.clearYouTubeUploadCalls)
	}
	if svc.publishCalls != 1 {
		t.Errorf("publishCalls: want 1 (fresh publisher.Publish after orphan recovery), got %d", svc.publishCalls)
	}
	if len(posts.updateTargets) == 0 || posts.updateTargets[len(posts.updateTargets)-1].Status != models.PostStatusPublished {
		t.Errorf("target must be stamped published after the fresh publish; last status=%v", posts.updateTargets)
	}
}
