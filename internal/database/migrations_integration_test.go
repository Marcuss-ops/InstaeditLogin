//go:build integration

// Package database_test contains the migration integration tests.
// They run under the `integration` build tag (go test -tags=integration)
// so unit-test runs (go test ./...) are not blocked when Docker is
// unavailable.
package database

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"testing"
	"time"

	_ "github.com/lib/pq"
	tpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// migrationsToTest is the closed set this file exercises. Matches the
// user-facing scope "migrations 001→012 inclusive". Files between
// (007..010, 013..016) are intentionally excluded — migrations 011 and
// 012 are the latest in the user-requested range.
//
// Note: only ONE `011_target_*.sql` file remains on disk. The
// earlier 011_target_publish_state.sql was consolidated away
// (commit renamed to drop the unused publish_state column). Migration
// 027_drop_publish_state.sql converges production databases that
// had already applied the old pair; new greenfield installs (and
// this testcontainer) never have the column in the first place.
var migrationsToTest = []string{
	"001_init.sql",
	"002_add_refresh_token.sql",
	"003_posts_workspaces.sql",
	"004_composite_token_index.sql",
	"005_account_lifecycle.sql",
	"006_media_assets.sql",
	"011_target_provider_state.sql",
	"012_async_threads_support.sql",
}

// expectedPostStatusActive is the documented 7-value active enum set
// after migration 012 has applied. The migration 003 CREATE TYPE
// introduces 5 values; migration 012 ADD VALUE introduces 3 more
// (waiting_provider / queued / partially_published). 'queued' is the
// rename target of the legacy 'scheduled' value which remains in the
// enum for back-compat with rows already inserted pre-012.
//
// Net on-disk enum labels after 012 = 5 (003) + 3 (012) = 8
// (the 7 active + the 1 deprecated 'scheduled').
var expectedPostStatusActive = map[string]bool{
	"draft":               true,
	"queued":              true,
	"publishing":          true,
	"published":           true,
	"failed":              true,
	"waiting_provider":    true,
	"partially_published": true,
}

// requiredColumns lists (table, column) tuples the test asserts exist
// after migrations 001→012 have applied. Derived from internal/models/
// post.go + the migration SQL bodies — every column the Go model
// reaches for via Scan/Query must be present and reachable.
var requiredColumns = []struct{ Table, Column string }{
	// 001_init
	{"users", "id"}, {"users", "email"}, {"users", "name"}, {"users", "created_at"}, {"users", "updated_at"},
	{"platform_accounts", "id"}, {"platform_accounts", "user_id"}, {"platform_accounts", "platform"}, {"platform_accounts", "platform_user_id"},
	{"platform_accounts", "username"}, {"platform_accounts", "created_at"}, {"platform_accounts", "updated_at"},
	{"tokens", "id"}, {"tokens", "platform_account_id"}, {"tokens", "token_type"}, {"tokens", "encrypted_token"},
	{"tokens", "expires_at"}, {"tokens", "scopes"}, {"tokens", "created_at"},
	// 002_add_refresh_token
	{"tokens", "encrypted_refresh_token"},
	// 003_posts_workspaces
	{"workspaces", "id"}, {"workspaces", "name"}, {"workspaces", "owner_id"}, {"workspaces", "created_at"},
	{"platform_accounts", "workspace_id"},
	{"posts", "id"}, {"posts", "workspace_id"}, {"posts", "title"}, {"posts", "caption"},
	{"posts", "media_url"}, {"posts", "scheduled_at"}, {"posts", "status"}, {"posts", "created_at"},
	{"post_targets", "id"}, {"post_targets", "post_id"}, {"post_targets", "platform_account_id"},
	{"post_targets", "status"}, {"post_targets", "platform_post_id"}, {"post_targets", "error_message"}, {"post_targets", "published_at"},
	// 005_account_lifecycle
	{"platform_accounts", "status"}, {"platform_accounts", "connected_at"},
	{"platform_accounts", "last_validated_at"}, {"platform_accounts", "last_refresh_at"},
	{"platform_accounts", "reauth_required_at"}, {"platform_accounts", "last_error_code"},
	{"platform_accounts", "last_error_message"}, {"platform_accounts", "metadata"},
	{"audit_logs", "id"}, {"audit_logs", "user_id"}, {"audit_logs", "session_id"}, {"audit_logs", "action"},
	{"audit_logs", "resource_type"}, {"audit_logs", "resource_id"}, {"audit_logs", "result"},
	{"audit_logs", "ip_hash"}, {"audit_logs", "metadata"}, {"audit_logs", "created_at"},
	// 006_media_assets
	{"media_assets", "id"}, {"media_assets", "user_id"}, {"media_assets", "upload_key"},
	{"media_assets", "content_type"}, {"media_assets", "size_bytes"}, {"media_assets", "status"},
	{"media_assets", "sha256"}, {"media_assets", "error_message"},
	{"media_assets", "expires_at"}, {"media_assets", "created_at"}, {"media_assets", "updated_at"},
	// 011_target_provider_state (canonical 011 after consolidation;
	// 011_target_publish_state.sql was deleted along with its column)
	{"post_targets", "provider_state"},
	// 012_async_threads_support + posts updates + post_targets updates
	{"posts", "idempotency_key"}, {"posts", "version"}, {"posts", "updated_at"},
	{"post_targets", "version"}, {"post_targets", "created_at"}, {"post_targets", "updated_at"},
	{"post_targets", "container_id"},
}

