package database

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

const testInstallationUUID = "00000000-0000-4000-8000-000000000001"

func TestVerifyInstallationIdentity_EmptyExpectedDisablesCheck(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	if err := VerifyInstallationIdentity(context.Background(), db, ""); err != nil {
		t.Fatalf("empty expected UUID: want nil, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unexpected database query: %v", err)
	}
}

func TestVerifyInstallationIdentity_MatchingUUID(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery(`SELECT installation_uuid::text FROM system_installation WHERE id = 1`).
		WillReturnRows(sqlmock.NewRows([]string{"installation_uuid"}).AddRow(testInstallationUUID))

	if err := VerifyInstallationIdentity(context.Background(), db, testInstallationUUID); err != nil {
		t.Fatalf("matching UUID: want nil, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("mock expectations: %v", err)
	}
}

func TestVerifyInstallationIdentity_MismatchIsClassifiableAndDoesNotExposeUUIDs(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	const actualUUID = "00000000-0000-4000-8000-000000000002"
	mock.ExpectQuery(`SELECT installation_uuid::text FROM system_installation WHERE id = 1`).
		WillReturnRows(sqlmock.NewRows([]string{"installation_uuid"}).AddRow(actualUUID))

	err = VerifyInstallationIdentity(context.Background(), db, testInstallationUUID)
	if !errors.Is(err, ErrDatabaseIdentityMismatch) {
		t.Fatalf("mismatch: want ErrDatabaseIdentityMismatch, got %v", err)
	}
	if strings.Contains(err.Error(), testInstallationUUID) || strings.Contains(err.Error(), actualUUID) {
		t.Fatalf("mismatch error must not expose UUID values: %v", err)
	}
	if !strings.Contains(err.Error(), "DATABASE_IDENTITY_MISMATCH") {
		t.Fatalf("mismatch error must contain stable class, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("mock expectations: %v", err)
	}
}

func TestVerifyInstallationIdentity_MissingIdentityFailsClosed(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery(`SELECT installation_uuid::text FROM system_installation WHERE id = 1`).
		WillReturnError(sql.ErrNoRows)

	err = VerifyInstallationIdentity(context.Background(), db, testInstallationUUID)
	if !errors.Is(err, ErrDatabaseIdentityMismatch) {
		t.Fatalf("missing identity: want ErrDatabaseIdentityMismatch, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("mock expectations: %v", err)
	}
}

func TestVerifyInstallationIdentity_InvalidExpectedFailsClosedWithoutQuery(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	err = VerifyInstallationIdentity(context.Background(), db, "not-a-uuid")
	if !errors.Is(err, ErrDatabaseIdentityMismatch) {
		t.Fatalf("invalid expected UUID: want ErrDatabaseIdentityMismatch, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unexpected database query: %v", err)
	}
}
