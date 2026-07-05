package config

import (
	"encoding/base64"
	"fmt"
	"os"

	"github.com/joho/godotenv"
)

// Config holds all configuration for the application.
type Config struct {
	// Server
	ServerPort string
	ServerHost string

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

	// YouTube OAuth
	YouTubeClientID     string
	YouTubeClientSecret string
	YouTubeRedirectURI  string

	// Encryption
	EncryptionKey string

	// JWT
	JWTSecret string

	// Logging
	LogLevel string
}

// Load reads configuration from environment variables.
// It first loads the .env file if present (silently ignores if missing).
func Load() (*Config, error) {
	// Load .env file if it exists (non-fatal if missing)
	_ = godotenv.Load()

	cfg := &Config{
		ServerPort:          getEnv("SERVER_PORT", "8080"),
		ServerHost:          getEnv("SERVER_HOST", "0.0.0.0"),
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
		YouTubeClientID:     getEnv("YOUTUBE_CLIENT_ID", ""),
		YouTubeClientSecret: getEnv("YOUTUBE_CLIENT_SECRET", ""),
		YouTubeRedirectURI:  getEnv("YOUTUBE_REDIRECT_URI", "http://localhost:8080/api/v1/auth/youtube/callback"),
		EncryptionKey:       getEnv("ENCRYPTION_KEY", ""),
		JWTSecret:           getEnv("JWT_SECRET", ""),
		LogLevel:            getEnv("LOG_LEVEL", "info"),
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// validate checks that required configuration values are present.
// Only Meta is mandatory; other platforms are optional.
func (c *Config) validate() error {
	if c.DBPassword == "" {
		return fmt.Errorf("DB_PASSWORD is required")
	}
	if c.MetaAppID == "" {
		return fmt.Errorf("META_APP_ID is required")
	}
	if c.MetaAppSecret == "" {
		return fmt.Errorf("META_APP_SECRET is required")
	}
	if c.EncryptionKey == "" {
		return fmt.Errorf("ENCRYPTION_KEY is required")
	}
	// Validate that the encryption key is a valid base64-encoded 32-byte key
	keyBytes, err := base64.StdEncoding.DecodeString(c.EncryptionKey)
	if err != nil {
		return fmt.Errorf("ENCRYPTION_KEY is not valid base64: %w", err)
	}
	if len(keyBytes) != 32 {
		return fmt.Errorf("ENCRYPTION_KEY must decode to 32 bytes (got %d)", len(keyBytes))
	}
	if c.JWTSecret == "" {
		return fmt.Errorf("JWT_SECRET is required")
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