// ────────────────────────────────────────────────────────────────────
//  Tests
// ────────────────────────────────────────────────────────────────────

// TestMigrations_001To012_AppliesCleanly: gate-keeping test. Running
// the migration runner against an empty database must succeed without
// any error message. If this fails, the other tests don't run.
func TestMigrations_001To012_AppliesCleanly(t *testing.T) {
	requireDocker(t)
	db, cleanup := startTestPostgres(t)
	defer cleanup()

	if err := RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
}

// TestPostStatus_HasExpectedSevenValues: the 7-value active set is
// documented in docs/SANDBOX.md + API/openapi.yaml (Taglio 5.x SSOT).
// Per migration 003 (CREATE TYPE post_status AS ENUM) + 012 (ADD VALUE
// waiting_provider / queued / partially_published), the on-disk enum
// has 8 labels (7 active + 'scheduled' deprecated back-compat alias).
//
// This test catches schema drift: if a future migration accidentally
// drops an active value OR adds a third alias, CI fails.
func TestPostStatus_HasExpectedSevenValues(t *testing.T) {
	requireDocker(t)
	db, cleanup := startTestPostgres(t)
	defer cleanup()

	if err := RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	rows, err := db.Query(`
		SELECT e.enumlabel
		  FROM pg_enum e
		  JOIN pg_type t ON t.oid = e.enumtypid
		 WHERE t.typname = 'post_status'
		 ORDER BY e.enumsortorder
	`)
	if err != nil {
		t.Fatalf("query pg_enum: %v", err)
	}
	defer rows.Close()

	var got []string
	for rows.Next() {
		var label string
		if err := rows.Scan(&label); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, label)
	}

	activeCount := 0
	deprecatedCount := 0
	for _, l := range got {
		if expectedPostStatusActive[l] {
			activeCount++
		} else if l == "scheduled" {
			deprecatedCount++
		} else {
			t.Errorf("unexpected post_status enum label on disk: %q (full set: %v)", l, got)
		}
	}

	if activeCount != len(expectedPostStatusActive) {
		t.Errorf("active post_status count: want %d (the documented set), got %d (labels: %v)",
			len(expectedPostStatusActive), activeCount, got)
	}
	if deprecatedCount > 1 {
		t.Errorf("found %d deprecated aliases (only 'scheduled' expected): %v", deprecatedCount+1, got)
	}
}

