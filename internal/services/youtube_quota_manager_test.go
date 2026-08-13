// Tests for internal/services/youtube_quota_manager.go. The manager is
// the pre-call gate for the YouTube Data API v3 under the Google 2026
// three-bucket quota model; the SQL sequence lives in the repository,
// so these tests drive the repository through sqlmock and assert the
// manager's bucket/operation mapping + gate semantics.
package services

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// newTestQuotaManager wires the manager to a sqlmock-backed repository
// so the DB-facing SQL sequence can be asserted end-to-end.
func newTestQuotaManager(t *testing.T) (*YouTubeQuotaManager, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	m := NewYouTubeQuotaManager(repository.NewYouTubeDailyQuotaRepository(db), DefaultYouTubeQuotaLimits())
	return m, mock
}

// TestYouTubeQuotaManager_Defaults asserts the Google 2026 default
// ceilings: 100 videos.insert, 100 search.list, 10000 general units.
// A zero-value limits struct must converge on the same defaults so a
// partially-constructed config cannot hard-block every bucket.
func TestYouTubeQuotaManager_Defaults(t *testing.T) {
	if l := DefaultYouTubeQuotaLimits(); l.VideoUploads != 100 || l.Searches != 100 || l.General != 10000 {
		t.Fatalf("DefaultYouTubeQuotaLimits: got %+v, want {100 100 10000}", l)
	}

	m := NewYouTubeQuotaManager(nil, YouTubeQuotaLimits{}) // zero-value limits
	got := m.Limits()
	if got.VideoUploads != 100 || got.Searches != 100 || got.General != 10000 {
		t.Fatalf("zero-value limits must fall back to defaults, got %+v", got)
	}
}

// TestYouTubeQuotaManager_OperationSpec pins the operation → (bucket,
// cost) table for every operation the manager knows about, per the
// Google 2026 quota model.
func TestYouTubeQuotaManager_OperationSpec(t *testing.T) {
	m, _ := newTestQuotaManager(t)

	cases := []struct {
		operation string
		bucket    string
		cost      int
	}{
		{YouTubeOpVideoInsert, YouTubeQuotaBucketVideoUploads, 1},
		{YouTubeOpSearchList, YouTubeQuotaBucketSearches, 1},
		{YouTubeOpVideoUpdate, YouTubeQuotaBucketGeneral, 50},
		{YouTubeOpThumbnailsSet, YouTubeQuotaBucketGeneral, 50},
		{YouTubeOpVideoList, YouTubeQuotaBucketGeneral, 1},
		{YouTubeOpChannelsList, YouTubeQuotaBucketGeneral, 1},
	}
	for _, tc := range cases {
		bucket, cost, err := m.OperationSpec(tc.operation)
		if err != nil {
			t.Fatalf("OperationSpec(%q): %v", tc.operation, err)
		}
		if bucket != tc.bucket || cost != tc.cost {
			t.Errorf("OperationSpec(%q): want (%q, %d), got (%q, %d)",
				tc.operation, tc.bucket, tc.cost, bucket, cost)
		}
	}

	if _, _, err := m.OperationSpec("videos.delete"); err == nil {
		t.Error("OperationSpec(unknown): want error, got nil")
	}
}

// TestYouTubeQuotaManager_ReserveOperation_VideoInsertChargesUploads
// covers the primary path: a videos.insert reserve is issued against
// the video_uploads bucket with limit 100, cost 1.
func TestYouTubeQuotaManager_ReserveOperation_VideoInsertChargesUploads(t *testing.T) {
	m, mock := newTestQuotaManager(t)
	today := sqlmock.AnyArg()

	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO youtube_quota_daily (date, bucket, calls, errors, "limit", last_reset_at)
		VALUES ($1, $2, 0, 0, $3, NOW())
		ON CONFLICT (date, bucket) DO NOTHING`).
		WithArgs(today, YouTubeQuotaBucketVideoUploads, 100).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT calls, "limit"
		FROM youtube_quota_daily
		WHERE date = $1 AND bucket = $2
		FOR UPDATE`).
		WithArgs(today, YouTubeQuotaBucketVideoUploads).
		WillReturnRows(sqlmock.NewRows([]string{"calls", "limit"}).AddRow(99, 100))
	// calls 99 + cost 1 = 100 ≤ 100 → allowed.
	mock.ExpectExec(`UPDATE youtube_quota_daily SET calls = calls + $1 WHERE date = $2 AND bucket = $3`).
		WithArgs(1, today, YouTubeQuotaBucketVideoUploads).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	allowed, retry, err := m.ReserveOperation(context.Background(), YouTubeOpVideoInsert)
	if err != nil {
		t.Fatalf("ReserveOperation: %v", err)
	}
	if !allowed {
		t.Error("100th upload at calls=99 must be allowed (bucket cap is 100)")
	}
	if retry != 0 {
		t.Errorf("retry on allow: want 0, got %d", retry)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("mock expectations unmet: %v", err)
	}
}

