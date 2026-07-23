//go:build integration

package database

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/testutil/postgres"
)

// TestMigrations_TracksAppliedMigrations verifies that RunMigrations
// records every applied migration in schema_migrations with a checksum.
func TestMigrations_TracksAppliedMigrations(t *testing.T) {
	db, cleanup := postgres.StartTestPostgres(t)
	defer cleanup()

	if err := RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	rows, err := db.Query("SELECT filename, checksum, applied_at FROM schema_migrations ORDER BY filename")
	if err != nil {
		t.Fatalf("query schema_migrations: %v", err)
	}
	defer rows.Close()

	recorded := make(map[string]string)
	for rows.Next() {
		var filename, checksum string
		var appliedAt interface{}
		if err := rows.Scan(&filename, &checksum, &appliedAt); err != nil {
			t.Fatalf("scan schema_migrations: %v", err)
		}
		recorded[filename] = checksum
		if appliedAt == nil {
			t.Errorf("applied_at missing for %s", filename)
		}
	}

	files, err := loadMigrationFiles(0)
	if err != nil {
		t.Fatalf("loadMigrationFiles: %v", err)
	}
	if len(recorded) != len(files) {
		t.Fatalf("schema_migrations row count mismatch: want %d, got %d", len(files), len(recorded))
	}
	for _, file := range files {
		checksum, ok := recorded[file.name]
		if !ok {
			t.Errorf("missing schema_migrations record for %s", file.name)
			continue
		}
		if checksum != file.checksum {
			t.Errorf("checksum mismatch for %s: want %s, got %s", file.name, file.checksum, checksum)
		}
	}
}

// TestMigrations_IdempotentReRun verifies that running the migration
// runner twice against the same database succeeds and does not add
// duplicate records to schema_migrations.
func TestMigrations_IdempotentReRun(t *testing.T) {
	db, cleanup := postgres.StartTestPostgres(t)
	defer cleanup()

	if err := RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations (first): %v", err)
	}

	countBefore := migrationRowCount(t, db)

	if err := RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations (second): %v", err)
	}

	countAfter := migrationRowCount(t, db)
	if countAfter != countBefore {
		t.Errorf("schema_migrations row count changed on re-run: before %d, after %d", countBefore, countAfter)
	}
}

// TestMigrations_ChecksumMismatchFails verifies that the runner refuses
// to proceed if an applied migration file has been modified after it was
// recorded.
func TestMigrations_ChecksumMismatchFails(t *testing.T) {
	db, cleanup := postgres.StartTestPostgres(t)
	defer cleanup()

	if err := RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	files, err := loadMigrationFiles(0)
	if err != nil {
		t.Fatalf("loadMigrationFiles: %v", err)
	}
	if len(files) == 0 {
		t.Skip("no migrations to test checksum mismatch")
	}
	first := files[0].name

	_, err = db.Exec("UPDATE schema_migrations SET checksum = 'deadbeef' WHERE filename = $1", first)
	if err != nil {
		t.Fatalf("update checksum: %v", err)
	}

	if err := RunMigrations(db); err == nil {
		t.Fatalf("expected error when migration %s checksum changed, got nil", first)
	} else if !strings.Contains(err.Error(), first) {
		t.Fatalf("expected error to mention %s, got: %v", first, err)
	}
}

// TestMigrations_RunMigrationsUpToResumes verifies that applying a subset
// of migrations and then resuming applies only the remaining ones.
func TestMigrations_RunMigrationsUpToResumes(t *testing.T) {
	db, cleanup := postgres.StartTestPostgres(t)
	defer cleanup()

	if err := RunMigrationsUpTo(db, 27); err != nil {
		t.Fatalf("RunMigrationsUpTo(27): %v", err)
	}

	countBefore := migrationRowCount(t, db)

	if err := RunMigrationsUpTo(db, 28); err != nil {
		t.Fatalf("RunMigrationsUpTo(28): %v", err)
	}

	countAfter := migrationRowCount(t, db)
	if countAfter <= countBefore {
		t.Fatalf("expected more migration records after resuming, before=%d after=%d", countBefore, countAfter)
	}

	// A full run after partial application should be a no-op.
	if err := RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations after partial: %v", err)
	}
}

func migrationRowCount(t *testing.T, db *sql.DB) int {
	t.Helper()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&count); err != nil {
		t.Fatalf("count schema_migrations: %v", err)
	}
	return count
}
