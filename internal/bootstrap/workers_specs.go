package bootstrap

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/outbox"
	"github.com/Marcuss-ops/InstaeditLogin/internal/outbox/processors"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
	"github.com/Marcuss-ops/InstaeditLogin/internal/worker"
	"github.com/Marcuss-ops/InstaeditLogin/pkg/metrics"
)

func (a *App) publishWorkerSpec() worker.WorkerSpec {
	return worker.WorkerSpec{
		Name:     "publish",
		Critical: true,
		Run: func(ctx context.Context) error {
			// MediaDownloadResolver (migration 080 followup): wire the
			// fresh-presigned-URL sign path here so executePublish mints
			// per-call signatures immediately before the platform API call.
			// assetsAdapter wraps *repository.MediaAssetRepository and applies
			// workspace-scoped ownership before the resolver mints a URL. The
			// adapter is function-local to keep the wiring change contained.
			pw := worker.NewPublishWorker(
				repository.NewPostRepository(a.DB),
				repository.NewUserRepository(a.DB),
				a.CapRouter,
				a.Vault,
				a.runtime.mediaDownloadResolver,
				a.WorkerID,
				a.MemoryLimiter,
				time.Duration(a.Cfg.Worker.PublishWorkerIntervalSeconds)*time.Second,
				slog.Default(),
			)
			// Burst capacity: pending targets in a single tick are drained
			// by a bounded worker pool (PUBLISH_CONCURRENCY, default 4) so
			// N videos scheduled at the same minute publish concurrently.
			pw.SetPublishConcurrency(a.Cfg.Worker.PublishConcurrency)
			deliveryRegistry := services.NewDeliveryRegistry()
			if ytPub, ok := a.CapRouter.Publisher(models.PlatformYouTube); ok {
				_ = deliveryRegistry.Register(services.NewYouTubeDeliveryAdapter(ytPub))
			}
			if a.Cfg.Auth.GoogleDriveClientID != "" && a.Cfg.Auth.GoogleDriveClientSecret != "" {
				driveSessionRepo := repository.NewDeliverySessionRepository(a.DB)
				var googleDriveOAuth *services.GoogleDriveOAuthService
				if gd, ok := a.CapRouter.Get(models.PlatformGoogleDrive); ok {
					if gdOAuth, typeOK := gd.(*services.GoogleDriveOAuthService); typeOK {
						googleDriveOAuth = gdOAuth
					}
				}
				if googleDriveOAuth != nil {
					driveVault, vaultOK := a.Vault.(services.DriveTokenVault)
					if !vaultOK {
						return fmt.Errorf("publish worker: credential vault lacks Drive refresh-token capability")
					}
					driveTokenProvider := services.NewDriveVaultTokenProvider(driveVault, googleDriveOAuth)
					driveDest, destErr := services.NewGoogleDriveDestination(
						driveSessionRepo,
						driveTokenProvider,
						a.Encryptor,
						services.NewHTTPClient(),
						16*1024*1024,
					)
					if destErr == nil {
						if driveAdapter, adapterErr := services.NewGoogleDriveDeliveryAdapter(driveDest); adapterErr == nil {
							if regErr := deliveryRegistry.Register(driveAdapter); regErr != nil {
								slog.Error("publish worker: register google drive delivery adapter", "error", regErr)
							}
						} else {
							slog.Error("publish worker: build google drive delivery adapter", "error", adapterErr)
						}
					} else {
						slog.Error("publish worker: build google drive destination", "error", destErr)
					}
				}
			}
			_ = deliveryRegistry.Register(services.NewVeloxCallbackDeliveryAdapter(false))
			pw = pw.WithDeliveryRegistry(deliveryRegistry)
			slog.Info("publish worker: delivery registry wired", "providers", deliveryRegistry.Names())
			// Blocco #1 P0 (P1 Migration 077 followup) — wire the
			// YouTube target publication lookup so PublishWorker.publishTarget's
			// Phase-2 bypass block can FindByPostTargetID the
			// Phase-1 stamped youtube_video_id and skip the fresh
			// videos.insert when status="youtube_uploaded". The
			// same *repository.YouTubeTargetPublicationRepository
			// the upload worker uses — the youtube_target_publications
			// table is the contract boundary for the Blocco #1
			// two-phase split (Phase-1 stamps, Phase-2 reads + videos.update).
			pw.SetYouTubeTargetPublicationStore(a.runtime.youtubeTargetPublicationStore)
			pw.SetContentPackageStateSynchronizer(repository.NewContentPackageRepository(a.DB))
			// Per-channel-language posting: wire the NVIDIA translator so
			// targets whose channel declares a language (metadata["language"])
			// get the post's title/caption translated at publish time. The
			// feature is off when NVIDIA_API_KEY is empty — the original
			// text is published unchanged.
			pw.SetNvidiaMetadataTranslator(services.NewMetadataGenerator(
				a.Cfg.AI.NVIDIAAPIKey,
				services.WithModel(a.Cfg.AI.NVIDIAModel),
			))
			if a.Cfg.AI.ArgosTranslateURL != "" {
				pw.SetArgosDescriptionTranslator(services.NewArgosDescriptionTranslator(a.Cfg.AI.ArgosTranslateURL))
				slog.Info("publish worker: Argos description translator wired", "endpoint", a.Cfg.AI.ArgosTranslateURL)
			} else {
				slog.Warn("publish worker: Argos description translator not configured; NVIDIA remains responsible for descriptions")
			}
			return pw.Run(ctx)
		},
	}
}

