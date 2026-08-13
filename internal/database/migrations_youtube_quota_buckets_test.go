//go:build integration

package database

import (
	"database/sql"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/testutil/postgres"
)

// TestYouTubeQuotaBuckets_FreshDatabase verifies migration 124 builds
// the (date, bucket)-keyed table on a fresh database. This covers the
// state where migration 059's body ran whole and its "+goose Down"
// DROP TABLE section dropped the table right after CREATE — 124 must
// recreate it with the bucket dimension.
func TestYouTubeQuotaBuckets_FreshDatabase(t *testing.T) {
	db, cleanup := postgres.StartTestPostgres(t)
	defer cleanup()

	if err := RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}
	assertBucketSchema(t, db)
	assertBucketUsage(t, db)
}

// TestYouTubeQuotaBuckets_LegacyTableUpgrade verifies migration 124
// upgrades a legacy single-bucket table (date-only PRIMARY KEY, no
// bucket column, "limit" default 300) instead of crashing. Existing
// rows must migrate to bucket='video_uploads' with their stored limit
// preserved (the repository never shrinks a stored ceiling).
func TestYouTubeQuotaBuckets_LegacyTableUpgrade(t *testing.T) {
	db, cleanup := postgres.StartTestPostgres(t)
	defer cleanup()

	if err := RunMigrationsUpTo(db, 123); err != nil {
		t.Fatalf("RunMigrationsUpTo(123): %v", err)
	}

	// Simulate a pre-124 deployment: drop whatever state migration 059
	// left behind (fresh-DB runners dropped the table via its Down
	// section; older runners may have kept it) and create the legacy
	// single-bucket table with a live row.
	if _, err := db.Exec(`DROP TABLE IF EXISTS youtube_quota_daily`); err != nil {
		t.Fatalf("drop table: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE youtube_quota_daily (
		date DATE PRIMARY KEY,
		calls INT NOT NULL DEFAULT 0,
		errors INT NOT NULL DEFAULT 0,
		"limit" INT NOT NULL DEFAULT 300,
		last_reset_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`); err != nil {
		t.Fatalf("create legacy table: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO youtube_quota_daily (date, calls, errors, "limit") VALUES (CURRENT_DATE, 12, 1, 300)`); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}

	if err := RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations (resume): %v", err)
	}
	assertBucketSchema(t, db)
	assertBucketUsage(t, db)

	// The legacy row must have migrated to the video_uploads bucket
	// with its stored limit preserved (never shrink).
	var bucket string
	var calls, limit int
	if err := db.QueryRow(`SELECT bucket, calls, "limit" FROM youtube_quota_daily WHERE date = CURRENT_DATE`).
		Scan(&bucket, &calls, &limit); err != nil {
		t.Fatalf("read migrated row: %v", err)
	}
	if bucket != "video_uploads" || calls != 12 || limit != 300 {
		t.Errorf("legacy row migration: got (bucket=%q calls=%d limit=%d), want (video_uploads, 12, 300)", bucket, calls, limit)
	}
}

// assertBucketSchema pins the post-124 schema contract: the primary key
// is (date, bucket) so each of the three 2026 buckets resets
// independently.
func assertBucketSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	var pkCols string
	if err := db.QueryRow(`
		SELECT string_agg(a.attname, ',' ORDER BY array_position(i.indkey::int2[], a.attnum))
		FROM pg_index i
		JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = ANY(i.indkey)
		WHERE i.indrelid = 'youtube_quota_daily'::regclass AND i.indisprimary
	`).Scan(&pkCols); err != nil {
		t.Fatalf("read pk: %v", err)
	}
	if pkCols != "date,bucket" {
		t.Errorf("youtube_quota_daily PK: want date,bucket, got %q", pkCols)
	}
}

// assertBucketUsage exercises the CHECK allow-list: the three 2026
// buckets insert fine, a bogus fourth bucket is rejected.
func assertBucketUsage(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, bucket := range []string{"video_uploads", "searches", "general"} {
		if _, err := db.Exec(`
			INSERT INTO youtube_quota_daily (date, bucket, calls, errors, "limit")
			VALUES (CURRENT_DATE, $1, 0, 0, 100)
			ON CONFLICT (date, bucket) DO NOTHING
		`, bucket); err != nil {
			t.Fatalf("insert %s bucket: %v", bucket, err)
		}
	}
	if _, err := db.Exec(`
		INSERT INTO youtube_quota_daily (date, bucket, calls, errors, "limit")
		VALUES (CURRENT_DATE, 'bogus', 0, 0, 100)
	`); err == nil {
		t.Error("CHECK constraint: bogus bucket must be rejected")
	}
}
