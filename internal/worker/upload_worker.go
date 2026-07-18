package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// UploadJobStore is the narrow repository interface the upload worker needs.
type UploadJobStore interface {
	ClaimNext() (*models.UploadJob, error)
	MarkCompleted(id int64, postID int64, assetID string) error
	MarkFailed(id int64, errMessage string) error
}

// UploadMediaStore is the narrow media asset repository interface.
type UploadMediaStore interface {
	Create(asset *models.MediaAsset) error
	MarkReady(id, sha256 string, sizeBytes int64, contentType string) error
	MarkFailed(id, reason string) error
	// MarkFailedWithReason: same as pkg/api MediaStore — caller passes
	// `cause` so the persist failure path emits a structured log
	// line. Replaces the historical `_ = store.MarkFailed(id, err.Error())`
	// pattern that silently lost errors on the failure-of-the-failure.
	MarkFailedWithReason(id, reason string, cause error) error
}

// UploadPostStore is the narrow post repository interface.
type UploadPostStore interface {
	Create(post *models.Post, targets []*models.PostTarget) error
	PublishPost(postID int64) error
}

// UploadUserStore resolves platform accounts for target validation.
type UploadUserStore interface {
	FindPlatformAccountByID(id int64) (*models.PlatformAccount, error)
}

// UploadWorker processes upload_jobs in the background. It downloads
// videos from public or authenticated Google Drive, uploads them to S3,
// creates posts + targets, and triggers publishing. Jobs survive server
// restarts because they are persisted in the upload_jobs table.
type UploadWorker struct {
	jobRepo       UploadJobStore
	mediaStore    UploadMediaStore
	postStore     UploadPostStore
	userRepo      UploadUserStore
	storage       services.StorageProvider
	capRouter     *services.CapabilityRouter
	vault         credentials.VaultAPI
	interval      time.Duration
	logger        *slog.Logger
	uploadTimeout time.Duration
}

// NewUploadWorker wires a new UploadWorker.
func NewUploadWorker(
	jobRepo UploadJobStore,
	mediaStore UploadMediaStore,
	postStore UploadPostStore,
	userRepo UploadUserStore,
	storage services.StorageProvider,
	capRouter *services.CapabilityRouter,
	vault credentials.VaultAPI,
	interval time.Duration,
	logger *slog.Logger,
) *UploadWorker {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &UploadWorker{
		jobRepo:       jobRepo,
		mediaStore:    mediaStore,
		postStore:     postStore,
		userRepo:      userRepo,
		storage:       storage,
		capRouter:     capRouter,
		vault:         vault,
		interval:      interval,
		logger:        logger,
		uploadTimeout: 30 * time.Minute,
	}
}

// Run blocks until ctx is cancelled, ticking every interval.
func (w *UploadWorker) Run(ctx context.Context) error {
	w.logger.Info("upload worker started", "interval_seconds", w.interval.Seconds())
	defer w.logger.Info("upload worker stopped")

	w.runOnce(ctx)

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			w.runOnce(ctx)
		}
	}
}

func (w *UploadWorker) runOnce(ctx context.Context) {
	job, err := w.jobRepo.ClaimNext()
	if err != nil {
		w.logger.Error("upload worker: failed to claim next job", "error", err)
		return
	}
	if job == nil {
		return
	}

	w.logger.Info("upload worker: processing job", "job_id", job.ID, "source_type", job.SourceType)
	if err := w.processJob(ctx, job); err != nil {
		w.logger.Error("upload worker: job failed", "job_id", job.ID, "error", err)
		if markErr := w.jobRepo.MarkFailed(job.ID, err.Error()); markErr != nil {
			w.logger.Error("upload worker: failed to mark job failed", "job_id", job.ID, "error", markErr)
		}
	}
}

