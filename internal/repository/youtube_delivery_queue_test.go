//go:build integration

package repository

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/database"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/testutil/postgres"
)

// seedDeliveryFixture seeds the users/workspaces/platform_accounts/
// posts/post_targets rows a youtube_target_publications row references,
// then creates `perJob` delivery rows (State=ready_to_upload) for each
// of `jobCount` upload jobs. Returns the created delivery ids grouped
// per job so tests can assert fan-out/claim semantics.
func seedDeliveryFixture(t *testing.T, db *sql.DB, jobCount, perJob int) [][]int64 {
	t.Helper()
	ctx := context.Background()

	if _, err := db.Exec(`
		INSERT INTO users (id, email, name) VALUES (1, 'delivery-queue@instaedit.local', 'Delivery Queue')
		ON CONFLICT (id) DO NOTHING;
		INSERT INTO workspaces (id, name, owner_id) VALUES (1, 'Delivery Workspace', 1)
		ON CONFLICT (id) DO NOTHING;
		INSERT INTO platform_accounts (id, user_id, workspace_id, platform, platform_user_id, username)
		VALUES (11, 1, 1, 'youtube', 'ch-11', 'ch-11'),
		       (12, 1, 1, 'youtube', 'ch-12', 'ch-12')
		ON CONFLICT (id) DO NOTHING;
	`); err != nil {
		t.Fatalf("seed users/workspaces/accounts: %v", err)
	}

	repo := NewYouTubeTargetPublicationRepository(db)
	deliveryIDs := make([][]int64, 0, jobCount)
	for jobIdx := 0; jobIdx < jobCount; jobIdx++ {
		jobID := int64(1000 + jobIdx)
		if _, err := db.Exec(`
			INSERT INTO upload_jobs (id, user_id, workspace_id, source_type, source_id, title, caption, targets, status, priority)
			VALUES ($1, 1, 1, 'authenticated_drive', $2, $2, '', '[]'::jsonb, 'pending', $3)
			ON CONFLICT (id) DO NOTHING
		`, jobID, fmt.Sprintf("delivery-job-%d", jobIdx), jobIdx); err != nil {
			t.Fatalf("seed upload job %d: %v", jobIdx, err)
		}
		var postID int64
		if err := db.QueryRow(`
			INSERT INTO posts (workspace_id, title, caption, status, upload_job_id)
			VALUES (1, $1, '', 'queued', $2) RETURNING id
		`, fmt.Sprintf("delivery-post-%d", jobIdx), jobID).Scan(&postID); err != nil {
			t.Fatalf("seed post %d: %v", jobIdx, err)
		}

		jobDeliveryIDs := make([]int64, 0, perJob)
		for targetIdx := 0; targetIdx < perJob; targetIdx++ {
			accountID := int64(11 + targetIdx)
			var targetID int64
			if err := db.QueryRow(`
				INSERT INTO post_targets (post_id, platform_account_id, status)
				VALUES ($1, $2, 'queued') RETURNING id
			`, postID, accountID).Scan(&targetID); err != nil {
				t.Fatalf("seed post_target job=%d target=%d: %v", jobIdx, targetIdx, err)
			}
			pub := &models.YouTubeTargetPublication{
				UploadJobID:         jobID,
				PostTargetID:        targetID,
				PlatformAccountID:   accountID,
				YouTubeUploadStatus: "youtube_uploading",
				DesiredPrivacy:      "public",
				State:               "ready_to_upload",
				Priority:            int16(jobIdx*10 + targetIdx),
				MaxAttempts:         2,
			}
			if err := repo.Create(ctx, pub); err != nil {
				t.Fatalf("create delivery job=%d target=%d: %v", jobIdx, targetIdx, err)
			}
			jobDeliveryIDs = append(jobDeliveryIDs, pub.ID)
		}
		deliveryIDs = append(deliveryIDs, jobDeliveryIDs)
	}
	return deliveryIDs
}

