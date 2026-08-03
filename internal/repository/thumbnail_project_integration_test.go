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
