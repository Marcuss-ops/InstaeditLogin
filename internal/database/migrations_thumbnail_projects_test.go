//go:build integration

package database

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/testutil/postgres"
)

func TestMigration094_ThumbnailProjectModuleSchemaAndIdempotency(t *testing.T) {
	db, cleanup := postgres.StartTestPostgres(t)
	defer cleanup()

	if err := RunMigrationsUpTo(db, 93); err != nil {
		t.Fatalf("RunMigrationsUpTo(93): %v", err)
	}
	seedThumbnailProjects094(t, db)

	if err := RunMigrationsUpTo(db, 94); err != nil {
		t.Fatalf("RunMigrationsUpTo(94): %v", err)
	}
	assertThumbnailProjects094Schema(t, db)

	var before int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE filename = '094_thumbnail_projects.sql'`).Scan(&before); err != nil {
		t.Fatalf("count migration 094 before rerun: %v", err)
	}
	if err := RunMigrationsUpTo(db, 94); err != nil {
		t.Fatalf("RunMigrationsUpTo(94) second pass: %v", err)
	}
	var after int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE filename = '094_thumbnail_projects.sql'`).Scan(&after); err != nil {
		t.Fatalf("count migration 094 after rerun: %v", err)
	}
	if before != 1 || after != before {
		t.Fatalf("migration 094 registry rows changed on rerun: before=%d after=%d", before, after)
	}
}

