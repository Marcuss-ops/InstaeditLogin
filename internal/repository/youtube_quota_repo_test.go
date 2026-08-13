// Tests for internal/repository/youtube_quota_repo.go. Verifies the
// daily quota gate's SQL sequence + the gate decision matrix for the
// 2026 three-bucket model. The sqlmock dependency is already in the
// repository package's test surface; no new imports added here.
package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

const testBucket = YouTubeQuotaBucketVideoUploads

// TestYouTubeDailyQuota_ReserveQuota_AllowsUnderLimit covers the
// happy path: calls < limit, the repo increments calls by cost,
// commits, and returns (true, 0, nil).
func TestYouTubeDailyQuota_ReserveQuota_AllowsUnderLimit(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	repo := NewYouTubeDailyQuotaRepository(db)

	// The repo computes today := time.Now().UTC().Truncate(24h). Bind
	// it as an argument matcher so we don't care about the exact wall
	// clock during the test.
	todayPattern := sqlmock.AnyArg()

	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO youtube_quota_daily (date, bucket, calls, errors, "limit", last_reset_at)
		VALUES ($1, $2, 0, 0, $3, NOW())
		ON CONFLICT (date, bucket) DO NOTHING`).
		WithArgs(todayPattern, testBucket, 100).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT calls, "limit"
		FROM youtube_quota_daily
		WHERE date = $1 AND bucket = $2
		FOR UPDATE`).
		WithArgs(todayPattern, testBucket).
		WillReturnRows(sqlmock.NewRows([]string{"calls", "limit"}).AddRow(50, 100))
	mock.ExpectExec(`UPDATE youtube_quota_daily SET calls = calls + $1 WHERE date = $2 AND bucket = $3`).
		WithArgs(1, todayPattern, testBucket).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	allowed, retry, err := repo.ReserveQuota(context.Background(), testBucket, 1, 100)
	if err != nil {
		t.Fatalf("ReserveQuota: %v", err)
	}
	if !allowed {
		t.Errorf("allowed = false; want true (calls=50 < limit=100)")
	}
	if retry != 0 {
		t.Errorf("retryAfterSeconds = %d; want 0 on allow", retry)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("mock expectations unmet: %v", err)
	}
}

// TestYouTubeDailyQuota_ReserveQuota_BlocksAtLimit covers the gate's
// refusal path: calls+cost > limit, the repo returns (false,
// retry_seconds, nil) WITHOUT incrementing calls and WITHOUT
// committing. Cost > 1 (e.g. a 50-unit videos.update) is exercised so
// a future bucket whose unit costs diverge from 1 is covered.
func TestYouTubeDailyQuota_ReserveQuota_BlocksAtLimit(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	repo := NewYouTubeDailyQuotaRepository(db)

	todayPattern := sqlmock.AnyArg()

	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO youtube_quota_daily (date, bucket, calls, errors, "limit", last_reset_at)
		VALUES ($1, $2, 0, 0, $3, NOW())
		ON CONFLICT (date, bucket) DO NOTHING`).
		WithArgs(todayPattern, testBucket, 100).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT calls, "limit"
		FROM youtube_quota_daily
		WHERE date = $1 AND bucket = $2
		FOR UPDATE`).
		WithArgs(todayPattern, testBucket).
		WillReturnRows(sqlmock.NewRows([]string{"calls", "limit"}).AddRow(99, 100))
	// 99 calls + 50-unit cost = 149 > 100 → blocked, no increment, tx
	// rolls back via defer.
	mock.ExpectRollback()

	allowed, retry, err := repo.ReserveQuota(context.Background(), testBucket, 50, 100)
	if err != nil {
		t.Fatalf("ReserveQuota: %v", err)
	}
	if allowed {
		t.Errorf("allowed = true; want false (calls=99, cost=50 > limit=100)")
	}
	// retryAfterSeconds is seconds-to-next-UTC-midnight; the exact
	// value depends on the wall clock at test time, so just assert
	// it's in (0, 86400].
	if retry <= 0 || retry > 86400 {
		t.Errorf("retryAfterSeconds = %d; want in (0, 86400]", retry)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("mock expectations unmet: %v", err)
	}
}

