package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// publishTarget drives the per-target 3-step status transition:
//
//  1. ATOMIC CLAIM: queued → publishing (verdict §10). The lease-aware
//     ClaimQueuedTargetWithLease stamps the per-replica lease and uses
//     SELECT FOR UPDATE SKIP LOCKED as a logical lock so only ONE
//     worker wins. The loser sees a `claimed=false` return and skips
//     — no double-publish.
//  2. Load parent Post (caption/title/media_url for the publish payload)
//     AND PlatformAccount (platform name + platform_user_id for dispatch).
//     Safe to do AFTER the claim: if either is missing, we transition
//     to 'failed' (we own the row), so the next tick won't re-pick it.
//  3. Refresh OAuth token via the CredentialVault (which serialises
//     concurrent refreshes with a Postgres advisory lock).
//     On failure: status → `failed` with error_message.
//  4. Publish via the platform's Publisher capability.
//     On sync platforms: status → 'published' with platform_post_id + published_at.
//     On async platforms (Taglio 4.2): status stays 'publishing', the
//     platform_post_id gets the publish_id from the result, and the
//     ReconcileWorker goroutine will drive the state machine on
//     subsequent ticks. (See reconcile_worker.go::reconcileTarget.)
//     On failure: status → `failed` with error_message.
//
// The 'failed' transitions only happen AFTER a successful claim, so
// two workers running in parallel won't redundantly write 'failed' to
// the same row (the loser would have already returned with
// claimed=false).
//
// The body is decomposed into phase methods so each transition is
// independently testable; each phase either returns its result or
// performs the terminal target transition itself and signals the
// orchestrator to stop (stop=true — the accompanying error, possibly
// nil, is publishTarget's return value).
func (w *PublishWorker) publishTarget(ctx context.Context, target *models.PostTarget) error {
	// 1. ATOMIC CLAIM: queued → publishing. If another worker
	// already claimed this target, claim returns false and we skip.
	claimed, err := w.claimTarget(ctx, target)
	if err != nil {
		return err
	}
	if !claimed {
		return nil // not an error — just skip
	}

	// 2. Load parent post and platform account.
	post, account, err := w.loadPostAndAccount(ctx, target)
	if err != nil {
		return w.markFailed(target, err.Error())
	}
	// A YouTube grant marked for reauthorization is not eligible for
	// publishing. Block already-queued targets before capability lookup or
	// token refresh; invalid_grant fan-out can otherwise leave a sibling
	// target attempting to publish until its own token expires.
	if account.Platform == models.PlatformYouTube &&
		(account.Status == models.AccountStatusReauthRequired || account.ReauthRequiredAt != nil) {
		return w.markPublishBlockedAuth(target, youtubeReauthReason())
	}
	// Google Drive is an exporter, not a social Publisher. It has an
	// OAuth credential and a DeliveryRegistry provider but deliberately
	// no CapabilityRouter Publisher implementation. Dispatch it directly
	// after claiming the target so Drive never falls through the YouTube
	// publish contract.
	if account.Platform == models.PlatformGoogleDrive && w.deliveryRegistry != nil {
		return w.publishDriveExport(ctx, target, account, post)
	}

	// 4. Resolve platform capabilities FIRST (cheap, fail-fast): we need
	// the OAuthProvider (for token refresh) AND the Publisher (for the
	// actual call). A platform missing either cannot be published to —
	// resolving BEFORE the expensive per-channel translation below
	// avoids burning a 30-180s NVIDIA call on a target that was going
	// to fail at capability lookup anyway.
	oauth, oauthOK := w.router.OAuth(account.Platform)
	publisher, pubOK := w.router.Publisher(account.Platform)
	if !oauthOK || !pubOK {
		return w.markFailed(target, fmt.Sprintf("platform %q missing capability (oauth=%v publish=%v)", account.Platform, oauthOK, pubOK))
	}

	// 3b. PER-CHANNEL-LANGUAGE POSTING: when the target channel
	// declares a language (account.Metadata["language"]) different
	// from the post's source language, translate title + caption
	// BEFORE building the publish payload. The localized post flows
	// through buildPayload (fresh publishes), publishYouTubePhase2
	// (Phase-2 videos.update reuse) and executePublish alike. A
	// translation failure marks the target failed — in production the
	// lease-aware repo routes that through the retrying state machine
	// (auto re-pick on the next tick); with legacy test doubles it is
	// terminal 'failed'. Either way we never publish the wrong language.
	localizedPost, translated, err := w.localizeForChannel(ctx, target, account, post)
	if err != nil {
		return w.markFailed(target, err.Error())
	}
	if translated {
		post = localizedPost
	}

	// 5. Refresh the OAuth token (with the Facebook page-token
	// override) via the CredentialVault.
	oauthToken, refresher, stop, err := w.resolvePublishToken(ctx, target, account, oauth)
	if stop {
		return err
	}

	// 5b. YOUTUBE ONLY — P0#3 server-side channel binding check.
	if stop, err := w.validateYouTubeChannelBinding(ctx, target, account, oauthToken); stop {
		return err
	}

	// 5c. Optional canary pre-flight (Task 7/10).
	oauthToken, stop, err = w.runCanaryPreflight(ctx, target, account, post, refresher, oauthToken)
	if stop {
		return err
	}

	// 6. Build payload + publish. MediaURL goes through as VideoURL (the
	// payload's ImageURL branch is reserved for image-only posts that
	// don't have a content_type column — future enhancement).
	key, err := w.ensureProviderIdempotencyKey(target, account)
	if err != nil {
		return err
	}
	// Build the publish payload, applying the privacy-level cascade
	// and platform-specific defaults in the process phase.
	payload := w.buildPayload(account, post, key)

	if handled, err := w.publishYouTubePhase2(ctx, target, account, post, oauthToken, payload); err != nil {
		return err
	} else if handled {
		return nil
	}

	// All platform uploads are finalized by executePublish. It throttles,
	// resolves the canonical media asset immediately before Publisher.Publish,
	// and owns the sync/async target transition plus completion dispatch.
	return w.executePublish(ctx, target, account, post, oauthToken, payload, publisher)
}

