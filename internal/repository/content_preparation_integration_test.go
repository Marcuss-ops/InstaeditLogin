//go:build integration

package repository_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/database"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/testutil/postgres"
	"github.com/Marcuss-ops/InstaeditLogin/internal/worker"
)

type preparationUploadSink struct {
	job   *models.UploadJob
	count int
}

func (s *preparationUploadSink) Create(job *models.UploadJob) error {
	s.count++
	copy := *job
	copy.ID = int64(s.count)
	s.job = &copy
	return nil
}

func (s *preparationUploadSink) FindByScheduleID(context.Context, int64) (*models.UploadJob, error) {
	return s.job, nil
}

func TestContentPreparationIntegration_DeferredGateSnapshotAndIdempotentHandoff(t *testing.T) {
	db, cleanup := postgres.StartTestPostgres(t, postgres.WithDatabase("instaedit_content_preparation_test"))
	defer cleanup()
	if err := database.RunMigrations(db); err != nil {
		t.Fatalf("migrations: %v", err)
	}

	const userID int64 = 98200
	const workspaceID int64 = 98200
	if _, err := db.Exec(`INSERT INTO users (id,email,name) VALUES ($1,$2,$3)`, userID, "preparation@example.test", "Preparation"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO workspaces (id,name,owner_id) VALUES ($1,$2,$3)`, workspaceID, "Preparation Workspace", userID); err != nil {
		t.Fatal(err)
	}
	for _, accountID := range []int64{98201, 98202, 98203} {
		if _, err := db.Exec(`INSERT INTO platform_accounts (id,user_id,workspace_id,platform,platform_user_id,username) VALUES ($1,$2,$3,'youtube',$4,$5)`, accountID, userID, workspaceID, "channel-"+toString(accountID), "channel-"+toString(accountID)); err != nil {
			t.Fatal(err)
		}
	}

	ctx := context.Background()
	store := repository.NewContentPackageRepository(db)
	cover := "preparation-cover"
	pkg := &models.ContentPackage{WorkspaceID: workspaceID, CreatedBy: userID, SourceType: "google_drive", DriveFileID: "drive-preparation-1", SourceFilename: "preparation.mp4", SourceFingerprint: "sha-preparation", SourceLanguage: "it", CurrentCoverMediaID: &cover}
	revision := &models.ContentMetadataRevision{SourceLanguage: "it", Title: "Titolo IT", Description: "Descrizione IT", Tags: json.RawMessage(`[]`), CreatedBy: userID}
	if err := store.CreatePackage(ctx, pkg, revision); err != nil {
		t.Fatalf("create package: %v", err)
	}
	targets := []*models.ContentPackageTarget{
		{ContentPackageID: pkg.ID, PlatformAccountID: 98201, Language: "it", PrivacyStatus: "public", Enabled: true},
		{ContentPackageID: pkg.ID, PlatformAccountID: 98202, Language: "en", PrivacyStatus: "public", Enabled: true},
		{ContentPackageID: pkg.ID, PlatformAccountID: 98203, Language: "es", PrivacyStatus: "public", Enabled: true},
	}
	if _, err := store.ReplaceTargets(ctx, pkg.ID, pkg.Version, targets); err != nil {
		t.Fatalf("targets: %v", err)
	}
	pkg.Version++
	if err := store.CreateTranslationBundle(ctx, &models.TranslationBundle{ContentPackageID: pkg.ID, SourceMetadataRevisionID: revision.ID, Provider: "nvidia", Status: "completed", RequestedLanguages: []string{"en", "es"}}, []*models.TranslationEntry{
		{Language: "en", Title: "Title EN", Description: "Description EN", Tags: json.RawMessage(`[]`), Origin: "nvidia"},
		{Language: "es", Title: "Title ES", Description: "Description ES", Tags: json.RawMessage(`[]`), Origin: "nvidia"},
	}); err != nil {
		t.Fatalf("translations: %v", err)
	}

	scheduledAt := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Second)
	schedule := &models.ContentSchedule{ContentPackageID: pkg.ID, ScheduledAt: scheduledAt, PrepareAt: scheduledAt.Add(-time.Hour), Timezone: "Europe/Rome"}
	if err := store.UpsertSchedule(ctx, schedule, pkg.Version); err != nil {
		t.Fatalf("schedule: %v", err)
	}
	sink := &preparationUploadSink{}
	prep := worker.NewContentPreparationWorker(store, sink, "functional-preparation-worker", worker.ContentPreparationWorkerOptions{Interval: time.Hour, LeaseTTL: time.Minute, BatchSize: 10}, slog.Default())
	if err := prep.RunOnce(ctx); err != nil {
		t.Fatalf("early preparation tick: %v", err)
	}
	if sink.count != 0 {
		t.Fatalf("future schedule created %d upload jobs before prepare_at", sink.count)
	}

	if _, err := db.Exec(`UPDATE content_schedules SET prepare_at=NOW() WHERE id=$1`, schedule.ID); err != nil {
		t.Fatal(err)
	}
	if err := prep.RunOnce(ctx); err != nil {
		t.Fatalf("due preparation tick: %v", err)
	}
	if sink.count != 1 || sink.job == nil {
		t.Fatalf("due preparation handoff: count=%d job=%+v", sink.count, sink.job)
	}
	if sink.job.SourceID != pkg.DriveFileID || sink.job.PublishAt == nil || !sink.job.PublishAt.Equal(scheduledAt) || !sink.job.IngestAfter.Before(*sink.job.PublishAt) {
		t.Fatalf("handoff cursors/source mismatch: %+v", sink.job)
	}
	var envelope struct {
		Snapshots map[string]struct {
			Title            string `json:"title"`
			PrivacyStatus    string `json:"privacy_status"`
			ThumbnailMediaID string `json:"thumbnail_media_id"`
		} `json:"publish_snapshots"`
	}
	if err := json.Unmarshal(sink.job.Metadata, &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Snapshots) != 3 || envelope.Snapshots["98202"].Title != "Title EN" || envelope.Snapshots["98202"].ThumbnailMediaID != cover {
		t.Fatalf("snapshot handoff metadata mismatch: %+v", envelope.Snapshots)
	}
	snapshots, err := store.ListPublishSnapshots(ctx, schedule.ID)
	if err != nil || len(snapshots) != 3 {
		t.Fatalf("snapshots: count=%d err=%v", len(snapshots), err)
	}

	if err := prep.RunOnce(ctx); err != nil {
		t.Fatalf("idempotent second tick: %v", err)
	}
	if sink.count != 1 {
		t.Fatalf("second preparation created a duplicate upload job: %d", sink.count)
	}
	currentSchedule, err := store.FindSchedule(ctx, pkg.ID)
	if err != nil || currentSchedule == nil || currentSchedule.Status != "ready_to_publish" {
		t.Fatalf("schedule after preparation: %+v err=%v", currentSchedule, err)
	}
}
