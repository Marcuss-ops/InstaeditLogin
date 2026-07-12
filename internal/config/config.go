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
	// Use DATABASE_URL for full connection string, OR individual fields below.
	// DATABASE_URL takes precedence.
	DatabaseURL string
	DBHost      string
	DBPort      string
	DBUser      string
	DBPassword  string
	DBName      string
	DBSSLMode   string

	// Meta OAuth — shared App ID and Secret across Instagram / Facebook / Threads.
	MetaAppID       string
	MetaAppSecret   string
	MetaRedirectURI string // DEPRECATED: use per-platform redirect URIs (Instagram/Facebook/Threads)

	// Per-platform redirect URIs (same Meta App, different callback paths).
	InstagramRedirectURI string
	FacebookRedirectURI  string
	ThreadsRedirectURI   string

	// TikTok OAuth
	TikTokClientKey    string
	TikTokClientSecret string
	TikTokRedirectURI  string

	// Twitter/X OAuth 2.0 PKCE
	TwitterClientID     string
	TwitterClientSecret string
	TwitterRedirectURI  string

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
	JWTSecret   string
	JWTTTLHours int

	// Logging
	LogLevel string

	// AppEnv is the deployment environment. Must be one of "dev",
	// "staging", "production". Default "dev" so missing env-var on local
	// shouldn't surprise developers.
	AppEnv string

	// Background worker tuning.
	PublishWorkerIntervalSeconds int

	// S3-compatible storage (mandatory). Taglio 3.1: Supabase was removed;
	// the only storage backend is S3-compatible (works with AWS S3,
	// MinIO, Cloudflare R2, Backblaze B2, Wasabi, etc.). All four env
	// vars must be set or the server refuses to start.
	S3Endpoint  string // e.g. "https://s3.us-east-1.amazonaws.com" or "https://minio.example.com" (no trailing slash, no bucket)
	S3Bucket    string
	S3AccessKey string
	S3SecretKey string
	// S3Region is the SigV4 credential-scope component. Optional; defaults
	// to "us-east-1" (acceptable for AWS S3, MinIO, R2, B2, Wasabi). Set
	// explicitly if your S3-compatible store rejects arbitrary region
	// strings in the credential scope.
	S3Region string

	// MaxUploadBytes caps the size of any single file the client may
	// upload. Default 200 MiB (200*1024*1024). Configurable so a deploy
	// with stricter storage quotas can dial it down without rebuilding.
	MaxUploadBytes int64

	// CapabilitiesMatrixPath is the on-disk location of the JSON file that
	// declares the per-platform theoretical capabilities (Taglio 5e
	// Level 1). Defaults to "config/capabilities.json" (canonical path
	// relative to the server's working directory). Empty value, missing
	// file, or malformed JSON falls back to capabilities.WithDefaults() —
	// logger.Info on the path actually used, so operators can verify the
	// boot picked up the JSON they intended. CAPABILITIES_CONFIG_PATH
	// env var overrides this field.
	CapabilitiesMatrixPath string
}

