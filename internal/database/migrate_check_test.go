package database

import (
	"errors"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

// TestSchemaHealthy_NilDB confirms the panic-safe entry guard. The
// HTTP handler can race against DB-close on shutdown; nil DB must
// not panic.
func TestSchemaHealthy_NilDB(t *testing.T) {
	if err := SchemaHealthy(nil); err == nil {
		t.Fatal("nil DB: want error, got nil")
	}
}

// TestSchemaHealthy_AllCanariesPresent asserts the happy path:
// every canary resolves to a relation in pg_catalog. The mock
// returns true for each `SELECT to_regclass('public.' || $1) IS NOT
// NULL` so SchemaHealthy walks the canary list cleanly and returns
// nil.
func TestSchemaHealthy_AllCanariesPresent(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	for _, table := range CanaryTables {
		mock.ExpectQuery(`SELECT to_regclass\('public\.' \|\| \$1\) IS NOT NULL`).
			WithArgs(table).
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	}

	if err := SchemaHealthy(db); err != nil {
		t.Fatalf("SchemaHealthy: want nil, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("mock expectations: %v", err)
	}
}

// TestSchemaHealthy_MissingCanary asserts the failure path: a
// to_regclass that returns false (the table doesn't exist yet, or
// failed to apply) must produce an error naming the missing table.
// The /ready endpoint surfaces this string to the operator.
func TestSchemaHealthy_MissingCanary(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	// Pretend "tokens" is the first missing canary (lex order
	// matches the CanaryTables slice).
	const missing = "tokens"
	hitMissing := false
	for _, table := range CanaryTables {
		exists := table != missing
		if !exists {
			hitMissing = true
		}
		mock.ExpectQuery(`SELECT to_regclass\('public\.' \|\| \$1\) IS NOT NULL`).
			WithArgs(table).
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(exists))
		if hitMissing {
			break
		}
	}

	err = SchemaHealthy(db)
	if err == nil {
		t.Fatal("SchemaHealthy: want error for missing canary, got nil")
	}
	if !strings.Contains(err.Error(), missing) {
		t.Errorf("error must name the missing table %q, got: %v", missing, err)
	}
}

// TestSchemaHealthy_DBError confirms a DB query error gets wrapped
// with the canary name so the operator can attribute the failure
// to the right selector.
func TestSchemaHealthy_DBError(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery(`SELECT to_regclass\('public\.' \|\| \$1\) IS NOT NULL`).
		WithArgs("users").
		WillReturnError(errors.New("simulated connection drop"))

	err = SchemaHealthy(db)
	if err == nil {
		t.Fatal("SchemaHealthy: want error wrapping DB err, got nil")
	}
	if !strings.Contains(err.Error(), "users") || !strings.Contains(err.Error(), "simulated connection drop") {
		t.Errorf("error must include the canary name + underlying DB error, got: %v", err)
	}
}
