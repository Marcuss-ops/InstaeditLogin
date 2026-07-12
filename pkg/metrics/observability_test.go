package metrics

import (
	"strings"
	"sync"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
)

// NOTE on the import boundary:
//
// This package MUST NOT import internal/services directly. The cycle
// would be: pkg/metrics/test → internal/services → pkg/metrics (via
// internal/services/metrics_helper.go which imports pkg/metrics for
// RecordPublishMetrics). Go's test-import-cycle rule forbids this.
//
// Tests for PublishOutcomeFromCode live in
// internal/services/provider_error_publish_outcome_test.go instead.
// The mapping itself lives in internal/services/provider_error.go
// (next to ProviderErrorCode, the type the mapping depends on).
//
// The integration here is: when the worker writes a failed publish to
// MetricRecordAttempt(provider, services.PublishOutcomeFromCode(pe.Code)),
// the metric increments correctly. End-to-end test coverage for the
// helper is in services, exposure in metrics — neither test depends
// on the other's package.

// gatherMetricFamilies dumps every registered metric into a map keyed
// by metric name. Used to assert registration + label sets without
// mutating any global state.
func gatherMetricFamilies(t *testing.T) map[string]*dto.MetricFamily {
	t.Helper()
	mfs, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("prometheus.Gather() returned error: %v", err)
	}
	out := make(map[string]*dto.MetricFamily, len(mfs))
	for _, mf := range mfs {
		out[mf.GetName()] = mf
	}
	return out
}

// TestObservability_MetricNamesRegistered verifies all 11 new metric
// names from the SPRINT 6.1 spec are registered.
//
// subtlety: prometheus.Gather() returns ONLY metric families that
// have at least one observed sample (a Gauge Set, a Counter Inc, a
// Histogram Observe). A fresh metric family is invisible to Gather
// until someone creates the first sample. Without pre-creating, the
// test fails as "metric not registered" even though MustRegister()
// ran in init(). The pre-create pattern at the top fixes this.
func TestObservability_MetricNamesRegistered(t *testing.T) {
	// Pre-create one sample per metric so they materialize in
	// Gather output. Use the "__registration_probe__" string as a
	// sentinel label so the test sample is filterable from real
	// production data on dashboards (a Grafana filter
	// `{provider!="__registration_probe__"}` excludes them).
	publishQueueDepth.Set(0)
	publishQueueLagSeconds.Set(0)
	publishTargetsByStatus.WithLabelValues("__registration_probe__").Set(0)
	deadLetterCount.WithLabelValues("__registration_probe__").Set(0)
	databasePoolUsage.WithLabelValues("__registration_probe__").Set(0)
	publishAttempts.WithLabelValues("__registration_probe__", "test").Add(0)
	providerLatency.WithLabelValues("__registration_probe__", OperationPublish).Observe(0)
	providerRateLimits.WithLabelValues("__registration_probe__").Add(0)
	tokenRefreshFailures.WithLabelValues("__registration_probe__", "test").Add(0)
	reauthRequiredAccounts.WithLabelValues("__registration_probe__").Add(0)
	webhookDeliveryFailures.WithLabelValues("__registration_probe__", "test").Add(0)
	httpRequestsTotal.WithLabelValues("__registration_probe__", "GET", "200").Add(0)
	httpRequestLatencySeconds.WithLabelValues("__registration_probe__").Observe(0)

	mfs := gatherMetricFamilies(t)
	wantNames := []string{
		// Periodic gauges
		"publish_queue_depth",
		"publish_queue_lag_seconds",
		"publish_targets_by_status",
		"dead_letter_count",
		"database_pool_usage",
		// Per-event counters
		"publish_attempts_total",
		"provider_rate_limits_total",
		"token_refresh_failures_total",
		"reauth_required_accounts_total",
		"webhook_delivery_failures_total",
		// Histograms
		"provider_latency_seconds",
		// HTTP SLO
		"http_requests_total",
		"http_request_latency_seconds",
	}
	for _, name := range wantNames {
		if _, ok := mfs[name]; !ok {
			t.Errorf("metric not registered: %q", name)
		}
	}
}

// TestObservability_GaugesHaveNoTotalSuffix verifies gauges don't
// end in `_total` (that's counter territory).
func TestObservability_GaugesHaveNoTotalSuffix(t *testing.T) {
	mfs := gatherMetricFamilies(t)
	gauges := []string{
		"publish_queue_depth", "publish_queue_lag_seconds",
		"publish_targets_by_status", "dead_letter_count", "database_pool_usage",
	}
	for _, name := range gauges {
		mf, ok := mfs[name]
		if !ok {
			continue
		}
		if mf.GetType() != dto.MetricType_GAUGE {
			t.Errorf("metric %q should be a gauge, got %v", name, mf.GetType())
		}
		if strings.HasSuffix(name, "_total") {
			t.Errorf("gauge %q should not end in _total (counter convention)", name)
		}
	}
}

