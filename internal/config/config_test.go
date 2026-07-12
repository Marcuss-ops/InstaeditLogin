package config

import (
	"encoding/base64"
	"strings"
	"testing"
)

// minValid32ByteBase64Key is base64.StdEncoding.EncodeToString(make([]byte, 32))
// i.e. the base64 encoding of 32 zero bytes. validate() rejects any other
// length, so every test that needs to reach later checks seeds EncryptionKey
// with this exact value.
//
// Math: 32 bytes = 10 groups of "AAAA" (40 chars) + "AAA=" for the 2-byte
// tail (4 chars) = 44 total chars (43 'A' + 1 '=').
const minValid32ByteBase64Key = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="

// validMetaSecret32 is exactly secretMinChars chars long — the smallest
// META_APP_SECRET that passes validate(). Tests that need to exercise the
// length boundary start from this baseline and override on top of it.
const validMetaSecret32 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" // 32 a's

// minimalValidConfig returns a *Config that passes every validate() check
// except whatever the caller overrides via jwtSecret. Every secret lives at
// its minimum-acceptable length so each subtest overrides exactly one field
// and failures point at a single cause. Optional OAuth platforms are
// intentionally left empty → disabled (passes by default).
//
// AppEnv is seeded to "dev" so the AppEnv switch in validate() passes —
// tests that exercise credential lengths don't have to know about the
// AppEnv check. Tests that DO want to exercise AppEnv validation
// (TestValidate_AppEnv, TestLoad_AppEnv_*) assert on the env-var-driven
// path explicitly.
//
// Taglio 2.4 follow-up: S3_* fields are seeded with syntactically-valid
// placeholders so the S3 mandatory check (Taglio 3.1) passes. The values
// don't need to be real AWS credentials — validate() only checks for
// non-emptiness, not reachability.
func minimalValidConfig(jwtSecret string) *Config {
	return &Config{
		AppEnv:        "dev",
		DatabaseURL:   "postgres://x",
		MetaAppID:     "meta-id",
		MetaAppSecret: validMetaSecret32,
		EncryptionKey: minValid32ByteBase64Key,
		JWTSecret:     jwtSecret,
		S3Endpoint:    "https://s3.example.com",
		S3Bucket:      "test-bucket",
		S3AccessKey:   "test-access-key",
		S3SecretKey:   "test-secret-key",
	}
}

// validJWTSecret returns a JWT_SECRET that satisfies jwtSecretMinBytes so
// tests that focus on other fields don't have to assemble their own.
func validJWTSecret() string { return strings.Repeat("a", jwtSecretMinBytes) }

func TestValidate_JWTSecretLength(t *testing.T) {
	tests := []struct {
		name      string
		secret    string
		wantErr   bool
		errSubstr string // substring expected in error when wantErr is true
	}{
		{
			name:      "empty secret fails with required-error",
			secret:    "",
			wantErr:   true,
			errSubstr: "JWT_SECRET is required",
		},
		{
			// 31 is the largest length that still fails the < 32 branch;
			// the off-by-one boundary that semantically matters.
			name:      "31-byte secret fails length check",
			secret:    strings.Repeat("a", 31),
			wantErr:   true,
			errSubstr: "at least 32 bytes",
		},
		{
			name:    "exactly 32-byte secret passes",
			secret:  strings.Repeat("a", 32),
			wantErr: false,
		},
		{
			name:    "64-byte secret passes (well above minimum)",
			secret:  strings.Repeat("a", 64),
			wantErr: false,
		},
		{
			// 11 × '€' = 33 bytes but only 11 runes. A naive rune-counting
			// check would reject this (< 32 runes); the byte-counting check
			// accepts it (>= 32 bytes). Proves the check counts UTF-8 bytes.
			name:    "11 multi-byte runes (33 bytes) passes byte check",
			secret:  strings.Repeat("€", 11),
			wantErr: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := minimalValidConfig(tc.secret)
			err := cfg.validate()
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.errSubstr)
				}
				if tc.errSubstr != "" && !strings.Contains(err.Error(), tc.errSubstr) {
					t.Fatalf("expected error containing %q, got %q", tc.errSubstr, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
		})
	}
}

