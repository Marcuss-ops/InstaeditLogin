package bootstrap

import (
	"context"
	"fmt"
	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/outbox"
	"github.com/Marcuss-ops/InstaeditLogin/internal/outbox/processors"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
	"github.com/Marcuss-ops/InstaeditLogin/internal/worker"
	"github.com/Marcuss-ops/InstaeditLogin/pkg/api"
	"github.com/Marcuss-ops/InstaeditLogin/pkg/metrics"
	"log/slog"
	"time"
)

// App is the wired runtime holding every dependency that the api and
// worker binaries share. cmd/api reads App.HTTPHandler (and App.Cfg for
// PORT); cmd/worker reads App.DB / App.Vault / App.CapRouter /
// App.WebhookRepo to construct and supervise the registered worker set.

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

// publishWorkerSpec wires goroutine 1: the publish worker driver —
// queued → publishing transition.
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
			return pw.Run(ctx)
		},
	}
}

// reconcileWorkerSpec wires goroutine 2: the reconcile worker —
// publishing → published | failed transition.
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
			return rw.Run(ctx)
		},
	}
}

// outboxWorkerSpec wires goroutine 3: the outbox dispatcher —
// materialises publish_jobs audit rows.
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

// webhookWorkerSpec wires goroutine 4: the webhook worker — drains
// webhook_deliveries.
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

// metricsWorkerSpec wires goroutine 5: the metrics collector —
// periodic gauges. When the YouTube OAuth Client Pool is configured
// (YOUTUBE_OAUTH_CLIENT_A/B_* env vars), the pool registry's client
// Keys() are passed to the collector so youtube_oauth_pool_health is
// zero-filled for every configured client (a client with no grants yet
// emits 0 / healthy instead of vanishing from /metrics).
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

// sessionsCleanupWorkerSpec wires goroutine 6: the sessions cleanup
// worker — retention policy.
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

// assetCleanupWorkerSpec wires the asset cleanup worker (Blocco
// Carosello cleanup) — hard-deletes media_assets whose YouTube publish
// pipeline has fully run AND aged past the operator-configured
// retention buffer (default 7d). NOT critical: a transient DB failure
// here MUST NOT take the process down.
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

// veloxDownloaderWorkerSpec wires goroutine 7: the Velox handoff
// consumer — polls external_deliveries for accepted rows and registers
// each claimed row as an upload_jobs row.
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

// uploadWorkerSpec wires goroutine 8: the upload worker —
// background import of public or authenticated Google Drive videos
// into S3 + posts + publish queue.
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

// driveBatchCrawlerWorkerSpec wires goroutine 9: the Drive batch
// crawler — drains import_batches rows.
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

// tokenRefreshSweepWorkerSpec wires goroutine 11: the token
// refresh sweep — renews dormant OAuth grants before Google
// garbage-collects them (~6-month inactivity policy). NON-critical:
// a transient failure here must not take the process down (maintenance
// task, same classification as asset_cleanup). Refreshers are wired
// only for the Google providers present in the CapabilityRouter; a
// deployment without YouTube/Drive simply runs an idle sweep.
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

// snapshotRefreshSweepWorkerSpec wires the snapshot refresh sweep:
// the background half of the strict rule "opening a channel page never
// calls the provider". The read path (GET /accounts and GET
// /accounts/{id}) serves the cached snapshot and stamps
// refresh_pending_at; this worker drains those rows and refreshes the
// snapshots asynchronously with bounded concurrency (4). NON-critical:
// a transient failure here must not take the process down (same
// classification as asset_cleanup / token_refresh_sweep).
//
// Fetchers are discovered via the AccountDetails capability (NOT a
// concrete type assertion): the router's Register() type-asserts at
// registration time, so a provider-shape change surfaces at boot
// wiring instead of silently leaving the fetcher map empty. Refreshers
// reuse the OAuth capability map, exactly like the token refresh sweep.
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

