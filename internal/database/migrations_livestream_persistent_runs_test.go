//go:build integration

package database

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/testutil/postgres"
)

func TestMigration093_LivestreamPersistentRunsConvergesAndIsIdempotent(t *testing.T) {
	db, cleanup := postgres.StartTestPostgres(t)
	defer cleanup()

	if err := RunMigrationsUpTo(db, 92); err != nil {
		t.Fatalf("RunMigrationsUpTo(92): %v", err)
	}

	seedLivestream093(t, db)

	if err := RunMigrationsUpTo(db, 93); err != nil {
		t.Fatalf("RunMigrationsUpTo(93): %v", err)
	}
	assertMigration093Schema(t, db)

	var accountID, generation, configVersion int64
	var broadcastID, streamID string
	if err := db.QueryRow(`
		SELECT platform_account_id, generation, configuration_version,
		       youtube_broadcast_id, youtube_stream_id
		  FROM livestream_runs WHERE id = 'run-093-a'
	`).Scan(&accountID, &generation, &configVersion, &broadcastID, &streamID); err != nil {
		t.Fatalf("scan migrated run: %v", err)
	}
	if accountID != 901 || generation != 1 || configVersion != 1 {
		t.Fatalf("unexpected migrated identity: account=%d generation=%d configuration_version=%d", accountID, generation, configVersion)
	}
	if broadcastID != "broadcast-legacy" || streamID != "stream-legacy" {
		t.Fatalf("legacy YouTube IDs were not copied to the run: broadcast=%q stream=%q", broadcastID, streamID)
	}

	// A second runner invocation must be a no-op and must preserve the
	// migration registry rather than attempting to replay 093.
	var before int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE filename = '093_livestreams_persistent_runs.sql'`).Scan(&before); err != nil {
		t.Fatalf("count migration 093 before rerun: %v", err)
	}
	if err := RunMigrationsUpTo(db, 93); err != nil {
		t.Fatalf("RunMigrationsUpTo(93) second pass: %v", err)
	}
	var after int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE filename = '093_livestreams_persistent_runs.sql'`).Scan(&after); err != nil {
		t.Fatalf("count migration 093 after rerun: %v", err)
	}
	if before != 1 || after != before {
		t.Fatalf("migration 093 registry rows changed on rerun: before=%d after=%d", before, after)
	}
}

func TestMigration093_ConstraintsRejectDuplicateGenerationAndActiveChannel(t *testing.T) {
	db, cleanup := postgres.StartTestPostgres(t)
	defer cleanup()

	if err := RunMigrationsUpTo(db, 92); err != nil {
		t.Fatalf("RunMigrationsUpTo(92): %v", err)
	}
	seedLivestream093(t, db)
	if err := RunMigrationsUpTo(db, 93); err != nil {
		t.Fatalf("RunMigrationsUpTo(93): %v", err)
	}

	// This is a second run of the same reusable configuration/generation.
	_, err := db.Exec(`
		INSERT INTO livestream_runs
		    (id, livestream_id, platform_account_id, generation,
		     configuration_version, status, attempt_count,
		     last_error_code, last_error_message)
		VALUES ('run-093-duplicate-generation', 'live-093', 901, 1, 1,
		        'completed', 0, '', '')
	`)
	if err == nil || !strings.Contains(err.Error(), "livestream_runs_generation_uq") {
		t.Fatalf("duplicate generation: want unique-index error, got %v", err)
	}

	// Two operational runs on one YouTube channel are forbidden even when
	// they belong to different livestream configurations.
	if _, err := db.Exec(`
		INSERT INTO livestreams
		    (id, workspace_id, platform_account_id, created_by, title,
		     privacy_status, playback_mode, schedule_type)
		VALUES ('live-093-b', 901, 901, 9001, 'Second config',
		        'unlisted', 'loop_continuous', 'manual')
	`); err != nil {
		t.Fatalf("insert second livestream: %v", err)
	}
	_, err = db.Exec(`
		INSERT INTO livestream_runs
		    (id, livestream_id, platform_account_id, generation,
		     configuration_version, status, attempt_count,
		     last_error_code, last_error_message)
		VALUES ('run-093-active-conflict', 'live-093-b', 901, 1, 1,
		        'live', 0, '', '')
	`)
	if err == nil || !strings.Contains(err.Error(), "livestream_one_active_run_per_channel") {
		t.Fatalf("active channel conflict: want unique-index error, got %v", err)
	}
}

func TestMigration093_RollsBackAllChangesWhenMigrationFails(t *testing.T) {
	db, cleanup := postgres.StartTestPostgres(t)
	defer cleanup()

	if err := RunMigrationsUpTo(db, 92); err != nil {
		t.Fatalf("RunMigrationsUpTo(92): %v", err)
	}

	files, err := loadMigrationFiles(93)
	if err != nil {
		t.Fatalf("loadMigrationFiles: %v", err)
	}
	var migration migrationFile
	for _, candidate := range files {
		if candidate.name == "093_livestreams_persistent_runs.sql" {
			migration = candidate
			break
		}
	}
	if migration.name == "" {
		t.Fatal("migration 093 not embedded")
	}

	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatalf("reserve DB connection: %v", err)
	}
	defer conn.Close()

	migration.body += "\nSELECT 1 / 0;\n"
	if err := applyMigration(context.Background(), conn, migration); err == nil {
		t.Fatal("failing migration unexpectedly committed")
	}

	var tableExists bool
	if err := db.QueryRow(`
		SELECT EXISTS (
		    SELECT 1 FROM information_schema.tables
		     WHERE table_schema = 'public' AND table_name = 'livestream_run_secrets'
		)
	`).Scan(&tableExists); err != nil {
		t.Fatalf("check rolled-back secrets table: %v", err)
	}
	if tableExists {
		t.Fatal("livestream_run_secrets survived the failed migration transaction")
	}

	var columnExists bool
	if err := db.QueryRow(`
		SELECT EXISTS (
		    SELECT 1 FROM information_schema.columns
		     WHERE table_schema = 'public' AND table_name = 'livestream_runs'
		       AND column_name = 'configuration_version'
		)
	`).Scan(&columnExists); err != nil {
		t.Fatalf("check rolled-back run column: %v", err)
	}
	if columnExists {
		t.Fatal("livestream_runs.configuration_version survived the failed migration transaction")
	}

	var recorded bool
	if err := db.QueryRow(`SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE filename = '093_livestreams_persistent_runs.sql')`).Scan(&recorded); err != nil {
		t.Fatalf("check rolled-back migration registry row: %v", err)
	}
	if recorded {
		t.Fatal("failed migration was recorded in schema_migrations")
	}
}

