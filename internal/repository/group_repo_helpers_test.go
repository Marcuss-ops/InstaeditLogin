package repository

import (
	"context"
	"database/sql"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

func TestScanGroupRow_MapsNullableParentAndJoinedAccount(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	rows := sqlmock.NewRows([]string{
		"id", "workspace_id", "parent_group_id", "name", "created_at", "updated_at", "account_id",
	}).AddRow(int64(11), int64(7), int64(10), "Editorial", time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC), time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC), int64(101))
	mock.ExpectQuery("SELECT group row").WillReturnRows(rows)

	queryRows, err := db.QueryContext(context.Background(), "SELECT group row")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if !queryRows.Next() {
		t.Fatal("expected one row")
	}
	var account sql.NullInt64
	group, err := scanGroupRow(queryRows, &account)
	if err != nil {
		t.Fatalf("scanGroupRow: %v", err)
	}
	if group.ID != 11 || group.WorkspaceID != 7 || group.Name != "Editorial" {
		t.Fatalf("group fields: %+v", group)
	}
	if group.ParentGroupID == nil || *group.ParentGroupID != 10 {
		t.Fatalf("parent_group_id: %v", group.ParentGroupID)
	}
	if !account.Valid || account.Int64 != 101 {
		t.Fatalf("account: %+v", account)
	}
	if err := queryRows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	_ = queryRows.Close()
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("mock expectations: %v", err)
	}
}

func TestResyncWorkspaceChannels_EmptyAccountsDoesNotTouchTransaction(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	mock.ExpectBegin()
	mock.ExpectCommit()
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := resyncWorkspaceChannels(context.Background(), tx, 7, 11, nil); err != nil {
		t.Fatalf("resync empty: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	// Verify the helper emitted no Exec and keep the model import used as a
	// compile-time reference for this repository test.
	var _ models.Group
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("mock expectations: %v", err)
	}
}
