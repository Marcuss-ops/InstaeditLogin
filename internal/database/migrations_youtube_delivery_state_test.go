//go:build integration

package database

import (
	"database/sql"
	"fmt"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/testutil/postgres"
)

// TestYouTubeDeliveryState_FreshDatabase verifies migration 125 promotes
// youtube_target_publications into an atomic delivery queue on a fresh
// database: the new operational columns land with the right nullability +
// defaults, the three claim/schedule/stuck indexes exist, and the migration
// 066 UNIQUE(post_target_id) constraint (1 target = 1 delivery) is intact.
func TestYouTubeDeliveryState_FreshDatabase(t *testing.T) {
	db, cleanup := postgres.StartTestPostgres(t)
	defer cleanup()

	if err := RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	assertDeliveryColumns(t, db)
	assertDeliveryIndexes(t, db)
	assertDeliveryUsage(t, db)
}

// assertDeliveryColumns pins the post-125 column contract. Every column the
// delivery worker / materializer will reach for must exist with the exact
// nullability so a future migration drift fails loudly here.
func assertDeliveryColumns(t *testing.T, db *sql.DB) {
	t.Helper()
	expected := map[string]struct{ nullable bool; def string }{
		"state":               {nullable: false, def: "'preflight'::text"},
		"priority":            {nullable: false, def: "100"},
		"prepare_at":          {nullable: true},
		"next_attempt_at":     {nullable: true},
		"max_attempts":        {nullable: false, def: "8"},
		"lease_owner":         {nullable: true},
		"lease_expires_at":    {nullable: true},
		"heartbeat_at":        {nullable: true},
		"resume_state":        {nullable: true},
		"last_error_code":     {nullable: true},
		"last_transition_at":  {nullable: false},
		"verified_at":         {nullable: true},
		"original_publish_at": {nullable: true},
		"spillover_count":     {nullable: false, def: "0"},
	}

	rows, err := db.Query(`
		SELECT column_name, is_nullable, column_default
		  FROM information_schema.columns
		 WHERE table_schema = 'public'
		   AND table_name = 'youtube_target_publications'
	`)
	if err != nil {
		t.Fatalf("query columns: %v", err)
	}
	defer rows.Close()

	got := map[string]struct{ nullable bool; def string }{}
	for rows.Next() {
		var name, nullable string
		var def sql.NullString
		if err := rows.Scan(&name, &nullable, &def); err != nil {
			t.Fatalf("scan columns: %v", err)
		}
		got[name] = struct{ nullable bool; def string }{nullable: nullable == "YES", def: def.String}
	}

	for col, want := range expected {
		g, ok := got[col]
		if !ok {
			t.Errorf("youtube_target_publications.%s missing", col)
			continue
		}
		if g.nullable != want.nullable {
			t.Errorf("youtube_target_publications.%s nullable: got %v want %v", col, g.nullable, want.nullable)
		}
		if want.def != "" && g.def != want.def {
			t.Errorf("youtube_target_publications.%s default: got %q want %q", col, g.def, want.def)
		}
	}
}

// assertDeliveryIndexes pins the post-125 index contract (the three indexes
// the worker / planner rely on) plus the migration 066 unique constraint.
func assertDeliveryIndexes(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, idx := range []string{
		"idx_yt_delivery_claim",
		"idx_yt_delivery_channel_schedule",
		"idx_yt_delivery_state_updated",
	} {
		var exists bool
		if err := db.QueryRow(`
			SELECT EXISTS (SELECT 1 FROM pg_indexes WHERE schemaname = 'public' AND indexname = $1)
		`, idx).Scan(&exists); err != nil {
			t.Fatalf("query index %s: %v", idx, err)
		}
		if !exists {
			t.Errorf("index %s missing", idx)
		}
	}

	// 1 PostTarget = 1 delivery invariant (migration 066) must still hold.
	var unique bool
	if err := db.QueryRow(`
		SELECT EXISTS (
			SELECT 1
			  FROM information_schema.table_constraints
			 WHERE table_schema = 'public'
			   AND table_name = 'youtube_target_publications'
			   AND constraint_type = 'UNIQUE'
			   AND constraint_name = 'youtube_target_publications_post_target_id_key'
		)
	`).Scan(&unique); err != nil {
		t.Fatalf("query unique constraint: %v", err)
	}
	if !unique {
		t.Error("youtube_target_publications_post_target_id_key UNIQUE constraint missing (1 target = 1 delivery invariant)")
	}
}

