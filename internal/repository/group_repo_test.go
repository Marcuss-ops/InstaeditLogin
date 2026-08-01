package repository

import (
	"errors"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
)

func TestGroupRepository_ListByWorkspaceWithAccounts(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	now := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	mock.ExpectQuery(`SELECT g\.id, g\.workspace_id, g\.parent_group_id, g\.name, g\.created_at, g\.updated_at, ga\.account_id`).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "workspace_id", "parent_group_id", "name", "created_at", "updated_at", "account_id",
		}).
			AddRow(int64(10), int64(7), nil, "Empty", now, now, nil).
			AddRow(int64(11), int64(7), nil, "Editorial", now, now, int64(101)).
			AddRow(int64(11), int64(7), nil, "Editorial", now, now, int64(102)))

	groups, err := NewGroupRepository(db).ListByWorkspaceWithAccounts(7)
	if err != nil {
		t.Fatalf("ListByWorkspaceWithAccounts: %v", err)
	}
	if len(groups) != 2 {
		t.Fatalf("groups: got %d, want 2", len(groups))
	}
	if groups[0].ID != 10 || groups[0].Name != "Empty" || len(groups[0].AccountIDs) != 0 {
		t.Fatalf("empty group not preserved: %+v", groups[0])
	}
	if groups[1].ID != 11 || len(groups[1].AccountIDs) != 2 || groups[1].AccountIDs[0] != 101 || groups[1].AccountIDs[1] != 102 {
		t.Fatalf("members/order: %+v", groups[1])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("mock expectations: %v", err)
	}
}

func TestGroupRepository_ListByWorkspaceWithAccounts_RowError(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	now := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	mock.ExpectQuery(`SELECT g\.id, g\.workspace_id, g\.parent_group_id, g\.name, g\.created_at, g\.updated_at, ga\.account_id`).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "workspace_id", "parent_group_id", "name", "created_at", "updated_at", "account_id",
		}).
			AddRow(int64(10), int64(7), nil, "Editorial", now, now, int64(101)).
			AddRow(int64(11), int64(7), nil, "Marketing", now, now, int64(102)).
			RowError(1, errors.New("simulated row failure")))

	_, gotErr := NewGroupRepository(db).ListByWorkspaceWithAccounts(7)
	if gotErr == nil {
		t.Fatal("expected row iteration error")
	}
	if gotErr.Error() != "failed to iterate groups with accounts: simulated row failure" {
		t.Fatalf("row error: got %q", gotErr)
	}
	if mockErr := mock.ExpectationsWereMet(); mockErr != nil {
		t.Fatalf("mock expectations: %v", mockErr)
	}
}