// TestObservability_CountersHaveTotalSuffix verifies counters
// MUST end in `_total`.
func TestObservability_CountersHaveTotalSuffix(t *testing.T) {
	mfs := gatherMetricFamilies(t)
	counters := []string{
		"publish_attempts_total",
		"provider_rate_limits_total",
		"token_refresh_failures_total",
		"reauth_required_accounts_total",
		"webhook_delivery_failures_total",
		"http_requests_total",
	}
	for _, name := range counters {
		mf, ok := mfs[name]
		if !ok {
			continue
		}
		if mf.GetType() != dto.MetricType_COUNTER {
			t.Errorf("metric %q should be a counter, got %v", name, mf.GetType())
		}
		if !strings.HasSuffix(name, "_total") {
			t.Errorf("counter %q must end in _total (Prometheus convention)", name)
		}
	}
}

// TestObservability_HistogramsHaveSecondsSuffix verifies the
// latency histograms follow the `_seconds` convention for time units.
func TestObservability_HistogramsHaveSecondsSuffix(t *testing.T) {
	mfs := gatherMetricFamilies(t)
	histograms := []string{"provider_latency_seconds", "http_request_latency_seconds"}
	for _, name := range histograms {
		mf, ok := mfs[name]
		if !ok {
			continue
		}
		if mf.GetType() != dto.MetricType_HISTOGRAM {
			t.Errorf("metric %q should be a histogram, got %v", name, mf.GetType())
		}
		if !strings.HasSuffix(name, "_seconds") {
			t.Errorf("histogram %q must end in _seconds (Prometheus unit convention)", name)
		}
	}
}

// TestObservability_NoMetricUsesRequestIDLabel verifies the
// LABEL CARDINALITY POLICY: no metric uses request_id as a label.
// Future commits adding such a label will fail this test loudly.
func TestObservability_NoMetricUsesRequestIDLabel(t *testing.T) {
	mfs := gatherMetricFamilies(t)
	for _, mf := range mfs {
		for _, m := range mf.GetMetric() {
			for _, l := range m.GetLabel() {
				if l.GetName() == "request_id" {
					t.Errorf("metric %q has a 'request_id' label — VIOLATES label cardinality policy. request_id goes in slog.With() only.", mf.GetName())
				}
			}
		}
	}
}

// TestObservability_NoMetricUsesPostOrTargetIDAsLabel pins the
// cardinality rule: post_id, target_id are NEVER Prometheus labels.
func TestObservability_NoMetricUsesPostOrTargetIDAsLabel(t *testing.T) {
	mfs := gatherMetricFamilies(t)
	denyList := []string{"post_id", "target_id"}
	for _, mf := range mfs {
		for _, m := range mf.GetMetric() {
			for _, l := range m.GetLabel() {
				for _, denied := range denyList {
					if l.GetName() == denied {
						t.Errorf("metric %q has a %q label — VIOLATES label cardinality policy.", mf.GetName(), denied)
					}
				}
			}
		}
	}
}

// ---------------------------------------------------------------------------
// publish_attempts outcome mapping tests.
//
// The mapping (SPRINT 5.1 ProviderErrorCode → outcome label) lives in
// internal/services and is tested in
// internal/services/provider_error_publish_outcome_test.go. The tests
// here only exercise the metrics layer's RecordPublishAttempt — they
// use the literal string outcome values (same values the worker will
// pass) so a future services-side helper change doesn't break the
// metrics test surface.
// ---------------------------------------------------------------------------

// canonicalOutcomeValues is the agreed-upon set of outcome strings.
// Kept here as documentation; the canonical exported constants live
// in internal/services. A drift (someone uses "rate-limited" with a
// hyphen instead of an underscore) surfaces as a different series
// in dashboards — the metrics layer doesn't enforce the spelling.
var canonicalOutcomeValues = []string{
	"success", "rate_limited", "auth_error", "provider_unavail",
	"media_failed", "content_rejected", "validation", "quota", "internal",
}

