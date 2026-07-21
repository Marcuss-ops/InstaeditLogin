package worker

import (
	"context"
	"errors"
	"fmt"

	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// blockedAuthError signals that a target should be transitioned to
// status='blocked_auth' rather than the generic 'failed'. It is
// intentionally package-private so the caller (publishTarget) can
// distinguish blocked-auth failures from transient failures without
// parsing error strings.
type blockedAuthError struct {
	reason string
}

func (e *blockedAuthError) Error() string {
	return e.reason
}

// prepareCredentials refreshes the OAuth token for the account, runs
// the YouTube channel-binding check, and performs the optional canary
// pre-flight. It returns the final access token to pass to Publish.
//
// On a non-recoverable auth refusal (channel mismatch, canary failure),
// it returns a *blockedAuthError so the caller can route the target to
// status='blocked_auth'. On transient failures it returns a regular
// error so the caller can route to status='failed'.
func (w *PublishWorker) prepareCredentials(ctx context.Context, target *models.PostTarget, account *models.PlatformAccount, post *models.Post, oauth services.OAuthProvider) (*models.OAuthToken, error) {
	refresher := credentials.TokenRefresher(func(ctx context.Context, refreshToken string) (*models.TokenData, error) {
		return oauth.RefreshOAuthToken(ctx, refreshToken)
	})

	oauthToken, err := w.vault.Renew(ctx, account.ID, models.TokenTypeBearer, refresher)
	if err != nil {
		oauthToken, err = w.vault.Renew(ctx, account.ID, models.TokenTypeLongLived, refresher)
		if err != nil {
			return nil, fmt.Errorf("token refresh failed: %w", err)
		}
	}

	// For providers that publish via a page-scoped token (Facebook
	// Pages), prefer the page access token stored for the account.
	// Page Access Tokens do not need refresh; the vault Get path
	// returns them as long as the grant is valid.
	if pageToken, err := w.vault.Get(ctx, account.ID, models.TokenTypePageAccess); err == nil && pageToken.AccessToken != "" {
		oauthToken = pageToken
	}

	// 5b. YOUTUBE ONLY — P0#3 server-side channel binding check.
	if account.Platform == models.PlatformYouTube {
		raw, hasRaw := w.router.Get(account.Platform)
		if hasRaw {
			if binder, ok := raw.(services.YouTubeChannelBinder); ok {
				if bindErr := binder.ValidateChannelBinding(ctx, oauthToken.AccessToken, account.PlatformUserID); bindErr != nil {
					if errors.Is(bindErr, services.ErrYouTubeChannelMismatch) {
						if flagErr := w.userRepo.MarkReauthRequired(ctx, account.ID, "youtube_channel_mismatch", bindErr.Error()); flagErr != nil {
							w.logger.Warn("could not flag platform_account reauth_required after youtube channel mismatch",
								"platform_account_id", account.ID, "post_id", target.PostID, "flag_error", flagErr)
						}
						w.recordChannelMismatch(account.Platform)
						w.logger.Warn("youtube channel binding mismatch; refusing upload",
							"target_id", target.ID, "post_id", target.PostID,
							"platform_account_id", account.ID,
							"expected_channel_id", account.PlatformUserID,
							"error", bindErr)
						return nil, &blockedAuthError{reason: "youtube channel binding check: " + bindErr.Error()}
					}
					w.logger.Warn("youtube channel binding check failed (transient); will retry",
						"target_id", target.ID, "post_id", target.PostID,
						"platform_account_id", account.ID, "error", bindErr)
					return nil, fmt.Errorf("youtube channel binding check: %w", bindErr)
				}
			}
		}
	}

	// 5c. Optional canary pre-flight (Task 7/10).
	if w.isCanaryEnabled(ctx, post) {
		oauthToken, renewErr := w.vault.Renew(ctx, account.ID, models.TokenTypeBearer, refresher)
		if renewErr != nil {
			return nil, &blockedAuthError{reason: "canary pre-flight: renew failed: " + renewErr.Error()}
		}
		uploader := w.canonicalCanaryUploader
		if uploader == nil {
			w.logger.Warn("publish worker: canary capability absent — skipping pre-flight",
				"platform_account_id", account.ID)
			return nil, &blockedAuthError{reason: "canary pre-flight: capability absent"}
		}
		res, canErr := uploader.CanaryUpload(ctx, oauthToken.AccessToken, account.PlatformUserID)
		if canErr != nil || res == nil || res.UploadedChannelID != account.PlatformUserID {
			w.logger.Warn("canary channel mismatch; flagging target blocked_auth",
				"target_id", target.ID, "platform_account_id", account.ID)
			return nil, &blockedAuthError{reason: "canary pre-flight: channel mismatch"}
		}
		if err := w.postRepo.SetTargetCanaryVideoID(target.ID, res.VideoID); err != nil {
			w.logger.Warn("canary_video_id persistence failed (non-fatal)",
				"target_id", target.ID, "video_id", res.VideoID, "error", err)
		}
	}

	return oauthToken, nil
}

// buildPayload assembles the PublishPayload for the target. It applies
// the privacy-level precedence cascade and platform-specific defaults
// in the process phase. The idempotency key is injected into the payload
// before the publish call.
func (w *PublishWorker) buildPayload(target *models.PostTarget, account *models.PlatformAccount, post *models.Post, key string) models.PublishPayload {
	_ = target // reserved for future per-target payload overrides
	payload := models.PublishPayload{
		Text:         post.Caption,
		Title:        post.Title,
		PublishAt:    post.PublishAt,
		PrivacyLevel: post.PrivacyLevel,
	}
	if post.MediaURL != "" {
		payload.VideoURL = post.MediaURL
	}
	// Fallback to the inherited batch default (middle term of the cascade).
	if payload.PrivacyLevel == "" {
		payload.PrivacyLevel = post.DefaultPrivacyLevel
	}
	// YouTube-safe default.
	if account.Platform == models.PlatformYouTube && payload.PrivacyLevel == "" {
		payload.PrivacyLevel = "unlisted"
	}
	// Generic fallback.
	if payload.PrivacyLevel == "" {
		payload.PrivacyLevel = "PUBLIC_TO_EVERYONE"
	}
	// TikTok's PULL_FROM_URL mode requires the video URL's domain to be
	// ownership-verified, so route through PULL_FROM_FILE instead.
	if account.Platform == models.PlatformTikTok && payload.Source == "" {
		payload.Source = models.PublishSourcePULLFromFile
	}
	payload.IdempotencyKey = key
	return payload
}