func TestValidate_MetaAppSecretLength(t *testing.T) {
	tests := []struct {
		name      string
		secret    string
		wantErr   bool
		errSubstr string
	}{
		{
			name:      "empty secret fails with required-error",
			secret:    "",
			wantErr:   true,
			errSubstr: "META_APP_SECRET is required",
		},
		{
			name:      "31 chars fails length check (off-by-one boundary)",
			secret:    strings.Repeat("a", 31),
			wantErr:   true,
			errSubstr: "at least 32 characters",
		},
		{
			name:    "exactly 32 chars passes",
			secret:  strings.Repeat("a", 32),
			wantErr: false,
		},
		{
			name:    "much longer 64 chars passes",
			secret:  strings.Repeat("a", 64),
			wantErr: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := minimalValidConfig(validJWTSecret())
			cfg.MetaAppSecret = tc.secret
			err := cfg.validate()
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.errSubstr)
				}
				if tc.errSubstr != "" && !strings.Contains(err.Error(), tc.errSubstr) {
					t.Fatalf("expected error containing %q, got %q", tc.errSubstr, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
		})
	}
}

func TestValidate_EncryptionKeyLength(t *testing.T) {
	tests := []struct {
		name       string
		key        string
		wantErr    bool
		errSubstrs []string // every entry must appear in the error
	}{
		{
			name:       "empty fails required-error",
			key:        "",
			wantErr:    true,
			errSubstrs: []string{"ENCRYPTION_KEY is required"},
		},
		{
			// 16 bytes — too short for AES-256. The error MUST mention both
			// the actual and the expected length so the operator can act on
			// it without consulting docs.
			name:       "16-byte key fails with both got and expected",
			key:        base64.StdEncoding.EncodeToString(make([]byte, 16)),
			wantErr:    true,
			errSubstrs: []string{"got 16", "exactly 32"},
		},
		{
			name:       "64-byte key fails (wrong size for AES-256)",
			key:        base64.StdEncoding.EncodeToString(make([]byte, 64)),
			wantErr:    true,
			errSubstrs: []string{"got 64", "exactly 32"},
		},
		{
			name:    "exactly 32-byte key passes",
			key:     minValid32ByteBase64Key,
			wantErr: false,
		},
		{
			name:       "invalid base64 fails decode-error",
			key:        "@@@not-base64@@@",
			wantErr:    true,
			errSubstrs: []string{"not valid base64"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := minimalValidConfig(validJWTSecret())
			cfg.EncryptionKey = tc.key
			err := cfg.validate()
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error containing %v, got nil", tc.errSubstrs)
				}
				for _, want := range tc.errSubstrs {
					if !strings.Contains(err.Error(), want) {
						t.Fatalf("expected error containing %q, got %q", want, err.Error())
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
		})
	}
}

func TestValidate_OptionalPlatforms(t *testing.T) {
	// Reuse validMetaSecret32 instead of duplicating the 32-char 'a' literal —
	// single source of truth for "the smallest valid OAuth secret".
	tests := []struct {
		name      string
		platform  string // "TIKTOK" | "TWITTER" | "YOUTUBE" — wire helper below
		id        string
		secret    string
		wantErr   bool
		errSubstr string
	}{
		// TikTok
		{"tiktok both empty OK (platform disabled)", "TIKTOK", "", "", false, ""},
		{"tiktok key without secret fails (secret required)", "TIKTOK", "tiktok-key", "", true, "TIKTOK_CLIENT_SECRET is required"},
		{"tiktok 31-char secret fails length", "TIKTOK", "tiktok-key", strings.Repeat("a", 31), true, "at least 32 characters"},
		{"tiktok valid pair passes", "TIKTOK", "tiktok-key", validMetaSecret32, false, ""},
		// Twitter
		{"twitter both empty OK (platform disabled)", "TWITTER", "", "", false, ""},
		{"twitter id without secret fails (secret required)", "X", "x-id", "", true, "X_CLIENT_SECRET is required"},
		{"twitter 31-char secret fails length", "X", "x-id", strings.Repeat("a", 31), true, "at least 32 characters"},
		{"twitter valid pair passes", "X", "x-id", validMetaSecret32, false, ""},
		// YouTube
		{"youtube both empty OK (platform disabled)", "YOUTUBE", "", "", false, ""},
		{"youtube id without secret fails (secret required)", "YOUTUBE", "youtube-id", "", true, "YOUTUBE_CLIENT_SECRET is required"},
		{"youtube 31-char secret fails length", "YOUTUBE", "youtube-id", strings.Repeat("a", 31), true, "at least 32 characters"},
		{"youtube valid pair passes", "YOUTUBE", "youtube-id", validMetaSecret32, false, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := minimalValidConfig(validJWTSecret())

			// Wire the platform fields based on the platform name.
			// Could use the Config's own setters but the test is more
			// readable when the wiring is explicit near the assertion.
			switch tc.platform {
			case "TIKTOK":
				cfg.TikTokClientID = tc.id
				cfg.TikTokClientSecret = tc.secret
			case "X":
				cfg.XClientID = tc.id
				cfg.XClientSecret = tc.secret
			case "YOUTUBE":
				cfg.YouTubeClientID = tc.id
				cfg.YouTubeClientSecret = tc.secret
			}

			err := cfg.validate()
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.errSubstr)
				}
				if tc.errSubstr != "" && !strings.Contains(err.Error(), tc.errSubstr) {
					t.Fatalf("expected error containing %q, got %q", tc.errSubstr, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
		})
	}
}

