// Package metrics owns the Prometheus instrumentation for InstaEditLogin.
//
// Metric naming follows the Prometheus best practices:
//   - <subject>_<unit>_total for monotonic counters
//   - <subject>_<unit> for histograms (latency in seconds)
//   - low-cardinality labels only: `platform` ∈ {instagram,facebook,threads,tiktok,twitter,youtube,linkedin},
//     `error_kind` ∈ {auth, api, network, internal}. NEVER put err.Error()
//     raw bytes into a label.
//
// Use the helper functions (RecordPublishSuccess, RecordPublishError, …) so
// call sites stay readable and the label contract stays in one place.
package metrics

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Platform label values. Kept in sync with internal/models.
const (
	PlatformInstagram = "instagram"
	PlatformFacebook  = "facebook"
	PlatformThreads   = "threads"
	PlatformTikTok    = "tiktok"
	PlatformTwitter   = "twitter"
	PlatformYouTube   = "youtube"
	PlatformLinkedIn  = "linkedin"
)

// error_kind label values.
const (
	ErrKindAuth     = "auth"
	ErrKindAPI      = "api"
	ErrKindNetwork  = "network"
	ErrKindInternal = "internal"
)

var (
	publishSuccess = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "publish_success_total",
			Help: "Successful content publishes, labeled by platform.",
		},
		[]string{"platform"},
	)
	publishError = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "publish_error_total",
			Help: "Failed content publishes, labeled by platform and error_kind (auth/api/network/internal).",
		},
		[]string{"platform", "error_kind"},
	)
	publishLatency = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name: "publish_latency_seconds",
			Help: "Latency of Publish() calls in seconds, by platform. Buckets tuned for typical 1-3s API publishes; includes a 50ms floor for fast ack paths and a 10s ceiling for slow / failing calls.",
			// Custom buckets: tighter than DefBuckets at the low end so we
			// can distinguish fast acks (TikTok init, Meta container)
			// from full publish round-trips, and a 10s ceiling covers the
			// YouTube resumable upload path.
			Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		},
		[]string{"platform"},
	)
	oauthLoginSuccess = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "oauth_login_success_total",
			Help: "Successful OAuth callbacks, by platform.",
		},
		[]string{"platform"},
	)
	oauthLoginError = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "oauth_login_error_total",
			Help: "Failed OAuth callbacks, by platform and error_kind.",
		},
		[]string{"platform", "error_kind"},
	)
	tokenRefreshSuccess = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "token_refresh_success_total",
			Help: "Successful OAuth token refreshes, by platform.",
		},
		[]string{"platform"},
	)
	tokenRefreshError = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "token_refresh_error_total",
			Help: "Failed OAuth token refreshes, by platform.",
		},
		[]string{"platform"},
	)
	jwtIssued = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "jwt_issued_total",
			Help: "JWTs issued at /api/v1/auth/{provider}/callback.",
		},
	)
	// C5 orphan-session metric — counts orphan session rows that
	// failed to revoke AFTER the helper's one-retry attempt. Each
	// increment means an orphan row will linger in the `sessions`
	// table until the periodic Cleanup() goroutine (sessions_cleanup,
	// grace window 30d revoked / 7d expired) hard-deletes it.
	// Monitor via SLO on the rate: any sustained non-zero value
	// indicates the underlying DB blip is large enough that even
	// 1-retry couldn't repair — investigate DB connection pool
	// health before the Cleanup() lag accumulates.
	sessionOrphanRevokeFailures = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "session_orphan_revoke_failures_total",
			Help: "SessionsService.cleanupOrphanSession: revoke attempts that failed even after the helper's one-retry. Each increment = stale orphan row awaiting periodic Cleanup.",
		},
	)
	// P2 ops — Token rotation alert. GaugeVec labeled by
	// (provider, subject) so the periodic collector (collector.go)
	// populates one series per Google manager Account. The naming
	// follows the user's spec ("oauth_connections_per_subject_total")
	// verbatim; the "_total" suffix suggests counter but the metric
	// is GAUGE because the value can go DOWN when a connection is
	// revoked (counters are forbidden to decrease). Alert when
	// value > 80 (operator's chosen soft margin under Google's
	// 100-refresh-token-per-client-ID cap).
	oauthConnectionsPerSubject = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "oauth_connections_per_subject_total",
			Help: "Active oauth_connections rows grouped by provider and the granter's stable subject id. Google subjects above 80 risk the 100-refresh-token cap. Runbook: docs/OPERATIONS.md \u00a78.",
		},
		[]string{"provider", "subject"},
	)
	// ConnectLinkConsumeTotal counts connect-link nonce consumption
	// attempts during the OAuth callback. The `reason` label
	// distinguishes the success path from replay-prevention
	// rejections (missing/expired/consumed) so operators can debug
	// 410 Gone responses without grepping logs.
	connectLinkConsumeTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "connect_link_consume_total",
			Help: "Connect-link nonce consumption attempts, by reason (ok, missing, expired, consumed).",
		},
		[]string{"reason"},
	)
)