// assertDeliveryUsage exercises the claimable-row semantics: a fresh row
// lands at state='preflight' with priority/max_attempts/spillover_count
// defaults and NULL lease fields, so the claim CTE can pick it up. It seeds
// the full FK chain (users → workspaces → platform_accounts → upload_jobs →
// posts → post_targets) because youtube_target_publications is fanned out
// from that lineage.
func assertDeliveryUsage(t *testing.T, db *sql.DB) {
	t.Helper()
	const (
		userID      int64 = 90001
		workspaceID int64 = 90001
		accountID   int64 = 90002
	)

	if _, err := db.Exec(`INSERT INTO users (id, email, name) VALUES ($1, $2, $3)`, userID, "delivery-state@example.test", "Delivery State"); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO workspaces (id, name, owner_id) VALUES ($1, $2, $3)`, workspaceID, "Delivery State Workspace", userID); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO platform_accounts (id, user_id, workspace_id, platform, platform_user_id, username) VALUES ($1, $2, $3, 'youtube', 'delivery-state-channel', 'delivery-state')`, accountID, userID, workspaceID); err != nil {
		t.Fatalf("insert platform_account: %v", err)
	}

	var uploadJobID int64
	if err := db.QueryRow(`
		INSERT INTO upload_jobs (user_id, workspace_id, source_type, source_id, title, caption, metadata, targets, status, ingest_after, publish_at)
		VALUES ($1, $2, 'authenticated_drive', 'delivery-state', 'Delivery State', '', '{}', $3, 'ingest_completed', NOW(), NOW())
		RETURNING id`, userID, workspaceID, fmt.Sprintf(`[%d]`, accountID)).Scan(&uploadJobID); err != nil {
		t.Fatalf("insert upload_job: %v", err)
	}
	var postID int64
	if err := db.QueryRow(`
		INSERT INTO posts (workspace_id, title, caption, status, publish_at, upload_job_id)
		VALUES ($1, 'Delivery State', '', 'queued', NOW(), $2)
		RETURNING id`, workspaceID, uploadJobID).Scan(&postID); err != nil {
		t.Fatalf("insert post: %v", err)
	}
	var postTargetID int64
	if err := db.QueryRow(`
		INSERT INTO post_targets (post_id, platform_account_id, status)
		VALUES ($1, $2, 'queued')
		RETURNING id`, postID, accountID).Scan(&postTargetID); err != nil {
		t.Fatalf("insert post_target: %v", err)
	}

	var pubID int64
	if err := db.QueryRow(`
		INSERT INTO youtube_target_publications (upload_job_id, post_target_id, platform_account_id)
		VALUES ($1, $2, $3)
		RETURNING id`, uploadJobID, postTargetID, accountID).Scan(&pubID); err != nil {
		t.Fatalf("insert delivery: %v", err)
	}

	var state string
	var priority, maxAttempts, spillover int
	var leaseOwner sql.NullString
	if err := db.QueryRow(`
		SELECT state, priority, max_attempts, spillover_count, lease_owner
		  FROM youtube_target_publications
		 WHERE id = $1
	`, pubID).Scan(&state, &priority, &maxAttempts, &spillover, &leaseOwner); err != nil {
		t.Fatalf("read delivery defaults: %v", err)
	}
	if state != "preflight" || priority != 100 || maxAttempts != 8 || spillover != 0 {
		t.Errorf("delivery defaults: got (state=%q priority=%d max_attempts=%d spillover=%d), want (preflight 100 8 0)",
			state, priority, maxAttempts, spillover)
	}
	if leaseOwner.Valid {
		t.Errorf("fresh delivery lease_owner must be NULL, got %q", leaseOwner.String)
	}
}
