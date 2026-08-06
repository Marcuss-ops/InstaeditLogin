package repository_test

import (
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

func TestPostRepository_ListPublishing_UsesReadyFilterAndLimit(t *testing.T) {
	db, mock := newMockPostDBExact(t)
	repo := repository.NewPostRepository(db)
	next := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)

	mock.ExpectQuery(`SELECT id, post_id, platform_account_id, status,
	        COALESCE(platform_post_id, ''), COALESCE(error_message, ''), published_at,
	        COALESCE(provider_state, ''), COALESCE(container_id, ''),
	provider_idempotency_key, completed_at, reconcile_attempt, next_reconcile_at
	 FROM post_targets
	 WHERE status = 'publishing'
	   AND platform_post_id IS NOT NULL
	   AND platform_post_id <> ''
	   AND next_reconcile_at <= NOW()
	 ORDER BY next_reconcile_at ASC, id ASC
	 LIMIT $1`).
		WithArgs(100).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "post_id", "platform_account_id", "status", "platform_post_id",
			"error_message", "published_at", "provider_state", "container_id",
			"provider_idempotency_key", "completed_at", "reconcile_attempt", "next_reconcile_at",
		}).AddRow(300, 100, 10, models.PostStatusPublishing, "publish-1", "", nil, "", "", nil, nil, 2, next))

	got, err := repo.ListPublishing(100)
	if err != nil {
		t.Fatalf("ListPublishing: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].ReconcileAttempt != 2 || got[0].NextReconcileAt == nil || !got[0].NextReconcileAt.Equal(next) {
		t.Fatalf("schedule fields = attempt:%d next:%v", got[0].ReconcileAttempt, got[0].NextReconcileAt)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestPostRepository_ListPublishing_RejectsNonPositiveLimit(t *testing.T) {
	db, mock := newMockPostDBExact(t)
	repo := repository.NewPostRepository(db)
	if _, err := repo.ListPublishing(0); err == nil {
		t.Fatal("expected non-positive limit error")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unexpected database call: %v", err)
	}
}

func TestPostRepository_ScheduleNextReconcile_UsesReadinessCAS(t *testing.T) {
	db, mock := newMockPostDBExact(t)
	repo := repository.NewPostRepository(db)
	next := time.Date(2026, 8, 6, 12, 1, 0, 0, time.UTC)

	mock.ExpectExec(`UPDATE post_targets
 SET reconcile_attempt = reconcile_attempt + 1,
     next_reconcile_at = $2,
     reconcile_owner_id = NULL,
     reconcile_until = NULL,
     reconcile_heartbeat_at = NULL
 WHERE id = $1
   AND reconcile_attempt = $3
   AND status = 'publishing'
   AND platform_post_id IS NOT NULL
   AND platform_post_id <> ''
   AND reconcile_owner_id = $4
   AND reconcile_until > NOW()`).
		WithArgs(int64(300), next, 2, "worker-1").
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := repo.ScheduleNextReconcileWithLease(300, "worker-1", 2, next); err != nil {
		t.Fatalf("ScheduleNextReconcile: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestPostRepository_ScheduleNextReconcile_ValidatesInput(t *testing.T) {
	db, mock := newMockPostDBExact(t)
	repo := repository.NewPostRepository(db)
	if err := repo.ScheduleNextReconcileWithLease(0, "worker-1", 0, time.Now()); err == nil {
		t.Fatal("expected invalid ID error")
	}
	if err := repo.ScheduleNextReconcileWithLease(1, "worker-1", 0, time.Time{}); err == nil {
		t.Fatal("expected zero time error")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unexpected database call: %v", err)
	}

}
