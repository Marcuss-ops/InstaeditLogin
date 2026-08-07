//go:build integration

package database

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/testutil/postgres"
)

func TestMigration112And114_VeloxProjectBridgeSchemaAndConstraints(t *testing.T) {
	db, cleanup := postgres.StartTestPostgres(t)
	defer cleanup()

	if err := RunMigrationsUpTo(db, 111); err != nil {
		t.Fatalf("RunMigrationsUpTo(111): %v", err)
	}
	seedVeloxProjectBridge112(t, db)
	if err := RunMigrationsUpTo(db, 112); err != nil {
		t.Fatalf("RunMigrationsUpTo(112): %v", err)
	}
	if err := RunMigrationsUpTo(db, 112); err != nil {
		t.Fatalf("RunMigrationsUpTo(112) second pass: %v", err)
	}

	var exists bool
	if err := db.QueryRow(`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema='public' AND table_name='velox_project_bridges')`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Fatal("missing table velox_project_bridges")
	}
	if _, err := db.Exec(`INSERT INTO velox_project_bridges (project_id, workspace_id, velox_project_id) VALUES ('bridge-112-a', 11201, 'vx-112-a')`); err != nil {
		t.Fatalf("insert autonomous bridge: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO thumbnail_projects (id, workspace_id, created_by, name, canvas_width, canvas_height) VALUES ('bridge-112-cross', 11201, 11201, 'Bridge cross-context project', 1280, 720), ('bridge-112-platform', 11201, 11201, 'Bridge platform-context project', 1280, 720), ('bridge-112-legacy', 11201, 11201, 'Bridge populated legacy context', 1280, 720)`); err != nil {
		t.Fatalf("insert cross-context projects: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO velox_project_bridges (project_id, workspace_id, velox_project_id, platform, platform_account_id, channel_id, video_id, language) VALUES ('bridge-112-legacy', 11201, 'vx-112-legacy', 'youtube', 11201, 'channel-112-legacy', 'video-112-legacy', 'it')`); err != nil {
		t.Fatalf("insert populated legacy bridge: %v", err)
	}
	var populatedLegacyRows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM velox_project_bridges WHERE platform = 'youtube' AND platform_account_id = 11201 AND channel_id = 'channel-112-legacy' AND video_id = 'video-112-legacy' AND language = 'it'`).Scan(&populatedLegacyRows); err != nil {
		t.Fatal(err)
	}
	if populatedLegacyRows != 1 {
		t.Fatalf("expected one populated legacy bridge row before cleanup, got %d", populatedLegacyRows)
	}
	assertBridgeConstraintRejects(t, db, `INSERT INTO velox_project_bridges (project_id, workspace_id, velox_project_id) VALUES ('bridge-112-a', 11201, 'vx-112-b')`, "velox_project_bridges_pkey")
	assertBridgeConstraintRejects(t, db, `INSERT INTO velox_project_bridges (project_id, workspace_id, velox_project_id) VALUES ('bridge-112-b', 11201, 'vx-112-a')`, "velox_project_bridges_velox_project_uq")

	var groupColumns int
	if err := db.QueryRow(`SELECT COUNT(*) FROM information_schema.columns WHERE table_name='velox_project_bridges' AND column_name IN ('group_id', 'channel_ids', 'member_ids')`).Scan(&groupColumns); err != nil {
		t.Fatal(err)
	}
	if groupColumns != 0 {
		t.Fatalf("bridge contains forbidden duplicated ownership columns: %d", groupColumns)
	}

	if err := RunMigrationsUpTo(db, 116); err != nil {
		t.Fatalf("RunMigrationsUpTo(116): %v", err)
	}
	if err := RunMigrationsUpTo(db, 116); err != nil {
		t.Fatalf("RunMigrationsUpTo(116) second pass: %v", err)
	}
	for _, migration := range []string{
		"112_velox_project_bridges.sql",
		"114_velox_project_bridge_run_id.sql",
		"115_velox_project_bridge_editor_metadata.sql",
		"116_velox_project_bridge_minimal.sql",
	} {
		var recorded bool
		if err := db.QueryRow(`SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE filename = $1)`, migration).Scan(&recorded); err != nil {
			t.Fatalf("check recorded migration %s: %v", migration, err)
		}
		if !recorded {
			t.Fatalf("bridge migration %s was not recorded", migration)
		}
	}
	var runIDColumnExists bool
	if err := db.QueryRow(`SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='velox_project_bridges' AND column_name='migration_run_id')`).Scan(&runIDColumnExists); err != nil {
		t.Fatal(err)
	}
	if !runIDColumnExists {
		t.Fatal("missing migration_run_id marker")
	}
	var legacyColumns int
	if err := db.QueryRow(`SELECT COUNT(*) FROM information_schema.columns WHERE table_name='velox_project_bridges' AND column_name IN ('velox_project_id', 'platform', 'platform_account_id', 'channel_id', 'video_id', 'language')`).Scan(&legacyColumns); err != nil {
		t.Fatal(err)
	}
	if legacyColumns != 0 {
		t.Fatalf("legacy context columns remain after migration 116: %d", legacyColumns)
	}
	var legacyConstraints int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pg_constraint WHERE conrelid = 'velox_project_bridges'::regclass AND (lower(conname) LIKE '%channel%' OR lower(conname) LIKE '%platform_account%' OR lower(conname) LIKE '%group%')`).Scan(&legacyConstraints); err != nil {
		t.Fatal(err)
	}
	if legacyConstraints != 0 {
		t.Fatalf("legacy bridge channel/group constraints remain after migration 116: %d", legacyConstraints)
	}
	var legacyIndexes int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pg_indexes WHERE schemaname = 'public' AND tablename = 'velox_project_bridges' AND (lower(indexname) LIKE '%channel%' OR lower(indexdef) LIKE '%platform_account%' OR lower(indexdef) LIKE '%channel%' OR lower(indexdef) LIKE '%group%')`).Scan(&legacyIndexes); err != nil {
		t.Fatal(err)
	}
	if legacyIndexes != 0 {
		t.Fatalf("legacy bridge channel/group indexes remain after migration 116: %d", legacyIndexes)
	}
	var forbiddenBridgeColumns int
	if err := db.QueryRow(`SELECT COUNT(*) FROM information_schema.columns WHERE table_schema='public' AND table_name='velox_project_bridges' AND column_name IN ('group_id', 'group_ids', 'channel_ids', 'member_ids', 'velox_group_id', 'velox_channel_group_id')`).Scan(&forbiddenBridgeColumns); err != nil {
		t.Fatal(err)
	}
	if forbiddenBridgeColumns != 0 {
		t.Fatalf("group/channel ownership columns remain in bridge after migration 116: %d", forbiddenBridgeColumns)
	}
	var metadataColumns int
	if err := db.QueryRow(`SELECT COUNT(*) FROM information_schema.columns WHERE table_name='velox_project_bridges' AND column_name IN ('editor_provider', 'editor_status', 'last_editor_sync_at')`).Scan(&metadataColumns); err != nil {
		t.Fatal(err)
	}
	if metadataColumns != 3 {
		t.Fatalf("minimal bridge metadata columns missing: %d", metadataColumns)
	}
	var cleanedLegacyRowCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM velox_project_bridges WHERE project_id = 'bridge-112-legacy' AND external_project_id = 'vx-112-legacy'`).Scan(&cleanedLegacyRowCount); err != nil {
		t.Fatal(err)
	}
	if cleanedLegacyRowCount != 1 {
		t.Fatalf("legacy bridge mapping was not preserved after cleanup: %d", cleanedLegacyRowCount)
	}
	var authoritativeChannelRows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM workspace_channels WHERE workspace_id = 11201 AND platform_account_id = 11201`).Scan(&authoritativeChannelRows); err != nil {
		t.Fatal(err)
	}
	if authoritativeChannelRows != 1 {
		t.Fatalf("legacy cleanup changed InstaEdit channel ownership data: %d", authoritativeChannelRows)
	}
	if _, err := db.Exec(`INSERT INTO thumbnail_projects (id, workspace_id, created_by, name, canvas_width, canvas_height) VALUES ('bridge-112-run', 11201, 11201, 'Bridge run marker project', 1280, 720)`); err != nil {
		t.Fatalf("seed bridge run marker project: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO velox_project_bridges (project_id, workspace_id, external_project_id, migration_run_id) VALUES ('bridge-112-run', 11201, 'vx-112-run', 'bridge-run-1')`); err != nil {
		t.Fatalf("insert bridge run marker: %v", err)
	}
	assertBridgeConstraintRejects(t, db, `INSERT INTO velox_project_bridges (project_id, workspace_id, external_project_id, migration_run_id) VALUES ('bridge-112-empty-run', 11201, 'vx-112-empty-run', '   ')`, "velox_project_bridges_migration_run_id_nonempty_ck")
}

