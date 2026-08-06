//go:build legacy_bootstrap

// This legacy worker wiring is kept only as a reference. The canonical
// self-hosted runtime is wired by workers_wiring.go; compiling both files
// would mix the old flat Config with the current nested configuration.

package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/outbox"
	"github.com/Marcuss-ops/InstaeditLogin/internal/outbox/processors"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
	"github.com/Marcuss-ops/InstaeditLogin/internal/worker"
	"github.com/Marcuss-ops/InstaeditLogin/pkg/metrics"
)

// WireWorkers builds the worker registry with all background goroutines
// wired to the shared Core. The returned registry is not started; callers
// must invoke StartAll (and StopAll for graceful shutdown) or use
// RunWorkers.
func WireWorkers(core *Core) *worker.Registry {
	registry := worker.NewRegistry()

	// 1. Publish worker driver — queued → publishing transition
	registry.Register(worker.WorkerSpec{
		Name:     "publish",
		Critical: true,
		Run: func(ctx context.Context) error {
			// MediaDownloadResolver (migration 080 followup): mint a
			// fresh presigned GET URL at publish time so the per-platform
			// API call sees a valid signature. Mirrors workers_wiring.go.
			mediaAssetRepoForResolver := repository.NewMediaAssetRepository(core.DB)
			resolver := services.NewMediaDownloadResolver(
				core.Storage,
				assetsAdapter{repo: mediaAssetRepoForResolver},
				slog.Default(),
			)
			pw := worker.NewPublishWorker(
				repository.NewPostRepository(core.DB),
				repository.NewUserRepository(core.DB),
				core.CapRouter,
				core.Vault,
				resolver,
				core.WorkerID,
				core.MemoryLimiter,
				time.Duration(core.Cfg.Worker.PublishWorkerIntervalSeconds)*time.Second,
				slog.Default(),
			)
			deliveryRegistry := services.NewDeliveryRegistry()
			if ytPub, ok := core.CapRouter.Publisher(models.PlatformYouTube); ok {
				_ = deliveryRegistry.Register(services.NewYouTubeDeliveryAdapter(ytPub))
			}
			if core.Cfg.Auth.GoogleDriveClientID != "" && core.Cfg.Auth.GoogleDriveClientSecret != "" {
				driveSessionRepo := repository.NewDeliverySessionRepository(core.DB)
				var googleDriveOAuth *services.GoogleDriveOAuthService
				if gd, ok := core.CapRouter.Get(models.PlatformGoogleDrive); ok {
					if gdOAuth, typeOK := gd.(*services.GoogleDriveOAuthService); typeOK {
						googleDriveOAuth = gdOAuth
					}
				}
				if googleDriveOAuth != nil {
					driveVault, vaultOK := core.Vault.(services.DriveTokenVault)
					if !vaultOK {
						return fmt.Errorf("publish worker: credential vault lacks Drive refresh-token capability")
					}
					driveTokenProvider := services.NewDriveVaultTokenProvider(driveVault, googleDriveOAuth)
					driveDest, destErr := services.NewGoogleDriveDestination(
						driveSessionRepo,
						driveTokenProvider,
						core.Encryptor,
						&http.Client{Timeout: 30 * time.Second},
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
			return pw.Run(ctx)
		},
	})

	// 2. Reconcile worker — publishing → published | failed transition
	registry.Register(worker.WorkerSpec{
		Name:     "reconcile",
		Critical: true,
		Run: func(ctx context.Context) error {
			rw := worker.NewReconcileWorker(
				repository.NewPostRepository(core.DB),
				repository.NewUserRepository(core.DB),
				core.CapRouter,
				core.Vault,
				core.WorkerID,
				core.MemoryLimiter,
				time.Duration(core.Cfg.Worker.ReconcileWorkerIntervalSeconds)*time.Second,
				slog.Default(),
			)
			return rw.Run(ctx)
		},
	})

	// 3. Outbox dispatcher — materialises publish_jobs audit rows
	registry.Register(worker.WorkerSpec{
		Name:     "outbox",
		Critical: true,
		Run: func(ctx context.Context) error {
			ds := outbox.NewDispatcher(outbox.DispatcherConfig{
				OutboxStore:  repository.NewOutboxRepository(core.DB),
				Process:      processors.NewPublishJobsMaterialiser(core.DB),
				Logger:       slog.Default(),
				TickInterval: outbox.DefaultTickInterval,
			})
			return ds.Run(ctx)
		},
	})

	// 4. Webhook worker — drains webhook_deliveries
	registry.Register(worker.WorkerSpec{
		Name:     "webhook",
		Critical: true,
		Run: func(ctx context.Context) error {
			ww := worker.NewWebhookWorker(core.WebhookRepo, time.Duration(core.Cfg.Worker.WebhookWorkerIntervalSeconds)*time.Second)
			return ww.Run(ctx)
		},
	})

	// 5. Metrics collector — periodic gauges
	registry.Register(worker.WorkerSpec{
		Name:     "metrics",
		Critical: true,
		Run: func(ctx context.Context) error {
			opts := []metrics.CollectorOption{}
			if poolRegistry, regErr := services.NewYouTubeOAuthClientRegistryFromConfig(core.Cfg); regErr != nil {
				slog.Warn("metrics collector: youtube oauth pool registry unavailable; pool health zero-fill skipped", "error", regErr)
			} else if poolRegistry != nil {
				opts = append(opts, metrics.WithYouTubeOAuthPoolKeys(poolRegistry.Keys()))
			}
			return metrics.RunPeriodicCollector(ctx, core.DB, metrics.DefaultCollectorInterval, slog.Default(), opts...)
		},
	})

	// 6. Sessions cleanup worker — retention policy
	registry.Register(worker.WorkerSpec{
		Name:     "sessions_cleanup",
		Critical: true,
		Run: func(ctx context.Context) error {
			scw := worker.NewSessionsCleanupWorker(
				core.sessionsSvc,
				time.Duration(core.Cfg.Worker.SessionCleanupIntervalSeconds)*time.Second,
				slog.Default(),
			)
			return scw.Run(ctx)
		},
	})

	// 7. Velox handoff consumer — polls external_deliveries for accepted
	// rows and registers each claimed row as an upload_jobs row.
	registry.Register(worker.WorkerSpec{
		Name:     "velox_downloader",
		Critical: true,
		Run: func(ctx context.Context) error {
			deliveryRepo := repository.NewExternalDeliveryRepository(core.DB)
			downloader := worker.NewVeloxArtifactDownloader(
				deliveryRepo,
				deliveryRepo,
				worker.NewIngestFSM(deliveryRepo, slog.Default()),
				repository.NewExternalDestinationRepository(core.DB),
				repository.NewWorkspaceRepository(core.DB),
				core.WorkerID,
				slog.Default(),
			)
			return downloader.Run(ctx)
		},
	})

	// 8. Upload worker — background import of public or authenticated
	// Google Drive videos into S3 + posts + publish queue.
	registry.Register(worker.WorkerSpec{
		Name:     "upload",
		Critical: true,
		Run: func(ctx context.Context) error {
			uploadOpts := worker.UploadWorkerOptions{
				IngestConcurrency:    core.Cfg.Worker.UploadIngestConcurrency,
				UploadConcurrency:    core.Cfg.Worker.YouTubeUploadConcurrency,
				LeaseTTL:             time.Duration(core.Cfg.Worker.UploadLeaseTTLSeconds) * time.Second,
				HeartbeatInterval:    time.Duration(core.Cfg.Worker.UploadHeartbeatIntervalSeconds) * time.Second,
				ReclaimInterval:      time.Duration(core.Cfg.Worker.UploadReclaimIntervalSeconds) * time.Second,
				ReclaimOnStart:       core.Cfg.Worker.UploadReclaimOnStart,
				EmptyQueueBackoffMin: time.Duration(core.Cfg.Worker.UploadEmptyQueueBackoffMinSeconds) * time.Second,
				EmptyQueueBackoffMax: time.Duration(core.Cfg.Worker.UploadEmptyQueueBackoffMaxSeconds) * time.Second,
			}
			sourceRegistry := worker.NewArtifactSourceRegistry()
			if provider, ok := core.CapRouter.Get("google-drive"); ok {
				if driveImporter, typeOK := provider.(services.DriveImporter); typeOK {
					if authDriveSrc, buildErr := worker.NewAuthenticatedDriveSource(driveImporter, core.Vault); buildErr == nil {
						if regErr := sourceRegistry.Register(authDriveSrc); regErr != nil {
							core.Logger.Error("upload worker: register authenticated drive source", "error", regErr)
						}
					} else {
						core.Logger.Error("upload worker: build authenticated drive source", "error", buildErr)
					}
				}
			}
			if regErr := sourceRegistry.Register(worker.NewVeloxSource(core.Logger, core.Cfg.Velox.VeloxAPIToken)); regErr != nil {
				core.Logger.Error("upload worker: register velox source", "error", regErr)
			}
			core.Logger.Info("upload worker: source registry built", "sources_registered", sourceRegistry.Names())

			uw := worker.NewUploadWorker(
				repository.NewUploadJobRepository(core.DB),
				repository.NewMediaAssetRepository(core.DB),
				repository.NewPostRepository(core.DB),
				repository.NewUserRepository(core.DB),
				core.Storage,
				core.CapRouter,
				core.Vault,
				sourceRegistry,
				repository.NewExternalDeliveryRepository(core.DB),
				time.Duration(core.Cfg.Worker.UploadWorkerIntervalSeconds)*time.Second,
				slog.Default(),
				uploadOpts,
			)
			return uw.Run(ctx)
		},
	})

	// 9. Drive batch crawler — drains import_batches rows
	registry.Register(worker.WorkerSpec{
		Name:     "drive_batch_crawler",
		Critical: true,
		Run: func(ctx context.Context) error {
			crawlerOpts := worker.DriveBatchCrawlerOptions{
				ClaimInterval:     5 * time.Second,
				LeaseTTL:          5 * time.Minute,
				HeartbeatInterval: 100 * time.Second,
				ReclaimInterval:   30 * time.Second,
				ReclaimOnStart:    true,
			}
			dbcc := worker.NewDriveBatchCrawler(
				repository.NewImportBatchRepository(core.DB),
				repository.NewUploadJobRepository(core.DB),
				core.Vault,
				core.CapRouter,
				"drive-batch-crawler",
				crawlerOpts,
				slog.Default(),
			)
			return dbcc.Run(ctx)
		},
	})

	slog.Info("9 background workers registered: publish / reconcile / outbox / webhook / metrics / sessions_cleanup / velox_downloader / upload / drive_batch_crawler")

	return registry
}

// RunWorkers starts the registered workers and blocks until the context
// is cancelled or a critical worker exits unexpectedly. It stops the
// OneTimeCodeStore sweeper on the way out.
func RunWorkers(ctx context.Context, core *Core, registry *worker.Registry) error {
	if registry == nil {
		return fmt.Errorf("RunWorkers called with nil registry")
	}

	criticalErrCh := registry.StartAll(ctx)

	select {
	case err := <-criticalErrCh:
		if err != nil {
			slog.Error("critical worker exited unexpectedly", "error", err)
			registry.StopAll(15 * time.Second)
			if core.OneTimeCodes != nil {
				core.OneTimeCodes.Stop()
			}
			return err
		}
	case <-ctx.Done():
		slog.Info("context cancelled, broadcasting shutdown to all workers")
	}

	shutdownErr := registry.StopAll(15 * time.Second)
	if shutdownErr != nil {
		slog.Warn("worker shutdown did not complete cleanly", "error", shutdownErr)
	} else {
		slog.Info("all background workers drained")
	}

	if core.OneTimeCodes != nil {
		core.OneTimeCodes.Stop()
		slog.Info("OneTimeCodeStore sweeper stopped")
	}

	return shutdownErr
}

// RegisterWorkerMetrics registers the worker registry as a Prometheus
// collector. It is safe to call multiple times; subsequent calls are
// no-ops. Callers that expose /metrics (cmd/worker and cmd/server)
// should invoke this before bootstrap.StartMetricsServer so the
// worker_state metric is available from the first scrape.
func RegisterWorkerMetrics(registry *worker.Registry) error {
	if registry == nil {
		return nil
	}
	if err := prometheus.Register(registry); err != nil {
		var already prometheus.AlreadyRegisteredError
		if !errors.As(err, &already) {
			return err
		}
	}
	return nil
}
