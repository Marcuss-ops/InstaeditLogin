package metrics

import (
	"context"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// RequestStats accumulates low-cardinality work performed while serving one
// HTTP request. It is shared through context so database instrumentation can
// account SQL work without changing every repository signature.
type RequestStats struct {
	sqlQueries       atomic.Int64
	sqlDurationNanos atomic.Int64
}

// NewRequestStats creates an empty per-request measurement accumulator.
func NewRequestStats() *RequestStats { return &RequestStats{} }

// WithRequestStats attaches stats to ctx and returns the derived context.
func WithRequestStats(ctx context.Context, stats *RequestStats) context.Context {
	if stats == nil {
		return ctx
	}
	return context.WithValue(ctx, requestStatsContextKey{}, stats)
}

// RequestStatsFromContext returns the request accumulator, if present.
func RequestStatsFromContext(ctx context.Context) *RequestStats {
	if ctx == nil {
		return nil
	}
	stats, _ := ctx.Value(requestStatsContextKey{}).(*RequestStats)
	return stats
}

type requestStatsContextKey struct{}

func (s *RequestStats) addSQL(duration time.Duration) {
	if s == nil {
		return
	}
	s.sqlQueries.Add(1)
	s.sqlDurationNanos.Add(duration.Nanoseconds())
}

// SQLQueries returns the number of SQL statements observed for the request.
func (s *RequestStats) SQLQueries() int64 {
	if s == nil {
		return 0
	}
	return s.sqlQueries.Load()
}

// SQLDuration returns the cumulative duration of observed SQL statements.
func (s *RequestStats) SQLDuration() time.Duration {
	if s == nil {
		return 0
	}
	return time.Duration(s.sqlDurationNanos.Load())
}

var (
	sqlQueriesTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "sql_queries_total",
			Help: "SQL statements executed by the application.",
		},
	)
	sqlQueryDurationSeconds = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "sql_query_duration_seconds",
			Help:    "Duration of SQL statement executions in seconds.",
			Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		},
	)
	httpResponseSizeBytes = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_response_size_bytes",
			Help:    "HTTP response payload size in bytes, labeled by bounded route pattern.",
			Buckets: []float64{256, 1024, 4096, 16 * 1024, 64 * 1024, 256 * 1024, 1024 * 1024, 4 * 1024 * 1024},
		},
		[]string{"route"},
	)
	httpRequestSQLQueries = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_sql_queries",
			Help:    "Number of SQL statements observed while serving one HTTP request, labeled by bounded route pattern.",
			Buckets: []float64{0, 1, 2, 5, 10, 20, 50, 100, 250},
		},
		[]string{"route"},
	)
	httpRequestSQLDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_sql_duration_seconds",
			Help:    "Cumulative SQL duration observed while serving one HTTP request, labeled by bounded route pattern.",
			Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		},
		[]string{"route"},
	)
	googleAPICallsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "google_api_calls_total",
			Help: "Outbound Google API calls, labeled by bounded operation and result.",
		},
		[]string{"operation", "result"},
	)
	mediaProcessesActive = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "media_processes_active",
			Help: "External media processes currently running, labeled by process (for example ffprobe or ffmpeg).",
		},
		[]string{"process"},
	)
	databasePoolConfigured = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "database_pool_configured_connections",
			Help: "Configured database/sql pool limits by process profile.",
		},
		[]string{"profile", "limit"},
	)
)

func init() {
	prometheus.MustRegister(
		sqlQueriesTotal,
		sqlQueryDurationSeconds,
		httpResponseSizeBytes,
		httpRequestSQLQueries,
		httpRequestSQLDurationSeconds,
		googleAPICallsTotal,
		mediaProcessesActive,
		databasePoolConfigured,
	)
}

// ObserveSQL records one SQL statement globally and, when available, on the
// current HTTP request. Query text is intentionally not a label.
func ObserveSQL(ctx context.Context, duration time.Duration) {
	if duration < 0 {
		duration = 0
	}
	sqlQueriesTotal.Inc()
	sqlQueryDurationSeconds.Observe(duration.Seconds())
	if stats := RequestStatsFromContext(ctx); stats != nil {
		stats.addSQL(duration)
	}
}

// ObserveHTTPRequestDetails records the existing HTTP SLO metrics plus
// response bytes and per-request SQL work.
func ObserveHTTPRequestDetails(route, method, status string, seconds float64, responseBytes int64, stats *RequestStats) {
	if status == "" {
		status = "200"
	}
	ObserveHTTPRequest(route, method, status, seconds)
	if route == "" {
		return
	}
	if responseBytes < 0 {
		responseBytes = 0
	}
	httpResponseSizeBytes.WithLabelValues(route).Observe(float64(responseBytes))
	if stats == nil {
		stats = &RequestStats{}
	}
	httpRequestSQLQueries.WithLabelValues(route).Observe(float64(stats.SQLQueries()))
	httpRequestSQLDurationSeconds.WithLabelValues(route).Observe(stats.SQLDuration().Seconds())
}

// RecordGoogleAPICall records one outbound Google API attempt. Operation is
// supplied by the bounded classifier in the HTTP transport; raw URLs and
// query strings are never metric labels.
func RecordGoogleAPICall(operation string, statusCode int, duration time.Duration) {
	if operation == "" {
		operation = "google_api"
	}
	result := "success"
	if statusCode == 429 {
		result = "rate_limited"
	} else if statusCode < http.StatusOK || statusCode >= http.StatusMultipleChoices {
		result = "error"
	}
	googleAPICallsTotal.WithLabelValues(operation, result).Inc()
	RecordProviderLatency("google", operation, duration.Seconds())
}

// StartMediaProcess increments the active-process gauge. Callers must pair
// it with EndMediaProcess using defer immediately after process start.
func StartMediaProcess(process string) {
	if process == "" {
		return
	}
	mediaProcessesActive.WithLabelValues(process).Inc()
}

// EndMediaProcess decrements the active-process gauge without allowing a
// missing/empty label to create an unbounded or phantom series.
func EndMediaProcess(process string) {
	if process == "" {
		return
	}
	mediaProcessesActive.WithLabelValues(process).Dec()
}

// SetDatabasePoolConfigured publishes the explicit role budget used by this
// process. It complements DBStats gauges: operators can distinguish a full
// pool from a correctly idle pool without consulting deployment env files.
func SetDatabasePoolConfigured(profile string, maxOpen, maxIdle int) {
	if profile == "" {
		profile = "legacy"
	}
	if maxOpen < 0 {
		maxOpen = 0
	}
	if maxIdle < 0 {
		maxIdle = 0
	}
	databasePoolConfigured.WithLabelValues(profile, "max_open").Set(float64(maxOpen))
	databasePoolConfigured.WithLabelValues(profile, "max_idle").Set(float64(maxIdle))
}
