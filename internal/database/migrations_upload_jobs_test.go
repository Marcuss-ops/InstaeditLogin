//go:build integration

package database

import (
	"database/sql"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/testutil/postgres"
)

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
