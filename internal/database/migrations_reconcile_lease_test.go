//go:build integration

package database

import (
	"database/sql"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/testutil/postgres"
)

func TestMigration108_ReconcileLeaseSchemaAndResetTrigger(t *testing.T) {
	db, cleanup := postgres.StartTestPostgres(t)
	defer cleanup()

	if err := RunMigrationsUpTo(db, 107); err != nil {
		t.Fatalf("RunMigrationsUpTo(107): %v", err)
	}
	if err := applyMigrationByName(t, db, "108_reconcile_target_lease.sql"); err != nil {
		t.Fatalf("apply migration 108: %v", err)
	}

	for _, column := range []string{"reconcile_owner_id", "reconcile_until", "reconcile_heartbeat_at"} {
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

	var indexDef sql.NullString
	if err := db.QueryRow(`
		SELECT indexdef FROM pg_indexes
		WHERE schemaname = 'public'
		  AND indexname = 'idx_post_targets_reconcile_lease'`).Scan(&indexDef); err != nil {
		t.Fatalf("find lease index: %v", err)
	}
	if !indexDef.Valid || !strings.Contains(indexDef.String, "reconcile_until") ||
		!strings.Contains(indexDef.String, "status = 'publishing'") {
		t.Fatalf("unexpected lease index definition: %s", indexDef.String)
	}

	var triggerCount int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM pg_trigger
		WHERE tgrelid = 'post_targets'::regclass
		  AND NOT tgisinternal
		  AND tgname = 'post_targets_reset_reconcile_schedule'`).Scan(&triggerCount); err != nil {
		t.Fatalf("count reconcile reset triggers: %v", err)
	}
	if triggerCount != 1 {
		t.Fatalf("reconcile reset trigger count = %d, want exactly 1", triggerCount)
	}

	userID, workspaceID, accountID, postID, targetID := seedReconcileLeaseFixture(t, db)
	_ = userID
	_ = workspaceID

	future := time.Now().Add(10 * time.Minute)
	if _, err := db.Exec(`UPDATE post_targets
		SET reconcile_owner_id = 'stale-worker', reconcile_until = $1,
		    reconcile_heartbeat_at = $1, reconcile_attempt = 7,
		    next_reconcile_at = $1
		WHERE id = $2`, future, targetID); err != nil {
		t.Fatalf("seed stale lease: %v", err)
	}
	if _, err := db.Exec(`UPDATE post_targets SET status = 'publishing' WHERE id = $1`, targetID); err != nil {
		t.Fatalf("transition to publishing: %v", err)
	}

	var owner sql.NullString
	var until, heartbeat sql.NullTime
	var next time.Time
	var attempt int
	if err := db.QueryRow(`SELECT reconcile_owner_id, reconcile_until,
		reconcile_heartbeat_at, reconcile_attempt, next_reconcile_at
		FROM post_targets WHERE id = $1`, targetID).Scan(&owner, &until, &heartbeat, &attempt, &next); err != nil {
		t.Fatalf("read reset schedule: %v", err)
	}
	if owner.Valid || until.Valid || heartbeat.Valid || attempt != 0 {
		t.Fatalf("publishing reset = owner:%v until:%v heartbeat:%v attempt:%d", owner, until, heartbeat, attempt)
	}
	if next.Before(time.Now().Add(-2*time.Second)) || next.After(time.Now().Add(2*time.Second)) {
		t.Fatalf("next_reconcile_at after reset = %v, want approximately now", next)
	}
	_ = accountID
	_ = postID
}

// TestMigration108_ReconcileLeaseThreeReplicasAndExpiry verifies the
// production concurrency contract against real PostgreSQL: three independent
// repository instances race the same due target, exactly one owner wins, and
// a different replica can recover the target after the durable lease expires.
func TestMigration108_ReconcileLeaseThreeReplicasAndExpiry(t *testing.T) {
	db, cleanup := postgres.StartTestPostgres(t)
	defer cleanup()

	if err := RunMigrationsUpTo(db, 107); err != nil {
		t.Fatalf("RunMigrationsUpTo(107): %v", err)
	}
	if err := applyMigrationByName(t, db, "108_reconcile_target_lease.sql"); err != nil {
		t.Fatalf("apply migration 108: %v", err)
	}

	_, _, _, _, targetID := seedReconcileLeaseFixture(t, db)
	if _, err := db.Exec(`UPDATE post_targets SET status = 'publishing' WHERE id = $1`, targetID); err != nil {
		t.Fatalf("transition target to publishing: %v", err)
	}

	replicas := []struct {
		name string
		repo *repository.PostRepository
	}{
		{name: "reconcile-replica-a", repo: repository.NewPostRepository(db)},
		{name: "reconcile-replica-b", repo: repository.NewPostRepository(db)},
		{name: "reconcile-replica-c", repo: repository.NewPostRepository(db)},
	}
	shortLease := time.Minute

	// All three independent worker replicas race the same atomic claim at
	// once. PostgreSQL's FOR UPDATE SKIP LOCKED plus the lease predicate must
	// produce exactly one winner without duplicate ownership.
	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make(chan struct {
		name    string
		claimed bool
		err     error
	}, len(replicas))
	for _, replica := range replicas {
		replica := replica
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			claimed, err := replica.repo.ClaimPublishingTarget(targetID, replica.name, shortLease)
			results <- struct {
				name    string
				claimed bool
				err     error
			}{replica.name, claimed, err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	var winner string
	winners := 0
	for result := range results {
		if result.err != nil {
			t.Fatalf("%s concurrent claim: %v", result.name, result.err)
		}
		if result.claimed {
			winner = result.name
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("concurrent three-replica winners = %d, want exactly 1", winners)
	}

	// Simulate a crashed owner without sleeping: the durable lease fields are
	// inconsistent, so another replica must still be able to recover it.
	if _, err := db.Exec(`UPDATE post_targets
		SET reconcile_until = NULL,
		    reconcile_heartbeat_at = NULL
		WHERE id = $1`, targetID); err != nil {
		t.Fatalf("expire initial lease owned by %s: %v", winner, err)
	}

	var recovered string
	for _, replica := range replicas {
		if replica.name == winner {
			continue
		}
		claimed, err := replica.repo.ClaimPublishingTarget(targetID, replica.name, shortLease)
		if err != nil {
			t.Fatalf("%s recovery claim: %v", replica.name, err)
		}
		if claimed {
			recovered = replica.name
			break
		}
	}
	if recovered == "" {
		t.Fatal("a different replica should reclaim the target after lease expiry")
	}

	for _, replica := range replicas {
		if replica.name == recovered {
			continue
		}
		claimed, err := replica.repo.ClaimPublishingTarget(targetID, replica.name, shortLease)
		if err != nil {
			t.Fatalf("%s claim after recovery: %v", replica.name, err)
		}
		if claimed {
			t.Fatalf("%s claimed while recovered owner %s held the lease", replica.name, recovered)
		}
	}
}

func seedReconcileLeaseFixture(t *testing.T, db *sql.DB) (userID, workspaceID, accountID, postID, targetID int64) {
	t.Helper()
	if err := db.QueryRow(`INSERT INTO users (email, name)
		VALUES ('migration108@example.test', 'Migration 108') RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if err := db.QueryRow(`INSERT INTO workspaces (name, owner_id)
		VALUES ('migration108', $1) RETURNING id`, userID).Scan(&workspaceID); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	if err := db.QueryRow(`INSERT INTO platform_accounts
		(user_id, workspace_id, platform, platform_user_id, username, status)
		VALUES ($1, $2, 'tiktok', 'migration108-channel', 'migration108', 'active')
		RETURNING id`, userID, workspaceID).Scan(&accountID); err != nil {
		t.Fatalf("insert account: %v", err)
	}
	if err := db.QueryRow(`INSERT INTO posts (workspace_id, title, caption, status)
		VALUES ($1, 'migration108', 'lease fixture', 'draft') RETURNING id`, workspaceID).Scan(&postID); err != nil {
		t.Fatalf("insert post: %v", err)
	}
	if err := db.QueryRow(`INSERT INTO post_targets
		(post_id, platform_account_id, status, platform_post_id)
		VALUES ($1, $2, 'queued', 'lease-provider-id') RETURNING id`, postID, accountID).Scan(&targetID); err != nil {
		t.Fatalf("insert target: %v", err)
	}
	return
}
