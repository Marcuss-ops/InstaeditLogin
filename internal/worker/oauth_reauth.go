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

// OAuthConnectionReauthStore is an optional extension implemented by the
// production user repository. It deliberately stays outside
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
		if store, ok := userRepo.(OAuthConnectionReauthStore); ok {
			if err := store.MarkOAuthConnectionAccountsReauthRequired(ctx, *account.OAuthConnectionID, YouTubeReauthCode, YouTubeReauthMessage); err == nil {
				return
			} else {
				logger.Warn("could not flag YouTube accounts sharing OAuth connection as reauth_required",
					"oauth_connection_id", *account.OAuthConnectionID, "error", err)
				// Fall through so the current channel still receives a
				// reauth_required signal if the grant-wide write is unavailable.
			}
		}
	}

	// Compatibility fallback for narrow test stores and legacy wiring without
	// a grant key. It still marks the current account and never exposes the
	// upstream OAuth response.
	if err := userRepo.MarkReauthRequired(ctx, account.ID, YouTubeReauthCode, YouTubeReauthMessage); err != nil {
		logger.Warn("could not flag YouTube platform account reauth_required",
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
