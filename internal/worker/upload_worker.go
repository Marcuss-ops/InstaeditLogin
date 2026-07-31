package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
	"github.com/Marcuss-ops/InstaeditLogin/pkg/metrics"
)

// UploadJobStore is the narrow repository interface the upload worker needs.
// P1 step 2 — ingest + upload pools:
//   - ClaimBatch          ingest pool claims status IN ('pending','retry_wait').
//   - ClaimBatchForPublish upload pool claims status = 'ready_to_publish' (the
//     ingest pool's MarkIngested output).
//   - MarkIngested         ingest pool's terminal-for-ingest: leased →
//     ready_to_publish + asset_id stamp + total_bytes/progress_bytes
//     set to the streamed size.
//   - ReclaimExpiredLeases reaper: returned leased rows past lease_expires_at
//     (5-min heartbeat grace window) back to 'pending'. Called both
//     synchronously on startup (ReclaimOnStart) and on a background
//     ticker cadence.
func storageBucket(provider services.StorageProvider) string {
	bucketProvider, ok := provider.(services.BucketProvider)
	if !ok {
		return ""
	}
	return bucketProvider.Bucket()
}

type UploadJobStore interface {
	ClaimBatch(ctx context.Context, workerID string, limit int, lease time.Duration) ([]*models.UploadJob, error)
	ClaimBatchForPublish(ctx context.Context, workerID string, limit int, lease time.Duration) ([]*models.UploadJob, error)
	Heartbeat(ctx context.Context, jobID int64, workerID string, lease time.Duration) error
	MarkCompleted(ctx context.Context, id int64, workerID string, postID int64, assetID string) error
	MarkFailed(ctx context.Context, id int64, workerID, errorCode, errMessage string) error
	MarkRetry(ctx context.Context, id int64, workerID, errorCode, errMessage string, nextAttemptAt time.Time) error
	MarkDeadLetter(ctx context.Context, id int64, workerID, errorCode, errMessage string) error
	MarkIngested(ctx context.Context, id int64, workerID, assetID string, totalBytes int64) error
	ReclaimExpiredLeases(ctx context.Context, maxRows int) (int64, error)
	// P1#5 — YouTube resumable session persistence. Called per-chunk
	// (Save) and once at terminal-success / session-expired (Clear).
	SaveYouTubeSession(ctx context.Context, id int64, workerID, sessionURI string, offset, chunkSize int64, expiresAt time.Time) error
	ClearYouTubeSession(ctx context.Context, id int64, workerID string) error
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
	// SetTargetStatus flips one post_target row's status atomically
	// with an optional error_message stamp. Used by the upload
	// worker's per-target phase to route a single target to
	// status='blocked_auth' on a YouTube channel-binding mismatch
	// (P0#3 channel-binding guard) WITHOUT touching the row's other
	// lifecycle columns (platform_post_id, provider_state, etc —
	// those stay whatever the prior failed/queued write left them at).
	// Caller passes targetID directly (no full target struct needed).
	// errorMessage empty == preserve any existing error_message.
	SetTargetStatus(ctx context.Context, targetID int64, status models.PostStatus, errorMessage string) error
}

// UploadUserStore resolves platform accounts + flips reauth flags for
// the per-target YouTube private upload phase. FindPlatformAccountByID
// resolves the grant's expected channel id. MarkReauthRequired
// (P0#3 server-side channel-binding guard — mirrors
// publish_worker_process.go::prepareCredentials) flips
// platform_account.status='reauth_required' on a
// channels.list(mine=true) mismatch so the operator's UI prompts the
// user to reconnect. The non-mismatch (transient) case does NOT call
// MarkReauthRequired — the upload worker surfaces the error to the
// outer job's retry path instead.
type UploadUserStore interface {
	FindPlatformAccountByID(id int64) (*models.PlatformAccount, error)
	MarkReauthRequired(ctx context.Context, accountID int64, code, message string) error
}

