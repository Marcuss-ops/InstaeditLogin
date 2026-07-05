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

// Migrate runs database migrations to create required tables.
func Migrate(db *sql.DB) error {
	migrations := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id            BIGSERIAL PRIMARY KEY,
			email         VARCHAR(255) UNIQUE,
			meta_user_id  VARCHAR(255) UNIQUE NOT NULL,
			name          VARCHAR(255),
			created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE TABLE IF NOT EXISTS instagram_accounts (
			id                  BIGSERIAL PRIMARY KEY,
			user_id             BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			instagram_user_id   VARCHAR(255) UNIQUE NOT NULL,
			username            VARCHAR(255),
			created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE TABLE IF NOT EXISTS tokens (
			id              BIGSERIAL PRIMARY KEY,
			user_id         BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			account_id      BIGINT REFERENCES instagram_accounts(id) ON DELETE CASCADE,
			token_type      VARCHAR(50) NOT NULL,
			encrypted_token BYTEA NOT NULL,
			expires_at      TIMESTAMPTZ,
			scopes          TEXT[],
			created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_tokens_user_id ON tokens(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_tokens_account_id ON tokens(account_id)`,
		`CREATE INDEX IF NOT EXISTS idx_instagram_accounts_user_id ON instagram_accounts(user_id)`,
	}

	for i, migration := range migrations {
		if _, err := db.Exec(migration); err != nil {
			return fmt.Errorf("migration %d failed: %w", i+1, err)
		}
	}

	return nil
}
