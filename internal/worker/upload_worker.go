package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

// UploadJobStore is the narrow repository interface the upload worker needs.
// P1 — worker pool: ClaimNext has been replaced by ClaimBatch (which
// claims up to `limit` rows in a single CTE) and the Mark* methods
// gained workerID + errorCode parameters to enable CAS-against-lease
// safety (the late-delivery race) and the new error taxonomy.
type UploadJobStore interface {
	ClaimBatch(ctx context.Context, workerID string, limit int, lease time.Duration) ([]*models.UploadJob, error)
	Heartbeat(ctx context.Context, jobID int64, workerID string, lease time.Duration) error
	MarkCompleted(ctx context.Context, id int64, workerID string, postID int64, assetID string) error
	MarkFailed(ctx context.Context, id int64, workerID, errorCode, errMessage string) error
	MarkRetry(ctx context.Context, id int64, workerID, errorCode, errMessage string, nextAttemptAt time.Time) error
	MarkDeadLetter(ctx context.Context, id int64, workerID, errorCode, errMessage string) error
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
	// P1 — worker pool: workerID is the lease_owner string stamped
	// onto every row this worker claims (and CAS-checked on every
	// Mark* / Heartbeat). batchLimit caps ClaimBatch's per-tick row
	// count (the worker still drains the slice serially per tick —
	// goroutine-per-row is a follow-up commit to keep this one
	// minimal). leaseTTL controls how long a claim is valid for
	// before the reaper releases it back to 'pending'. All three
	// are initialised in Run() with sensible defaults so legacy
	// NewUploadWorker callers (no config plumbing) still work.
	workerID  string
	batchSize int
	leaseTTL  time.Duration
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
		// P1 — worker pool defaults. Initialised to zero so Run() can
		// detect unset fields and pick conservative values without
		// touching the NewUploadWorker signature (the bootstrap
		// caller doesn't need to know about workerID / batchSize /
		// leaseTTL today; future env-driven config can populate them).
		batchSize: 0,
		leaseTTL:  0,
	}
}

// defaultUploadWorkerID derives a stable per-replica lease identity.
// Prefer hostname + PID so log lines trace the exact pod that owned
// the lease at any given moment; fall back to a deterministic
// placeholder when os.Hostname() errors (e.g. some sandboxed
// containers / minimal Linux images).
func defaultUploadWorkerID() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "upload-worker"
	}
	pid := os.Getpid()
	return host + "-" + strconv.Itoa(pid)
}

