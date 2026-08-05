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

const (
	lockGroupSQL = `SELECT id, name FROM groups WHERE id = $1 AND workspace_id = $2 FOR UPDATE`
	validateSQL  = `SELECT id FROM platform_accounts WHERE user_id = $1 AND id = ANY($2)`
	existingSQL  = `SELECT account_id FROM group_accounts WHERE group_id = $1`
	deleteSQL    = `DELETE FROM group_accounts WHERE group_id = $1`
	insertSQL    = `INSERT INTO group_accounts (group_id, account_id) VALUES ($1, $2)`
	languageSQL  = `UPDATE platform_accounts
			 SET metadata = COALESCE(metadata, '{}'::jsonb) || jsonb_build_object('language', $1::text),
			     updated_at = NOW()
			 WHERE id = $2 AND user_id = $3`
	bindSQL = `INSERT INTO workspace_channels (workspace_id, platform_account_id, group_name, enabled)
			 SELECT $1, ids.account_id, $2, TRUE
			   FROM unnest($3::bigint[]) AS ids(account_id)
			 ON CONFLICT (workspace_id, platform_account_id)
			 DO UPDATE SET group_name = EXCLUDED.group_name`
	resyncSQL = `UPDATE workspace_channels AS wc
			 SET group_name = (
			     SELECT g.name
			       FROM group_accounts AS ga
			       JOIN groups AS g ON g.id = ga.group_id
			      WHERE ga.account_id = wc.platform_account_id
			        AND g.workspace_id = $1
			      ORDER BY CASE WHEN g.id = $2 THEN 0 ELSE 1 END, g.name, g.id
			      LIMIT 1
			 )
			 WHERE wc.workspace_id = $1
			   AND wc.platform_account_id = ANY($3)`
)

func expectSettingsPrefix(mock sqlmock.Sqlmock, existing ...int64) {
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(lockGroupSQL)).
		WithArgs(int64(7), int64(9)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name"}).AddRow(int64(7), "Editorial"))
	mock.ExpectQuery(regexp.QuoteMeta(validateSQL)).
		WithArgs(int64(9), pq.Array([]int64{101})).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(101)))
	rows := sqlmock.NewRows([]string{"account_id"})
	for _, id := range existing {
		rows.AddRow(id)
	}
	mock.ExpectQuery(regexp.QuoteMeta(existingSQL)).
		WithArgs(int64(7)).WillReturnRows(rows)
}

func expectSingleIncoming(mock sqlmock.Sqlmock, language string, expectWorkspace bool) {
	mock.ExpectExec(regexp.QuoteMeta(deleteSQL)).
		WithArgs(int64(7)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(insertSQL)).
		WithArgs(int64(7), int64(101)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(languageSQL)).
		WithArgs(language, int64(101), int64(9)).WillReturnResult(sqlmock.NewResult(0, 1))
	if expectWorkspace {
		mock.ExpectExec(regexp.QuoteMeta(bindSQL)).
			WithArgs(int64(9), "Editorial", pq.Array([]int64{101})).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec(regexp.QuoteMeta(resyncSQL)).
			WithArgs(int64(9), int64(7), pq.Array([]int64{101})).
			WillReturnResult(sqlmock.NewResult(0, 1))
	}
}

func TestGroupRepository_UpdateSettings_CommitsMembershipAndLanguageAtomically(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	expectSettingsPrefix(mock, 101)
	expectSingleIncoming(mock, "it", true)
	mock.ExpectCommit()

	if err := NewGroupRepository(db).UpdateSettings(context.Background(), 7, 9, 9, []models.GroupAccountLanguageUpdate{{AccountID: 101, Language: "it"}}); err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sqlmock expectations: %v", err)
	}
}

func TestGroupRepository_UpdateSettings_ClearsRemovedWorkspaceChannelGroup(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	expectSettingsPrefix(mock, 101, 102)
	mock.ExpectExec(regexp.QuoteMeta(deleteSQL)).
		WithArgs(int64(7)).WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec(regexp.QuoteMeta(insertSQL)).
		WithArgs(int64(7), int64(101)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(languageSQL)).
		WithArgs("en", int64(101), int64(9)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(bindSQL)).
		WithArgs(int64(9), "Editorial", pq.Array([]int64{101})).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(resyncSQL)).
		WithArgs(int64(9), int64(7), pq.Array([]int64{101, 102})).
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectCommit()

	if err := NewGroupRepository(db).UpdateSettings(context.Background(), 7, 9, 9, []models.GroupAccountLanguageUpdate{{AccountID: 101, Language: "en"}}); err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sqlmock expectations: %v", err)
	}
}

