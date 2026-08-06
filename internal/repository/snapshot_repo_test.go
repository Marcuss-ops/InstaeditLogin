package repository

import (
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
)

// TestSnapshotRepository_ListSnapshotsByAccountIDs proves the batched
// snapshot read executes exactly ONE query for many account ids (the
// repository half of the aggregated /accounts list N+1 fix) and decodes
// the JSONB profile columns.
func TestSnapshotRepository_ListSnapshotsByAccountIDs(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	repo := NewSnapshotRepository(db)

	const listQuery = `SELECT platform_account_id, resource_type, profile, statistics, status, content,
	        provider_etag, fetched_at, updated_at
	 FROM account_resource_snapshots
	 WHERE platform_account_id = ANY($1)`

	now := time.Now()
	mock.ExpectQuery(listQuery).
		WithArgs(pq.Array([]int64{1, 2})).
		WillReturnRows(sqlmock.NewRows([]string{
			"platform_account_id", "resource_type", "profile", "statistics", "status", "content",
			"provider_etag", "fetched_at", "updated_at",
		}).
			AddRow(int64(1), "youtube_channel", `{"avatar_url":"https://avatars/one"}`, `{"subscribers":10}`, `{}`, `{}`, "", now, now).
			AddRow(int64(2), "youtube_channel", `{"avatar_url":"https://avatars/two"}`, `{}`, `{}`, `{}`, "", now.Add(-time.Hour), now.Add(-time.Hour)))

	snaps, err := repo.ListSnapshotsByAccountIDs([]int64{1, 2})
	if err != nil {
		t.Fatalf("ListSnapshotsByAccountIDs: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sqlmock expectations: %v", err)
	}
	if len(snaps) != 2 {
		t.Fatalf("snapshots: got %d, want 2", len(snaps))
	}
	if snaps[1] == nil || snaps[1].Profile["avatar_url"] != "https://avatars/one" {
		t.Fatalf("snapshot 1 avatar missing/wrong: %+v", snaps[1])
	}
	if snaps[2] == nil || snaps[2].FetchedAt.IsZero() {
		t.Fatalf("snapshot 2 not decoded: %+v", snaps[2])
	}
	if _, ok := snaps[99]; ok {
		t.Fatal("map must not contain ids absent from the result set")
	}
}

// TestSnapshotRepository_ListSnapshotsByAccountIDs_EmptyShortcut proves
// the empty-id shortcut performs zero queries.
func TestSnapshotRepository_ListSnapshotsByAccountIDs_EmptyShortcut(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	repo := NewSnapshotRepository(db)
	snaps, err := repo.ListSnapshotsByAccountIDs(nil)
	if err != nil {
		t.Fatalf("ListSnapshotsByAccountIDs(nil): %v", err)
	}
	if snaps == nil || len(snaps) != 0 {
		t.Fatalf("empty shortcut: got %v, want empty map", snaps)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unexpected SQL executed: %v", err)
	}
}
