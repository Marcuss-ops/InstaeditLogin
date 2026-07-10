package database

import (
	"database/sql"
	"fmt"

	"github.com/Marcuss-ops/InstaeditLogin/internal/config"

	_ "github.com/lib/pq"
)

// Connect establishes a connection to the PostgreSQL database.
func Connect(cfg *config.Config) (*sql.DB, error) {
	db, err := sql.Open("postgres", cfg.DSN())
	if err != nil {
		return nil, fmt.Errorf("failed to open database connection: %w", err)
	}

	// Connection pool settings
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)

	// Verify connection
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return db, nil
}

// Migrate runs database migrations from embedded SQL files. Each file in
// migrations/ is idempotent (CREATE IF NOT EXISTS, ALTER … IF NOT EXISTS)
// and sorted lexicographically so 001_init.sql runs before 002_add_refresh_token.sql.
func Migrate(db *sql.DB) error {
	return RunMigrations(db)
}