func TestValidate_JWTSecretLengthDoesNotMaskEncryptionCheck(t *testing.T) {
	// Sanity: with MetaAppSecret valid and JWT_SECRET long enough, an empty
	// ENCRYPTION_KEY still produces an ENCRYPTION_KEY error (the new
	// JWT/JWTSecret length check doesn't short-circuit earlier validations).
	cfg := minimalValidConfig(strings.Repeat("a", 64))
	cfg.EncryptionKey = ""

	err := cfg.validate()
	if err == nil || !strings.Contains(err.Error(), "ENCRYPTION_KEY") {
		t.Fatalf("expected ENCRYPTION_KEY error before JWT length check, got %v", err)
	}
}

func TestValidate_MetaAppLengthDoesNotMaskEncryptionCheck(t *testing.T) {
	// Companion sanity: with a too-short META_APP_SECRET and valid JWT_SECRET,
	// validate() surfaces META_APP_SECRET first — confirms the new ordering
	// doesn't let META_APP_SECRET short-circuit into later checks (this
	// would catch a regression where someone reorders meta after encryption).
	cfg := minimalValidConfig(validJWTSecret())
	cfg.MetaAppSecret = strings.Repeat("a", 31) // too short

	err := cfg.validate()
	if err == nil || !strings.Contains(err.Error(), "META_APP_SECRET") {
		t.Fatalf("expected META_APP_SECRET length error to surface, got %v", err)
	}
}

func TestLoad_ConfigValidationErrors(t *testing.T) {
	// Load reads from the environment, so populate the minimum set of
	// fields it needs before exercising the new checks. t.Setenv
	// automatically restores the previous values at test teardown.
	//
	// Implicit dependency: this test relies on godotenv.Load() honoring
	// its "skip vars already in environment" rule, so t.Setenv values
	// here win over any .env file a developer may have locally. If
	// godotenv is ever swapped for Overload() the test could silently
	// turn green by reading a valid secret from .env — for that case
	// the failure mode is silent, not noisy.
	baseEnv := func() {
		t.Setenv("DATABASE_URL", "postgres://x")
		t.Setenv("META_APP_ID", "meta-id")
		t.Setenv("META_APP_SECRET", strings.Repeat("a", 32))
		t.Setenv("ENCRYPTION_KEY", minValid32ByteBase64Key)
		t.Setenv("JWT_SECRET", strings.Repeat("a", 32))
		// Taglio 2.4 follow-up: S3_* env vars (mandatory since
		// Taglio 3.1) are seeded with the same syntactically-valid
		// placeholders used by minimalValidConfig. Load() reads them
		// through godotenv; the placeholders never need to be real
		// AWS creds because the tests don't dial the storage
		// backend.
		t.Setenv("S3_ENDPOINT", "https://s3.example.com")
		t.Setenv("S3_BUCKET", "test-bucket")
		t.Setenv("S3_ACCESS_KEY", "test-access-key")
		t.Setenv("S3_SECRET_KEY", "test-secret-key")
	}

	t.Run("all required envs valid succeeds", func(t *testing.T) {
		baseEnv()
		if _, err := Load(); err != nil {
			t.Fatalf("Load() with valid envs should succeed; got %v", err)
		}
	})

	t.Run("empty META_APP_SECRET fails", func(t *testing.T) {
		baseEnv()
		t.Setenv("META_APP_SECRET", "")
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), "META_APP_SECRET is required") {
			t.Fatalf("Load() with empty META_APP_SECRET should fail with required-error; got %v", err)
		}
	})

	t.Run("short META_APP_SECRET fails with length-error", func(t *testing.T) {
		baseEnv()
		t.Setenv("META_APP_SECRET", strings.Repeat("a", 31))
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), "at least 32 characters") {
			t.Fatalf("Load() with too-short META_APP_SECRET should fail with length-error; got %v", err)
		}
	})

	t.Run("empty JWT_SECRET fails", func(t *testing.T) {
		baseEnv()
		t.Setenv("JWT_SECRET", "")
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), "JWT_SECRET is required") {
			t.Fatalf("Load() with empty JWT_SECRET should fail with required-error; got %v", err)
		}
	})

	t.Run("short JWT_SECRET fails with length-error", func(t *testing.T) {
		baseEnv()
		t.Setenv("JWT_SECRET", strings.Repeat("a", 31))
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), "at least 32 bytes") {
			t.Fatalf("Load() with too-short JWT_SECRET should fail with length-error; got %v", err)
		}
	})

	t.Run("tiktok key without secret fails", func(t *testing.T) {
		baseEnv()
		t.Setenv("TIKTOK_CLIENT_ID", "tiktok-key")
		t.Setenv("TIKTOK_CLIENT_SECRET", "")
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), "TIKTOK_CLIENT_SECRET is required") {
			t.Fatalf("Load() with TikTok key but no secret should fail; got %v", err)
		}
	})

	t.Run("short tiktok secret fails with length-error", func(t *testing.T) {
		baseEnv()
		t.Setenv("TIKTOK_CLIENT_ID", "tiktok-key")
		t.Setenv("TIKTOK_CLIENT_SECRET", strings.Repeat("a", 31))
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), "at least 32 characters") {
			t.Fatalf("Load() with too-short TikTok secret should fail; got %v", err)
		}
	})

	t.Run("youtube id without secret fails", func(t *testing.T) {
		baseEnv()
		t.Setenv("YOUTUBE_CLIENT_ID", "youtube-id")
		t.Setenv("YOUTUBE_CLIENT_SECRET", "")
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), "YOUTUBE_CLIENT_SECRET is required") {
			t.Fatalf("Load() with YouTube id but no secret should fail; got %v", err)
		}
	})
}

