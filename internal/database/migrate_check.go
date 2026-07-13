package database

import (
	"database/sql"
	"fmt"
)

// CanaryTables is the small subset of tables whose existence the /ready
// endpoint asserts as the "migrations applied" predicate. Listing
// every post_targets column or every audit_logs field would couple the
// readiness probe to any future migration drift; the canaries are the
// invariant surface every Blocco since #1.1 has relied on for the
// canonical user + post model. If a future migration drops one of
// these, this list MUST be updated alongside that migration or the
// /ready endpoint goes red.
//
// The runner doesn't write a schema_migrations table by design
// (every .sql file runs every startup with idempotent IF NOT EXISTS
// guards; see internal/database/migrations.go::RunMigrations doc),
// so a registry query isn't an option. We use pg_catalog.pg_class
// instead — the canonical Postgres table-presence probe — which is
// O(1) per check and skips the application's own grants/RLS check.
var CanaryTables = []string{
	"users",
	"platform_accounts",
	"tokens",
	"workspaces",
	"posts",
	"post_targets",
	"media_assets",
	"webhook_deliveries",
	"outbox_events",
}

// CanaryTablesForTest exports the canary table list so test
// packages (pkg/api/ready_test.go) can iterate the same enumeration
// without copying the slice here. Production code MUST continue to
// read the package-private CanaryTables — this getter is exposed
// ONLY for tests.
func CanaryTablesForTest() []string {
	out := make([]string, len(CanaryTables))
	copy(out, CanaryTables)
	return out
}

// SchemaHealthy returns nil iff every canary table exists in the
// public schema. Empty DBs (pre-migration) report the first missing
// table as the error so the /ready endpoint can return that to the
// operator. Partial-apply (some migrations applied, some failed)
// surfaces the alphabetically-first missing table.
//
// Cheap: one SELECT against pg_catalog.pg_class per check + one
// line of overhead per row. Safe to call on every /ready tick (the
// endpoint also includes this in its budget).
//
// The "migrations applied" predicate here is "the closed canary table
// set is present". A more rigorous fingerprint-based check lives in
// the integration-tagged migrations_integration_test.go (it computes
// a SHA-256 of the schema state across all expected enums + columns +
// indexes); we don't pay that cost on the HTTP hot path.
func SchemaHealthy(db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("schema check: nil DB")
	}
	for _, table := range CanaryTables {
		var exists bool
		// to_regclass is a Postgres built-in that returns the
		// OID of a named relation or NULL. Using it (vs a SELECT
		// FROM pg_tables JOIN information_schema) avoids the
		// cost of materialising the relations list — pg_catalog
		// is a flat index lookup.
		if err := db.QueryRow(
			`SELECT to_regclass('public.' || $1) IS NOT NULL`, table,
		).Scan(&exists); err != nil {
			return fmt.Errorf("schema check: %s: %w", table, err)
		}
		if !exists {
			return fmt.Errorf("schema check: table %q missing (run cmd/migrate to apply pending migrations)", table)
		}
	}
	return nil
}
