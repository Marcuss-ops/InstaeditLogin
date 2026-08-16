package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

// ---------------------------------------------------------------------------
// SPRINT 6.1 (P1#13) — Observability with SLO.
//
// This file DEFINES the 11 production observability metrics the user
// spec requires, plus 2 HTTP-describing metrics (http_requests_total +
// http_request_latency_seconds) the API-layer SLOs depend on. Phase 1
// adds the definitions + label sets + bucket choices; Phase 2+ (separate
// commits per metric family) wires the consumers (workers, dispatcher,
// API middleware, periodic collector goroutine).
//
// ---------------------------------------------------------------------------
//
// LABEL CARDINALITY POLICY (the canonical answer to "why doesn't
// request_id appear as a metric label?"):
//
//   workspace_id  — bounded by tenant count. Used only on metrics
//                   where the per-tenant split is the primary signal
//                   (publish_attempts, reauth_required_accounts).
//                   NOT used on periodic gauges (queue_depth,
//                   targets_by_status) where the global sum is what
//                   the SLO checks.
//
//   post_id,      — HIGH or unbounded cardinality. NEVER used as
//   target_id       Prometheus labels. They live in:
//
//                     - exemplars (trace-id linkage, future OTel work)
//                     - structured log lines (slog.With("post_id", id))
//
//   worker_id     — per-process UUID. NEVER used as a metric label.
//                   The scrape job's `external_labels` block injects
//                   it on every metric at scrape time, so worker_id
//                   appears as a Prometheus target-label on every
//                   panel without the app code paying the cardinality
//                   cost. Application code carries the id as a struct
//                   field on each worker / service and threads it
//                   via constructor (commit DI refactor) — no global
//                   reader — so workers' slog context lines carry
//                   the id without a metrics package coupling.
//
//   provider      — bounded set (7 platforms). ALWAYS used when
//                   applicable — it's the canonical breakdown.
//
//   request_id    — ALSO never a Prometheus label (high cardinality,
//                   no real per-request aggregation needed beyond
//                   log correlation). Goes in slog.With("request_id", id)
//                   on every handler-internal log line.
//
// See docs/ARCHITECTURE.md for the rationale; this comment is the
// single source of truth at the metric surface.
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Label vocabulary constants.
//
// These are the canonical strings call sites pass to Record* helpers.
// Using constants instead of bare strings prevents typos from creating
// phantom series ("reson" instead of "reason" would-be-unqueryable).
// Each block corresponds to one label on one metric.
// ---------------------------------------------------------------------------

// Operation values for provider_latency_seconds{operation}.
const (
	OperationPublish     = "publish"      // Publisher.Publish(entry path)
	OperationAsyncInit   = "async_init"   // AsyncPublisher.StartPublish
	OperationAsyncStatus = "async_status" // AsyncPublisher.CheckPublishStatus
	OperationReconcile   = "reconcile"    // AsyncPublisher.Reconcile
	OperationRefresh     = "refresh"      // OAuthProvider.RefreshOAuthToken
)

// DeadLetterSource values for dead_letter_count{source}.
const (
	DeadLetterSourcePublish = "publish" // post_targets.status='dlq'
	DeadLetterSourceOutbox  = "outbox"  // outbox_events.status='dead_letter'
	DeadLetterSourceWebhook = "webhook" // webhook_deliveries.status='dead'
)

// PoolState values for database_pool_usage{state}.
// Mirrors *sql.DB.Stats()'s field names; lowercase-for-grep-friendly.
const (
	PoolStateInUse = "in_use"
	PoolStateIdle  = "idle"
	PoolStateOpen  = "open"
	PoolStateWait  = "wait"
)

// WebhookFailureReason values for webhook_delivery_failures{reason}.
const (
	WebhookFailureReasonTimeout     = "timeout"       // DefaultWebhookHTTPTimeout elapsed
	WebhookFailureReasonNon2xxRetry = "non_2xx_retry" // 408/425/429 → retry
	WebhookFailureReasonNon2xxDead  = "non_2xx_dead"  // 4xx terminal → dead
	WebhookFailureReasonLoadFailed  = "load_failed"   // FindEventByID/FindEndpointByID errored
	WebhookFailureReasonBuildFailed = "build_failed"  // http.NewRequest errored
)

// ---------------------------------------------------------------------------
// Metric definitions.
// ---------------------------------------------------------------------------