func (a *App) metadataGenerationWorkerSpec() worker.WorkerSpec {
	return worker.WorkerSpec{
		Name:     "metadata_generation",
		Critical: false,
		Run: func(ctx context.Context) error {
			// Async NVIDIA metadata generation (migration 113): claims
			// metadata_generation_jobs and calls NVIDIA in the background
			// so POST /generate-metadata returns 202 immediately. Feature
			// off when NVIDIA_API_KEY is empty (the worker idles on an
			// empty queue; the POST endpoint still returns 503).
			mw := worker.NewMetadataGenerationWorker(
				repository.NewMetadataGenerationJobRepository(a.DB),
				services.NewMetadataGenerator(a.Cfg.AI.NVIDIAAPIKey, services.WithModel(a.Cfg.AI.NVIDIAModel)),
				time.Duration(a.Cfg.Worker.MetadataGenerationWorkerIntervalSeconds)*time.Second,
				slog.Default(),
			)
			return mw.Run(ctx)
		},
	}
}

func (a *App) reconcileWorkerSpec() worker.WorkerSpec {
	return worker.WorkerSpec{
		Name:     "reconcile",
		Critical: true,
		Run: func(ctx context.Context) error {
			rw := worker.NewReconcileWorker(
				repository.NewPostRepository(a.DB),
				repository.NewUserRepository(a.DB),
				a.CapRouter,
				a.Vault,
				a.WorkerID,
				a.MemoryLimiter,
				time.Duration(a.Cfg.Worker.ReconcileWorkerIntervalSeconds)*time.Second,
				slog.Default(),
			)
			rw.SetContentPackageStateSynchronizer(repository.NewContentPackageRepository(a.DB))
			return rw.Run(ctx)
		},
	}
}

func (a *App) outboxWorkerSpec() worker.WorkerSpec {
	return worker.WorkerSpec{
		Name:     "outbox",
		Critical: true,
		Run: func(ctx context.Context) error {
			ds := outbox.NewDispatcher(outbox.DispatcherConfig{
				OutboxStore:  repository.NewOutboxRepository(a.DB),
				Process:      processors.NewPublishJobsMaterialiser(a.DB),
				Logger:       slog.Default(),
				TickInterval: outbox.DefaultTickInterval,
			})
			return ds.Run(ctx)
		},
	}
}

func (a *App) webhookWorkerSpec() worker.WorkerSpec {
	return worker.WorkerSpec{
		Name:     "webhook",
		Critical: true,
		Run: func(ctx context.Context) error {
			ww := worker.NewWebhookWorkerWithOptions(a.WebhookRepo, worker.WebhookWorkerOptions{
				Interval:          time.Duration(a.Cfg.Worker.WebhookWorkerIntervalSeconds) * time.Second,
				Concurrency:       a.Cfg.Worker.WebhookWorkerConcurrency,
				HTTPTimeout:       time.Duration(a.Cfg.Worker.WebhookHTTPTimeoutSeconds) * time.Second,
				LeaseTTL:          time.Duration(a.Cfg.Worker.WebhookLeaseTTLSeconds) * time.Second,
				HeartbeatInterval: time.Duration(a.Cfg.Worker.WebhookHeartbeatIntervalSeconds) * time.Second,
			})
			return ww.Run(ctx)
		},
	}
}

