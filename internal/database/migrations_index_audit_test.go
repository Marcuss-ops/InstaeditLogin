//go:build integration

// Package database contains the migration integration tests.
package database

import (
	"database/sql"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/testutil/postgres"
)

// TestMigration103_PlatformAccountsUserStatusIndex verifies the Fase 7
// index audit for the N+1 fix aggregated list query
// (GET /api/v1/accounts batch LEFT JOIN on account_resource_snapshots):
//
//   - the ONLY missing DoD index — platform_accounts(user_id, status) —
//     exists after migration 103 applies;
//   - the other three DoD targets are already covered by PRIMARY KEYs
//     (account_resource_snapshots(platform_account_id),
//     group_accounts(group_id, account_id),
//     workspace_channels(workspace_id, platform_account_id)) — asserted
//     here so a future migration can't silently drop the coverage;
//   - re-running the runner is a no-op (idempotency contract).
func TestMigration103_PlatformAccountsUserStatusIndex(t *testing.T) {
	db, cleanup := postgres.StartTestPostgres(t)
	defer cleanup()

	if err := RunMigrationsUpTo(db, 102); err != nil {
		t.Fatalf("RunMigrationsUpTo(102): %v", err)
	}

	// Pre-condition: composite index must NOT exist before 103.
	assertIndexExists(t, db, "idx_platform_accounts_user_status", false)

	if err := RunMigrationsUpTo(db, 103); err != nil {
		t.Fatalf("RunMigrationsUpTo(103): %v", err)
	}

	// The new composite index is present and shaped (user_id, status).
	assertIndexExists(t, db, "idx_platform_accounts_user_status", true)

	// The other three DoD index targets are covered by PRIMARY KEYs that
	// must remain present (left-join probe on the index definition).
	assertIndexExists(t, db, "account_resource_snapshots_pkey", true)
	assertIndexExists(t, db, "group_accounts_pkey", true)
	assertIndexExists(t, db, "workspace_channels_pkey", true)

	// Pre-existing single-column index on user_id stays (read path also
	// uses the plain user_id filter for other surfaces).
	assertIndexExists(t, db, "idx_platform_accounts_user_id", true)

	// Idempotency: a second run must succeed without drift.
	var before int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE filename = '103_platform_accounts_user_status_index.sql'`).Scan(&before); err != nil {
		t.Fatalf("count migration 103 before rerun: %v", err)
	}
	if before != 1 {
		t.Fatalf("migration 103 registry rows before rerun = %d, want 1", before)
	}
	if err := RunMigrationsUpTo(db, 103); err != nil {
		t.Fatalf("RunMigrationsUpTo(103) second pass: %v", err)
	}
	var after int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE filename = '103_platform_accounts_user_status_index.sql'`).Scan(&after); err != nil {
		t.Fatalf("count migration 103 after rerun: %v", err)
	}
	if after != before {
		t.Fatalf("migration 103 registry rows changed on rerun: before=%d after=%d", before, after)
	}

	// The composite must really be (user_id, status) so the paginated
	// status NOT IN (...) predicate is answered inside the index.
	var definition string
	if err := db.QueryRow(`SELECT indexdef FROM pg_indexes WHERE schemaname = 'public' AND indexname = 'idx_platform_accounts_user_status'`).Scan(&definition); err != nil {
		t.Fatalf("read composite index definition: %v", err)
	}
	if definition != "CREATE INDEX idx_platform_accounts_user_status ON public.platform_accounts USING btree (user_id, status)" {
		t.Fatalf("composite index definition mismatch:\nwant: CREATE INDEX idx_platform_accounts_user_status ON public.platform_accounts USING btree (user_id, status)\ngot:  %s", definition)
	}
}

func assertIndexExists(t *testing.T, db *sql.DB, name string, want bool) {
	t.Helper()
	var exists bool
	if err := db.QueryRow(`SELECT EXISTS (SELECT 1 FROM pg_indexes WHERE schemaname = 'public' AND indexname = $1)`, name).Scan(&exists); err != nil {
		t.Fatalf("check index %s: %v", name, err)
	}
	if exists != want {
		t.Fatalf("index %s presence = %v, want %v", name, exists, want)
	}
}