// publishDriveExport is the Google Drive branch of publishTarget: Drive
// is an exporter, not a social Publisher, so after the claim we resolve
// a fresh media URL, complete the Drive delivery, and only then mark the
// target published. Callers must have verified the platform is
// Google Drive and that the DeliveryRegistry is configured.
func (w *PublishWorker) publishDriveExport(ctx context.Context, target *models.PostTarget, account *models.PlatformAccount, post *models.Post) error {
	if w.resolver == nil {
		return w.markFailed(target, "media download resolver is not configured")
	}
	deliveryURL, err := w.resolver.ResolveForUpload(ctx, post, time.Hour)
	if err != nil {
		return w.markFailed(target, "resolve fresh media URL for Drive delivery: "+err.Error())
	}
	// Velox destination defaults are carried through UploadJob.Metadata
	// and materialised on Post.Metadata. Forward only the destination
	// folder to the provider; credentials and account selection remain
	// server-side.
	config := map[string]string{}
	var metadata map[string]json.RawMessage
	if json.Unmarshal(post.Metadata, &metadata) == nil {
		if raw, ok := metadata["folder_id"]; ok {
			var folderID string
			if json.Unmarshal(raw, &folderID) == nil {
				config["folder_id"] = folderID
			}
		}
	}
	res, deliverErr := w.dispatchPostCompletion(ctx, target, account, &models.MediaAsset{
		ID:          mediaReferenceValue(post.MediaAssetID),
		ContentType: "video/mp4",
	}, deliveryURL, config)
	if deliverErr != nil {
		return w.markFailed(target, "Drive delivery failed: "+deliverErr.Error())
	}
	if res == nil || res.Status != "published" {
		reason := "Drive delivery did not complete"
		if res != nil && res.Metadata != nil {
			if code := res.Metadata["error_code"]; code != "" {
				reason = "Drive delivery " + code
			}
			if res.Metadata["error_code"] == "drive_auth_required" {
				if flagErr := w.userRepo.MarkReauthRequired(ctx, account.ID, "drive_auth_required", reason); flagErr != nil {
					w.logger.Warn("could not flag Drive account reauth_required", "platform_account_id", account.ID, "error", flagErr)
				}
				return w.markPublishBlockedAuth(target, reason)
			}
		}
		return w.markFailed(target, reason)
	}
	target.Status = models.PostStatusPublished
	now := time.Now()
	target.PublishedAt = &now
	if err := w.updateTargetStatus(ctx, target); err != nil {
		return fmt.Errorf("mark Drive export target published: %w", err)
	}
	w.syncContentPackageState(ctx, post)
	return nil
}

