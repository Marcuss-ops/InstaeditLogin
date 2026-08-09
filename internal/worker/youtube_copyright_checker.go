package worker

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

type YouTubeCopyrightWorker struct {
	store    repository.YouTubeCopyrightCheckStore
	users    PublisherUserStore
	cap      *services.CapabilityRouter
	vault    credentials.VaultAPI
	interval time.Duration
	logger   *slog.Logger
}

func NewYouTubeCopyrightWorker(store repository.YouTubeCopyrightCheckStore, users PublisherUserStore, cap *services.CapabilityRouter, vault credentials.VaultAPI, interval time.Duration, logger *slog.Logger) *YouTubeCopyrightWorker {
	if logger == nil {
		logger = slog.Default()
	}
	if interval <= 0 {
		interval = 20 * time.Minute
	}
	return &YouTubeCopyrightWorker{store: store, users: users, cap: cap, vault: vault, interval: interval, logger: logger}
}

func (w *YouTubeCopyrightWorker) Run(ctx context.Context) error {
	w.logger.Info("youtube copyright checker started", "interval", w.interval.String())
	defer w.logger.Info("youtube copyright checker stopped")
	w.runOnce(ctx)
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			w.runOnce(ctx)
		}
	}
}

func (w *YouTubeCopyrightWorker) runOnce(ctx context.Context) {
	items, err := w.store.ListPendingCopyrightChecks(ctx, 100, time.Now().Add(-w.interval))
	if err != nil {
		w.logger.Error("youtube copyright check list failed", "error", err)
		return
	}
	for _, item := range items {
		if err := w.checkOne(ctx, item); err != nil {
			w.logger.Warn("youtube copyright check failed", "publication_id", item.ID, "video_id", item.VideoID, "error", err)
			if markErr := w.store.MarkCopyrightCheckError(ctx, item.ID, err.Error()); markErr != nil {
				w.logger.Error("youtube copyright error state failed", "publication_id", item.ID, "error", markErr)
			}
		}
	}
}

func (w *YouTubeCopyrightWorker) checkOne(ctx context.Context, item models.YouTubeCopyrightCandidate) error {
	account, err := w.users.FindPlatformAccountByID(item.PlatformAccountID)
	if err != nil {
		return fmt.Errorf("find platform account: %w", err)
	}
	if account == nil {
		return fmt.Errorf("platform account %d not found", item.PlatformAccountID)
	}
	oauth, ok := w.cap.OAuth(account.Platform)
	if !ok {
		return fmt.Errorf("oauth provider %q unavailable", account.Platform)
	}
	token, err := credentials.RenewYouTubeToken(ctx, w.vault, account.ID, oauth.RefreshOAuthToken, w.logger)
	if err != nil {
		return fmt.Errorf("renew youtube token: %w", err)
	}
	raw, ok := w.cap.Get(models.PlatformYouTube)
	if !ok {
		return fmt.Errorf("youtube provider unavailable")
	}
	checker, ok := raw.(services.YouTubeCopyrightChecker)
	if !ok {
		return fmt.Errorf("youtube provider does not support copyright checks")
	}
	check, err := checker.CheckCopyright(ctx, token.AccessToken, item.VideoID)
	if err != nil {
		return err
	}
	return w.store.MarkCopyrightChecked(ctx, item.ID, models.YouTubeCopyrightResult{
		Status: models.YouTubeCopyrightStatus(check.Status), Message: check.Message,
		ProcessingStatus: check.ProcessingStatus, RejectionReason: check.RejectionReason,
		FailureReason: check.FailureReason, LicensedContent: check.LicensedContent,
		BlockedRegions: check.BlockedRegions, AllowedRegions: check.AllowedRegions,
	})
}
