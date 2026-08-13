//go:build integration

package database

import (
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/testutil/postgres"
)

// TestYouTubeTargetNativePublishAt_Migration verifies migration 126
// adds the native_publish_at column to youtube_target_publications so
// the publish worker can distinguish native-scheduled uploads (the
// Phase-1 videos.insert carried status.publishAt → YouTube owns the
// private→public transition and the videos.update must be skipped)
// from plain private uploads (NULL → the videos.update path stays).
func TestYouTubeTargetNativePublishAt_Migration(t *testing.T) {
	db, cleanup := postgres.StartTestPostgres(t)
	defer cleanup()

	if err := RunMigrations(db); err != nil {
		t.Fatalf("RunMigrations: %v", err)
	}

	var colType string
	err := db.QueryRow(`
		SELECT data_type FROM information_schema.columns
		WHERE table_schema = current_schema()
		  AND table_name = 'youtube_target_publications'
		  AND column_name = 'native_publish_at'
	`).Scan(&colType)
	if err != nil {
		t.Fatalf("native_publish_at column missing after migrations: %v", err)
	}
	if colType != "timestamp with time zone" {
		t.Errorf("native_publish_at type: want timestamp with time zone, got %q", colType)
	}

	// NULL is the default for existing rows — the fast path must stay
	// disabled (videos.update path) for anything uploaded pre-126.
	var nullable string
	if err := db.QueryRow(`
		SELECT is_nullable FROM information_schema.columns
		WHERE table_schema = current_schema()
		  AND table_name = 'youtube_target_publications'
		  AND column_name = 'native_publish_at'
	`).Scan(&nullable); err != nil {
		t.Fatalf("native_publish_at nullability lookup: %v", err)
	}
	if nullable != "YES" {
		t.Errorf("native_publish_at must be nullable (NULL = plain private upload), got %q", nullable)
	}
}