func TestMigration094_ThumbnailProjectModuleReferencesAndConstraints(t *testing.T) {
	db, cleanup := postgres.StartTestPostgres(t)
	defer cleanup()

	if err := RunMigrationsUpTo(db, 94); err != nil {
		t.Fatalf("RunMigrationsUpTo(94): %v", err)
	}
	seedThumbnailProjects094(t, db)

	if _, err := db.Exec(`
		INSERT INTO thumbnail_projects
		    (id, workspace_id, created_by, name, canvas_width, canvas_height)
		VALUES ('thumbproj-094-a', 9401, 9401, 'Autonomous cover', 1920, 1080)
	`); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO thumbnail_projects
		    (id, workspace_id, created_by, name, canvas_width, canvas_height)
		VALUES ('thumbproj-094-b', 9401, 9401, 'Second project', 1280, 720)
	`); err != nil {
		t.Fatalf("insert second project: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO thumbnail_project_revisions
		    (id, project_id, revision_number, schema_version, snapshot_json,
		     snapshot_sha256, renderer_version, created_by)
		VALUES ('thumbrev-094-a', 'thumbproj-094-a', 1, 1,
		        '{"canvas":{"width":1920,"height":1080},"objects":[]}'::jsonb,
		        decode(repeat('ab', 32), 'hex'), 'renderer-1', 9401),
		       ('thumbrev-094-b', 'thumbproj-094-b', 1, 1,
		        '{"canvas":{"width":1280,"height":720},"objects":[]}'::jsonb,
		        decode(repeat('bc', 32), 'hex'), 'renderer-1', 9401)
	`); err != nil {
		t.Fatalf("insert revisions: %v", err)
	}
	if _, err := db.Exec(`
		UPDATE thumbnail_projects
		   SET current_revision_id = 'thumbrev-094-a', version = version + 1
		 WHERE id = 'thumbproj-094-a'
	`); err != nil {
		t.Fatalf("set current revision: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO thumbnail_project_assets (project_id, media_id, role, object_id)
		VALUES ('thumbproj-094-a', '00000000-0000-0000-0000-000000000941', 'background', NULL)
	`); err != nil {
		t.Fatalf("insert project asset: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO thumbnail_exports
		    (id, project_id, revision_id, media_id, content_type, width, height,
		     file_size, sha256, renderer_version, status)
		VALUES ('thumbexp-094-a', 'thumbproj-094-a', 'thumbrev-094-a',
		        '00000000-0000-0000-0000-000000000941', 'image/png', 1920, 1080,
		        12345, decode(repeat('cd', 32), 'hex'), 'renderer-1', 'ready'),
		       ('thumbexp-094-b', 'thumbproj-094-b', 'thumbrev-094-b',
		        '00000000-0000-0000-0000-000000000941', 'image/png', 1280, 720,
		        5432, decode(repeat('de', 32), 'hex'), 'renderer-1', 'ready')
	`); err != nil {
		t.Fatalf("insert exports: %v", err)
	}
	if _, err := db.Exec(`
		UPDATE thumbnail_projects
		   SET latest_export_id = 'thumbexp-094-a', preview_media_id = '00000000-0000-0000-0000-000000000941'
		 WHERE id = 'thumbproj-094-a'
	`); err != nil {
		t.Fatalf("set preview/export pointers: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO thumbnail_assignments
		    (id, workspace_id, project_id, export_id, platform_account_id,
		     youtube_video_id, target_language, status)
		VALUES ('thumbassign-094-a', 9401, 'thumbproj-094-a', 'thumbexp-094-a',
		        9401, 'youtube-video-094-a', 'en', 'draft')
	`); err != nil {
		t.Fatalf("insert assignment: %v", err)
	}

	assertConstraintRejects(t, db, "duplicate revision number", `
		INSERT INTO thumbnail_project_revisions
		    (id, project_id, revision_number, schema_version, snapshot_json,
		     snapshot_sha256, renderer_version, created_by)
		VALUES ('thumbrev-094-duplicate-number', 'thumbproj-094-a', 1, 1,
		        '{}'::jsonb, decode(repeat('ef', 32), 'hex'), 'renderer-1', 9401)
	`, "thumbnail_project_revisions_project_revision_uq")

	assertConstraintRejects(t, db, "duplicate revision hash", `
		INSERT INTO thumbnail_project_revisions
		    (id, project_id, revision_number, schema_version, snapshot_json,
		     snapshot_sha256, renderer_version, created_by)
		VALUES ('thumbrev-094-duplicate-hash', 'thumbproj-094-a', 2, 1,
		        '{}'::jsonb, decode(repeat('ab', 32), 'hex'), 'renderer-1', 9401)
	`, "thumbnail_project_revisions_project_hash_uq")

	assertConstraintRejects(t, db, "export from another project revision", `
		INSERT INTO thumbnail_exports
		    (id, project_id, revision_id, media_id, content_type, width, height,
		     file_size, sha256, renderer_version, status)
		VALUES ('thumbexp-094-cross', 'thumbproj-094-b', 'thumbrev-094-a',
		        '00000000-0000-0000-0000-000000000941', 'image/png', 1920, 1080,
		        10, decode(repeat('12', 32), 'hex'), 'renderer-1', 'ready')
	`, "thumbnail_exports_project_revision_fk")

	assertConstraintRejects(t, db, "project pointer to another project's revision", `
		UPDATE thumbnail_projects
		   SET current_revision_id = 'thumbrev-094-b'
		 WHERE id = 'thumbproj-094-a'
	`, "thumbnail_projects_current_revision_same_project_fk")

	assertConstraintRejects(t, db, "project pointer to another project's export", `
		UPDATE thumbnail_projects
		   SET latest_export_id = 'thumbexp-094-b'
		 WHERE id = 'thumbproj-094-a'
	`, "thumbnail_projects_latest_export_same_project_fk")

	assertConstraintRejects(t, db, "assignment from another project export", `
		INSERT INTO thumbnail_assignments
		    (id, workspace_id, project_id, export_id, platform_account_id,
		     youtube_video_id)
		VALUES ('thumbassign-094-cross', 9401, 'thumbproj-094-a', 'thumbexp-094-missing',
		        9401, 'youtube-video-094-cross')
	`, "thumbnail_assignments_project_export_fk")

	assertConstraintRejects(t, db, "assignment to an unbound workspace account", `
		INSERT INTO thumbnail_assignments
		    (id, workspace_id, project_id, export_id, platform_account_id,
		     youtube_video_id)
		VALUES ('thumbassign-094-unbound', 9401, 'thumbproj-094-a', 'thumbexp-094-a',
		        9403, 'youtube-video-094-unbound')
	`, "thumbnail_assignments_workspace_account_fk")

	assertConstraintRejects(t, db, "assignment to a non-YouTube account", `
		INSERT INTO thumbnail_assignments
		    (id, workspace_id, project_id, export_id, platform_account_id,
		     youtube_video_id)
		VALUES ('thumbassign-094-non-youtube', 9401, 'thumbproj-094-a', 'thumbexp-094-a',
		        9402, 'video-on-non-youtube')
	`, "thumbnail_assignments_workspace_account_platform_fk")

	var revisionCount, assignmentCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM thumbnail_project_revisions WHERE project_id = 'thumbproj-094-a'`).Scan(&revisionCount); err != nil {
		t.Fatalf("count revisions: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM thumbnail_assignments WHERE project_id = 'thumbproj-094-a'`).Scan(&assignmentCount); err != nil {
		t.Fatalf("count assignments: %v", err)
	}
	if revisionCount != 1 || assignmentCount != 1 {
		t.Fatalf("unexpected persisted rows: revisions=%d assignments=%d", revisionCount, assignmentCount)
	}
}

func TestMigration094_RollsBackAllChangesWhenMigrationFails(t *testing.T) {
	db, cleanup := postgres.StartTestPostgres(t)
	defer cleanup()

	if err := RunMigrationsUpTo(db, 93); err != nil {
		t.Fatalf("RunMigrationsUpTo(93): %v", err)
	}
	files, err := loadMigrationFiles(94)
	if err != nil {
		t.Fatalf("loadMigrationFiles: %v", err)
	}
	var migration migrationFile
	for _, candidate := range files {
		if candidate.name == "094_thumbnail_projects.sql" {
			migration = candidate
			break
		}
	}
	if migration.name == "" {
		t.Fatal("migration 094 not embedded")
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

	for _, table := range []string{"thumbnail_projects", "thumbnail_project_revisions", "thumbnail_project_assets", "thumbnail_exports", "thumbnail_assignments"} {
		var exists bool
		if err := db.QueryRow(`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = $1)`, table).Scan(&exists); err != nil {
			t.Fatalf("check rolled-back table %s: %v", table, err)
		}
		if exists {
			t.Fatalf("table %s survived the failed migration transaction", table)
		}
	}
}

func seedThumbnailProjects094(t *testing.T, db *sql.DB) {
	t.Helper()

	if _, err := db.Exec(`
		INSERT INTO users (id, email, name)
		VALUES (9401, 'thumbnail-094@example.test', 'Thumbnail 094')
	`); err != nil {
		// The schema test calls this helper after migration 094, while the
		// first test calls it before migration 094. Both need the same seed;
		// a duplicate means the caller accidentally seeded twice.
		if !strings.Contains(err.Error(), "duplicate key") {
			t.Fatalf("seed user: %v", err)
		}
	}
	if _, err := db.Exec(`
		INSERT INTO workspaces (id, name, owner_id)
		VALUES (9401, 'Thumbnail 094 workspace', 9401)
	`); err != nil && !strings.Contains(err.Error(), "duplicate key") {
		t.Fatalf("seed workspace: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO platform_accounts
		    (id, user_id, workspace_id, platform, platform_user_id, username)
		VALUES (9401, 9401, 9401, 'youtube', 'yt-thumbnail-094', 'thumbnail094'),
		       (9402, 9401, 9401, 'tiktok', 'tt-thumbnail-094', 'thumbnail094-tt')
	`); err != nil && !strings.Contains(err.Error(), "duplicate key") {
		t.Fatalf("seed platform accounts: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO workspace_channels (workspace_id, platform_account_id, enabled)
		VALUES (9401, 9401, TRUE), (9401, 9402, TRUE)
	`); err != nil && !strings.Contains(err.Error(), "duplicate key") {
		t.Fatalf("seed workspace channels: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO media_assets
		    (id, user_id, upload_key, content_type, size_bytes, status, sha256, expires_at)
		VALUES ('00000000-0000-0000-0000-000000000941', 9401,
		        'uploads/9401/thumbnail-094.png', 'image/png', 12345, 'ready',
		        repeat('a', 64), NOW() + INTERVAL '1 day')
	`); err != nil && !strings.Contains(err.Error(), "duplicate key") {
		t.Fatalf("seed media asset: %v", err)
	}
}

func assertThumbnailProjects094Schema(t *testing.T, db *sql.DB) {
	t.Helper()

	for _, table := range []string{"thumbnail_projects", "thumbnail_project_revisions", "thumbnail_project_assets", "thumbnail_exports", "thumbnail_assignments"} {
		var exists bool
		if err := db.QueryRow(`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = $1)`, table).Scan(&exists); err != nil {
			t.Fatalf("check table %s: %v", table, err)
		}
		if !exists {
			t.Fatalf("migration 094 did not create table %s", table)
		}
	}

	var projectVersionDefault string
	if err := db.QueryRow(`
		SELECT column_default
		  FROM information_schema.columns
		 WHERE table_schema = 'public'
		   AND table_name = 'thumbnail_projects'
		   AND column_name = 'version'
	`).Scan(&projectVersionDefault); err != nil {
		t.Fatalf("read project version default: %v", err)
	}
	if !strings.Contains(projectVersionDefault, "1") {
		t.Fatalf("thumbnail_projects.version default = %q, want default 1", projectVersionDefault)
	}

	for _, indexName := range []string{
		"thumbnail_projects_workspace_status_idx",
		"thumbnail_project_revisions_project_revision_uq",
		"thumbnail_project_assets_project_media_role_pk",
		"thumbnail_exports_project_status_idx",
		"thumbnail_assignments_export_account_video_uq",
	} {
		var exists bool
		if err := db.QueryRow(`SELECT EXISTS (SELECT 1 FROM pg_indexes WHERE schemaname = 'public' AND indexname = $1)`, indexName).Scan(&exists); err != nil {
			t.Fatalf("check index %s: %v", indexName, err)
		}
		if !exists {
			t.Fatalf("migration 094 missing index %s", indexName)
		}
	}
}

func assertConstraintRejects(t *testing.T, db *sql.DB, label, statement, constraintName string) {
	t.Helper()
	if _, err := db.Exec(statement); err == nil {
		t.Fatalf("%s: invalid statement unexpectedly succeeded", label)
	} else if !strings.Contains(err.Error(), constraintName) {
		t.Fatalf("%s: error does not mention %q: %v", label, constraintName, err)
	}
}
