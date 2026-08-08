package repository_test

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

const reconcileLeaseClaimSQL = `WITH candidate AS (
 SELECT id
 FROM post_targets
 WHERE id = $1
   AND status = 'publishing'
   AND platform_post_id IS NOT NULL
   AND platform_post_id <> ''
   AND next_reconcile_at <= NOW()
   AND (reconcile_owner_id IS NULL OR reconcile_until IS NULL OR reconcile_until <= NOW())
 FOR UPDATE SKIP LOCKED
)
UPDATE post_targets pt
 SET reconcile_owner_id = $2,
     reconcile_until = NOW() + ($3 || ' seconds')::INTERVAL,
     reconcile_heartbeat_at = NOW()
 FROM candidate
 WHERE pt.id = candidate.id
 RETURNING pt.id`

func TestPostRepository_ClaimPublishingTargetWithLease_ClaimsAndReclaims(t *testing.T) {
	db, mock := newMockPostDBExact(t)
	repo := repository.NewPostRepository(db)

	mock.ExpectQuery(reconcileLeaseClaimSQL).
		WithArgs(int64(300), "worker-a", "60").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(300))
	claimed, err := repo.ClaimPublishingTarget(300, "worker-a", time.Minute)
	if err != nil || !claimed {
		t.Fatalf("claim = %v, %v; want true,nil", claimed, err)
	}
	mock.ExpectQuery(reconcileLeaseClaimSQL).
		WithArgs(int64(300), "worker-b", "60").
		WillReturnError(sql.ErrNoRows)
	claimed, err = repo.ClaimPublishingTarget(300, "worker-b", time.Minute)
	if err != nil || claimed {
		t.Fatalf("active lease claim = %v, %v; want false,nil", claimed, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPostRepository_HeartbeatReconcileTarget_UsesOwnerCAS(t *testing.T) {
	db, mock := newMockPostDBExact(t)
	repo := repository.NewPostRepository(db)
	mock.ExpectExec(`UPDATE post_targets SET reconcile_until = NOW() + ($3 || ' seconds')::INTERVAL, reconcile_heartbeat_at = NOW() WHERE id = $1 AND reconcile_owner_id = $2 AND reconcile_until > NOW() AND status = 'publishing'`).
		WithArgs(int64(300), "worker-a", "60").
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := repo.HeartbeatReconcileTarget(300, "worker-a", time.Minute); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPostRepository_HeartbeatReconcileTarget_ReportsLeaseLoss(t *testing.T) {
	db, mock := newMockPostDBExact(t)
	repo := repository.NewPostRepository(db)
	mock.ExpectExec(`UPDATE post_targets SET reconcile_until = NOW() + ($3 || ' seconds')::INTERVAL, reconcile_heartbeat_at = NOW() WHERE id = $1 AND reconcile_owner_id = $2 AND reconcile_until > NOW() AND status = 'publishing'`).
		WithArgs(int64(300), "worker-a", "60").
		WillReturnResult(sqlmock.NewResult(0, 0))
	if err := repo.HeartbeatReconcileTarget(300, "worker-a", time.Minute); !errors.Is(err, repository.ErrReconcileLeaseLost) {
		t.Fatalf("heartbeat error = %v, want ErrReconcileLeaseLost", err)
	}
}

func TestPostRepository_ReleaseReconcileTarget_UsesOwnerAndExpiryCAS(t *testing.T) {
	db, mock := newMockPostDBExact(t)
	repo := repository.NewPostRepository(db)
	mock.ExpectExec(`UPDATE post_targets SET reconcile_owner_id = NULL, reconcile_until = NULL, reconcile_heartbeat_at = NULL WHERE id = $1 AND reconcile_owner_id = $2 AND reconcile_until > NOW() AND status = 'publishing'`).
		WithArgs(int64(300), "worker-a").
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := repo.ReleaseReconcileTarget(300, "worker-a"); err != nil {
		t.Fatalf("release: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPostRepository_UpdateReconcileStatusWithLease_CAS(t *testing.T) {
	db, mock := newMockPostDBExact(t)
	repo := repository.NewPostRepository(db)
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT post_id FROM post_targets WHERE id = $1`).WithArgs(int64(300)).
		WillReturnRows(sqlmock.NewRows([]string{"post_id"}).AddRow(100))
	mock.ExpectQuery(`SELECT id FROM post_targets WHERE id = $1 FOR UPDATE`).WithArgs(int64(300)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(300))
	mock.ExpectQuery(`SELECT id FROM posts WHERE id = $1 FOR UPDATE`).WithArgs(int64(100)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(100))
	mock.ExpectExec(`UPDATE post_targets SET status = $1::text::post_status, platform_post_id = $2, error_message = $3, published_at = $4, provider_state = $6, container_id = $7, last_error_code = $8, completed_at = CASE WHEN $1::text IN ('failed', 'dlq', 'blocked_auth') THEN COALESCE(completed_at, NOW()) ELSE completed_at END, reconcile_owner_id = NULL, reconcile_until = NULL, reconcile_heartbeat_at = NULL WHERE id = $5 AND status = 'publishing' AND reconcile_owner_id = $9 AND reconcile_until > NOW()`).
		WithArgs(models.PostStatusPublished, "provider-id", "", sqlmock.AnyArg(), int64(300), "PUBLISH_COMPLETE", "", "", "worker-a").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT status FROM post_targets WHERE post_id = $1 ORDER BY id ASC`).WithArgs(int64(100)).
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow(models.PostStatusPublished))
	mock.ExpectExec(`UPDATE posts SET status = $1 WHERE id = $2`).WithArgs(models.PostStatusPublished, int64(100)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	if err := repo.UpdateReconcileStatusWithLease(&models.PostTarget{ID: 300, Status: models.PostStatusPublished, PlatformPostID: "provider-id", ProviderState: "PUBLISH_COMPLETE"}, "worker-a"); err != nil {
		t.Fatalf("terminal CAS: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
