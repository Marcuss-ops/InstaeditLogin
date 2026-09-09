package worker

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
	"github.com/Marcuss-ops/InstaeditLogin/pkg/metrics"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// TestPublishTarget_YouTube_RevokedGrant_InvalidGrantSentinel_MarksReauthAndBlocksTarget
// is the P1 end-to-end chain at the worker layer: Google revoked the
// refresh token, so the real YouTube service surfaces the typed
// credentials.ErrInvalidGrant sentinel (wrapped by postTokenRequest),
// RenewYouTubeToken maps it to ErrYouTubeInvalidGrant, and the worker
// must:
//  1. mark the platform_account reauth_required via markYouTubeGrantReauth
//     (code="invalid_grant" → the dashboard "Ricollega YouTube" CTA);
//  2. transition the post_target to blocked_auth (NOT failed) so the row
//     drops out of the tick filter with the operator-dashboard code;
//  3. never reach Publish;
//  4. attempt ONLY the canonical bearer refresh (no long_lived fallback
//     for invalid_grant).
func TestPublishTarget_YouTube_RevokedGrant_InvalidGrantSentinel_MarksReauthAndBlocksTarget(t *testing.T) {
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
				ID:             10,
				Platform:       models.PlatformYouTube,
				PlatformUserID: "UCrevokedChan",
				Status:         models.AccountStatusActive,
			}, nil
		},
		markReauthRequiredFn: func(ctx context.Context, id int64, code, message string) error {
			order = append(order, "markReauth")
			return nil
		},
	}
	svc := &mockProvider{
		baseMockProvider: baseMockProvider{platform: models.PlatformYouTube},
		publishFn: func(ctx context.Context, accessToken, platformUserID string, payload models.PublishPayload) (*models.PublishResult, error) {
			t.Error("Publish MUST NOT be reached when the refresh grant is revoked")
			return nil, nil
		},
	}
	var renewTypes []string
	vault := &mockCredentialVault{
		renewFn: func(ctx context.Context, accountID int64, tokenType string, refresh credentials.TokenRefresher) (*models.OAuthToken, error) {
			order = append(order, "renew")
			renewTypes = append(renewTypes, tokenType)
			// Shape the production error: Google 400 + invalid_grant body
			// → postTokenRequest wraps credentials.ErrInvalidGrant.
			return nil, fmt.Errorf("youtube refresh: token exchange failed (status 400): %w", credentials.ErrInvalidGrant)
		},
	}
	origUpdate := posts.updateStatusFn
	posts.updateStatusFn = func(t *models.PostTarget) error {
		order = append(order, "updateStatus")
		if origUpdate != nil {
			return origUpdate(t)
		}
		return nil
	}
	w := newTestWorker(posts, users, models.PlatformYouTube, svc, vault)

	if err := w.publishTarget(context.Background(), scheduledTarget()); err == nil {
		t.Fatal("publishTarget must return an error when the refresh grant is revoked")
	}

	// 1. Only the canonical bearer refresh was attempted — invalid_grant
	//    must NOT fall back to the legacy long_lived row.
	if len(renewTypes) != 1 || renewTypes[0] != models.TokenTypeBearer {
		t.Fatalf("renew types: want only [%q], got %v", models.TokenTypeBearer, renewTypes)
	}
	// 2. The channel was marked reauth_required with the stable code.
	if users.markReauthRequiredCalls != 1 {
		t.Errorf("MarkReauthRequired calls: want 1, got %d", users.markReauthRequiredCalls)
	}
	if users.lastMarkReauthAccountID != 10 {
		t.Errorf("MarkReauthRequired account id: want 10, got %d", users.lastMarkReauthAccountID)
	}
	if users.lastMarkReauthCode != YouTubeReauthCode {
		t.Errorf("MarkReauthRequired code: want %q, got %q", YouTubeReauthCode, users.lastMarkReauthCode)
	}
	// 3. Target → blocked_auth (NOT failed) with the dashboard code.
	if posts.updateCalls != 1 {
		t.Fatalf("UpdateStatus calls: want 1, got %d", posts.updateCalls)
	}
	if posts.updateTargets[0].Status != models.PostStatusBlockedAuth {
		t.Errorf("final status: want blocked_auth, got %q", posts.updateTargets[0].Status)
	}
	if posts.updateTargets[0].LastErrorCode != "blocked_auth" {
		t.Errorf("LastErrorCode: want %q (operator-dashboard filter), got %q", "blocked_auth", posts.updateTargets[0].LastErrorCode)
	}
	// 4. Publish never ran, and no idempotency key was stamped for a
	//    blocked-auth refusal (mirrors the channel-mismatch path).
	if svc.publishCalls != 0 {
		t.Errorf("Publish calls: want 0, got %d", svc.publishCalls)
	}
	if posts.setKeyCalls != 0 {
		t.Errorf("SetProviderIdempotencyKey calls on invalid_grant: want 0 (no key stamped for blocked-auth refused publishes), got %d", posts.setKeyCalls)
	}
	// 5. Order: the account reauth flag lands BEFORE the target terminal
	//    transition (same MED-1 sequencing the channel-mismatch path pins).
	want := []string{"findByID", "findAccount", "renew", "markReauth", "updateStatus"}
	if len(order) != len(want) {
		t.Fatalf("call order: want %v, got %v", want, order)
	}
	for i, step := range want {
		if order[i] != step {
			t.Errorf("step[%d]: want %q, got %q (full order: %v)", i, step, order[i], order)
		}
	}
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