// resolvePublishToken is phase 5 of publishTarget: refresh the OAuth
// token via the CredentialVault and apply the Facebook page-token
// override. The returned refresher is reused by the canary pre-flight.
// On failure it performs the terminal target transition itself and
// returns stop=true with publishTarget's return value.
func (w *PublishWorker) resolvePublishToken(ctx context.Context, target *models.PostTarget, account *models.PlatformAccount, oauth services.OAuthProvider) (*models.OAuthToken, credentials.TokenRefresher, bool, error) {
	// Refresh token via the CredentialVault. The provider's
	// RefreshOAuthToken method is adapted to a credentials.TokenRefresher
	// closure so the vault only knows the function signature. YouTube
	// uses the canonical bearer type first and keeps long_lived only as
	// a temporary compatibility fallback for pre-normalization rows.
	refresher := credentials.TokenRefresher(func(ctx context.Context, refreshToken string) (*models.TokenData, error) {
		return oauth.RefreshOAuthToken(ctx, refreshToken)
	})
	var oauthToken *models.OAuthToken
	var err error
	if account.Platform == models.PlatformYouTube {
		oauthToken, err = credentials.RenewYouTubeToken(ctx, w.vault, account.ID, refresher, w.logger)
	} else {
		oauthToken, err = w.vault.Renew(ctx, account.ID, models.TokenTypeBearer, refresher)
		if errors.Is(err, credentials.ErrModernGrantMissing) {
			oauthToken, err = w.vault.Renew(ctx, account.ID, models.TokenTypeLongLived, refresher)
		}
	}
	if err != nil {
		if account.Platform == models.PlatformYouTube && errors.Is(err, credentials.ErrYouTubeInvalidGrant) {
			w.markYouTubeGrantReauth(ctx, account)
			return nil, nil, true, w.markPublishBlockedAuth(target, youtubeReauthReason())
		}
		return nil, nil, true, w.markFailed(target, "token refresh failed")
	}

	// For providers that publish via a page-scoped token (Facebook
	// Pages), prefer the page access token stored for the account.
	// Page Access Tokens do not need refresh; the vault Get path
	// returns them as long as the grant is valid.
	if pageToken, err := w.vault.Get(ctx, account.ID, models.TokenTypePageAccess); err == nil && pageToken.AccessToken != "" {
		oauthToken = pageToken
	}
	return oauthToken, refresher, false, nil
}

