//go:build integration

package repository_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/lib/pq"

	"github.com/Marcuss-ops/InstaeditLogin/internal/database"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/testutil/postgres"
)

func TestContentPackageIntegration_PublicationStatusFansOutAcrossChannels(t *testing.T) {
	db, cleanup := postgres.StartTestPostgres(t, postgres.WithDatabase("instaedit_content_package_publication_test"))
	defer cleanup()
	if err := database.RunMigrations(db); err != nil {
		t.Fatalf("migrations: %v", err)
	}

	const userID int64 = 98300
	const workspaceID int64 = 98300
	const driveAccountID int64 = 98301
	const firstYouTubeAccountID int64 = 98302
	const secondYouTubeAccountID int64 = 98303

	if _, err := db.Exec(`INSERT INTO users (id,email,name) VALUES ($1,$2,$3)`, userID, "publication-status@example.test", "Publication Status"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO workspaces (id,name,owner_id) VALUES ($1,$2,$3)`, workspaceID, "Publication Status Workspace", userID); err != nil {
		t.Fatal(err)
	}
	for id, platform := range map[int64]string{
		driveAccountID:         "google_drive",
		firstYouTubeAccountID:  "youtube",
		secondYouTubeAccountID: "youtube",
	} {
		if _, err := db.Exec(`INSERT INTO platform_accounts (id,user_id,workspace_id,platform,platform_user_id,username) VALUES ($1,$2,$3,$4,$5,$6)`, id, userID, workspaceID, platform, fmt.Sprintf("channel-%d", id), fmt.Sprintf("channel-%d", id)); err != nil {
			t.Fatal(err)
		}
	}
	for _, accountID := range []int64{firstYouTubeAccountID, secondYouTubeAccountID} {
		if _, err := db.Exec(`INSERT INTO workspace_channels (workspace_id,platform_account_id,enabled) VALUES ($1,$2,true)`, workspaceID, accountID); err != nil {
			t.Fatal(err)
		}
	}

	ctx := context.Background()
	store := repository.NewContentPackageRepository(db)
	driveID := driveAccountID
	pkg := &models.ContentPackage{WorkspaceID: workspaceID, CreatedBy: userID, SourceType: "google_drive", DriveAccountID: &driveID, DriveFileID: "drive-publication-status", SourceFilename: "multi-channel.mp4", SourceLanguage: "it"}
	revision := &models.ContentMetadataRevision{SourceLanguage: "it", Title: "Multi channel", Description: "Description", Tags: json.RawMessage(`[]`), CreatedBy: userID}
	if err := store.CreatePackage(ctx, pkg, revision); err != nil {
		t.Fatalf("create package: %v", err)
	}

	targets, err := store.ReplaceTargets(ctx, pkg.ID, pkg.Version, []*models.ContentPackageTarget{
		{ContentPackageID: pkg.ID, PlatformAccountID: firstYouTubeAccountID, Language: "it", PrivacyStatus: "public", Enabled: true},
		{ContentPackageID: pkg.ID, PlatformAccountID: secondYouTubeAccountID, Language: "it", PrivacyStatus: "unlisted", Enabled: true},
	})
	if err != nil || len(targets) != 2 {
		t.Fatalf("replace targets: count=%d err=%v", len(targets), err)
	}
	pkg.Version++
	scheduledAt := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	schedule := &models.ContentSchedule{ContentPackageID: pkg.ID, ScheduledAt: scheduledAt, PrepareAt: scheduledAt.Add(-30 * time.Minute), Timezone: "UTC"}
	if err := store.UpsertSchedule(ctx, schedule, pkg.Version); err != nil {
		t.Fatalf("schedule: %v", err)
	}
	pkg.Version++
	for _, target := range targets {
		if err := store.CreatePublishSnapshot(ctx, &models.PublishSnapshot{
			ContentScheduleID:  schedule.ID,
			ContentPackageID:   pkg.ID,
			PackageVersion:     pkg.Version,
			TargetAccountID:    target.PlatformAccountID,
			Language:           target.Language,
			MetadataRevisionID: revision.ID,
			Title:              revision.Title,
			Description:        revision.Description,
			Tags:               json.RawMessage(`[]`),
			PrivacyStatus:      target.PrivacyStatus,
			PublishAt:          scheduledAt,
		}); err != nil {
			t.Fatalf("snapshot for account %d: %v", target.PlatformAccountID, err)
		}
	}

	metadata := fmt.Sprintf(`{"content_package_id":"%d","content_schedule_id":"%d"}`, pkg.ID, schedule.ID)
	var uploadJobID int64
	if err := db.QueryRow(`INSERT INTO upload_jobs
		(user_id,workspace_id,source_type,source_id,title,caption,metadata,targets,status,ingest_after,publish_at)
		VALUES ($1,$2,'authenticated_drive','drive-publication-status','Multi channel','Description',$3,$4,'ingest_completed',NOW(),$5)
		RETURNING id`, userID, workspaceID, metadata, json.RawMessage(fmt.Sprintf(`[%d,%d]`, firstYouTubeAccountID, secondYouTubeAccountID)), scheduledAt).Scan(&uploadJobID); err != nil {
		t.Fatalf("upload job: %v", err)
	}
	var postID int64
	if err := db.QueryRow(`INSERT INTO posts (workspace_id,title,caption,status,publish_at,upload_job_id)
		VALUES ($1,'Multi channel','Description','queued',$2,$3) RETURNING id`, workspaceID, scheduledAt, uploadJobID).Scan(&postID); err != nil {
		t.Fatalf("post: %v", err)
	}
	postTargetIDs := make([]int64, 0, 2)
	for _, accountID := range []int64{firstYouTubeAccountID, secondYouTubeAccountID} {
		var postTargetID int64
		if err := db.QueryRow(`INSERT INTO post_targets (post_id,platform_account_id,status) VALUES ($1,$2,'queued') RETURNING id`, postID, accountID).Scan(&postTargetID); err != nil {
			t.Fatalf("post target %d: %v", accountID, err)
		}
		postTargetIDs = append(postTargetIDs, postTargetID)
		if _, err := db.Exec(`INSERT INTO youtube_target_publications (upload_job_id,post_target_id,platform_account_id,youtube_upload_status,desired_privacy,publish_at) VALUES ($1,$2,$3,'youtube_uploaded','public',$4)`, uploadJobID, postTargetID, accountID, scheduledAt); err != nil {
			t.Fatalf("youtube publication %d: %v", accountID, err)
		}
	}

	statuses, err := store.ListPublicationStatuses(ctx, pkg.ID)
	if err != nil || len(statuses) != 2 {
		t.Fatalf("initial publication statuses: count=%d err=%v", len(statuses), err)
	}
	for _, status := range statuses {
		if status.UploadJobID == nil || status.PostID == nil || status.PostTargetID == nil || status.TargetStatus != "queued" {
			t.Fatalf("incomplete execution handoff for account %d: %+v", status.TargetAccountID, status)
		}
		if status.PublishedAt != nil {
			t.Fatalf("queued target already reported published: %+v", status)
		}
	}

	if _, err := db.Exec(`UPDATE post_targets SET status='published', published_at=NOW() WHERE id = ANY($1)`, pq.Array(postTargetIDs)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE youtube_target_publications SET published_at=NOW(), youtube_video_id='video-' || post_target_id::text WHERE post_target_id = ANY($1)`, pq.Array(postTargetIDs)); err != nil {
		t.Fatal(err)
	}
	statuses, err = store.ListPublicationStatuses(ctx, pkg.ID)
	if err != nil || len(statuses) != 2 {
		t.Fatalf("published publication statuses: count=%d err=%v", len(statuses), err)
	}
	for _, status := range statuses {
		if status.TargetStatus != "published" || status.PublishedAt == nil || status.YouTubeVideoID == nil {
			t.Fatalf("published target not reflected for account %d: %+v", status.TargetAccountID, status)
		}
	}
	if err := store.SyncPublicationState(ctx, pkg.ID); err != nil {
		t.Fatalf("sync package publication state: %v", err)
	}
	var packageState string
	if err := db.QueryRow(`SELECT state FROM content_packages WHERE id=$1`, pkg.ID).Scan(&packageState); err != nil {
		t.Fatalf("read package state: %v", err)
	}
	if packageState != string(models.ContentPackageStatePublished) {
		t.Fatalf("package state = %q, want %q", packageState, models.ContentPackageStatePublished)
	}
}
