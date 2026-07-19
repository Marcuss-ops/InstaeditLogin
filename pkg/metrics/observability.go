package metrics

import (
	"log/slog"

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
		publishTargetsByStatus,
		deadLetterCount,
		databasePoolUsage,
		publishAttempts,
		providerLatency,
		providerRateLimits,
		tokenRefreshFailures,
		reauthRequiredAccounts,
		webhookDeliveryFailures,
		uploadThroughputBytes,
		httpRequestsTotal,
		httpRequestLatencySeconds,
	)
}

// ---------------------------------------------------------------------------
// Record helpers — STUBS in commit 1; Phase 2+ wires consumers.
// ---------------------------------------------------------------------------

// RecordPublishAttempt increments publish_attempts_total with the
// given provider + outcome label. Caller is responsible for
// computing the outcome from the ProviderError code
// (see internal/services.PublishOutcomeFromCode).
//
// Pass outcome="" to mean "uncategorised" — the helper substitutes
// the canonical PublishOutcomeInternal so dashboards always have a
// value to query. Pass outcome=services.PublishOutcomeSuccess on
// the happy path.
func RecordPublishAttempt(provider, outcome string) {
	if outcome == "" {
		outcome = "uncategorised"
	}
	if provider == "" {
		// Empty-provider recordings would create a phantom series
		// unattributable to any specific platform. Log at DEBUG and
		// skip — same as the existing RecordPublishSuccess tolerates
		// (silent no-op).
		slog.Debug("metrics.RecordPublishAttempt: empty provider label, recording skipped",
			"outcome", outcome)
		return
	}
	publishAttempts.WithLabelValues(provider, outcome).Inc()
}

// RecordProviderLatency observes provider_latency_seconds. Empty
// inputs are tolerated (DEBUG log + skip) — same shape as the
// existing RecordPublishSuccess / RecordOAuthLoginSuccess helpers
// in metrics.go, which also tolerate empty strings. A panic would
// be inconsistent with the file's existing style and harder to
// reason about in cluster-wide scrapes (one panicking goroutine
// would crash the process; a silent skip is the recoverable path).
func RecordProviderLatency(provider, operation string, seconds float64) {
	if provider == "" || operation == "" {
		slog.Debug("metrics.RecordProviderLatency: empty label, observation skipped",
			"provider", provider, "operation", operation)
		return
	}
	providerLatency.WithLabelValues(provider, operation).Observe(seconds)
}

// RecordProviderRateLimit increments provider_rate_limits_total.
// Use this when a provider returns a rate_limited ProviderError OR
// an HTTP 429 — the worker hook (Phase 3) calls this from the
// IsRateLimitError branch.
func RecordProviderRateLimit(provider string) {
	if provider == "" {
		return
	}
	providerRateLimits.WithLabelValues(provider).Inc()
}

// RecordTokenRefreshFailure increments token_refresh_failures_total
// with the canonical ErrKind label vocabulary (auth/api/network/
// internal). The metrics.ErrorKind helper does the classification
// at the call site: in a defer block, `RecordTokenRefreshFailure(
// platform, ErrorKind(err))`.
func RecordTokenRefreshFailure(platform, errorKind string) {
	if platform == "" || errorKind == "" {
		return
	}
	tokenRefreshFailures.WithLabelValues(platform, errorKind).Inc()
}

// RecordReauthRequired increments reauth_required_accounts_total.
// The metric is monotonic (counter) — once an account moves to
// reauth_required, the counter ticks; the subsequent OAuth callback
// re-grants the user, after which RecordReauthRequired is NOT
// reset (counters are append-only). The dashboard reads "rate over
// 24h" to see the daily reauth traffic.
func RecordReauthRequired(provider string) {
	if provider == "" {
		return
	}
	reauthRequiredAccounts.WithLabelValues(provider).Inc()
}

// RecordWebhookDeliveryFailure increments webhook_delivery_failures_total.
// reason values are the WebhookFailureReason* constants.
func RecordWebhookDeliveryFailure(eventType, reason string) {
	if eventType == "" || reason == "" {
		return
	}
	webhookDeliveryFailures.WithLabelValues(eventType, reason).Inc()
}

// RecordUploadBytes (P2) increments upload_throughput_bytes_total
// for the ingest → publish pipeline. provider is the upstream
// (google_drive for ingest; youtube for publish); phase is the
// pipeline boundary crossed (ingest when bytes land in S3; publish
// when YouTube acks the upload). The worker's hot path stays a
// single IncBy — a no-op on empty labels or non-positive bytes so
// the helper tolerates the MarkIngested / MarkCompleted branches
// that fire on partial-state rows.
func RecordUploadBytes(provider, phase string, bytes int64) {
	if provider == "" || phase == "" || bytes <= 0 {
		return
	}
	uploadThroughputBytes.WithLabelValues(provider, phase).Add(float64(bytes))
}

// ObserveHTTPRequest increments http_requests_total AND observes
// http_request_latency_seconds in a single call. route is chi's
// pattern (e.g. "/api/v1/posts/{id}") — MUST NOT be a raw URL.
func ObserveHTTPRequest(route, method, status string, seconds float64) {
	if route == "" || method == "" || status == "" {
		return
	}
	httpRequestsTotal.WithLabelValues(route, method, status).Inc()
	httpRequestLatencySeconds.WithLabelValues(route).Observe(seconds)
}

// SetQueueDepth / SetQueueLagSeconds / SetTargetsByStatus / SetDeadLetterCount /
// SetDatabasePoolUsage are the periodic-gauge setters called by the
// collector goroutine (Phase 2). Each takes the value directly;
// zero values are valid (e.g. empty queue is depth=0, lag=0).
func SetQueueDepth(depth int)            { publishQueueDepth.Set(float64(depth)) }
func SetQueueLagSeconds(seconds float64) { publishQueueLagSeconds.Set(seconds) }
func SetTargetsByStatus(status string, count int) {
	if status == "" {
		return
	}
	publishTargetsByStatus.WithLabelValues(status).Set(float64(count))
}
func SetDeadLetterCount(source string, count int) {
	if source == "" {
		return
	}
	deadLetterCount.WithLabelValues(source).Set(float64(count))
}
func SetDatabasePoolUsage(state string, count int) {
	if state == "" {
		return
	}
	databasePoolUsage.WithLabelValues(state).Set(float64(count))
}