func (w *UploadWorker) processJob(ctx context.Context, job *models.UploadJob) error {
	// Resolve the Drive importer capability.
	provider, ok := w.capRouter.Get("google-drive")
	if !ok {
		return fmt.Errorf("google drive provider not configured")
	}
	importer, ok := provider.(services.DriveImporter)
	if !ok {
		return fmt.Errorf("google drive provider misconfigured")
	}

	// Download the file.
	var downloadResp *http.Response
	var err error
	switch job.SourceType {
	case models.UploadJobSourceAuthenticatedDrive:
		if job.DriveAccountID == nil {
			return fmt.Errorf("authenticated drive source requires drive_account_id")
		}
		oauthToken, tokenErr := w.vault.Renew(ctx, *job.DriveAccountID, models.TokenTypeBearer,
			func(ctx context.Context, refreshToken string) (*models.TokenData, error) {
				return importer.RefreshOAuthToken(ctx, refreshToken)
			})
		if tokenErr != nil {
			return fmt.Errorf("refresh drive token: %w", tokenErr)
		}
		downloadResp, err = importer.DownloadFile(ctx, oauthToken.AccessToken, job.SourceID)
	case models.UploadJobSourcePublicDrive:
		downloadResp, err = importer.DownloadPublicFile(ctx, job.SourceID)
	default:
		return fmt.Errorf("unsupported source type: %s", job.SourceType)
	}
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer downloadResp.Body.Close()

	contentType := downloadResp.Header.Get("Content-Type")
	sizeBytes := downloadResp.ContentLength
	if sizeBytes <= 0 {
		return fmt.Errorf("drive file size is unknown or zero; cannot import")
	}

	// Build S3 key and create pending media asset.
	key := services.BuildUploadKey(job.UserID, job.SourceID)
	asset := &models.MediaAsset{
		UserID:      job.UserID,
		UploadKey:   key,
		ContentType: contentType,
		SizeBytes:   sizeBytes,
		Status:      models.MediaAssetStatusPending,
		ExpiresAt:   time.Now().Add(7 * 24 * time.Hour),
	}
	if err := w.mediaStore.Create(asset); err != nil {
		return fmt.Errorf("create media asset: %w", err)
	}

	// Sign S3 PUT and stream.
	grant, err := w.storage.SignUpload(ctx, job.UserID, key, contentType, sizeBytes, 15*time.Minute)
	if err != nil {
		_ = w.mediaStore.MarkFailedWithReason(asset.ID, err.Error(), err)
		return fmt.Errorf("sign s3 upload: %w", err)
	}

	uploadReq, err := http.NewRequestWithContext(ctx, http.MethodPut, grant.UploadURL, downloadResp.Body)
	if err != nil {
		_ = w.mediaStore.MarkFailedWithReason(asset.ID, err.Error(), err)
		return fmt.Errorf("build s3 upload request: %w", err)
	}
	uploadReq.Header.Set("Content-Type", contentType)
	uploadReq.ContentLength = sizeBytes

	s3Client := &http.Client{Timeout: w.uploadTimeout}
	uploadResp, err := s3Client.Do(uploadReq)
	if err != nil {
		_ = w.mediaStore.MarkFailedWithReason(asset.ID, err.Error(), err)
		return fmt.Errorf("upload to s3: %w", err)
	}
	uploadResp.Body.Close()
	if uploadResp.StatusCode >= 300 {
		reason := fmt.Sprintf("s3 upload returned %d", uploadResp.StatusCode)
		_ = w.mediaStore.MarkFailedWithReason(asset.ID, reason, errors.New(reason))
		return fmt.Errorf("%s", reason)
	}

	// Verify upload.
	verifiedContentType, verifiedSize, err := w.storage.VerifyUpload(ctx, key)
	if err != nil {
		_ = w.mediaStore.MarkFailedWithReason(asset.ID, err.Error(), err)
		return fmt.Errorf("verify s3 upload: %w", err)
	}
	if err := w.mediaStore.MarkReady(asset.ID, "", verifiedSize, verifiedContentType); err != nil {
		return fmt.Errorf("mark media asset ready: %w", err)
	}

	// Create post with targets. The optional job.ScheduledAt (migration
	// 037) is propagated into post.ScheduledAt so the existing
	// publish_worker `WHERE scheduled_at <= NOW()` predicate gates the
	// publish until the right time. Nil => existing immediate-publish
	// behaviour is preserved for legacy sync-style async calls.
	mediaURL := w.storage.AssetURL(key)
	post := &models.Post{
		WorkspaceID: job.WorkspaceID,
		Title:       job.Title,
		Caption:     job.Caption,
		MediaURL:    mediaURL,
		Status:      models.PostStatusQueued,
		ScheduledAt: job.ScheduledAt,
	}
	targets := make([]*models.PostTarget, 0, len(job.Targets))
	for _, accountID := range job.Targets {
		targets = append(targets, &models.PostTarget{
			PlatformAccountID: accountID,
			Status:            models.PostStatusQueued,
		})
	}
	if err := w.postStore.Create(post, targets); err != nil {
		return fmt.Errorf("create post: %w", err)
	}

	// Trigger publishing only for jobs that should publish NOW.
	// Future-scheduled jobs (job.ScheduledAt > now) stay in the
	// `status='queued'` state and the publish_worker picks them up
	// when scheduled_at <= now(). Calling PublishPost on a future
	// post would race the scheduler and risk an out-of-order publish.
	if job.ScheduledAt == nil || !job.ScheduledAt.After(time.Now()) {
		if err := w.postStore.PublishPost(post.ID); err != nil {
			return fmt.Errorf("trigger publish: %w", err)
		}
	} else {
		w.logger.Info("upload worker: post scheduled for future publish",
			"job_id", job.ID, "post_id", post.ID, "scheduled_at", job.ScheduledAt.Format(time.RFC3339))
	}

	// Mark job completed.
	if err := w.jobRepo.MarkCompleted(job.ID, post.ID, asset.ID); err != nil {
		return fmt.Errorf("mark job completed: %w", err)
	}

	w.logger.Info("upload worker: job completed",
		"job_id", job.ID,
		"post_id", post.ID,
		"asset_id", asset.ID,
	)
	return nil
}
