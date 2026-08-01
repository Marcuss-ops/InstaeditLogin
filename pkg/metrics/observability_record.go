package metrics

import (
	"log/slog"
)

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

// RecordYouTubePublishChannelMismatch (P0 #2) increments
// youtube_publish_channel_mismatch_total. Called from the publish
// worker when YouTubeOAuthService.ValidateChannelBinding returns
// ErrYouTubeChannelMismatch (the channels.list?mine=true response
// does not contain the channel id stored on
// platform_accounts.platform_user_id). The worker ALSO writes
// platform_accounts.status='reauth_required' + reauth_required_at
// =NOW() via UserStore.MarkReauthRequired in the same branch; the
// metric is the operator-facing / dashboard signal alongside the
// DB-side flag.
//
// Increment is unconditional on the mismatch DETECTION (not on
// DB-write success): a transient MarkReauthRequired blip must NOT
// hide reauth rates from the dashboard, which is the operator's
// signal to investigate before reconnecting.
func RecordYouTubePublishChannelMismatch(provider string) {
	if provider == "" {
		return
	}
	YouTubePublishChannelMismatch.WithLabelValues(provider).Inc()
}

// RecordYouTubeVideosInsertCall increments the videos.insert counter
// with a canonical result label. Valid labels are "ok", "quota_exceeded",
// or "error". Other values are accepted by Prometheus but should not be
// used — dashboards + alerts are wired to the three canonicals and a
// stray label will silently miss them.
func RecordYouTubeVideosInsertCall(result string) {
	if result != "ok" && result != "quota_exceeded" && result != "error" {
		return
	}
	YouTubeVideosInsertCalls.WithLabelValues(result).Inc()
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

// RecordLeaseExpiry (Task 10/10) increments lease_expiry_total with
// the per-row Add so a tick that recovers 7 rows shows up as +7 on
// the counter, not +1. The source label scopes the metric to the
// pool whose reclaim tick fired (upload today; publish will be
// added as Task 10.10 follow-up).
//
// Callers: pkg.Worker.ReclaimerLoop ticks, after a successful
// ReclaimExpiredLeases + affected > 0.
//
// Reason values (when defined): see WorkerLeaseSource* constants
// below.
func RecordLeaseExpiry(source string, recoveredRows int64) {
	if source == "" || recoveredRows <= 0 {
		return
	}
	LeaseExpiryCount.WithLabelValues(source).Add(float64(recoveredRows))
}

// Worker lease source label vocabulary for lease_expiry_total{source}.
const (
	WorkerLeaseSourceUpload  = "upload"  // upload_worker.runReclaimerLoop
	WorkerLeaseSourcePublish = "publish" // publish_worker.runReclaimerLoop (future)
	WorkerLeaseSourceIngest  = "ingest"  // ingest pool (alias to upload)
)

// ResumableRecoveryReason label values for resumable_recovery_total{reason}.
const (
	ResumableRecoveryReasonWorkerRestart = "worker_restart" // process restarted; persisted session URI reloaded
	ResumableRecoveryReasonChunkLost     = "chunk_lost"     // mid-chunk crash; offset resumes from DB
	ResumableRecoveryReasonUpstream5xx   = "upstream_5xx"   // YouTube replied 500/503; retried from offset
	ResumableRecoveryReasonUpstreamTO    = "upstream_timeout"
)

// RecordResumableRecovery (Task 10/10) increments
// resumable_recovery_total. Called whenever the worker re-attaches
// to a persisted YouTube session URI + offset (SaveYouTubeSession
// path) OR re-initiates a session after a recoverable upstream
// failure (chunk PUT 308 + session URI persisting the offset).
//
// Reason vocabulary is canonical for grep / dashboard filter.
func RecordResumableRecovery(reason string) {
	if reason == "" {
		reason = ResumableRecoveryReasonChunkLost
	}
	ResumableRecoveryCount.WithLabelValues(reason).Inc()
}

// SetQueueDepth / SetQueueLagSeconds / SetTargetsByStatus / SetDeadLetterCount /
// SetDatabasePoolUsage are the periodic-gauge setters called by the
// collector goroutine (Phase 2). Each takes the value directly;
