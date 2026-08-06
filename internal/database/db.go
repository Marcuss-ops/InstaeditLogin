package database

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/config"

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
	if maxOpen <= 0 {
		maxOpen = 25
	}
	maxIdle := cfg.DBMaxIdleConns
	if maxIdle < 0 {
		maxIdle = 0
	}
	if maxIdle > maxOpen {
		maxIdle = maxOpen
	}
	lifetime := time.Duration(cfg.DBConnMaxLifetimeSeconds) * time.Second
	if lifetime <= 0 {
		lifetime = 30 * time.Minute
	}
	idleTime := time.Duration(cfg.DBConnMaxIdleTimeSeconds) * time.Second
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

// configurePool applies explicit database/sql pool limits and connection
// recycling settings. Keeping this separate from Ping makes the sizing
// policy unit-testable without requiring a live PostgreSQL server.
func configurePool(db *sql.DB, cfg *config.DatabaseConfig) {
	settings := normalizePoolSettings(cfg)
	db.SetMaxOpenConns(settings.maxOpen)
	db.SetMaxIdleConns(settings.maxIdle)
	db.SetConnMaxLifetime(settings.maxLifetime)
	db.SetConnMaxIdleTime(settings.maxIdleTime)
}

// Migrate runs database migrations from embedded SQL files. Each file in
// migrations/ is idempotent (CREATE IF NOT EXISTS, ALTER … IF NOT EXISTS)
// and sorted lexicographically so 001_init.sql runs before 002_add_refresh_token.sql.
func Migrate(db *sql.DB) error {
	return RunMigrations(db)
}