func (a *App) metricsWorkerSpec() worker.WorkerSpec {
	return worker.WorkerSpec{
		Name:     "metrics",
		Critical: true,
		Run: func(ctx context.Context) error {
			opts := []metrics.CollectorOption{}
			if a.runtime.youtubeOAuthClientRegistry != nil {
				opts = append(opts, metrics.WithYouTubeOAuthPoolKeys(a.runtime.youtubeOAuthClientRegistry.Keys()))
			}
			return metrics.RunPeriodicCollector(ctx, a.DB, metrics.DefaultCollectorInterval, slog.Default(), opts...)
		},
	}
}

func (a *App) sessionsCleanupWorkerSpec() worker.WorkerSpec {
	return worker.WorkerSpec{
		Name:     "sessions_cleanup",
		Critical: true,
		Run: func(ctx context.Context) error {
			scw := worker.NewSessionsCleanupWorker(
				a.SessionsSvc,
				time.Duration(a.Cfg.Worker.SessionCleanupIntervalSeconds)*time.Second,
				slog.Default(),
			)
			return scw.Run(ctx)
		},
	}
}

func (a *App) assetCleanupWorkerSpec() worker.WorkerSpec {
	return worker.WorkerSpec{
		Name:     "asset_cleanup",
		Critical: false,
		Run: func(ctx context.Context) error {
			acw := worker.NewAssetCleanupWorker(
				repository.NewMediaAssetRepository(a.DB),
				time.Duration(a.Cfg.Worker.AssetCleanupIntervalSeconds)*time.Second,
				a.Cfg.Worker.VideoRetentionBufferDays,
				slog.Default(),
			)
			return acw.Run(ctx)
		},
	}
}

func (a *App) veloxDownloaderWorkerSpec() worker.WorkerSpec {
	return worker.WorkerSpec{
		Name:     "velox_downloader",
		Critical: true,
		Run: func(ctx context.Context) error {
			deliveryRepo := repository.NewExternalDeliveryRepository(a.DB)
			downloader := worker.NewVeloxArtifactDownloader(
				deliveryRepo,
				deliveryRepo,
				worker.NewIngestFSM(deliveryRepo, slog.Default()),
				repository.NewExternalDestinationRepository(a.DB),
				repository.NewWorkspaceRepository(a.DB),
				a.WorkerID,
				slog.Default(),
			)
			return downloader.Run(ctx)
		},
	}
}

func (a *App) uploadWorkerSpec() worker.WorkerSpec {
	return worker.WorkerSpec{
		Name:     "upload",
		Critical: true,
		Run: func(ctx context.Context) error {
			uploadOpts := worker.UploadWorkerOptions{
				IngestConcurrency:    a.Cfg.Worker.UploadIngestConcurrency,
				UploadConcurrency:    a.Cfg.Worker.YouTubeUploadConcurrency,
				LeaseTTL:             time.Duration(a.Cfg.Worker.UploadLeaseTTLSeconds) * time.Second,
				HeartbeatInterval:    time.Duration(a.Cfg.Worker.UploadHeartbeatIntervalSeconds) * time.Second,
				ReclaimInterval:      time.Duration(a.Cfg.Worker.UploadReclaimIntervalSeconds) * time.Second,
				ReclaimOnStart:       a.Cfg.Worker.UploadReclaimOnStart,
				EmptyQueueBackoffMin: time.Duration(a.Cfg.Worker.UploadEmptyQueueBackoffMinSeconds) * time.Second,
				EmptyQueueBackoffMax: time.Duration(a.Cfg.Worker.UploadEmptyQueueBackoffMaxSeconds) * time.Second,
				// Blocco #2 P0 — propagate the env-driven retention
				// buffer (default 7d) so processIngestJob's media_asset
				// create site uses the same value as the HTTP layer's
				// computeMediaAssetLifetime helper.
				VideoRetentionBufferDays: a.Cfg.Worker.VideoRetentionBufferDays,
			}
			sourceRegistry := worker.NewArtifactSourceRegistry()
			if provider, ok := a.CapRouter.Get("google-drive"); ok {
				if driveImporter, typeOK := provider.(services.DriveImporter); typeOK {
					if authDriveSrc, buildErr := worker.NewAuthenticatedDriveSource(driveImporter, a.Vault); buildErr == nil {
						if regErr := sourceRegistry.Register(authDriveSrc); regErr != nil {
							a.Logger.Error("upload worker: register authenticated drive source", "error", regErr)
						}
					} else {
						a.Logger.Error("upload worker: build authenticated drive source", "error", buildErr)
					}
				}
			}
			if regErr := sourceRegistry.Register(worker.NewVeloxSource(a.Logger, a.Cfg.Velox.VeloxAPIToken)); regErr != nil {
				a.Logger.Error("upload worker: register velox source", "error", regErr)
			}
			a.Logger.Info("upload worker: source registry built", "sources_registered", sourceRegistry.Names())

			uw := worker.NewUploadWorker(
				repository.NewUploadJobRepository(a.DB),
				repository.NewMediaAssetRepository(a.DB),
				repository.NewPostRepository(a.DB),
				repository.NewUserRepository(a.DB),
				a.StorageProvider,
				a.CapRouter,
				a.Vault,
				sourceRegistry,
				repository.NewExternalDeliveryRepository(a.DB),
				time.Duration(a.Cfg.Worker.UploadWorkerIntervalSeconds)*time.Second,
				slog.Default(),
				uploadOpts,
			)
			// Blocco #1 P0 — wire the per-target YouTube publication
			// store so processPublishJob's per-target private upload
			// phase can Create / MarkYouTubeUploaded / IncrementAttempt
			// on youtube_target_publications (migration 066). Constructed
			// here (post-construction of UploadWorker) so the setter
			// pattern keeps the constructor signature stable across
			// wires (production + tests).
			uw.SetYouTubeTargetPublishStore(a.runtime.youtubeTargetPublicationStore)
			uw.SetMediaDownloadResolver(a.runtime.mediaDownloadResolver)
			// Blocco delivery-queue — wire the post/target lookup surface the
			// global delivery pool needs to resolve a claimed (video,
			// channel) row back to its parent post + target.
			uw.SetYouTubeDeliveryPostStore(repository.NewPostRepository(a.DB))
			// Migration 092 — wire the ffprobe pass so every asset the
			// upload worker ingests gets duration/resolution/FPS/audio
			// probed (the live wizard's compatibility badge reads from
			// these columns). Best-effort by design: a missing ffprobe
			// binary or a probe error never fails the ingest.
			uw.SetMediaProber(worker.NewFFprobeProberWithRegistry(a.RenderRegistry))
			return uw.Run(ctx)
		},
	}
}