func TestValidate_AppEnv(t *testing.T) {
	// APP_ENV must be one of dev|staging|production. Anything else is
	// rejected so a typo (e.g. "prod", "PRODUCTION", "stage") can't masquerade
	// as a valid deployment environment.
	tests := []struct {
		name      string
		env       string
		wantErr   bool
		errSubstr string
	}{
		{name: "dev passes", env: "dev", wantErr: false},
		{name: "staging passes", env: "staging", wantErr: false},
		{name: "production passes", env: "production", wantErr: false},
		// Common typos / different casing
		{name: "prod (typo) fails", env: "prod", wantErr: true, errSubstr: "APP_ENV must be one of dev|staging|production"},
		{name: "PRODUCTION (upper-cased) fails", env: "PRODUCTION", wantErr: true, errSubstr: "APP_ENV must be one of dev|staging|production"},
		{name: "stage (typo) fails", env: "stage", wantErr: true, errSubstr: "APP_ENV must be one of dev|staging|production"},
		{name: "preview (not in allowlist) fails", env: "preview", wantErr: true, errSubstr: "got \"preview\""},
		{name: "empty fails", env: "", wantErr: true, errSubstr: "got \"\""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := minimalValidConfig(validJWTSecret())
			cfg.AppEnv = tc.env
			err := cfg.validate()
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.errSubstr)
				}
				if tc.errSubstr != "" && !strings.Contains(err.Error(), tc.errSubstr) {
					t.Fatalf("expected error containing %q, got %q", tc.errSubstr, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
		})
	}
}

func TestLoad_AppEnv_PropagatesValue(t *testing.T) {
	// Ensures the APP_ENV env var actually reaches Config.AppEnv (not just
	// get consumed silently by getEnv defaults). Without this we couldn't
	// trust that main.go's fail-fast guard reads the right value.
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("META_APP_ID", "meta-id")
	t.Setenv("META_APP_SECRET", strings.Repeat("a", 32))
	t.Setenv("ENCRYPTION_KEY", minValid32ByteBase64Key)
	t.Setenv("JWT_SECRET", strings.Repeat("a", 32))
	// S3 env vars mandatory since Taglio 3.1.
	t.Setenv("S3_ENDPOINT", "https://s3.example.com")
	t.Setenv("S3_BUCKET", "test-bucket")
	t.Setenv("S3_ACCESS_KEY", "test-access-key")
	t.Setenv("S3_SECRET_KEY", "test-secret-key")
	t.Setenv("APP_ENV", "staging")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() should succeed with APP_ENV=staging, got %v", err)
	}
	if cfg.AppEnv != "staging" {
		t.Fatalf("AppEnv: want staging, got %q", cfg.AppEnv)
	}
}

