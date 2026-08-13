package worker

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// TestPublishTarget_YouTube_ReusesPhase1VideoID_VideosUpdate validates
// the Phase-2 publish-bypass fix (Blocco #1 followup, P1 Migration 077).
//
// CONCRETE FIX:
//  1. PublishWorker.publishTarget now queries
//     youtube_target_publications.youtube_video_id for the target
//     BEFORE dispatching publisher.Publish.
//  2. When the row exists with youtube_upload_status="youtube_uploaded"
//     AND a non-empty youtube_video_id, the worker routes through
//     services.YouTubePrivacyUpdater.UpdateVideoPrivacy
//     (videos.update) instead of publisher.Publish (videos.insert).
//  3. publisher.Publish is NEVER called on the reuse path. The
//     YouTube quota charge drops from 1600 + chunking units (videos.insert)
//     to ~50 (videos.update metadata-only call).
//  4. The post_target ends with PlatformPostID == phase1VideoID
//     (NOT a brand-new videos.insert's id) and PublishedAt set.
//
// ─────────── BEFORE THE FIX (documented for posterity) ───────────
//
// Two-phase YouTube publishing flow:
//
//	PHASE 1 (upload_worker.processPublishJob::uploadVideoAsPrivateForTarget)
//	  - Calls services.YouTubeOAuthService.UploadVideoAsPrivate
//	    (videos.insert, privacy HARDCODED to "private")
//	  - On success stamps the youtube_target_publications row via
//	    YouTubeTargetPublicationRepository.MarkYouTubeUploaded.
//	PHASE 2 (publish_worker.publishTarget) — PRE-FIX behaviour:
//	  - Called publisher.Publish UNCONDITIONALLY for YouTube; produced
//	    a fresh full-resumable videos.insert. Orphaned the Phase-1
//	    private video. The post_target ended up with PlatformPostID =
//	    <PHASE2_VID> instead of <PHASE1_VID>. Dashboard "Published
//	    Video" link pointed at the wrong upload. YouTube quota was
//	    double-charged.
//
// ─────────── WHAT THE FIX-PATH TEST ASSERTS ───────────
//
//  1. publisher.Publish was NOT called for the reused Phase-1 video.
//     (Phase 2 took the videos.update path, not the videos.insert path.)
//  2. services.YouTubePrivacyUpdater.UpdateVideoPrivacy WAS called
//     exactly once with:
//     - videoID == phase1VideoID           (the YouTube video id Phase 1 stamped)
//     - privacyStatus == "public"         (post.PrivacyLevel cascade result)
//     - publishAt == post.PublishAt        (schedule cursor passed verbatim)
//     - title/description == post fields   (snippet reuse)
//     - accessToken == "fresh-bearer"      (post-vault.Renew, not stale)
//  3. The post_target transitioned to status=published with
//     PlatformPostID == phase1VideoID and PublishedAt != nil
//     (matches the SYNC-PUBLISH branch's full target stamp shape).
//  4. The youtube_target_publications row had MarkPublished called
//     exactly once (stamp published_at on the row).
//
// ─────────── SCOPE (YOUTUBE-ONLY) ───────────
//
// This test exercises the YouTube publish path specifically. A future
// bypass on Instagram/Twitter/TikTok/etc. would NOT trip this test
// (those platforms don't have an upload-worker → publish-worker
// deduplication gap because their Phase-1 path doesn't stamp a
// per-target public-id). Add a sibling test per-platform if a similar
// bypass exists for them.
func TestPublishTarget_YouTube_ReusesPhase1VideoID_VideosUpdate(t *testing.T) {
	const (
		phase1VideoID = "PHASE1_VID" // stamped by upload_worker.MarkYouTubeUploaded
		// phase1YTPubRowID is the PK the rows-returned-by-FindByPostTargetID
		// lookup carries — MarkPublished stamps published_at on this id.
		phase1YTPubRowID int64 = 7777
	)

	// Keep the schedule in the future regardless of when the suite runs.
	// A fixed historical date made this test fail after 12:00 UTC on the
	// fixture date, even though the production coercion was correct.
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
		// publishFn panics if invoked: the fix MUST skip
		// publisher.Publish for the reuse path. If the worker hits
		// this, the bypass is broken.
		publishFn: func(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (*models.PublishResult, error) {
			t.Fatalf("FIX VIOLATION: publisher.Publish was called by PublishWorker.publishTarget for a post_target whose Phase-1 youtube_target_publications row exists (video_id=%q). The Phase-2 bypass is broken; videos.insert overran the Phase-1 videos.insert and is re-charging YouTube quota.", phase1VideoID)
			return nil, nil // unreachable; t.Fatalf already killed the test
		},
		// Channel binding check returns nil so the worker proceeds
		// past the P0#3 guard and reaches the bypass block.
		validateChannelBindingFn: func(ctx context.Context, accessToken, expectedChannelID string) error {
			return nil
		},
		// UpdateVideoPrivacy returns nil — the publish-side bypass
		// mark-completion is delegated to the row-stamp (post
		// UpdateStatus). The bypass test's real assertion is
		// updateVideoPrivacyCalls == 1 (this fn was invoked).
		updateVideoPrivacyFn: func(ctx context.Context, accessToken, videoID, privacyStatus string, pa *time.Time, title, description string) error {
			return nil
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
			}, nil
		},
	}
	w := newTestWorker(posts, users, "youtube", svc, vault)
	w.SetYouTubeTargetPublicationStore(ytPubLookup)

	t.Logf("PHASE 1 PRECONDITION: youtube_target_publications row stamped with youtube_video_id=%q + youtube_upload_status=\"youtube_uploaded\" (post_target_id=200, yt_pub_id=%d)", phase1VideoID, phase1YTPubRowID)
	t.Logf("PHASE 2 TRIGGER:      PublishWorker.publishTarget should route through YouTubePrivacyUpdater.UpdateVideoPrivacy, NOT publisher.Publish")

	if err := w.publishTarget(context.Background(), scheduledTarget()); err != nil {
		t.Fatalf("publishTarget: %v", err)
	}

	// ─────────── Fix-path assertions ───────────

	// (1) publisher.Publish MUST NOT have been called (the bypass
	// replaced it with UpdateVideoPrivacy). t.Fatalf inside publishFn
	// would have killed the test already if this assertion is
	// violated — the counter check is defensive depth.
	if svc.publishCalls != 0 {
		t.Errorf("publishCalls: want 0 (Phase-2 bypass must skip publisher.Publish when Phase-1 stamped youtube_video_id exists), got %d", svc.publishCalls)
	}

	// (2) UpdateVideoPrivacy MUST have been called exactly once.
	if svc.updateVideoPrivacyCalls != 1 {
		t.Errorf("updateVideoPrivacyCalls: want 1 (Phase-2 bypass must call YouTubePrivacyUpdater.UpdateVideoPrivacy across the Phase-1 stamped video_id), got %d", svc.updateVideoPrivacyCalls)
	}

	// (3) UpdateVideoPrivacy inputs MUST match the Phase-1 stamped
	// video_id, the cascade-resolved privacy, and the post.PublishAt
	// cursor. This is the precise "did we route through the right
	// code path with the right parameters" assertion.
	if svc.capturedUpdatePrivacyVID != phase1VideoID {
		t.Errorf("UpdateVideoPrivacy videoID: want %q (Phase-1 stamped), got %q", phase1VideoID, svc.capturedUpdatePrivacyVID)
	}
	// After Commit #2 (Finding #2 videos.update coercion): for a
	// future publishAt + privacy=public input, the publish_worker bypass
	// + UpdateVideoPrivacy both pass through services.CoercePrivacyForUpdate
	// which flips privacy to "private" (YouTube's API requires
	// privacyStatus=private alongside publishAt for scheduled
	// transitions). The captured mock value reflects the post-coerce
	// arg passed INTO UpdateVideoPrivacy — so "private" (NOT the
	// pre-coerce "public") is correct here.
	if svc.capturedUpdatePrivacyStatus != "private" {
		t.Errorf("UpdateVideoPrivacy privacyStatus: want %q (CoercePrivacyForUpdate: future+public → \"private\"), got %q", "private", svc.capturedUpdatePrivacyStatus)
	}
	if svc.capturedUpdatePrivacyTitle != "yt-title" {
		t.Errorf("UpdateVideoPrivacy title: want %q, got %q", "yt-title", svc.capturedUpdatePrivacyTitle)
	}
	if svc.capturedUpdatePrivacyDescription != "yt-caption" {
		t.Errorf("UpdateVideoPrivacy description: want %q, got %q", "yt-caption", svc.capturedUpdatePrivacyDescription)
	}
	if svc.capturedUpdatePrivacyPublishAt == nil || !svc.capturedUpdatePrivacyPublishAt.Equal(publishAt) {
		t.Errorf("UpdateVideoPrivacy publishAt: want %v, got %v", publishAt, svc.capturedUpdatePrivacyPublishAt)
	}
	if svc.capturedUpdatePrivacyAccessToken != "fresh-bearer" {
		t.Errorf("UpdateVideoPrivacy accessToken: want %q (post-vault.Renew), got %q", "fresh-bearer", svc.capturedUpdatePrivacyAccessToken)
	}

	// (4) The post_target transitioned to status='published' with
	// PlatformPostID == phase1VideoID AND PublishedAt != nil. This
	// mirrors the SYNC-PUBLISH branch's full stamp shape so the
	// dashboard "Published Video" view renders correctly.
	if len(posts.updateTargets) == 0 {
		t.Fatalf("UpdateStatus was never called; expected at least one call after publishTarget")
	}
	finalTarget := posts.updateTargets[len(posts.updateTargets)-1]
	if finalTarget.Status != models.PostStatusPublished {
		t.Errorf("final target.Status: want published, got %q", finalTarget.Status)
	}
	if finalTarget.PlatformPostID != "UCexpectedYtChan:PHASE1_VID" {
		t.Errorf("final target.PlatformPostID: want %q (composite channelID:videoID shape — Finding #1 blocker fix), got %q", "UCexpectedYtChan:PHASE1_VID", finalTarget.PlatformPostID)
	}
	if finalTarget.PublishedAt == nil {
		t.Errorf("final target.PublishedAt: want non-nil (must mirror the SYNC-PUBLISH branch's full stamp shape), got nil")
	}

	// (5) The youtube_target_publications row's MarkPublished was
	// called exactly once — stamps published_at on the row that
	// the Phase-2 bypass consumed.
	if ytPubLookup.markPublishedCalls != 1 {
		t.Errorf("ytPubLookup.markPublishedCalls: want 1, got %d", ytPubLookup.markPublishedCalls)
	}
	if ytPubLookup.lastMarkPublishedID != phase1YTPubRowID {
		t.Errorf("ytPubLookup.lastMarkPublishedID: want %d (Phase-1 yt-pub row id), got %d", phase1YTPubRowID, ytPubLookup.lastMarkPublishedID)
	}
}

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
		phase1VideoID  = "PHASE1_VID_NATIVE"
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
}