func seedVeloxProjectBridge112(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO users (id, email, name) VALUES (11201, 'bridge-112@example.test', 'Bridge 112')`); err != nil && !strings.Contains(err.Error(), "duplicate key") {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO workspaces (id, name, owner_id) VALUES (11201, 'Bridge 112 workspace', 11201)`); err != nil && !strings.Contains(err.Error(), "duplicate key") {
		t.Fatalf("seed workspace: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO platform_accounts (id, user_id, platform, platform_user_id, username) VALUES (11201, 11201, 'youtube', 'bridge-112-youtube', 'bridge112'), (11202, 11201, 'youtube', 'bridge-112-other', 'bridge112-other')`); err != nil && !strings.Contains(err.Error(), "duplicate key") {
		t.Fatalf("seed accounts: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO workspace_channels (workspace_id, platform_account_id, enabled) VALUES (11201, 11201, TRUE)`); err != nil && !strings.Contains(err.Error(), "duplicate key") {
		t.Fatalf("seed channel binding: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO thumbnail_projects (id, workspace_id, created_by, name, canvas_width, canvas_height) VALUES ('bridge-112-a', 11201, 11201, 'Bridge project', 1920, 1080)`); err != nil && !strings.Contains(err.Error(), "duplicate key") {
		t.Fatalf("seed project: %v", err)
	}
}

func assertBridgeConstraintRejects(t *testing.T, db *sql.DB, statement, constraint string) {
	t.Helper()
	if _, err := db.Exec(statement); err == nil {
		t.Fatalf("statement unexpectedly succeeded: %s", statement)
	} else if !strings.Contains(err.Error(), constraint) {
		t.Fatalf("error %q does not mention %q", err, constraint)
	}
}
