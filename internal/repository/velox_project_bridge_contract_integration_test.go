//go:build integration

package repository_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/database"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/testutil/postgres"
)

func TestVeloxProjectBridge_ContractPostgresSourceOfTruthAndIsolation(t *testing.T) {
	db, cleanup := postgres.StartTestPostgres(t, postgres.WithDatabase("instaedit_bridge_contract"))
	defer cleanup()
	if err := database.RunMigrationsUpTo(db, 114); err != nil {
		t.Fatalf("migrations: %v", err)
	}
	seedBridgeContractDatabase(t, db)

	const (
		workspaceID    int64 = 7711
		otherWorkspace int64 = 7712
		projectID            = "thumbproj_contract_pg"
		veloxProjectID       = "vx_contract_pg"
	)
	repo := repository.NewThumbnailProjectRepository(db)
	accountID := int64(7711)
	channelID := "UC-contract-pg"
	videoID := "video-contract-pg"
	language := "en"
	bridge := &models.VeloxProjectBridge{
		ProjectID: projectID, WorkspaceID: workspaceID, VeloxProjectID: veloxProjectID,
		Platform: "youtube", PlatformAccountID: &accountID,
		ChannelID: &channelID, VideoID: &videoID, Language: &language,
	}

	var editorRowsBefore int
	if err := db.QueryRow(`SELECT COUNT(*) FROM youtube_video_edits WHERE velox_project_id = $1`, veloxProjectID).Scan(&editorRowsBefore); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateVeloxProjectBridge(context.Background(), bridge); err != nil {
		t.Fatalf("create bridge: %v", err)
	}

	got, err := repo.FindVeloxProjectBridge(context.Background(), workspaceID, projectID)
	if err != nil {
		t.Fatalf("find bridge: %v", err)
	}
	if got == nil || got.ProjectID != projectID || got.VeloxProjectID != veloxProjectID || got.WorkspaceID != workspaceID {
		t.Fatalf("unexpected authoritative bridge: %+v", got)
	}
	if got.PlatformAccountID == nil || *got.PlatformAccountID != accountID || got.ChannelID == nil || *got.ChannelID != channelID {
		t.Fatalf("channel context was not preserved: %+v", got)
	}

	// The existing Velox handle is replayed as an equivalent bridge. The
	// database remains the final idempotency/ownership authority.
	replay := *bridge
	if err := repo.CreateVeloxProjectBridge(context.Background(), &replay); !errors.Is(err, repository.ErrVeloxProjectBridgeConflict) {
		t.Fatalf("duplicate project replay: want conflict, got %v", err)
	}
	var bridgeCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM velox_project_bridges WHERE velox_project_id = $1`, veloxProjectID).Scan(&bridgeCount); err != nil {
		t.Fatal(err)
	}
	if bridgeCount != 1 {
		t.Fatalf("equivalent replay must leave one bridge row, got %d", bridgeCount)
	}
	otherProject := *bridge
	otherProject.ProjectID = "thumbproj_contract_pg_other"
	if err := repo.CreateVeloxProjectBridge(context.Background(), &otherProject); !errors.Is(err, repository.ErrVeloxProjectBridgeConflict) {
		t.Fatalf("duplicate Velox ownership: want conflict, got %v", err)
	}

	if foreign, err := repo.FindVeloxProjectBridge(context.Background(), otherWorkspace, projectID); err != nil || foreign != nil {
		t.Fatalf("cross-workspace bridge leaked: bridge=%+v err=%v", foreign, err)
	}
	var editorRowsAfter int
	if err := db.QueryRow(`SELECT COUNT(*) FROM youtube_video_edits WHERE velox_project_id = $1`, veloxProjectID).Scan(&editorRowsAfter); err != nil {
		t.Fatal(err)
	}
	if editorRowsAfter != editorRowsBefore {
		t.Fatalf("bridge changed editor session ownership/data: before=%d after=%d", editorRowsBefore, editorRowsAfter)
	}

	if err := repo.DeleteVeloxProjectBridge(context.Background(), workspaceID, projectID); err != nil {
		t.Fatalf("delete bridge relation: %v", err)
	}
	var editorRowsFinal int
	if err := db.QueryRow(`SELECT COUNT(*) FROM youtube_video_edits WHERE velox_project_id = $1`, veloxProjectID).Scan(&editorRowsFinal); err != nil {
		t.Fatal(err)
	}
	if editorRowsFinal != editorRowsBefore {
		t.Fatalf("deleting bridge touched editor data: before=%d after=%d", editorRowsBefore, editorRowsFinal)
	}
}

func seedBridgeContractDatabase(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO users (id, email, name) VALUES
			(7711, 'bridge-contract-owner@example.test', 'Bridge Contract Owner'),
			(7712, 'bridge-contract-other@example.test', 'Bridge Contract Other')
	`); err != nil {
		t.Fatalf("seed users: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO workspaces (id, name, owner_id) VALUES
			(7711, 'Bridge Contract Workspace', 7711),
			(7712, 'Bridge Contract Other Workspace', 7712)
	`); err != nil {
		t.Fatalf("seed workspaces: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO platform_accounts (id, user_id, workspace_id, platform, platform_user_id, username, status)
		VALUES (7711, 7711, 7711, 'youtube', 'UC-contract-pg', 'bridge-contract', 'active')
	`); err != nil {
		t.Fatalf("seed platform account: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO workspace_channels (workspace_id, platform_account_id, group_name, enabled)
		VALUES (7711, 7711, NULL, TRUE)
	`); err != nil {
		t.Fatalf("seed workspace channel: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO thumbnail_projects (id, workspace_id, created_by, name, canvas_width, canvas_height)
		VALUES
			('thumbproj_contract_pg', 7711, 7711, 'Bridge Contract Project', 1920, 1080),
			('thumbproj_contract_pg_other', 7711, 7711, 'Bridge Contract Other Project', 1920, 1080)
	`); err != nil {
		t.Fatalf("seed thumbnail projects: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO youtube_video_edits
			(id, workspace_id, platform_account_id, youtube_video_id, velox_project_id, desired_privacy, status)
		VALUES ('ytedit_bridge_contract', 7711, 7711, 'video-contract-pg', 'vx_contract_pg', 'private', 'editing')
	`); err != nil {
		t.Fatalf("seed editor session: %v", err)
	}
}
