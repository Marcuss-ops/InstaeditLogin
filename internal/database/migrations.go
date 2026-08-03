package database

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// advisoryLockKey is an arbitrary 64-bit integer used with
// pg_advisory_lock to serialize concurrent migration runs.
const advisoryLockKey int64 = 84739201948

// migrationOperationTimeout bounds the entire migration run so a stuck
// migration cannot hold an advisory lock or a connection forever.
const migrationOperationTimeout = 5 * time.Minute

// migrationLockTimeout bounds the time we are willing to wait to
// acquire the advisory lock from another runner.
const migrationLockTimeout = 30 * time.Second

type migrationFile struct {
	name     string
	seq      int
	body     string
	checksum string
}

// RunMigrations reads all embedded SQL migration files from migrations/,
// sorts them lexicographically, and executes each against the database.
// Already-applied migrations (tracked in schema_migrations) are skipped
// and their checksums are verified. Each migration runs inside a
// transaction, and the whole run is protected by a PostgreSQL advisory
// lock to prevent concurrent runners.
func RunMigrations(db *sql.DB) error {
	return runMigrationsRange(db, 0, "")
}

// MigrateWithExpectedInstallationUUID applies migrations while exposing the
// operator-provided installation UUID to the identity migration. On a new
// database that UUID seeds the singleton row; on an existing database the
// row is left untouched and VerifyInstallationIdentity performs the check.
func MigrateWithExpectedInstallationUUID(db *sql.DB, expectedUUID string) error {
	expectedUUID = strings.TrimSpace(expectedUUID)
	if expectedUUID != "" {
		if _, err := uuid.Parse(expectedUUID); err != nil {
			return fmt.Errorf("migrations: %w: configured installation UUID is invalid", ErrDatabaseIdentityMismatch)
		}
	}
	return runMigrationsRange(db, 0, expectedUUID)
}

// RunMigrationsUpTo runs all migrations up to and including the migration
// whose filename starts with the given sequence number (e.g. 27 runs
// 001..027). Useful for testing: insert data after N-1 migrations, then
// apply migration N to verify its behaviour.
func RunMigrationsUpTo(db *sql.DB, maxSeq int) error {
	return runMigrationsRange(db, maxSeq, "")
}

func runMigrationsRange(db *sql.DB, maxSeq int, expectedUUID string) error {
	files, err := loadMigrationFiles(maxSeq)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), migrationOperationTimeout)
	defer cancel()

	// Use a dedicated connection so the advisory lock is held for the
	// entire session and all migration transactions run on the same
	// database connection.
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("migrations: failed to reserve database connection: %w", err)
	}
	defer func() { _ = conn.Close() }()

	if err := acquireAdvisoryLock(ctx, conn); err != nil {
		return err
	}
	if expectedUUID != "" {
		if err := verifyExistingInstallationIdentity(ctx, conn, expectedUUID); err != nil {
			return err
		}
		if _, err := conn.ExecContext(ctx,
			"SELECT set_config('app.expected_installation_uuid', $1, false)", expectedUUID,
		); err != nil {
			return fmt.Errorf("migrations: failed to set installation identity context: %w", err)
		}
	}
	defer func() {
		_, _ = conn.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1)", advisoryLockKey)
	}()

	if err := ensureSchemaMigrationsTable(ctx, conn); err != nil {
		return err
	}

	applied, err := loadAppliedMigrations(ctx, conn)
	if err != nil {
		return err
	}

	for _, file := range files {
		record, exists := applied[file.name]
		if exists {
			if record.Checksum != file.checksum {
				return fmt.Errorf(
					"migration %s has been modified after it was applied (recorded checksum %s, current %s)",
					file.name, record.Checksum, file.checksum,
				)
			}
			continue
		}

		if err := applyMigration(ctx, conn, file); err != nil {
			return err
		}
	}

	return nil
}

func loadMigrationFiles(maxSeq int) ([]migrationFile, error) {
	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		return nil, fmt.Errorf("migrations: failed to read embedded migrations: %w", err)
	}

	// Sort lexicographically so 001 runs before 002.
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	var files []migrationFile
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}

		seq := extractMigrationSeq(entry.Name())
		if maxSeq > 0 && seq > maxSeq {
			continue
		}

		bodyBytes, err := migrationFiles.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return nil, fmt.Errorf("migrations: failed to read migration %s: %w", entry.Name(), err)
		}

		sum := sha256.Sum256(bodyBytes)
		files = append(files, migrationFile{
			name:     entry.Name(),
			seq:      seq,
			body:     string(bodyBytes),
			checksum: hex.EncodeToString(sum[:]),
		})
	}

	return files, nil
}

