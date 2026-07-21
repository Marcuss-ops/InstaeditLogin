package worker

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
	"github.com/Marcuss-ops/InstaeditLogin/pkg/metrics"
	"github.com/prometheus/client_golang/prometheus/testutil"
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

// TestPublishTarget_YouTube_ChannelMismatch_CallOrderMarksAccountBeforeTarget
// is the MED-1 sequencing guard: when the YouTube channel binding
// check fails with ErrYouTubeChannelMismatch, the worker MUST
// flip platform_account.status to 'reauth_required' (via
// MarkReauthRequired) BEFORE it flips post_target.status to
// 'blocked_auth' (via markPublishBlockedAuth → UpdateStatus).
// A regression that swaps the order would leave the operator
// dashboard with a half-finished reauth signal: the post_target
// drops out of the publish filter set (so the upload does abort),
// but the platform_account's reauth_required flag never lands in
// the DB (so the operator is never prompted to reconnect). The
// call-order tracker mirrors the existing
// TestPublishTarget_ClaimFiresBeforeFindByID pattern.
func TestPublishTarget_YouTube_ChannelMismatch_CallOrderMarksAccountBeforeTarget(t *testing.T) {
	var order []string
	posts := &mockPostStore{
		claimFn: func(id int64) (bool, error) { return true, nil },
		findByIDFn: func(id int64) (*models.Post, error) {
			order = append(order, "findByID")
			return &models.Post{ID: 100, Caption: "x", MediaURL: "https://cdn.example.com/v.mp4"}, nil
		},
	}
	users := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			order = append(order, "findAccount")
			return &models.PlatformAccount{
				ID:             11,
				Platform:       models.PlatformYouTube,
				PlatformUserID: "UCexpectedChanID",
			}, nil
		},
		markReauthRequiredFn: func(ctx context.Context, id int64, code, message string) error {
			order = append(order, "markReauth")
			return nil
		},
	}
	svc := &mockProvider{
		baseMockProvider: baseMockProvider{platform: "youtube"},
		publishFn: func(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (*models.PublishResult, error) {
			t.Error("Publish MUST NOT be reached on channel mismatch (guard short-circuits before idempotency stamp)")
			return nil, nil
		},
		validateChannelBindingFn: func(ctx context.Context, accessToken, expectedChannelID string) error {
			return fmt.Errorf("%w: %q is not in channels.list(mine=true) result",
				services.ErrYouTubeChannelMismatch, "UCactualChan")
		},
	}
	vault := &mockCredentialVault{
		renewFn: func(ctx context.Context, accountID int64, tokenType string, refresh credentials.TokenRefresher) (*models.OAuthToken, error) {
			order = append(order, "renew")
			return &models.OAuthToken{AccessToken: "t"}, nil
		},
	}
	// Capture UpdateStatus call ordering without conflicting with
	// the other tests' use of updateStatusFn.
	origUpdate := posts.updateStatusFn
	posts.updateStatusFn = func(t *models.PostTarget) error {
		order = append(order, "updateStatus")
		if origUpdate != nil {
			return origUpdate(t)
		}
		return nil
	}
	w := newTestWorker(posts, users, "youtube", svc, vault)

	if err := w.publishTarget(context.Background(), scheduledTarget()); err == nil {
		t.Fatal("publishTarget must return an error on channel binding mismatch")
	}

	// Sequence to assert: findByID → findAccount → renew →
	// markReauth → updateStatus (markPublishBlockedAuth). A
	// regression that fires updateStatus BEFORE markReauth would
	// fail this assertion; that ordering drift would leave the
	// operator's "needs reconnect" signal missing from the DB.
	want := []string{"findByID", "findAccount", "renew", "markReauth", "updateStatus"}
	if len(order) != len(want) {
		t.Fatalf("call order: want %v, got %v", want, order)
	}
	for i, step := range want {
		if order[i] != step {
			t.Errorf("step[%d]: want %q, got %q (full order: %v)", i, step, order[i], order)
		}
	}
	if posts.updateCalls != 1 {
		t.Errorf("UpdateStatus calls: want 1, got %d", posts.updateCalls)
	}
	if posts.updateTargets[0].Status != models.PostStatusBlockedAuth {
		t.Errorf("final status: want blocked_auth, got %q", posts.updateTargets[0].Status)
	}
	// MED-1.2 explicit count assertion: also locks the
	// markReauthRequiredCalls counter so a future regression
	// that calls MarkReauthRequired TWICE on the mismatch
	// branch (each appends "markReauth", so the sequence list
	// grows but the first occurrence is still before
	// updateStatus) trips this assertion independently of
	// the sequence check.
	if users.markReauthRequiredCalls != 1 {
		t.Errorf("MarkReauthRequired calls: want 1 (one per mismatch), got %d", users.markReauthRequiredCalls)
	}
}

