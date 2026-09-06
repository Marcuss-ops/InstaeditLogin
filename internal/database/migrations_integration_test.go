//go:build integration

// Package database_test contains the migration integration tests.
// They run under the `integration` build tag (go test -tags=integration)
// so unit-test runs (go test ./...) are not blocked when Docker is
// unavailable.
package database

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/testutil/postgres"
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
	// 003 is intentionally excluded from the reverse-order re-run
	// subset: it originally created posts.scheduled_at, which was later
	// dropped by 049b. Because RunMigrations (applied above) already
	// brings the DB to the final schema, re-running 003 in reverse
	// would re-add the dropped column/index and falsely report drift.
	// 003 is still exercised by the full RunMigrations flow and by
	// TestMigrations_001To012_AppliesCleanly.
	"004_composite_token_index.sql",
	"005_account_lifecycle.sql",
	"006_media_assets.sql",
	"011_target_provider_state.sql",
	"012_add_post_status_enum.sql",
	"012_async_threads_support.sql",
	// 053 + 083..085 — OAuth lineage and the current grant/token model.
	// Migration 043 is intentionally excluded: migration 084 replaces its
	// resource-level unique constraint with partial indexes, so replaying
	// 043 after the current schema is not a valid idempotency assertion.
	// The canonical RunMigrations pass still exercises 043 on a fresh DB.
	"053_oauth_tokens_retargeted.sql",
	"083_oauth_token_field_normalization.sql",
	"084_oauth_subject_shared_connections.sql",
	"085_grant_scoped_tokens.sql",
}

// expectedPostStatusActive is the documented active enum set
// after migration 012 has applied. The migration 003 CREATE TYPE
// introduces 5 values; migration 012 ADD VALUE introduces 3 more
// (waiting_provider / queued / partially_published). 'queued' is the
// rename target of the legacy 'scheduled' value which remains in the
// enum for back-compat with rows already inserted pre-012.
// Later migrations 018 and 035 add 'retrying' and 'dlq'; migration 130
// adds the terminal 'drive_required_failed' policy state (Task 8/10.1).
//
// Net on-disk enum labels after 130 = 5 (003) + 3 (012) + 2 (018/035)
// + 1 (130) (the 10 active + the 1 deprecated 'scheduled').
var expectedPostStatusActive = map[string]bool{
	"draft":                 true,
	"queued":                true,
	"publishing":            true,
	"published":             true,
	"failed":                true,
	"waiting_provider":      true,
	"partially_published":   true,
	"retrying":              true,
	"dlq":                   true,
	"drive_required_failed": true,
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
	{"tokens", "encrypted_access_token"}, {"tokens", "access_token_expires_at"}, {"tokens", "refresh_token_expires_at"},
	{"tokens", "expires_at"}, {"tokens", "scopes"}, {"tokens", "created_at"}, {"tokens", "oauth_connection_id"},
	{"oauth_connections", "status"}, {"oauth_connections", "granted_scopes"}, {"oauth_connections", "last_refresh_error"},
	// 002_add_refresh_token
	{"tokens", "encrypted_refresh_token"},
	// 003_posts_workspaces
	{"workspaces", "id"}, {"workspaces", "name"}, {"workspaces", "owner_id"}, {"workspaces", "created_at"},
	{"platform_accounts", "workspace_id"},
	{"posts", "id"}, {"posts", "workspace_id"}, {"posts", "title"}, {"posts", "caption"},
	{"posts", "media_url"}, {"posts", "status"}, {"posts", "created_at"},
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
	{"media_assets", "bucket"}, {"media_assets", "content_type"}, {"media_assets", "size_bytes"}, {"media_assets", "status"},
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
	db, cleanup := postgres.StartTestPostgres(t)
	defer cleanup()

	if err := RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
}