var (
	// ------------------------------------------------------------------
	// Periodic gauges — refreshed every 10s by the collector goroutine
	// (pkg/metrics/collector.go, lands in commit 2).
	// ------------------------------------------------------------------

	// publishQueueDepth is the count of post_targets whose status='queued'
	// AND scheduled_at <= NOW(). The metric a Grafana panel named
	// "Queue Depth" reads. Sampled periodically to keep DB load bounded.
	publishQueueDepth = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "publish_queue_depth",
			Help: "post_targets currently in status='queued' AND scheduled_at<=NOW(). Sampled every 10s by the metrics collector goroutine.",
		},
	)

	// publishQueueLagSeconds is the seconds-between-now-and-scheduled_at
	// for the OLDEST queued target. This is the metric behind the
	// "queue lag p95<30s" SLO. Sampled periodically.
	publishQueueLagSeconds = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "publish_queue_lag_seconds",
			Help: "Seconds between NOW() and the scheduled_at of the oldest queued post_target. The metric behind the queue_lag p95<30s SLO; sampled periodically. 0 when the queue is empty.",
		},
	)

	// uploadJobQueueDepth counts durable upload jobs waiting for either the
	// ingest or publish worker. It complements publish_queue_depth, which is
	// the post-target queue, and makes worker backlog visible before a Post
	// exists.
	uploadJobQueueDepth = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "upload_job_queue_depth",
			Help: "Durable upload jobs waiting for ingest or publish workers, sampled by the metrics collector.",
		},
	)

	// publishTargetsByStatus is the per-status count of post_targets.
	// The labels are the canonical 6 statuses from SPRINTs 5.0-5.2:
	// draft/queued/retrying/publishing/published/failed/dlq. Sampled
	// periodically; a 7-row result-set is cheap.
	publishTargetsByStatus = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "publish_targets_by_status",
			Help: "Number of post_targets per status (draft/queued/retrying/publishing/published/failed/dlq). Sampled every 10s by the metrics collector goroutine.",
		},
		[]string{"status"},
	)

	// deadLetterCount is the total DLQ depth across all DLQ sources.
	// Broke into 3 series by source label so the dashboard can plot
	// them separately.
	deadLetterCount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "dead_letter_count",
			Help: "DLQ depth (publish/outbox/webhook). Sampled every 10s by the metrics collector goroutine. The integration with the operator-triage workflow is a follow-up.",
		},
		[]string{"source"},
	)

	// databasePoolUsage is the *sql.DB pool stats from db.Stats().
	// Labels: in_use/idle/open/wait. Updated by the collector
	// goroutine reading db.Stats() every 10s.
	databasePoolUsage = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "database_pool_usage",
			Help: "*sql.DB pool stats from db.Stats() (in_use/idle/open/wait). Sampled every 10s by the metrics collector goroutine. wait grows when the pool saturates; in_use close to MaxOpenConns (25) is a saturation signal.",
		},
		[]string{"state"},
	)
	databasePoolWaitDurationSeconds = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "database_pool_wait_duration_seconds",
			Help: "Cumulative time callers have waited for a database connection, from database/sql DBStats.WaitDuration.",
		},
	)

	// refreshTokensNearExpiry counts OAuth refresh grants whose
	// provider-issued expiry (tokens.refresh_token_expires_at) falls
	// within the 7-day lookahead window OR is already in the past
	// (already-expired grants still need a reconnect — they're the
	// most urgent cohort). Same horizon as the vault.Renew warning
	// (internal/credentials/vault_refresh.go) and the admin health
	// "Token rotation" view, so the metric, the log line, and the
	// dashboard always agree on one number.
	//
	// The metric is a plain Gauge with NO provider label: the value
	// is a fleet-wide count, matching the other periodic gauges
	// (queue_depth, targets_by_status) where the global sum is the
	// SLO signal. Per-provider splits belong to the admin health
	// view (ConnectionsPerSubject), not this gauge.
	//
	// INTENTIONAL: the count includes grants whose expiry is ALREADY
	// in the past, not just "near" it — a grant past its provider
	// TTL is the most urgent reconnect cohort. An alert on this
	// gauge firing for already-expired grants is correct behaviour,
	// not a bug; do not add an `AND refresh_token_expires_at >= NOW()`
	// filter without revisiting the reconnection workflow.
	refreshTokensNearExpiry = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "refresh_tokens_near_expiry",
			Help: "OAuth refresh grants whose refresh_token_expires_at is within the 7-day warning window or already past it. Sampled every 10s by the metrics collector goroutine; each unit is a grant that needs reconnecting before the provider garbage-collects the refresh token.",
		},
	)

	// ------------------------------------------------------------------
	// Per-event counters — incremented inline by callers.
	// ------------------------------------------------------------------

	// publishAttempts counts each per-target publish attempt. The
	// outcome label values are the services.PublishOutcome* constants
	// (defined in internal/services/provider_error.go next to the
	// taxonomy they're derived from; re-exported here as strings for
	// the worker call sites).
	//
	// The mapping (services.PublishOutcomeFromCode) is exhaustive
	// over the 10 SPRINT 5.1 ProviderError codes.
	publishAttempts = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "publish_attempts_total",
			Help: "Per-target publish attempts, labeled by provider and outcome (success / rate_limited / auth_error / provider_unavail / media_failed / content_rejected / validation / quota / internal). The metric behind the worker terminal failure <1% SLO.",
		},
		[]string{"provider", "outcome"},
	)

	// providerLatency is the per-platform API call latency. The
	// operation label values are the Operation* constants (publish /
	// async_init / async_status / reconcile / refresh).
	//
	// Buckets match the existing publish_latency_seconds (0.05, 0.1,
	// 0.25, 0.5, 1, 2.5, 5, 10) + a 30s tail to capture the YouTube
	// resumable upload path's slow segments. The existing histogram's
	// 10s ceiling is documented as legacy (pre-resumable-upload);
	// future Prometheus dashboards should use provider_latency_seconds
	// for new panels (30s ceiling covers YouTube).
	providerLatency = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "provider_latency_seconds",
			Help:    "Per-platform API call latency in seconds, labeled by provider and operation. Buckets span 50ms to 30s; the 30s tail covers the YouTube resumable upload path.",
			Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30},
		},
		[]string{"provider", "operation"},
	)

	// providerRateLimits counts each rate-limit hit (SPRINT 5.1
	// provider_error.code == rate_limited). The metric behind the
	// platform-partner reporting tier: a sustained rate_limited
	// count tells us "the caller is too noisy for this platform".
	providerRateLimits = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "provider_rate_limits_total",
			Help: "Provider returned a rate-limited response (HTTP 429 OR Meta error_subcode 4 OR equivalent). Labeled by provider. The worker honors Retry-After on this branch.",
		},
		[]string{"provider"},
	)

	// tokenRefreshFailures counts refresh failures. The error_kind
	// label reuses the existing ErrKind* string constants (auth/api/
	// network/internal) — the pkg/metrics.ErrorKind helper does the
	// classification; usage is `RecordTokenRefreshFailure(p, metrics.
	// ErrorKind(err))`.
	tokenRefreshFailures = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "token_refresh_failures_total",
			Help: "OAuth token refresh failures, labeled by platform and error_kind (auth/api/network/internal). Reuses the existing ErrKind vocabulary to keep dashboard panels consistent.",
		},
		[]string{"platform", "error_kind"},
	)

	// reauthRequiredAccounts counts refresh paths that returned
	// reauthentication_required (token is no longer valid via refresh;
	// the user must re-do the OAuth flow). The metric and the
	// "reauth_required_accounts" SLO behind the user's OAuth
	// callback-success-rate >98% — a drift up here typically means
	// the user has revoked the OAuth grant externally.
	reauthRequiredAccounts = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "reauth_required_accounts_total",
			Help: "OAuth refresh paths that returned reauthentication_required (user must re-do the OAuth flow). Labeled by provider. An uptick here typically means the user revoked the OAuth grant externally; the dashboard surfaces a hint for the operator to email the user.",
		},
		[]string{"provider"},
	)

	// youtubePublishChannelMismatch (P0 #2 — pre-upload channel binding
	// re-check). Counts every YouTube publish attempt where the
	// channels.list?mine=true response reports a channel set that does
	// NOT contain the channel id stored on platform_accounts.
	// platform_user_id. Each increment means a publish was refused AND
	// the platform_account was flagged status='reauth_required' +
	// reauth_required_at=NOW() so the operator's dashboard prompts
	// the user to reconnect. Drift up here typically means Google
	// silently re-bound the OAuth grant to a different Brand Account
	// (operator migration or fraud); the operator should investigate
	// before reconnecting.
	//
	// Exported (capital Y) so cross-package consumers (notably
	// internal/worker/publish_worker_test.go) can read the counter
	// value with prometheus/client_golang/prometheus/testutil for
	// behaviour assertions. The other counters in this file are
	// unexported because their tests live in this same package;
	// this one needs cross-package access because the detection
	// site lives one package over.
	YouTubePublishChannelMismatch = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "youtube_publish_channel_mismatch_total",
			Help: "YouTube OAuth grants that are bound to a different channel than the platform_account row expected. Each increment means a publish attempt was refused AND the platform_account was flagged reauth_required. Drift up here typically means Google silently re-bound the grant to a different Brand Account.",
		},
		[]string{"provider"},
	)

	// YouTubeVideosInsertCalls counts every YouTube Data API v3 videos.insert
	// HTTP call made by InstaEdit (resumable upload). Label `result` is
	// one of three canonical values:
	//
	//	"ok"             — successful 2xx response from YouTube.
	//	"quota_exceeded" — either the YouTubeQuotaManager pre-call gate
	//	                   (2026 three-bucket model, internal/services/
	//	                   youtube_quota_manager.go) refused the call, OR the
	//	                   call was made and YouTube returned a quotaExceeded
	//	                   error envelope. Both paths roll up here.
	//	"error"          — all other failure modes (5xx, transport, validation).
	//
	// Cardinality: result has 3 labels, so the time-series count is bounded
	// to 3 series regardless of fleet size — operators can safely alert on
	// a sudden spike of the quota_exceeded label across the whole fleet.
	YouTubeVideosInsertCalls = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "youtube_videos_insert_calls_total",
			Help: "Total videos.insert calls to YouTube Data API v3, labeled by result (ok | quota_exceeded | error).",
		},
		[]string{"result"},
	)

	// webhookDeliveryFailures counts webhook delivery outcomes that
	// did NOT succeed (retry or dead). Three series:
	//   - "event_type" — webhook event name. CARDINALITY ALERT IDEA
	//     (Phase 9 follow-up): watch distinct event_type values; if
	//     >N (say 50) for any 24h window, page the operator. The
	//     system has no per-user event types today, but a future
	//     SPRINT could add them — the alert is the safety net.
	//   - "reason" — WebhookFailureReason* constants (timeout /
	//     non_2xx_retry / non_2xx_dead / load_failed / build_failed).
	webhookDeliveryFailures = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "webhook_delivery_failures_total",
			Help: "Webhook delivery outcomes that did NOT succeed (retry or dead). Labeled by event_type and reason (timeout / non_2xx_retry / non_2xx_dead / load_failed / build_failed). The metric behind the webhook delivery p95<60s SLO via the webhook_latency_seconds histogram (Phase 5).",
		},
		[]string{"event_type", "reason"},
	)

	// leaseExpiryCount (Task 10/10) tracks lease expiries
	// reclaimed by the background reclaimer. Labelled by source so
	// the operator can distinguish an upload-pool reclaim storm
	// from a publish-pool reclaim storm. An uptick typically means
	// a worker crash mid-flight (heartbeat stopped) — the reaper
	// recovers the row so the next pool tick can re-claim it.
	// Couple with the upload_job_count-by-status gauge for the
	// full picture (claim_rate vs expire_rate).
	//
	// Exported (capitalised L) so cross-package test rigs
	// (notably internal/worker/task_10_10_recovery_test.go) can
	// assert the counter increments via prometheus/testutil.
	// Mirrors the YouTubePublishChannelMismatch export pattern.
	LeaseExpiryCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "lease_expiry_total",
			Help: "Worker lease expiries reclaimed by the background reclaimer, labelled by source (upload / publish). Each increment represents one tick of the reclaimer recovering N rows; the Add-by-N variant preserves per-row fidelity.",
		},
		[]string{"source"},
	)

	// resumableRecoveryCount (Task 10/10) tracks YouTube resumable
	// session recoveries. Labelled by reason so the operator can
	// distinguish a worker_restart (cold start, expected) from a
	// chunk_lost (mid-upload crash, careful) from an upstream
	// _timeout or _5xx (YouTube side degraded). This is the metric
	// the runbook anchors for "is the upload path surviving crashes".
	//
	// Exported (capitalised R) so cross-package test rigs
	// (notably internal/worker/task_10_10_recovery_test.go) can
	// assert the counter increments via prometheus/testutil. Same
	// trade-off as YouTubePublishChannelMismatch — production path
	// is unexported call sites, test path needs cross-package read.
	ResumableRecoveryCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "resumable_recovery_total",
			Help: "YouTube resumable session recoveries, labelled by reason (worker_restart / chunk_lost / upstream_timeout / upstream_5xx). The metric behind the upload-path survival SLO; rate(this[5m]) > 0.1/min trips a warning.",
		},
		[]string{"reason"},
	)

	// uploadThroughputBytes (P2 — ops dashboard) tracks bytes that
	// crossed a worker boundary. provider discriminates the
	// upstream (google_drive for ingest; youtube for publish);
	// phase discriminates the pipeline boundary (ingest when bytes
	// land in S3; publish when YouTube acks). The dashboard derives
	// "upload throughput" via rate(this_counter[5m])/300 — the raw
	// counter stays cheap so the hot path is one IncBy call. The
	// 200-channel rollout ingest baseline (~1 MB/s drive-side) and
	// publish baseline (~10–30 MB/s YouTube-side) both fit inside
	// the int64 counter envelope indefinitely.
	uploadThroughputBytes = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "upload_throughput_bytes_total",
			Help: "Bytes that crossed a worker boundary (provider=google_drive/youtube; phase=ingest/publish). Derive throughput via rate(this_counter[5m])/300.",
		},
		[]string{"provider", "phase"},
	)

	// ------------------------------------------------------------------
	// HTTP request metrics — wired by Phase 6 (request middleware).
	// ------------------------------------------------------------------

	// httpRequestsTotal is the canonical Prometheus HTTP middleware
	// output. route uses chi's route pattern (e.g. "/api/v1/posts/{id}")
	// to keep cardinality bounded (the middleware MUST not let raw
	// URLs into the label — chi pattern is stable, raw URL is not).
	httpRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "HTTP requests handled by the API server, labeled by route pattern, method, and status (2xx / 4xx / 5xx). The metric behind the API availability 99.9% SLO.",
		},
		[]string{"route", "method", "status"},
	)

	// httpRequestLatencySeconds is the per-endpoint latency. Buckets
	// are tuned around the POST /posts p95<300ms SLO so the histogram
	// emits directly into a quantile that lines up with the alert
	// threshold.
	httpRequestLatencySeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_latency_seconds",
			Help:    "HTTP request latency in seconds, labeled by route pattern. Buckets include 0.3 (the POST /posts p95 SLO threshold) so histogram_quantile returns the SLO directly.",
			Buckets: []float64{0.05, 0.1, 0.15, 0.2, 0.25, 0.3, 0.4, 0.5, 1, 2.5, 5, 10},
		},
		[]string{"route"},
	)
)

