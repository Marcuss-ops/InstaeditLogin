package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

// Vault refresh observability (C6). refreshOperationTimeout caps each
// attempt at 30s, but nothing measured HOW OFTEN the slow path runs, how
// long renewals take, or how many callers joined an existing flight —
// exactly the signals that catch provider degradation and shared-grant
// queueing before callers start timing out.
//
// Observable facts map 1:1 to series (no guessed gauges):
//   - flights_total      — slow-path refreshes started (singleflight leaders).
//   - flight_shared_total— callers that received a Shared result (joined an
//     in-flight renewal instead of leading one). Sustained growth here means
//     hot shared grants are queueing behind each other.
//   - slow_path_duration — wall-clock slow-path renew latency per caller,
//     including singleflight wait time.
//
// Labels are bounded by design: outcome ∈ {success, error, unknown} and
// token_type is the canonical token type; no account or connection
// identifiers ever become label values (unbounded series).

var vaultRefreshFlightsTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "vault_refresh_flights_total",
		Help: "Slow-path credential-vault refreshes started (one per grant-keyed singleflight leader).",
	},
	[]string{"token_type"},
)

var vaultRefreshFlightSharedTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "vault_refresh_flight_shared_total",
		Help: "Callers that joined an in-flight grant-keyed refresh (singleflight Shared result) instead of leading a new one.",
	},
	[]string{"token_type"},
)

var vaultRefreshSlowPathDuration = prometheus.NewHistogramVec(
	prometheus.HistogramOpts{
		Name: "vault_refresh_slow_path_duration_seconds",
		Help: "Wall-clock duration of credential-vault renewal attempts per caller (slow path, includes singleflight wait; pre-cancelled calls record 0).",
		Buckets: []float64{
			0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30,
		},
	},
	[]string{"outcome", "token_type"},
)

func init() {
	prometheus.MustRegister(vaultRefreshFlightsTotal)
	prometheus.MustRegister(vaultRefreshFlightSharedTotal)
	prometheus.MustRegister(vaultRefreshSlowPathDuration)
}

// RecordVaultRefreshFlightStart counts one slow-path renewal started
// (singleflight leader).
func RecordVaultRefreshFlightStart(tokenType string) {
	if tokenType == "" {
		tokenType = "unknown"
	}
	vaultRefreshFlightsTotal.WithLabelValues(tokenType).Inc()
}

// RecordVaultRefreshFlightShared counts one caller that joined an existing
// refresh flight and received the shared result.
func RecordVaultRefreshFlightShared(tokenType string) {
	if tokenType == "" {
		tokenType = "unknown"
	}
	vaultRefreshFlightSharedTotal.WithLabelValues(tokenType).Inc()
}

// RecordVaultRefreshSlowPath observes one completed slow-path renewal.
// outcome is "success" or "error"; unknown/empty values are bucketed under
// "unknown" so no unbounded label series can be created.
func RecordVaultRefreshSlowPath(outcome, tokenType string, seconds float64) {
	if outcome == "" {
		outcome = "unknown"
	}
	if tokenType == "" {
		tokenType = "unknown"
	}
	vaultRefreshSlowPathDuration.WithLabelValues(outcome, tokenType).Observe(seconds)
}
