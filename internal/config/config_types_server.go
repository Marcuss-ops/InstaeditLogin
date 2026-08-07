package config

// MonitoringConfig holds observability and metrics configuration.
type MonitoringConfig struct {
	// Sentry (optional, Blocco #5.3).
	//
	// SENTRY_DSN is the SDK DSN string (`https://key@sentry.io/projid`).
	// Empty (the default) disables the entire observability surface:
	//   - sentry.Init is NOT called at startup.
	//   - the panic-catching middleware falls back to a plain
	//     `recover(http.Handler)` that writes 500 with NO outbound
	//     network traffic.
	// - Non-empty: sentry.Init runs at startup; the panic-catching
	//   middleware wraps with sentryhttp.New so CaptureException is
	//   called for every recovered panic and the SDK buffers out-of-band.
	SentryDSN string
	// SENTRY_ENVIRONMENT defaults to AppEnv ("dev"/"staging"/"production")
	// when empty; SENTRY_RELEASE is passed straight through to the SDK.
	SentryEnvironment string
	SentryRelease     string

	// Metrics basic-auth credentials. In production both must be set;
	// validate() fail-closes the boot if either is empty.
	MetricsBasicAuthUser string
	MetricsBasicAuthPass string
	// MetricsHost/MetricsPort optionally start a separate internal
	// listener for the /metrics endpoint. When MetricsPort is 0, the
	// endpoint is served only on the main HTTP server at
	// /api/v1/metrics. When MetricsPort > 0, an additional listener
	// is started on MetricsHost:MetricsPort (default MetricsHost
	// 127.0.0.1 if empty) so scrapers on a private network can reach
	// metrics without exposing the main API.
	MetricsHost string
	MetricsPort int
}

// HTTPConfig holds HTTP/server surface configuration.
type HTTPConfig struct {
	// FrontendURL is where the OAuth callback should redirect.
	FrontendURL string
	// EditorURL is the base URL of the InstaEditor SPA. When empty,
	// FrontendURL is used as a fallback.
	EditorURL string
	// AllowedCORSOrigins is the comma-separated list of origins.
	AllowedCORSOrigins []string
	// CookieDomain is the optional `Domain` attribute applied to the
	// csrf_token cookie ONLY (session + refresh cookies stay host-only).
	CookieDomain string
	// LogLevel is one of "debug" or "info" (default "info").
	LogLevel string
	// AppEnv is the deployment environment: dev|staging|production.
	AppEnv string
}
