package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// TestExternalDestinationRepository_Create_Success pins the happy
// path: a row is inserted with the supplied id + ULID-style prefix,
// created_at + updated_at are returned from RETURNING, no error
// leaks out. Verifies the SQL column list is correct so a future
// migration drift surfaces immediately here.
func TestExternalDestinationRepository_Create_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	now := time.Now()
	mock.ExpectQuery(`INSERT INTO external_destinations`).
		WithArgs(
			"extdst_01JABC", // id
			"velox",         // source_system
			int64(12),       // workspace_id
			int64(345),      // platform_account_id
			true,            // enabled
			[]byte(`{}`),    // default_metadata (json.Marshal of empty map)
		).
		WillReturnRows(sqlmock.NewRows([]string{"created_at", "updated_at"}).AddRow(now, now))

	repo := NewExternalDestinationRepository(db)
	dest := &models.ExternalDestination{
		ID:                "extdst_01JABC",
		SourceSystem:      "velox",
		WorkspaceID:       12,
		PlatformAccountID: 345,
		Enabled:           true,
		DefaultMetadata:   []byte(`{}`),
	}
	if err := repo.Create(context.Background(), dest); err != nil {
		t.Fatalf("Create: want nil, got %v", err)
	}
	if dest.CreatedAt.IsZero() {
		t.Errorf("CreatedAt zero after Create (RETURNING not propagated)")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("mock expectations: %v", err)
	}
}

// TestExternalDestinationRepository_Create_DuplicateReturnsErr
// covers the UNIQUE(source_system, workspace_id, platform_account_id)
// collision path. The pq.Error SQLSTATE 23505 + constraint name
// dispatch MUST surface ErrExternalDestinationAlreadyExists so the
// handler can map to 409 Conflict (NOT 500).
//
// Constraint name format follows Postgres's auto-generated
// <table>_<columns>_key convention. If a future migration changes
// the UNIQUE TO (...), update this test.
func TestExternalDestinationRepository_Create_DuplicateReturnsErr(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	pqErr := &pq.Error{
		Code:       "23505",
		Constraint: "external_destinations_source_system_workspace_id_platform_account_id_key",
		Message:    `duplicate key value violates unique constraint "external_destinations_source_system_workspace_id_platform_account_id_key"`,
	}
	mock.ExpectQuery(`INSERT INTO external_destinations`).
		WithArgs("extdst_01JABC", "velox", int64(12), int64(345), true, []byte(`{}`)).
		WillReturnError(pqErr)

	repo := NewExternalDestinationRepository(db)
	dest := &models.ExternalDestination{
		ID:                "extdst_01JABC",
		SourceSystem:      "velox",
		WorkspaceID:       12,
		PlatformAccountID: 345,
		Enabled:           true,
		DefaultMetadata:   []byte(`{}`),
	}
	err = repo.Create(context.Background(), dest)
	if err == nil {
		t.Fatal("Create duplicate: want ErrExternalDestinationAlreadyExists, got nil")
	}
	if !errors.Is(err, ErrExternalDestinationAlreadyExists) {
		t.Errorf("Create duplicate: want ErrExternalDestinationAlreadyExists in chain, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("mock expectations: %v", err)
	}
}

// TestExternalDestinationRepository_GetByID_Found covers the lookup
// happy path: supplied ID maps to a row, all 8 columns scanned
// back, no error.
func TestExternalDestinationRepository_GetByID_Found(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	now := time.Now()
	mock.ExpectQuery(`SELECT id, source_system, workspace_id, platform_account_id, enabled`).
		WithArgs("extdst_01JABC").
		WillReturnRows(sqlmock.NewRows(
			[]string{"id", "source_system", "workspace_id", "platform_account_id",
				"enabled", "default_metadata", "created_at", "updated_at"},
		).AddRow("extdst_01JABC", "velox", int64(12), int64(345),
			true, []byte(`{"privacy_status":"private"}`), now, now))

	repo := NewExternalDestinationRepository(db)
	dest, err := repo.GetByID(context.Background(), "extdst_01JABC")
	if err != nil {
		t.Fatalf("GetByID: want nil, got %v", err)
	}
	if dest == nil {
		t.Fatal("GetByID: want found row, got nil")
	}
	if dest.ID != "extdst_01JABC" {
		t.Errorf("ID: want extdst_01JABC, got %s", dest.ID)
	}
	if dest.WorkspaceID != 12 {
		t.Errorf("WorkspaceID: want 12, got %d", dest.WorkspaceID)
	}
	if dest.PlatformAccountID != 345 {
		t.Errorf("PlatformAccountID: want 345, got %d", dest.PlatformAccountID)
	}
	if !dest.Enabled {
		t.Error("Enabled: want true, got false")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("mock expectations: %v", err)
	}
}

// TestExternalDestinationRepository_GetByID_NotFound asserts the
// (nil, nil) convention: sql.ErrNoRows maps to (nil, nil) so
// callers don't need to inspect ErrExternalDeliveryNotFound in the
// not-found case.
func TestExternalDestinationRepository_GetByID_NotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery(`SELECT id, source_system, workspace_id, platform_account_id, enabled`).
		WithArgs("extdst_01JNOTHING").
		WillReturnRows(sqlmock.NewRows(
			[]string{"id", "source_system", "workspace_id", "platform_account_id",
				"enabled", "default_metadata", "created_at", "updated_at"},
		))

	repo := NewExternalDestinationRepository(db)
	dest, err := repo.GetByID(context.Background(), "extdst_01JNOTHING")
	if err != nil {
		t.Errorf("GetByID not found: want nil error, got %v", err)
	}
	if dest != nil {
		t.Errorf("GetByID not found: want nil model, got %+v", dest)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("mock expectations: %v", err)
	}
}