// TestPublishTarget_YouTube_NoPhase1Row_FallsThroughToPublish captures
// the FALL-THROUGH behaviour required for the bypass to be safe: when
// no youtube_target_publications row exists for the target (Phase 1
// has NOT run / FAILED / not yet stamped), the worker MUST continue
// to call publisher.Publish so a fresh videos.insert lands on the
// channel. Without this test, a future refactor that flips the
// default to "always UpdateVideoPrivacy" could regress to a state
// where pre-Phase-1 posts never get publicly uploaded at all.
//
// SCOPE: same YouTube channel-binding precondition; ytPubLookup
// returns (nil, nil) (the contract for the worker's FindByPostTargetID
// shape — "no row" is NOT an error).
func TestPublishTarget_YouTube_NoPhase1Row_FallsThroughToPublish(t *testing.T) {
	posts := &mockPostStore{
		claimFn: func(id int64) (bool, error) { return true, nil },
		findByIDFn: func(id int64) (*models.Post, error) {
			return &models.Post{
				ID:           100,
				Caption:      "yt-caption",
				Title:        "yt-title",
				MediaURL:     "https://cdn.example.com/yt-video.mp4",
				PrivacyLevel: "public",
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
			return &models.PublishResult{
				PlatformMediaID: "FRESH_VID_NO_PHASE1_ROW",
				PlatformURL:     "https://www.youtube.com/watch?v=FRESH_VID_NO_PHASE1_ROW",
			}, nil
		},
		validateChannelBindingFn: func(ctx context.Context, accessToken, expectedChannelID string) error {
			return nil
		},
	}
	vault := &mockCredentialVault{
		renewFn: func(ctx context.Context, accountID int64, tokenType string, refresh credentials.TokenRefresher) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "fresh-bearer"}, nil
		},
	}
	ytPubLookup := &mockYouTubeTargetPublicationLookup{
		// Phase 1 has not stamped anything yet — the upload worker
		// either hasn't run, FAILED, or was never scheduled for this
		// post_target. The worker MUST fall through to publisher.Publish.
		findByPostTargetIDFn: func(ctx context.Context, postTargetID int64) (*models.YouTubeTargetPublication, error) {
			return nil, nil
		},
	}
	w := newTestWorker(posts, users, "youtube", svc, vault)
	w.SetYouTubeTargetPublicationStore(ytPubLookup)

	if err := w.publishTarget(context.Background(), scheduledTarget()); err != nil {
		t.Fatalf("publishTarget: %v", err)
	}

	if svc.publishCalls != 1 {
		t.Errorf("publishCalls: want 1 (no Phase-1 row exists so worker MUST publish normally), got %d", svc.publishCalls)
	}
	if svc.updateVideoPrivacyCalls != 0 {
		t.Errorf("updateVideoPrivacyCalls: want 0 (no Phase-1 row to reuse), got %d", svc.updateVideoPrivacyCalls)
	}
	if len(posts.updateTargets) == 0 {
		t.Fatalf("UpdateStatus was never called")
	}
	if posts.updateTargets[len(posts.updateTargets)-1].PlatformPostID != "FRESH_VID_NO_PHASE1_ROW" {
		t.Errorf("final target.PlatformPostID: want %q (publisher mock return), got %q",
			"FRESH_VID_NO_PHASE1_ROW",
			posts.updateTargets[len(posts.updateTargets)-1].PlatformPostID)
	}
}