func (a *App) driveBatchCrawlerWorkerSpec() worker.WorkerSpec {
	return worker.WorkerSpec{
		Name:     "drive_batch_crawler",
		Critical: true,
		Run: func(ctx context.Context) error {
			crawlerOpts := worker.DriveBatchCrawlerOptions{
				ClaimInterval:     5 * time.Second,
				LeaseTTL:          5 * time.Minute,
				HeartbeatInterval: 100 * time.Second,
				ReclaimInterval:   30 * time.Second,
				ReclaimOnStart:    true,
				// Blocco #3 P0 — propagate the env-driven horizon
				// (default 30) so the crawler's D6 EXACT re-stamp
				// rejects batches whose projected cursor lands past
				// now + horizon. Mirrors r.publishHorizonDays() in
				// pkg/api/limits.go and handleDriveBatchImportV2's
				// worst-case producer check; the crawler applies the
				// REAL file count instead of the 10k-file heuristic.
				PublishHorizonDays: a.Cfg.Worker.PublishHorizonDays,
				PrepareLeadTime:    time.Duration(a.Cfg.Worker.UploadPrepareLeadMinutes) * time.Minute,
			}
			dbcc := worker.NewDriveBatchCrawler(
				repository.NewImportBatchRepository(a.DB),
				repository.NewUploadJobRepository(a.DB),
				a.Vault,
				a.CapRouter,
				"drive-batch-crawler",
				crawlerOpts,
				slog.Default(),
			)
			return dbcc.Run(ctx)
		},
	}
}

func (a *App) contentPreparationWorkerSpec() worker.WorkerSpec {
	return worker.WorkerSpec{
		Name:     "content_preparation",
		Critical: true,
		Run: func(ctx context.Context) error {
			preparation := worker.NewContentPreparationWorker(
				repository.NewContentPackageRepository(a.DB),
				repository.NewUploadJobRepository(a.DB),
				a.WorkerID+"-content-preparation",
				worker.ContentPreparationWorkerOptions{Interval: 10 * time.Second, LeaseTTL: 5 * time.Minute, BatchSize: 10},
				slog.Default(),
			)
			return preparation.Run(ctx)
		},
	}
}

func (a *App) driveInboxScannerWorkerSpec() worker.WorkerSpec {
	return worker.WorkerSpec{
		Name:     "drive_inbox_scanner",
		Critical: false,
		Run: func(ctx context.Context) error {
			scanner := worker.NewDriveInboxScanner(
				repository.NewDriveInboxRepository(a.DB),
				worker.NewDriveFolderDiscovery(a.Vault, a.CapRouter),
				worker.DriveInboxScannerOptions{Interval: 5 * time.Minute},
				slog.Default(),
			)
			return scanner.Run(ctx)
		},
	}
}