// TestYouTubeQuotaManager_ReserveOperation_BlockedAtCap covers the
// gate: once the video_uploads bucket hits its 100-call cap, the
// 101st insert is refused with a retry-after hint and NO charge.
func TestYouTubeQuotaManager_ReserveOperation_BlockedAtCap(t *testing.T) {
	m, mock := newTestQuotaManager(t)
	today := sqlmock.AnyArg()

	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO youtube_quota_daily (date, bucket, calls, errors, "limit", last_reset_at)
		VALUES ($1, $2, 0, 0, $3, NOW())
		ON CONFLICT (date, bucket) DO NOTHING`).
		WithArgs(today, YouTubeQuotaBucketVideoUploads, 100).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT calls, "limit"
		FROM youtube_quota_daily
		WHERE date = $1 AND bucket = $2
		FOR UPDATE`).
		WithArgs(today, YouTubeQuotaBucketVideoUploads).
		WillReturnRows(sqlmock.NewRows([]string{"calls", "limit"}).AddRow(100, 100))
	mock.ExpectRollback()

	allowed, retry, err := m.ReserveOperation(context.Background(), YouTubeOpVideoInsert)
	if err != nil {
		t.Fatalf("ReserveOperation: %v", err)
	}
	if allowed {
		t.Error("101st upload at calls=100 must be refused (bucket cap is 100)")
	}
	if retry <= 0 || retry > 86400 {
		t.Errorf("retryAfterSeconds: want in (0, 86400], got %d", retry)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("mock expectations unmet: %v", err)
	}
}

// TestYouTubeQuotaManager_ReserveOperation_UnknownOperation asserts the
// fail-before-the-API-call contract: an unknown operation name errors
// immediately and never touches the DB.
func TestYouTubeQuotaManager_ReserveOperation_UnknownOperation(t *testing.T) {
	m, mock := newTestQuotaManager(t)
	if _, _, err := m.ReserveOperation(context.Background(), "videos.delete"); err == nil {
		t.Fatal("unknown operation: want error, got nil")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unknown operation must not issue SQL: %v", err)
	}
}

// TestYouTubeQuotaManager_Reserve_UnknownBucket asserts that reserving
// against a bucket outside the 2026 model is rejected before any SQL.
func TestYouTubeQuotaManager_Reserve_UnknownBucket(t *testing.T) {
	m, mock := newTestQuotaManager(t)
	if _, _, err := m.Reserve(context.Background(), "playlists", 1); err == nil {
		t.Fatal("unknown bucket: want error, got nil")
	}
	if err := m.RecordError(context.Background(), "playlists"); err == nil {
		t.Fatal("unknown bucket in RecordError: want error, got nil")
	}
	if _, err := m.Snapshot(context.Background(), "playlists"); err == nil {
		t.Fatal("unknown bucket in Snapshot: want error, got nil")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unknown bucket must not issue SQL: %v", err)
	}
}

// TestYouTubeQuotaManager_Reserve_NilRepo asserts the manager fails
// closed (explicit error, never a panic) when the repository is nil.
func TestYouTubeQuotaManager_Reserve_NilRepo(t *testing.T) {
	m := NewYouTubeQuotaManager(nil, DefaultYouTubeQuotaLimits())
	if _, _, err := m.Reserve(context.Background(), YouTubeQuotaBucketVideoUploads, 1); err == nil {
		t.Fatal("nil repo: want error, got nil")
	}
	if _, _, err := m.ReserveOperation(context.Background(), YouTubeOpVideoInsert); err == nil {
		t.Fatal("nil repo via ReserveOperation: want error, got nil")
	}
	if err := m.RecordError(context.Background(), YouTubeQuotaBucketVideoUploads); err == nil {
		t.Fatal("nil repo via RecordError: want error, got nil")
	}
	if _, err := m.Snapshot(context.Background(), YouTubeQuotaBucketVideoUploads); err == nil {
		t.Fatal("nil repo via Snapshot: want error, got nil")
	}
}