func init() {
	prometheus.MustRegister(
		publishSuccess,
		publishError,
		publishLatency,
		oauthLoginSuccess,
		oauthLoginError,
		tokenRefreshSuccess,
		tokenRefreshError,
		jwtIssued,
		sessionOrphanRevokeFailures,
		oauthConnectionsPerSubject,
		connectLinkConsumeTotal,
	)
}

// --- Record helpers ------------------------------------------------------

func RecordPublishSuccess(platform string) {
	publishSuccess.WithLabelValues(platform).Inc()
}

func RecordPublishError(platform, kind string) {
	publishError.WithLabelValues(platform, kind).Inc()
}

func ObservePublishLatency(platform string, seconds float64) {
	publishLatency.WithLabelValues(platform).Observe(seconds)
}

func RecordOAuthLoginSuccess(platform string) {
	oauthLoginSuccess.WithLabelValues(platform).Inc()
}

func RecordOAuthLoginError(platform, kind string) {
	oauthLoginError.WithLabelValues(platform, kind).Inc()
}

// RecordConnectLinkConsume increments the connect_link_consume_total
// counter for the supplied reason. The OAuth callback uses this to
// expose whether a connect-link nonce was consumed successfully or
// rejected because it was missing, expired, or already consumed.
// Callers should pass one of: "ok", "missing", "expired", "consumed".
func RecordConnectLinkConsume(reason string) {
	if reason == "" {
		reason = "unknown"
	}
	connectLinkConsumeTotal.WithLabelValues(reason).Inc()
}

func RecordTokenRefreshSuccess(platform string) {
	tokenRefreshSuccess.WithLabelValues(platform).Inc()
}

func RecordTokenRefreshError(platform string) {
	tokenRefreshError.WithLabelValues(platform).Inc()
}

func IncJWTIssued() {
	jwtIssued.Inc()
}

// RecordSessionOrphanRevokeFailure is called by
// SessionsService.cleanupOrphanSession when BOTH the initial
// s.repo.Revoke attempt AND the one-retry attempt fail. The orphan
// row stays in the sessions table until periodic Cleanup() deletes
// it per the C5 contract.
func RecordSessionOrphanRevokeFailure() {
	sessionOrphanRevokeFailures.Inc()
}

// SetOAuthConnectionsPerSubject is called by the periodic
// collector (pkg/metrics/collector.go::collectOAuthConnectionsPerSubject)
// once per tick AFTER ResetOAuthConnectionsPerSubjectMetrics so a
// revoked connection immediately stops emitting. Each call writes
// one series (provider, subject) -> count.
//
// `subject` MUST be label-safe (≤64 chars, no null bytes);
// collectors SHOULD pass TruncateSubjectForLabel() before calling
// here. The cap is a soft guard against accidental unbounded
// series memory growth (Prometheus best practice: each label
// series costs ~3KB of process memory).
func SetOAuthConnectionsPerSubject(provider, subject string, count int64) {
	oauthConnectionsPerSubject.WithLabelValues(provider, subject).Set(float64(count))
}

// ResetOAuthConnectionsPerSubjectMetrics clears ALL series on the
// oauthConnectionsPerSubject gauge. Called once per tick BEFORE
// the new SET loop so a revoked connection's series is removed
// from the scrape (a missing series is more operator-honest than
// a stale series at the old count). Costs O(N) where N is the
// current subject count — at 200 subjects that's a no-op.
func ResetOAuthConnectionsPerSubjectMetrics() {
	oauthConnectionsPerSubject.Reset()
}

