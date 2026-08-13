package repository

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestMarkPreparedUsesWaitingStateAndLeaseCAS(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	expect := mock.ExpectExec(regexp.QuoteMeta("UPDATE upload_jobs"))
	expect.WithArgs(int64(7), int64(42), "asset-7", "worker-1")
	expect.WillReturnResult(sqlmock.NewResult(0, 1))

	if err := NewUploadJobRepository(db).MarkPrepared(context.Background(), 7, "worker-1", 42, "asset-7"); err != nil {
		t.Fatalf("MarkPrepared: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestRetriesWithStagedAssetStayOutOfDriveIngest(t *testing.T) {
	if !strings.Contains(SQLClaimBatch, "asset_id IS NULL") {
		t.Fatal("ingest claim must exclude retry rows that already have a staged asset")
	}
	if !strings.Contains(SQLClaimBatchForPublish, "asset_id IS NOT NULL") {
		t.Fatal("publish claim must accept retry rows with a staged asset")
	}
}
