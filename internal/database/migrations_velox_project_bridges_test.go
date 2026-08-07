//go:build integration

package database

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/testutil/postgres"
)

func TestMigration112_VeloxProjectBridgeSchemaAndConstraints(t *testing.T) {
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
	if _, err := db.Exec(`INSERT INTO thumbnail_projects (id, workspace_id, created_by, name, canvas_width, canvas_height) VALUES ('bridge-112-cross', 11201, 11201, 'Bridge cross-context project', 1280, 720), ('bridge-112-platform', 11201, 11201, 'Bridge platform-context project', 1280, 720)`); err != nil {
		t.Fatalf("insert cross-context projects: %v", err)
	}
	assertBridgeConstraintRejects(t, db, `INSERT INTO velox_project_bridges (project_id, workspace_id, velox_project_id) VALUES ('bridge-112-a', 11201, 'vx-112-b')`, "velox_project_bridges_pkey")
	assertBridgeConstraintRejects(t, db, `INSERT INTO velox_project_bridges (project_id, workspace_id, velox_project_id) VALUES ('bridge-112-b', 11201, 'vx-112-a')`, "velox_project_bridges_velox_project_uq")
	assertBridgeConstraintRejects(t, db, `INSERT INTO velox_project_bridges (project_id, workspace_id, velox_project_id, platform, platform_account_id) VALUES ('bridge-112-cross', 11201, 'vx-112-cross', 'youtube', 11202)`, "velox_project_bridges_channel_fk")
	assertBridgeConstraintRejects(t, db, `INSERT INTO velox_project_bridges (project_id, workspace_id, velox_project_id, platform, platform_account_id) VALUES ('bridge-112-platform', 11201, 'vx-112-platform', 'tiktok', 11201)`, "velox_project_bridges_platform_account_fk")

	var groupColumns int
	if err := db.QueryRow(`SELECT COUNT(*) FROM information_schema.columns WHERE table_name='velox_project_bridges' AND column_name IN ('group_id', 'channel_ids', 'member_ids')`).Scan(&groupColumns); err != nil {
		t.Fatal(err)
	}
	if groupColumns != 0 {
		t.Fatalf("bridge contains forbidden duplicated ownership columns: %d", groupColumns)
	}
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