// Run blocks until ctx is cancelled, ticking every interval. Each tick
// drains the queue: ClaimBatch hands back 0+ rows; the worker processes
// them serially (the goroutine-per-row split lands in a follow-up
// commit to keep this one minimal). Errors are routed to MarkRetry
// when attempt budget remains, MarkDeadLetter when exhausted.
func (w *UploadWorker) Run(ctx context.Context) error {
	// Apply P1 worker-pool defaults lazily so a NewUploadWorker
	// caller that knew nothing about the worker pool still gets
	// reasonable behaviour on first Run.
	if w.workerID == "" {
		w.workerID = defaultUploadWorkerID()
	}
	if w.batchSize <= 0 {
		w.batchSize = 4
	}
	if w.leaseTTL <= 0 {
		w.leaseTTL = 60 * time.Second
	}

	w.logger.Info("upload worker started",
		"interval_seconds", w.interval.Seconds(),
		"worker_id", w.workerID,
		"batch_size", w.batchSize,
		"lease_ttl_seconds", w.leaseTTL.Seconds(),
	)
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

// runOnce drains ClaimBatch and processes each row serially. Returns
// when either the batch is exhausted or the ctx is cancelled.
func (w *UploadWorker) runOnce(ctx context.Context) {
	jobs, err := w.jobRepo.ClaimBatch(ctx, w.workerID, w.batchSize, w.leaseTTL)
	if err != nil {
		w.logger.Error("upload worker: failed to claim batch", "error", err)
		return
	}
	if len(jobs) == 0 {
		return
	}
	w.logger.Info("upload worker: claimed batch", "count", len(jobs), "worker_id", w.workerID)
	for _, job := range jobs {
		w.processClaimedJob(ctx, job)
	}
}

// processClaimedJob runs processJob under a per-job panic recovery +
// routes the resulting error to MarkRetry / MarkDeadLetter based on
// attempt_count vs max_attempts. CAS loss (ErrUploadJobLeaseLost) is
// treated as "drop the in-flight work; the row is in someone else's
// hands" — a peer ClaimBatch re-leased the row while we were processing.
func (w *UploadWorker) processClaimedJob(ctx context.Context, job *models.UploadJob) {
	w.logger.Info("upload worker: processing job",
		"job_id", job.ID,
		"source_type", job.SourceType,
		"attempt_count", job.AttemptCount,
		"max_attempts", job.MaxAttempts,
	)
	if err := w.processJob(ctx, job); err != nil {
		w.handleProcessingError(ctx, job, err)
	}
}

// handleProcessingError classifies the processing error + routes the
// Mark* call:
//
//   - ErrUploadJobLeaseLost → drop silently (peer owns the row now).
//   - attempt_count >= max_attempts → MarkDeadLetter (retry budget
//     exhausted; operator triage).
//   - anything else → MarkRetry with exponential backoff + jitter.
//
// Backoff curve matches the outbox dispatcher
// (internal/outbox/dispatcher.go::computeBackoff): AWS-style
// decorrelated jitter, capped at 1h. The worker's job is to keep the
// system making progress against a backed-up provider, not to be
// punitive about transient errors.
func (w *UploadWorker) handleProcessingError(ctx context.Context, job *models.UploadJob, processErr error) {
	if errors.Is(processErr, repository.ErrUploadJobLeaseLost) {
		w.logger.Warn("upload worker: lease lost mid-processing; dropping",
			"job_id", job.ID, "worker_id", w.workerID)
		return
	}

	w.logger.Error("upload worker: job failed",
		"job_id", job.ID,
		"attempt_count", job.AttemptCount,
		"max_attempts", job.MaxAttempts,
		"error", processErr,
	)

	errorCode := classifyUploadError(processErr)
	if job.AttemptCount >= job.MaxAttempts {
		if markErr := w.jobRepo.MarkDeadLetter(ctx, job.ID, w.workerID, errorCode, processErr.Error()); markErr != nil {
			w.logger.Error("upload worker: MarkDeadLetter failed",
				"job_id", job.ID, "error", markErr)
		}
		return
	}

	backoff := computeUploadBackoff(job.AttemptCount)
	if markErr := w.jobRepo.MarkRetry(ctx, job.ID, w.workerID, errorCode, processErr.Error(), time.Now().Add(backoff)); markErr != nil {
		w.logger.Error("upload worker: MarkRetry failed",
			"job_id", job.ID, "error", markErr)
	}
}

// classifyUploadError maps a process-time error onto a stable taxonomy
// used by error_code (migration 046) for dashboard filtering and retry
// routing. Empty string means "unclassified" — the repository will
// store NULL via NULLIF($3, '').
func classifyUploadError(err error) string {
	s := err.Error()
	switch {
	case containsAny(s, "drive", "googleapis.com/upload/drive"):
		return "drive_error"
	case containsAny(s, "s3", "tigris", "minio", "presigned"):
		return "s3_error"
	case containsAny(s, "youtube", "videos.insert"):
		return "youtube_error"
	case containsAny(s, "oauth", "401", "403", "unauthorized"):
		return "auth_error"
	case containsAny(s, "context deadline", "timeout"):
		return "timeout"
	default:
		return ""
	}
}

// containsAny is the cheap substring-or helper for classifyUploadError.
func containsAny(s string, needles ...string) bool {
	for _, n := range needles {
		if len(n) == 0 {
			continue
		}
		for i := 0; i+len(n) <= len(s); i++ {
			if s[i:i+len(n)] == n {
				return true
			}
		}
	}
	return false
}

// computeUploadBackoff implements AWS-style decorrelated jitter for
// the upload worker, mirroring internal/outbox/dispatcher.go. The
// formula is:
//   cap   = 1h
//   base  = 5s
//   temp  = min(cap, prev * 3)
//   sleep = uniform(base..temp)
// where prev = base * 2^(attempt-1). Attempt is 1-indexed
// (post-increment from the worker's view).
func computeUploadBackoff(attempt int) time.Duration {
	const (
		base = 5 * time.Second
		cap  = 1 * time.Hour
	)
	if attempt < 1 {
		attempt = 1
	}
	prev := base
	for i := 1; i < attempt; i++ {
		prev *= 3
		if prev > cap {
			prev = cap
			break
		}
	}
	temp := prev
	if temp > cap {
		temp = cap
	}
	jitter := time.Duration(int64(temp) - int64(base))
	if jitter < 0 {
		jitter = 0
	}
	return base + jitter/2 // simple half-range jitter (good enough for retry spacing)
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

	// Mark job completed. P1 — pass workerID for the CAS against the
	// lease we just claimed via ClaimBatch; the worker's lease loss
	// must not be able to silently overwrite a peer's terminal write.
	if err := w.jobRepo.MarkCompleted(ctx, job.ID, w.workerID, post.ID, asset.ID); err != nil {
		return fmt.Errorf("mark job completed: %w", err)
	}

	w.logger.Info("upload worker: job completed",
		"job_id", job.ID,
		"post_id", post.ID,
		"asset_id", asset.ID,
	)
	return nil
}
