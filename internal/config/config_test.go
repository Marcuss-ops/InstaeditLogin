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
func minimalValidConfig(jwtSecret string) *Config {
	return &Config{
		DatabaseURL:   "postgres://x",
		MetaAppID:     "meta-id",
		MetaAppSecret: validMetaSecret32,
		EncryptionKey: minValid32ByteBase64Key,
		JWTSecret:     jwtSecret,
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
			errSubstrs: []string{"got 16", "expected 32"},
		},
		{
			name:       "64-byte key fails (wrong size for AES-256)",
			key:        base64.StdEncoding.EncodeToString(make([]byte, 64)),
			wantErr:    true,
			errSubstrs: []string{"got 64", "expected 32"},
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
		{"twitter id without secret fails (secret required)", "TWITTER", "twitter-id", "", true, "TWITTER_CLIENT_SECRET is required"},
		{"twitter 31-char secret fails length", "TWITTER", "twitter-id", strings.Repeat("a", 31), true, "at least 32 characters"},
		{"twitter valid pair passes", "TWITTER", "twitter-id", validMetaSecret32, false, ""},
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
				cfg.TikTokClientKey = tc.id
				cfg.TikTokClientSecret = tc.secret
			case "TWITTER":
				cfg.TwitterClientID = tc.id
				cfg.TwitterClientSecret = tc.secret
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
		t.Setenv("TIKTOK_CLIENT_KEY", "tiktok-key")
		t.Setenv("TIKTOK_CLIENT_SECRET", "")
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), "TIKTOK_CLIENT_SECRET is required") {
			t.Fatalf("Load() with TikTok key but no secret should fail; got %v", err)
		}
	})

	t.Run("short tiktok secret fails with length-error", func(t *testing.T) {
		baseEnv()
		t.Setenv("TIKTOK_CLIENT_KEY", "tiktok-key")
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
