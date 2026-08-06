package repository

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

const refreshPendingUpsert = `INSERT INTO account_resource_snapshots
		    (platform_account_id, resource_type, profile, statistics, status, content,
		     fetched_at, updated_at, refresh_pending_at, refresh_attempts)
		 VALUES ($1, 'pending', '{}'::jsonb, '{}'::jsonb, '{}'::jsonb, '{}'::jsonb,
		         to_timestamp(0), NOW(), $2, 0)
		 ON CONFLICT (platform_account_id) DO UPDATE SET
		    refresh_pending_at = LEAST(
		        COALESCE(account_resource_snapshots.refresh_pending_at, EXCLUDED.refresh_pending_at),
		        EXCLUDED.refresh_pending_at
		    ),
		    refresh_claimed_until = CASE
		        WHEN account_resource_snapshots.refresh_claimed_until > NOW()
		        THEN account_resource_snapshots.refresh_claimed_until
		        ELSE NULL
		    END`

func TestSnapshotRepository_MarkSnapshotRefreshPending(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	now := time.Now()
	mock.ExpectExec(refreshPendingUpsert).
		WithArgs(int64(21), now).
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := NewSnapshotRepository(db).MarkSnapshotRefreshPending(21, now); err != nil {
		t.Fatalf("MarkSnapshotRefreshPending: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sqlmock expectations: %v", err)
	}
}

func TestSnapshotRepository_MarkSnapshotRefreshPending_CreatesMissingRow(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	now := time.Now()
	mock.ExpectExec(refreshPendingUpsert).
		WithArgs(int64(99), now).
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := NewSnapshotRepository(db).MarkSnapshotRefreshPending(99, now); err != nil {
		t.Fatalf("missing snapshot must be enqueueable: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sqlmock expectations: %v", err)
	}
}

func TestSnapshotRepository_MarkSnapshotRefreshPending_DBError(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	now := time.Now()
	mock.ExpectExec(refreshPendingUpsert).
		WithArgs(int64(21), now).
		WillReturnError(errors.New("connection lost"))

	err = NewSnapshotRepository(db).MarkSnapshotRefreshPending(21, now)
	if err == nil || !strings.Contains(err.Error(), "connection lost") {
		t.Fatalf("expected wrapped connection error, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sqlmock expectations: %v", err)
	}
}
