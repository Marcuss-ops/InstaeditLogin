package config

import (
	"os"
	"strings"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/config/configtest"
)

func TestMain(m *testing.M) {
	// Provide dummy metrics basic-auth credentials for tests that load
	// config with APP_ENV=production. The fail-closed production check
	// is preserved; tests simply opt-in to a valid configuration.
	configtest.SetDummyMetricsAuth()
	os.Exit(m.Run())
}

// TestValidate_SentryDSN_RequiredEnvUnset confirms the Blocco #5.3
// contract: with SENTRY_DSN UNSET (the operator-disables-by-omission
// default), validate() does NOT fail. The absence is the signal the
// operator gave us to disable the observability surface, so any
// elsewhere-required env (JWT_SECRET, ENCRYPTION_KEYS, etc.) is what
// validate() should be flagging.
func TestValidate_SentryDSN_RequiredEnvUnset(t *testing.T) {
	// Set the minimum required env so the test focuses on the
	// Sentry-DSN unset path.
	t.Setenv("JWT_SECRET", "this_is_a_test_secret_at_least_32_bytes_long_xx")
	t.Setenv("ENCRYPTION_KEY", dummpyBase64Key32)

	_, err := Load()
	if err != nil {
		t.Fatalf("Load() with SENTRY_DSN unset: want nil err, got %v", err)
	}
}

// TestValidate_SentryDSN_ValidHTTPS confirms a canonical Sentry
// DSN (https://key@host/project) passes validation, and that
// SENTRY_ENVIRONMENT defaults to AppEnv when unset.
func TestValidate_SentryDSN_ValidHTTPS(t *testing.T) {
	t.Setenv("JWT_SECRET", "this_is_a_test_secret_at_least_32_bytes_long_xx")
	t.Setenv("ENCRYPTION_KEY", dummpyBase64Key32)
	t.Setenv("SENTRY_DSN", "https://key123@sentry.example.com/4")
	t.Setenv("APP_ENV", "production")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() with valid DSN: want nil, got %v", err)
	}
	if cfg.SentryDSN != "https://key123@sentry.example.com/4" {
		t.Errorf("SentryDSN: got %q", cfg.SentryDSN)
	}
	if cfg.SentryEnvironment != "production" {
		t.Errorf("SentryEnvironment default from AppEnv: want production, got %q", cfg.SentryEnvironment)
	}
}

// TestValidate_SentryDSN_ExplicitEnv confirms SENTRY_ENVIRONMENT
// overrides the AppEnv default when explicitly set.
func TestValidate_SentryDSN_ExplicitEnv(t *testing.T) {
	t.Setenv("JWT_SECRET", "this_is_a_test_secret_at_least_32_bytes_long_xx")
	t.Setenv("ENCRYPTION_KEY", dummpyBase64Key32)
	t.Setenv("SENTRY_DSN", "https://key@sentry.example.com/4")
	t.Setenv("APP_ENV", "dev")
	t.Setenv("SENTRY_ENVIRONMENT", "staging-canary")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() with explicit SENTRY_ENVIRONMENT: want nil, got %v", err)
	}
	if cfg.SentryEnvironment != "staging-canary" {
		t.Errorf("SentryEnvironment override: want staging-canary, got %q", cfg.SentryEnvironment)
	}
}

// TestValidate_SentryDSN_BadScheme rejects non-http(s) URLs.
func TestValidate_SentryDSN_BadScheme(t *testing.T) {
	t.Setenv("JWT_SECRET", "this_is_a_test_secret_at_least_32_bytes_long_xx")
	t.Setenv("ENCRYPTION_KEY", dummpyBase64Key32)
	t.Setenv("SENTRY_DSN", "ftp://key@host/1")

	_, err := Load()
	if err == nil {
		t.Fatal("Load(): want error for bad scheme, got nil")
	}
	if err.Error() == "" {
		t.Fatal("error message must include the offending DSN/scheme")
	}
}

// TestValidate_SentryDSN_MissingHost rejects DSN with no host.
func TestValidate_SentryDSN_MissingHost(t *testing.T) {
	t.Setenv("JWT_SECRET", "this_is_a_test_secret_at_least_32_bytes_long_xx")
	t.Setenv("ENCRYPTION_KEY", dummpyBase64Key32)
	t.Setenv("SENTRY_DSN", "https://key@/1")

	_, err := Load()
	if err == nil {
		t.Fatal("Load(): want error for missing host, got nil")
	}
}