// TestValidate_NoOAuthPlatformsValid (Taglio 2.4) proves that a
// config with EVERY OAuth platform empty (Meta AppID+Secret empty,
// no TikTok/Twitter/YouTube/LinkedIn creds) still passes validate().
// The server is then expected to start with zero registered providers;
// /api/v1/auth/{anything} returns 404 for every {anything}.
func TestValidate_NoOAuthPlatformsValid(t *testing.T) {
	cfg := minimalValidConfig(validJWTSecret())
	cfg.MetaAppID = ""
	cfg.MetaAppSecret = ""
	if err := cfg.validate(); err != nil {
		t.Fatalf("validate() with zero OAuth platforms should succeed; got %v", err)
	}
}

// TestValidate_OnlyYouTubeValid (Taglio 2.4) proves that a config
// with Meta entirely empty + only YouTube configured still passes
// validate(). This is the canonical example called out in the user
// spec: "il server deve avviarsi anche con un solo provider
// configurato (es. solo YouTube o solo LinkedIn)".
func TestValidate_OnlyYouTubeValid(t *testing.T) {
	cfg := minimalValidConfig(validJWTSecret())
	cfg.MetaAppID = ""
	cfg.MetaAppSecret = ""
	cfg.YouTubeClientID = "yt-id"
	cfg.YouTubeClientSecret = validMetaSecret32
	if err := cfg.validate(); err != nil {
		t.Fatalf("validate() with only YouTube configured should succeed; got %v", err)
	}
}

// TestValidate_OnlyLinkedInValid (Taglio 2.4) is the second
// canonical example: server starts with only LinkedIn configured.
func TestValidate_OnlyLinkedInValid(t *testing.T) {
	cfg := minimalValidConfig(validJWTSecret())
	cfg.MetaAppID = ""
	cfg.MetaAppSecret = ""
	cfg.LinkedInClientID = "li-id"
	cfg.LinkedInClientSecret = validMetaSecret32
	if err := cfg.validate(); err != nil {
		t.Fatalf("validate() with only LinkedIn configured should succeed; got %v", err)
	}
}

// TestValidate_MetaHalfConfigured (Taglio 2.4) covers the two
// half-configured Meta cases that must still fail: a Meta config
// with only the App ID, or only the App Secret, is a misconfiguration
// (it would produce a Facebook service with an empty client_id or
// an empty client_secret) and must be rejected at startup.
func TestValidate_MetaHalfConfigured(t *testing.T) {
	tests := []struct {
		name      string
		id        string
		secret    string
		errSubstr string
	}{
		{
			name:      "META_APP_ID set, META_APP_SECRET empty fails",
			id:        "meta-id",
			secret:    "",
			errSubstr: "META_APP_SECRET is required when META_APP_ID is set",
		},
		{
			name:      "META_APP_ID empty, META_APP_SECRET set fails",
			id:        "",
			secret:    validMetaSecret32,
			errSubstr: "META_APP_ID is required when META_APP_SECRET is set",
		},
		{
			name:      "META_APP_ID set, META_APP_SECRET 31 chars fails (length)",
			id:        "meta-id",
			secret:    strings.Repeat("a", 31),
			errSubstr: "META_APP_SECRET must be at least 32 characters",
		},
		{
			name:      "META_APP_ID empty, META_APP_SECRET empty OK (platform disabled)",
			id:        "",
			secret:    "",
			errSubstr: "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := minimalValidConfig(validJWTSecret())
			cfg.MetaAppID = tc.id
			cfg.MetaAppSecret = tc.secret
			err := cfg.validate()
			if tc.errSubstr == "" {
				if err != nil {
					t.Fatalf("validate() should succeed for %q; got %v", tc.name, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("validate() should fail for %q with substring %q; got nil", tc.name, tc.errSubstr)
			}
			if !strings.Contains(err.Error(), tc.errSubstr) {
				t.Fatalf("validate() error: want substring %q, got %q", tc.errSubstr, err.Error())
			}
		})
	}
}

func TestLoad_AppEnv_BogusFails(t *testing.T) {
	// End-to-end: a bogus APP_ENV makes Load() fail. Validates that the
	// config validation runs against user-supplied values, not just the
	// minimalValidConfig fixture used by struct-direct tests.
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("META_APP_ID", "meta-id")
	t.Setenv("META_APP_SECRET", strings.Repeat("a", 32))
	t.Setenv("ENCRYPTION_KEY", minValid32ByteBase64Key)
	t.Setenv("JWT_SECRET", strings.Repeat("a", 32))
	// S3 env vars mandatory since Taglio 3.1.
	t.Setenv("S3_ENDPOINT", "https://s3.example.com")
	t.Setenv("S3_BUCKET", "test-bucket")
	t.Setenv("S3_ACCESS_KEY", "test-access-key")
	t.Setenv("S3_SECRET_KEY", "test-secret-key")
	t.Setenv("APP_ENV", "bogus")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "APP_ENV must be one of") {
		t.Fatalf("Load() with APP_ENV=bogus should fail; got %v", err)
	}
}