func (a *App) tokenRefreshSweepWorkerSpec() worker.WorkerSpec {
	return worker.WorkerSpec{
		Name:     "token_refresh_sweep",
		Critical: false,
		Run: func(ctx context.Context) error {
			// Refreshers are discovered via the OAuthProvider capability
			// (NOT a concrete type assertion): both YouTubeOAuthService
			// and GoogleDriveOAuthService satisfy it, and the router's
			// Register() type-asserts the capability at registration
			// time, so a provider-shape change surfaces at boot wiring
			// instead of silently leaving the refresher map empty.
			refreshers := map[string]credentials.TokenRefresher{}
			if oauth, ok := a.CapRouter.OAuth(models.PlatformYouTube); ok {
				refreshers[models.PlatformYouTube] = oauth.RefreshOAuthToken
			}
			if oauth, ok := a.CapRouter.OAuth(models.PlatformGoogleDrive); ok {
				refreshers[models.PlatformGoogleDrive] = oauth.RefreshOAuthToken
			}
			if len(refreshers) == 0 {
				// Not an error: a deployment without Google providers
				// simply idles. Logged so a MISWIRE (provider registered
				// but OAuth capability missing) is visible at boot.
				a.Logger.Info("token refresh sweep: no Google OAuth providers wired — sweep will idle")
			}
			sw := worker.NewTokenRefreshSweepWorker(
				repository.NewRefreshSweepRepository(a.DB),
				a.Vault,
				refreshers,
				time.Duration(a.Cfg.Worker.TokenRefreshSweepIntervalSeconds)*time.Second,
				a.Cfg.Worker.TokenRefreshSweepHorizonDays,
				slog.Default(),
			)
			return sw.Run(ctx)
		},
	}
}

func (a *App) snapshotRefreshSweepWorkerSpec() worker.WorkerSpec {
	return worker.WorkerSpec{
		Name:     "snapshot_refresh_sweep",
		Critical: false,
		Run: func(ctx context.Context) error {
			refreshers := map[string]credentials.TokenRefresher{}
			fetchers := map[string]worker.AccountDetailsFetcher{}
			for _, name := range a.CapRouter.Names() {
				if oauth, ok := a.CapRouter.OAuth(name); ok {
					refreshers[name] = oauth.RefreshOAuthToken
				}
				if dp, ok := a.CapRouter.AccountDetails(name); ok {
					fetchers[name] = dp
				}
			}
			if len(fetchers) == 0 {
				// Not an error: a deployment without AccountDetails
				// providers simply idles. Logged so a MISWIRE (provider
				// registered but details capability missing) is visible.
				a.Logger.Info("snapshot refresh sweep: no AccountDetails providers wired — sweep will idle")
			}
			sw := worker.NewSnapshotRefreshSweepWorker(
				repository.NewSnapshotRepository(a.DB),
				repository.NewAccountMetricsRepository(a.DB),
				a.Vault,
				refreshers,
				fetchers,
				time.Duration(a.Cfg.Worker.SnapshotRefreshSweepIntervalSeconds)*time.Second,
				slog.Default(),
			)
			return sw.Run(ctx)
		},
	}
}

func (a *App) youtubeProcessingReconcilerWorkerSpec() worker.WorkerSpec {
	return worker.WorkerSpec{
		Name:     "youtube_processing_reconciler",
		Critical: true,
		Run: func(ctx context.Context) error {
			ytPubRepo := a.runtime.youtubeTargetPublicationStore
			uploadRepo := repository.NewUploadJobRepository(a.DB)
			adapter := &routerEditorSessionAdapter{router: a.Router}
			ypr := worker.NewYoutubeProcessingReconciler(
				ytPubRepo,
				uploadRepo,
				adapter,
				worker.YoutubeProcessingReconcilerOptions{},
				a.Logger,
			)
			return ypr.Run(ctx)
		},
	}
}

func (a *App) youtubeCopyrightWorkerSpec() worker.WorkerSpec {
	return worker.WorkerSpec{
		Name: "youtube_copyright_checker", Critical: false,
		Run: func(ctx context.Context) error {
			cw := worker.NewYouTubeCopyrightWorker(
				a.runtime.youtubeTargetPublicationStore,
				repository.NewUserRepository(a.DB), a.CapRouter, a.Vault,
				time.Duration(a.Cfg.Worker.YouTubeCopyrightCheckIntervalSeconds)*time.Second,
				a.Logger,
			)
			return cw.Run(ctx)
		},
	}
}
