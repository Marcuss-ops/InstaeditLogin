// Package configtest provides helpers for tests that load
// config.Config in production mode without requiring real metrics
// basic-auth credentials.
package configtest

import "os"

// SetDummyMetricsAuth sets non-empty dummy values for the metrics
// basic-auth env vars so that config.Load() can succeed in
// production mode during tests.
func SetDummyMetricsAuth() {
	os.Setenv("METRICS_BASIC_AUTH_USER", "dummy-metrics-user")
	os.Setenv("METRICS_BASIC_AUTH_PASS", "dummy-metrics-pass")
}
