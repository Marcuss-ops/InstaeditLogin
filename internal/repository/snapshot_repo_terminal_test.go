package repository

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestSnapshotRepository_MarkSnapshotRefreshTerminal_InvalidatesSharedGrant(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE oauth_connections").
		WithArgs(int64(21)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE platform_accounts").
		WithArgs(int64(21), "OAUTH_INVALID_GRANT", "OAuth grant requires reauthorization").
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec("UPDATE account_resource_snapshots").
		WithArgs(int64(21), "OAuth grant requires reauthorization").
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectCommit()

	if err := NewSnapshotRepository(db).MarkSnapshotRefreshTerminal(
		context.Background(), 21, "OAUTH_INVALID_GRANT", "OAuth grant requires reauthorization",
	); err != nil {
		t.Fatalf("MarkSnapshotRefreshTerminal: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sqlmock expectations: %v", err)
	}
}
