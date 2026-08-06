//go:build integration

package repository

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/database"
	"github.com/Marcuss-ops/InstaeditLogin/internal/testutil/postgres"
)

func TestClaimPublishingTargetWithLease_ThreeWorkersOneOwnerAndTakeover(t *testing.T) {
	db, cleanup := postgres.StartTestPostgres(t, postgres.WithDatabase("instaedit_reconcile_lease_test"))
	defer cleanup()
	if err := database.RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	var userID, workspaceID, accountID, postID, targetID int64
	if err := db.QueryRow(`INSERT INTO users (email, name)
		VALUES ('reconcile-lease@example.test', 'Reconcile Lease') RETURNING id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`INSERT INTO workspaces (name, owner_id)
		VALUES ('reconcile-lease', $1) RETURNING id`, userID).Scan(&workspaceID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`INSERT INTO platform_accounts
		(user_id, workspace_id, platform, platform_user_id, username, status)
		VALUES ($1, $2, 'tiktok', 'reconcile-lease-channel', 'reconcile-lease', 'active')
		RETURNING id`, userID, workspaceID).Scan(&accountID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`INSERT INTO posts (workspace_id, title, caption, status)
		VALUES ($1, 'reconcile lease', 'three workers', 'publishing') RETURNING id`, workspaceID).Scan(&postID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`INSERT INTO post_targets
		(post_id, platform_account_id, status, platform_post_id, next_reconcile_at)
		VALUES ($1, $2, 'publishing', 'provider-lease-id', NOW()) RETURNING id`, postID, accountID).Scan(&targetID); err != nil {
		t.Fatal(err)
	}

	const workers = 3
	results := make(chan bool, workers)
	errs := make(chan error, workers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			<-start
			claimed, err := NewPostRepository(db).ClaimPublishingTarget(targetID, fmt.Sprintf("reconcile-worker-%d", n), 60*time.Second)
			if err != nil {
				errs <- err
				return
			}
			results <- claimed
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent claim: %v", err)
	}
	winners := 0
	for claimed := range results {
		if claimed {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("concurrent lease winners = %d, want 1", winners)
	}

	var owner string
	if err := db.QueryRow(`SELECT reconcile_owner_id FROM post_targets WHERE id = $1`, targetID).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if owner == "" {
		t.Fatal("lease owner is empty after successful claim")
	}

	if _, err := db.Exec(`UPDATE post_targets
		SET reconcile_until = NOW() - INTERVAL '1 second',
		    reconcile_heartbeat_at = NOW() - INTERVAL '1 second'
		WHERE id = $1`, targetID); err != nil {
		t.Fatal(err)
	}
	claimed, err := NewPostRepository(db).ClaimPublishingTarget(targetID, "recovery-worker", 60*time.Second)
	if err != nil {
		t.Fatalf("expired lease takeover: %v", err)
	}
	if !claimed {
		t.Fatal("expired lease was not reclaimable")
	}
	var recoveredOwner string
	if err := db.QueryRow(`SELECT reconcile_owner_id FROM post_targets WHERE id = $1`, targetID).Scan(&recoveredOwner); err != nil {
		t.Fatal(err)
	}
	if recoveredOwner != "recovery-worker" {
		t.Fatalf("recovered owner = %q, want recovery-worker", recoveredOwner)
	}

	// The old owner cannot release the recovered lease after takeover.
	if err := NewPostRepository(db).ReleaseReconcileTarget(targetID, owner); !errors.Is(err, ErrReconcileLeaseLost) {
		t.Fatalf("stale owner release error = %v, want ErrReconcileLeaseLost", err)
	}

	// A stale worker cannot complete after takeover.
	result, err := db.Exec(`UPDATE post_targets
		SET status = 'published'
		WHERE id = $1 AND reconcile_owner_id = $2 AND reconcile_until > NOW()`, targetID, owner)
	if err != nil {
		t.Fatal(err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		t.Fatal(err)
	}
	if affected != 0 {
		t.Fatalf("stale owner terminal update affected %d rows, want 0", affected)
	}

	var status string
	if err := db.QueryRow(`SELECT status FROM post_targets WHERE id = $1`, targetID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "publishing" {
		t.Fatalf("target status after stale update = %q, want publishing", status)
	}
}