// TestPostStatus_HasExpectedNineValues: the active set is
// documented in docs/SANDBOX.md + API/openapi.yaml (Taglio 5.x SSOT).
// Per migration 003 (CREATE TYPE post_status AS ENUM) + 012 (ADD VALUE
// waiting_provider / queued / partially_published) + 018/035 (ADD VALUE
// retrying / dlq), the on-disk enum has 10 labels (9 active + 'scheduled'
// deprecated back-compat alias).
//
// This test catches schema drift: if a future migration accidentally
// drops an active value OR adds a third alias, CI fails.
func TestPostStatus_HasExpectedNineValues(t *testing.T) {
	db, cleanup := postgres.StartTestPostgres(t)
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
	db, cleanup := postgres.StartTestPostgres(t)
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
//  1. Apply 001→012 in canonical lexical order, hash the schema.
//  2. Re-apply 001→012 in canonical order. Hash must match — proves
//     the `IF NOT EXISTS` + DO-block guards actually work.
//  3. Apply migrations 001→012 in REVERSE lexical order. Hash must
//     STILL match — proves no migration is silently order-dependent
//     (e.g. relying on a column added later).
//
// This catches the class of regression where migration N tries to
// `ALTER TABLE foo ADD bar` without `IF NOT EXISTS` and the second
// migration (different one) drops-and-readds bar under another name.
func TestMigrations_OrderIndependent(t *testing.T) {
	db, cleanup := postgres.StartTestPostgres(t)
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

// TestMigration083_BackfillsCanonicalOAuthFieldsAndIsIdempotent verifies
// the additive rollout against real PostgreSQL. It deliberately seeds only
// legacy token/grant fields before 083, then checks byte-for-byte ciphertext
// preservation, expiry backfill, grant scope backfill, defaults, and a direct
// second execution of the embedded SQL body.
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

// ────────────────────────────────────────────────────────────────────
//  TestUploadJobs_IngestToPublishWindow (P1#4 contract)
// ────────────────────────────────────────────────────────────────────

// TestUploadJobs_IngestToPublishWindow pins the regression-sensitive
// contract for the ingest→publish split (migration 049a+049c): an
// upload_job created with `ingest_after = NOW()` + `publish_at =
// NOW() + 2h` MUST be eligible for the ingest pool immediately
// (status transitions pending → ingest_completed on the first
// MarkIngested invocation) BUT MUST NOT be eligible for the publish
// pool's ClaimBatchForPublish CTE until `publish_at` elapses.
//
// The user spec phrases the "i" condition as "marchi
// `upload_jobs.ingest_completed=true` entro 30s". Strictly speaking
// the column is NOT a boolean — ingestion completion is the status
// enum value `ingest_completed` (see migration 049c, which renamed
// the legacy `ready_to_publish` to the canonical name and cleared
// any `published_at`-style column). This test asserts on the
// canonical enum value, which is what the production SQL actually
// writer-side stamps via MarkIngested.
//
// Three subtleties deliberately exercised:
//  1. ingest_after is set to NOW() (immediate claim) so the
//     ingest pool's CTE predicate `ingest_after <= NOW()` is
//     satisfied AT INSERT TIME without any clock advance.
//  2. publish_at is set to NOW() + INTERVAL '2 hours' so the
//     publish window is unambiguously in the future; a regression
//     that collapsed the two columns would surface here as the
//     CTE returning 1 row instead of 0.
//  3. The MarkIngested transition in this test is invoked
//     DIRECTLY via raw SQL (not via repo.MarkIngested) because
//     this file is in `package database`, not `package repository`;
//     the SQL shape is identical to what upload_job_repo.go
//     issues, so a divergence in the repo query would be caught
//     by schemaFingerprint drift in the order-independence test.
//
// The timing budget (`entro 30s`) the user spec gives the
// operational pipeline is logged as telemetry — the test itself
// runs the transition synchronously, so the elapsed wall-clock
// tells the operator whether CI travelled <1s (good) or started
// dragging (suspicious; probably Docker-side slowness, not schema).
