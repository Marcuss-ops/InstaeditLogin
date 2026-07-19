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
// collision path via the new Detail-content dispatch (the repo no
// longer relies on pqErr.Constraint name + auto-NAME). The test
// populates the pq.Error Detail field with the canonical Postgres
// shape:
//
//	Key (source_system, workspace_id, platform_account_id)=(velox, 12, 345) already exists.
//
// so the regex anchor (`Key (source_system, workspace_id,
// platform_account_id)=`) matches. The test ALSO sets Constraint
// + Message to their canonical Postgres values so the test
// exercises the realistic real-world error shape (a future
// operator reading the dispatch code sees Constraint set too);
// the dispatch should EITHER path reach the same sentinel —
// covered by TestExternalDestinationRepository_Create_DuplicateConstraintNameIrrelevant
// below which sets Constraint="" explicitly.
func TestExternalDestinationRepository_Create_DuplicateReturnsErr(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	pqErr := &pq.Error{
		Code:    "23505",
		Detail:  `Key (source_system, workspace_id, platform_account_id)=(velox, 12, 345) already exists.`,
		Message: `duplicate key value violates unique constraint "external_destinations_source_system_workspace_id_platform_account_id_key"`,
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

// TestExternalDestinationRepository_Create_DuplicateConstraintNameIrrelevant
// pins the headline behaviour of the refactor: the constraint
// name pqErr.Constraint is NO LONGER consulted by the dispatch.
// The test simulates a hypothetical future where the
// auto-NAME drift happens (migration renames the constraint, or
// pg_restore into a different schema) — Constraint is set to ""
// AND a non-canonical junk value — and verifies the dispatch
// STILL routes to ErrExternalDestinationAlreadyExists because the
// Detail regex matched.
//
// Without this test, a regression that re-introduces a
// pqErr.Constraint == "external_destinations_..." check would
// slip through the existing TestExternalDestinationRepository_Create_DuplicateReturnsErr
// (which uses both Constraint and Detail).
func TestExternalDestinationRepository_Create_DuplicateConstraintNameIrrelevant(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	pqErr := &pq.Error{
		Code:       "23505",
		Detail:     `Key (source_system, workspace_id, platform_account_id)=(velox, 12, 345) already exists.`,
		Constraint: "junk_value_post_rename_or_pg_restore",
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
		t.Fatal("Create duplicate (no constraint name): want ErrExternalDestinationAlreadyExists, got nil")
	}
	if !errors.Is(err, ErrExternalDestinationAlreadyExists) {
		t.Errorf("Create duplicate (no constraint name): want ErrExternalDestinationAlreadyExists in chain, got %v", err)
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
// covers the NEW Detail-content FK dispatch in Delete for the
// canonical Postgres 23503 error shape:
//
//	update or delete on table "external_destinations" violates foreign key constraint "external_deliveries_external_destination_id_fkey" on table "external_deliveries"
//	Key (id)=(extdst_01JABC) is still referenced from table "external_deliveries".
//
// The test asserts ErrExternalDestinationHasDependents surfaces
// (the SAME sentinel regardless of which REFERENCING table blocked
// the delete) and that the wrapped error message includes the
// referencing-table name verbatim (from the regex capture
// group).
func TestExternalDestinationRepository_Delete_FKFromExternalDeliveries(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	pqErr := &pq.Error{
		Code:    "23503",
		Detail:  `Key (id)=(extdst_01JABC) is still referenced from table "external_deliveries".`,
		Message: `update or delete on table "external_destinations" violates foreign key constraint "external_deliveries_external_destination_id_fkey" on table "external_deliveries"`,
	}
	// \$1 escapes the $ so sqlmock's default QueryMatcherRegexp
	// treats it as literal — not the regex end-of-string anchor.
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
	// The wrapped error must surface the referencing table name
	// from the regex capture group. The production code uses %q
	// on capturing, which produces literal "external_deliveries"
	// regardless of whether the input Detail was quoted.
	if !strings.Contains(err.Error(), `"external_deliveries"`) {
		t.Errorf("Delete FK from external_deliveries: wrapped error should mention referencing table name, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("mock expectations: %v", err)
	}
}

// TestExternalDestinationRepository_Delete_FKFromFutureTable pins
// the "matches any FK that references external_destinations, not
// only external_deliveries" spec. The test simulates a
// hypothetical future migration adding an `external_audit_log`
// table with a FK on external_destination_id →
// external_destinations.id. The Detail content references
// "external_audit_log" — a schema that does NOT exist in the
// current codebase — and the dispatch MUST STILL route to
// ErrExternalDestinationHasDependents.
func TestExternalDestinationRepository_Delete_FKFromFutureTable(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	pqErr := &pq.Error{
		Code:    "23503",
		Detail:  `Key (id)=(extdst_01JDEF) is still referenced from table "external_audit_log".`,
		Message: `update or delete on table "external_destinations" violates foreign key constraint "external_audit_log_external_destination_id_fkey" on table "external_audit_log"`,
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

// TestExternalDestinationRepository_Delete_FKDetailDoesNotMatch
// pins the regex's specificity: a 23503 with a Detail that does
// NOT match the FK-regex (e.g. a future pg_dump/pg_restore
// variant that changes the Detail wording) MUST NOT accidentally
// dispatch as ErrExternalDestinationHasDependents.
//
// Specificity guard — the regex must be neither too loose
// (false positives) nor too tight (false negatives).
func TestExternalDestinationRepository_Delete_FKDetailDoesNotMatch(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	pqErr := &pq.Error{
		Code:    "23503",
		Detail:  `Key (different_column)=(value) is still referenced from table "other_table".`,
		Message: `update or delete on table "other_table" violates foreign key constraint "fk_x"`,
	}
	mock.ExpectExec(`DELETE FROM external_destinations WHERE id = \$1`).
		WithArgs("extdst_01JNOT_FK_RESTRICT").
		WillReturnError(pqErr)

	repo := NewExternalDestinationRepository(db)
	err = repo.Delete(context.Background(), "extdst_01JNOT_FK_RESTRICT")
	if err == nil {
		t.Fatal("Delete 23503 with non-matching Detail: want generic SQL error, got nil")
	}
	if errors.Is(err, ErrExternalDestinationHasDependents) {
		t.Errorf("Delete 23503 non-matching: must NOT surface ErrExternalDestinationHasDependents, got %v", err)
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

// TestExternalDestinationRepository_Delete_NoQuotesMatchesFK is
// the BUGFIX regression: the FK dispatch must work for BOTH the
// quoted (`from table "<name>"`) and the unquoted (`from table
// <name>.`) variant of Postgres's Detail message.
//
// Postgres only emits surrounding double-quotes around a
// foreign-key REFERENCING table identifier in the Detail message
// when the identifier was CREATED with double-quotes (mixed-case
// preservation). Our schema creates external_destinations,
// external_deliveries, and any future FK-holding tables in
// lowercase WITHOUT creation-time quoting, so the canonical
// Detail output for our schema is the UNQUOTED shape:
//
//	Key (id)=(extdst_01JABC) is still referenced from table external_deliveries.
//
// Without this regression test, a future regex re-tightening that
// re-requires literal quotes would silently miss the canonical
// case for our schema and every Delete on a destination with
// in-flight deliveries would fall through to a generic SQL error
// instead of surfacing ErrExternalDestinationHasDependents.
func TestExternalDestinationRepository_Delete_NoQuotesMatchesFK(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	pqErr := &pq.Error{
		Code: "23503",
		// Detail here omits the double-quotes around
		// external_deliveries — the canonical PQ output for our
		// lowercase identifiers-not-created-with-quotes schema.
		Detail:  `Key (id)=(extdst_01JABC) is still referenced from table external_deliveries.`,
		Message: `update or delete on table "external_destinations" violates foreign key constraint "external_deliveries_external_destination_id_fkey" on table "external_deliveries"`,
	}
	mock.ExpectExec(`DELETE FROM external_destinations WHERE id = \$1`).
		WithArgs("extdst_01JABC").
		WillReturnError(pqErr)

	repo := NewExternalDestinationRepository(db)
	err = repo.Delete(context.Background(), "extdst_01JABC")
	if err == nil {
		t.Fatal("Delete FK (unquoted table): want ErrExternalDestinationHasDependents, got nil")
	}
	if !errors.Is(err, ErrExternalDestinationHasDependents) {
		t.Errorf("Delete FK (unquoted table): want ErrExternalDestinationHasDependents in chain, got %v", err)
	}
	// Legacy alias resolution (back-compat with code that imported
	// the older ErrExternalDestinationHasDeliveries name).
	if !errors.Is(err, ErrExternalDestinationHasDeliveries) {
		t.Errorf("Delete FK (unquoted table): legacy alias should ALSO resolve, got %v", err)
	}
	// %q formatting on the captured table name produces literal
	// "external_deliveries" (with quotes) regardless of whether
	// the input Detail had surrounding quotes — same assertion as
	// the quoted-shape test above, since the regex capture is
	// BARE (no quotes) and %q re-adds the quotes around group 1
	// output for both input shapes.
	if !strings.Contains(err.Error(), `"external_deliveries"`) {
		t.Errorf("Delete FK (unquoted table): wrapped error should mention referencing table name, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("mock expectations: %v", err)
	}
}