// UploadYouTubeTargetPubStore is the narrow persistence contract the
// per-target YouTube private upload phase needs on
// youtube_target_publications (migration 066). Concrete impl is
// *repository.YouTubeTargetPublicationRepository; tests inject an
// in-memory fake so worker-level integration tests don't need a DB.
//
// Methods included cover:
//   - Create / FindByPostTargetID  : idempotent row setup per (post_target_id).
//   - MarkYouTubeUploaded         : happy-path terminal transition (status='youtube_uploaded').
//   - IncrementAttempt            : bump attempt_count + stamp last_error on chunked-PUT failure.
//   - Update                      : blocked_auth / last_error mutations (full row for partial fields).
//
// Methods related to the POST-upload phases are intentionally absent:
// FindByYouTubeVideoID (webhook callbacks), ListByUploadJobID
// (unified pipeline view), MarkThumbnailReady (Velox editor hand-off),
// MarkPublished (publish phase). Those run on separate goroutines and
// use the full repository surface directly — keeping the upload worker's
// interface narrow prevents accidental coupling.
type UploadYouTubeTargetPubStore interface {
	Create(ctx context.Context, pub *models.YouTubeTargetPublication) error
	FindByPostTargetID(ctx context.Context, postTargetID int64) (*models.YouTubeTargetPublication, error)
	MarkYouTubeUploaded(ctx context.Context, id int64, videoID string) error
	IncrementAttempt(ctx context.Context, id int64, lastError string) error
	// MarkYouTubeUploadedAtomic (Blocco #1 followup — Finding #3
	// split-tx drift fix) is the success-path atomic transition. The
	// worker calls this INSTEAD of the standalone MarkYouTubeUploaded
	// so attempt_count + status + youtube_video_id commit or not in one
	// Postgres UPDATE. The standalone MarkYouTubeUploaded stays in the
	// interface for legacy callers (handler tests, read-only mocks)
	// that don't need the increment-folded shape.
	MarkYouTubeUploadedAtomic(ctx context.Context, id int64, videoID string) error
	Update(ctx context.Context, pub *models.YouTubeTargetPublication) error
}

// UploadWorkerOptions configures the worker pool sizing + cadence.
// All fields are zero-value safe; defaults are applied in Run() so
// NewUploadWorker never panics on a half-initialised options struct.
type UploadWorkerOptions struct {
	// IngestConcurrency caps the per-tick concurrent goroutines
	// the ingest pool can run (Drive → S3 streaming). The valutazione
	// doc recommends 2–3 on a dev box; default 3.
	IngestConcurrency int
	// UploadConcurrency caps the per-tick concurrent goroutines
	// the upload pool can run (videos.insert per-channel). The
	// valutazione doc recommends 3–4 on a dev box; default 4.
	UploadConcurrency int
	// LeaseTTL is the lifetime of a claim before ReclaimExpiredLeases
	// recovers it. Heartbeat must run at leaseTTL/3 so the lease
	// is renewed twice before expiry. Default 60s.
	LeaseTTL time.Duration
	// HeartbeatInterval is the cadence of the per-claimed-row
	// heartbeat goroutine. Default LeaseTTL/3 (e.g. 20s for a 60s
	// lease); three renewals before expiry is the safety margin.
	HeartbeatInterval time.Duration
	// ReclaimInterval is the cadence of the background
	// ReclaimExpiredLeases ticker (separate goroutine from the
	// per-row heartbeats). Default 30s.
	ReclaimInterval time.Duration
	// ReclaimOnStart, when true, runs ReclaimExpiredLeases
	// synchronously BEFORE the first tick of the pools so workers
	// don't race against any leases left over by a previous
	// crash. Default true.
	ReclaimOnStart bool
	// VideoRetentionBufferDays (Blocco #2 P0) drives the media_asset
	// expires_at calc at the worker ingest site. Default 7 = env
	// VIDEO_RETENTION_BUFFER_DAYS. Without this, the worker used a
	// hardcoded `time.Now().Add(7*24h)` which silently expired assets
	// scheduled 8..30 days out (since 7 < horizon 30). The new formula:
	//   expires_at = now + VideoRetentionBufferDays (no publish_at on
	//                this path because the post hasn't been created yet)
	// The bootstrap reads cfg.Worker.VideoRetentionBufferDays and passes
	// it via this field; defaults in applyDefaults handle the
	// test-fixture / option-bypass path.
	VideoRetentionBufferDays int
}

// UploadWorker processes upload_jobs in the background. It downloads
// videos from public or authenticated Google Drive, uploads them to S3,
// creates posts + targets, and triggers publishing. Jobs survive server
// restarts because they are persisted in the upload_jobs table.
//
// P1 step 2 — the worker is split into an ingest pool (Drive → S3)
// and an upload pool (S3 → posts → YouTube videos.insert). Both
// pools share the lease + heartbeat machinery added in P1 step 1
// (commit 4888c40). Per-claimed-row heartbeat goroutines keep the
// lease alive during the long streaming phases.
//
// Blocco #1 P0 — ytPubStore is the per-target YouTube publication
// store. Wired post-construction via SetYouTubeTargetPublishStore
// (boom-strapped in internal/bootstrap/app.go to
// *repository.YouTubeTargetPublicationRepository). When nil, the
// upload-as-private phase is a skip-and-warn — the legacy publish-only
// flow remains intact for non-YouTube platforms and for environments
// where the YT pub store isn't wired.
type UploadWorker struct {
	jobRepo          UploadJobStore
	mediaStore       UploadMediaStore
	postStore        UploadPostStore
	userRepo         UploadUserStore
	storage          services.StorageProvider
	capRouter        *services.CapabilityRouter
	vault            credentials.VaultAPI
	sourceRegistry   *ArtifactSourceRegistry
	deliveryVerifier ExternalDeliveryVerifier
	ytPubStore       UploadYouTubeTargetPubStore
	interval         time.Duration
	logger           *slog.Logger
	uploadTimeout    time.Duration
	opts             UploadWorkerOptions
}

