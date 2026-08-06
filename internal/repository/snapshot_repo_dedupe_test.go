package repository

import (
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestSnapshotRepository_MarkSnapshotsRefreshPending_DeduplicatesIDs(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	now := time.Now()
	mock.ExpectExec(`(?s)INSERT INTO account_resource_snapshots.*VALUES \(\$1, 'pending'.*\$2, 0\), \(\$3, 'pending'.*\$4, 0\).*ON CONFLICT \(platform_account_id\)`).
		WithArgs(int64(21), now, int64(22), now).
		WillReturnResult(sqlmock.NewResult(0, 2))

	if err := NewSnapshotRepository(db).MarkSnapshotsRefreshPending([]int64{21, 21, 22, 0, -1, 22}, now); err != nil {
		t.Fatalf("MarkSnapshotsRefreshPending: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sqlmock expectations: %v", err)
	}
}
