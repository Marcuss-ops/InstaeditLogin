package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

type ContentPreparationStore interface {
	repository.ContentPackageStore
}

type ScheduledUploadLookup interface {
	Create(job *models.UploadJob) error
	FindByScheduleID(ctx context.Context, scheduleID int64) (*models.UploadJob, error)
}

type ContentPreparationWorkerOptions struct {
	Interval  time.Duration
	LeaseTTL  time.Duration
	BatchSize int
}

type ContentPreparationWorker struct {
	store    ContentPreparationStore
	uploads  ScheduledUploadLookup
	resolver *services.PublicationResolver
	workerID string
	opts     ContentPreparationWorkerOptions
	logger   *slog.Logger
}

func NewContentPreparationWorker(store ContentPreparationStore, uploads ScheduledUploadLookup, workerID string, opts ContentPreparationWorkerOptions, logger *slog.Logger) *ContentPreparationWorker {
	if opts.Interval <= 0 {
		opts.Interval = 10 * time.Second
	}
	if opts.LeaseTTL <= 0 {
		opts.LeaseTTL = 5 * time.Minute
	}
	if opts.BatchSize <= 0 || opts.BatchSize > 100 {
		opts.BatchSize = 10
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &ContentPreparationWorker{store: store, uploads: uploads, resolver: services.NewPublicationResolver(store), workerID: workerID, opts: opts, logger: logger}
}

func (w *ContentPreparationWorker) RunOnce(ctx context.Context) error {
	schedules, err := w.store.ClaimDueSchedules(ctx, w.workerID, w.opts.LeaseTTL, w.opts.BatchSize)
	if err != nil {
		return err
	}
	for _, schedule := range schedules {
		if err := w.prepareSchedule(ctx, schedule); err != nil {
			w.logger.Error("content preparation failed", "schedule_id", schedule.ID, "error", err)
		}
	}
	return nil
}

func (w *ContentPreparationWorker) prepareSchedule(ctx context.Context, schedule *models.ContentSchedule) error {
	pkg, err := w.store.FindPackageByID(ctx, schedule.ContentPackageID)
	if err != nil {
		_ = w.store.MarkScheduleRetry(ctx, schedule.ID, w.workerID, time.Now().Add(time.Minute), err.Error())
		return err
	}
	if pkg.Version != schedule.PackageVersion {
		reason := "package_version_conflict"
		_ = w.store.AppendPublicationEvent(ctx, &models.PublicationEvent{
			ContentPackageID:  pkg.ID,
			ContentScheduleID: &schedule.ID,
			Stage:             "preparation",
			EventType:         "BLOCKED",
			ErrorCode:         &reason,
			Message:           contentPackageStringPtr("package changed after scheduling"),
		})
		return w.store.MarkScheduleBlocked(ctx, schedule.ID, w.workerID, reason)
	}
	resolved, err := w.resolver.ResolveAll(ctx, pkg.WorkspaceID, pkg.ID)
	if err != nil {
		_ = w.store.MarkScheduleRetry(ctx, schedule.ID, w.workerID, time.Now().Add(time.Minute), err.Error())
		return err
	}
	if len(resolved) == 0 {
		return w.blockSchedule(ctx, schedule, "targets_missing")
	}
	for _, publication := range resolved {
		if !publication.Ready() {
			reason := "publication_blocked"
			if len(publication.Blockers) > 0 && publication.Blockers[0].Code != "" {
				reason = publication.Blockers[0].Code
			}
			return w.blockSchedule(ctx, schedule, reason)
		}
		snapshot := &models.PublishSnapshot{ContentScheduleID: schedule.ID, ContentPackageID: pkg.ID, PackageVersion: pkg.Version, TargetAccountID: publication.Target.PlatformAccountID, Language: publication.Target.Language, MetadataRevisionID: publication.MetadataRevision.ID, TranslationBundleID: publication.TranslationBundleID, CoverMediaID: publication.ThumbnailMediaID, Title: publication.Title, Description: publication.Description, Tags: publication.Tags, PrivacyStatus: publication.PrivacyStatus, PublishAt: schedule.ScheduledAt}
		if err := w.store.CreatePublishSnapshot(ctx, snapshot); err != nil {
			_ = w.store.MarkScheduleRetry(ctx, schedule.ID, w.workerID, time.Now().Add(time.Minute), err.Error())
			return err
		}
	}
	if existing, lookupErr := w.uploads.FindByScheduleID(ctx, schedule.ID); lookupErr != nil {
		return w.store.MarkScheduleRetry(ctx, schedule.ID, w.workerID, time.Now().Add(time.Minute), lookupErr.Error())
	} else if existing != nil {
		_ = w.store.MarkSchedulePrepared(ctx, schedule.ID, w.workerID)
		return nil
	}
	targetIDs := make([]int64, 0, len(resolved))
	snapshots := make(map[string]any, len(resolved))
	defaultPrivacy := "private"
	for _, publication := range resolved {
		targetIDs = append(targetIDs, publication.Target.PlatformAccountID)
		if publication.PrivacyStatus != "" {
			defaultPrivacy = publication.PrivacyStatus
		}
		snapshots[fmt.Sprint(publication.Target.PlatformAccountID)] = map[string]any{"title": publication.Title, "description": publication.Description, "tags": publication.Tags, "thumbnail_media_id": publication.ThumbnailMediaID, "language": publication.Target.Language, "privacy_status": publication.PrivacyStatus}
	}
	metadata, err := json.Marshal(map[string]any{"source_language": pkg.SourceLanguage, "content_package_id": pkg.ID, "content_schedule_id": schedule.ID, "publish_snapshots": snapshots})
	if err != nil {
		return err
	}
	job := &models.UploadJob{UserID: pkg.CreatedBy, WorkspaceID: pkg.WorkspaceID, SourceType: models.UploadJobSourceAuthenticatedDrive, SourceID: pkg.DriveFileID, DriveAccountID: pkg.DriveAccountID, Title: resolved[0].Title, Caption: resolved[0].Description, Metadata: metadata, Targets: targetIDs, Status: models.UploadJobStatusPending, DefaultPrivacyLevel: defaultPrivacy, IngestAfter: schedule.PrepareAt, PublishAt: &schedule.ScheduledAt}
	if err := w.uploads.Create(job); err != nil {
		if existing, lookupErr := w.uploads.FindByScheduleID(ctx, schedule.ID); lookupErr == nil && existing != nil {
			_ = w.store.MarkSchedulePrepared(ctx, schedule.ID, w.workerID)
			return nil
		} else if lookupErr != nil {
			_ = w.store.MarkScheduleRetry(ctx, schedule.ID, w.workerID, time.Now().Add(time.Minute), lookupErr.Error())
			return lookupErr
		}
		_ = w.store.MarkScheduleRetry(ctx, schedule.ID, w.workerID, time.Now().Add(time.Minute), err.Error())
		return err
	}
	_ = w.store.AppendPublicationEvent(ctx, &models.PublicationEvent{ContentPackageID: pkg.ID, ContentScheduleID: &schedule.ID, Stage: "preparation", EventType: "SNAPSHOT_FROZEN"})
	if err := w.store.MarkSchedulePrepared(ctx, schedule.ID, w.workerID); err != nil {
		return err
	}
	return nil
}

func contentPackageStringPtr(value string) *string {
	return &value
}

func (w *ContentPreparationWorker) blockSchedule(ctx context.Context, schedule *models.ContentSchedule, reason string) error {
	_ = w.store.AppendPublicationEvent(ctx, &models.PublicationEvent{ContentPackageID: schedule.ContentPackageID, ContentScheduleID: &schedule.ID, Stage: "preparation", EventType: "BLOCKED", ErrorCode: &reason})
	return w.store.MarkScheduleBlocked(ctx, schedule.ID, w.workerID, reason)
}

func (w *ContentPreparationWorker) Run(ctx context.Context) error {
	if err := w.RunOnce(ctx); err != nil {
		w.logger.Error("initial content preparation tick failed", "error", err)
	}
	ticker := time.NewTicker(w.opts.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := w.RunOnce(ctx); err != nil {
				w.logger.Error("content preparation tick failed", "error", err)
			}
		}
	}
}
