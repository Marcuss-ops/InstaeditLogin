package repository_test

import (
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

func TestPostRepository_RepairAggregateStatusForPost_ResolvesQueuedParent(t *testing.T) {
	db, mock := newMockPostDBExact(t)
	repo := repository.NewPostRepository(db)

	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT id FROM post_targets WHERE post_id = $1 ORDER BY id ASC FOR UPDATE`).
		WithArgs(int64(100)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(200)))
	mock.ExpectQuery(`SELECT status FROM posts WHERE id = $1 FOR UPDATE`).
		WithArgs(int64(100)).
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow(models.PostStatusQueued))
	mock.ExpectQuery(`SELECT status FROM post_targets WHERE post_id = $1 ORDER BY id ASC`).
		WithArgs(int64(100)).
		WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow(models.PostStatusPublished))
	mock.ExpectExec(`UPDATE posts SET status = $1 WHERE id = $2`).
		WithArgs(models.PostStatusPublished, int64(100)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	oldStatus, newStatus, changed, err := repo.RepairAggregateStatusForPost(100)
	if err != nil {
		t.Fatalf("RepairAggregateStatusForPost: %v", err)
	}
	if oldStatus != models.PostStatusQueued {
		t.Fatalf("old status = %q, want queued", oldStatus)
	}
	if newStatus != models.PostStatusPublished {
		t.Fatalf("new status = %q, want published", newStatus)
	}
	if !changed {
		t.Fatal("changed = false, want true")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sqlmock expectations: %v", err)
	}
}

func TestPostRepository_RepairAggregateStatusForPost_IsIdempotent(t *testing.T) {
	db, mock := newMockPostDBExact(t)
	repo := repository.NewPostRepository(db)

	for i := 0; i < 2; i++ {
		mock.ExpectBegin()
		mock.ExpectQuery(`SELECT id FROM post_targets WHERE post_id = $1 ORDER BY id ASC FOR UPDATE`).
			WithArgs(int64(100)).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(200)))
		mock.ExpectQuery(`SELECT status FROM posts WHERE id = $1 FOR UPDATE`).
			WithArgs(int64(100)).
			WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow(models.PostStatusPublished))
		mock.ExpectQuery(`SELECT status FROM post_targets WHERE post_id = $1 ORDER BY id ASC`).
			WithArgs(int64(100)).
			WillReturnRows(sqlmock.NewRows([]string{"status"}).AddRow(models.PostStatusPublished))
		mock.ExpectCommit()
	}

	oldStatus, newStatus, changed, err := repo.RepairAggregateStatusForPost(100)
	if err != nil {
		t.Fatalf("first repair: %v", err)
	}
	if oldStatus != models.PostStatusPublished || newStatus != models.PostStatusPublished || changed {
		t.Fatalf("first repair = (%q, %q, %v), want published/published/false", oldStatus, newStatus, changed)
	}

	oldStatus, newStatus, changed, err = repo.RepairAggregateStatusForPost(100)
	if err != nil {
		t.Fatalf("second repair: %v", err)
	}
	if oldStatus != models.PostStatusPublished || newStatus != models.PostStatusPublished || changed {
		t.Fatalf("second repair = (%q, %q, %v), want published/published/false", oldStatus, newStatus, changed)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sqlmock expectations: %v", err)
	}
}
