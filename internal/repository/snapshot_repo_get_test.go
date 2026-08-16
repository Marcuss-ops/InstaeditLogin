package repository

import (
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

const getSnapshotQuery = `SELECT platform_account_id, resource_type, profile, statistics, status, content,
		        provider_etag, fetched_at, updated_at
		 FROM account_resource_snapshots
		 WHERE platform_account_id = $1`

func TestSnapshotRepository_GetSnapshot_AllowsNullProviderETag(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	defer db.Close()

	now := time.Now()
	mock.ExpectQuery(getSnapshotQuery).
		WithArgs(int64(365113)).
		WillReturnRows(sqlmock.NewRows([]string{
			"platform_account_id", "resource_type", "profile", "statistics", "status", "content",
			"provider_etag", "fetched_at", "updated_at",
		}).AddRow(
			int64(365113), "google_drive", []byte(`{"name":"Drive"}`), []byte(`{}`), []byte(`{}`), []byte(`{}`),
			nil, now, now,
		))

	snapshot, err := NewSnapshotRepository(db).GetSnapshot(365113)
	if err != nil {
		t.Fatalf("GetSnapshot with NULL provider_etag: %v", err)
	}
	if snapshot == nil {
		t.Fatal("GetSnapshot returned nil snapshot")
	}
	if snapshot.ProviderETag != "" {
		t.Fatalf("ProviderETag = %q, want empty string for NULL", snapshot.ProviderETag)
	}
	if snapshot.Profile["name"] != "Drive" {
		t.Fatalf("Profile = %#v, want decoded Drive profile", snapshot.Profile)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet sqlmock expectations: %v", err)
	}
}
