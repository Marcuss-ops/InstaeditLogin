package repository

import (
	"context"
	"errors"
	"regexp"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

func TestGroupRepository_UpdateSettings_CommitsMembershipAndLanguageAtomically(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	updates := []models.GroupAccountLanguageUpdate{{AccountID: 101, Language: "it"}}
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id FROM groups WHERE id = $1 AND workspace_id = $2 FOR UPDATE`)).
		WithArgs(int64(7), int64(9)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(7)))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id FROM platform_accounts WHERE user_id = $1 AND id = ANY($2)`)).
		WithArgs(int64(9), pq.Array([]int64{101})).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(101)))
	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM group_accounts WHERE group_id = $1`)).
		WithArgs(int64(7)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO group_accounts (group_id, account_id) VALUES ($1, $2)`)).
		WithArgs(int64(7), int64(101)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE platform_accounts
			 SET metadata = COALESCE(metadata, '{}'::jsonb) || jsonb_build_object('language', $1::text),
			     updated_at = NOW()
			 WHERE id = $2 AND user_id = $3`)).
		WithArgs("it", int64(101), int64(9)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if err := NewGroupRepository(db).UpdateSettings(context.Background(), 7, 9, 9, updates); err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sqlmock expectations: %v", err)
	}
}

func TestGroupRepository_UpdateSettings_RollsBackOnLanguageUpdateFailure(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id FROM groups WHERE id = $1 AND workspace_id = $2 FOR UPDATE`)).
		WithArgs(int64(7), int64(9)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(7)))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id FROM platform_accounts WHERE user_id = $1 AND id = ANY($2)`)).
		WithArgs(int64(9), pq.Array([]int64{101})).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(101)))
	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM group_accounts WHERE group_id = $1`)).
		WithArgs(int64(7)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO group_accounts (group_id, account_id) VALUES ($1, $2)`)).
		WithArgs(int64(7), int64(101)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE platform_accounts
			 SET metadata = COALESCE(metadata, '{}'::jsonb) || jsonb_build_object('language', $1::text),
			     updated_at = NOW()
			 WHERE id = $2 AND user_id = $3`)).
		WithArgs("it", int64(101), int64(9)).WillReturnError(errors.New("metadata write failed"))
	mock.ExpectRollback()

	if err := NewGroupRepository(db).UpdateSettings(context.Background(), 7, 9, 9, []models.GroupAccountLanguageUpdate{{AccountID: 101, Language: "it"}}); err == nil {
		t.Fatal("UpdateSettings: expected metadata update error")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sqlmock expectations: %v", err)
	}
}

func TestGroupRepository_UpdateSettings_RejectsForeignAccountBeforeDelete(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id FROM groups WHERE id = $1 AND workspace_id = $2 FOR UPDATE`)).
		WithArgs(int64(7), int64(9)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(7)))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id FROM platform_accounts WHERE user_id = $1 AND id = ANY($2)`)).
		WithArgs(int64(9), pq.Array([]int64{999})).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(1000)))
	mock.ExpectRollback()

	err = NewGroupRepository(db).UpdateSettings(context.Background(), 7, 9, 9, []models.GroupAccountLanguageUpdate{{AccountID: 999, Language: "it"}})
	if !errors.Is(err, ErrGroupAccountOwnership) {
		t.Fatalf("expected ErrGroupAccountOwnership, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sqlmock expectations: %v", err)
	}
}
