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
// Every statement is idempotent (CREATE IF NOT EXISTS, ADD COLUMN IF NOT EXISTS,
// DO-block guarded CREATE TYPE).
func RunMigrations(db *sql.DB) error {
	return runMigrationsRange(db, 0)
}

// RunMigrationsUpTo runs all migrations up to and including the migration
// whose filename starts with the given sequence number (e.g. 27 runs
// 001..027). Useful for testing: insert data after N-1 migrations, then
// apply migration N to verify its behaviour.
func RunMigrationsUpTo(db *sql.DB, maxSeq int) error {
	return runMigrationsRange(db, maxSeq)
}

func runMigrationsRange(db *sql.DB, maxSeq int) error {
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

		// If a maxSeq is set, skip migrations beyond it.
		if maxSeq > 0 {
			seq := extractMigrationSeq(entry.Name())
			if seq > maxSeq {
				continue
			}
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

// extractMigrationSeq parses the leading digits from a filename like
// "028_multi_tenancy.sql" → 28.
func extractMigrationSeq(name string) int {
	var seq int
	for _, c := range name {
		if c >= '0' && c <= '9' {
			seq = seq*10 + int(c-'0')
		} else {
			break
		}
	}
	return seq
}