// TestColumns_AllExpectedPresent: every (table, column) the Go model
// layer reaches for must exist after migrations 001→012. Drift would
// show up here with a clear FAIL message naming the missing column.
func TestColumns_AllExpectedPresent(t *testing.T) {
	requireDocker(t)
	db, cleanup := startTestPostgres(t)
	defer cleanup()

	if err := RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	present := map[string]map[string]bool{}
	rows, err := db.Query(`
		SELECT table_name, column_name
		  FROM information_schema.columns
		 WHERE table_schema = 'public'
	`)
	if err != nil {
		t.Fatalf("query columns: %v", err)
	}
	for rows.Next() {
		var tn, cn string
		if err := rows.Scan(&tn, &cn); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if present[tn] == nil {
			present[tn] = map[string]bool{}
		}
		present[tn][cn] = true
	}
	rows.Close()

	missing := 0
	for _, want := range requiredColumns {
		if !present[want.Table][want.Column] {
			t.Errorf("column missing: %s.%s", want.Table, want.Column)
			missing++
		}
	}
	if missing > 0 {
		t.Logf("Run `internal/database/db.go:Migrate(...)` locally to see the diff.")
	}
}

// TestMigrations_OrderIndependent: idempotency + order-tolerance.
//   1. Apply 001→012 in canonical lexical order, hash the schema.
//   2. Re-apply 001→012 in canonical order. Hash must match — proves
//      the `IF NOT EXISTS` + DO-block guards actually work.
//   3. Apply migrations 001→012 in REVERSE lexical order. Hash must
//      STILL match — proves no migration is silently order-dependent
//      (e.g. relying on a column added later).
//
// This catches the class of regression where migration N tries to
// `ALTER TABLE foo ADD bar` without `IF NOT EXISTS` and the second
// migration (different one) drops-and-readds bar under another name.
func TestMigrations_OrderIndependent(t *testing.T) {
	requireDocker(t)
	db, cleanup := startTestPostgres(t)
	defer cleanup()

	// 1. canonical first-pass
	if err := RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations (canonical, 1st): %v", err)
	}
	canonical := schemaFingerprint(t, db)

	// 2. canonical re-run
	if err := RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations (canonical, 2nd): %v", err)
	}
	if got := schemaFingerprint(t, db); got != canonical {
		t.Errorf("schema drifted on idempotent re-run:\nbefore: %s\nafter:  %s", canonical, got)
	} else {
		t.Logf("✓ canonical re-run idempotent (sha256 %s)", first16(canonical))
	}

	// 3. reverse-order re-run. We bypass RunMigrations and invoke
	//    each migration body in reversed lexical order directly.
	bodies, err := readMigrationBodies(t)
	if err != nil {
		t.Fatalf("readMigrationBodies: %v", err)
	}
	order := append([]string(nil), migrationsToTest...)
	sort.Sort(sort.Reverse(sort.StringSlice(order)))
	for _, name := range order {
		if _, err := db.Exec(bodies[name]); err != nil {
			t.Fatalf("reverse-order apply %s: %v", name, err)
		}
	}
	if got := schemaFingerprint(t, db); got != canonical {
		t.Errorf("schema drifted on reverse-order re-run:\ncanonical: %s\nreverse:   %s", canonical, got)
	} else {
		t.Logf("✓ reverse-order re-run idempotent (sha256 %s)", first16(canonical))
	}
}

// ────────────────────────────────────────────────────────────────────
//  helpers
// ────────────────────────────────────────────────────────────────────

// requireDocker short-circuits the test if Docker isn't available so
// dev environments without Docker don't see false failures.
func requireDocker(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skipf("docker not on PATH: %v", err)
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skipf("docker daemon not reachable: %v", err)
	}
}

