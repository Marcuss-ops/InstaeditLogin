//go:build integration

package repository_test

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/database"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
	"github.com/Marcuss-ops/InstaeditLogin/internal/testutil/postgres"
)

func TestContentPackageIntegration_DomainWorkflowAndRecoveryInvariants(t *testing.T) {
	db, cleanup := postgres.StartTestPostgres(t, postgres.WithDatabase("instaedit_content_package_test"))
	defer cleanup()
	if err := database.RunMigrations(db); err != nil {
		t.Fatalf("migrations: %v", err)
	}

	const userID int64 = 98100
	const workspaceID int64 = 98100
	driveAccountID := int64(98101)
	if _, err := db.Exec(`INSERT INTO users (id,email,name) VALUES ($1,$2,$3)`, userID, "content-package@example.test", "Content Package"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO workspaces (id,name,owner_id) VALUES ($1,$2,$3)`, workspaceID, "Content Package Workspace", userID); err != nil {
		t.Fatal(err)
	}
	for id, platform := range map[int64]string{driveAccountID: "google_drive", 98102: "youtube", 98103: "youtube", 98104: "youtube"} {
		if _, err := db.Exec(`INSERT INTO platform_accounts (id,user_id,workspace_id,platform,platform_user_id,username) VALUES ($1,$2,$3,$4,$5,$6)`, id, userID, workspaceID, platform, "user-"+toString(id), "channel-"+toString(id)); err != nil {
			t.Fatal(err)
		}
	}

	ctx := context.Background()
	store := repository.NewContentPackageRepository(db)
	inboxStore := repository.NewDriveInboxRepository(db)
	inbox := &models.DriveInbox{WorkspaceID: workspaceID, DriveAccountID: driveAccountID, FolderID: "folder-functional", Enabled: true}
	if err := inboxStore.CreateInbox(ctx, inbox); err != nil {
		t.Fatalf("create inbox: %v", err)
	}
	item := &models.DriveInboxItem{InboxID: inbox.ID, DriveFileID: "drive-functional-1", Filename: "boxing.mp4", MimeType: "video/mp4", Fingerprint: "sha-1"}
	if err := inboxStore.UpsertInboxItem(ctx, item); err != nil {
		t.Fatalf("first inbox discovery: %v", err)
	}
	for i := 0; i < 4; i++ {
		if err := inboxStore.UpsertInboxItem(ctx, &models.DriveInboxItem{InboxID: inbox.ID, DriveFileID: "drive-functional-1", Filename: "boxing.mp4", MimeType: "video/mp4", Fingerprint: "sha-1"}); err != nil {
			t.Fatalf("repeat inbox discovery %d: %v", i, err)
		}
	}
	items, err := inboxStore.ListInboxItems(ctx, inbox.ID, "ready_for_review")
	if err != nil || len(items) != 1 {
		t.Fatalf("inbox dedupe: items=%d err=%v", len(items), err)
	}

	cover := "cover-functional"
	pkg := &models.ContentPackage{SourceType: "google_drive", DriveAccountID: &driveAccountID, DriveFileID: item.DriveFileID, SourceFilename: item.Filename, SourceFingerprint: item.Fingerprint, SourceLanguage: "it", CurrentCoverMediaID: &cover}
	revision := &models.ContentMetadataRevision{SourceLanguage: "it", Title: "Come iniziare", Description: "Descrizione italiana", Tags: json.RawMessage(`["boxe"]`), CreatedBy: userID}
	claimed, err := inboxStore.ClaimInboxItem(ctx, inbox.ID, item.ID, userID, pkg, revision)
	if err != nil {
		t.Fatalf("claim inbox item: %v", err)
	}
	if claimed.ID == 0 || claimed.DriveFileID != item.DriveFileID {
		t.Fatalf("unexpected claimed package: %+v", claimed)
	}
	if _, err := inboxStore.ClaimInboxItem(ctx, inbox.ID, item.ID, userID, &models.ContentPackage{}, &models.ContentMetadataRevision{}); !errors.Is(err, repository.ErrDriveInboxItemNotFound) {
		t.Fatalf("second claim: want not found/idempotent rejection, got %v", err)
	}

	targets := []*models.ContentPackageTarget{
		{ContentPackageID: pkg.ID, PlatformAccountID: 98102, Language: "it", PrivacyStatus: "public", Enabled: true},
		{ContentPackageID: pkg.ID, PlatformAccountID: 98103, Language: "en", PrivacyStatus: "public", Enabled: true},
		{ContentPackageID: pkg.ID, PlatformAccountID: 98104, Language: "es", PrivacyStatus: "public", Enabled: true},
	}
	storedTargets, err := store.ReplaceTargets(ctx, pkg.ID, pkg.Version, targets)
	if err != nil || len(storedTargets) != 3 {
		t.Fatalf("store targets: count=%d err=%v", len(storedTargets), err)
	}
	pkg.Version++
	if err := store.CreateTranslationBundle(ctx, &models.TranslationBundle{ContentPackageID: pkg.ID, SourceMetadataRevisionID: revision.ID, Provider: "nvidia", Status: "completed", RequestedLanguages: []string{"en", "es"}}, []*models.TranslationEntry{
		{Language: "en", Title: "How to Start Boxing", Description: "English description", Tags: json.RawMessage(`[]`), Origin: "nvidia"},
		{Language: "es", Title: "Cómo empezar a boxear", Description: "Descripción española", Tags: json.RawMessage(`[]`), Origin: "nvidia"},
	}); err != nil {
		t.Fatalf("translation bundle: %v", err)
	}

	resolver := services.NewPublicationResolver(store)
	resolved, err := resolver.ResolveAll(ctx, workspaceID, pkg.ID)
	if err != nil || len(resolved) != 3 {
		t.Fatalf("resolve all: count=%d err=%v", len(resolved), err)
	}
	for _, publication := range resolved {
		if !publication.Ready() {
			t.Fatalf("resolved publication blocked: %+v", publication)
		}
		if publication.Target.Language == "en" && publication.Title != "How to Start Boxing" {
			t.Fatalf("EN resolver title=%q", publication.Title)
		}
		if publication.Target.Language == "es" && publication.Title != "Cómo empezar a boxear" {
			t.Fatalf("ES resolver title=%q", publication.Title)
		}
	}

	scheduledAt := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Second)
	prepareAt := scheduledAt.Add(-time.Hour)
	schedule := &models.ContentSchedule{ContentPackageID: pkg.ID, ScheduledAt: scheduledAt, PrepareAt: prepareAt, Timezone: "Europe/Rome"}
	if err := store.UpsertSchedule(ctx, schedule, pkg.Version); err != nil {
		t.Fatalf("schedule: %v", err)
	}
	pkg.Version++
	staleSchedule := &models.ContentSchedule{ContentPackageID: pkg.ID, ScheduledAt: scheduledAt.Add(3 * time.Hour), PrepareAt: scheduledAt.Add(2 * time.Hour), Timezone: "UTC"}
	if err := store.UpsertSchedule(ctx, staleSchedule, pkg.Version-1); !errors.Is(err, repository.ErrContentPackageVersionConflict) {
		t.Fatalf("stale schedule version: want PACKAGE_CHANGED conflict, got %v", err)
	}
	if got, err := store.ClaimDueSchedules(ctx, "functional-worker", time.Minute, 10); err != nil || len(got) != 0 {
		t.Fatalf("future prepare gate: claimed=%d err=%v", len(got), err)
	}

	for _, publication := range resolved {
		snapshot := &models.PublishSnapshot{ContentScheduleID: schedule.ID, ContentPackageID: pkg.ID, PackageVersion: pkg.Version, TargetAccountID: publication.Target.PlatformAccountID, Language: publication.Target.Language, MetadataRevisionID: publication.MetadataRevision.ID, TranslationBundleID: publication.TranslationBundleID, CoverMediaID: publication.ThumbnailMediaID, Title: publication.Title, Description: publication.Description, Tags: publication.Tags, PrivacyStatus: publication.PrivacyStatus, PublishAt: scheduledAt}
		if err := store.CreatePublishSnapshot(ctx, snapshot); err != nil {
			t.Fatalf("create snapshot %s: %v", publication.Target.Language, err)
		}
		if publication.Target.Language == "it" {
			errCh := make(chan error, 8)
			var wg sync.WaitGroup
			for i := 0; i < 8; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					copy := *snapshot
					errCh <- store.CreatePublishSnapshot(ctx, &copy)
				}()
			}
			wg.Wait()
			close(errCh)
			for duplicateErr := range errCh {
				if duplicateErr != nil {
					t.Fatalf("concurrent duplicate snapshot: %v", duplicateErr)
				}
			}
		}
		originalTitle := snapshot.Title
		snapshot.Title = "mutated caller value"
		if err := store.CreatePublishSnapshot(ctx, snapshot); err != nil {
			t.Fatalf("idempotent snapshot %s: %v", publication.Target.Language, err)
		}
		if snapshot.Title != originalTitle {
			t.Fatalf("snapshot idempotency changed immutable title from %q to %q", originalTitle, snapshot.Title)
		}
	}
	snapshots, err := store.ListPublishSnapshots(ctx, schedule.ID)
	if err != nil || len(snapshots) != 3 {
		t.Fatalf("snapshot uniqueness: count=%d err=%v", len(snapshots), err)
	}
	for _, snapshot := range snapshots {
		if snapshot.PublishAt != scheduledAt || snapshot.CoverMediaID == nil || *snapshot.CoverMediaID != cover {
			t.Fatalf("snapshot parity mismatch: %+v", snapshot)
		}
	}

	if _, err := db.Exec(`UPDATE content_schedules SET prepare_at=NOW() WHERE id=$1`, schedule.ID); err != nil {
		t.Fatal(err)
	}
	claimedSchedules, err := store.ClaimDueSchedules(ctx, "functional-worker", time.Minute, 10)
	if err != nil || len(claimedSchedules) != 1 || claimedSchedules[0].Status != "preparing" {
		t.Fatalf("due prepare claim: schedules=%+v err=%v", claimedSchedules, err)
	}
	if err := store.MarkSchedulePrepared(ctx, schedule.ID, "functional-worker"); err != nil {
		t.Fatalf("mark prepared: %v", err)
	}

	if err := store.CreateMetadataRevision(ctx, &models.ContentMetadataRevision{ContentPackageID: pkg.ID, SourceLanguage: "it", Title: "Titolo sorgente modificato", Description: "Nuova descrizione", Tags: json.RawMessage(`[]`), CreatedBy: userID}, pkg.Version); err != nil {
		t.Fatalf("new metadata revision: %v", err)
	}
	bundle, entries, err := store.ResolveTranslationEntries(ctx, pkg.ID, revision.ID, []string{"en", "es"})
	if err != nil {
		t.Fatal(err)
	}
	if bundle != nil || len(entries) != 0 {
		t.Fatalf("stale translation bundle remained usable: bundle=%+v entries=%v", bundle, entries)
	}
	snapshotsAfterEdit, err := store.ListPublishSnapshots(ctx, schedule.ID)
	if err != nil || len(snapshotsAfterEdit) != 3 || snapshotsAfterEdit[0].Title == "Titolo sorgente modificato" {
		t.Fatalf("snapshot changed after source edit: count=%d err=%v snapshots=%+v", len(snapshotsAfterEdit), err, snapshotsAfterEdit)
	}

	var uploadCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM upload_jobs WHERE metadata->>'content_schedule_id'=$1`, strconv.FormatInt(schedule.ID, 10)).Scan(&uploadCount); err != nil {
		t.Fatal(err)
	}
	if uploadCount != 0 {
		t.Fatalf("package/schedule domain created %d UploadJob rows before preparation worker", uploadCount)
	}
}

func toString(value int64) string { return strconv.FormatInt(value, 10) }