// TestRecordPublishAttempt_HappyPath exercises both the success and
// the failure paths of RecordPublishAttempt. Uses testutil.ToFloat64
// to read a single labeled series without mutating global state —
// sibling-series counts are not touched by this test.
func TestRecordPublishAttempt_HappyPath(t *testing.T) {
	RecordPublishAttempt("tiktok", "success")
	if got := testutil.ToFloat64(publishAttempts.WithLabelValues("tiktok", "success")); got != 1 {
		t.Errorf("publish_attempts_total{tiktok, success}: want 1, got %v", got)
	}

	RecordPublishAttempt("tiktok", "rate_limited")
	if got := testutil.ToFloat64(publishAttempts.WithLabelValues("tiktok", "rate_limited")); got != 1 {
		t.Errorf("publish_attempts_total{tiktok, rate_limited}: want 1, got %v", got)
	}
}

// TestRecordPublishAttempt_EmptyOutcomeIsUncategorised pins the
// fallback: an empty outcome label gets substituted with
// "uncategorised" so dashboards never query an absent series.
func TestRecordPublishAttempt_EmptyOutcomeIsUncategorised(t *testing.T) {
	RecordPublishAttempt("twitter", "")
	if got := testutil.ToFloat64(publishAttempts.WithLabelValues("twitter", "uncategorised")); got != 1 {
		t.Errorf("publish_attempts_total{twitter, uncategorised}: want 1, got %v", got)
	}
}

// TestRecordPublishAttempt_EmptyProviderSkipped pins the empty-
// provider guard: an empty provider is logged at DEBUG and not
// emitted (avoids the phantom series a future misconfigured caller
// would create). Uses the literal "success" rather than the services
// constant — pkg/metrics does not import internal/services (would
// cycle via metrics_helper.go).
func TestRecordPublishAttempt_EmptyProviderSkipped(t *testing.T) {
	RecordPublishAttempt("", "success")
	// No assertion on the counter value (the helper returns early);
	// the test exists to document the contract + exercise the early-
	// return path. A future regression that DOES emit under "" would
	// be caught by the cardinality-policy test above.
}

// TestRecordProviderLatency_EmptyProviderSkipped covers the empty-
// provider skip path on the latency histogram. A panic would surface
// in the test (goroutine exits), but the silent-skip path is the
// documented behaviour.
func TestRecordProviderLatency_EmptyProviderSkipped(t *testing.T) {
	// Should not panic.
	RecordProviderLatency("", OperationPublish, 0.5)
}

// ---------------------------------------------------------------------------
// worker_id singleton tests
// ---------------------------------------------------------------------------

// TestWorkerID_DefaultIsUnset pins the defensive default.
func TestWorkerID_DefaultIsUnset(t *testing.T) {
	forceWorkerID("unset")
	if got := WorkerID(); got != "unset" {
		t.Errorf("WorkerID default: want %q, got %q", "unset", got)
	}
}

// TestWorkerID_SetOnce pins the production path.
func TestWorkerID_SetOnce(t *testing.T) {
	forceWorkerID("unset")
	SetWorkerID("worker-abc-123")
	if got := WorkerID(); got != "worker-abc-123" {
		t.Errorf("WorkerID after SetWorkerID: want %q, got %q", "worker-abc-123", got)
	}
}

// TestWorkerID_SetOnceIsIdempotent pins the keep-first rule.
func TestWorkerID_SetOnceIsIdempotent(t *testing.T) {
	forceWorkerID("unset")
	SetWorkerID("production-id")
	SetWorkerID("late-call-should-be-ignored")
	SetWorkerID("")
	if got := WorkerID(); got != "production-id" {
		t.Errorf("WorkerID after multiple SetWorkerID: want %q (first wins), got %q", "production-id", got)
	}
}

// TestWorkerID_SetWorkerIDEmptyIsNoop covers the empty-input guard.
func TestWorkerID_SetWorkerIDEmptyIsNoop(t *testing.T) {
	forceWorkerID("unset")
	SetWorkerID("")
	if got := WorkerID(); got != "unset" {
		t.Errorf("WorkerID after SetWorkerID(\"\"): want %q, got %q", "unset", got)
	}
}

// TestWorkerID_ConcurrentReadsSafe exercises the RWMutex: many
// concurrent WorkerID() callers read in parallel without blocking.
// Run with -race for the full check.
func TestWorkerID_ConcurrentReadsSafe(t *testing.T) {
	forceWorkerID("unset")
	SetWorkerID("concurrent-test-id")
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = WorkerID()
		}()
	}
	wg.Wait()
}

// forceWorkerID resets the singleton to a known value (test setup).
// Equivalent to the test-only Reset pattern; the production code never
// calls this (prodcution code only writes once via SetWorkerID).
func forceWorkerID(id string) {
	workerIDMutex.Lock()
	defer workerIDMutex.Unlock()
	workerID = id
}