// TestYouTubeDailyQuota_ReserveQuota_HonorsInboundBump covers the
// "operator raised the bucket ceiling mid-day" path: a stored limit
// of 200 is bumped to 500 by the inbound config.
func TestYouTubeDailyQuota_ReserveQuota_HonorsInboundBump(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	repo := NewYouTubeDailyQuotaRepository(db)

	todayPattern := sqlmock.AnyArg()

	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO youtube_quota_daily (date, bucket, calls, errors, "limit", last_reset_at)
		VALUES ($1, $2, 0, 0, $3, NOW())
		ON CONFLICT (date, bucket) DO NOTHING`).
		WithArgs(todayPattern, testBucket, 500).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT calls, "limit"
		FROM youtube_quota_daily
		WHERE date = $1 AND bucket = $2
		FOR UPDATE`).
		WithArgs(todayPattern, testBucket).
		WillReturnRows(sqlmock.NewRows([]string{"calls", "limit"}).AddRow(100, 200))
	mock.ExpectExec(`UPDATE youtube_quota_daily SET "limit" = $1 WHERE date = $2 AND bucket = $3`).
		WithArgs(500, todayPattern, testBucket).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(`UPDATE youtube_quota_daily SET calls = calls + $1 WHERE date = $2 AND bucket = $3`).
		WithArgs(1, todayPattern, testBucket).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	allowed, retry, err := repo.ReserveQuota(context.Background(), testBucket, 1, 500)
	if err != nil {
		t.Fatalf("ReserveQuota: %v", err)
	}
	if !allowed {
		t.Errorf("allowed = false; want true (calls=100, effective limit=500)")
	}
	if retry != 0 {
		t.Errorf("retryAfterSeconds on bump-allow: want 0, got %d", retry)
	}
}

