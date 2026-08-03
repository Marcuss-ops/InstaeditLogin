//go:build integration

package database

import (
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/testutil/postgres"
)

func TestMigration095_LivestreamStateMachineColumnsAndIdempotency(t *testing.T) {
	db, cleanup := postgres.StartTestPostgres(t)
	defer cleanup()

	if err := RunMigrationsUpTo(db, 94); err != nil {
		t.Fatalf("RunMigrationsUpTo(94): %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO users (id, email, name) VALUES (9501, 'state-machine@example.test', 'State machine')
	`); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO workspaces (id, name, owner_id) VALUES (9501, 'State machine workspace', 9501)
	`); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO platform_accounts (id, user_id, workspace_id, platform, platform_user_id)
		VALUES (9501, 9501, 9501, 'youtube', 'state-machine-channel')
	`); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO livestreams
		    (id, workspace_id, platform_account_id, created_by, title, privacy_status,
		     playback_mode, schedule_type, desired_state, actual_state)
		VALUES ('state-machine-live', 9501, 9501, 9501, 'State machine', 'unlisted',
		        'loop_continuous', 'manual', 'live', 'live')
	`); err != nil {
		t.Fatalf("seed livestream: %v", err)
	}

	if err := RunMigrationsUpTo(db, 95); err != nil {
		t.Fatalf("RunMigrationsUpTo(95): %v", err)
	}

	var desiredState string
	var desiredGeneration, configurationVersion int64
	if err := db.QueryRow(`SELECT desired_state, desired_generation, configuration_version FROM livestreams WHERE id = 'state-machine-live'`).Scan(&desiredState, &desiredGeneration, &configurationVersion); err != nil {
		t.Fatalf("read migrated livestream: %v", err)
	}
	if desiredState != "running" || desiredGeneration != 1 || configurationVersion != 1 {
		t.Fatalf("unexpected state-machine backfill: state=%q generation=%d version=%d", desiredState, desiredGeneration, configurationVersion)
	}

	if _, err := db.Exec(`UPDATE livestreams SET desired_state = 'preparing' WHERE id = 'state-machine-live'`); err == nil {
		t.Fatal("desired-state constraint accepted a runtime-only state")
	}
	if err := RunMigrationsUpTo(db, 95); err != nil {
		t.Fatalf("second RunMigrationsUpTo(95): %v", err)
	}
}