// NewUploadWorker wires a new UploadWorker. opts fields default in
// Run() when zero; the bootstrap should pass an explicit options
// struct built from cfg so the operator-facing env vars take effect.
func NewUploadWorker(
	jobRepo UploadJobStore,
	mediaStore UploadMediaStore,
	postStore UploadPostStore,
	userStore UploadUserStore,
	storage services.StorageProvider,
	capRouter *services.CapabilityRouter,
	vault credentials.VaultAPI,
	sourceRegistry *ArtifactSourceRegistry,
	deliveryVerifier ExternalDeliveryVerifier,
	interval time.Duration,
	logger *slog.Logger,
	opts UploadWorkerOptions,
) *UploadWorker {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &UploadWorker{
		jobRepo:          jobRepo,
		mediaStore:       mediaStore,
		postStore:        postStore,
		userRepo:         userStore,
		storage:          storage,
		capRouter:        capRouter,
		vault:            vault,
		sourceRegistry:   sourceRegistry,
		deliveryVerifier: deliveryVerifier,
		interval:         interval,
		logger:           logger,
		uploadTimeout:    30 * time.Minute,
		opts:             opts,
	}
}

// SetYouTubeTargetPublishStore wires the per-target YouTube
// publication store. The upload worker reads this from
// processPublishJob's per-target phase to Create / MarkYouTubeUploaded
// / IncrementAttempt on the youtube_target_publications table.
//
// Called once at bootstrap (cmd/server) immediately after
// NewUploadWorker. If never called (or called with nil), the upload
// worker logs at its first per-target upload attempt + gracefully
// skips the private upload phase — the legacy publish-only flow
// remains intact. The setter pattern keeps the constructor signature
// stable across wires (production + tests) without breaking the
// optional-stage contract.
func (w *UploadWorker) SetYouTubeTargetPublishStore(store UploadYouTubeTargetPubStore) {
	w.ytPubStore = store
}

// YouTubeTargetPublishStore returns the wired per-target publication
// store, or nil if not yet wired. Read-only accessor used by tests
// assertions + the per-target helper's nil-check.
func (w *UploadWorker) YouTubeTargetPublishStore() UploadYouTubeTargetPubStore {
	return w.ytPubStore
}

// handleProcessingError classifies the error and routes MarkRetry
// vs MarkDeadLetter based on attempt_count vs max_attempts.
// ErrUploadJobLeaseLost is treated as "drop silently" (peer owns
// the row).
func (w *UploadWorker) handleProcessingError(
	ctx context.Context,
	poolName string,
	workerID string,
	job *models.UploadJob,
	processErr error,
) {
	if errors.Is(processErr, repository.ErrUploadJobLeaseLost) {
		w.logger.Warn("upload worker: lease lost mid-processing; dropping",
			"pool", poolName, "job_id", job.ID, "worker_id", workerID)
		return
	}

	w.logger.Error("upload worker: job failed",
		"pool", poolName, "job_id", job.ID,
		"attempt_count", job.AttemptCount, "max_attempts", job.MaxAttempts,
		"error", processErr,
	)

	errorCode := classifyUploadError(processErr)
	// Task 5/10 — permanent-error fast-path. Drive files with
	// capabilities.canDownload=false (and SHA / size / MIME mismatch
	// failures from artifact_verify) wrap PermanentError via errors.Join
	// upstream so the canDownload false case matches the same sentinel.
	// Short-circuit to MarkDeadLetter WITHOUT consuming the retry
	// budget — a non-downloadable file will not become downloadable on
	// retry; burning attempt_count for ~5 min × 8 attempts (max_attempts
	// envelope) before dead-letter triggers anyway is purely wasted
	// wall-clock + DB log noise. Routed BEFORE the attempt-count gate
	// so a single canDownload=false rejection lands the row in
	// 'dead_letter' (= 'perm_error' per the docs/OPERATIONS.md
	// runbook) on the very first failed tick.
	if errors.Is(processErr, ErrPermanent) {
		if markErr := w.jobRepo.MarkDeadLetter(ctx, job.ID, workerID, errorCode, processErr.Error()); markErr != nil {
			w.logger.Error("upload worker: MarkDeadLetter (permanent) failed",
				"pool", poolName, "job_id", job.ID, "error", markErr)
		}
		return
	}
	if job.AttemptCount >= job.MaxAttempts {
		if markErr := w.jobRepo.MarkDeadLetter(ctx, job.ID, workerID, errorCode, processErr.Error()); markErr != nil {
			w.logger.Error("upload worker: MarkDeadLetter failed",
				"pool", poolName, "job_id", job.ID, "error", markErr)
		}
		return
	}

	backoff := computeUploadBackoff(job.AttemptCount)
	if markErr := w.jobRepo.MarkRetry(ctx, job.ID, workerID, errorCode, processErr.Error(), time.Now().Add(backoff)); markErr != nil {
		w.logger.Error("upload worker: MarkRetry failed",
			"pool", poolName, "job_id", job.ID, "error", markErr)
	}
}

