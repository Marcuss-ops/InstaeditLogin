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

func RecordTokenRefreshSuccess(platform string) {
	tokenRefreshSuccess.WithLabelValues(platform).Inc()
}

func RecordTokenRefreshError(platform string) {
	tokenRefreshError.WithLabelValues(platform).Inc()
}

func IncJWTIssued() {
	jwtIssued.Inc()
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
