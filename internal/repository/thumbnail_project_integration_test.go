//go:build integration

package repository_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/database"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/testutil/postgres"
)

func TestThumbnailProjectRepository_IntegrationWorkspaceCRUDAndCAS(t *testing.T) {
	db, cleanup := postgres.StartTestPostgres(t)
	defer cleanup()
	if err := database.RunMigrationsUpTo(db, 94); err != nil {
		t.Fatalf("migrations: %v", err)
	}
	_, err := db.Exec(`INSERT INTO users (id, email, name) VALUES (9911, 'thumb-repo@example.test', 'Thumb Repo')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO workspaces (id, name, owner_id) VALUES (9911, 'Thumb Repo WS', 9911)`)
	if err != nil {
		t.Fatal(err)
	}

	repo := repository.NewThumbnailProjectRepository(db)
	project := &models.ThumbnailProject{ID: "thumbproj_integration", WorkspaceID: 9911, CreatedBy: 9911, Name: "Integration", CanvasWidth: 1920, CanvasHeight: 1080}
	if err := repo.Create(context.Background(), project); err != nil {
		t.Fatal(err)
	}
	if project.Version != 1 {
		t.Fatalf("version=%d", project.Version)
	}
	if got, err := repo.FindByID(context.Background(), 9911, project.ID); err != nil || got == nil {
		t.Fatalf("find own project: %+v %v", got, err)
	}
	if got, err := repo.FindByID(context.Background(), 9912, project.ID); err != nil || got != nil {
		t.Fatalf("cross-workspace read leaked: %+v %v", got, err)
	}
	project.Name = "Updated"
	if err := repo.UpdateCAS(context.Background(), project, 1); err != nil {
		t.Fatal(err)
	}
	if project.Version != 2 {
		t.Fatalf("version after CAS=%d", project.Version)
	}
	if err := repo.UpdateCAS(context.Background(), project, 1); err == nil {
		t.Fatal("stale CAS unexpectedly succeeded")
	}
	if err := repo.UpdateStatusCAS(context.Background(), 9911, project.ID, models.ThumbnailProjectStatusArchived, 2); err != nil {
		t.Fatal(err)
	}
	if err := repo.UpdateStatusCAS(context.Background(), 9911, project.ID, models.ThumbnailProjectStatusDeleted, 2); err == nil {
		t.Fatal("stale delete unexpectedly succeeded")
	}
}

func TestThumbnailProjectRepository_IntegrationSnapshotDedupRestoreAndIsolation(t *testing.T) {
	db, cleanup := postgres.StartTestPostgres(t)
	defer cleanup()
	if err := database.RunMigrationsUpTo(db, 97); err != nil {
		t.Fatalf("migrations: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO users (id, email, name) VALUES (9921, 'thumb-snapshot@example.test', 'Thumb Snapshot')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO workspaces (id, name, owner_id) VALUES (9921, 'Thumb Snapshot WS', 9921)`); err != nil {
		t.Fatal(err)
	}

	repo := repository.NewThumbnailProjectRepository(db)
	project := &models.ThumbnailProject{ID: "thumbproj_snapshot", WorkspaceID: 9921, CreatedBy: 9921, Name: "Snapshot", CanvasWidth: 1920, CanvasHeight: 1080}
	if err := repo.Create(context.Background(), project); err != nil {
		t.Fatal(err)
	}
	first, err := repo.SaveSnapshot(context.Background(), 9921, project.ID, models.ThumbnailProjectSnapshot{
		SchemaVersion: 1, SnapshotJSON: []byte(`{"canvas":{"background":"#000"},"objects":[]}`), RendererVersion: "renderer-1", BaseVersion: 1,
	}, 9921)
	if err != nil {
		t.Fatalf("first snapshot: %v", err)
	}
	if first.Version != 2 || first.RevisionNumber != 1 || first.Deduplicated {
		t.Fatalf("unexpected first snapshot result: %+v", first)
	}
	duplicate, err := repo.SaveSnapshot(context.Background(), 9921, project.ID, models.ThumbnailProjectSnapshot{
		SchemaVersion: 1, SnapshotJSON: []byte(`{ "objects": [], "canvas": {"background":"#000"} }`), RendererVersion: "renderer-1", BaseVersion: 2,
	}, 9921)
	if err != nil {
		t.Fatalf("deduplicated snapshot: %v", err)
	}
	if !duplicate.Deduplicated || duplicate.RevisionID != first.RevisionID || duplicate.Version != 2 {
		t.Fatalf("deduplication failed: first=%+v duplicate=%+v", first, duplicate)
	}
	second, err := repo.SaveSnapshot(context.Background(), 9921, project.ID, models.ThumbnailProjectSnapshot{
		SchemaVersion: 1, SnapshotJSON: []byte(`{"canvas":{"background":"#fff"},"objects":[]}`), RendererVersion: "renderer-1", BaseVersion: 2,
	}, 9921)
	if err != nil {
		t.Fatalf("second snapshot: %v", err)
	}
	if second.Version != 3 || second.RevisionNumber != 2 {
		t.Fatalf("unexpected second snapshot result: %+v", second)
	}
	restored, err := repo.RestoreRevision(context.Background(), 9921, project.ID, first.RevisionID, 3, 9921, "renderer-2")
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if restored.Version != 4 || restored.RevisionNumber != 3 || restored.RevisionID == first.RevisionID || restored.SnapshotSHA256 != first.SnapshotSHA256 {
		t.Fatalf("restore did not create a new immutable revision: first=%+v restored=%+v", first, restored)
	}
	if _, err := repo.SaveSnapshot(context.Background(), 9922, project.ID, models.ThumbnailProjectSnapshot{
		SchemaVersion: 1, SnapshotJSON: []byte(`{"canvas":{},"objects":[]}`), RendererVersion: "renderer-1", BaseVersion: 4,
	}, 9922); !errors.Is(err, repository.ErrThumbnailProjectNotFound) {
		t.Fatalf("cross-workspace snapshot: want not found, got %v", err)
	}
}
