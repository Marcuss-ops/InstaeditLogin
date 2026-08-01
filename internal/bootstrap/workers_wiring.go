package bootstrap

import (
	"context"
	"fmt"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/outbox"
	"github.com/Marcuss-ops/InstaeditLogin/internal/outbox/processors"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
	"github.com/Marcuss-ops/InstaeditLogin/internal/worker"
	"github.com/Marcuss-ops/InstaeditLogin/pkg/api"
	"github.com/Marcuss-ops/InstaeditLogin/pkg/metrics"
	"log/slog"
	"net/http"
	"time"
)

// App is the wired runtime holding every dependency that the api and
// worker binaries share. cmd/api reads App.HTTPHandler (and App.Cfg for
// PORT); cmd/worker reads App.DB / App.Vault / App.CapRouter /
// App.WebhookRepo to construct + supervise the 7 goroutines; cmd/server
// (the wrapper) reads both halves.

// assetsAdapter bridges *repository.MediaAssetRepository to the
// resolver's services.MediaAssetStore interface (which takes a ctx
// first arg). The repo's FindByID is a sync lookup that doesn't
// take a ctx; the adapter accepts the ctx to keep the resolver API
// future-proof (when the repo upgrades to ctx-aware queries the
// adapter can forward ctx without changing the wiring site).
// (P3 — migration 080 followup; touch only RunWorkers + this type.)
type assetsAdapter struct {
	repo *repository.MediaAssetRepository
}

func (a assetsAdapter) FindForPost(ctx context.Context, workspaceID int64, assetID, bucket, key string) (*models.MediaAsset, error) {
	return a.repo.FindForPost(ctx, workspaceID, assetID, bucket, key)
}

// FindByUploadKey is the bridge for the resolver's legacy URL fallback.
// The repository applies the same workspace ownership predicate as the
// canonical asset-id path, so legacy media_url rows cannot bypass tenant
// isolation.
func (a assetsAdapter) FindByUploadKey(ctx context.Context, workspaceID int64, key string) (*models.MediaAsset, error) {
	return a.repo.FindByUploadKey(ctx, workspaceID, key)
}

// routerEditorSessionAdapter bridges worker.EditorSessionCreatorInput
// (internal/worker) → api.CreateEditorSessionInput (pkg/api). The two
// structs are deliberately different so worker (which pkg/api must
// not import) doesn't cycle back through pkg/api. Adapter pattern
// keeps the bridge in one place; the reconciler goroutine calls
// routerEditorSessionAdapter.CreateEditorSession(...) which hands
// off to Router.CreateEditorSession (the shared per-target 1:1
// helper that mints fresh uuids and validates workspace + account +
// channel + token + video-state invariants).
//
// Compile-time assertion in pkg/api/youtube_editor_sessions.go
// confirms *api.Router satisfies the predicate
// "CreateEditorSession(context.Context, CreateEditorSessionInput)
// (*models.YouTubeVideoEdit, error)". This adapter enforces field-
// by-field struct identity at runtime via Go's struct-literal type
// checking.
type routerEditorSessionAdapter struct {
	router *api.Router
}

// CreateEditorSession forwards to Router.CreateEditorSession. All
// sentinel errors propagate untouched so the reconciler can branch
// on errors.Is for retry/skip decisions (see writeEditorSessionError
// for the HTTP-side mapping).
func (a *routerEditorSessionAdapter) CreateEditorSession(ctx context.Context, in worker.EditorSessionCreatorInput) (*models.YouTubeVideoEdit, error) {
	return a.router.CreateEditorSession(ctx, api.CreateEditorSessionInput{
		WorkspaceID:        in.WorkspaceID,
		PlatformAccountID:  in.PlatformAccountID,
		YouTubeVideoID:     in.YouTubeVideoID,
		SourceThumbnailURL: in.SourceThumbnailURL,
	})
}