// init registers the new metrics with the default Prometheus
// registry. Existing metrics register in metrics.go's init; this
// init() runs after metrics.go's (Go runs init funcs in source-file
// lexical order within a package; alphabetically observability.go
// follows metrics.go, so metrics.go's init() runs first, then ours).
//
// If a metric name collides with one already registered,
// prometheus.MustRegister panics on process start — that's the
// canonical fail-fast behaviour for a misconfigured rename.
//
// REGISTERING ADDITIONAL METRICS: a future commit adding a new
// metric (e.g. worker_metrics.go) MUST also add its prometheus.
// MustRegister call either in this init() or in its own init().
// Forgetting the registration surfaces only when Prometheus scrapes
// — burying failures late in the test cycle. A process-private
// comment here pins the convention.
func init() {
	prometheus.MustRegister(
		publishQueueDepth,
		publishQueueLagSeconds,
		uploadJobQueueDepth,
		publishTargetsByStatus,
		deadLetterCount,
		databasePoolUsage,
		databasePoolWaitDurationSeconds,
		refreshTokensNearExpiry,
		publishAttempts,
		providerLatency,
		providerRateLimits,
		tokenRefreshFailures,
		reauthRequiredAccounts,
		YouTubePublishChannelMismatch,
		YouTubeVideosInsertCalls,
		webhookDeliveryFailures,
		LeaseExpiryCount,
		ResumableRecoveryCount,
		uploadThroughputBytes,
		httpRequestsTotal,
		httpRequestLatencySeconds,
	)
}

// ---------------------------------------------------------------------------