// TestYouTubeDailyQuota_ReserveQuota_NeverShrinksLimit covers the
// inverse case: the operator accidentally set a SMALLER limit than the
// stored value (maybe a typo). The repo refuses to lower the stored
// limit so the deliberate-downgrade constraint isn't silently relaxed.
func TestYouTubeDailyQuota_ReserveQuota_NeverShrinksLimit(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	repo := NewYouTubeDailyQuotaRepository(db)

	todayPattern := sqlmock.AnyArg()

	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO youtube_quota_daily (date, bucket, calls, errors, "limit", last_reset_at)
		VALUES ($1, $2, 0, 0, $3, NOW())
		ON CONFLICT (date, bucket) DO NOTHING`).
		WithArgs(todayPattern, testBucket, 100).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`SELECT calls, "limit"
		FROM youtube_quota_daily
		WHERE date = $1 AND bucket = $2
		FOR UPDATE`).
		WithArgs(todayPattern, testBucket).
		WillReturnRows(sqlmock.NewRows([]string{"calls", "limit"}).AddRow(150, 300))
	// No UPDATE limit SQL — effective limit stays at 300.
	mock.ExpectExec(`UPDATE youtube_quota_daily SET calls = calls + $1 WHERE date = $2 AND bucket = $3`).
		WithArgs(1, todayPattern, testBucket).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	allowed, _, err := repo.ReserveQuota(context.Background(), testBucket, 1, 100)
	if err != nil {
		t.Fatalf("ReserveQuota: %v", err)
	}
	if !allowed {
		t.Errorf("allowed = false; want true (calls=150, effective limit remains 300)")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("mock expectations unmet: %v", err)
	}
}

// TestYouTubeDailyQuota_ReserveQuota_RejectsInvalidInput guards the
// config-side error paths: a non-positive defaultLimit, a non-positive
// cost, or an empty bucket are operator errors, NOT runtime conditions,
// so we surface them immediately.
func TestYouTubeDailyQuota_ReserveQuota_RejectsInvalidInput(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	repo := NewYouTubeDailyQuotaRepository(db)

	cases := []struct {
		bucket string
		cost   int
		limit  int
	}{
		{testBucket, 0, 100},
		{testBucket, -1, 100},
		{testBucket, 1, 0},
		{testBucket, 1, -1000},
		{"", 1, 100},
	}
	for _, tc := range cases {
		_, _, err := repo.ReserveQuota(context.Background(), tc.bucket, tc.cost, tc.limit)
		if err == nil {
			t.Errorf("bucket=%q cost=%d limit=%d: want error, got nil", tc.bucket, tc.cost, tc.limit)
		}
		if err != nil && err.Error() == "" {
			t.Errorf("bucket=%q cost=%d limit=%d: empty error message", tc.bucket, tc.cost, tc.limit)
		}
	}
}

// TestYouTubeDailyQuota_RecordError covers the daily error-bump path.
// RecordError should be a single-statement UPSERT, no tx needed.
func TestYouTubeDailyQuota_RecordError(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	repo := NewYouTubeDailyQuotaRepository(db)

	todayPattern := sqlmock.AnyArg()

	mock.ExpectExec(`INSERT INTO youtube_quota_daily (date, bucket, calls, errors, "limit", last_reset_at)
		VALUES ($1, $2, 0, 1, $3, NOW())
		ON CONFLICT (date, bucket) DO UPDATE SET errors = youtube_quota_daily.errors + 1`).
		WithArgs(todayPattern, testBucket, 100).
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := repo.RecordError(context.Background(), testBucket, 100); err != nil {
		t.Fatalf("RecordError: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("mock expectations unmet: %v", err)
	}
}

// TestYouTubeDailyQuota_RecordError_RejectsEmptyBucket guards the
// bucket argument: an empty bucket is an operator error surfaced
// immediately, before any SQL is issued.
func TestYouTubeDailyQuota_RecordError_RejectsEmptyBucket(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	repo := NewYouTubeDailyQuotaRepository(db)

	if err := repo.RecordError(context.Background(), "", 100); err == nil {
		t.Fatal("RecordError with empty bucket: want error, got nil")
	}
}

// TestYouTubeDailyQuota_GetSnapshot_NoRow covers the read-only path:
// when no row exists for today, GetSnapshot returns a zero snapshot
// WITHOUT erroring. The /admin/health endpoint relies on this so the
// dashboard works on the very first morning of a deploy.
func TestYouTubeDailyQuota_GetSnapshot_NoRow(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	repo := NewYouTubeDailyQuotaRepository(db)

	todayPattern := sqlmock.AnyArg()

	mock.ExpectQuery(`SELECT calls, errors, "limit", last_reset_at
		FROM youtube_quota_daily
		WHERE date = $1 AND bucket = $2`).
		WithArgs(todayPattern, testBucket).
		WillReturnRows(sqlmock.NewRows([]string{"calls", "errors", "limit", "last_reset_at"})) // empty set

	calls, errors, limit, lastReset, err := repo.GetSnapshot(context.Background(), testBucket)
	if err != nil {
		t.Fatalf("GetSnapshot: %v", err)
	}
	if calls != 0 || errors != 0 || limit != 0 || !lastReset.IsZero() {
		t.Errorf("zero snapshot violated: calls=%d errors=%d limit=%d lastReset=%v", calls, errors, limit, lastReset)
	}
}

// TestYouTubeDailyQuota_GetSnapshot_HappyPath covers the read-only
// path when the today's row exists.
func TestYouTubeDailyQuota_GetSnapshot_HappyPath(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	repo := NewYouTubeDailyQuotaRepository(db)

	todayPattern := sqlmock.AnyArg()
	resetAt := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)

	mock.ExpectQuery(`SELECT calls, errors, "limit", last_reset_at
		FROM youtube_quota_daily
		WHERE date = $1 AND bucket = $2`).
		WithArgs(todayPattern, testBucket).
		WillReturnRows(sqlmock.NewRows([]string{"calls", "errors", "limit", "last_reset_at"}).
			AddRow(187, 3, 100, resetAt))

	calls, errs, limit, last, err := repo.GetSnapshot(context.Background(), testBucket)
	if err != nil {
		t.Fatalf("GetSnapshot: %v", err)
	}
	if calls != 187 || errs != 3 || limit != 100 || !last.Equal(resetAt) {
		t.Errorf("snapshot mismatch: calls=%d errs=%d limit=%d lastReset=%v", calls, errs, limit, last)
	}
}

// TestYouTubeDailyQuota_GetSnapshot_RejectsEmptyBucket guards the
// bucket argument on the read path as well.
func TestYouTubeDailyQuota_GetSnapshot_RejectsEmptyBucket(t *testing.T) {
	db, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()
	repo := NewYouTubeDailyQuotaRepository(db)

	if _, _, _, _, err := repo.GetSnapshot(context.Background(), ""); err == nil {
		t.Fatal("GetSnapshot with empty bucket: want error, got nil")
	}
}