// TestClaimReadyDeliveries_FanOutAcrossJobsAndWorkers verifies the
// core fan-out semantic: one upload_job with N targets produces N
// INDEPENDENT claimable rows, and multiple workers claiming the same
// queue never double-claim a row (FOR UPDATE SKIP LOCKED). This is the
// multi-channel fan-out with a global worker pool — a slow channel can
// no longer block its siblings inside a single job claim.
func TestClaimReadyDeliveries_FanOutAcrossJobsAndWorkers(t *testing.T) {
	db, cleanup := postgres.StartTestPostgres(t, postgres.WithDatabase("instaedit_delivery_queue_fanout"))
	defer cleanup()
	if err := database.RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	const jobCount = 3
	const perJob = 2
	deliveryIDs := seedDeliveryFixture(t, db, jobCount, perJob)
	expected := make(map[int64]bool)
	for _, ids := range deliveryIDs {
		if len(ids) != perJob {
			t.Fatalf("job seeded %d deliveries, want %d", len(ids), perJob)
		}
		for _, id := range ids {
			expected[id] = true
		}
	}

	ctx := context.Background()
	const workerCount = 4
	claimed := make(chan int64, len(expected))
	errs := make(chan error, workerCount)
	var wg sync.WaitGroup
	for n := 0; n < workerCount; n++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			repo := NewYouTubeTargetPublicationRepository(db)
			workerID := fmt.Sprintf("delivery-worker-%d", n)
			for {
				rows, err := repo.ClaimReadyDeliveries(ctx, workerID, 1, 10*time.Minute)
				if err != nil {
					errs <- fmt.Errorf("worker %d claim: %w", n, err)
					return
				}
				if len(rows) == 0 {
					return
				}
				for _, row := range rows {
					if row.State != "uploading" || row.LeaseOwner == nil || *row.LeaseOwner != workerID {
						errs <- fmt.Errorf("worker %d claimed row %d not leased (state=%q owner=%v)", n, row.ID, row.State, row.LeaseOwner)
						return
					}
					claimed <- row.ID
				}
			}
		}(n)
	}
	wg.Wait()
	close(claimed)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	got := make(map[int64]int)
	for id := range claimed {
		got[id]++
	}
	if len(got) != len(expected) {
		t.Fatalf("claimed %d distinct deliveries, want %d (ids=%v)", len(got), len(expected), got)
	}
	for id := range expected {
		if got[id] != 1 {
			t.Fatalf("delivery %d claimed %d times, want exactly 1 (no double-claim)", id, got[id])
		}
	}
}

// TestClaimReadyDeliveries_PriorityOrdering verifies the claim ORDER BY
// priority ASC (lower = higher priority) so a higher-priority delivery
// is handed to the pool before a lower-priority one.
func TestClaimReadyDeliveries_PriorityOrdering(t *testing.T) {
	db, cleanup := postgres.StartTestPostgres(t, postgres.WithDatabase("instaedit_delivery_queue_priority"))
	defer cleanup()
	if err := database.RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	// seedDeliveryFixture assigns priority jobIdx*10+targetIdx:
	// job0 → 0,1 ; job1 → 10,11 ; job2 → 20,21.
	deliveryIDs := seedDeliveryFixture(t, db, 3, 2)
	byID := make(map[int64]int)
	for jobIdx, ids := range deliveryIDs {
		for targetIdx, id := range ids {
			byID[id] = jobIdx*10 + targetIdx
		}
	}

	ctx := context.Background()
	repo := NewYouTubeTargetPublicationRepository(db)
	rows, err := repo.ClaimReadyDeliveries(ctx, "priority-worker", 6, time.Minute)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(rows) != 6 {
		t.Fatalf("claimed %d rows, want 6", len(rows))
	}
	for i := 1; i < len(rows); i++ {
		if byID[rows[i-1].ID] > byID[rows[i].ID] {
			t.Fatalf("claim order not priority-ascending: %d (prio %d) before %d (prio %d)",
				rows[i-1].ID, byID[rows[i-1].ID], rows[i].ID, byID[rows[i].ID])
		}
	}
}