// youtubeProcessingReconcilerWorkerSpec wires goroutine 10: the YouTube
// processing reconciler — polls youtube_target_publications rows in
// 'processed' state that haven't been linked to an editor session
// (editor_session_id IS NULL) and creates the per-target Velox editor
// session via Router.CreateEditorSession through the
// routerEditorSessionAdapter. 1-per-target contract preserved (every
// call mints fresh uuids). MarkEditorSessionCreated's atomic CAS-link
// guards concurrent reconciler replicas from double-stamping.
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

// workerSpecs is the single lifecycle plan. It deliberately returns the
// existing worker.WorkerSpec values rather than introducing another registry:
// RunWorkers registers these specs on App.WorkerRegistry in this exact order.
func (a *App) workerSpecs() []worker.WorkerSpec {
	return []worker.WorkerSpec{
		a.publishWorkerSpec(),
		a.reconcileWorkerSpec(),
		a.outboxWorkerSpec(),
		a.webhookWorkerSpec(),
		a.metricsWorkerSpec(),
		a.sessionsCleanupWorkerSpec(),
		a.assetCleanupWorkerSpec(),
		a.veloxDownloaderWorkerSpec(),
		a.uploadWorkerSpec(),
		a.driveBatchCrawlerWorkerSpec(),
		a.youtubeProcessingReconcilerWorkerSpec(),
		a.tokenRefreshSweepWorkerSpec(),
		a.snapshotRefreshSweepWorkerSpec(),
	}
}

// RunWorkers starts the 13 background workers (publish, reconcile,
// outbox, webhook, metrics, sessions_cleanup, asset_cleanup,
// velox_downloader, upload, drive_batch_crawler,
// youtube_processing_reconciler, token_refresh_sweep,
// snapshot_refresh_sweep) under the shared WorkerRegistry. The registry handles startup, heartbeat
// tracking, supervision, logging, and shutdown. A critical worker
// that exits with a non-context error aborts the whole process by
// returning the error from RunWorkers.
//
// The per-worker construction closures live in the *WorkerSpec factories
// above (one per goroutine, in registration order) so this function
// stays a thin orchestration loop: register all → StartAll → block on
// critical-error-or-cancel → StopAll with the shared 15s drain budget.
//
// On cancellation it cancels every goroutine concurrently and waits
// up to 15s total for their Run loops to drain gracefully.
func (a *App) RunWorkers(ctx context.Context) error {
	if a.WorkerRegistry == nil {
		return fmt.Errorf("RunWorkers called with nil App.WorkerRegistry")
	}
	if _, err := a.requireRuntime(); err != nil {
		return fmt.Errorf("RunWorkers capability validation failed: %w", err)
	}

	// The plan is the single source of truth for worker count, order and
	// criticality. Each spec still contains a lazy Run closure; no worker
	// dependency is constructed before StartAll invokes it.
	for _, spec := range a.workerSpecs() {
		a.WorkerRegistry.Register(spec)
	}

	slog.Info("13 background workers registered: publish / reconcile / outbox / webhook / metrics / sessions_cleanup / asset_cleanup / velox_downloader / upload / drive_batch_crawler / youtube_processing_reconciler / token_refresh_sweep / snapshot_refresh_sweep")

	criticalErrCh := a.WorkerRegistry.StartAll(ctx)

	var criticalErr error
	select {
	case criticalErr = <-criticalErrCh:
		if criticalErr != nil {
			slog.Error("critical worker exited unexpectedly", "error", criticalErr)
		}
	case <-ctx.Done():
		slog.Info("context cancelled, broadcasting shutdown to all workers")
		// A critical worker may report just as cancellation arrives. Give
		// the buffered error channel one non-blocking turn before declaring
		// this a clean cancellation, so the failure reaches the caller.
		select {
		case criticalErr = <-criticalErrCh:
		default:
		}
	}

	shutdownErr := a.WorkerRegistry.StopAll(15 * time.Second)
	if criticalErr == nil {
		// StopAll waits for every supervisor, so any critical failure that
		// raced the cancellation has now had a chance to reach the buffer.
		select {
		case criticalErr = <-criticalErrCh:
		default:
		}
	}
	if criticalErr != nil {
		if a.OneTimeCodes != nil {
			a.OneTimeCodes.Stop()
		}
		return criticalErr
	}
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