// TestPublishTarget_YouTube_ChannelMismatch_FlagsReauthAndFailsTarget
// verifies the mismatch path: the binding check returns
// ErrYouTubeChannelMismatch (wrapped). The worker must:
//   - call MarkReauthRequired with code="youtube_channel_mismatch"
//     so the operator's dashboard prompts a reconnect.
//   - NOT publish (upload on the wrong channel is the bug we are
//     guarding against).
//   - transition the post_target to 'failed' with a descriptive
//     ErrorMessage so the operator sees WHY the upload was refused.
func TestPublishTarget_YouTube_ChannelMismatch_FlagsReauthAndFailsTarget(t *testing.T) {
	posts := &mockPostStore{
		claimFn: func(id int64) (bool, error) { return true, nil },
		findByIDFn: func(id int64) (*models.Post, error) {
			return &models.Post{
				ID:       200,
				Caption:  "mismatched yt caption",
				Title:    "yt-title",
				MediaURL: "https://cdn.example.com/y.mp4",
			}, nil
		},
	}
	var markCode, markMsg string
	var markID int64
	users := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return &models.PlatformAccount{
				ID:             11,
				Platform:       "youtube",
				PlatformUserID: "UCexpectedChan",
			}, nil
		},
		markReauthRequiredFn: func(ctx context.Context, id int64, code, message string) error {
			markID = id
			markCode = code
			markMsg = message
			return nil
		},
	}
	svc := &mockProvider{
		baseMockProvider: baseMockProvider{platform: "youtube"},
		publishFn: func(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (*models.PublishResult, error) {
			// P0#3: Publish MUST NOT be reached on channel mismatch.
			// Reaching Publish would silently upload to the wrong
			// channel — exactly the bug this guard is preventing.
			t.Error("Publish called despite channel mismatch (this is the silent-wrong-channel bug we are guarding against)")
			return nil, errors.New("unreachable")
		},
		// Mismatch: grant lists UCwrongChan, expected UCexpectedChan.
		validateChannelBindingFn: func(ctx context.Context, accessToken, expectedChannelID string) error {
			return fmt.Errorf("%w: expected %q, grant bound to %q",
				services.ErrYouTubeChannelMismatch, expectedChannelID, "UCwrongChan")
		},
	}
	vault := &mockCredentialVault{
		renewFn: func(ctx context.Context, accountID int64, tokenType string, refresh credentials.TokenRefresher) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "any-bearer"}, nil
		},
	}
	w := newTestWorker(posts, users, "youtube", svc, vault)

	err := w.publishTarget(context.Background(), scheduledTarget())
	if err == nil {
		t.Fatal("expected error on channel mismatch, got nil")
	}

	// 1. The binding check ran exactly once.
	if svc.validateChannelBindingCalls != 1 {
		t.Errorf("ValidateChannelBinding calls: want 1, got %d", svc.validateChannelBindingCalls)
	}
	// 2. Publish was NOT called (CRITICAL — no silent wrong-channel upload).
	if svc.publishCalls != 0 {
		t.Fatalf("Publish calls on mismatch: want 0, got %d (wrong-channel upload)", svc.publishCalls)
	}
	// 3. MarkReauthRequired was called with the right code + message.
	if users.markReauthRequiredCalls != 1 {
		t.Errorf("MarkReauthRequired calls: want 1 (flag platform_account for reauth), got %d", users.markReauthRequiredCalls)
	}
	if markID != 11 {
		t.Errorf("MarkReauthRequired account id: want 11, got %d", markID)
	}
	if markCode != "youtube_channel_mismatch" {
		t.Errorf("MarkReauthRequired code: want youtube_channel_mismatch, got %q", markCode)
	}
	if !strings.Contains(markMsg, "UCexpectedChan") {
		t.Errorf("MarkReauthRequired message should include expected channel id, got %q", markMsg)
	}
	if !strings.Contains(markMsg, "UCwrongChan") {
		t.Errorf("MarkReauthRequired message should include actual channel id (operator visibility), got %q", markMsg)
	}
	// 4. Post_target transitioned to blocked_auth with a descriptive
	//    message AND a stable LastErrorCode so dashboards can index
	//    on the code without parsing ErrorMessage prose. Task 2/10:
	//    blocked_auth is the dedicated post_target status for
	//    channel-drift refusals, distinct from 'failed' (a generic
	//    per-attempt failure). The worker does NOT auto-retry
	//    blocked_auth rows; the dashboard "reconnect channel" CTA
	//    drives the recovery.
	if posts.updateCalls != 1 {
		t.Fatalf("UpdateStatus calls: want 1, got %d", posts.updateCalls)
	}
	if posts.updateTargets[0].Status != models.PostStatusBlockedAuth {
		t.Errorf("final status: want blocked_auth (Task 2/10 channel-drift terminal), got %q", posts.updateTargets[0].Status)
	}
	if posts.updateTargets[0].LastErrorCode != "blocked_auth" {
		t.Errorf("LastErrorCode: want \"blocked_auth\" (operator-dashboard filter), got %q", posts.updateTargets[0].LastErrorCode)
	}
	if !strings.Contains(posts.updateTargets[0].ErrorMessage, "youtube channel binding") {
		t.Errorf("ErrorMessage should mention the binding check, got %q", posts.updateTargets[0].ErrorMessage)
	}
	// 5. The idempotency key stamp happened BEFORE the mismatch check
	//    would have produced an error... actually no: the stamp comes
	//    AFTER the binding check in our placement. So on mismatch we
	//    expect setKeyCalls==0 (no key stamped on a failed publish).
	if posts.setKeyCalls != 0 {
		t.Errorf("SetProviderIdempotencyKey calls on mismatch: want 0 (no key stamped for blocked-auth refused publishes), got %d", posts.setKeyCalls)
	}
	_ = err
}

