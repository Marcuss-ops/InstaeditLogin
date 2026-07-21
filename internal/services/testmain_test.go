package services

import (
	"os"
	"testing"
)

// TestMain sets up the environment for all tests in this package.
// Several tests call config.Load(), which now requires metrics
// basic-auth credentials when APP_ENV=production. Providing dummy
// values here avoids leaking that requirement into every test.
func TestMain(m *testing.M) {
	os.Setenv("METRICS_BASIC_AUTH_USER", "dummy-metrics-user")
	os.Setenv("METRICS_BASIC_AUTH_PASS", "dummy-metrics-pass")
	os.Exit(m.Run())
}
