//go:build integration

package repository

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/database"
	"github.com/Marcuss-ops/InstaeditLogin/internal/testutil/postgres"
)

func TestClaimBatch_MultipleWorkersDoNotDoubleClaim(t *testing.T) {
	db, cleanup := postgres.StartTestPostgres(t, postgres.WithDatabase("instaedit_worker_claim_test"))
	defer cleanup()
	if err := database.RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	if _, err := db.Exec(`
		INSERT INTO users (id, email, name)
		VALUES (1, 'claim-test@instaedit.local', 'Claim Test')
		ON CONFLICT (id) DO NOTHING;
		INSERT INTO workspaces (id, name, owner_id)
		VALUES (1, 'Claim Test Workspace', 1)
		ON CONFLICT (id) DO NOTHING;
	`); err != nil {
		t.Fatalf("seed owner/workspace: %v", err)
	}

	const jobCount = 24
	for i := 0; i < jobCount; i++ {
		if _, err := db.Exec(`
			INSERT INTO upload_jobs (
				user_id, workspace_id, source_type, source_id, title, caption, targets, status,
				ingest_after, next_attempt_at, priority
			) VALUES (1, 1, 'authenticated_drive', $1, $1, '', '[]'::jsonb, 'pending', NOW(), NULL, $2)
		`, fmt.Sprintf("claim-test-%d", i), i%3); err != nil {
			t.Fatalf("seed job %d: %v", i, err)
		}
	}

	for _, indexName := range []string{"idx_upload_jobs_claim_available", "idx_upload_jobs_publish_claim"} {
		var exists bool
		if err := db.QueryRow(`SELECT EXISTS (SELECT 1 FROM pg_indexes WHERE schemaname = 'public' AND indexname = $1)`, indexName).Scan(&exists); err != nil {
			t.Fatalf("check index %s: %v", indexName, err)
		}
		if !exists {
			t.Fatalf("migration 104 did not create %s", indexName)
		}
	}

	ctx := context.Background()
	const workerCount = 8
	claimed := make(chan int64, jobCount)
	errs := make(chan error, workerCount)
	var wg sync.WaitGroup
	for workerNumber := 0; workerNumber < workerCount; workerNumber++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			repo := NewUploadJobRepository(db)
			workerID := fmt.Sprintf("integration-worker-%d", n)
			for {
				jobs, err := repo.ClaimBatch(ctx, workerID, 1, 10*time.Minute)
				if err != nil {
					errs <- err
					return
				}
				if len(jobs) == 0 {
					return
				}
				for _, job := range jobs {
					claimed <- job.ID
				}
			}
		}(workerNumber)
	}
	wg.Wait()
	close(claimed)
	close(errs)

	for err := range errs {
		t.Fatalf("concurrent claim: %v", err)
	}

	seen := make(map[int64]struct{}, jobCount)
	for id := range claimed {
		if _, duplicate := seen[id]; duplicate {
			t.Fatalf("job %d was claimed by more than one worker", id)
		}
		seen[id] = struct{}{}
	}
	if len(seen) != jobCount {
		t.Fatalf("claimed jobs: got %d distinct jobs, want %d", len(seen), jobCount)
	}

	var leasedCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM upload_jobs WHERE status = 'leased'`).Scan(&leasedCount); err != nil {
		t.Fatalf("count leased jobs: %v", err)
	}
	if leasedCount != jobCount {
		t.Fatalf("leased rows: got %d, want %d", leasedCount, jobCount)
	}
}