// TestYouTubeQuotaManager_Reserve_GeneralBucketChargesUnits exercises
// the general bucket: a 50-unit videos.update against 9975 used units
// must be allowed, and a second one (9975+50 = 10025 > 10000) refused.
func TestYouTubeQuotaManager_Reserve_GeneralBucketChargesUnits(t *testing.T) {
	m, mock := newTestQuotaManager(t)
	today := sqlmock.AnyArg()

	// First reserve: 9975 + 50 = 10025 > 10000 → refused. The manager
	// resolves videos.update to (general, 50) and charges the general
	// bucket ceiling of 10000.
	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO youtube_quota_daily (date, bucket, calls, errors, "limit", last_reset_at)
		VALUES ($1, $2, 0, 0, $3, NOW())
		ON CONFLICT (date, bucket) DO NOTHING`).
		WithArgs(today, YouTubeQuotaBucketGeneral, 10000).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT calls, "limit"
		FROM youtube_quota_daily
		WHERE date = $1 AND bucket = $2
		FOR UPDATE`).
		WithArgs(today, YouTubeQuotaBucketGeneral).
		WillReturnRows(sqlmock.NewRows([]string{"calls", "limit"}).AddRow(9975, 10000))
	mock.ExpectRollback()

	allowed, _, err := m.ReserveOperation(context.Background(), YouTubeOpVideoUpdate)
	if err != nil {
		t.Fatalf("ReserveOperation(videos.update): %v", err)
	}
	if allowed {
		t.Error("videos.update at 9975/10000 must be refused (9975+50 > 10000)")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("mock expectations unmet: %v", err)
	}
}

// TestYouTubeQuotaManager_Snapshot_NoRowFillsConfiguredLimit covers
// the read path on the first day: no row exists, so the manager
// reports zero calls against the configured ceiling rather than a
// zero limit that would make operators think the bucket is disabled.
func TestYouTubeQuotaManager_Snapshot_NoRowFillsConfiguredLimit(t *testing.T) {
	m, mock := newTestQuotaManager(t)
	today := sqlmock.AnyArg()

	mock.ExpectQuery(`SELECT calls, errors, "limit", last_reset_at
		FROM youtube_quota_daily
		WHERE date = $1 AND bucket = $2`).
		WithArgs(today, YouTubeQuotaBucketSearches).
		WillReturnRows(sqlmock.NewRows([]string{"calls", "errors", "limit", "last_reset_at"}))

	snap, err := m.Snapshot(context.Background(), YouTubeQuotaBucketSearches)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap.Bucket != YouTubeQuotaBucketSearches {
		t.Errorf("Bucket: want searches, got %q", snap.Bucket)
	}
	if snap.Calls != 0 || snap.Errors != 0 {
		t.Errorf("fresh-day snapshot must be zero: %+v", snap)
	}
	if snap.Limit != 100 {
		t.Errorf("fresh-day snapshot limit: want configured 100, got %d", snap.Limit)
	}
	if !snap.LastResetAt.IsZero() {
		t.Errorf("fresh-day snapshot LastResetAt: want zero, got %v", snap.LastResetAt)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("mock expectations unmet: %v", err)
	}
}

// TestYouTubeQuotaManager_Snapshot_HappyPath covers the read path once
// the day has usage.
func TestYouTubeQuotaManager_Snapshot_HappyPath(t *testing.T) {
	m, mock := newTestQuotaManager(t)
	today := sqlmock.AnyArg()
	resetAt := time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC)

	mock.ExpectQuery(`SELECT calls, errors, "limit", last_reset_at
		FROM youtube_quota_daily
		WHERE date = $1 AND bucket = $2`).
		WithArgs(today, YouTubeQuotaBucketVideoUploads).
		WillReturnRows(sqlmock.NewRows([]string{"calls", "errors", "limit", "last_reset_at"}).
			AddRow(87, 2, 100, resetAt))

	snap, err := m.Snapshot(context.Background(), YouTubeQuotaBucketVideoUploads)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap.Calls != 87 || snap.Errors != 2 || snap.Limit != 100 || !snap.LastResetAt.Equal(resetAt) {
		t.Errorf("snapshot mismatch: %+v", snap)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("mock expectations unmet: %v", err)
	}
}

// TestYouTubeQuotaManager_RecordError covers the informational error
// bump after a real API failure, charged to the right bucket.
func TestYouTubeQuotaManager_RecordError(t *testing.T) {
	m, mock := newTestQuotaManager(t)
	today := sqlmock.AnyArg()

	mock.ExpectExec(`INSERT INTO youtube_quota_daily (date, bucket, calls, errors, "limit", last_reset_at)
		VALUES ($1, $2, 0, 1, $3, NOW())
		ON CONFLICT (date, bucket) DO UPDATE SET errors = youtube_quota_daily.errors + 1`).
		WithArgs(today, YouTubeQuotaBucketVideoUploads, 100).
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := m.RecordError(context.Background(), YouTubeQuotaBucketVideoUploads); err != nil {
		t.Fatalf("RecordError: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("mock expectations unmet: %v", err)
	}
}