// validateYouTubeChannelBinding is phase 5b of publishTarget — the P0#3
// server-side channel binding check. No-op (stop=false) for non-YouTube
// platforms and for providers that don't implement the binder.
//
// The OAuth grant we just refreshed MUST be bound to the SAME
// channel as platform_account.platform_user_id. The refresh above
// doesn't tell us; only channels.list?mine=true can confirm.
// Without this check, a grant that was silently re-bound to a
// different channel (Google rotation, operator migration, fraud)
// would happily upload the video to the wrong channel.
//
// Placement rationale:
//   - AFTER refresh + page-token override so oauthToken is the
//     final access token we will pass to Publish (the check uses
//     it AND the publish uses it; no double-refresh).
//   - BEFORE the idempotency-key stamp so a flag-failed upload
//     does NOT stamp a key (the post_target is going to
//     'failed', not 'publishing'; no future retries should
//     dedup against it).
//
// On ErrYouTubeChannelMismatch (channel id NOT in the grant's
// channel set): flag the platform_account reauth_required (so
// the dashboard prompts the operator to reconnect) AND mark
// this post_target failed (so the worker stops trying).
//
// On any other error (5xx, network, decode): treat as transient
// — DO NOT flag reauth — and let the next tick retry.
func (w *PublishWorker) validateYouTubeChannelBinding(ctx context.Context, target *models.PostTarget, account *models.PlatformAccount, oauthToken *models.OAuthToken) (bool, error) {
	if account.Platform != models.PlatformYouTube {
		return false, nil
	}
	raw, hasRaw := w.router.Get(account.Platform)
	if !hasRaw {
		return false, nil
	}
	binder, ok := raw.(services.YouTubeChannelBinder)
	if !ok {
		// If the registered provider doesn't implement the
		// binder (older test fixtures, future non-YouTube
		// provider that accidentally registers under the
		// youtube name), the check is skipped — the existing
		// publish path proceeds. New YouTubeOAuthService
		// implementations MUST satisfy the compile-time
		// assertion in services/youtube_oauth.go.
		return false, nil
	}
	bindErr := binder.ValidateChannelBinding(ctx, oauthToken.AccessToken, account.PlatformUserID)
	if bindErr == nil {
		return false, nil
	}
	if errors.Is(bindErr, services.ErrYouTubeChannelMismatch) {
		if flagErr := w.userRepo.MarkReauthRequired(ctx, account.ID, "youtube_channel_mismatch", bindErr.Error()); flagErr != nil {
			// Soft error — the post_target still goes
			// to 'blocked_auth' below; we just couldn't
			// stamp the platform_account's flag. Log
			// so the operator sees both signals.
			w.logger.Warn("could not flag platform_account reauth_required after youtube channel mismatch",
				"platform_account_id", account.ID, "post_id", target.PostID, "flag_error", flagErr)
		}
		// P0 #2: increment the operator-facing /
		// dashboard signal alongside the DB-side
		// flag. Drift up means Google silently
		// re-bound the OAuth grant to a different
		// Brand Account — the operator must
		// investigate before reconnecting.
		// Increment is UNCONDITIONAL on mismatch
		// detection (not on DB-write success) so a
		// transient MarkReauthRequired blip cannot
		// hide reauth rates from the dashboard.
		w.recordChannelMismatch(account.Platform)
		w.logger.Warn("youtube channel binding mismatch; refusing upload",
			"target_id", target.ID, "post_id", target.PostID,
			"platform_account_id", account.ID,
			"expected_channel_id", account.PlatformUserID,
			"error", bindErr)
		// Task 2/10: route the post_target to
		// PostStatusBlockedAuth (distinct from the
		// generic PostStatusFailed so dashboards
		// can answer "what's pending reauth?").
		// The operator reconnects the channel,
		// platform_account.status flips back to
		// active, and the NEXT tick (driven by
		// resume) rewrites the row to queued.
		return true, w.markPublishBlockedAuth(target, "youtube channel binding check: "+bindErr.Error())
	}
	w.logger.Warn("youtube channel binding check failed (transient); will retry",
		"target_id", target.ID, "post_id", target.PostID,
		"platform_account_id", account.ID, "error", bindErr)
	return true, w.markFailed(target, "youtube channel binding check: "+bindErr.Error())
}

// runCanaryPreflight is phase 5c of publishTarget — the optional canary
// pre-flight (Task 7/10). When the post carries
// metadata.canary_upload=true, upload a 5-10s/<5MB/privacy=private canary
// video to the same channel and confirm the binding matches BEFORE the real
// publish. On mismatch the post_target is marked PostStatusBlockedAuth (via
// the existing markPublishBlockedAuth helper) and the real Publish must NOT
// run. Documented in docs/OAUTH-PRODUCTION.md (canary pre-flight).
//
// Returns the (possibly re-renewed) token to use for the real publish.
func (w *PublishWorker) runCanaryPreflight(ctx context.Context, target *models.PostTarget, account *models.PlatformAccount, post *models.Post, refresher credentials.TokenRefresher, oauthToken *models.OAuthToken) (*models.OAuthToken, bool, error) {
	if !w.isCanaryEnabled(ctx, post) {
		return oauthToken, false, nil
	}
	var renewErr error
	if account.Platform == models.PlatformYouTube {
		oauthToken, renewErr = credentials.RenewYouTubeToken(ctx, w.vault, account.ID, refresher, w.logger)
	} else {
		oauthToken, renewErr = w.vault.Renew(ctx, account.ID, models.TokenTypeBearer, refresher)
	}
	if renewErr != nil {
		return oauthToken, true, w.markPublishBlockedAuth(target, "canary pre-flight: renew failed")
	}
	uploader := w.canonicalCanaryUploader
	if uploader == nil {
		w.logger.Warn("publish worker: canary capability absent — skipping pre-flight",
			"platform_account_id", account.ID)
		return oauthToken, true, w.markPublishBlockedAuth(target, "canary pre-flight: capability absent")
	}
	res, canErr := uploader.CanaryUpload(ctx, oauthToken.AccessToken, account.PlatformUserID)
	if canErr != nil || res == nil || res.UploadedChannelID != account.PlatformUserID {
		w.logger.Warn("canary channel mismatch; flagging target blocked_auth",
			"target_id", target.ID, "platform_account_id", account.ID)
		return oauthToken, true, w.markPublishBlockedAuth(target, "canary pre-flight: channel mismatch")
	}
	if err := w.postRepo.SetTargetCanaryVideoID(target.ID, res.VideoID); err != nil {
		w.logger.Warn("canary_video_id persistence failed (non-fatal)",
			"target_id", target.ID, "video_id", res.VideoID, "error", err)
	}
	return oauthToken, false, nil
}

