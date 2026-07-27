package credentials

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
	configtest.ClearOptionalOAuthEnv()
	// Provide dummy database and storage credentials so config.Load()
	// passes validate() without requiring real infrastructure.
	os.Setenv("DATABASE_URL", "postgresql://test:test@localhost:5432/test?sslmode=disable")
	os.Setenv("S3_ENDPOINT", "http://localhost:9000")
	os.Setenv("S3_BUCKET", "test-bucket")
	os.Setenv("S3_ACCESS_KEY", "test-key")
	os.Setenv("S3_SECRET_KEY", "test-secret")
	os.Setenv("ENCRYPTION_KEY", "YWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWE=") // base64 32-byte key
	os.Setenv("JWT_SECRET", "this_is_a_test_secret_at_least_32_bytes_long_xx")
	os.Exit(m.Run())
}
