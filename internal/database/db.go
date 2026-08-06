package database

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/config"
	"github.com/Marcuss-ops/InstaeditLogin/pkg/metrics"

	_ "github.com/lib/pq"
)

// Connect establishes a connection to the PostgreSQL database.
func Connect(cfg *config.DatabaseConfig) (*sql.DB, error) {
	db, err := sql.Open(instrumentedPostgresDriverName, cfg.DSN())
	if err != nil {
		return nil, fmt.Errorf("failed to open database connection: %w", err)
	}

	configurePool(db, cfg)

	// Verify connection
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return db, nil
}

type poolSettings struct {
	maxOpen     int
	maxIdle     int
	maxLifetime time.Duration
	maxIdleTime time.Duration
}

func normalizePoolSettings(cfg *config.DatabaseConfig) poolSettings {
	maxOpen := cfg.DBMaxOpenConns
	maxIdle := cfg.DBMaxIdleConns
	lifetimeSeconds := cfg.DBConnMaxLifetimeSeconds
	idleTimeSeconds := cfg.DBConnMaxIdleTimeSeconds

	if profile, ok := cfg.Profile(); ok {
		maxOpen = profile.MaxOpenConns
		maxIdle = profile.MaxIdleConns
		lifetimeSeconds = profile.ConnMaxLifetimeSeconds
		idleTimeSeconds = profile.ConnMaxIdleTimeSeconds
	}
	if maxOpen <= 0 {
		maxOpen = 25
	}
	if maxIdle < 0 {
		maxIdle = 0
	}
	if maxIdle > maxOpen {
		maxIdle = maxOpen
	}
	lifetime := time.Duration(lifetimeSeconds) * time.Second
	if lifetime <= 0 {
		lifetime = 30 * time.Minute
	}
	idleTime := time.Duration(idleTimeSeconds) * time.Second
	if idleTime <= 0 {
		idleTime = 5 * time.Minute
	}
	return poolSettings{
		maxOpen:     maxOpen,
		maxIdle:     maxIdle,
		maxLifetime: lifetime,
		maxIdleTime: idleTime,
	}
}

// Profile returns the selected role's pool profile. An unset role keeps
// the legacy DB_* settings, preserving compatibility for tools and tests.
// configurePool applies explicit database/sql pool limits and connection
// recycling settings. Keeping this separate from Ping makes the sizing
// policy unit-testable without requiring a live PostgreSQL server.
func configurePool(db *sql.DB, cfg *config.DatabaseConfig) {
	settings := normalizePoolSettings(cfg)
	db.SetMaxOpenConns(settings.maxOpen)
	db.SetMaxIdleConns(settings.maxIdle)
	db.SetConnMaxLifetime(settings.maxLifetime)
	db.SetConnMaxIdleTime(settings.maxIdleTime)
	profile := "legacy"
	if cfg != nil && cfg.DBPoolRole != "" {
		profile = cfg.DBPoolRole
	}
	metrics.SetDatabasePoolConfigured(profile, settings.maxOpen, settings.maxIdle)
}

// Migrate runs database migrations from embedded SQL files. Each file in
// migrations/ is idempotent (CREATE IF NOT EXISTS, ALTER … IF NOT EXISTS)
// and sorted lexicographically so 001_init.sql runs before 002_add_refresh_token.sql.
func Migrate(db *sql.DB) error {
	return RunMigrations(db)
}