// verifyExistingInstallationIdentity is the migration preflight. A missing
// table is the only allowed enrollment state: migration 096 will create it.
// Once the table exists, the singleton row must exist and match before any
// pending migration is applied. This prevents a wrong/rebuilt database from
// receiving further schema changes before the identity mismatch is reported.
func verifyExistingInstallationIdentity(ctx context.Context, conn *sql.Conn, expectedUUID string) error {
	var tableExists bool
	if err := conn.QueryRowContext(ctx,
		`SELECT to_regclass('public.system_installation') IS NOT NULL`,
	).Scan(&tableExists); err != nil {
		return fmt.Errorf("migrations: %w: identity preflight unavailable", ErrDatabaseIdentityMismatch)
	}
	if !tableExists {
		return nil
	}

	var actualUUID string
	if err := conn.QueryRowContext(ctx,
		`SELECT installation_uuid::text FROM system_installation WHERE id = 1`,
	).Scan(&actualUUID); err != nil {
		return fmt.Errorf("migrations: %w: existing installation identity is invalid", ErrDatabaseIdentityMismatch)
	}
	expected, err := uuid.Parse(expectedUUID)
	if err != nil {
		return fmt.Errorf("migrations: %w: configured installation UUID is invalid", ErrDatabaseIdentityMismatch)
	}
	actual, err := uuid.Parse(strings.TrimSpace(actualUUID))
	if err != nil || actual != expected {
		return fmt.Errorf("migrations: %w: database installation is not the expected installation", ErrDatabaseIdentityMismatch)
	}
	return nil
}

func acquireAdvisoryLock(ctx context.Context, conn *sql.Conn) error {
	lockCtx, cancel := context.WithTimeout(ctx, migrationLockTimeout)
	defer cancel()

	var acquired bool
	if err := conn.QueryRowContext(lockCtx, "SELECT pg_try_advisory_lock($1)", advisoryLockKey).Scan(&acquired); err != nil {
		return fmt.Errorf("migrations: failed to acquire advisory lock: %w", err)
	}
	if !acquired {
		return fmt.Errorf("migrations: could not acquire advisory lock (key %d); another migration may be running", advisoryLockKey)
	}
	return nil
}

func ensureSchemaMigrationsTable(ctx context.Context, conn *sql.Conn) error {
	const createTableSQL = `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			filename TEXT PRIMARY KEY,
			checksum TEXT NOT NULL,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);
	`
	if _, err := conn.ExecContext(ctx, createTableSQL); err != nil {
		return fmt.Errorf("migrations: failed to create schema_migrations table: %w", err)
	}
	return nil
}

type migrationRecord struct {
	Filename string
	Checksum string
}

func loadAppliedMigrations(ctx context.Context, conn *sql.Conn) (map[string]migrationRecord, error) {
	rows, err := conn.QueryContext(ctx, "SELECT filename, checksum FROM schema_migrations")
	if err != nil {
		return nil, fmt.Errorf("migrations: failed to query schema_migrations: %w", err)
	}
	defer rows.Close()

	applied := make(map[string]migrationRecord)
	for rows.Next() {
		var rec migrationRecord
		if err := rows.Scan(&rec.Filename, &rec.Checksum); err != nil {
			return nil, fmt.Errorf("migrations: failed to scan schema_migrations: %w", err)
		}
		applied[rec.Filename] = rec
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("migrations: failed reading schema_migrations: %w", err)
	}
	return applied, nil
}

func applyMigration(ctx context.Context, conn *sql.Conn, file migrationFile) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("migrations: failed to begin transaction for %s: %w", file.name, err)
	}
	defer func() { _ = tx.Rollback() }()

	// The runner owns the transaction boundary. A legacy migration may still
	// contain standalone BEGIN;/COMMIT; lines; remove those wrappers before
	// executing it so PostgreSQL does not commit the runner transaction early.
	if _, err := tx.ExecContext(ctx, migrationSQLForTransaction(file.body)); err != nil {
		return fmt.Errorf("migration %s failed: %w", file.name, err)
	}

	if _, err := tx.ExecContext(ctx,
		"INSERT INTO schema_migrations (filename, checksum, applied_at) VALUES ($1, $2, NOW())",
		file.name, file.checksum,
	); err != nil {
		return fmt.Errorf("migrations: failed to record migration %s: %w", file.name, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("migrations: failed to commit migration %s: %w", file.name, err)
	}

	return nil
}

// migrationSQLForTransaction removes only outer standalone transaction
// wrappers from a migration body. RunMigrations already wraps every
// migration, so an outer BEGIN/COMMIT pair would otherwise leave the *sql.Tx
// idle before the runner records the migration. Inner BEGIN/COMMIT lines,
// including those inside procedural SQL, are preserved.
func migrationSQLForTransaction(body string) string {
	lines := strings.Split(body, "\n")
	first := 0
	for first < len(lines) {
		trimmed := strings.TrimSpace(lines[first])
		if trimmed == "" || strings.HasPrefix(trimmed, "--") {
			first++
			continue
		}
		break
	}
	if first < len(lines) && strings.TrimSpace(lines[first]) == "BEGIN;" {
		lines = append(lines[:first], lines[first+1:]...)
	}

	last := len(lines) - 1
	for last >= 0 && (strings.TrimSpace(lines[last]) == "" || strings.HasPrefix(strings.TrimSpace(lines[last]), "--")) {
		last--
	}
	if last >= 0 && strings.TrimSpace(lines[last]) == "COMMIT;" {
		lines = append(lines[:last], lines[last+1:]...)
	}
	return strings.Join(lines, "\n")
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