// ---- Blocco #2.2 — multi-key encryption tests ----

// twoDistinctKeys returns two syntactically-distinct valid 32-byte
// base64 keys (key1 != key2) so multi-key tests can stamp ciphertexts
// under different ids.
func twoDistinctKeys() (string, string) {
	raw1 := make([]byte, 32)
	raw2 := make([]byte, 32)
	for i := range raw1 {
		raw1[i] = byte(i)
		raw2[i] = byte(i + 1)
	}
	return base64.StdEncoding.EncodeToString(raw1), base64.StdEncoding.EncodeToString(raw2)
}

// TestValidate_EncryptionLegacyPromotesToMultiKey is the backward-compat
// promise: when only ENCRYPTION_KEY is set, the post-validate Config
// exposes EncryptionKeys={1: key} and ActiveEncryptionKeyID=1 so the
// downstream callers (bootstrap.Wire → crypto.NewEncryptor) see the
// same struct shape regardless of which env-var surface the operator
// used.
func TestValidate_EncryptionLegacyPromotesToMultiKey(t *testing.T) {
	cfg := minimalValidConfig(validJWTSecret())
	cfg.EncryptionKey = minValid32ByteBase64Key
	cfg.EncryptionKeysRaw = ""
	cfg.ActiveEncryptionKeyIDRaw = ""

	if err := cfg.validate(); err != nil {
		t.Fatalf("legacy single-key validate() should succeed; got %v", err)
	}
	if cfg.ActiveEncryptionKeyID != 1 {
		t.Fatalf("legacy promotion: want ActiveEncryptionKeyID=1, got %d", cfg.ActiveEncryptionKeyID)
	}
	if got, ok := cfg.EncryptionKeys[1]; !ok || got != minValid32ByteBase64Key {
		t.Fatalf("legacy promotion: want EncryptionKeys[1]=%q, got %q (present=%v)", minValid32ByteBase64Key, got, ok)
	}
	if len(cfg.EncryptionKeys) != 1 {
		t.Fatalf("legacy promotion: want exactly 1 key in map, got %d", len(cfg.EncryptionKeys))
	}
}

// TestValidate_EncryptionMultiKey_HappyPath exercises the full
// multi-key path: ENCRYPTION_KEYS with 2 entries + ACTIVE_ENCRYPTION_KEY_ID
// pointing at id=2. validate() must populate EncryptionKeys correctly
// and select the active id.
func TestValidate_EncryptionMultiKey_HappyPath(t *testing.T) {
	key1, key2 := twoDistinctKeys()
	cfg := minimalValidConfig(validJWTSecret())
	cfg.EncryptionKey = "" // legacy path disabled
	cfg.EncryptionKeysRaw = "1:" + key1 + ",2:" + key2
	cfg.ActiveEncryptionKeyIDRaw = "2"

	if err := cfg.validate(); err != nil {
		t.Fatalf("multi-key validate() should succeed; got %v", err)
	}
	if cfg.ActiveEncryptionKeyID != 2 {
		t.Fatalf("want ActiveEncryptionKeyID=2, got %d", cfg.ActiveEncryptionKeyID)
	}
	if got := cfg.EncryptionKeys[1]; got != key1 {
		t.Fatalf("EncryptionKeys[1]: want %q, got %q", key1, got)
	}
	if got := cfg.EncryptionKeys[2]; got != key2 {
		t.Fatalf("EncryptionKeys[2]: want %q, got %q", key2, got)
	}
	if len(cfg.EncryptionKeys) != 2 {
		t.Fatalf("want exactly 2 keys in map, got %d", len(cfg.EncryptionKeys))
	}
}

// TestValidate_EncryptionMultiKey_SingleEntry verifies that the
// multi-key path also accepts a single-entry CSV (effectively the
// same as the legacy path, but routed through the new code). The
// "promotion" behavior is identical, just with the active id
// pinned to whatever the operator set.
func TestValidate_EncryptionMultiKey_SingleEntry(t *testing.T) {
	key1, _ := twoDistinctKeys()
	cfg := minimalValidConfig(validJWTSecret())
	cfg.EncryptionKey = ""
	cfg.EncryptionKeysRaw = "1:" + key1
	cfg.ActiveEncryptionKeyIDRaw = "1"

	if err := cfg.validate(); err != nil {
		t.Fatalf("single-entry multi-key validate() should succeed; got %v", err)
	}
	if cfg.ActiveEncryptionKeyID != 1 {
		t.Fatalf("want ActiveEncryptionKeyID=1, got %d", cfg.ActiveEncryptionKeyID)
	}
	if len(cfg.EncryptionKeys) != 1 {
		t.Fatalf("want exactly 1 key in map, got %d", len(cfg.EncryptionKeys))
	}
}