// TestPublishTarget_YouTube_ChannelCheck_Transient_FailsTargetWithoutFlaggingReauth
// verifies the transient-error path: the binding check returns a
// plain error (NOT wrapping ErrYouTubeChannelMismatch). The worker
// MUST treat this as transient:
//   - DO NOT flag reauth_required (network blips / 5xx do not mean
//     the grant is dead; flagging would lock the operator out).
//   - DO transition the target to 'failed' so the row drops out of
//     the tick filter (impossible to retry on the same tick without
//     claim re-acquisition across processes — i.e. on the next
//     process restart / tick).
//
// In production, transient failures would be handled with
// decorator-jitter backoff (Taglio ~ future). Today the tick's
// per-target error counter increments and the error is logged; the
// next scheduler pass CAN retry if the platform_account was not
// flagged (which is what we want for transient cases).
func TestPublishTarget_YouTube_ChannelCheck_Transient_FailsTargetWithoutFlaggingReauth(t *testing.T) {
	posts := &mockPostStore{
		claimFn: func(id int64) (bool, error) { return true, nil },
		findByIDFn: func(id int64) (*models.Post, error) {
			return &models.Post{ID: 300, Caption: "x", MediaURL: "https://cdn.example.com/v.mp4"}, nil
		},
	}
	users := &mockUserStore{
		findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
			return &models.PlatformAccount{
				ID:             12,
				Platform:       "youtube",
				PlatformUserID: "UCtransientChan",
			}, nil
		},
		// CRITICAL: the worker's transient branch must NOT call this
		// function. We configure a marker to detect any false
		// positive: if the worker calls MarkReauthRequired, the
		// platform_account will be wrongly flagged as dead.
		markReauthRequiredFn: func(ctx context.Context, id int64, code, message string) error {
			t.Errorf("MarkReauthRequired MUST NOT be called on transient binding-check failure (would lock the operator out); got id=%d code=%q", id, code)
			return nil
		},
	}
	svc := &mockProvider{
		baseMockProvider: baseMockProvider{platform: "youtube"},
		publishFn: func(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (*models.PublishResult, error) {
			t.Error("Publish called despite transient binding-check failure")
			return nil, errors.New("unreachable")
		},
		// Transient: 5xx from channels.list. PLAIN error — does NOT
		// wrap ErrYouTubeChannelMismatch. The worker must detect via
		// errors.Is(err, ErrYouTubeChannelMismatch) == false.
		validateChannelBindingFn: func(ctx context.Context, accessToken, expectedChannelID string) error {
			return errors.New("youtube channel binding: channels.list returned 503: service unavailable")
		},
	}
	vault := &mockCredentialVault{
		renewFn: func(ctx context.Context, accountID int64, tokenType string, refresh credentials.TokenRefresher) (*models.OAuthToken, error) {
			return &models.OAuthToken{AccessToken: "any"}, nil
		},
	}
	w := newTestWorker(posts, users, "youtube", svc, vault)

	err := w.publishTarget(context.Background(), scheduledTarget())
	if err == nil {
		t.Fatal("expected transient error to propagate so the tick counter increments, got nil")
	}
	// The error must NOT wrap ErrYouTubeChannelMismatch (otherwise the
	// worker's errors.Is would route it to the reauth branch).
	if errors.Is(err, services.ErrYouTubeChannelMismatch) {
		t.Errorf("transient error must NOT wrap ErrYouTubeChannelMismatch (would misroute to reauth branch), got %v", err)
	}
	// Validate the structure (counter & status).
	if svc.validateChannelBindingCalls != 1 {
		t.Errorf("ValidateChannelBinding calls: want 1, got %d", svc.validateChannelBindingCalls)
	}
	if svc.publishCalls != 0 {
		t.Errorf("Publish calls on transient: want 0, got %d", svc.publishCalls)
	}
	// markReauthRequiredFn is configured to fail via t.Errorf on any
	// call. The counter assertion is therefore unnecessary — the
	// configured fn already aborts the test if MarkReauthRequired
	// was reached.
	if posts.updateCalls != 1 {
		t.Fatalf("UpdateStatus calls: want 1 (mark target failed), got %d", posts.updateCalls)
	}
	if posts.updateTargets[0].Status != models.PostStatusFailed {
		t.Errorf("final status: want failed, got %q", posts.updateTargets[0].Status)
	}
	if !strings.Contains(posts.updateTargets[0].ErrorMessage, "channel binding") {
		t.Errorf("ErrorMessage should mention the binding check, got %q", posts.updateTargets[0].ErrorMessage)
	}
	_ = err
}

