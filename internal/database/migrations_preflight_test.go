package database

import (
	"context"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestVerifyExistingInstallationIdentity_MissingTableAllowsEnrollment(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatalf("db.Conn: %v", err)
	}
	defer conn.Close()

	mock.ExpectQuery(`SELECT to_regclass\('public\.system_installation'\) IS NOT NULL`).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	if err := verifyExistingInstallationIdentity(context.Background(), conn, testInstallationUUID); err != nil {
		t.Fatalf("missing table: want enrollment allowed, got %v", err)
	}
}

func TestVerifyExistingInstallationIdentity_MismatchBlocksPreflight(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatalf("db.Conn: %v", err)
	}
	defer conn.Close()

	mock.ExpectQuery(`SELECT to_regclass\('public\.system_installation'\) IS NOT NULL`).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectQuery(`SELECT installation_uuid::text FROM system_installation WHERE id = 1`).
		WillReturnRows(sqlmock.NewRows([]string{"installation_uuid"}).AddRow("00000000-0000-4000-8000-000000000002"))

	err = verifyExistingInstallationIdentity(context.Background(), conn, testInstallationUUID)
	if err == nil || !strings.Contains(err.Error(), "DATABASE_IDENTITY_MISMATCH") {
		t.Fatalf("mismatch: want stable identity error, got %v", err)
	}
	if strings.Contains(err.Error(), testInstallationUUID) {
		t.Fatalf("preflight error leaked expected UUID: %v", err)
	}
}

func TestVerifyExistingInstallationIdentity_MissingRowBlocksPreflight(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatalf("db.Conn: %v", err)
	}
	defer conn.Close()

	mock.ExpectQuery(`SELECT to_regclass\('public\.system_installation'\) IS NOT NULL`).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectQuery(`SELECT installation_uuid::text FROM system_installation WHERE id = 1`).
		WillReturnRows(sqlmock.NewRows([]string{"installation_uuid"}))

	err = verifyExistingInstallationIdentity(context.Background(), conn, testInstallationUUID)
	if err == nil || !strings.Contains(err.Error(), "DATABASE_IDENTITY_MISMATCH") {
		t.Fatalf("missing row: want stable identity error, got %v", err)
	}
}