// TestValidate_EncryptionMultiKey_Errors pins every malformed-input
// branch of the multi-key parser. Each row is a separate subtest so
// a failure points at exactly one cause.
func TestValidate_EncryptionMultiKey_Errors(t *testing.T) {
	key1, key2 := twoDistinctKeys()
	shortKey := base64.StdEncoding.EncodeToString(make([]byte, 16))

	tests := []struct {
		name          string
		keysRaw       string
		activeRaw     string
		wantErrSubstr string
	}{
		{
			// Empty ENCRYPTION_KEYS with no legacy fallback → required-error
			// (the legacy fallback is also empty in this subtest).
			name:          "empty ENCRYPTION_KEYS + no legacy → required",
			keysRaw:       "",
			activeRaw:     "",
			wantErrSubstr: "ENCRYPTION_KEY is required",
		},
		{
			// Missing colon → format error.
			name:          "missing colon in entry",
			keysRaw:       "1" + key1, // no colon
			activeRaw:     "1",
			wantErrSubstr: "must be in the form 'id:base64key'",
		},
		{
			// Trailing colon (id present, key empty).
			name:          "trailing colon in entry",
			keysRaw:       "1:",
			activeRaw:     "1",
			wantErrSubstr: "must be in the form 'id:base64key'",
		},
		{
			// Leading colon (id empty, key present).
			name:          "leading colon in entry",
			keysRaw:       ":" + key1,
			activeRaw:     "1",
			wantErrSubstr: "must be in the form 'id:base64key'",
		},
		{
			// Non-numeric id.
			name:          "non-numeric id",
			keysRaw:       "abc:" + key1,
			activeRaw:     "1",
			wantErrSubstr: "is not a uint32",
		},
		{
			// Negative id (parse fails).
			name:          "negative id (parse fails)",
			keysRaw:       "-1:" + key1,
			activeRaw:     "1",
			wantErrSubstr: "is not a uint32",
		},
		{
			// Key too short.
			name:          "key too short (16 bytes)",
			keysRaw:       "1:" + shortKey,
			activeRaw:     "1",
			wantErrSubstr: "must be exactly 32 bytes (got 16)",
		},
		{
			// Invalid base64.
			name:          "invalid base64 in key",
			keysRaw:       "1:@@@not-base64@@@",
			activeRaw:     "1",
			wantErrSubstr: "not valid base64",
		},
		{
			// Duplicate id.
			name:          "duplicate key id in CSV",
			keysRaw:       "1:" + key1 + ",1:" + key2,
			activeRaw:     "1",
			wantErrSubstr: "duplicate key id 1",
		},
		{
			// Empty active id with non-empty ENCRYPTION_KEYS.
			name:          "empty ACTIVE_ENCRYPTION_KEY_ID",
			keysRaw:       "1:" + key1,
			activeRaw:     "",
			wantErrSubstr: "ACTIVE_ENCRYPTION_KEY_ID is required",
		},
		{
			// Non-numeric active id.
			name:          "non-numeric ACTIVE_ENCRYPTION_KEY_ID",
			keysRaw:       "1:" + key1,
			activeRaw:     "abc",
			wantErrSubstr: "is not a uint32",
		},
		{
			// Active id not in map.
			name:          "ACTIVE_ENCRYPTION_KEY_ID not in map",
			keysRaw:       "1:" + key1 + ",2:" + key2,
			activeRaw:     "5",
			wantErrSubstr: "ACTIVE_ENCRYPTION_KEY_ID=5 not in ENCRYPTION_KEYS",
		},
		{
			// Empty entry (trailing comma).
			name:          "empty entry (trailing comma)",
			keysRaw:       "1:" + key1 + ",",
			activeRaw:     "1",
			wantErrSubstr: "entry 2 is empty",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := minimalValidConfig(validJWTSecret())
			cfg.EncryptionKey = "" // disable legacy fallback so multi-key path is exercised
			cfg.EncryptionKeysRaw = tc.keysRaw
			cfg.ActiveEncryptionKeyIDRaw = tc.activeRaw

			err := cfg.validate()
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErrSubstr)
			}
			if !strings.Contains(err.Error(), tc.wantErrSubstr) {
				t.Fatalf("expected error containing %q, got %q", tc.wantErrSubstr, err.Error())
			}
			// Post-failure invariant: EncryptionKeys must NOT be
			// partially populated. A failed validate() should leave
			// the map nil so the caller doesn't accidentally use a
			// half-built config.
			if cfg.EncryptionKeys != nil {
				t.Fatalf("validate() failure must leave EncryptionKeys nil, got %v", cfg.EncryptionKeys)
			}
		})
	}
}

