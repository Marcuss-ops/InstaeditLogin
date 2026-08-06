package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestSnapshotRepository_ClaimPendingSnapshotRefreshes(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery("WITH candidates").
		WithArgs(25, "120.000000 seconds").
		WillReturnRows(sqlmock.NewRows([]string{
			"platform_account_id", "refresh_attempts", "platform", "platform_user_id", "username",
		}).AddRow(int64(21), 2, "youtube", "UC-pending", "Pending Chan"))

	repo := NewSnapshotRepository(db)
	rows, err := repo.ClaimPendingSnapshotRefreshes(context.Background(), 0, 0)
	if err != nil {
		t.Fatalf("ClaimPendingSnapshotRefreshes: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sqlmock expectations: %v", err)
	}
	if len(rows) != 1 || rows[0].PlatformAccountID != 21 || rows[0].Attempts != 2 {
		t.Fatalf("claimed rows: got %+v", rows)
	}
}

func TestSnapshotRepository_ClaimPendingSnapshotRefreshes_ExplicitLimitAndLease(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery("WITH candidates").
		WithArgs(5, "45.000000 seconds").
		WillReturnRows(sqlmock.NewRows([]string{
			"platform_account_id", "refresh_attempts", "platform", "platform_user_id", "username",
		}).AddRow(int64(1), 0, "youtube", "UC-one", "One"))

	repo := NewSnapshotRepository(db)
	rows, err := repo.ClaimPendingSnapshotRefreshes(context.Background(), 5, 45*time.Second)
	if err != nil {
		t.Fatalf("ClaimPendingSnapshotRefreshes: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sqlmock expectations: %v", err)
	}
	if len(rows) != 1 || rows[0].PlatformAccountID != 1 {
		t.Fatalf("claimed rows: got %+v", rows)
	}
}
