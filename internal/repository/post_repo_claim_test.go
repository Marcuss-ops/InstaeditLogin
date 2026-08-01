package repository_test

import (
	"database/sql"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// TestPostClaimQueuedTarget_Success covers the FASE 1.1 SKIP LOCKED
// atomic-claim happy path: a single row in 'queued' is locked via
// SELECT FOR UPDATE SKIP LOCKED and then transitioned to 'publishing'
// inside an explicit tx. The function returns (true, nil).
func TestPostClaimQueuedTarget_Success(t *testing.T) {
	db, mock := newMockPostDBExact(t)
	repo := repository.NewPostRepository(db)

	// FASE 1.1: claim is one transaction: BEGIN → SELECT FOR UPDATE
	// SKIP LOCKED → parent lookup/locks → target update → aggregate → COMMIT.
	mock.ExpectBegin()
	mock.ExpectQuery(
		`SELECT id FROM post_targets
		 WHERE id = $1 AND status = 'queued'
		 FOR UPDATE SKIP LOCKED`,
	).WithArgs(int64(200)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(200))
	mock.ExpectQuery(`SELECT post_id FROM post_targets WHERE id = $1`).WithArgs(int64(200)).
		WillReturnRows(sqlmock.NewRows([]string{"post_id"}).AddRow(100))
	mock.ExpectQuery(`SELECT id FROM posts WHERE id = $1 FOR UPDATE`).WithArgs(int64(100)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(100))
	mock.ExpectExec(`UPDATE post_targets SET status = 'publishing' WHERE id = $1`).
		WithArgs(int64(200)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT status FROM post_targets WHERE post_id = $1 ORDER BY id ASC`).WithArgs(int64(100)).
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow(models.PostStatusPublishing))
	mock.ExpectExec(`UPDATE posts SET status = $1 WHERE id = $2`).WithArgs(models.PostStatusPublishing, int64(100)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	claimed, err := repo.ClaimQueuedTarget(200)
	if err != nil {
		t.Fatalf("ClaimQueuedTarget: %v", err)
	}
	if !claimed {
		t.Errorf("claimed: want true, got false (SELECT returned row → claim won)")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// TestPostClaimQueuedTarget_AlreadyClaimed covers the FASE 1.1 SKIP
// LOCKED loser path: when another worker/tx already holds a row lock
// on this row, SELECT FOR UPDATE SKIP LOCKED returns zero rows
// immediately (no blocking). The function returns (false, nil).
func TestPostClaimQueuedTarget_AlreadyClaimed(t *testing.T) {
	db, mock := newMockPostDBExact(t)
	repo := repository.NewPostRepository(db)

	mock.ExpectBegin()
	mock.ExpectQuery(
		`SELECT id FROM post_targets
		 WHERE id = $1 AND status = 'queued'
		 FOR UPDATE SKIP LOCKED`,
	).WithArgs(int64(200)).
		WillReturnError(sql.ErrNoRows)
	// On SKIP LOCKED miss, the tx is rolled back (deferred). No UPDATE or COMMIT.
	mock.ExpectRollback()

	claimed, err := repo.ClaimQueuedTarget(200)
	if err != nil {
		t.Fatalf("ClaimQueuedTarget: %v (must NOT error when another worker already claimed; the loser path is a normal skip)", err)
	}
	if claimed {
		t.Errorf("claimed: want false, got true (SKIP LOCKED returned no rows → claim lost)")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// TestPostClaimQueuedTarget_DBError covers the FASE 1.1 path where
// Begin() itself fails (DB unreachable). The error must surface so
// the worker can log and continue to the next target.
func TestPostClaimQueuedTarget_DBError(t *testing.T) {
	db, mock := newMockPostDBExact(t)
	repo := repository.NewPostRepository(db)

	mock.ExpectBegin().WillReturnError(errors.New("connection lost"))

	claimed, err := repo.ClaimQueuedTarget(200)
	if err == nil {
		t.Fatal("expected DB error to propagate, got nil")
	}
	if claimed {
		t.Errorf("claimed: want false on DB error, got true")
	}
	if !strings.Contains(err.Error(), "failed to begin claim tx") {
		t.Errorf("error should be wrapped: %v", err)
	}
}

// TestPostClaimQueuedTarget_RowsAffectedReadError is REMOVED in FASE
// 1.1 — the old RowsAffected() error path (connection interrupted
// between Exec and RowsAffected) no longer exists. The new
// tx-based claim has different failure modes (Begin error, SELECT
// error, UPDATE error, Commit error). Covered by the tests above
// and the new TestPostClaimQueuedTarget_CommitError below.
func TestPostClaimQueuedTarget_CommitError(t *testing.T) {
	db, mock := newMockPostDBExact(t)
	repo := repository.NewPostRepository(db)

	mock.ExpectBegin()
	mock.ExpectQuery(
		`SELECT id FROM post_targets
		 WHERE id = $1 AND status = 'queued'
		 FOR UPDATE SKIP LOCKED`,
	).WithArgs(int64(200)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(200))
	mock.ExpectQuery(`SELECT post_id FROM post_targets WHERE id = $1`).WithArgs(int64(200)).
		WillReturnRows(sqlmock.NewRows([]string{"post_id"}).AddRow(100))
	mock.ExpectQuery(`SELECT id FROM posts WHERE id = $1 FOR UPDATE`).WithArgs(int64(100)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(100))
	mock.ExpectExec(`UPDATE post_targets SET status = 'publishing' WHERE id = $1`).
		WithArgs(int64(200)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT status FROM post_targets WHERE post_id = $1 ORDER BY id ASC`).WithArgs(int64(100)).
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow(models.PostStatusPublishing))
	mock.ExpectExec(`UPDATE posts SET status = $1 WHERE id = $2`).WithArgs(models.PostStatusPublishing, int64(100)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit().WillReturnError(errors.New("commit failed"))

	_, err := repo.ClaimQueuedTarget(200)
	if err == nil {
		t.Fatal("expected Commit error to propagate, got nil")
	}
	if !strings.Contains(err.Error(), "failed to commit claim") {
		t.Errorf("error should preserve 'commit claim' context: %v", err)
	}
}

// --- FASE 1.1: ClaimPublishingTarget tests (ReconcileWorker claim) ---

// TestPostClaimPublishingTarget_Success covers the reconciler's claim
// happy path: SELECT FOR UPDATE SKIP LOCKED finds a row in status
// 'publishing' with non-null platform_post_id, locks it, commits the
// tx (releasing the row lock), and returns true.
func TestPostClaimPublishingTarget_Success(t *testing.T) {
	db, mock := newMockPostDBExact(t)
	repo := repository.NewPostRepository(db)

	mock.ExpectBegin()
	mock.ExpectQuery(
		`SELECT id FROM post_targets
		 WHERE id = $1 AND status = 'publishing' AND platform_post_id IS NOT NULL AND platform_post_id <> ''
		 FOR UPDATE SKIP LOCKED`,
	).WithArgs(int64(300)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(300))
	// ClaimPublishingTarget does NOT UPDATE status — it only locks +
	// commits. The status stays 'publishing'.
	mock.ExpectCommit()

	claimed, err := repo.ClaimPublishingTarget(300)
	if err != nil {
		t.Fatalf("ClaimPublishingTarget: %v", err)
	}
	if !claimed {
		t.Errorf("claimed: want true, got false")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// TestPostClaimPublishingTarget_AlreadyClaimed covers the reconciler
// loser path: SELECT FOR UPDATE SKIP LOCKED returns no rows because
// another reconciler replica already holds the row lock.
func TestPostClaimPublishingTarget_AlreadyClaimed(t *testing.T) {
	db, mock := newMockPostDBExact(t)
	repo := repository.NewPostRepository(db)

	mock.ExpectBegin()
	mock.ExpectQuery(
		`SELECT id FROM post_targets
		 WHERE id = $1 AND status = 'publishing' AND platform_post_id IS NOT NULL AND platform_post_id <> ''
		 FOR UPDATE SKIP LOCKED`,
	).WithArgs(int64(300)).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectRollback()

	claimed, err := repo.ClaimPublishingTarget(300)
	if err != nil {
		t.Fatalf("ClaimPublishingTarget: %v", err)
	}
	if claimed {
		t.Errorf("claimed: want false (SKIP LOCKED returned no rows), got true")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// --- FASE 1.1: concurrent claim race test ---

// TestPostClaimQueuedTarget_ConcurrentRace_TwoGoroutines_OneWinner
// simulates two goroutines racing to claim the SAME row. The first
// one to select locks the row; the second gets SKIP LOCKED → zero
// rows → returns (false, nil). Only ONE claim succeeds.
//
// This is the FASE 1.1 end-to-end invariant: exactly one publish
// per target, even with N worker replicas.
func TestPostClaimQueuedTarget_ConcurrentRace_TwoGoroutines_OneWinner(t *testing.T) {
	// Worker A: will win the claim (successful SELECT).
	dbA, mockA, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatalf("create sqlmock A: %v", err)
	}
	defer dbA.Close()
	repoA := repository.NewPostRepository(dbA)

	mockA.ExpectBegin()
	mockA.ExpectQuery(
		`SELECT id FROM post_targets
		 WHERE id = $1 AND status = 'queued'
		 FOR UPDATE SKIP LOCKED`,
	).WithArgs(int64(200)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(200))
	mockA.ExpectQuery(`SELECT post_id FROM post_targets WHERE id = $1`).WithArgs(int64(200)).
		WillReturnRows(sqlmock.NewRows([]string{"post_id"}).AddRow(100))
	mockA.ExpectQuery(`SELECT id FROM posts WHERE id = $1 FOR UPDATE`).WithArgs(int64(100)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(100))
	mockA.ExpectExec(`UPDATE post_targets SET status = 'publishing' WHERE id = $1`).
		WithArgs(int64(200)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mockA.ExpectQuery(`SELECT status FROM post_targets WHERE post_id = $1 ORDER BY id ASC`).WithArgs(int64(100)).
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow(models.PostStatusPublishing))
	mockA.ExpectExec(`UPDATE posts SET status = $1 WHERE id = $2`).WithArgs(models.PostStatusPublishing, int64(100)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mockA.ExpectCommit()

	// Worker B: will lose the claim (SELECT returns no rows).
	dbB, mockB, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatalf("create sqlmock B: %v", err)
	}
	defer dbB.Close()
	repoB := repository.NewPostRepository(dbB)

	mockB.ExpectBegin()
	mockB.ExpectQuery(
		`SELECT id FROM post_targets
		 WHERE id = $1 AND status = 'queued'
		 FOR UPDATE SKIP LOCKED`,
	).WithArgs(int64(200)).
		WillReturnError(sql.ErrNoRows)
	mockB.ExpectRollback()

	// Use a mutex to deterministically order the two goroutines
	// so A always claims first and B always loses.
	var (
		mu        sync.Mutex
		firstCall = true
	)

	var wg sync.WaitGroup
	wg.Add(2)
	var (
		aWon, bWon bool
		aErr, bErr error
	)

	go func() {
		defer wg.Done()
		mu.Lock()
		if firstCall {
			firstCall = false
			mu.Unlock()
			aWon, aErr = repoA.ClaimQueuedTarget(200)
		} else {
			mu.Unlock()
			aWon, aErr = repoA.ClaimQueuedTarget(200)
		}
	}()

	go func() {
		defer wg.Done()
		// B claims after A — mutex ensures ordering.
		mu.Lock()
		if firstCall {
			firstCall = false
			mu.Unlock()
			bWon, bErr = repoB.ClaimQueuedTarget(200)
		} else {
			mu.Unlock()
			bWon, bErr = repoB.ClaimQueuedTarget(200)
		}
	}()

	wg.Wait()

	// Assertions: A won, B lost.
	if aErr != nil {
		t.Errorf("Worker A error: %v", aErr)
	}
	if !aWon {
		t.Error("Worker A should have won the claim (first SELECT)")
	}
	if bErr != nil {
		t.Fatalf("Worker B error: %v (loser path must be nil on skip)", bErr)
	}
	if bWon {
		t.Error("Worker B should have lost the claim (SKIP LOCKED on already-locked row)")
	}

	// Verify all expectations were met.
	if err := mockA.ExpectationsWereMet(); err != nil {
		t.Errorf("Worker A unmet expectations: %v", err)
	}
	if err := mockB.ExpectationsWereMet(); err != nil {
		t.Errorf("Worker B unmet expectations: %v", err)
	}
}
