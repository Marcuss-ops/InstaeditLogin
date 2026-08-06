package repository

import (
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestGroupRepositoryListByWorkspacePage(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	when := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	mock.ExpectQuery(`SELECT id, workspace_id, parent_group_id, name, created_at, updated_at`).
		WithArgs(int64(7), nil, int64(0), 3).
		WillReturnRows(sqlmock.NewRows([]string{"id", "workspace_id", "parent_group_id", "name", "created_at", "updated_at"}).
			AddRow(3, 7, nil, "Zeta", when, when).
			AddRow(2, 7, nil, "Alpha", when, when).
			AddRow(1, 7, nil, "Beta", when, when))
	groups, more, err := NewGroupRepository(db).ListByWorkspacePage(7, nil, 0, 2)
	if err != nil || !more || len(groups) != 2 {
		t.Fatalf("page = %d, %v, %v", len(groups), more, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestGroupRepositoryListByWorkspaceWithAccountsPage(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	when := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	mock.ExpectQuery(`WITH page_groups AS`).
		WithArgs(int64(7), nil, int64(0), 3).
		WillReturnRows(sqlmock.NewRows([]string{"id", "workspace_id", "parent_group_id", "name", "created_at", "updated_at", "account_id", "page_count"}).
			AddRow(3, 7, nil, "Zeta", when, when, int64(10), 3).
			AddRow(2, 7, nil, "Alpha", when, when, nil, 3).
			AddRow(1, 7, nil, "Beta", when, when, int64(11), 3))
	groups, more, err := NewGroupRepository(db).ListByWorkspaceWithAccountsPage(7, nil, 0, 2)
	if err != nil || !more || len(groups) != 2 {
		t.Fatalf("page = %d, %v, %v", len(groups), more, err)
	}
	if len(groups[0].AccountIDs) != 1 || len(groups[1].AccountIDs) != 0 {
		t.Fatalf("memberships = %+v", groups)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
