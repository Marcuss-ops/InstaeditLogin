package metrics

import "github.com/prometheus/client_golang/prometheus"

var legacyJobEndpointUsage = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "legacy_job_endpoint_usage_total",
		Help: "Legacy video/job creation endpoint requests, labeled by endpoint and outcome.",
	},
	[]string{"endpoint", "outcome"},
)

func init() {
	prometheus.MustRegister(legacyJobEndpointUsage)
}

const (
	LegacyJobEndpointVeloxJobs = "/api/v1/velox/jobs"
	LegacyJobOutcomeAccepted   = "accepted"
	LegacyJobOutcomeAuth       = "auth_error"
	LegacyJobOutcomeBadRequest = "bad_request"
	LegacyJobOutcomeValidation = "validation_error"
	LegacyJobOutcomeUpstream   = "upstream_error"
	LegacyJobOutcomeMismatch   = "workspace_mismatch"
)

// LegacyJobEndpointUsageCounter exposes one bounded labeled series for tests
// and diagnostics without exposing the mutable CounterVec itself.
func LegacyJobEndpointUsageCounter(endpoint, outcome string) prometheus.Counter {
	if endpoint != LegacyJobEndpointVeloxJobs {
		endpoint = "unknown"
	}
	switch outcome {
	case LegacyJobOutcomeAccepted, LegacyJobOutcomeAuth, LegacyJobOutcomeBadRequest, LegacyJobOutcomeValidation, LegacyJobOutcomeUpstream, LegacyJobOutcomeMismatch:
	default:
		outcome = "unknown"
	}
	return legacyJobEndpointUsage.WithLabelValues(endpoint, outcome)
}

// RecordLegacyJobEndpointUsage records one legacy creation request. Endpoint
// and outcome are fixed vocabulary values to prevent unbounded label growth.
func RecordLegacyJobEndpointUsage(endpoint, outcome string) {
	if endpoint != LegacyJobEndpointVeloxJobs {
		endpoint = "unknown"
	}
	switch outcome {
	case LegacyJobOutcomeAccepted, LegacyJobOutcomeAuth, LegacyJobOutcomeBadRequest, LegacyJobOutcomeValidation, LegacyJobOutcomeUpstream, LegacyJobOutcomeMismatch:
	default:
		outcome = "unknown"
	}
	LegacyJobEndpointUsageCounter(endpoint, outcome).Inc()
}