// Load reads configuration from environment variables.
// It first loads the .env file if present (silently ignores if missing).
func Load() (*Config, error) {
	// Load .env file if it exists (non-fatal if missing)
	_ = godotenv.Load()

	cfg := &Config{
		ServerPort:                   getEnv("SERVER_PORT", "8080"),
		ServerHost:                   getEnv("SERVER_HOST", "0.0.0.0"),
		FrontendURL:                  getEnv("FRONTEND_URL", ""),
		AllowedCORSOrigins:           splitCSV(getEnv("CORS_ALLOWED_ORIGINS", "")),
		DatabaseURL:                  getEnv("DATABASE_URL", ""),
		DBHost:                       getEnv("DB_HOST", "localhost"),
		DBPort:                       getEnv("DB_PORT", "5432"),
		DBUser:                       getEnv("DB_USER", "instaedit"),
		DBPassword:                   getEnv("DB_PASSWORD", ""),
		DBName:                       getEnv("DB_NAME", "instaedit_login"),
		DBSSLMode:                    getEnv("DB_SSLMODE", "disable"),
		MetaAppID:                    getEnv("META_APP_ID", ""),
		MetaAppSecret:                getEnv("META_APP_SECRET", ""),
		MetaRedirectURI:              getEnv("META_REDIRECT_URI", ""),
		InstagramRedirectURI:         getEnv("INSTAGRAM_REDIRECT_URI", "http://localhost:8080/api/v1/auth/instagram/callback"),
		FacebookRedirectURI:          getEnv("FACEBOOK_REDIRECT_URI", "http://localhost:8080/api/v1/auth/facebook/callback"),
		ThreadsRedirectURI:           getEnv("THREADS_REDIRECT_URI", "http://localhost:8080/api/v1/auth/threads/callback"),
		TikTokClientKey:              getEnv("TIKTOK_CLIENT_KEY", ""),
		TikTokClientSecret:           getEnv("TIKTOK_CLIENT_SECRET", ""),
		TikTokRedirectURI:            getEnv("TIKTOK_REDIRECT_URI", "http://localhost:8080/api/v1/auth/tiktok/callback"),
		TwitterClientID:              getEnv("TWITTER_CLIENT_ID", ""),
		TwitterClientSecret:          getEnv("TWITTER_CLIENT_SECRET", ""),
		TwitterRedirectURI:           getEnv("TWITTER_REDIRECT_URI", "http://localhost:8080/api/v1/auth/twitter/callback"),
		YouTubeClientID:              getEnv("YOUTUBE_CLIENT_ID", ""),
		YouTubeClientSecret:          getEnv("YOUTUBE_CLIENT_SECRET", ""),
		YouTubeRedirectURI:           getEnv("YOUTUBE_REDIRECT_URI", "http://localhost:8080/api/v1/auth/youtube/callback"),
		LinkedInClientID:             getEnv("LINKEDIN_CLIENT_ID", ""),
		LinkedInClientSecret:         getEnv("LINKEDIN_CLIENT_SECRET", ""),
		LinkedInRedirectURI:          getEnv("LINKEDIN_REDIRECT_URI", "http://localhost:8080/api/v1/auth/linkedin/callback"),
		EncryptionKey:                getEnv("ENCRYPTION_KEY", ""),
		JWTSecret:                    getEnv("JWT_SECRET", ""),
		JWTTTLHours:                  getEnvInt("JWT_TTL_HOURS", 168),
		LogLevel:                     getEnv("LOG_LEVEL", "info"),
		AppEnv:                       getEnv("APP_ENV", "dev"),
		PublishWorkerIntervalSeconds: getEnvInt("PUBLISH_WORKER_INTERVAL_SECONDS", 30),

		S3Endpoint:  getEnv("S3_ENDPOINT", ""),
		S3Bucket:    getEnv("S3_BUCKET", ""),
		S3AccessKey: getEnv("S3_ACCESS_KEY", ""),
		S3SecretKey: getEnv("S3_SECRET_KEY", ""),
		S3Region:    getEnv("S3_REGION", ""),

		MaxUploadBytes: getEnvInt64("STORAGE_MAX_UPLOAD_BYTES", 200*1024*1024),

		CapabilitiesMatrixPath: getEnv("CAPABILITIES_CONFIG_PATH", "config/capabilities.json"),
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
//  2. S3-compatible storage (mandatory, Taglio 3.1)
//  3. Encryption key (mandatory) — guards every persisted token
//  4. JWT signing key (mandatory) — gates every authenticated request
//  5. Optional OAuth platforms (including Meta, Taglio 2.4) — only
//     error if a platform is half-configured
//
// Sequential validation (early return on first error) is deliberate: it
// surfaces the most upstream misconfiguration first, so the operator
// fixes one thing at a time.
func (c *Config) validate() error {
	// APP_ENV is a deployment-environment flag — checked first because a
	// misconfigured production deploy must fail fast before any DB
	// connection or key material is touched.
	switch c.AppEnv {
	case "dev", "staging", "production":
		// ok
	default:
		return fmt.Errorf("APP_ENV must be one of dev|staging|production (got %q)", c.AppEnv)
	}

	// Database: DATABASE_URL takes precedence; fallback to individual params.
	if c.DatabaseURL == "" {
		if c.DBPassword == "" {
			return fmt.Errorf("DB_PASSWORD is required (or set DATABASE_URL)")
		}
	}

	// S3-compatible storage is mandatory (Taglio 3.1). All four env vars
	// must be set or the server refuses to start. Supabase was removed
	// entirely. The explicit panic
	// in main.go is belt-and-suspenders against a future refactor that
	// relaxes this validation.
	if c.S3Endpoint == "" {
		return fmt.Errorf("S3_ENDPOINT is required (e.g. https://s3.us-east-1.amazonaws.com or https://minio.example.com)")
	}
	if c.S3Bucket == "" {
		return fmt.Errorf("S3_BUCKET is required")
	}
	if c.S3AccessKey == "" {
		return fmt.Errorf("S3_ACCESS_KEY is required")
	}
	if c.S3SecretKey == "" {
		return fmt.Errorf("S3_SECRET_KEY is required")
	}

	// Meta OAuth (Taglio 2.4: same optional-platform semantics as
	// TikTok/Twitter/YouTube/LinkedIn). The App ID + Secret are shared
	// across Instagram / Facebook / Threads; each Meta-family provider is
	// registered independently in providers/registry.go based on which
	// redirect URI is set + whether META_APP_ID + META_APP_SECRET are
	// both present. An entirely empty Meta config (no App ID, no
	// redirect URIs) is valid — it means all Meta-family platforms are
	// disabled. A half-configured Meta (ID set, secret empty, or vice
	// versa) is rejected so a misconfiguration fails fast at startup
	// rather than at the first /auth/{provider}/{login,callback} hit.
	//
	// The previous "if any redirect URI is set, META_APP_ID +
	// META_APP_SECRET are mandatory" logic was removed in Taglio 2.4
	// so a deployment can run with only YouTube / only LinkedIn / etc.
	if c.MetaAppID == "" && c.MetaAppSecret == "" {
		// platform disabled — no validation needed
	} else if c.MetaAppID == "" {
		return fmt.Errorf("META_APP_ID is required when META_APP_SECRET is set (or unset both to disable the platform)")
	} else if c.MetaAppSecret == "" {
		return fmt.Errorf("META_APP_SECRET is required when META_APP_ID is set (or unset both to disable the platform)")
	} else if len(c.MetaAppSecret) < secretMinChars {
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
// If DATABASE_URL is set, it is returned directly.
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