// processPublishJob handles the S3 → post → YouTube publish path.
// Assumes the row is in 'ready_to_publish' state with asset_id set.
func (w *UploadWorker) processPublishJob(ctx context.Context, job *models.UploadJob, workerID string) error {
	if job.AssetID == nil || *job.AssetID == "" {
		return fmt.Errorf("publish job %d missing asset_id; ingest did not complete", job.ID)
	}
	assetID := *job.AssetID

	key := services.BuildUploadKey(job.UserID, job.SourceID)
	mediaURL := w.storage.AssetURL(key)

	post := &models.Post{
		WorkspaceID: job.WorkspaceID,
		Title:       job.Title,
		Caption:     job.Caption,
		MediaURL:    mediaURL,
		Status:      models.PostStatusQueued,
		// P1#4 — IngestAfter is server-side DEFAULT NOW() at SQL
		// level; we pass job.IngestAfter through so a queued
		// ingest-after-future row preserves its ingest schedule.
		IngestAfter: job.IngestAfter,
		// PublishAt stamps the user-facing "what time should this
		// fire" cursor onto the created post. The publish_worker
		// ListPending predicate (queries.go::qSelectPendingTargets)
		// gates on publish_at <= NOW(), so the post stays queued
		// until the cursor elapses.
		PublishAt: job.PublishAt,
		// P1 (migration 053) — propagate the inherited batch default
		// onto the post. The publish_worker uses this as the middle
		// term of the precedence cascade:
		//   payload override (post.PrivacyLevel) > post.DefaultPrivacyLevel
		//   > "unlisted" (YouTube fallback) > PUBLIC_TO_EVERYONE (other platforms)
		// post.PrivacyLevel is left empty by this flow — the operator
		// sets it explicitly via the post-update endpoint when they want a
		// per-post override.
		DefaultPrivacyLevel: job.DefaultPrivacyLevel,
		// Blocco #1 P0 — FIXED via migration 077: stamp the upload_job_id
		// onto the post so PostRepository.Create's ON CONFLICT
		// (upload_job_id) DO NOTHING path can re-use the existing row on
		// a MarkRetry instead of stacking phantom posts. The pointer
		// &job.ID is taken because models.Post.UploadJobID is *int64
		// (Migration 077 made the column nullable + partial-unique so
		// the HTTP /api/v1/posts path can leave it nil and coexist).
		UploadJobID: &job.ID,
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

	// Blocco #1 P0 — per-target YouTube private upload phase. Runs
	// AFTER post + targets are persisted (so target.ID is populated
	// via RETURNING id) and BEFORE the publish_at gate below. The
	// upload lands as privacy='private' immediately so the rest of
	// the pipeline (Velox thumbnail editor, etc.) can resolve to a
	// real youtube_video_id without waiting on the user's calendar
	// cursor. publish_at stays on the post_target row for the LATER
	// videos.update phase (Blocco #1 phase 2, owned by publish_worker).
	//
	// Inside the loop, transient failures bubble up so handleProcessingError
	// MarkRetry's the parent upload_job (next claim re-runs the helper
	// idempotently — UNIQUE(post_target_id) on the YT pub table means
	// re-runs hit the existing row + idempotently stamp status).
	// blocked_auth (channel-binding mismatch) is handled IN-band: the
	// helper routes that target to status='blocked_auth' and returns
	// nil so the parent job can continue for OTHER targets.
	if w.ytPubStore != nil {
		for _, target := range targets {
			if target == nil {
				continue
			}
			if err := w.uploadVideoAsPrivateForTarget(ctx, job, target, post, mediaURL); err != nil {
				return fmt.Errorf("per-target youtube private upload target=%d: %w", target.ID, err)
			}
		}
	} else {
		w.logger.Warn("upload worker: ytPubStore not wired — per-target youtube private upload skipped (publish-phase trigger will still fire)",
			"job_id", job.ID)
	}

	// Trigger publishing only for jobs that should publish NOW.
	// Future-scheduled jobs (job.PublishAt > now) stay in the
	// `status='queued'` state and the publish_worker picks them up
	// when publish_at <= now(). Calling PublishPost on a future post
	// would race the scheduler and risk an out-of-order publish.
	//
	// P1#4 — defense-in-depth keep this go-level gate: ingest and
	// publish pools are separate goroutines; the publish pool's
	// ClaimBatchForPublish CTE also gates on (publish_at IS NULL OR
	// publish_at <= NOW()) so under normal conditions a row claimed
	// here already has publish_at <= now. The go-level check stays
	// for legacy single-file flows (POST /posts direct + cmd
	// binaries) where rows bypass the upload_jobs batching path and
	// the publish pool's CTE has no claim opportunity. A future
	// Taskilino can remove this check once every flow routes through
	// ClaimBatchForPublish.
	if job.PublishAt == nil || !job.PublishAt.After(time.Now()) {
		if err := w.postStore.PublishPost(post.ID); err != nil {
			return fmt.Errorf("trigger publish: %w", err)
		}
	} else {
		w.logger.Info("upload worker: post scheduled for future publish",
			"job_id", job.ID, "post_id", post.ID, "publish_at", job.PublishAt.Format(time.RFC3339))
	}

	// P2 — publish-phase throughput counter. Increment on the
	// hot path after the post + targets are persisted but BEFORE
	// the MarkCompleted CAS so a worker crash between persist
	// and the CAS double-counts on retry (acceptable: the
	// operator's "throughput" is a 5-minute rate over a counter,
	// not a strict sum, so a one-byte overcount per failed
	// transition is invisible at the dashboard).
	if assetID != "" && job.TotalBytes != nil && *job.TotalBytes > 0 {
		metrics.RecordUploadBytes(models.PlatformYouTube, "publish", *job.TotalBytes)
	}

	// Mark job completed. CAS against workerID ensures a peer that
	// stole the lease (reaper release + peer's ClaimBatch
	// re-claim) cannot overwrite a peer's terminal write.
	if err := w.jobRepo.MarkCompleted(ctx, job.ID, workerID, post.ID, assetID); err != nil {
		return fmt.Errorf("mark job completed: %w", err)
	}

	w.logger.Info("upload worker: publish done",
		"pool", "upload", "job_id", job.ID, "post_id", post.ID, "asset_id", assetID)
	return nil
}

// uploadVideoAsPrivateForTarget performs the per-target YouTube
// resumable upload-as-private for a single post_target row (Blocco
// #1 P0). The upload lands regardless of publish_at so the rest of
// the pipeline (Velox thumbnail editor, etc.) can resolve to a real
// youtube_video_id immediately. publish_at remains on the
// post_target row for the LATER videos.update phase (publish
// worker / Blocco #1 phase 2).
//
// Routing:
//   - targetID == 0 OR platform_account_id == 0 → error (caller bug).
//   - Platform ≠ YouTube                          → skip (return nil);
//     the per-target private step is YouTube-only; other platforms
//     keep using publish_worker's synchronous upload+publish flow.
//   - Token refresh transient error              → return error
//     (outer job retries on next claim).
//   - Channel binding ErrYouTubeChannelMismatch   → handleTargetBlockedAuth
//     (post_target.status='blocked_auth' +
//      platform_account.status='reauth_required' +
//      yt_pub row status='failed' + last_error) + return nil so the
//     parent job continues. Other binding errors (5xx, network) →
//     return error (retry).
//   - UploadChannelUploader not on the provider  → return error
//     (registration bug — BootstrapRegistry must register YouTube's
//     UploadChannelUploader conformance).
//   - Chunked PUT erred                          → IncrementAttempt on
//     yt_pub row + return error (outer retries).
//   - Chunked PUT succeeded with non-empty videoID → MarkYouTubeUploaded
//     + return nil.
//
// Runs inside runWithHeartbeat's lease heartbeat so a worker crash
// mid-upload leaves the row with youtube_upload_status='youtube_uploading'
// (UNIQUE(post_target_id) makes the next worker's re-run idempotent).
//
// Idempotent on the row level (Create is best-effort + re-fetch on
// UNIQUE collision); not idempotent on the YouTube side — every
// re-run does a fresh videos.insert, which YouTube itself dedupes
// via the resumable-session protocol only if the worker re-attaches// to the prior session URI (NOT a concern here since the helper
// always starts a fresh session).
//
// FIXED (Blocco #1 followup — migration 077): the prior transient-failure
// phantom-posts-on-MarkRetry symptom was eliminated by the migration
// below: posts.upload_job_id is now stamped with &job.ID at this layer
// (see the post struct literal at the top of processPublishJob), and
// PostRepository.Create's ON CONFLICT (upload_job_id) WHERE
// upload_job_id IS NOT NULL DO NOTHING + qSelectPostByUploadJobID
// re-fetch path (internal/repository/post_repo.go::Create +
// fetchExistingByUploadJobID) reuses the existing post row + its
// post_targets fan-out instead of inserting a fresh row when the
// retry's processPublishJob reaches this code path. youtube_target_
// publications rows already-per-target (per attempt one) remain
// unaffected: those have UNIQUE(post_target_id) which the per-target
// helper's FindByPostTargetID short-circuit already handles for
// within-claim reruns; across claim retries the OnConflict-style
// shim below the post-rehydrate reuses the existing target.IDs and
// the helper's idempotent-skip fires correctly.

func (w *UploadWorker) uploadVideoAsPrivateForTarget(
	ctx context.Context,
	job *models.UploadJob,
	target *models.PostTarget,
	post *models.Post,
	mediaURL string,
) error {
	if w.ytPubStore == nil {
		w.logger.Warn("upload worker: ytPubStore unset at per-target upload time (skipping)",
			"job_id", job.ID, "target_id", target.ID)
		return nil
	}
	if target == nil || target.ID == 0 {
		return fmt.Errorf("per-target private upload on nil/zero-id target (PostRepository.Create must populate via RETURNING id)")
	}
	if target.PlatformAccountID == 0 {
		return fmt.Errorf("per-target private upload on target with platform_account_id=0 (target_id=%d)", target.ID)
	}

	// Resolve platform_account so we know the channel + grant.
	account, err := w.userRepo.FindPlatformAccountByID(target.PlatformAccountID)
	if err != nil {
		return fmt.Errorf("FindPlatformAccountByID(%d): %w", target.PlatformAccountID, err)
	}
	if account == nil {
		return fmt.Errorf("nil platform account for id=%d", target.PlatformAccountID)
	}
	if account.Platform != models.PlatformYouTube {
		// Per verdict: only YouTube gets the per-target private upload
		// step. TikTok / Instagram / Facebook keep using publish_worker's
		// synchronous upload+publish flow at publish_at.
		return nil
	}

	provider, has := w.capRouter.Get(account.Platform)
	if !has {
		return fmt.Errorf("provider not found for platform=%s", account.Platform)
	}

	// Token refresh via vault.Renew + OAuthProvider.RefreshOAuthToken.
	// Mirrors publish_worker_process.go::prepareCredentials.
	oauthProvider, ok := provider.(services.OAuthProvider)
	if !ok {
		return fmt.Errorf("provider for %s does not implement OAuthProvider", account.Platform)
	}
	refresher := credentials.TokenRefresher(func(c context.Context, refreshToken string) (*models.TokenData, error) {
		return oauthProvider.RefreshOAuthToken(c, refreshToken)
	})
	oauthToken, err := w.vault.Renew(ctx, account.ID, models.TokenTypeBearer, refresher)
	if err != nil {
		// Same transient-classify as publish_worker::prepareCredentials:
		// retry via outer MarkRetry.
		return fmt.Errorf("token refresh for platform_account=%d: %w", account.ID, err)
	}

	// Channel-binding check (channels.list mine=true verify) — same
	// pre-flight publish_worker drives. Mismatch is structural
	// (non-recoverable without user re-auth) so we route to
	// blocked_auth + reauth_required + DON'T fail the parent job.
	if binder, hasBinder := provider.(services.YouTubeChannelBinder); hasBinder {
		bindErr := binder.ValidateChannelBinding(ctx, oauthToken.AccessToken, account.PlatformUserID)
		if bindErr != nil {
			if errors.Is(bindErr, services.ErrYouTubeChannelMismatch) {
				if err := w.handleTargetBlockedAuth(ctx, target, account, post.ID, bindErr.Error()); err != nil {
					w.logger.Warn("upload worker: handleTargetBlockedAuth partial-failure (continuing with parent job)",
						"target_id", target.ID, "platform_account_id", account.ID, "error", err)
				}
				return nil
			}
			// Transient (5xx, network, decode) — retry.
			return fmt.Errorf("channel binding check platform_account=%d (transient): %w", account.ID, bindErr)
		}
	}

	// Create or fetch the per-target publication row. The Create path
	// stamps server-side fields (id, created_at, updated_at) and lands
	// with youtube_upload_status='youtube_uploading' (DB DEFAULT).
	pub, err := w.ytPubStore.FindByPostTargetID(ctx, target.ID)
	if err != nil {
		return fmt.Errorf("FindByPostTargetID(target=%d): %w", target.ID, err)
	}
	// Idempotency skip: a previous claim's helper already stamped
	// youtube_upload_status='youtube_uploaded' on this target. Re-runs
	// would otherwise re-fire a fresh videos.insert for the same
	// channel (wasted YouTube quota + a duplicate video on the channel).
	// UNIQUE(post_target_id) keeps the row singular; this check is a
	// CPU-only short-circuit on top of that. The retry path still
	// re-runs the channel-binding check + DB writes (idempotent) so a
	// crash mid-MarkYouTubeUploaded is recoverable (next claim finds a
	// row with status='youtube_uploading' or 'failed' and retries).
	if pub != nil && pub.YouTubeUploadStatus == "youtube_uploaded" {
		w.logger.Debug("upload worker: per-target youtube already uploaded (idempotent skip)",
			"job_id", job.ID, "target_id", target.ID, "yt_pub_id", pub.ID)
		return nil
	}
	if pub == nil {
		pub = &models.YouTubeTargetPublication{
			UploadJobID:         job.ID,
			PostTargetID:        target.ID,
			PlatformAccountID:   account.ID,
			YouTubeUploadStatus: "youtube_uploading",
			DesiredPrivacy:      resolveDesiredPrivacy(post),
			PublishAt:           post.PublishAt,
		}
		if err := w.ytPubStore.Create(ctx, pub); err != nil {
			// UNIQUE violation on post_target_id OR a peer raced to
			// create — re-fetch and continue without re-creating.
			existing, eErr := w.ytPubStore.FindByPostTargetID(ctx, target.ID)
			if eErr == nil && existing != nil {
				pub = existing
			} else {
				return fmt.Errorf("Create youtube_target_publication: %w", err)
			}
		}
	}

	// Resolve the UploadChannelUploader capability + start the upload.
	uploader, ok := provider.(services.UploadChannelUploader)
	if !ok {
		return fmt.Errorf("provider for %s does not implement UploadChannelUploader (YouTubeOAuthService implements it; bootstrap must register the capability)", account.Platform)
	}
	videoID, err := uploader.UploadVideoAsPrivate(ctx, oauthToken.AccessToken, post, mediaURL)
	if err != nil {
		// Stamp attempt + last_error then bubble up so the parent
		// upload_job retry path picks up the row on its next claim.
		if incErr := w.ytPubStore.IncrementAttempt(ctx, pub.ID, fmt.Sprintf("upload failed: %v", err)); incErr != nil {
			w.logger.Warn("upload worker: IncrementAttempt failed (continuing with parent error)",
				"yt_pub_id", pub.ID, "target_id", target.ID, "error", incErr)
		}
		return fmt.Errorf("UploadVideoAsPrivate target=%d: %w", target.ID, err)
	}
	if videoID == "" {
		return fmt.Errorf("UploadVideoAsPrivate target=%d returned empty videoID", target.ID)
	}

	// Transition the per-target row: status='youtube_uploaded' +
	// youtube_video_id set. Blocco #1 followup — Finding #3 split-tx
	// drift fix: use MarkYouTubeUploadedAtomic instead of the
	// standalone MarkYouTubeUploaded so the attempt_count++ bump is
	// folded into the same row-level Postgres UPDATE. Row-level UPDATEs
	// are ACID-atomic, so a worker crash mid-call cannot leave the
	// row in status='youtube_uploading' with attempt_count bumped (the
	// pre-fix failure mode that produced orphan videos.insert on the
	// next claim).
	if err := w.ytPubStore.MarkYouTubeUploadedAtomic(ctx, pub.ID, videoID); err != nil {
		return fmt.Errorf("MarkYouTubeUploadedAtomic(pub=%d, videoID=%s): %w", pub.ID, videoID, err)
	}
	w.logger.Info("upload worker: per-target youtube private upload OK",
		"job_id", job.ID, "target_id", target.ID, "platform_account_id", account.ID, "youtube_video_id", videoID)
	return nil
}

// handleTargetBlockedAuth centralizes the per-target side effects on a
// channels.list(mine=true) mismatch:
//  1. persist last_error on youtube_target_publications (status='failed' + attempt++),
//     so dashboards + the unified pipeline view surface the failure cause.
//  2. set post_target.status='blocked_auth' so the publish worker
//     skips the row (and any "schedule it again" UI flow prompts
//     re-connect first).
//  3. set platform_account.status='reauth_required' (P0#3 server-side
//     channel-binding guard) so the operator's UI prompts the user to
//     reconnect.
//
// All side effects are best-effort — a partial failure logs WARN and
// returns nil so the parent job continues for OTHER targets. The
// uploadVideoAsPrivateForTarget caller treats a nil result as a
// "target done" so the loop advances to the next target.
func (w *UploadWorker) handleTargetBlockedAuth(
	ctx context.Context,
	target *models.PostTarget,
	account *models.PlatformAccount,
	postID int64,
	reason string,
) error {
	w.logger.Warn("upload worker: youtube channel binding mismatch; routing target to blocked_auth",
		"target_id", target.ID, "post_id", postID, "platform_account_id", account.ID,
		"expected_channel_id", account.PlatformUserID, "reason", reason)

	// (1) Persist YT pub row's last_error + attempted-count. Find
	// first (idempotent — may already exist from a prior partial upload).
	pub, err := w.ytPubStore.FindByPostTargetID(ctx, target.ID)
	if err == nil && pub != nil {
		if uErr := w.ytPubStore.Update(ctx, &models.YouTubeTargetPublication{
			ID:                  pub.ID,
			UploadJobID:         pub.UploadJobID,
			PostTargetID:        pub.PostTargetID,
			PlatformAccountID:   pub.PlatformAccountID,
			YouTubeUploadStatus: "failed",
			DesiredPrivacy:      pub.DesiredPrivacy,
			PublishAt:           pub.PublishAt,
			LastError:           "youtube_channel_mismatch: " + reason,
			AttemptCount:        pub.AttemptCount + 1,
			CreatedAt:           pub.CreatedAt,
			UpdatedAt:           time.Now().UTC(),
		}); uErr != nil {
			w.logger.Warn("upload worker: YT pub row Update on blocked_auth failed (continuing)",
				"yt_pub_id", pub.ID, "target_id", target.ID, "error", uErr)
		}
	}

	// (2) post_target.status='blocked_auth'. error_message stamps the
	// mismatch reason for the operator's audit log.
	if tErr := w.postStore.SetTargetStatus(ctx, target.ID, models.PostStatusBlockedAuth,
		"youtube channel mismatch: "+reason); tErr != nil {
		w.logger.Warn("upload worker: post_target SetTargetStatus(blocked_auth) failed (continuing)",
			"target_id", target.ID, "error", tErr)
	}

	// (3) platform_account.status='reauth_required' (mirrors
	// publish_worker_process.go::prepareCredentials).
	if aErr := w.userRepo.MarkReauthRequired(ctx, account.ID, "youtube_channel_mismatch", reason); aErr != nil {
		w.logger.Warn("upload worker: userRepo.MarkReauthRequired failed (continuing)",
			"platform_account_id", account.ID, "error", aErr)
	}
	return nil
}

// resolveDesiredPrivacy mirrors the publish_worker_process.go buildPayload
// cascade (post.PrivacyLevel > post.DefaultPrivacyLevel > "unlisted"
// YouTube-safe fallback). Used at Create-time of the per-target
// youtube_target_publications row so the row snapshots the EVENTUAL
// desired privacy the publish phase will target via videos.update.
// The upload ITSELF always uses "private" (independent of this
// snapshot) — the publish phase flips to the snapshot value at
// publish_at.
func resolveDesiredPrivacy(post *models.Post) string {
	if post.PrivacyLevel != "" {
		return post.PrivacyLevel
	}
	if post.DefaultPrivacyLevel != "" {
		return post.DefaultPrivacyLevel
	}
	return "unlisted"
}

// classifyUploadError maps a process-time error onto a stable taxonomy
// used by error_code (migration 046) for dashboard filtering and retry
// routing. Empty string means "unclassified" — the repository will
// store NULL via NULLIF.
func classifyUploadError(err error) string {
	s := err.Error()
	switch {
	case containsAny(s, "drive", "googleapis.com/upload/drive"):
		return "drive_error"
	case containsAny(s, "s3", "minio", "presigned"):
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

// computeUploadBackoff implements a deterministic decorrelated-jitter
// curve for the upload worker. AWS-style: temp = min(cap, prev * 3),
// sleep = base + (temp - base) / 2. Capped at 1h. Production polish
// in a follow-up commit replaces this with math/rand-based uniform
// sampling (mirroring internal/outbox/dispatcher.go::computeBackoff).
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
	return base + jitter/2
}