// TestValidate_EncryptionAmbiguousConfig covers the operator-error
// case where both ENCRYPTION_KEY and ENCRYPTION_KEYS are set. The
// behaviour is reject — silently picking one is exactly the kind
// of "magic choice" that breaks deployments at 2am.
func TestValidate_EncryptionAmbiguousConfig(t *testing.T) {
	_, key2 := twoDistinctKeys()
	cfg := minimalValidConfig(validJWTSecret())
	cfg.EncryptionKey = minValid32ByteBase64Key // legacy set
	cfg.EncryptionKeysRaw = "2:" + key2         // multi-key also set
	cfg.ActiveEncryptionKeyIDRaw = "2"

	err := cfg.validate()
	if err == nil {
		t.Fatal("validate() must reject ambiguous config (both ENCRYPTION_KEY and ENCRYPTION_KEYS set)")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("expected 'ambiguous' in error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "EITHER") {
		t.Fatalf("error message should guide the operator (EITHER/OR); got: %v", err)
	}
}

// TestLoad_EncryptionMultiKey_E2E exercises the end-to-end Load()
// path with ENCRYPTION_KEYS+ACTIVE_ENCRYPTION_KEY_ID set. Confirms
// the env vars reach the post-validate struct intact.
func TestLoad_EncryptionMultiKey_E2E(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("META_APP_ID", "meta-id")
	t.Setenv("META_APP_SECRET", strings.Repeat("a", 32))
	// Legacy is unset.
	t.Setenv("ENCRYPTION_KEY", "")
	key1, key2 := twoDistinctKeys()
	t.Setenv("ENCRYPTION_KEYS", "1:"+key1+",2:"+key2)
	t.Setenv("ACTIVE_ENCRYPTION_KEY_ID", "2")
	t.Setenv("JWT_SECRET", strings.Repeat("a", 32))
	t.Setenv("S3_ENDPOINT", "https://s3.example.com")
	t.Setenv("S3_BUCKET", "test-bucket")
	t.Setenv("S3_ACCESS_KEY", "test-access-key")
	t.Setenv("S3_SECRET_KEY", "test-secret-key")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() with multi-key env vars should succeed; got %v", err)
	}
	if cfg.ActiveEncryptionKeyID != 2 {
		t.Fatalf("post-Load ActiveEncryptionKeyID: want 2, got %d", cfg.ActiveEncryptionKeyID)
	}
	if len(cfg.EncryptionKeys) != 2 {
		t.Fatalf("post-Load EncryptionKeys: want 2 entries, got %d", len(cfg.EncryptionKeys))
	}
	if cfg.EncryptionKeys[1] != key1 || cfg.EncryptionKeys[2] != key2 {
		t.Fatalf("post-Load EncryptionKeys contents mismatch")
	}
}

// TestLoad_EncryptionLegacyE2E confirms the legacy path still works
// through Load() (not just validate()). This is the
// backward-compat promise from the user spec: a pre-Blocco #2.2
// deployment with only ENCRYPTION_KEY set continues to boot.
func TestLoad_EncryptionLegacyE2E(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("META_APP_ID", "meta-id")
	t.Setenv("META_APP_SECRET", strings.Repeat("a", 32))
	t.Setenv("ENCRYPTION_KEY", minValid32ByteBase64Key)
	// Multi-key vars unset.
	t.Setenv("ENCRYPTION_KEYS", "")
	t.Setenv("ACTIVE_ENCRYPTION_KEY_ID", "")
	t.Setenv("JWT_SECRET", strings.Repeat("a", 32))
	t.Setenv("S3_ENDPOINT", "https://s3.example.com")
	t.Setenv("S3_BUCKET", "test-bucket")
	t.Setenv("S3_ACCESS_KEY", "test-access-key")
	t.Setenv("S3_SECRET_KEY", "test-secret-key")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() with legacy env vars should succeed; got %v", err)
	}
	if cfg.ActiveEncryptionKeyID != 1 {
		t.Fatalf("legacy Load: want ActiveEncryptionKeyID=1, got %d", cfg.ActiveEncryptionKeyID)
	}
	if got, ok := cfg.EncryptionKeys[1]; !ok || got != minValid32ByteBase64Key {
		t.Fatalf("legacy Load: EncryptionKeys[1] mismatch (present=%v, val=%q)", ok, got)
	}
}
