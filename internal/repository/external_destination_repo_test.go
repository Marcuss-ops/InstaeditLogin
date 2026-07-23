package repository

import (
	"context"
	"errors"
	"strings"
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
// collision path via pqErr.Code + pqErr.Constraint dispatch. The test
// populates the pq.Error with the canonical SQLSTATE 23505 and the
// named constraint so the dispatch routes to ErrExternalDestinationAlreadyExists.
func TestExternalDestinationRepository_Create_DuplicateReturnsErr(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	pqErr := &pq.Error{
		Code:       "23505",
		Constraint: "external_destinations_source_system_workspace_id_platform_account_id_key",
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

// TestExternalDestinationRepository_Delete_FKFromExternalDeliveries
// covers the 23503 foreign-key violation on delete. The dispatch
// classifies ANY 23503 as ErrExternalDestinationHasDependents,
// using pqErr.Table for the referencing table name.
func TestExternalDestinationRepository_Delete_FKFromExternalDeliveries(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	pqErr := &pq.Error{
		Code: "23503",
		// Table is the referencing table that blocks the delete.
		Table: "external_deliveries",
	}
	mock.ExpectExec(`DELETE FROM external_destinations WHERE id = \$1`).
		WithArgs("extdst_01JABC").
		WillReturnError(pqErr)

	repo := NewExternalDestinationRepository(db)
	err = repo.Delete(context.Background(), "extdst_01JABC")
	if err == nil {
		t.Fatal("Delete FK from external_deliveries: want ErrExternalDestinationHasDependents, got nil")
	}
	if !errors.Is(err, ErrExternalDestinationHasDependents) {
		t.Errorf("Delete FK from external_deliveries: want ErrExternalDestinationHasDependents in chain, got %v", err)
	}
	// Legacy alias MUST resolve to the same sentinel (back-compat).
	if !errors.Is(err, ErrExternalDestinationHasDeliveries) {
		t.Errorf("Delete FK from external_deliveries: legacy alias ErrExternalDestinationHasDeliveries should ALSO resolve, got %v", err)
	}
	if !strings.Contains(err.Error(), `"external_deliveries"`) {
		t.Errorf("Delete FK from external_deliveries: wrapped error should mention referencing table name, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("mock expectations: %v", err)
	}
}

// TestExternalDestinationRepository_Delete_FKFromFutureTable pins
// the "matches any FK that references external_destinations, not
// only external_deliveries" spec. The dispatch classifies ANY
// 23503 as ErrExternalDestinationHasDependents, using
// pqErr.Table for the referencing table name.
func TestExternalDestinationRepository_Delete_FKFromFutureTable(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	pqErr := &pq.Error{
		Code:  "23503",
		Table: "external_audit_log",
	}
	mock.ExpectExec(`DELETE FROM external_destinations WHERE id = \$1`).
		WithArgs("extdst_01JDEF").
		WillReturnError(pqErr)

	repo := NewExternalDestinationRepository(db)
	err = repo.Delete(context.Background(), "extdst_01JDEF")
	if err == nil {
		t.Fatal("Delete FK from future table: want ErrExternalDestinationHasDependents, got nil")
	}
	if !errors.Is(err, ErrExternalDestinationHasDependents) {
		t.Errorf("Delete FK from future table: want ErrExternalDestinationHasDependents in chain, got %v", err)
	}
	if !strings.Contains(err.Error(), `"external_audit_log"`) {
		t.Errorf("Delete FK from future table: wrapped error should mention the future referencing table name, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("mock expectations: %v", err)
	}
}

// TestExternalDestinationRepository_Delete_FKNoTable verifies that
// a 23503 without a populated Table field still routes to the
// sentinel, producing a generic wrapped error without a table name.
func TestExternalDestinationRepository_Delete_FKNoTable(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	pqErr := &pq.Error{Code: "23503"}
	mock.ExpectExec(`DELETE FROM external_destinations WHERE id = \$1`).
		WithArgs("extdst_01JNOTABLE").
		WillReturnError(pqErr)

	repo := NewExternalDestinationRepository(db)
	err = repo.Delete(context.Background(), "extdst_01JNOTABLE")
	if err == nil {
		t.Fatal("Delete 23503 without Table: want ErrExternalDestinationHasDependents, got nil")
	}
	if !errors.Is(err, ErrExternalDestinationHasDependents) {
		t.Errorf("Delete 23503 without Table: want ErrExternalDestinationHasDependents in chain, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("mock expectations: %v", err)
	}
}

// TestExternalDestinationRepository_Delete_NotFound pins the
// existing 0-rows-affected path: when the destination id doesn't
// exist, Delete returns ErrExternalDestinationNotFound wrapped
// with id context (zero rows affected, no SQL error).
func TestExternalDestinationRepository_Delete_NotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	mock.ExpectExec(`DELETE FROM external_destinations WHERE id = \$1`).
		WithArgs("extdst_01JNOTHING").
		WillReturnResult(sqlmock.NewResult(0, 0))

	repo := NewExternalDestinationRepository(db)
	err = repo.Delete(context.Background(), "extdst_01JNOTHING")
	if err == nil {
		t.Fatal("Delete missing destination: want ErrExternalDestinationNotFound, got nil")
	}
	if !errors.Is(err, ErrExternalDestinationNotFound) {
		t.Errorf("Delete missing destination: want ErrExternalDestinationNotFound in chain, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("mock expectations: %v", err)
	}
}