// ensureProviderIdempotencyKey stamps (or reuses) the deterministic
// provider_idempotency_key for the target.
//
// Taglio 4.7 LEVEL 2 (migration 022): ensure the post_target has
// the deterministic provider_idempotency_key stamped onto it BEFORE
// publishing. The key is computed from (post.ID, account.ID) so it
// is stable across retries — the platform's native API dedup
// catches the duplicate publish on its end. Forward it on the
// payload so providers that support per-call idempotency keys
// (LinkedIn "X-Restli-Idempotency-Key", Twitter v2 "request_id",
// TikTok "idempotent" query param) drive the upstream API to
// dedup; providers without native support ignore the field, but
// the DB-level UNIQUE(platform_account_id, provider_idempotency_key)
// constraint is the catch-all safety net.
func (w *PublishWorker) ensureProviderIdempotencyKey(target *models.PostTarget, account *models.PlatformAccount) (string, error) {
	if target.ProviderIdempotencyKey != nil && *target.ProviderIdempotencyKey != "" {
		return *target.ProviderIdempotencyKey, nil
	}
	key := computeProviderIdempotencyKey(target.PostID, account.ID)
	// Mirror the stamped key onto the in-memory struct so any
	// SUBSEQUENT path that reads target.ProviderIdempotencyKey
	// (UpdateStatus captures, future debug-log wires) sees the
	// stamped value, not the pre-stamp nil. Without this mirror
	// we trust ListPending's SELECT to include the column on
	// every re-fetch (the case today) — setting it locally
	// removes that implicit coupling.
	target.ProviderIdempotencyKey = &key
	if err := w.postRepo.SetProviderIdempotencyKey(target.ID, key); err != nil {
		if errors.Is(err, repository.ErrProviderIdempotencyConflict) {
			// Degenerate: another row on the same account already
			// has this key (collision with extremely low probability
			// for SHA-256 prefix, OR a stale key from a prior failed
			// attempt). Do NOT leave the row in 'publishing' — it
			// would be polled forever by the reconciler and never
			// re-picked by the driver either. Promote to 'failed'
			// so the row drops out of BOTH filter sets and the
			// operator can see + reconcile it.
			w.logger.Warn("provider idempotency key conflict on stamp; promoting target to failed",
				"target_id", target.ID, "post_id", target.PostID,
				"platform_account_id", account.ID, "key", key, "error", err)
			target.Status = models.PostStatusFailed
			target.ErrorMessage = "provider idempotency key conflict: " + err.Error()
			if updateErr := w.updateTargetStatus(context.Background(), target); updateErr != nil {
				// Surface both errors so the tick counter increments
				// AND the operator sees the underlying failure mode.
				return "", fmt.Errorf("provider idempotency key conflict (also failed to mark failed: %v): %w",
					updateErr, err)
			}
			return "", fmt.Errorf("provider idempotency key conflict: %w", err)
		}
		if errors.Is(err, repository.ErrPostTargetNotFound) {
			// Stale id — another worker or a manual op touched the row.
			// Don't double-publish; treat as a failed tick entry.
			return "", fmt.Errorf("provider idempotency key stamp on missing target: %w", err)
		}
		return "", fmt.Errorf("ensure provider idempotency key: %w", err)
	}
	return key, nil
}
