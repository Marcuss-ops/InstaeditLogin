package database

import (
	"database/sql"
	"embed"
	"fmt"
	"sort"
	"strings"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// RunMigrations reads all embedded SQL migration files from migrations/,
// sorts them lexicographically, and executes each against the database.
// Every statement is idempotent (CREATE IF NOT EXISTS, ADD COLUMN IF NOT EXISTS).
func RunMigrations(db *sql.DB) error {
	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("failed to read embedded migrations: %w", err)
	}

	// Sort lexicographically so 001 runs before 002.
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}

		sqlBytes, err := migrationFiles.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return fmt.Errorf("failed to read migration %s: %w", entry.Name(), err)
		}

		if _, err := db.Exec(string(sqlBytes)); err != nil {
			return fmt.Errorf("migration %s failed: %w", entry.Name(), err)
		}
	}

	return nil
}
