package repository_test

import (
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

func TestPostRepository_UpdateStatus_RejectsTerminalRegression(t *testing.T) {
	for _, terminal := range []models.PostStatus{
		models.PostStatusPublished,
		models.PostStatusDLQ,
		models.PostStatus("dead_letter"),
	} {
		t.Run(string(terminal), func(t *testing.T) {
			db, mock := newMockPostDBExact(t)
			repo := repository.NewPostRepository(db)

			mock.ExpectBegin()
			mock.ExpectQuery(`SELECT post_id FROM post_targets WHERE id = $1`).WithArgs(int64(200)).
				WillReturnRows(sqlmock.NewRows([]string{"post_id"}).AddRow(100))
			mock.ExpectQuery(`SELECT id FROM post_targets WHERE id = $1 FOR UPDATE`).WithArgs(int64(200)).
				WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(200))
			mock.ExpectQuery(`SELECT id FROM posts WHERE id = $1 FOR UPDATE`).WithArgs(int64(100)).
				WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(100))
			mock.ExpectExec(
				`UPDATE post_targets
				 SET status = $1, platform_post_id = $2, error_message = $3, published_at = $4,
				     provider_state = $6, container_id = $7
				 WHERE id = $5
				   AND (status = $1 OR status NOT IN ('published', 'partially_published', 'failed', 'dlq'))`,
			).WithArgs(models.PostStatusPublishing, "", "", (*time.Time)(nil), int64(200), "", "").
				WillReturnResult(sqlmock.NewResult(0, 0))
			mock.ExpectQuery(`SELECT status FROM post_targets WHERE id = $1`).WithArgs(int64(200)).
				WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow(terminal))
			mock.ExpectRollback()

			err := repo.UpdateStatus(&models.PostTarget{ID: 200, Status: models.PostStatusPublishing})
			if err == nil {
				t.Fatal("expected stale terminal transition error, got nil")
			}
			if !errors.Is(err, repository.ErrPostTargetTransitionStale) {
				t.Fatalf("error must wrap ErrPostTargetTransitionStale, got %v", err)
			}
			if errors.Is(err, repository.ErrPostTargetNotFound) {
				t.Fatalf("stale terminal transition must not be reported as not found: %v", err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatalf("unmet expectations: %v", err)
			}
		})
	}
}

func TestPostRepository_RetryTarget_OnlyFailedTargetsCanBeReset(t *testing.T) {
	for _, terminal := range []string{"dead_letter", "dlq"} {
		t.Run(terminal, func(t *testing.T) {
			db, mock := newMockPostDBExact(t)
			repo := repository.NewPostRepository(db)

			mock.ExpectBegin()
			mock.ExpectQuery(`SELECT post_id FROM post_targets WHERE id = $1`).WithArgs(int64(200)).
				WillReturnRows(sqlmock.NewRows([]string{"post_id"}).AddRow(100))
			mock.ExpectQuery(`SELECT id FROM post_targets WHERE id = $1 FOR UPDATE`).WithArgs(int64(200)).
				WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(200))
			mock.ExpectQuery(`SELECT id FROM posts WHERE id = $1 FOR UPDATE`).WithArgs(int64(100)).
				WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(100))
			mock.ExpectExec(`UPDATE post_targets SET status = 'queued', error_message = '' WHERE id = $1 AND status = 'failed'`).
				WithArgs(int64(200)).
				WillReturnResult(sqlmock.NewResult(0, 0))
			mock.ExpectQuery(`SELECT status FROM post_targets WHERE id = $1`).WithArgs(int64(200)).
				WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow(terminal))
			mock.ExpectRollback()

			err := repo.RetryTarget(200)
			if err == nil {
				t.Fatal("expected terminal retry to be rejected")
			}
			if !errors.Is(err, repository.ErrPostTargetTransitionStale) {
				t.Fatalf("terminal retry must wrap ErrPostTargetTransitionStale, got %v", err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatalf("unmet expectations: %v", err)
			}
		})
	}
}

func TestPostRepository_RepairAggregateStatuses_IsIdempotent(t *testing.T) {
	db, mock := newMockPostDBExact(t)
	repo := repository.NewPostRepository(db)

	// Candidate snapshot contains one drifted parent. The first repair fixes
	// it; the second repair sees the same post already consistent and does not
	// issue a parent UPDATE.
	for i := 0; i < 2; i++ {
		mock.ExpectQuery(`SELECT id, status FROM posts ORDER BY id ASC`).
			WillReturnRows(sqlmock.NewRows([]string{"id", "status"}).AddRow(100, models.PostStatusQueued))
		mock.ExpectBegin()
		mock.ExpectQuery(`SELECT id FROM post_targets WHERE post_id = $1 ORDER BY id ASC FOR UPDATE`).
			WithArgs(int64(100)).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(200))
		mock.ExpectQuery(`SELECT status FROM posts WHERE id = $1 FOR UPDATE`).
			WithArgs(int64(100)).
			WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow(func() models.PostStatus {
				if i == 0 {
					return models.PostStatusQueued
				}
				return models.PostStatusPublished
			}()))
		if i == 0 {
			mock.ExpectQuery(`SELECT status FROM post_targets WHERE post_id = $1 ORDER BY id ASC`).
				WithArgs(int64(100)).
				WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow(models.PostStatusPublished))
			mock.ExpectExec(`UPDATE posts SET status = $1 WHERE id = $2`).
				WithArgs(models.PostStatusPublished, int64(100)).
				WillReturnResult(sqlmock.NewResult(0, 1))
		} else {
			mock.ExpectQuery(`SELECT status FROM post_targets WHERE post_id = $1 ORDER BY id ASC`).
				WithArgs(int64(100)).
				WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow(models.PostStatusPublished))
		}
		mock.ExpectCommit()
	}

	repaired, err := repo.RepairAggregateStatuses()
	if err != nil {
		t.Fatalf("first repair: %v", err)
	}
	if repaired != 1 {
		t.Fatalf("first repair count = %d, want 1", repaired)
	}
	repaired, err = repo.RepairAggregateStatuses()
	if err != nil {
		t.Fatalf("second repair: %v", err)
	}
	if repaired != 0 {
		t.Fatalf("second repair count = %d, want 0", repaired)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}
