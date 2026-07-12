package repository_test

import (
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// newMockWorkspaceDB returns a (*sql.DB, sqlmock.Sqlmock, error) trio wired
// to a single sqlmock controller. Caller is responsible for closing the DB
// after the test (use t.Cleanup).
func newMockWorkspaceDB(t *testing.T) (*sql.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatalf("create sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, mock
}

func TestWorkspaceCreate_HappyAssignsIDs(t *testing.T) {
	db, mock := newMockWorkspaceDB(t)
	repo := repository.NewWorkspaceRepository(db)
	fixedTime := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)

	mock.ExpectQuery(
		`INSERT INTO workspaces (name, owner_id) VALUES ($1, $2)
		 RETURNING id, created_at`,
	).WithArgs("My Workspace", int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(42, fixedTime))

	w := &models.Workspace{Name: "My Workspace", OwnerID: 7}
	if err := repo.Create(w); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if w.ID != 42 {
		t.Errorf("ID: want 42, got %d", w.ID)
	}
	if !w.CreatedAt.Equal(fixedTime) {
		t.Errorf("CreatedAt: want %v, got %v", fixedTime, w.CreatedAt)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet mock expectations: %v", err)
	}
}

func TestWorkspaceCreate_DBErrorPropagates(t *testing.T) {
	db, mock := newMockWorkspaceDB(t)
	repo := repository.NewWorkspaceRepository(db)

	mock.ExpectQuery(
		`INSERT INTO workspaces (name, owner_id) VALUES ($1, $2)
		 RETURNING id, created_at`,
	).WithArgs("X", int64(1)).
		WillReturnError(errors.New("connection refused"))

	err := repo.Create(&models.Workspace{Name: "X", OwnerID: 1})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, errors.Unwrap(err)) && err.Error() != "failed to create workspace: connection refused" {
		t.Errorf("error message: want wrapping pattern, got %q", err.Error())
	}
}

// errContains is a tiny helper: t.Fatal if err is nil OR if err's message
// doesn't contain substr. Used to assert fmt.Errorf-style wrapping.
func errContains(t *testing.T, err error, substr string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error containing %q, got nil", substr)
	}
	if !strings.Contains(err.Error(), substr) {
		t.Errorf("error %q did not contain %q", err.Error(), substr)
	}
}

func TestWorkspaceFindByID_FoundReturnsRow(t *testing.T) {
	db, mock := newMockWorkspaceDB(t)
	repo := repository.NewWorkspaceRepository(db)
	createdAt := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	mock.ExpectQuery(
		`SELECT id, name, owner_id, created_at
		 FROM workspaces
		 WHERE id = $1`,
	).WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "owner_id", "created_at"}).
			AddRow(42, "Mine", 7, createdAt))

	w, err := repo.FindByID(42)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if w == nil {
		t.Fatal("FindByID returned nil workspace, want populated")
	}
	if w.ID != 42 || w.Name != "Mine" || w.OwnerID != 7 {
		t.Errorf("workspace fields mismatch: %+v", w)
	}
	if !w.CreatedAt.Equal(createdAt) {
		t.Errorf("CreatedAt: want %v, got %v", createdAt, w.CreatedAt)
	}
}

func TestWorkspaceFindByID_NotFoundReturnsNilNil(t *testing.T) {
	// Convention from rest of repo layer: FindByID returns (nil, nil) for
	// sql.ErrNoRows so callers don't have to inspect error types.
	db, mock := newMockWorkspaceDB(t)
	repo := repository.NewWorkspaceRepository(db)

	mock.ExpectQuery(
		`SELECT id, name, owner_id, created_at
		 FROM workspaces
		 WHERE id = $1`,
	).WithArgs(int64(999)).
		WillReturnError(sql.ErrNoRows)

	w, err := repo.FindByID(999)
	if err != nil {
		t.Fatalf("FindByID expected nil err for ErrNoRows, got %v", err)
	}
	if w != nil {
		t.Errorf("FindByID expected nil workspace, got %+v", w)
	}
}

func TestWorkspaceFindByID_DBErrorWrapped(t *testing.T) {
	db, mock := newMockWorkspaceDB(t)
	repo := repository.NewWorkspaceRepository(db)

	mock.ExpectQuery(`SELECT id, name, owner_id, created_at
		 FROM workspaces
		 WHERE id = $1`).
		WithArgs(int64(7)).
		WillReturnError(errors.New("connection timeout"))

	_, err := repo.FindByID(7)
	errContains(t, err, "failed to find workspace by id")
	errContains(t, err, "connection timeout")
}

func TestWorkspaceListByOwner_MultipleRows(t *testing.T) {
	db, mock := newMockWorkspaceDB(t)
	repo := repository.NewWorkspaceRepository(db)
	t1 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2025, 2, 2, 0, 0, 0, 0, time.UTC)

	mock.ExpectQuery(
		`SELECT id, name, owner_id, created_at
		 FROM workspaces
		 WHERE owner_id = $1
		 ORDER BY created_at DESC`,
	).WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "owner_id", "created_at"}).
			AddRow(2, "Second", 7, t2).
			AddRow(1, "First", 7, t1))

	got, err := repo.ListByOwner(7)
	if err != nil {
		t.Fatalf("ListByOwner: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len: want 2, got %d", len(got))
	}
	if got[0].ID != 2 || got[0].Name != "Second" {
		t.Errorf("first row: %+v", got[0])
	}
	if got[1].ID != 1 || got[1].Name != "First" {
		t.Errorf("second row: %+v", got[1])
	}
}

// TestWorkspaceListByOwner_NoResultsHasZeroLen locks in that an empty
// result set is treated as "no rows" by callers (len==0). The returned
// slice may be nil or non-nil; callers MUST use len()==0 as the empty
// contract, NOT got == nil. If you change this to a non-nil
// invariant, add a json.Marshal regression test.
func TestWorkspaceListByOwner_NoResultsHasZeroLen(t *testing.T) {
	db, mock := newMockWorkspaceDB(t)
	repo := repository.NewWorkspaceRepository(db)

	mock.ExpectQuery(
		`SELECT id, name, owner_id, created_at
	 FROM workspaces
	 WHERE owner_id = $1
	 ORDER BY created_at DESC`,
	).WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "owner_id", "created_at"})) // empty

	got, err := repo.ListByOwner(7)
	if err != nil {
		t.Fatalf("ListByOwner: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len: want 0, got %d", len(got))
	}
}

func TestWorkspaceListByOwner_QueryError(t *testing.T) {
	db, mock := newMockWorkspaceDB(t)
	repo := repository.NewWorkspaceRepository(db)

	mock.ExpectQuery(
		`SELECT id, name, owner_id, created_at
		 FROM workspaces
		 WHERE owner_id = $1
		 ORDER BY created_at DESC`,
	).WithArgs(int64(7)).
		WillReturnError(errors.New("query timeout"))

	_, err := repo.ListByOwner(7)
	errContains(t, err, "failed to list workspaces by owner")
	errContains(t, err, "query timeout")
}