func seedLivestream093(t *testing.T, db *sql.DB) {
	t.Helper()

	if _, err := db.Exec(`INSERT INTO users (id, email, name) VALUES (9001, 'migration093@example.test', 'Migration 093')`); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO workspaces (id, name, owner_id) VALUES (901, 'Migration 093 workspace', 9001)`); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO platform_accounts (id, user_id, workspace_id, platform,
		    platform_user_id, username)
		VALUES (901, 9001, 901, 'youtube', 'yt-migration-093', 'migration093')
	`); err != nil {
		t.Fatalf("seed platform account: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO livestreams
		    (id, workspace_id, platform_account_id, created_by, title,
		     privacy_status, playback_mode, schedule_type,
		     youtube_broadcast_id, youtube_stream_id)
		VALUES ('live-093', 901, 901, 9001, 'Migration 093 live',
		        'unlisted', 'loop_continuous', 'manual',
		        'broadcast-legacy', 'stream-legacy')
	`); err != nil {
		t.Fatalf("seed livestream: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO livestream_runs
		    (id, livestream_id, status, created_at)
		VALUES ('run-093-a', 'live-093', 'live', '2026-01-01T00:00:00Z')
	`); err != nil {
		t.Fatalf("seed legacy run: %v", err)
	}
}

func assertMigration093Schema(t *testing.T, db *sql.DB) {
	t.Helper()

	for _, table := range []string{"livestream_runs", "livestream_media_items", "livestream_events", "livestream_run_secrets"} {
		var exists bool
		if err := db.QueryRow(`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = $1)`, table).Scan(&exists); err != nil {
			t.Fatalf("check table %s: %v", table, err)
		}
		if !exists {
			t.Fatalf("migration 093 did not create/preserve table %s", table)
		}
	}

	for _, column := range []string{"platform_account_id", "generation", "configuration_version", "worker_id", "lease_expires_at", "heartbeat_at", "last_frame_at", "live_at", "attempt_count", "last_error_code", "last_error_message", "updated_at"} {
		var exists bool
		if err := db.QueryRow(`SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'livestream_runs' AND column_name = $1)`, column).Scan(&exists); err != nil {
			t.Fatalf("check run column %s: %v", column, err)
		}
		if !exists {
			t.Fatalf("migration 093 missing run column %s", column)
		}
	}

	var mediaIDType string
	if err := db.QueryRow(`SELECT data_type FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'livestream_media_items' AND column_name = 'id'`).Scan(&mediaIDType); err != nil {
		t.Fatalf("check playlist id type: %v", err)
	}
	if mediaIDType != "text" {
		t.Fatalf("playlist id type = %q, want text", mediaIDType)
	}
	var mediaIDDefault sql.NullString
	if err := db.QueryRow(`SELECT column_default FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'livestream_media_items' AND column_name = 'id'`).Scan(&mediaIDDefault); err != nil {
		t.Fatalf("check playlist id default: %v", err)
	}
	if mediaIDDefault.Valid {
		t.Fatalf("playlist id retained a default after BIGSERIAL conversion: %q", mediaIDDefault.String)
	}

	var secretColumns int
	if err := db.QueryRow(`SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'livestream_run_secrets' AND column_name IN ('run_id', 'encrypted_ingest_url', 'encryption_key_id')`).Scan(&secretColumns); err != nil {
		t.Fatalf("check secret columns: %v", err)
	}
	if secretColumns != 3 {
		t.Fatalf("secret column count = %d, want 3", secretColumns)
	}

	var secretFK bool
	if err := db.QueryRow(`
		SELECT EXISTS (
		    SELECT 1 FROM pg_constraint
		     WHERE conname = 'livestream_run_secrets_run_id_fkey'
		       AND conrelid = 'livestream_run_secrets'::regclass
		)
	`).Scan(&secretFK); err != nil {
		t.Fatalf("check secret foreign key: %v", err)
	}
	if !secretFK {
		t.Fatal("livestream_run_secrets is missing its run foreign key")
	}

	for _, indexName := range []string{
		"livestream_runs_broadcast_uq",
		"livestream_runs_stream_uq",
		"livestream_one_active_run_per_channel",
	} {
		var exists bool
		if err := db.QueryRow(`SELECT EXISTS (SELECT 1 FROM pg_indexes WHERE schemaname = 'public' AND indexname = $1)`, indexName).Scan(&exists); err != nil {
			t.Fatalf("check index %s: %v", indexName, err)
		}
		if !exists {
			t.Fatalf("migration 093 missing index %s", indexName)
		}
	}
}
