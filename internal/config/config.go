package config

import (
	"encoding/base64"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

// Minimum-credential thresholds.
const (
	jwtSecretMinBytes = 32
	aesKeyBytes       = 32
	secretMinChars    = 32
)

// Config holds all configuration for the application.
//
// Taglio 5b: SERVER_PORT + SERVER_HOST removed — the server listens on the
// PORT env var only (Vercel / Railway / Render standard). TWITTER_* env vars
// renamed to X_*; TIKTOK_CLIENT_KEY renamed to TIKTOK_CLIENT_ID.
type Config struct {
	// FrontendURL is where the OAuth callback should redirect.
	FrontendURL string
	// AllowedCORSOrigins is the comma-separated list of origins.
	AllowedCORSOrigins []string

	// Database (PostgreSQL). DATABASE_URL for production; individual fields
	// (DB_HOST, DB_PORT, DB_USER, DB_PASSWORD, DB_NAME, DB_SSLMODE) are kept
	// for local tooling. DATABASE_URL takes precedence.
	DatabaseURL string
	DBHost      string
	DBPort      string
	DBUser      string
	DBPassword  string
	DBName      string
	DBSSLMode   string

	// Meta OAuth — shared App ID and Secret.
	MetaAppID       string
	MetaAppSecret   string
	MetaRedirectURI string // DEPRECATED

	// Per-platform redirect URIs.
	InstagramRedirectURI string
	FacebookRedirectURI  string
	ThreadsRedirectURI   string

	// TikTok OAuth
	TikTokClientID     string
	TikTokClientSecret string
	TikTokRedirectURI  string

	// X (Twitter) OAuth 2.0 PKCE
	XClientID     string
	XClientSecret string
	XRedirectURI  string

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

	// AppEnv is the deployment environment.
	AppEnv string

	// Background worker tuning.
	PublishWorkerIntervalSeconds int

	// S3-compatible storage (mandatory).
	S3Endpoint  string
	S3Bucket    string
	S3AccessKey string
	S3SecretKey string
	S3Region    string

	// MaxUploadBytes caps the size of any single file upload.
	MaxUploadBytes int64
}

// Load reads configuration from environment variables.
func Load() (*Config, error) {
	_ = godotenv.Load()

	cfg := &Config{
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
		TikTokClientID:               getEnv("TIKTOK_CLIENT_ID", ""),
		TikTokClientSecret:           getEnv("TIKTOK_CLIENT_SECRET", ""),
		TikTokRedirectURI:            getEnv("TIKTOK_REDIRECT_URI", "http://localhost:8080/api/v1/auth/tiktok/callback"),
		XClientID:                    getEnv("X_CLIENT_ID", ""),
		XClientSecret:                getEnv("X_CLIENT_SECRET", ""),
		XRedirectURI:                 getEnv("X_REDIRECT_URI", "http://localhost:8080/api/v1/auth/twitter/callback"),
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
		S3Endpoint:                   getEnv("S3_ENDPOINT", ""),
		S3Bucket:                     getEnv("S3_BUCKET", ""),
		S3AccessKey:                  getEnv("S3_ACCESS_KEY", ""),
		S3SecretKey:                  getEnv("S3_SECRET_KEY", ""),
		S3Region:                     getEnv("S3_REGION", ""),
		MaxUploadBytes:               getEnvInt64("STORAGE_MAX_UPLOAD_BYTES", 200*1024*1024),
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) validate() error {
	switch c.AppEnv {
	case "dev", "staging", "production":
	default:
		return fmt.Errorf("APP_ENV must be one of dev|staging|production (got %q)", c.AppEnv)
	}

	// Database: DATABASE_URL takes precedence; individual params fallback.
	if c.DatabaseURL == "" {
		if c.DBPassword == "" {
			return fmt.Errorf("DB_PASSWORD is required (or set DATABASE_URL)")
		}
	}

	// S3-compatible storage (mandatory).
	if c.S3Endpoint == "" {
		return fmt.Errorf("S3_ENDPOINT is required")
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

	// Meta OAuth (optional).
	if c.MetaAppID == "" && c.MetaAppSecret == "" {
		// platform disabled
	} else if c.MetaAppID == "" {
		return fmt.Errorf("META_APP_ID is required when META_APP_SECRET is set (or unset both)")
	} else if c.MetaAppSecret == "" {
		return fmt.Errorf("META_APP_SECRET is required when META_APP_ID is set (or unset both)")
	} else if len(c.MetaAppSecret) < secretMinChars {
		return fmt.Errorf("META_APP_SECRET must be at least %d characters (got %d)", secretMinChars, len(c.MetaAppSecret))
	}

	// Encryption key.
	if c.EncryptionKey == "" {
		return fmt.Errorf("ENCRYPTION_KEY is required (generate with `openssl rand -base64 32`)")
	}
	keyBytes, err := base64.StdEncoding.DecodeString(c.EncryptionKey)
	if err != nil {
		return fmt.Errorf("ENCRYPTION_KEY is not valid base64: %w", err)
	}
	if len(keyBytes) != aesKeyBytes {
		return fmt.Errorf("ENCRYPTION_KEY must decode to exactly %d bytes for AES-256-GCM (got %d)", aesKeyBytes, len(keyBytes))
	}

	// JWT signing key.
	if c.JWTSecret == "" {
		return fmt.Errorf("JWT_SECRET is required (must be at least %d bytes)", jwtSecretMinBytes)
	}
	if len(c.JWTSecret) < jwtSecretMinBytes {
		return fmt.Errorf("JWT_SECRET must be at least %d bytes for HS256 (got %d)", jwtSecretMinBytes, len(c.JWTSecret))
	}

	// Optional OAuth platforms.
	if err := c.validateOptionalPlatform("TIKTOK", c.TikTokClientID, c.TikTokClientSecret); err != nil {
		return err
	}
	if err := c.validateOptionalPlatform("X", c.XClientID, c.XClientSecret); err != nil {
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

func (c *Config) validateOptionalPlatform(name, id, secret string) error {
	if id == "" && secret == "" {
		return nil
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
func (c *Config) DSN() string {
	if c.DatabaseURL != "" {
		return c.DatabaseURL
	}
	return fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		c.DBHost, c.DBPort, c.DBUser, c.DBPassword, c.DBName, c.DBSSLMode,
	)
}

func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

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

func getEnvInt(key string, fallback int) int {
	if value, ok := os.LookupEnv(key); ok {
		if n, err := strconv.Atoi(value); err == nil {
			return n
		}
	}
	return fallback
}

func getEnvInt64(key string, fallback int64) int64 {
	if value, ok := os.LookupEnv(key); ok {
		if n, err := strconv.ParseInt(value, 10, 64); err == nil {
			return n
		}
	}
	return fallback
}
