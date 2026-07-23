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

// ClearOptionalOAuthEnv masks optional OAuth provider credentials
// that may be present in a local .env file. godotenv.Load() does
// not overwrite environment variables that are already set, so
// pre-setting these keys to an empty string forces the config
// loader to treat the platforms as disabled unless a test
// explicitly enables them with t.Setenv.
func ClearOptionalOAuthEnv() {
	vars := []string{
		"META_APP_ID",
		"META_APP_SECRET",
		"TIKTOK_CLIENT_KEY",
		"TIKTOK_CLIENT_ID",
		"TIKTOK_CLIENT_SECRET",
		"X_CLIENT_ID",
		"X_CLIENT_SECRET",
		"YOUTUBE_CLIENT_ID",
		"YOUTUBE_CLIENT_SECRET",
		"GOOGLE_DRIVE_CLIENT_ID",
		"GOOGLE_DRIVE_CLIENT_SECRET",
		"LINKEDIN_CLIENT_ID",
		"LINKEDIN_CLIENT_SECRET",
	}
	for _, v := range vars {
		os.Setenv(v, "")
	}
}