// TestIsOrphanedYouTubeVideo covers the publish-worker's Phase-1 orphan-
// video classifier (Blocco #1 followup — Finding #4). The classifier
// decides whether UpdateVideoPrivacy returned a 404 referencing OUR
// yt_pub row's youtube_video_id, signalling that the Phase-1 orphan
// was deleted out from under us (user manual delete via YouTube
// Studio, moderator takedown, etc.) and that the worker should
// synchronously fall through to publisher.Publish after clearing
// the stale yt_pub row.
//
// Primary signal: typed sentinel errors.Is on
// services.ErrYouTubeVideoNotFound. Defense-in-depth substring fallback:
// when the err message contains BOTH the offending videoID AND a
// "not found" marker, fire the recovery branch anyway. Covers any
// future code path not yet re-wired to wrap with the typed sentinel.
//
// Scenarios verified:
//   - nil error                       → false (defensive nil-check)
//   - typed sentinel wrapped error     → true (canonical orphan path)
//   - non-sentinel error, empty videoID → false (substring fallback disabled when no videoID)
//   - non-sentinel error, missing videoID in msg → false (no false-positive orphan classification)
//   - non-sentinel error, msg contains both videoID + "not found" → true (substring fallback fires)
func TestIsOrphanedYouTubeVideo(t *testing.T) {
	const orphanVideoID = "dQw4w9WgXcQ"

	tests := []struct {
		name    string
		err     error
		videoID string
		want    bool
	}{
		{
			name:    "nil error returns false (defensive nil-check)",
			err:     nil,
			videoID: orphanVideoID,
			want:    false,
		},
		{
			name:    "nil error with empty videoID returns false",
			err:     nil,
			videoID: "",
			want:    false,
		},
		{
			name:    "typed sentinel-wrapped error returns true (canonical orphan path)",
			err:     fmt.Errorf("youtube update video: video not found (status 404): video_id=%s: %w", orphanVideoID, services.ErrYouTubeVideoNotFound),
			videoID: orphanVideoID,
			want:    true,
		},
		{
			name:    "typed sentinel-wrapped error returns true even with empty videoID (sentinel is authoritative)",
			err:     fmt.Errorf("orphan: %w", services.ErrYouTubeVideoNotFound),
			videoID: "",
			want:    true,
		},
		{
			name:    "non-sentinel error with empty videoID returns false (substring fallback disabled)",
			err:     errors.New("youtube update video: video not found (status 404)"),
			videoID: "",
			want:    false,
		},
		{
			name:    "non-sentinel error with videoID but no 'not found' marker returns false",
			err:     errors.New("youtube update video: internal server error (status 500) for video_id=" + orphanVideoID),
			videoID: orphanVideoID,
			want:    false,
		},
		{
			name:    "non-sentinel error with 'not found' but no matching videoID returns false",
			err:     errors.New("youtube update video: video not found (status 404) for video_id=other123abc"),
			videoID: orphanVideoID,
			want:    false,
		},
		{
			name:    "non-sentinel error matching both videoID and 'not found' returns true (substring fallback)",
			err:     errors.New("youtube update video: video not found (status 404) for video_id=" + orphanVideoID),
			videoID: orphanVideoID,
			want:    true,
		},
		{
			name:    "non-sentinel error with case-insensitive 'NOT FOUND' marker still matches (defense-in-depth)",
			err:     errors.New("youtube update video: video NOT FOUND (status 404) for video_id=" + orphanVideoID),
			videoID: orphanVideoID,
			want:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isOrphanedYouTubeVideo(tt.err, tt.videoID)
			if got != tt.want {
				t.Errorf("isOrphanedYouTubeVideo(%q, %q) = %v, want %v", errString(tt.err), tt.videoID, got, tt.want)
			}
		})
	}
}

// errString is a tiny helper for test output readability — returns
// "<nil>" when err is nil so the message renders cleanly. Avoids
// pulling in a third-party dep just for nil-error formatting.
func errString(err error) string {
	if err == nil {
		return "<nil>"
	}
	// Truncate very long error messages so test names stay
	// readable; the substring-fallback test cases use msg strings
	// ~70 chars long, so a 60-char cap keeps output balanced.
	msg := err.Error()
	if len(msg) > 60 {
		msg = strings.TrimRight(msg[:57], " ") + "..."
	}
	return msg
}