// TestPublishTarget_YouTube_ChannelBindingMismatch_IncrementsMetric (P0 #2)
// is the table-driven coverage of the
// youtube_publish_channel_mismatch_total counter. The metric MUST
// increment ONLY on the ErrYouTubeChannelMismatch branch (which
// ALSO calls MarkReauthRequired); the match branch and the
// transient 5xx branch MUST NOT increment because no reauth flag
// is written on those paths (drift up = Google silently re-bound
// the OAuth grant to a different Brand Account).
//
// Delta-based assertion (read before + read after) instead of
// Reset() so other parallel-sibling metric tests that share the
// global CounterVec don't get wiped between cases. The
// (provider="youtube") label is the only series this test reads;
// sibling tests use other labels.
func TestPublishTarget_YouTube_ChannelBindingMismatch_IncrementsMetric(t *testing.T) {
	cases := []struct {
		name                string
		bindResultErr       error
		wantMetricDelta     float64
		wantMarkReauthCalls int
	}{
		{
			name:                "match_does_not_increment",
			bindResultErr:       nil,
			wantMetricDelta:     0,
			wantMarkReauthCalls: 0,
		},
		{
			name: "mismatch_increments_by_one",
			bindResultErr: fmt.Errorf("%w: %q is not in channels.list(mine=true) result",
				services.ErrYouTubeChannelMismatch, "UCexpectedChanID"),
			wantMetricDelta:     1,
			wantMarkReauthCalls: 1,
		},
		{
			name: "transient_does_not_increment",
			// 503 from channels.list — MISMATCH PATH MUST NOT FIRE
			// because this is wrapped plainly (no ErrYouTubeChannelMismatch
			// in the chain). Mirrors the existing
			// TestPublishTarget_YouTube_ChannelCheck_Transient_ path.
			bindResultErr:       errors.New("youtube channel binding: channels.list returned 503: upstream"),
			wantMetricDelta:     0,
			wantMarkReauthCalls: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := testutil.ToFloat64(metrics.YouTubePublishChannelMismatch.WithLabelValues("youtube"))

			posts := &mockPostStore{
				claimFn: func(id int64) (bool, error) { return true, nil },
				findByIDFn: func(id int64) (*models.Post, error) {
					return &models.Post{ID: 100, Caption: "x", MediaURL: "https://cdn.example.com/v.mp4"}, nil
				},
			}
			users := &mockUserStore{
				findPlatformAccountFn: func(id int64) (*models.PlatformAccount, error) {
					return &models.PlatformAccount{
						ID:             11,
						Platform:       models.PlatformYouTube,
						PlatformUserID: "UCexpectedChanID",
					}, nil
				},
				markReauthRequiredFn: func(ctx context.Context, id int64, code, message string) error {
					if tc.wantMarkReauthCalls == 0 {
						t.Errorf("MarkReauthRequired MUST NOT be called when bindResultErr is non-mismatch (%v)", tc.bindResultErr)
					}
					return nil
				},
			}
			svc := &mockProvider{
				baseMockProvider: baseMockProvider{platform: "youtube"},
				publishFn: func(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (*models.PublishResult, error) {
					// For match sub-case Publish is expected to be
					// called. For mismatch/transient the
					// channel-binding branch short-circuits BEFORE
					// Publish and the existing tests
					// (TestPublishTarget_YouTube_ChannelMismatch_FlagsReauthAndFailsTarget_
					// + TestPublishTarget_YouTube_ChannelCheck_Transient_)
					// assert that. Returning a non-nil result here
					// keeps the sync publishing path (which reads
					// result.PlatformMediaID) free of nil-deref in
					// the match happy path.
					return &models.PublishResult{PlatformMediaID: "yt-test-media"}, nil
				},
				validateChannelBindingFn: func(ctx context.Context, accessToken, expectedChannelID string) error {
					return tc.bindResultErr
				},
			}
			vault := &mockCredentialVault{
				renewFn: func(ctx context.Context, accountID int64, tokenType string, refresh credentials.TokenRefresher) (*models.OAuthToken, error) {
					return &models.OAuthToken{AccessToken: "t"}, nil
				},
			}
			w := newTestWorker(posts, users, "youtube", svc, vault)

			_ = w.publishTarget(context.Background(), scheduledTarget())

			after := testutil.ToFloat64(metrics.YouTubePublishChannelMismatch.WithLabelValues("youtube"))
			delta := after - before
			if delta != tc.wantMetricDelta {
				t.Errorf("youtube_publish_channel_mismatch_total{youtube} delta: want %v, got %v", tc.wantMetricDelta, delta)
			}
			if users.markReauthRequiredCalls != tc.wantMarkReauthCalls {
				t.Errorf("MarkReauthRequired calls: want %d, got %d (must match metric increment: the two fire together)", tc.wantMarkReauthCalls, users.markReauthRequiredCalls)
			}
		})
	}
}