// startTestPostgres spins up an ephemeral Postgres 16-alpine via
// testcontainers-go. Returns the *sql.DB and a cleanup function that
// terminates the container.
func startTestPostgres(t *testing.T) (*sql.DB, func()) {
	t.Helper()
	ctx := context.Background()

	pgC, err := tpostgres.Run(ctx,
		"postgres:16-alpine",
		tpostgres.WithDatabase("instaedit_test"),
		tpostgres.WithUsername("test"),
		tpostgres.WithPassword("test"),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}

	dsn, err := pgC.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("ConnectionString: %v", err)
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	// Retry db.Ping with a short backoff. testcontainers' built-in
	// log-based readiness check ("database system is ready to accept
	// connections") can fire BEFORE the TCP listener is actually
	// bound on some Docker configs — the first Ping() then hits
	// "connection reset by peer". Subsequent pings (typically within
	// 1–3 attempts) succeed once the listener is up.
	pingDeadline := time.Now().Add(15 * time.Second)
	for attempt := 1; ; attempt++ {
		pingErr := db.Ping()
		if pingErr == nil {
			break
		}
		if time.Now().After(pingDeadline) {
			t.Fatalf("db.Ping: %v (after %d attempts over 15s)", pingErr, attempt)
		}
		time.Sleep(200 * time.Millisecond)
	}

	cleanup := func() {
		_ = db.Close()
		_ = pgC.Terminate(ctx)
	}
	return db, cleanup
}

// readMigrationBodies reads each migration's SQL body via the
// same `embed.FS` package the runner uses. Internal-package access
// (`package database`, not `database_test`) is what makes this work.
func readMigrationBodies(t *testing.T) (map[string]string, error) {
	t.Helper()
	out := map[string]string{}
	for _, name := range migrationsToTest {
		body, err := migrationFiles.ReadFile("migrations/" + name)
		if err != nil {
			return nil, fmt.Errorf("read embedded %s: %w", name, err)
		}
		out[name] = string(body)
	}
	return out, nil
}

// schemaFingerprint returns a SHA-256 over a stable JSON
// representation of the schema state: post_status enum labels +
// per-table column lists + per-table index names. Used by the
// order-independence test to detect drift.
func schemaFingerprint(t *testing.T, db *sql.DB) string {
	t.Helper()
	state := map[string]any{}

	// enums
	enumRows, err := db.Query(`
		SELECT t.typname, e.enumlabel
		  FROM pg_enum e
		  JOIN pg_type t ON t.oid = e.enumtypid
		 WHERE t.typnamespace = (SELECT oid FROM pg_namespace WHERE nspname = 'public')
		 ORDER BY t.typname, e.enumsortorder
	`)
	if err != nil {
		t.Fatalf("query enums: %v", err)
	}
	enums := map[string][]string{}
	for enumRows.Next() {
		var typname, label string
		if err := enumRows.Scan(&typname, &label); err != nil {
			enumRows.Close()
			t.Fatalf("scan enum: %v", err)
		}
		enums[typname] = append(enums[typname], label)
	}
	enumRows.Close()
	state["enums"] = enums

	// column lists per table
	colRows, err := db.Query(`
		SELECT table_name, column_name, data_type
		  FROM information_schema.columns
		 WHERE table_schema = 'public'
		 ORDER BY table_name, ordinal_position
	`)
	if err != nil {
		t.Fatalf("query columns: %v", err)
	}
	cols := map[string][]map[string]string{}
	for colRows.Next() {
		var tn, cn, dt string
		if err := colRows.Scan(&tn, &cn, &dt); err != nil {
			colRows.Close()
			t.Fatalf("scan cols: %v", err)
		}
		cols[tn] = append(cols[tn], map[string]string{"name": cn, "type": dt})
	}
	colRows.Close()
	state["columns"] = cols

	// index names per table
	idxRows, err := db.Query(`
		SELECT tablename, indexname
		  FROM pg_indexes
		 WHERE schemaname = 'public'
		 ORDER BY tablename, indexname
	`)
	if err != nil {
		t.Fatalf("query indexes: %v", err)
	}
	indexes := map[string][]string{}
	for idxRows.Next() {
		var tn, idx string
		if err := idxRows.Scan(&tn, &idx); err != nil {
			idxRows.Close()
			t.Fatalf("scan idx: %v", err)
		}
		indexes[tn] = append(indexes[tn], idx)
	}
	idxRows.Close()
	state["indexes"] = indexes

	b, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// first16 returns the first 16 hex chars of a SHA-256 (used for logs).
func first16(hexHash string) string {
	if len(hexHash) < 16 {
		return hexHash
	}
	return hexHash[:16]
}