// TestMarkDeliveryUploaded_SuccessTransition verifies the atomic
// success path: state='youtube_uploaded' + youtube_upload_status +
// youtube_video_id + attempt++ + lease release in one UPDATE.
func TestMarkDeliveryUploaded_SuccessTransition(t *testing.T) {
	db, cleanup := postgres.StartTestPostgres(t, postgres.WithDatabase("instaedit_delivery_queue_uploaded"))
	defer cleanup()
	if err := database.RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	deliveryIDs := seedDeliveryFixture(t, db, 1, 1)
	ctx := context.Background()
	repo := NewYouTubeTargetPublicationRepository(db)

	rows, err := repo.ClaimReadyDeliveries(ctx, "upload-worker", 1, time.Minute)
	if err != nil || len(rows) != 1 {
		t.Fatalf("claim: rows=%d err=%v", len(rows), err)
	}
	delivery := rows[0]
	if err := repo.MarkDeliveryUploaded(ctx, delivery.ID, "upload-worker", "video-abc123"); err != nil {
		t.Fatalf("MarkDeliveryUploaded: %v", err)
	}
	got, err := repo.FindByID(ctx, delivery.ID)
	if err != nil || got == nil {
		t.Fatalf("FindByID: %+v err=%v", got, err)
	}
	if got.State != "youtube_uploaded" {
		t.Errorf("state=%q, want youtube_uploaded", got.State)
	}
	if got.YouTubeUploadStatus != "youtube_uploaded" {
		t.Errorf("youtube_upload_status=%q, want youtube_uploaded", got.YouTubeUploadStatus)
	}
	if got.YouTubeVideoID == nil || *got.YouTubeVideoID != "video-abc123" {
		t.Errorf("youtube_video_id=%v, want video-abc123", got.YouTubeVideoID)
	}
	if got.AttemptCount != 1 {
		t.Errorf("attempt_count=%d, want 1", got.AttemptCount)
	}
	if got.LeaseOwner != nil || got.LeaseExpiresAt != nil {
		t.Errorf("lease not cleared after success: owner=%v expires=%v", got.LeaseOwner, got.LeaseExpiresAt)
	}
	_ = deliveryIDs
}

