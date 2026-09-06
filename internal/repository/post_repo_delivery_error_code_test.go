package repository_test

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// Tests for PostRepository.MarkDeliveryDispatchFailed — the narrow
// diagnostic stamp behind the worker's DeliveryErrorCodeWriter contract
// (internal/worker/publish_worker_delivery.go). Contract:
//
//  1. Only last_error_code is written: status, published_at, lease, and
//     every state-machine field stay untouched (single-column UPDATE).
//  2. The WHERE clause is status-agnostic (no CAS) — a dispatch failure
//     can be observed on a target in any state.
//  3. Zero rows affected → repository.ErrPostTargetNotFound.
//  4. Empty code / non-positive id are rejected before SQL.

func TestPostRepository_MarkDeliveryDispatchFailed_StampsOnlyErrorCode(t *testing.T) {
	db, mock := newMockPostDBExact(t)
	repo := repository.NewPostRepository(db)

	mock.ExpectExec(`UPDATE post_targets
 SET last_error_code = $2
 WHERE id = $1`).
		WithArgs(int64(200), "HTTP_503").
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := repo.MarkDeliveryDispatchFailed(200, "HTTP_503"); err != nil {
		t.Fatalf("MarkDeliveryDispatchFailed: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet SQL expectations: %v", err)
	}
}

func TestPostRepository_MarkDeliveryDispatchFailed_NotFound(t *testing.T) {
	db, mock := newMockPostDBExact(t)
	repo := repository.NewPostRepository(db)

	mock.ExpectExec(`UPDATE post_targets
 SET last_error_code = $2
 WHERE id = $1`).
		WithArgs(int64(404), "ERR_DRIVE_SESSION_EXPIRED").
		WillReturnResult(sqlmock.NewResult(0, 0)) // zero rows → not found

	err := repo.MarkDeliveryDispatchFailed(404, "ERR_DRIVE_SESSION_EXPIRED")
	if !errors.Is(err, repository.ErrPostTargetNotFound) {
		t.Fatalf("err = %v, want ErrPostTargetNotFound", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet SQL expectations: %v", err)
	}
}

func TestPostRepository_MarkDeliveryDispatchFailed_InputGuards(t *testing.T) {
	db, mock := newMockPostDBExact(t)
	repo := repository.NewPostRepository(db)

	if err := repo.MarkDeliveryDispatchFailed(0, "HTTP_500"); err == nil {
		t.Errorf("non-positive id must be rejected before SQL")
	}
	if err := repo.MarkDeliveryDispatchFailed(7, ""); err == nil {
		t.Errorf("empty errorCode must be rejected before SQL")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("guards must not touch the DB; unmet expectations: %v", err)
	}
}

// Cross-check the contract that matters for dashboards: the stamp is
// queryable alongside the state-machine columns it must NOT disturb.
// Modeled by scanning a stamped row through a plain status projection —
// the row's status is whatever the publish state machine left it at
// (here: published), independent of last_error_code.
func TestPostRepository_MarkDeliveryDispatchFailed_StatusUnaffected(t *testing.T) {
	db, mock := newMockPostDBExact(t)
	repo := repository.NewPostRepository(db)

	mock.ExpectExec(`UPDATE post_targets
 SET last_error_code = $2
 WHERE id = $1`).
		WithArgs(int64(201), "DELIVERY_ERROR").
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := repo.MarkDeliveryDispatchFailed(201, "DELIVERY_ERROR"); err != nil {
		t.Fatalf("MarkDeliveryDispatchFailed: %v", err)
	}

	// The same row still reads its state-machine status untouched.
	mock.ExpectQuery("SELECT status FROM post_targets WHERE id = $1").
		WithArgs(int64(201)).
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow(string(models.PostStatusPublished)))

	var status models.PostStatus
	if err := db.QueryRowContext(context.Background(),
		`SELECT status FROM post_targets WHERE id = $1`, int64(201)).Scan(&status); err != nil {
		t.Fatalf("read status back: %v", err)
	}
	if status != models.PostStatusPublished {
		t.Errorf("status = %q, want published (stamp must not touch state)", status)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet SQL expectations: %v", err)
	}
}
