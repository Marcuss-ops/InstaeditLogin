package repository

import (
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

// joinedSelect must mirror the raw SQL literal in
// platform_account_repo.go's ListPlatformAccountsWithSnapshotsByUser
// byte-for-byte (QueryMatcherEqual compares exact strings).
const joinedSelect = `SELECT pa.id, pa.user_id, pa.platform, pa.platform_user_id, pa.username, pa.status, pa.connected_at,
	        pa.last_validated_at, pa.last_refresh_at, pa.reauth_required_at,
	        COALESCE(pa.last_error_code, '') AS last_error_code,
	        COALESCE(pa.last_error_message, '') AS last_error_message,
	        pa.metadata, pa.created_at, pa.updated_at,
	        ars.platform_account_id, ars.resource_type, ars.profile, ars.statistics, ars.status, ars.content,
	        ars.provider_etag, ars.fetched_at, ars.updated_at
	 FROM platform_accounts pa
	 LEFT JOIN account_resource_snapshots ars ON ars.platform_account_id = pa.id`

var joinedCols = []string{
	"pa.id", "pa.user_id", "pa.platform", "pa.platform_user_id", "pa.username", "pa.status", "pa.connected_at",
	"pa.last_validated_at", "pa.last_refresh_at", "pa.reauth_required_at",
	"last_error_code", "last_error_message", "pa.metadata", "pa.created_at", "pa.updated_at",
	"ars.platform_account_id", "ars.resource_type", "ars.profile", "ars.statistics", "ars.status", "ars.content",
	"ars.provider_etag", "ars.fetched_at", "ars.updated_at",
}

// TestUserRepository_ListPlatformAccountsWithSnapshotsByUser proves the
// aggregated list executes exactly ONE query joining platform_accounts
// with account_resource_snapshots, decoding the snapshot profile for
// accounts that have a row and leaving Snapshot == nil for those that
// don't (the N+1 fan-out is replaced by a single LEFT JOIN).
func TestUserRepository_ListPlatformAccountsWithSnapshotsByUser(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	now := time.Now()
	mock.ExpectQuery(joinedSelect + ` WHERE pa.user_id = $1 ORDER BY pa.created_at DESC`).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows(joinedCols).
			AddRow(
				int64(1), int64(7), "youtube", "UC-one", "one", "active", now,
				nil, nil, nil, "", "", `{"language":"it"}`, now, now,
				int64(1), "youtube_channel", `{"avatar_url":"https://avatars/one"}`, `{"subscribers":10}`, `{}`, `{}`, "", now, now,
			).
			AddRow(
				int64(2), int64(7), "youtube", "UC-two", "two", "active", now,
				nil, nil, nil, "", "", `{}`, now, now,
				nil, nil, nil, nil, nil, nil, nil, nil, nil,
			))

	repo := NewUserRepository(db)
	rows, err := repo.ListPlatformAccountsWithSnapshotsByUser(7, "")
	if err != nil {
		t.Fatalf("ListPlatformAccountsWithSnapshotsByUser: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sqlmock expectations: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("rows: got %d, want 2", len(rows))
	}
	if rows[0].Snapshot == nil || rows[0].Snapshot.Profile["avatar_url"] != "https://avatars/one" {
		t.Fatalf("account 1 snapshot missing/wrong: %+v", rows[0].Snapshot)
	}
	if rows[0].Account.Metadata["language"] != "it" {
		t.Fatalf("account 1 metadata not decoded: %+v", rows[0].Account.Metadata)
	}
	if rows[1].Snapshot != nil {
		t.Fatalf("account 2 must have nil snapshot (no LEFT JOIN row), got %+v", rows[1].Snapshot)
	}
	if rows[1].Account.ID != 2 {
		t.Fatalf("account 2 id: got %d", rows[1].Account.ID)
	}
}

// TestUserRepository_ListPlatformAccountsWithSnapshotsByUser_PlatformFilter
// proves the platform-scoped variant keeps the join and passes both args.
func TestUserRepository_ListPlatformAccountsWithSnapshotsByUser_PlatformFilter(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	now := time.Now()
	mock.ExpectQuery(joinedSelect+` WHERE pa.user_id = $1 AND pa.platform = $2 ORDER BY pa.created_at DESC`).
		WithArgs(int64(7), "youtube").
		WillReturnRows(sqlmock.NewRows(joinedCols).
			AddRow(
				int64(5), int64(7), "youtube", "UC-five", "five", "active", now,
				nil, nil, nil, "", "", `{}`, now, now,
				nil, nil, nil, nil, nil, nil, nil, nil, nil,
			))

	repo := NewUserRepository(db)
	rows, err := repo.ListPlatformAccountsWithSnapshotsByUser(7, "youtube")
	if err != nil {
		t.Fatalf("ListPlatformAccountsWithSnapshotsByUser: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sqlmock expectations: %v", err)
	}
	if len(rows) != 1 || rows[0].Account.ID != 5 {
		t.Fatalf("rows: got %+v", rows)
	}
}

// TestUserRepository_ListPlatformAccountsWithSnapshotsByUser_Empty proves
// the no-rows case returns an empty slice without error.
func TestUserRepository_ListPlatformAccountsWithSnapshotsByUser_Empty(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery(joinedSelect + ` WHERE pa.user_id = $1 ORDER BY pa.created_at DESC`).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows(joinedCols))

	repo := NewUserRepository(db)
	rows, err := repo.ListPlatformAccountsWithSnapshotsByUser(7, "")
	if err != nil {
		t.Fatalf("ListPlatformAccountsWithSnapshotsByUser: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sqlmock expectations: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("rows: got %d, want 0", len(rows))
	}
}
