package repository

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

// markAllPendingQuery pins the critical clauses of the bulk enqueue SQL.
// A regression that drops the pa.user_id scoping (cross-tenant enqueue
// guard) or the deleted/disconnected exclusion must fail the match.
const markAllPendingQuery = `(?s)INSERT INTO account_resource_snapshots.*` +
	`SELECT pa[.]id.*` +
	`WHERE pa[.]user_id = [$]1.*` +
	`status NOT IN [(]'deleted', 'disconnected'[)].*` +
	`refresh_pending_at = EXCLUDED[.]refresh_pending_at.*` +
	`refresh_claimed_until = CASE.*` +
	`account_resource_snapshots[.]refresh_claimed_until > NOW[(][)].*` +
	`THEN account_resource_snapshots[.]refresh_claimed_until.*` +
	`ELSE NULL.*` +
	`END`

// TestSnapshotRepository_MarkAllSnapshotRefreshesPending pins the bulk
// "refresh all channels" enqueue: ONE statement marks every non-deleted
// account owned by the user and returns the affected row count (the
// number of channels the sweep worker will refresh in the background).
func TestSnapshotRepository_MarkAllSnapshotRefreshesPending(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	now := time.Now()
	mock.ExpectExec(markAllPendingQuery).
		WithArgs(int64(7), now).
		WillReturnResult(sqlmock.NewResult(0, 46))

	count, err := NewSnapshotRepository(db).MarkAllSnapshotRefreshesPending(7, now)
	if err != nil {
		t.Fatalf("MarkAllSnapshotRefreshesPending: %v", err)
	}
	if count != 46 {
		t.Fatalf("count: got %d, want 46", count)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sqlmock expectations: %v", err)
	}
}

func TestSnapshotRepository_MarkAllSnapshotRefreshesPending_DBError(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	now := time.Now()
	mock.ExpectExec(markAllPendingQuery).
		WithArgs(int64(7), now).
		WillReturnError(errors.New("connection lost"))

	_, err = NewSnapshotRepository(db).MarkAllSnapshotRefreshesPending(7, now)
	if err == nil || !strings.Contains(err.Error(), "connection lost") {
		t.Fatalf("expected wrapped connection error, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sqlmock expectations: %v", err)
	}
}