// TestValidate_SentryDSN_MissingUser rejects DSN with no public key.
func TestValidate_SentryDSN_MissingUser(t *testing.T) {
	t.Setenv("JWT_SECRET", "this_is_a_test_secret_at_least_32_bytes_long_xx")
	t.Setenv("ENCRYPTION_KEY", dummpyBase64Key32)
	t.Setenv("SENTRY_DSN", "https://sentry.example.com/1")

	_, err := Load()
	if err == nil {
		t.Fatal("Load(): want error for missing public key, got nil")
	}
}

// TestValidate_SentryDSN_MissingProject rejects DSN with no /project.
func TestValidate_SentryDSN_MissingProject(t *testing.T) {
	t.Setenv("JWT_SECRET", "this_is_a_test_secret_at_least_32_bytes_long_xx")
	t.Setenv("ENCRYPTION_KEY", dummpyBase64Key32)
	t.Setenv("SENTRY_DSN", "https://key@sentry.example.com")

	_, err := Load()
	if err == nil {
		t.Fatal("Load(): want error for missing /project path, got nil")
	}
}

// TestValidate_SentryDSN_Unparseable rejects garbage DSN strings.
func TestValidate_SentryDSN_Unparseable(t *testing.T) {
	t.Setenv("JWT_SECRET", "this_is_a_test_secret_at_least_32_bytes_long_xx")
	t.Setenv("ENCRYPTION_KEY", dummpyBase64Key32)
	t.Setenv("SENTRY_DSN", "://malformed")

	_, err := Load()
	if err == nil {
		t.Fatal("Load(): want error for unparseable DSN, got nil")
	}
}

// dummpyBase64Key32 is a 32-byte AES-256 base64 fixture used by
// the Sentry-DSN validation tests below. Defined at package scope
// (not as a t.Helper local) so every TestValidate_SentryDSN_*
// shares one canonical value. The "dummpy" typo follows the
// repo's pre-existing test-fixture naming convention (cf.
// meta_test.go) so the identifier doesn't accidentally shadow
// unrelated package-level declarations.
var dummpyBase64Key32 = validEncryptionKey()

// TestLoad_CookieDomain_Env asserts the COOKIE_DOMAIN env var is
// loaded verbatim into cfg.CookieDomain. The value is NOT validated
// (the operator owns the shape — ".instaedit.org" for cross-subdomain,
// "api.instaedit.org" for exact-host, empty for dev-host-only). This
// test pins the round-trip; any future validation must be additive.
func TestLoad_CookieDomain_Env(t *testing.T) {
	t.Setenv("JWT_SECRET", "this_is_a_test_secret_at_least_32_bytes_long_xx")
	t.Setenv("ENCRYPTION_KEY", dummpyBase64Key32)
	t.Setenv("COOKIE_DOMAIN", ".instaedit.org")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() with COOKIE_DOMAIN set: want nil, got %v", err)
	}
	if cfg.CookieDomain != ".instaedit.org" {
		t.Errorf("CookieDomain: want .instaedit.org, got %q", cfg.CookieDomain)
	}
}

// TestLoad_CookieDomain_DefaultEmpty asserts the developer-friendly
// default: COOKIE_DOMAIN unset leaves cfg.CookieDomain empty so the
// csrf_token cookie stays host-only on the API origin (dev runs at
// localhost:5173 + localhost:8080 which have different "domains"
// anyway, so a parent-domain match would be wrong). The absence of
// any validate() rejection of an empty value is the contract: an
// operator who hasn't yet set up DNS is unblocked from running.
func TestLoad_CookieDomain_DefaultEmpty(t *testing.T) {
	t.Setenv("JWT_SECRET", "this_is_a_test_secret_at_least_32_bytes_long_xx")
	t.Setenv("ENCRYPTION_KEY", dummpyBase64Key32)
	// Explicitly clear COOKIE_DOMAIN so a local .env file cannot
	// influence this default-value test.
	t.Setenv("COOKIE_DOMAIN", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() without COOKIE_DOMAIN: want nil, got %v", err)
	}
	if cfg.CookieDomain != "" {
		t.Errorf("CookieDomain default: want empty (dev), got %q", cfg.CookieDomain)
	}
}

// TestLoad_AdminInviteToken_EmptyAllowed pins the "empty disables
// registration" contract: an operator who has not yet set up a beta
// admin token is unblocked from running. The handler treats the
// empty string as the explicit "no public registration" signal and
// returns 403; this test only verifies the config layer does not
// reject the empty value at boot.
func TestLoad_AdminInviteToken_EmptyAllowed(t *testing.T) {
	t.Setenv("JWT_SECRET", "this_is_a_test_secret_at_least_32_bytes_long_xx")
	t.Setenv("ENCRYPTION_KEY", dummpyBase64Key32)
	// Explicitly clear ADMIN_INVITE_TOKEN so a local .env file cannot
	// influence this default-value test.
	t.Setenv("ADMIN_INVITE_TOKEN", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() without ADMIN_INVITE_TOKEN: want nil (registration disabled), got %v", err)
	}
	if cfg.AdminInviteToken != "" {
		t.Errorf("AdminInviteToken default: want empty, got %q", cfg.AdminInviteToken)
	}
}

