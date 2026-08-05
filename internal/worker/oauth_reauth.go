package worker

import (
	"context"
	"log/slog"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

const (
	// YouTubeReauthCode is the stable, shared-grant classification used by
	// both refresh and worker propagation paths.
	YouTubeReauthCode = "SHARED_GRANT_REAUTH_REQUIRED"
	// YouTubeReauthMessage is deliberately provider-safe and contains no
	// upstream response body, SQL error, or credential material.
	YouTubeReauthMessage = "Shared OAuth grant requires reauthorization"
)

// OAuthConnectionReauthStore is the invalid_grant-specific extension
// implemented by the production user repository. Channel-binding mismatch
// paths use MarkReauthRequired on the individual account and do not call this
// grant-wide method. It deliberately stays outside
// PublisherUserStore/ReconcileUserStore so existing test doubles and other
// consumers do not need to grow a grant-wide mutation surface.
type OAuthConnectionReauthStore interface {
	MarkOAuthConnectionAccountsReauthRequired(ctx context.Context, oauthConnectionID int64, code, message string) error
}

// markYouTubeGrantReauth marks every YouTube channel sharing the same OAuth
// grant when the repository supports the canonical connection-wide update.
// Legacy/narrow stores fall back to marking only the current account. No token
// or upstream error is returned to logs or clients.
func markYouTubeGrantReauth(ctx context.Context, userRepo PublisherUserStore, logger *slog.Logger, account *models.PlatformAccount) {
	if account == nil || account.Platform != models.PlatformYouTube {
		return
	}
	if logger == nil {
		logger = slog.Default()
	}

	// A production repository can atomically fan out by the canonical grant
	// key. Use that path exclusively when available so the current channel is
	// not updated twice and all sibling channels are transitioned together.
	if account.OAuthConnectionID != nil && *account.OAuthConnectionID > 0 {
		store, ok := userRepo.(OAuthConnectionReauthStore)
		if !ok {
			logger.Error("shared OAuth grant reauth propagation capability is unavailable",
				"oauth_connection_id", *account.OAuthConnectionID)
			return
		}
		if err := store.MarkOAuthConnectionAccountsReauthRequired(ctx, *account.OAuthConnectionID, YouTubeReauthCode, YouTubeReauthMessage); err != nil {
			// Do not fall back to a single-account update: that would leave
			// sibling channels with a misleading active state. The vault and
			// worker both fail closed until the atomic grant-wide operation can
			// be retried.
			logger.Error("shared OAuth grant reauth propagation failed",
				"oauth_connection_id", *account.OAuthConnectionID, "error", err)
		}
		return
	}

	// Compatibility fallback is limited to legacy rows without a canonical
	// grant key; there are no siblings that can be addressed safely in that
	// shape.
	if err := userRepo.MarkReauthRequired(ctx, account.ID, YouTubeReauthCode, YouTubeReauthMessage); err != nil {
		logger.Warn("could not flag legacy YouTube platform account reauth_required",
			"platform_account_id", account.ID, "error", err)
	}
}

func (w *PublishWorker) markYouTubeGrantReauth(ctx context.Context, account *models.PlatformAccount) {
	markYouTubeGrantReauth(ctx, w.userRepo, w.logger, account)
}

func (w *ReconcileWorker) markYouTubeGrantReauth(ctx context.Context, account *models.PlatformAccount) {
	markYouTubeGrantReauth(ctx, w.userRepo, w.logger, account)
}

func youtubeReauthReason() string {
	return YouTubeReauthMessage + ": autorizzazione Google revocata o scaduta"
}
