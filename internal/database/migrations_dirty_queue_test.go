//go:build integration

package database

import (
	"database/sql"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/testutil/postgres"
)

func TestMigration105_DirtyAggregateQueueEnqueuesAndDeduplicates(t *testing.T) {
	db, cleanup := postgres.StartTestPostgres(t)
	defer cleanup()

	if err := RunMigrationsUpTo(db, 104); err != nil {
		t.Fatalf("RunMigrationsUpTo(104): %v", err)
	}

	var userID, workspaceID, accountID, postID int64
	if err := db.QueryRow(`
		INSERT INTO users (email, name)
		VALUES ('migration105@example.test', 'Migration 105')
		RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if err := db.QueryRow(`
		INSERT INTO workspaces (name, owner_id)
		VALUES ('migration105', $1)
		RETURNING id`, userID).Scan(&workspaceID); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	if err := db.QueryRow(`
		INSERT INTO platform_accounts
		    (user_id, workspace_id, platform, platform_user_id, username, status)
		VALUES ($1, $2, 'tiktok', 'migration105-channel', 'migration105', 'active')
		RETURNING id`, userID, workspaceID).Scan(&accountID); err != nil {
		t.Fatalf("insert platform account: %v", err)
	}
	if err := db.QueryRow(`
		INSERT INTO posts (workspace_id, title, caption, status)
		VALUES ($1, 'migration105', 'queue trigger', 'draft')
		RETURNING id`, workspaceID).Scan(&postID); err != nil {
		t.Fatalf("insert post: %v", err)
	}

	// Apply migration 105 after the parent exists: the rollout backfill must
	// enqueue that pre-existing post before any new target transition occurs.
	if err := applyMigrationByName(t, db, "105_post_aggregate_repair_queue.sql"); err != nil {
		t.Fatalf("apply migration 105: %v", err)
	}
	assertDirtyQueueCount(t, db, postID, 1)

	var targetID int64
	if err := db.QueryRow(`
		INSERT INTO post_targets (post_id, platform_account_id, status)
		VALUES ($1, $2, 'queued')
		RETURNING id`, postID, accountID).Scan(&targetID); err != nil {
		t.Fatalf("insert target: %v", err)
	}
	assertDirtyQueueCount(t, db, postID, 1)
	if _, err := db.Exec(`DELETE FROM post_aggregate_repair_queue WHERE post_id = $1`, postID); err != nil {
		t.Fatalf("clear backfill queue row: %v", err)
	}
	assertDirtyQueueCount(t, db, postID, 0)

	if _, err := db.Exec(`UPDATE post_targets SET status = 'publishing' WHERE id = $1`, targetID); err != nil {
		t.Fatalf("update target status: %v", err)
	}
	// The queue is keyed by post_id, so repeated target transitions do not
	// create one queue row per transition.
	assertDirtyQueueCount(t, db, postID, 1)
}

func assertDirtyQueueCount(t *testing.T, db *sql.DB, postID, want int64) {
	t.Helper()
	var got int64
	if err := db.QueryRow(`
		SELECT COUNT(*)
		FROM post_aggregate_repair_queue
		WHERE post_id = $1`, postID).Scan(&got); err != nil {
		t.Fatalf("count dirty queue for post %d: %v", postID, err)
	}
	if got != want {
		t.Fatalf("dirty queue count for post %d = %d, want %d", postID, got, want)
	}
}
