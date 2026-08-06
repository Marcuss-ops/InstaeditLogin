//go:build integration

package database

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/testutil/postgres"
)

func TestMigration109_ReconcileDueUnleasedIndex(t *testing.T) {
	db, cleanup := postgres.StartTestPostgres(t)
	defer cleanup()

	if err := RunMigrationsUpTo(db, 108); err != nil {
		t.Fatalf("RunMigrationsUpTo(108): %v", err)
	}
	if err := applyMigrationByName(t, db, "109_reconcile_polling_lease_index.sql"); err != nil {
		t.Fatalf("apply migration 109: %v", err)
	}

	var indexDef sql.NullString
	if err := db.QueryRow(`
		SELECT indexdef FROM pg_indexes
		WHERE schemaname = 'public'
		  AND indexname = 'idx_post_targets_reconcile_due_unleased'`).Scan(&indexDef); err != nil {
		t.Fatalf("find due unleased index: %v", err)
	}
	if !indexDef.Valid || indexDef.String == "" {
		t.Fatal("due unleased index definition is empty")
	}
	if strings.Contains(indexDef.String, "NOW()") || strings.Contains(indexDef.String, "now()") {
		t.Fatalf("partial index must not contain dynamic time predicate: %s", indexDef.String)
	}
	for _, predicate := range []string{"status = 'publishing'", "platform_post_id IS NOT NULL", "reconcile_owner_id IS NULL"} {
		if !strings.Contains(indexDef.String, predicate) {
			t.Fatalf("due unleased index lacks %q: %s", predicate, indexDef.String)
		}
	}
}

func TestMigration107_ReconcilePollingColumnsAndIndex(t *testing.T) {
	db, cleanup := postgres.StartTestPostgres(t)
	defer cleanup()

	if err := RunMigrationsUpTo(db, 106); err != nil {
		t.Fatalf("RunMigrationsUpTo(106): %v", err)
	}
	if err := applyMigrationByName(t, db, "107_reconcile_polling_schedule.sql"); err != nil {
		t.Fatalf("apply migration 107: %v", err)
	}

	for _, column := range []string{"next_reconcile_at", "reconcile_attempt"} {
		var exists bool
		if err := db.QueryRow(`
			SELECT EXISTS (
				SELECT 1 FROM information_schema.columns
				WHERE table_schema = 'public'
				  AND table_name = 'post_targets'
				  AND column_name = $1
			)`, column).Scan(&exists); err != nil {
			t.Fatalf("check column %s: %v", column, err)
		}
		if !exists {
			t.Errorf("missing post_targets.%s", column)
		}
	}

	var predicate sql.NullString
	if err := db.QueryRow(`
		SELECT indexdef
		FROM pg_indexes
		WHERE schemaname = 'public'
		  AND indexname = 'idx_post_targets_reconcile_ready'`).Scan(&predicate); err != nil {
		t.Fatalf("find reconcile index: %v", err)
	}
	if !predicate.Valid || predicate.String == "" {
		t.Fatal("reconcile index definition is empty")
	}
	if strings.Contains(predicate.String, "next_reconcile_at <=") {
		t.Fatalf("partial index must not contain dynamic due-time predicate: %s", predicate.String)
	}
	if !strings.Contains(predicate.String, "status = 'publishing'") || !strings.Contains(predicate.String, "platform_post_id IS NOT NULL") {
		t.Fatalf("reconcile index lacks static readiness predicates: %s", predicate.String)
	}

	var userID, workspaceID, accountID, postID, targetID int64
	if err := db.QueryRow(`
		INSERT INTO users (email, name)
		VALUES ('migration107@example.test', 'Migration 107')
		RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if err := db.QueryRow(`
		INSERT INTO workspaces (name, owner_id)
		VALUES ('migration107', $1)
		RETURNING id`, userID).Scan(&workspaceID); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	if err := db.QueryRow(`
		INSERT INTO platform_accounts
		    (user_id, workspace_id, platform, platform_user_id, username, status)
		VALUES ($1, $2, 'tiktok', 'migration107-channel', 'migration107', 'active')
		RETURNING id`, userID, workspaceID).Scan(&accountID); err != nil {
		t.Fatalf("insert platform account: %v", err)
	}
	if err := db.QueryRow(`
		INSERT INTO posts (workspace_id, title, caption, status)
		VALUES ($1, 'migration107', 'reconcile trigger', 'draft')
		RETURNING id`, workspaceID).Scan(&postID); err != nil {
		t.Fatalf("insert post: %v", err)
	}
	if err := db.QueryRow(`
		INSERT INTO post_targets (post_id, platform_account_id, status, platform_post_id)
		VALUES ($1, $2, 'queued', 'provider-id')
		RETURNING id`, postID, accountID).Scan(&targetID); err != nil {
		t.Fatalf("insert post target: %v", err)
	}

	future := time.Now().Add(10 * time.Minute)
	if _, err := db.Exec(`
		UPDATE post_targets
		SET reconcile_attempt = 7, next_reconcile_at = $1
		WHERE id = $2`, future, targetID); err != nil {
		t.Fatalf("seed future reconcile schedule: %v", err)
	}
	if _, err := db.Exec(`
		UPDATE post_targets
		SET status = 'publishing'
		WHERE id = $1`, targetID); err != nil {
		t.Fatalf("transition target to publishing: %v", err)
	}

	var attempt int
	var next time.Time
	if err := db.QueryRow(`
		SELECT reconcile_attempt, next_reconcile_at
		FROM post_targets
		WHERE id = $1`, targetID).Scan(&attempt, &next); err != nil {
		t.Fatalf("read reset reconcile schedule: %v", err)
	}
	if attempt != 0 {
		t.Fatalf("reconcile_attempt after entering publishing = %d, want 0", attempt)
	}
	now := time.Now()
	if next.Before(now.Add(-2*time.Second)) || next.After(now.Add(2*time.Second)) {
		t.Fatalf("next_reconcile_at after entering publishing = %v, want approximately now (%v)", next, now)
	}
}