// TruncateSubjectForLabel promotes a raw Google subject id
// (which can be up to 255 chars per Google's OIDC spec) to a
// label-safe value (<=64 chars). Strategy: sha256 hex of the
// raw id keeps the cardinality AND uniqueness — different
// subjects map to different hex strings, and Prometheus sees
// `subject=ai3f...` labels instead of the raw id. Operators
// querying PromQL can still aggregate by the truncated form.
//
// NOTE: the sha256 hex output is 64 chars = at the Prometheus
// label-length ceiling. Inputs <= 64 chars pass through verbatim
// so production IDs are operator-readable in /metrics output.
func TruncateSubjectForLabel(subject string) string {
	const maxLabelLen = 64
	if len(subject) <= maxLabelLen && !strings.ContainsAny(subject, "\x00") {
		return subject
	}
	sum := sha256.Sum256([]byte(subject))
	return hex.EncodeToString(sum[:])
}

// --- Error classification -------------------------------------------------

// ErrorKind classifies an error into one of {auth, api, network, internal}.
// This is the ONLY function allowed to put a value into the error_kind label.
// Never inline pattern-matching at call sites — keep the bucketing here so
// dashboards stay consistent.
//
// Contract: callers must guard with `err != nil` before calling. The function
// does not nil-check on purpose: a nil here indicates a programming error
// upstream (the metric record happens unconditionally inside the deferred
// block, which already gates on err != nil). Returning "" for nil would
// silently produce unlabeled series; panicking would be too aggressive.
func ErrorKind(err error) string {
	s := strings.ToLower(err.Error())
	switch {
	case
		strings.Contains(s, "401"),
		strings.Contains(s, "unauthorized"),
		strings.Contains(s, "invalid_token"),
		strings.Contains(s, "token expired"),
		strings.Contains(s, "expired at"),
		strings.Contains(s, "missing authorization"):
		return ErrKindAuth
	case
		strings.Contains(s, "timeout"),
		strings.Contains(s, "timed out"),
		strings.Contains(s, "connection refused"),
		strings.Contains(s, "no such host"),
		strings.Contains(s, "dial tcp"),
		strings.Contains(s, "tls"),
		strings.Contains(s, "eof"),
		strings.Contains(s, "network"):
		return ErrKindNetwork
	case
		strings.Contains(s, "publish failed"),
		strings.Contains(s, "media container"),
		strings.Contains(s, "tiktok init"),
		strings.Contains(s, "missing authorization code"),
		strings.Contains(s, "invalid payload"),
		strings.Contains(s, "400"),
		strings.Contains(s, "403"),
		strings.Contains(s, "404"),
		strings.Contains(s, "500"),
		strings.Contains(s, "502"),
		strings.Contains(s, "503"),
		strings.Contains(s, "504"):
		// Note: "401" is intentionally absent here. Go switch evaluates
		// cases top-down and the auth branch above already matches any
		// error containing "401", so a duplicate here would be dead code.
		// Add it back only if a future endpoint returns 401 for a clearly
		// non-auth reason.
		return ErrKindAPI
	default:
		return ErrKindInternal
	}
}

// --- HTTP handler --------------------------------------------------------

// Handler returns the Prometheus exposition-format HTTP handler.
//
// Commit DI refactor (commit 2 of the bootstrap DI refactor): the
// previous sync.Once lazy-init pattern was dropped — user feedback
// was 'drop the sync.Once sparsi (metrics handler, memory limiter)'.
// promhttp.Handler() builds an internal gatherer snapshot on every
// call against the default registry; per-call construction is O(1)
// and cheap, so the cache was a false optimisation. If a future
// caller measures non-trivial construction overhead, reintroduce a
// sync.Once AND document the share-across-scrapes justification.
//
// Optional basic auth (env-driven) is layered by pkg/api/routes.handleMetrics
// when METRICS_BASIC_AUTH_USER + METRICS_BASIC_AUTH_PASS are both configured.
func Handler() http.Handler {
	return promhttp.Handler()
}