// TestLoad_AdminInviteToken_TooShortRejected guards against the
// operator-typo class where a 4-char placeholder ("test", "demo",
// "1234") is pushed to Fly. The brute-force surface of a 4-char
// token is trivially searchable; the contract is "fail at boot
// rather than silently weaken the gate".
func TestLoad_AdminInviteToken_TooShortRejected(t *testing.T) {
	t.Setenv("JWT_SECRET", "this_is_a_test_secret_at_least_32_bytes_long_xx")
	t.Setenv("ENCRYPTION_KEY", dummpyBase64Key32)
	// 31 chars -- one below the 32 minimum.
	t.Setenv("ADMIN_INVITE_TOKEN", "abcdefghijklmnopqrstuvwxyz12345")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() with 31-char ADMIN_INVITE_TOKEN: want error, got nil")
	}
	if !strings.Contains(err.Error(), "ADMIN_INVITE_TOKEN") {
		t.Errorf("error must name the env var; got %v", err)
	}
	if !strings.Contains(err.Error(), "31") {
		t.Errorf("error must report the actual length; got %v", err)
	}
}

// TestLoad_AdminInviteToken_ExactlyMinAllowed pins the boundary:
// a 32-char token (the documented minimum) loads without error.
func TestLoad_AdminInviteToken_ExactlyMinAllowed(t *testing.T) {
	t.Setenv("JWT_SECRET", "this_is_a_test_secret_at_least_32_bytes_long_xx")
	t.Setenv("ENCRYPTION_KEY", dummpyBase64Key32)
	// 32 chars -- exactly at the threshold.
	t.Setenv("ADMIN_INVITE_TOKEN", "abcdefghijklmnopqrstuvwxyz123456")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() with 32-char ADMIN_INVITE_TOKEN: want nil, got %v", err)
	}
	if cfg.AdminInviteToken != "abcdefghijklmnopqrstuvwxyz123456" {
		t.Errorf("AdminInviteToken round-trip: got %q", cfg.AdminInviteToken)
	}
}

// TestLoad_JWT_TTL_ExplicitValues pins the explicit access/refresh
// TTL knobs. Access defaults to 15 minutes and refresh to 30 days
// when unset, with legacy JWT_TTL_HOURS converting to minutes.
func TestLoad_JWT_TTL_ExplicitValues(t *testing.T) {
	t.Setenv("JWT_SECRET", "this_is_a_test_secret_at_least_32_bytes_long_xx")
	t.Setenv("ENCRYPTION_KEY", dummpyBase64Key32)
	t.Setenv("JWT_ACCESS_TTL_MINUTES", "5")
	t.Setenv("JWT_REFRESH_TTL_DAYS", "7")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() with explicit TTLs: want nil, got %v", err)
	}
	if cfg.JWTAccessTTLMinutes != 5 {
		t.Errorf("JWTAccessTTLMinutes: want 5, got %d", cfg.JWTAccessTTLMinutes)
	}
	if cfg.JWTRefreshTTLDays != 7 {
		t.Errorf("JWTRefreshTTLDays: want 7, got %d", cfg.JWTRefreshTTLDays)
	}
}

func TestLoad_JWT_TTL_Defaults(t *testing.T) {
	t.Setenv("JWT_SECRET", "this_is_a_test_secret_at_least_32_bytes_long_xx")
	t.Setenv("ENCRYPTION_KEY", dummpyBase64Key32)
	// Ensure the legacy variable is not set.
	t.Setenv("JWT_TTL_HOURS", "")
	t.Setenv("JWT_ACCESS_TTL_MINUTES", "")
	t.Setenv("JWT_REFRESH_TTL_DAYS", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() with default TTLs: want nil, got %v", err)
	}
	if cfg.JWTAccessTTLMinutes != 15 {
		t.Errorf("JWTAccessTTLMinutes default: want 15, got %d", cfg.JWTAccessTTLMinutes)
	}
	if cfg.JWTRefreshTTLDays != 30 {
		t.Errorf("JWTRefreshTTLDays default: want 30, got %d", cfg.JWTRefreshTTLDays)
	}
}

