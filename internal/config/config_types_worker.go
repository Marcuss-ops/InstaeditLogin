package config

// WorkerConfig holds background-worker tuning parameters.
type WorkerConfig struct {
	// PublishWorkerIntervalSeconds is the cadence of the publish worker.
	PublishWorkerIntervalSeconds int
	// ReconcileWorkerIntervalSeconds is the cadence of the reconcile worker.
	ReconcileWorkerIntervalSeconds int
	// WebhookWorkerIntervalSeconds is the cadence of the webhook worker.
	WebhookWorkerIntervalSeconds int
	// WebhookWorkerConcurrency caps concurrent HTTP deliveries per replica.
	WebhookWorkerConcurrency int
	// WebhookHTTPTimeoutSeconds bounds one outbound webhook request.
	WebhookHTTPTimeoutSeconds int
	// WebhookLeaseTTLSeconds must exceed the HTTP timeout with margin.
	WebhookLeaseTTLSeconds int
	// WebhookHeartbeatIntervalSeconds renews active delivery leases.
	WebhookHeartbeatIntervalSeconds int
	// SessionCleanupIntervalSeconds is the cadence of the sessions cleanup worker.
	SessionCleanupIntervalSeconds int
	// AssetCleanupIntervalSeconds is the cadence of the media-asset
	// cleanup worker. Drives the periodic AssetCleanupWorker that
	// DELETEs rows from media_assets whose YouTube publish + post
	// pipeline has fully completed AND aged past
	// VideoRetentionBufferDays. Cadence is intentionally coarse
	// (default 86400s = 24h) because the cleanup predicate is
	// multi-table and benefits from a snapshot read; under typical
	// load a daily sweep keeps the S3 footprint bounded without
	// thrashing Postgres with DELETE/.../USING queries. Operators
	// wanting more aggressive space reclamation can lower to e.g.
	// 3600 (hourly) — set ASSET_CLEANUP_INTERVAL_SECONDS.
	AssetCleanupIntervalSeconds int
	// UploadWorkerIntervalSeconds is the cadence of the upload worker.
	UploadWorkerIntervalSeconds int
	// RenderMaxConcurrency is the global process limit for ffmpeg/ffprobe
	// and future CPU-heavy media renders. It is intentionally independent
	// from upload goroutine counts.
	RenderMaxConcurrency int
	// FFmpegThreads is the explicit per-process thread budget reserved for
	// future ffmpeg commands admitted by the render registry. ffprobe uses
	// the same process budget but does not support ffmpeg's -threads flag.
	FFmpegThreads int
	// UploadIngestConcurrency is the number of ingest goroutines.
	UploadIngestConcurrency int
	// YouTubeUploadConcurrency is the number of YouTube upload goroutines.
	YouTubeUploadConcurrency int
	// UploadLeaseTTLSeconds is the lease TTL for upload jobs.
	UploadLeaseTTLSeconds int
	// UploadHeartbeatIntervalSeconds is the heartbeat interval for upload jobs.
	UploadHeartbeatIntervalSeconds int
	// UploadReclaimIntervalSeconds is the reclaim interval for stale uploads.
	UploadReclaimIntervalSeconds int
	// UploadReclaimOnStart controls whether stale uploads are reclaimed at startup.
	UploadReclaimOnStart bool
	// UploadEmptyQueueBackoffMinSeconds is the initial delay after an
	// empty upload queue claim. Defaults to 1 second.
	UploadEmptyQueueBackoffMinSeconds int
	// UploadEmptyQueueBackoffMaxSeconds caps the empty-queue backoff.
	// Defaults to 30 seconds.
	UploadEmptyQueueBackoffMaxSeconds int
	// YouTubeUploadChunkBytes is the resumable upload chunk size.
	YouTubeUploadChunkBytes int64
	// YouTubeUploadMaxRetries is the per-chunk PUT retry budget.
	YouTubeUploadMaxRetries int
	// YouTubeUploadBackoffBaseMs is the base backoff in milliseconds.
	YouTubeUploadBackoffBaseMs int
	// YouTubeUploadBackoffCapMs is the backoff cap in milliseconds.
	YouTubeUploadBackoffCapMs int
	// YouTubeDailyQuotaLimit is the daily videos.insert quota cap.
	YouTubeDailyQuotaLimit int
	// YouTubeGroupVideosMaxAccounts caps group-video fan-out size.
	YouTubeGroupVideosMaxAccounts int
	// YouTubeGroupVideosMaxVideos caps the aggregate group-video projection.
	YouTubeGroupVideosMaxVideos int
	// YouTubeGroupVideosCacheTTLSeconds controls the short-lived per-account cache.
	YouTubeGroupVideosCacheTTLSeconds int
	// YouTubeGroupVideosDefaultPageSize is the default response page size.
	YouTubeGroupVideosDefaultPageSize int
	// TokenRefreshSweepIntervalSeconds is the cadence of the token
	// refresh sweep worker — renews dormant OAuth grants (last
	// refresh older than TokenRefreshSweepHorizonDays, or provider
	// TTL within 7 days) so Google's ~6-month refresh-token
	// inactivity garbage collection never kills a rarely-publishing
	// channel. Default 900 (15m): access tokens expire hourly and are
	// proactively renewed before they become unusable. Env
	// TOKEN_REFRESH_SWEEP_INTERVAL_SECONDS.
	TokenRefreshSweepIntervalSeconds int
	// TokenRefreshSweepHorizonDays is the inactivity lookahead: a
	// grant whose last_refresh_at (or created_at when never
	// refreshed) is older than this is renewed. Default 120 (~4
	// months — 2 months of margin under Google's 6-month GC). Env
	// TOKEN_REFRESH_SWEEP_HORIZON_DAYS.
	TokenRefreshSweepHorizonDays int
	// SnapshotRefreshSweepIntervalSeconds is the cadence of the
	// snapshot refresh sweep worker — drains accounts whose cached
	// resource snapshot is stale (refresh_pending_at stamped by the
	// read path serving a cached snapshot) and refreshes them in the
	// background with bounded concurrency (4), so opening a channel
	// page NEVER triggers a provider call (strict rule). Default 60s:
	// a page load stamps pending and the worker refreshes within a
	// minute. Env SNAPSHOT_REFRESH_SWEEP_INTERVAL_SECONDS.
	SnapshotRefreshSweepIntervalSeconds int
	// PublishHorizonDays (Blocco #2 P0) caps how far in the future a
	// user/operator can schedule a publish. Used by:
	//   - uploads_handlers.go::handleRescheduleUpload (drag-drop reject
	//     when publish_at > now+horizon),
	//   - drive_batch_v2_handlers.go::handleDriveBatchImportV2 (HARD 422
	//     when the projected worst-case horizon > cap),
	//   - the /api/v1/health response (so the SPA can render the cap).
	// Default 30 = env PUBLISH_HORIZON_DAYS. Operators wanting a longer
	// horizon (e.g. annual content calendars) bump this without a redeploy.
	PublishHorizonDays int
	// VideoRetentionBufferDays (Blocco #2 P0) is the post-publish tail
	// for media_assets.expires_at. Formula:
	//   - with publish_at: max(now+1d, publish_at + buffer)
	//   - without publish_at: now + PublishHorizonDays
	// The 1-day min-floor keeps a slow uploader from racing /complete
	// (returns 410 when asset is already expired). Default 7 = env
	// VIDEO_RETENTION_BUFFER_DAYS. Smaller values free S3 space faster;
	// larger values give the retry worker more slack.
	VideoRetentionBufferDays int
}