// RunWorkers starts the 9 background goroutines (publish, reconcile,
// outbox, webhook, metrics, sessions_cleanup, velox_downloader,
// upload, drive_batch_crawler) under a shared WorkerRegistry. The
// registry handles startup, heartbeat tracking, supervision, logging,
// and shutdown. A critical worker that exits with a non-context error
// aborts the whole process by returning the error from RunWorkers.
//
// On cancellation it cancels every goroutine concurrently and waits
// up to 15s total for their Run loops to drain gracefully.
func (a *App) RunWorkers(ctx context.Context) error {
	if a.WorkerRegistry == nil {
		return fmt.Errorf("RunWorkers called with nil App.WorkerRegistry")
	}

	// 1. Publish worker driver — queued → publishing transition
	a.WorkerRegistry.Register(worker.WorkerSpec{
		Name:     "publish",
		Critical: true,
		Run: func(ctx context.Context) error {
			// MediaDownloadResolver (migration 080 followup): wire the
			// fresh-presigned-URL sign path here so executePublish mints
			// per-call signatures immediately before the platform API call.
			// assetsAdapter wraps *repository.MediaAssetRepository and applies
			// workspace-scoped ownership before the resolver mints a URL. The
			// adapter is function-local to keep the wiring change contained.
			mediaAssetRepoForResolver := repository.NewMediaAssetRepository(a.DB)
			resolver := services.NewMediaDownloadResolver(
				a.StorageProvider,
				assetsAdapter{repo: mediaAssetRepoForResolver},
				slog.Default(),
			)
			pw := worker.NewPublishWorker(
				repository.NewPostRepository(a.DB),
				repository.NewUserRepository(a.DB),
				a.CapRouter,
				a.Vault,
				resolver,
				a.WorkerID,
				a.MemoryLimiter,
				time.Duration(a.Cfg.Worker.PublishWorkerIntervalSeconds)*time.Second,
				slog.Default(),
			)
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
			// Blocco #1 P0 (P1 Migration 077 followup) — wire the
			// YouTube target publication lookup so PublishWorker.publishTarget's
			// Phase-2 bypass block can FindByPostTargetID the
			// Phase-1 stamped youtube_video_id and skip the fresh
			// videos.insert when status="youtube_uploaded". The
			// same *repository.YouTubeTargetPublicationRepository
			// the upload worker uses — the youtube_target_publications
			// table is the contract boundary for the Blocco #1
			// two-phase split (Phase-1 stamps, Phase-2 reads + videos.update).
			pw.SetYouTubeTargetPublicationStore(repository.NewYouTubeTargetPublicationRepository(a.DB))
			return pw.Run(ctx)
		},
	})

	// 2. Reconcile worker — publishing → published | failed transition
	a.WorkerRegistry.Register(worker.WorkerSpec{
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
			return rw.Run(ctx)
		},
	})

	// 3. Outbox dispatcher — materialises publish_jobs audit rows
	a.WorkerRegistry.Register(worker.WorkerSpec{
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
	})

	// 4. Webhook worker — drains webhook_deliveries
	a.WorkerRegistry.Register(worker.WorkerSpec{
		Name:     "webhook",
		Critical: true,
		Run: func(ctx context.Context) error {
			ww := worker.NewWebhookWorker(a.WebhookRepo, time.Duration(a.Cfg.Worker.WebhookWorkerIntervalSeconds)*time.Second)
			return ww.Run(ctx)
		},
	})

	// 5. Metrics collector — periodic gauges
	a.WorkerRegistry.Register(worker.WorkerSpec{
		Name:     "metrics",
		Critical: true,
		Run: func(ctx context.Context) error {
			return metrics.RunPeriodicCollector(ctx, a.DB, metrics.DefaultCollectorInterval, slog.Default())
		},
	})

	// 6. Sessions cleanup worker — retention policy
	a.WorkerRegistry.Register(worker.WorkerSpec{
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
	})

	// 11. Asset cleanup worker (Blocco Carosello cleanup) -- hard-deletes media_assets whose YouTube
	// publish pipeline has fully run AND aged past the operator-
	// configured retention buffer (default 7d). NOT critical: a
	// transient DB failure here MUST NOT take the process down.
	a.WorkerRegistry.Register(worker.WorkerSpec{
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
	})

	// 7. Velox handoff consumer — polls external_deliveries for accepted
	// rows and registers each claimed row as an upload_jobs row.
	a.WorkerRegistry.Register(worker.WorkerSpec{
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
	})

	// 8. Upload worker — background import of public or authenticated
	// Google Drive videos into S3 + posts + publish queue.
	a.WorkerRegistry.Register(worker.WorkerSpec{
		Name:     "upload",
		Critical: true,
		Run: func(ctx context.Context) error {
			uploadOpts := worker.UploadWorkerOptions{
				IngestConcurrency: a.Cfg.Worker.UploadIngestConcurrency,
				UploadConcurrency: a.Cfg.Worker.YouTubeUploadConcurrency,
				LeaseTTL:          time.Duration(a.Cfg.Worker.UploadLeaseTTLSeconds) * time.Second,
				HeartbeatInterval: time.Duration(a.Cfg.Worker.UploadHeartbeatIntervalSeconds) * time.Second,
				ReclaimInterval:   time.Duration(a.Cfg.Worker.UploadReclaimIntervalSeconds) * time.Second,
				ReclaimOnStart:    a.Cfg.Worker.UploadReclaimOnStart,
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
			uw.SetYouTubeTargetPublishStore(repository.NewYouTubeTargetPublicationRepository(a.DB))
			mediaAssetRepoForResolver := repository.NewMediaAssetRepository(a.DB)
			uw.SetMediaDownloadResolver(services.NewMediaDownloadResolver(
				a.StorageProvider,
				assetsAdapter{repo: mediaAssetRepoForResolver},
				slog.Default(),
			))
			return uw.Run(ctx)
		},
	})

	// 9. Drive batch crawler — drains import_batches rows
	a.WorkerRegistry.Register(worker.WorkerSpec{
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
	})

	// 10. YouTube processing reconciler — polls
	// youtube_target_publications rows in 'processed' state that
	// haven't been linked to an editor session (editor_session_id IS
	// NULL) and creates the per-target Velox editor session via
	// Router.CreateEditorSession through the routerEditorSessionAdapter.
	// 1-per-target contract preserved (every call mints fresh uuids).
	// MarkEditorSessionCreated's atomic CAS-link guards concurrent
	// reconciler replicas from double-stamping.
	a.WorkerRegistry.Register(worker.WorkerSpec{
		Name:     "youtube_processing_reconciler",
		Critical: true,
		Run: func(ctx context.Context) error {
			ytPubRepo := repository.NewYouTubeTargetPublicationRepository(a.DB)
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
	})

	slog.Info("10 background workers registered: publish / reconcile / outbox / webhook / metrics / sessions_cleanup / velox_downloader / upload / drive_batch_crawler / youtube_processing_reconciler")

	criticalErrCh := a.WorkerRegistry.StartAll(ctx)

	select {
	case err := <-criticalErrCh:
		if err != nil {
			slog.Error("critical worker exited unexpectedly", "error", err)
			a.WorkerRegistry.StopAll(15 * time.Second)
			if a.OneTimeCodes != nil {
				a.OneTimeCodes.Stop()
			}
			return err
		}
	case <-ctx.Done():
		slog.Info("context cancelled, broadcasting shutdown to all workers")
	}

	shutdownErr := a.WorkerRegistry.StopAll(15 * time.Second)
	if shutdownErr != nil {
		slog.Warn("worker shutdown did not complete cleanly", "error", shutdownErr)
	} else {
		slog.Info("all background workers drained")
	}

	if a.OneTimeCodes != nil {
		a.OneTimeCodes.Stop()
		slog.Info("OneTimeCodeStore sweeper stopped")
	}

	return shutdownErr
}
