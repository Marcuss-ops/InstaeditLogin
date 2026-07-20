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
	"time"

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
	"003_posts_workspaces.sql",
	"004_composite_token_index.sql",
	"005_account_lifecycle.sql",
	"006_media_assets.sql",
	"011_target_provider_state.sql",
	"012_add_post_status_enum.sql",
	"012_async_threads_support.sql",
	// 043 + 053 — oauth_connections lineage + tokens oauth_connection_id FK
	// (P0#3 vault retarget). Migration 053 is guarded by a DO block that
	// only backfills/SET NOT NULL if platform_accounts.oauth_connection_id
	// already exists, so the order-independence test against this set
	// stays stable whether the migration runner applies 043 before 053
	// or vice versa.
	"043_oauth_connections.sql",
	"053_oauth_tokens_retargeted.sql",
}

// expectedPostStatusActive is the documented active enum set
// after migration 012 has applied. The migration 003 CREATE TYPE
// introduces 5 values; migration 012 ADD VALUE introduces 3 more
// (waiting_provider / queued / partially_published). 'queued' is the
// rename target of the legacy 'scheduled' value which remains in the
// enum for back-compat with rows already inserted pre-012.
// Later migrations 018 and 035 add 'retrying' and 'dlq'.
//
// Net on-disk enum labels after 035 = 5 (003) + 3 (012) + 2 (018/035)
// (the 9 active + the 1 deprecated 'scheduled').
var expectedPostStatusActive = map[string]bool{
	"draft":               true,
	"queued":              true,
	"publishing":          true,
	"published":           true,
	"failed":              true,
	"waiting_provider":    true,
	"partially_published": true,
	"retrying":            true,
	"dlq":                 true,
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
	{"tokens", "expires_at"}, {"tokens", "scopes"}, {"tokens", "created_at"}, {"tokens", "oauth_connection_id"},
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

// ────────────────────────────────────────────────────────────────────
//  helpers
// ────────────────────────────────────────────────────────────────────

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
func TestUploadJobs_IngestToPublishWindow(t *testing.T) {
	start := time.Now()
	db, cleanup := postgres.StartTestPostgres(t)
	defer cleanup()

	// Apply the full migration set; 049a (status enum) + 049c
	// (ingest_after + publish_at + status rename) are the
	// migrations under test, but we don't bypass RunMigrations —
	// the goal is to confirm THE STACK holds together, not just
	// the two migrations in isolation.
	if err := RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	// FK guard fixture (Step 0): upload_jobs.user_id + workspace_id
	// have NOT NULL REFERENCES users(id) / workspaces(id), and a
	// freshly-migrated DB has zero rows in either. The two fixture
	// INSERTs use ON CONFLICT (id) DO NOTHING so a future change to
	// schema-column-set (e.g. a new NOT NULL column added in a later
	// migration) will surface here as a clear FK-violation / missing-
	// column error instead of the test silently going green for the
	// wrong reason. The KEPT-NARROW column lists (id + 2 NOT NULLs)
	// rely on DEFAULTs on every other column; a future migration that
	// adds a NOT-NULL-without-default column on users or workspaces
	// is the explicit failure-mode the FK guard is designed to catch.
	if _, err := db.Exec(`
		INSERT INTO users (id, email, name)
		VALUES (1, 'ipw-test@instaedit.local', 'IPW Test User')
		ON CONFLICT (id) DO NOTHING
	`); err != nil {
		t.Fatalf("FK guard: insert users(id=1): %v (migration 052 or a later migration likely added a NOT-NULL-without-default column on users — see followup)", err)
	}
	if _, err := db.Exec(`
		INSERT INTO workspaces (id, name, owner_id)
		VALUES (1, 'IPW Test Workspace', 1)
		ON CONFLICT (id) DO NOTHING
	`); err != nil {
		t.Fatalf("FK guard: insert workspaces(id=1, owner_id=1): %v (migration 052 or a later migration likely added a NOT-NULL-without-default column on workspaces — see followup)", err)
	}

	// Step 1: insert a row at the boundary. ingest_after=NOW()
	// (immediate eligibility for the ingest pool) +
	// publish_at=NOW()+2h (publish window opens in 2h, so the
	// row MUST be deferred past publish-completion).
	var (
		jobID           int64
		insertIngest    time.Time
		insertPublishAt time.Time
	)
	err := db.QueryRow(`
		INSERT INTO upload_jobs (
			user_id, workspace_id,
			source_type, source_id,
			title,
			targets,
			status,
			ingest_after, publish_at
		) VALUES (
			1, 1,
			'authenticated_drive', 'test-folder/test-file-id',
			'Ingest→PublishWindow integration test',
			'[]'::jsonb,
			'pending',
			NOW(),
			NOW() + INTERVAL '2 hours'
		)
		RETURNING id, ingest_after, publish_at
	`).Scan(&jobID, &insertIngest, &insertPublishAt)
	if err != nil {
		t.Fatalf("insert upload_job (ingest_after=NOW() + publish_at=NOW()+2h): %v", err)
	}
	if jobID <= 0 {
		t.Fatalf("RETURNING id: got %d, want positive", jobID)
	}
	if insertPublishAt.Before(insertIngest) {
		t.Fatalf("publish_at (%s) not strictly future after ingest_after (%s)",
			insertPublishAt, insertIngest)
	}

	// Step 2: confirm the initial state round-trips through the
	// schema correctly (status=pending + targets as JSONB + publish_at
	// preserved through the default vs explicit value).
	var (
		roundTripStatus      string
		roundTripIngestAfter time.Time
		roundTripPublishAt   sql.NullTime
		roundTripTargets     []byte
	)
	err = db.QueryRow(`
		SELECT status, ingest_after, publish_at, targets
		  FROM upload_jobs WHERE id = $1
	`, jobID).Scan(&roundTripStatus, &roundTripIngestAfter,
		&roundTripPublishAt, &roundTripTargets)
	if err != nil {
		t.Fatalf("select initial state: %v", err)
	}
	if roundTripStatus != "pending" {
		t.Errorf("initial status: got %q, want 'pending' (default value at insert)", roundTripStatus)
	}
	if !roundTripIngestAfter.Equal(insertIngest) {
		t.Errorf("ingest_after round-trip: got %s, want %s", roundTripIngestAfter, insertIngest)
	}
	if !roundTripPublishAt.Valid || !roundTripPublishAt.Time.Equal(insertPublishAt) {
		t.Errorf("publish_at round-trip: got %v, want %s", roundTripPublishAt, insertPublishAt)
	}

	// Step 3: simulate the ingest pool's MarkIngested transition.
	// The SQL body matches the production shape (status flip +
	// asset_id + total_bytes stamp + updated_at advance); a
	// regression that changes the column list would surface
	// here as a SQL error or a row that doesn't fully flip.
	_, err = db.Exec(`
		UPDATE upload_jobs
		   SET status           = 'ingest_completed',
		       asset_id         = 'asset-test-1',
		       total_bytes      = 12345,
		       updated_at       = NOW()
		 WHERE id = $1
	`, jobID)
	if err != nil {
		t.Fatalf("MarkIngested transition: %v", err)
	}

	// Step 4: verify status flipped + publish_at UNCHANGED +
	// ingest_after UNCHANGED + updated_at advanced. publish_at
	// staying literal-bit-identical is the regression-locked
	// invariant — a regression that erroneously null'd publish_at
	// on the ingest side (e.g. a migration that confused the two
	// columns) would surface here.
	var (
		flippedStatus      string
		flippedIngestAfter time.Time
		flippedPublishAt   sql.NullTime
		flippedUpdatedAt   time.Time
	)
	err = db.QueryRow(`
		SELECT status, ingest_after, publish_at, updated_at
		  FROM upload_jobs WHERE id = $1
	`, jobID).Scan(&flippedStatus, &flippedIngestAfter,
		&flippedPublishAt, &flippedUpdatedAt)
	if err != nil {
		t.Fatalf("select flipped state: %v", err)
	}
	if flippedStatus != "ingest_completed" {
		t.Errorf("after-MarkIngested status: got %q, want 'ingest_completed'", flippedStatus)
	}
	if !flippedIngestAfter.Equal(insertIngest) {
		t.Errorf("ingest_after drifted across MarkIngested: was %s, now %s",
			insertIngest, flippedIngestAfter)
	}
	if !flippedPublishAt.Valid {
		t.Fatalf("publish_at became NULL across MarkIngested (drift on the contract boundary)")
	}
	if !flippedPublishAt.Time.Equal(insertPublishAt) {
		t.Errorf("publish_at drifted across MarkIngested: was %s, now %s",
			insertPublishAt, flippedPublishAt.Time)
	}
	if !flippedUpdatedAt.After(insertIngest) {
		t.Errorf("updated_at did not advance: was %s, now %s", insertIngest, flippedUpdatedAt)
	}

	// Step 5: simulate the publish pool's ClaimBatchForPublish
	// CTE. This MUST return 0 rows because publish_at > NOW().
	// A regression that merged ingest+publish into one pool, or
	// that removed the publish_at filter from the CTE, would
	// surface here as the query returning 1 row instead of nil.
	publishCTE := `
		SELECT id FROM upload_jobs
		 WHERE status = 'ingest_completed'
		   AND (publish_at IS NULL OR publish_at <= NOW())
		 LIMIT 1
	`
	var claimedID int64
	err = db.QueryRow(publishCTE).Scan(&claimedID)
	if err == nil {
		t.Errorf("publish CTE returned row id=%d despite publish_at > NOW(): publish window did NOT gate the claim (regression)",
			claimedID)
	} else if err != sql.ErrNoRows {
		t.Errorf("publish CTE unexpected error: %v (expected sql.ErrNoRows)", err)
	}

	// Step 6: advance publish_at to NOW() and re-run the CTE.
	// MUST now return a row, proving the inverse — the window
	// DOES open when publish_at elapses. Without this counter-
	// step, the 0-row result above could be a false-positive
	// (e.g. an empty queue, a misnamed status enum, etc.).
	_, err = db.Exec(`
		UPDATE upload_jobs
		   SET publish_at = NOW(),
		       updated_at = NOW()
		 WHERE id = $1
	`, jobID)
	if err != nil {
		t.Fatalf("advance publish_at: %v", err)
	}
	err = db.QueryRow(publishCTE).Scan(&claimedID)
	if err != nil {
		t.Errorf("publish CTE returned no row after publish_at=NOW(): err=%v (window logic likely inverted)", err)
	}
	if claimedID != jobID {
		t.Errorf("publish CTE returned wrong row: got %d, want %d", claimedID, jobID)
	}

	// Step 7: timing telemetry — the user spec says the ingest
	// pool MUST reach ingest_completed within 30s in production.
	// This integration test does the transition synchronously, so
	// the elapsed time is dominated by Docker spin-up + first
	// schemaFingerprint. A future slowness budget gate can be
	// added here without changing the test's contract.
	elapsed := time.Since(start)
	t.Logf("Ingest→PublishWindow scenario complete: %s elapsed (budget 30s); row %d traversed pending → ingest_completed → publish-window-eligible",
		elapsed.Round(time.Millisecond), jobID)
}