func TestGroupRepository_UpdateSettings_RollsBackOnWorkspaceChannelSyncFailure(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	expectSettingsPrefix(mock, 101)
	mock.ExpectExec(regexp.QuoteMeta(deleteSQL)).
		WithArgs(int64(7)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(insertSQL)).
		WithArgs(int64(7), int64(101)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(languageSQL)).
		WithArgs("it", int64(101), int64(9)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(bindSQL)).
		WithArgs(int64(9), "Editorial", pq.Array([]int64{101})).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(resyncSQL)).
		WithArgs(int64(9), int64(7), pq.Array([]int64{101})).WillReturnError(errors.New("workspace channel sync failed"))
	mock.ExpectRollback()

	if err := NewGroupRepository(db).UpdateSettings(context.Background(), 7, 9, 9, []models.GroupAccountLanguageUpdate{{AccountID: 101, Language: "it"}}); err == nil {
		t.Fatal("UpdateSettings: expected workspace channel sync error")
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

	expectSettingsPrefix(mock, 101)
	mock.ExpectExec(regexp.QuoteMeta(deleteSQL)).
		WithArgs(int64(7)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(insertSQL)).
		WithArgs(int64(7), int64(101)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(languageSQL)).
		WithArgs("it", int64(101), int64(9)).WillReturnError(errors.New("metadata write failed"))
	mock.ExpectRollback()

	if err := NewGroupRepository(db).UpdateSettings(context.Background(), 7, 9, 9, []models.GroupAccountLanguageUpdate{{AccountID: 101, Language: "it"}}); err == nil {
		t.Fatal("UpdateSettings: expected metadata update error")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sqlmock expectations: %v", err)
	}
}

const (
	removeLockSQL = `SELECT id FROM groups WHERE id = $1 AND workspace_id = $2 FOR UPDATE`
	removeDeleteSQL = `DELETE FROM group_accounts WHERE group_id = $1 AND account_id = $2`
	removeResyncSQL = `UPDATE workspace_channels AS wc
			 SET group_name = (
			     SELECT g.name
			       FROM group_accounts AS ga
			       JOIN groups AS g ON g.id = ga.group_id
			      WHERE ga.account_id = wc.platform_account_id
			        AND g.workspace_id = $1
			      ORDER BY CASE WHEN g.id = $2 THEN 0 ELSE 1 END, g.name, g.id
			      LIMIT 1
			 )
			 WHERE wc.workspace_id = $1 AND wc.platform_account_id = $2`
)

func TestGroupRepository_RemoveAccountFromGroupTx_CommitsMembershipAndResync(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(removeLockSQL)).
		WithArgs(int64(7), int64(9)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(7)))
	mock.ExpectExec(regexp.QuoteMeta(removeDeleteSQL)).
		WithArgs(int64(7), int64(101)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(removeResyncSQL)).
		WithArgs(int64(9), int64(7), int64(101)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if err := NewGroupRepository(db).RemoveAccountFromGroupTx(context.Background(), 7, 9, 101); err != nil {
		t.Fatalf("RemoveAccountFromGroupTx: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sqlmock expectations: %v", err)
	}
}

func TestGroupRepository_RemoveAccountFromGroupTx_GroupNotFoundRollsBack(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(removeLockSQL)).
		WithArgs(int64(7), int64(9)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectRollback()

	err = NewGroupRepository(db).RemoveAccountFromGroupTx(context.Background(), 7, 9, 101)
	if !errors.Is(err, ErrGroupNotFound) {
		t.Fatalf("expected ErrGroupNotFound, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sqlmock expectations: %v", err)
	}
}

func TestGroupRepository_RemoveAccountFromGroupTx_ResyncFailureRollsBack(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(removeLockSQL)).
		WithArgs(int64(7), int64(9)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(7)))
	mock.ExpectExec(regexp.QuoteMeta(removeDeleteSQL)).
		WithArgs(int64(7), int64(101)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(removeResyncSQL)).
		WithArgs(int64(9), int64(7), int64(101)).WillReturnError(errors.New("workspace channel resync failed"))
	mock.ExpectRollback()

	err = NewGroupRepository(db).RemoveAccountFromGroupTx(context.Background(), 7, 9, 101)
	if err == nil {
		t.Fatal("RemoveAccountFromGroupTx: expected resync error")
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
	mock.ExpectQuery(regexp.QuoteMeta(lockGroupSQL)).
		WithArgs(int64(7), int64(9)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name"}).AddRow(int64(7), "Editorial"))
	mock.ExpectQuery(regexp.QuoteMeta(validateSQL)).
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
