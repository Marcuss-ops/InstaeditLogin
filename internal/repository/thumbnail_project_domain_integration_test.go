//go:build integration

package repository_test

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/database"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/testutil/postgres"
)

func TestThumbnailProjectRepository_IntegrationAssetsExportsAssignments(t *testing.T) {
	db, cleanup := postgres.StartTestPostgres(t)
	defer cleanup()
	if err := database.RunMigrationsUpTo(db, 122); err != nil {
		t.Fatalf("migrations: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO users (id, email, name) VALUES (9941, 'thumb-domain@example.test', 'Thumb Domain')
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO workspaces (id, name, owner_id) VALUES (9941, 'Thumb Domain WS', 9941)
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO platform_accounts (id, user_id, workspace_id, platform, platform_user_id, username)
		VALUES (9941, 9941, 9941, 'youtube', 'yt-domain-9941', 'domain9941')
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO workspace_channels (workspace_id, platform_account_id) VALUES (9941, 9941)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO media_assets (id, user_id, upload_key, content_type, size_bytes, status, sha256, expires_at)
		VALUES ('00000000-0000-0000-0000-000000000941', 9941, 'uploads/9941/thumb.png', 'image/png', 32, 'ready', repeat('a', 64), NOW() + INTERVAL '1 day')
	`); err != nil {
		t.Fatal(err)
	}

	repo := repository.NewThumbnailProjectRepository(db)
	project := &models.ThumbnailProject{ID: "thumbproj_domain", WorkspaceID: 9941, CreatedBy: 9941, Name: "Domain", CanvasWidth: 1920, CanvasHeight: 1080}
	if err := repo.Create(context.Background(), project); err != nil {
		t.Fatal(err)
	}
	revision, err := repo.SaveSnapshot(context.Background(), 9941, project.ID, models.ThumbnailProjectSnapshot{
		SchemaVersion: 1, SnapshotJSON: []byte(`{"canvas":{},"objects":[]}`), RendererVersion: "renderer-1", BaseVersion: 1,
	}, 9941)
	if err != nil {
		t.Fatal(err)
	}

	asset := &models.ThumbnailProjectAsset{ProjectID: project.ID, MediaID: "00000000-0000-0000-0000-000000000941", Role: models.ThumbnailProjectAssetRoleBackground}
	if err := repo.CreateAsset(context.Background(), 9941, asset); err != nil {
		t.Fatalf("CreateAsset: %v", err)
	}
	assets, err := repo.ListAssets(context.Background(), 9941, project.ID)
	if err != nil || len(assets) != 1 {
		t.Fatalf("ListAssets: len=%d err=%v", len(assets), err)
	}

	export := &models.ThumbnailExport{
		ProjectID: project.ID, RevisionID: revision.RevisionID, MediaID: asset.MediaID,
		ContentType: models.ThumbnailProjectExportContentTypePNG, Width: 1920, Height: 1080,
		FileSize: 32, SHA256: make([]byte, 32), RendererVersion: "renderer-1", Status: models.ThumbnailProjectExportStatusRendering,
	}
	if err := repo.CreateExport(context.Background(), 9941, export); err != nil {
		t.Fatalf("CreateExport: %v", err)
	}
	if err := repo.UpdateExportStatus(context.Background(), 9941, export.ID, " READY ", "", make([]byte, 32), 32, " renderer-1 "); err != nil {
		t.Fatalf("UpdateExportStatus ready: %v", err)
	}
	if err := repo.UpdateExportStatus(context.Background(), 9941, export.ID, models.ThumbnailProjectExportStatusFailed, "should not transition", make([]byte, 32), 32, "renderer-1"); err == nil {
		t.Fatal("terminal ready export unexpectedly transitioned to failed")
	}
	foundExport, err := repo.FindExport(context.Background(), 9941, export.ID)
	if err != nil || foundExport == nil {
		t.Fatalf("FindExport: export=%+v err=%v", foundExport, err)
	}
	var latestExport, previewMedia string
	if err := db.QueryRow(`SELECT latest_export_id, preview_media_id FROM thumbnail_projects WHERE id = $1`, project.ID).Scan(&latestExport, &previewMedia); err != nil {
		t.Fatal(err)
	}
	if latestExport != export.ID || previewMedia != asset.MediaID {
		t.Fatalf("project pointers not updated: latest=%s preview=%s", latestExport, previewMedia)
	}

	failedExport := &models.ThumbnailExport{
		ProjectID: project.ID, RevisionID: revision.RevisionID, MediaID: asset.MediaID,
		ContentType: models.ThumbnailProjectExportContentTypePNG, Width: 1920, Height: 1080,
		FileSize: 32, SHA256: make([]byte, 32), RendererVersion: "renderer-1", Status: models.ThumbnailProjectExportStatusRendering,
	}
	if err := repo.CreateExport(context.Background(), 9941, failedExport); err != nil {
		t.Fatalf("CreateExport failed case: %v", err)
	}
	if err := repo.UpdateExportStatus(context.Background(), 9941, failedExport.ID, " failed ", " renderer crashed ", nil, 0, " renderer-2 "); err != nil {
		t.Fatalf("UpdateExportStatus failed: %v", err)
	}
	if err := repo.CreateAssignment(context.Background(), &models.ThumbnailAssignment{
		WorkspaceID: 9941, ProjectID: project.ID, ExportID: failedExport.ID, PlatformAccountID: 9941,
		YouTubeVideoID: "video-failed-9941",
	}); err == nil {
		t.Fatal("assignment to failed export unexpectedly succeeded")
	}

	assignment := &models.ThumbnailAssignment{
		WorkspaceID: 9941, ProjectID: project.ID, ExportID: export.ID, PlatformAccountID: 9941,
		YouTubeVideoID: "video-domain-9941",
	}
	if err := repo.CreateAssignment(context.Background(), assignment); err != nil {
		t.Fatalf("CreateAssignment: %v", err)
	}
	assignments, err := repo.ListAssignments(context.Background(), 9941, project.ID)
	if err != nil || len(assignments) != 1 {
		t.Fatalf("ListAssignments: len=%d err=%v", len(assignments), err)
	}
	if err := repo.UpdateAssignmentStatus(context.Background(), 9941, assignment.ID, models.ThumbnailProjectAssignmentStatusApplied); err != nil {
		t.Fatalf("UpdateAssignmentStatus: %v", err)
	}

	if _, err := repo.FindExport(context.Background(), 9942, export.ID); err == nil {
		t.Fatal("cross-workspace export lookup unexpectedly succeeded")
	}
	crossAssignments, err := repo.ListAssignments(context.Background(), 9942, project.ID)
	if err != nil {
		t.Fatalf("cross-workspace assignment list returned infrastructure error: %v", err)
	}
	if len(crossAssignments) != 0 {
		t.Fatalf("cross-workspace assignment list leaked %d rows", len(crossAssignments))
	}
}