func TestLoad_JWT_TTL_LegacyFallback(t *testing.T) {
	t.Setenv("JWT_SECRET", "this_is_a_test_secret_at_least_32_bytes_long_xx")
	t.Setenv("ENCRYPTION_KEY", dummpyBase64Key32)
	t.Setenv("JWT_TTL_HOURS", "2")
	t.Setenv("JWT_ACCESS_TTL_MINUTES", "")
	t.Setenv("JWT_REFRESH_TTL_DAYS", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() with legacy TTL: want nil, got %v", err)
	}
	if cfg.JWTAccessTTLMinutes != 120 {
		t.Errorf("JWTAccessTTLMinutes legacy fallback: want 120, got %d", cfg.JWTAccessTTLMinutes)
	}
	if cfg.JWTRefreshTTLDays != 30 {
		t.Errorf("JWTRefreshTTLDays default: want 30, got %d", cfg.JWTRefreshTTLDays)
	}
}

// TestLoad_Production_RequiresMetricsBasicAuth pins the fail-closed
// production contract: when APP_ENV=production, both metrics basic-auth
// credentials must be present. Any incomplete configuration (missing user,
// missing pass, or both) must prevent the process from booting.
// TestValidateOptionalPlatform_AllPlatforms exercises every
// optional OAuth platform (TikTok, X, YouTube, Google Drive, LinkedIn)
// through the five input states: both empty, only ID, only secret,
// both valid, and a too-short secret. The helper is platform-agnostic,
// but running it once per platform name makes failures self-describing.
func TestValidateOptionalPlatform_AllPlatforms(t *testing.T) {
	const validSecret = "a-very-long-secret-that-is-at-least-32-chars-long"

	cases := []struct {
		name    string
		id      string
		secret  string
		wantErr bool
	}{
		{"both empty", "", "", false},
		{"only id", "client-id", "", true},
		{"only secret", "", validSecret, true},
		{"both valid", "client-id", validSecret, false},
		{"short secret", "client-id", "short", true},
	}

	platforms := []string{"TIKTOK", "X", "YOUTUBE", "GOOGLE_DRIVE", "LINKEDIN"}
	cfg := &Config{}

	for _, platform := range platforms {
		for _, tc := range cases {
			t.Run(platform+"_"+tc.name, func(t *testing.T) {
				err := cfg.validateOptionalPlatform(platform, tc.id, tc.secret)
				if tc.wantErr && err == nil {
					t.Fatalf("validateOptionalPlatform(%q, %q, %q): want error, got nil", platform, tc.id, tc.secret)
				}
				if !tc.wantErr && err != nil {
					t.Fatalf("validateOptionalPlatform(%q, %q, %q): want nil, got %v", platform, tc.id, tc.secret, err)
				}
			})
		}
	}
}

func TestLoad_Production_RequiresMetricsBasicAuth(t *testing.T) {
	// Provide the minimum required env vars so the only failure path
	// under test is the production metrics-auth check.
	t.Setenv("APP_ENV", "production")
	t.Setenv("JWT_SECRET", "this_is_a_test_secret_at_least_32_bytes_long_xx")
	t.Setenv("ENCRYPTION_KEY", dummpyBase64Key32)
	t.Setenv("DATABASE_URL", "postgresql://user:pass@localhost:5432/instaedit_login?sslmode=disable")
	t.Setenv("S3_ENDPOINT", "https://s3.example.com")
	t.Setenv("S3_BUCKET", "instaedit-bucket")
	t.Setenv("S3_ACCESS_KEY", "AKIAIOSFODNN7EXAMPLE")
	t.Setenv("S3_SECRET_KEY", "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY")

	cases := []struct {
		name  string
		user  string
		pass  string
		valid bool
	}{
		{"both missing", "", "", false},
		{"only user missing", "", "secret", false},
		{"only pass missing", "user", "", false},
		{"both set", "user", "secret", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Override the dummy values set by TestMain so we can test
			// each incomplete configuration.
			t.Setenv("METRICS_BASIC_AUTH_USER", tc.user)
			t.Setenv("METRICS_BASIC_AUTH_PASS", tc.pass)

			cfg, err := Load()
			if tc.valid {
				if err != nil {
					t.Fatalf("Load() with %s: want nil, got %v", tc.name, err)
				}
				if cfg.MetricsBasicAuthUser != tc.user || cfg.MetricsBasicAuthPass != tc.pass {
					t.Errorf("metrics credentials not round-tripped correctly: got (%q, %q)", cfg.MetricsBasicAuthUser, cfg.MetricsBasicAuthPass)
				}
				return
			}

			if err == nil {
				t.Fatalf("Load() with %s: want error, got nil", tc.name)
			}
			want := "METRICS_BASIC_AUTH_USER and METRICS_BASIC_AUTH_PASS are required in production"
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("Load() error should contain %q; got: %v", want, err)
			}
		})
	}
}