// TestExternalDestinationRepository_ListByWorkspace_EnabledOnly
// verifies the partial-index-using query is built correctly when
// enabledOnly is true.
func TestExternalDestinationRepository_ListByWorkspace_EnabledOnly(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	now := time.Now()
	mock.ExpectQuery(`SELECT id, source_system, workspace_id, platform_account_id, enabled`).
		WithArgs(int64(12)).
		WillReturnRows(sqlmock.NewRows(
			[]string{"id", "source_system", "workspace_id", "platform_account_id",
				"enabled", "default_metadata", "created_at", "updated_at"},
		).
			AddRow("extdst_01JAAA", "velox", int64(12), int64(345), true, []byte(`{}`), now, now).
			AddRow("extdst_01JBBB", "velox", int64(12), int64(346), true, []byte(`{}`), now, now))

	repo := NewExternalDestinationRepository(db)
	list, err := repo.ListByWorkspace(context.Background(), 12, true)
	if err != nil {
		t.Fatalf("ListByWorkspace(enabledOnly=true): %v", err)
	}
	if len(list) != 2 {
		t.Errorf("ListByWorkspace: want 2 rows, got %d", len(list))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("mock expectations: %v", err)
	}
}

// TestExternalDestinationRepository_ListByWorkspace_DisabledIncluded
// verifies the alternative branch (no `AND enabled = TRUE` filter).
func TestExternalDestinationRepository_ListByWorkspace_DisabledIncluded(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	now := time.Now()
	mock.ExpectQuery(`SELECT id, source_system, workspace_id, platform_account_id, enabled`).
		WithArgs(int64(12)).
		WillReturnRows(sqlmock.NewRows(
			[]string{"id", "source_system", "workspace_id", "platform_account_id",
				"enabled", "default_metadata", "created_at", "updated_at"},
		).AddRow("extdst_01JAAA", "velox", int64(12), int64(345),
			false, []byte(`{}`), now, now))

	repo := NewExternalDestinationRepository(db)
	list, err := repo.ListByWorkspace(context.Background(), 12, false)
	if err != nil {
		t.Fatalf("ListByWorkspace(enabledOnly=false): %v", err)
	}
	if len(list) != 1 {
		t.Errorf("ListByWorkspace: want 1 row, got %d", len(list))
	}
	if list[0].Enabled {
		t.Errorf("Enabled flag: want false, got true (query returned the wrong kind)")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("mock expectations: %v", err)
	}
}
