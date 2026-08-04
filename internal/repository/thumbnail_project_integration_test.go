//go:build integration

package repository_test

import (
	"context"
	"encoding/hex"
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

func TestThumbnailProjectRepository_IntegrationSnapshotRegistersAssets(t *testing.T) {
	db, cleanup := postgres.StartTestPostgres(t)
	defer cleanup()
	if err := database.RunMigrationsUpTo(db, 97); err != nil {
		t.Fatalf("migrations: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO users (id, email, name) VALUES (9931, 'thumb-assets@example.test', 'Thumb Assets')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO workspaces (id, name, owner_id) VALUES (9931, 'Thumb Assets WS', 9931)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO media_assets (id, user_id, upload_key, content_type, size_bytes, status, sha256, expires_at)
		VALUES ('00000000-0000-0000-0000-000000000931', 9931, 'uploads/9931/canvas.png', 'image/png', 32, 'ready', repeat('b', 64), NOW() + INTERVAL '1 day')
	`); err != nil {
		t.Fatal(err)
	}

	repo := repository.NewThumbnailProjectRepository(db)
	project := &models.ThumbnailProject{ID: "thumbproj_assets", WorkspaceID: 9931, CreatedBy: 9931, Name: "Assets", CanvasWidth: 1920, CanvasHeight: 1080}
	if err := repo.Create(context.Background(), project); err != nil {
		t.Fatal(err)
	}

	// An image entering the canvas registers the asset IN the same
	// transaction as the revision — no separate client call. This is the
	// durable source the open resolver serves after any restart.
	snapshotWithImage := `{"canvas":{"background":"#30305a"},"objects":[{"id":"img-1","type":"image","media_id":"00000000-0000-4000-8000-000000000931"}]}`
	if _, err := repo.SaveSnapshot(context.Background(), 9931, project.ID, models.ThumbnailProjectSnapshot{
		SchemaVersion: 1, SnapshotJSON: []byte(snapshotWithImage), RendererVersion: "renderer-1", BaseVersion: 1,
	}, 9931); err != nil {
		t.Fatalf("snapshot with image: %v", err)
	}
	assets, err := repo.ListAssets(context.Background(), 9931, project.ID)
	if err != nil {
		t.Fatalf("ListAssets: %v", err)
	}
	if len(assets) != 1 {
		t.Fatalf("expected exactly one registered asset, got %d: %+v", len(assets), assets)
	}
	if assets[0].MediaID != "00000000-0000-0000-0000-000000000931" || assets[0].Role != models.ThumbnailProjectAssetRoleForeground {
		t.Fatalf("unexpected asset: %+v", assets[0])
	}
	if assets[0].ObjectID == nil || *assets[0].ObjectID != "img-1" {
		t.Fatalf("object_id not captured: %+v", assets[0])
	}

	// Saving again with the SAME media (new object) upserts, never
	// duplicates (PK project_id, media_id, role).
	snapshotRenamed := `{"canvas":{"background":"#30305a"},"objects":[{"id":"img-2","type":"image","media_id":"00000000-0000-4000-8000-000000000931"}]}`
	if _, err := repo.SaveSnapshot(context.Background(), 9931, project.ID, models.ThumbnailProjectSnapshot{
		SchemaVersion: 1, SnapshotJSON: []byte(snapshotRenamed), RendererVersion: "renderer-1", BaseVersion: 2,
	}, 9931); err != nil {
		t.Fatalf("second snapshot: %v", err)
	}
	assets, err = repo.ListAssets(context.Background(), 9931, project.ID)
	if err != nil {
		t.Fatalf("ListAssets after second save: %v", err)
	}
	if len(assets) != 1 || assets[0].ObjectID == nil || *assets[0].ObjectID != "img-2" {
		t.Fatalf("upsert did not refresh object_id: %+v", assets)
	}

	// A foreign (non-workspace) media reference never links and never
	// fails the save — the snapshot is still persisted untouched.
	foreignSnapshot := `{"canvas":{},"objects":[{"id":"img-x","type":"image","media_id":"00000000-0000-4000-8000-000000000999"}]}`
	if _, err := repo.SaveSnapshot(context.Background(), 9931, project.ID, models.ThumbnailProjectSnapshot{
		SchemaVersion: 1, SnapshotJSON: []byte(foreignSnapshot), RendererVersion: "renderer-1", BaseVersion: 3,
	}, 9931); err != nil {
		t.Fatalf("snapshot with foreign media must still save: %v", err)
	}
	assets, err = repo.ListAssets(context.Background(), 9931, project.ID)
	if err != nil {
		t.Fatalf("ListAssets after foreign media: %v", err)
	}
	if len(assets) != 1 || assets[0].MediaID != "00000000-0000-0000-0000-000000000931" {
		t.Fatalf("foreign media leaked into assets: %+v", assets)
	}
}

// TestThumbnailProjectRepository_IntegrationRestartPersistence certifies
// DoD Test 2 "Riavvio completo" at the persistence layer: after a full
// API/worker restart the project, its current revision (with the image
// object), and the registered assets must all be re-readable from
// Postgres alone — nothing lives in process memory. MinIO media is
// re-resolvable because presigned URLs are minted per request from the
// persisted upload_key (covered by the media resolver handler tests).
func TestThumbnailProjectRepository_IntegrationRestartPersistence(t *testing.T) {
	db, cleanup := postgres.StartTestPostgres(t)
	defer cleanup()
	if err := database.RunMigrationsUpTo(db, 97); err != nil {
		t.Fatalf("migrations: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO users (id, email, name) VALUES (9951, 'thumb-restart@example.test', 'Thumb Restart')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO workspaces (id, name, owner_id) VALUES (9951, 'Thumb Restart WS', 9951)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO media_assets (id, user_id, upload_key, content_type, size_bytes, status, sha256, expires_at)
		VALUES ('00000000-0000-0000-0000-000000000951', 9951, 'uploads/9951/restart.png', 'image/png', 32, 'ready', repeat('c', 64), NOW() + INTERVAL '1 day')
	`); err != nil {
		t.Fatal(err)
	}

	// "Process 1": create the project and persist a snapshot with media.
	repo := repository.NewThumbnailProjectRepository(db)
	project := &models.ThumbnailProject{ID: "thumbproj_restart", WorkspaceID: 9951, CreatedBy: 9951, Name: "Restart", CanvasWidth: 1920, CanvasHeight: 1080}
	if err := repo.Create(context.Background(), project); err != nil {
		t.Fatal(err)
	}
	saved, err := repo.SaveSnapshot(context.Background(), 9951, project.ID, models.ThumbnailProjectSnapshot{
		SchemaVersion: 1, SnapshotJSON: []byte(`{"canvas":{"background":"#30305a"},"objects":[{"id":"img-r","type":"image","media_id":"00000000-0000-0000-0000-000000000951","x":0,"y":0,"width":480,"height":270,"scale_x":1,"scale_y":1,"rotation":0}]}`),
		RendererVersion: "go-canvas-v1", BaseVersion: 1,
	}, 9951)
	if err != nil {
		t.Fatalf("save snapshot: %v", err)
	}

	// "Restart": a brand-new repository instance — no in-memory state is
	// carried over; every read below comes from Postgres.
	repo2 := repository.NewThumbnailProjectRepository(db)
	proj, err := repo2.FindByID(context.Background(), 9951, project.ID)
	if err != nil || proj == nil {
		t.Fatalf("restart: FindByID = %+v err=%v", proj, err)
	}
	if proj.Version != 2 || proj.CurrentRevisionID == nil || *proj.CurrentRevisionID != saved.RevisionID {
		t.Fatalf("restart: project state not persisted: %+v", proj)
	}
	rev, err := repo2.FindRevision(context.Background(), 9951, proj.ID, *proj.CurrentRevisionID)
	if err != nil {
		t.Fatalf("restart: FindRevision: %v", err)
	}
	if string(rev.SnapshotJSON) == "" || rev.SnapshotSHA256 == nil || len(rev.SnapshotSHA256) != 32 {
		t.Fatalf("restart: revision not fully persisted: %+v", rev)
	}
	assets, err := repo2.ListAssets(context.Background(), 9951, proj.ID)
	if err != nil {
		t.Fatalf("restart: ListAssets: %v", err)
	}
	if len(assets) != 1 || assets[0].MediaID != "00000000-0000-0000-0000-000000000951" || assets[0].Role != models.ThumbnailProjectAssetRoleForeground {
		t.Fatalf("restart: assets not persisted: %+v", assets)
	}
	// The media row is still ready in the workspace, so the resolver can
	// mint a fresh presigned URL after the restart (stateless per request).
	var status string
	if err := db.QueryRow(`SELECT status FROM media_assets WHERE id = '00000000-0000-0000-0000-000000000951'`).Scan(&status); err != nil || status != "ready" {
		t.Fatalf("restart: media not ready after restart: status=%q err=%v", status, err)
	}
}

// TestThumbnailProjectRepository_IntegrationVersionHistoryRestore certifies
// DoD Test 7 "Versioni": save A → B → C, then restore A. The restore must
// create a NEW revision D whose content equals A, while the history (A, B,
// C) is preserved untouched.
func TestThumbnailProjectRepository_IntegrationVersionHistoryRestore(t *testing.T) {
	db, cleanup := postgres.StartTestPostgres(t)
	defer cleanup()
	if err := database.RunMigrationsUpTo(db, 97); err != nil {
		t.Fatalf("migrations: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO users (id, email, name) VALUES (9952, 'thumb-versions@example.test', 'Thumb Versions')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO workspaces (id, name, owner_id) VALUES (9952, 'Thumb Versions WS', 9952)`); err != nil {
		t.Fatal(err)
	}

	repo := repository.NewThumbnailProjectRepository(db)
	project := &models.ThumbnailProject{ID: "thumbproj_versions", WorkspaceID: 9952, CreatedBy: 9952, Name: "Versions", CanvasWidth: 1920, CanvasHeight: 1080}
	if err := repo.Create(context.Background(), project); err != nil {
		t.Fatal(err)
	}

	snapshotFor := func(background string, version int64) *models.ThumbnailProjectSnapshotResult {
		result, err := repo.SaveSnapshot(context.Background(), 9952, project.ID, models.ThumbnailProjectSnapshot{
			SchemaVersion: 1, SnapshotJSON: []byte(`{"canvas":{"background":"` + background + `"},"objects":[]}`),
			RendererVersion: "go-canvas-v1", BaseVersion: version,
		}, 9952)
		if err != nil {
			t.Fatalf("save %s: %v", background, err)
		}
		return result
	}

	a := snapshotFor("#aaa111", 1) // version 2
	b := snapshotFor("#bbb222", 2) // version 3
	c := snapshotFor("#ccc333", 3) // version 4

	restored, err := repo.RestoreRevision(context.Background(), 9952, project.ID, a.RevisionID, 4, 9952, "go-canvas-v1")
	if err != nil {
		t.Fatalf("restore A: %v", err)
	}
	// D is a NEW revision with content equal to A.
	if restored.RevisionID == a.RevisionID || restored.RevisionNumber != 4 || restored.Version != 5 {
		t.Fatalf("restore did not create revision D: %+v", restored)
	}
	if restored.SnapshotSHA256 != a.SnapshotSHA256 {
		t.Fatalf("restored content differs from A: d=%s a=%s", restored.SnapshotSHA256, a.SnapshotSHA256)
	}

	// History preserved: A, B, C, D all present with their original hashes.
	revisions, err := repo.ListRevisions(context.Background(), 9952, project.ID)
	if err != nil {
		t.Fatalf("ListRevisions: %v", err)
	}
	if len(revisions) != 4 {
		t.Fatalf("expected 4 revisions (A,B,C,D), got %d", len(revisions))
	}
	hashes := map[string]string{}
	for _, r := range revisions {
		hashes[r.ID] = hex.EncodeToString(r.SnapshotSHA256)
	}
	if hashes[a.RevisionID] != a.SnapshotSHA256 ||
		hashes[b.RevisionID] != b.SnapshotSHA256 ||
		hashes[c.RevisionID] != c.SnapshotSHA256 {
		t.Fatalf("history mutated by restore: got A=%q B=%q C=%q", hashes[a.RevisionID], hashes[b.RevisionID], hashes[c.RevisionID])
	}
	if revisions[0].ID != restored.RevisionID {
		t.Fatalf("newest revision should be D, got %s", revisions[0].ID)
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
