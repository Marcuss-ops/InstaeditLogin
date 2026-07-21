package services

import (
	"os"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/config/configtest"
)

// TestMain sets up the environment for all tests in this package.
// Several tests call config.Load(), which now requires metrics
// basic-auth credentials when APP_ENV=production. Providing dummy
// values here avoids leaking that requirement into every test.
func TestMain(m *testing.M) {
	configtest.SetDummyMetricsAuth()
	os.Exit(m.Run())
}