// TestMarkDeliveryFailed_RetryThenDeadLetter verifies the retry budget:
// attempt 1 → retry_wait with a future next_attempt_at (unclaimable
// until it elapses); attempt 2 (max_attempts=2) → dead_letter.
func TestMarkDeliveryFailed_RetryThenDeadLetter(t *testing.T) {
	db, cleanup := postgres.StartTestPostgres(t, postgres.WithDatabase("instaedit_delivery_queue_retry"))
	defer cleanup()
	if err := database.RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	seedDeliveryFixture(t, db, 1, 1)
	ctx := context.Background()
	repo := NewYouTubeTargetPublicationRepository(db)

	rows, err := repo.ClaimReadyDeliveries(ctx, "retry-worker", 1, time.Minute)
	if err != nil || len(rows) != 1 {
		t.Fatalf("claim: rows=%d err=%v", len(rows), err)
	}
	delivery := rows[0]
	future := time.Now().Add(5 * time.Minute)
	if err := repo.MarkDeliveryFailed(ctx, delivery.ID, "retry-worker", "upload_failed", "boom", future); err != nil {
		t.Fatalf("MarkDeliveryFailed #1: %v", err)
	}
	got, err := repo.FindByID(ctx, delivery.ID)
	if err != nil || got == nil {
		t.Fatalf("FindByID after fail #1: %+v err=%v", got, err)
	}
	if got.State != "retry_wait" {
		t.Errorf("state=%q after fail #1, want retry_wait", got.State)
	}
	if got.AttemptCount != 1 {
		t.Errorf("attempt_count=%d after fail #1, want 1", got.AttemptCount)
	}
	if got.NextAttemptAt == nil || !got.NextAttemptAt.After(time.Now()) {
		t.Errorf("next_attempt_at=%v, want future backoff cursor", got.NextAttemptAt)
	}
	if got.LastErrorCode == nil || *got.LastErrorCode != "upload_failed" {
		t.Errorf("last_error_code=%v, want upload_failed", got.LastErrorCode)
	}
	// The row is unclaimable while next_attempt_at is in the future.
	again, err := repo.ClaimReadyDeliveries(ctx, "retry-worker-2", 1, time.Minute)
	if err != nil {
		t.Fatalf("claim while in backoff: %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("claimed %d rows while next_attempt_at is future, want 0 (backoff honored)", len(again))
	}

	// Force the backoff cursor to elapse, then re-claim and fail again:
	// attempt 2 hits max_attempts=2 → dead_letter.
	if _, err := db.Exec(`UPDATE youtube_target_publications SET next_attempt_at = NOW() - INTERVAL '1 second' WHERE id = $1`, delivery.ID); err != nil {
		t.Fatalf("expire backoff: %v", err)
	}
	rows2, err := repo.ClaimReadyDeliveries(ctx, "retry-worker-3", 1, time.Minute)
	if err != nil || len(rows2) != 1 {
		t.Fatalf("re-claim after backoff: rows=%d err=%v", len(rows2), err)
	}
	if err := repo.MarkDeliveryFailed(ctx, delivery.ID, "retry-worker-3", "upload_failed", "boom again", time.Now().Add(time.Minute)); err != nil {
		t.Fatalf("MarkDeliveryFailed #2: %v", err)
	}
	got, err = repo.FindByID(ctx, delivery.ID)
	if err != nil || got == nil {
		t.Fatalf("FindByID after fail #2: %+v err=%v", got, err)
	}
	if got.State != "dead_letter" {
		t.Errorf("state=%q after fail #2, want dead_letter", got.State)
	}
	if got.AttemptCount != 2 {
		t.Errorf("attempt_count=%d after fail #2, want 2", got.AttemptCount)
	}
}

// TestReclaimExpiredDeliveryLeases_ReturnsStuckRows verifies the crash
// recovery path: a delivery stuck in 'uploading' with an expired lease
// is returned to 'ready_to_upload' so a peer worker re-claims it.
func TestReclaimExpiredDeliveryLeases_ReturnsStuckRows(t *testing.T) {
	db, cleanup := postgres.StartTestPostgres(t, postgres.WithDatabase("instaedit_delivery_queue_reclaim"))
	defer cleanup()
	if err := database.RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	seedDeliveryFixture(t, db, 2, 2)
	ctx := context.Background()
	repo := NewYouTubeTargetPublicationRepository(db)

	rows, err := repo.ClaimReadyDeliveries(ctx, "crash-worker", 4, time.Minute)
	if err != nil || len(rows) != 4 {
		t.Fatalf("claim: rows=%d err=%v", len(rows), err)
	}
	// Simulate a crashed worker: expire every lease immediately.
	if _, err := db.Exec(`UPDATE youtube_target_publications SET lease_expires_at = NOW() - INTERVAL '1 second' WHERE lease_owner IS NOT NULL`); err != nil {
		t.Fatalf("expire leases: %v", err)
	}
	reclaimed, err := repo.ReclaimExpiredDeliveryLeases(ctx, 100)
	if err != nil {
		t.Fatalf("reclaim: %v", err)
	}
	if reclaimed != 4 {
		t.Fatalf("reclaimed %d rows, want 4", reclaimed)
	}
	// The rows are claimable again by a fresh worker.
	again, err := repo.ClaimReadyDeliveries(ctx, "fresh-worker", 4, time.Minute)
	if err != nil || len(again) != 4 {
		t.Fatalf("re-claim after reclaim: rows=%d err=%v", len(again), err)
	}
	for _, row := range again {
		if row.LeaseOwner == nil || *row.LeaseOwner != "fresh-worker" {
			t.Fatalf("re-claimed row %d not leased to fresh-worker (owner=%v)", row.ID, row.LeaseOwner)
		}
	}
}
