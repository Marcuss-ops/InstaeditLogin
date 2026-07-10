package config

import (
	"encoding/base64"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

// Minimum-credential thresholds. Each constant is referenced from
// validate() and shows up in error messages so an operator can
// reverse-engineer the policy without reading the code.
const (
	// HS256 (RFC 7518 §3.2) requires the signing key to be at least as long
	// as the hash output: 256 bits = 32 bytes for SHA-256.
	jwtSecretMinBytes = 32

	// AES-256-GCM needs exactly 32 bytes of key material after base64 decode.
	// The validate() function rejects any other length.
	aesKeyBytes = 32

	// OAuth client secrets minimum length policy. Modern providers
	// (Meta/TikTok/Twitter/YouTube) issue secrets of ~50 chars; a shorter
	// value usually indicates a copy/paste mistake or an unsigned
	// placeholder. Enforced on every non-empty OAuth secret so a
	// misconfiguration fails fast at startup rather than at the first
	// /auth/{provider}/callback hit.
	secretMinChars = 32
)

// Config holds all configuration for the application.
//
// Minimum-credential thresholds live as constants near the top of the file
// (jwtSecretMinBytes, aesKeyBytes, secretMinChars) so validate() stays
// readable and the bounds aren't buried in numeric literals.
type Config struct {
	// Server
	ServerPort string
	ServerHost string

	// FrontendURL is where the OAuth callback should redirect after the
	// backend exchanges the auth code. When empty, the callback falls back to
	// returning JSON (useful for non-browser clients and local testing).
	FrontendURL string
	// AllowedCORSOrigins is the comma-separated list of origins allowed to
	// call the API via the browser. Defaults to FrontendURL.
	AllowedCORSOrigins []string

	// Database (PostgreSQL)
	// Use DATABASE_URL for full connection string (e.g. Supabase), OR individual fields below.
	// DATABASE_URL takes precedence.
	DatabaseURL string
	DBHost      string
	DBPort      string
	DBUser      string
	DBPassword  string
	DBName      string
	DBSSLMode   string

	// Meta OAuth
	MetaAppID       string
	MetaAppSecret   string
	MetaRedirectURI string

	// TikTok OAuth
	TikTokClientKey    string
	TikTokClientSecret string
	TikTokRedirectURI  string

	// Twitter/X OAuth
	TwitterClientID     string
	TwitterClientSecret string
	TwitterRedirectURI  string

	// Twitter/X OAuth 1.0a (static credentials for direct publishing —
	// bypasses OAuth 2.0 user login; all posts go to the owner's account).
	TwitterAPIKey            string
	TwitterAPIKeySecret      string
	TwitterAccessToken       string
	TwitterAccessTokenSecret string

	// YouTube OAuth
	YouTubeClientID     string
	YouTubeClientSecret string
	YouTubeRedirectURI  string

	// LinkedIn OAuth
	LinkedInClientID     string
	LinkedInClientSecret string
	LinkedInRedirectURI  string

	// Encryption
	EncryptionKey string

	// JWT
	JWTSecret     string
	JWTTTLHours   int
	StrictJWTAuth bool

	// Logging
	LogLevel string

	// Background worker tuning.
	PublishWorkerIntervalSeconds int

	// Supabase Storage (alternative to AWS S3). When SUPABASE_URL +
	// SUPABASE_SERVICE_KEY + SUPABASE_BUCKET are all set, presigned upload
	// URLs use the Supabase Storage REST API. Otherwise the endpoint
	// /api/v1/storage/upload-url returns 501 in production.
	SupabaseURL        string
	SupabaseServiceKey string
	SupabaseBucket     string

	// AWS S3 (alternative to Supabase Storage). When AWS_REGION +
	// AWS_ACCESS_KEY_ID + AWS_SECRET_ACCESS_KEY + AWS_S3_BUCKET are all
	// set, presigned upload URLs use AWS SigV4-signed PUT URLs.
	AWSRegion          string
	AWSAccessKeyID     string
	AWSSecretAccessKey string
	AWSBucket          string

	// MaxUploadBytes caps the size of any single file the client may
	// upload. Default 200 MiB (200*1024*1024). Configurable so a deploy
	// with stricter storage quotas can dial it down without rebuilding.
	MaxUploadBytes int64
}

// Load reads configuration from environment variables.
// It first loads the .env file if present (silently ignores if missing).
func Load() (*Config, error) {
	// Load .env file if it exists (non-fatal if missing)
	_ = godotenv.Load()

	cfg := &Config{
		ServerPort:          getEnv("SERVER_PORT", "8080"),
		ServerHost:          getEnv("SERVER_HOST", "0.0.0.0"),
		FrontendURL:         getEnv("FRONTEND_URL", ""),
		AllowedCORSOrigins:  splitCSV(getEnv("CORS_ALLOWED_ORIGINS", "")),
		DatabaseURL:         getEnv("DATABASE_URL", ""),
		DBHost:              getEnv("DB_HOST", "localhost"),
		DBPort:              getEnv("DB_PORT", "5432"),
		DBUser:              getEnv("DB_USER", "instaedit"),
		DBPassword:          getEnv("DB_PASSWORD", ""),
		DBName:              getEnv("DB_NAME", "instaedit_login"),
		DBSSLMode:           getEnv("DB_SSLMODE", "disable"),
		MetaAppID:           getEnv("META_APP_ID", ""),
		MetaAppSecret:       getEnv("META_APP_SECRET", ""),
		MetaRedirectURI:     getEnv("META_REDIRECT_URI", "http://localhost:8080/api/v1/auth/meta/callback"),
		TikTokClientKey:     getEnv("TIKTOK_CLIENT_KEY", ""),
		TikTokClientSecret:  getEnv("TIKTOK_CLIENT_SECRET", ""),
		TikTokRedirectURI:   getEnv("TIKTOK_REDIRECT_URI", "http://localhost:8080/api/v1/auth/tiktok/callback"),
		TwitterClientID:     getEnv("TWITTER_CLIENT_ID", ""),
		TwitterClientSecret: getEnv("TWITTER_CLIENT_SECRET", ""),
		TwitterRedirectURI:  getEnv("TWITTER_REDIRECT_URI", "http://localhost:8080/api/v1/auth/twitter/callback"),
		TwitterAPIKey:            getEnv("TWITTER_API_KEY", ""),
		TwitterAPIKeySecret:      getEnv("TWITTER_API_KEY_SECRET", ""),
		TwitterAccessToken:       getEnv("TWITTER_ACCESS_TOKEN", ""),
		TwitterAccessTokenSecret: getEnv("TWITTER_ACCESS_TOKEN_SECRET", ""),
		YouTubeClientID:     getEnv("YOUTUBE_CLIENT_ID", ""),
		YouTubeClientSecret: getEnv("YOUTUBE_CLIENT_SECRET", ""),
		YouTubeRedirectURI:  getEnv("YOUTUBE_REDIRECT_URI", "http://localhost:8080/api/v1/auth/youtube/callback"),
		LinkedInClientID:     getEnv("LINKEDIN_CLIENT_ID", ""),
		LinkedInClientSecret: getEnv("LINKEDIN_CLIENT_SECRET", ""),
		LinkedInRedirectURI:  getEnv("LINKEDIN_REDIRECT_URI", "http://localhost:8080/api/v1/auth/linkedin/callback"),
		EncryptionKey:       getEnv("ENCRYPTION_KEY", ""),
		JWTSecret:           getEnv("JWT_SECRET", ""),
		JWTTTLHours:         getEnvInt("JWT_TTL_HOURS", 168),
		StrictJWTAuth:       getEnvBool("STRICT_JWT_AUTH", true),
		LogLevel:                     getEnv("LOG_LEVEL", "info"),
		PublishWorkerIntervalSeconds: getEnvInt("PUBLISH_WORKER_INTERVAL_SECONDS", 30),

		SupabaseURL:        getEnv("SUPABASE_URL", ""),
		SupabaseServiceKey: getEnv("SUPABASE_SERVICE_KEY", ""),
		SupabaseBucket:     getEnv("SUPABASE_BUCKET", ""),

		AWSRegion:          getEnv("AWS_REGION", ""),
		AWSAccessKeyID:     getEnv("AWS_ACCESS_KEY_ID", ""),
		AWSSecretAccessKey: getEnv("AWS_SECRET_ACCESS_KEY", ""),
		AWSBucket:          getEnv("AWS_S3_BUCKET", ""),

		MaxUploadBytes: getEnvInt64("STORAGE_MAX_UPLOAD_BYTES", 200*1024*1024),
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// validate checks that every credential present in the Config meets its
// minimum-length policy. Order is intentional:
//
//  1. Database — nothing else can run without it
//  2. Meta OAuth (mandatory) — earliest mandatory credential
//  3. Encryption key (mandatory) — guards every persisted token
//  4. JWT signing key (mandatory) — gates every authenticated request
//  5. Optional OAuth platforms — only error if a platform is half-configured
//
// Sequential validation (early return on first error) is deliberate: it
// surfaces the most upstream misconfiguration first, so the operator
// fixes one thing at a time.
func (c *Config) validate() error {
	// Database: DATABASE_URL takes precedence; fallback to individual params.
	if c.DatabaseURL == "" {
		if c.DBPassword == "" {
			return fmt.Errorf("DB_PASSWORD is required (or set DATABASE_URL)")
		}
	}

	// Meta OAuth is mandatory. Fail fast on missing or weak credentials.
	if c.MetaAppID == "" {
		return fmt.Errorf("META_APP_ID is required")
	}
	if c.MetaAppSecret == "" {
		return fmt.Errorf("META_APP_SECRET is required (must be at least %d characters)", secretMinChars)
	}
	if len(c.MetaAppSecret) < secretMinChars {
		return fmt.Errorf("META_APP_SECRET must be at least %d characters (got %d)", secretMinChars, len(c.MetaAppSecret))
	}

	// Encryption key: must decode to exactly aesKeyBytes for AES-256-GCM.
	if c.EncryptionKey == "" {
		return fmt.Errorf("ENCRYPTION_KEY is required (generate with `openssl rand -base64 32`)")
	}
	keyBytes, err := base64.StdEncoding.DecodeString(c.EncryptionKey)
	if err != nil {
		return fmt.Errorf("ENCRYPTION_KEY is not valid base64: %w", err)
	}
	if len(keyBytes) != aesKeyBytes {
		// Includes both actual and expected lengths so the operator doesn't
		// have to guess what `openssl rand -base64 32` should produce.
		return fmt.Errorf("ENCRYPTION_KEY must decode to exactly %d bytes for AES-256-GCM (got %d; expected %d). Generate with `openssl rand -base64 32`", aesKeyBytes, len(keyBytes), aesKeyBytes)
	}

	// JWT signing key: HS256 needs at least jwtSecretMinBytes bytes.
	//
	// len(s) on a Go string returns byte length (UTF-8 byte count), not rune
	// count — a multi-byte-friendly secret like "€"×11 (33 bytes, 11 runes)
	// still passes; a future contributor who changes this to
	// utf8.RuneCountInString would break that contract.
	if c.JWTSecret == "" {
		return fmt.Errorf("JWT_SECRET is required (must be at least %d bytes; generate with `openssl rand -hex 32`)", jwtSecretMinBytes)
	}
	if len(c.JWTSecret) < jwtSecretMinBytes {
		return fmt.Errorf("JWT_SECRET must be at least %d bytes for HS256 (got %d)", jwtSecretMinBytes, len(c.JWTSecret))
	}

	// Optional OAuth platforms. Skipped only when BOTH id and secret are empty.
	// A half-configured provider (key set, secret empty) registers the
	// platform in main.go and only fails noisily at the first /auth hit —
	// validateOptionalPlatform catches it here at startup instead.
	if err := c.validateOptionalPlatform("TIKTOK", c.TikTokClientKey, c.TikTokClientSecret); err != nil {
		return err
	}
	if err := c.validateOptionalPlatform("TWITTER", c.TwitterClientID, c.TwitterClientSecret); err != nil {
		return err
	}
	if err := c.validateOptionalPlatform("YOUTUBE", c.YouTubeClientID, c.YouTubeClientSecret); err != nil {
		return err
	}
	if err := c.validateOptionalPlatform("LINKEDIN", c.LinkedInClientID, c.LinkedInClientSecret); err != nil {
		return err
	}

	return nil
}

// validateOptionalPlatform enforces two invariants for an OPTIONAL OAuth
// provider passed by env-var-style platform name (e.g. "TIKTOK"):
//
//  1. If the public ID/KEY is set, the SECRET must also be set (and meet
//     length policy). A half-configured provider would otherwise register
//     successfully and only fail at first /auth/{provider} attempt.
//  2. If the SECRET is set, it must meet secretMinChars. A shorter value
//     usually indicates a copy/paste mistake or an unsigned placeholder.
//
// An entirely empty pair (ID empty, SECRET empty) keeps the platform
// disabled — main.go skips registration in that case.
//
// The platform name is interpolated into env-var-style error messages so
// an operator can map the error directly to a missing env var.
func (c *Config) validateOptionalPlatform(name, id, secret string) error {
	if id == "" && secret == "" {
		return nil // platform disabled
	}
	if secret == "" {
		return fmt.Errorf("%s_CLIENT_SECRET is required when %s_CLIENT_ID is set (or unset both to disable the platform)", name, name)
	}
	if len(secret) < secretMinChars {
		return fmt.Errorf("%s_CLIENT_SECRET must be at least %d characters (got %d)", name, secretMinChars, len(secret))
	}
	return nil
}

// DSN returns the PostgreSQL connection string.
// If DATABASE_URL is set (e.g. from Supabase), it is returned directly.
func (c *Config) DSN() string {
	if c.DatabaseURL != "" {
		return c.DatabaseURL
	}
	return fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		c.DBHost, c.DBPort, c.DBUser, c.DBPassword, c.DBName, c.DBSSLMode,
	)
}

// getEnv reads an environment variable with a default fallback.
func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

// splitCSV splits a comma-separated string, trimming whitespace and dropping
// empty entries. Returns a nil slice for empty input.
func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// getEnvInt reads an integer environment variable with a default fallback.
// Invalid values silently fall back to the default.
func getEnvInt(key string, fallback int) int {
	if value, ok := os.LookupEnv(key); ok {
		if n, err := strconv.Atoi(value); err == nil {
			return n
		}
	}
	return fallback
}

// getEnvInt64 reads a 64-bit integer environment variable with a default
// fallback. Used for byte sizes (e.g. STORAGE_MAX_UPLOAD_BYTES) where
// int range is insufficient at >2 GB. Invalid values silently fall back
// to the default.
func getEnvInt64(key string, fallback int64) int64 {
	if value, ok := os.LookupEnv(key); ok {
		if n, err := strconv.ParseInt(value, 10, 64); err == nil {
			return n
		}
	}
	return fallback
}

// getEnvBool reads a boolean environment variable (true/1/yes/on) with a default fallback.
func getEnvBool(key string, fallback bool) bool {
	if value, ok := os.LookupEnv(key); ok {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "true", "1", "yes", "on":
			return true
		case "false", "0", "no", "off":
			return false
		}
	}
	return fallback
}
