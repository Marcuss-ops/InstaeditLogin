package worker

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/credentials"
	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
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

// PreparedUploadJobStore is optional for compatibility with small test
// doubles and legacy adapters. Production repositories implement it so a
// future job is not reported as publish_completed before publish_at.
type PreparedUploadJobStore interface {
	MarkPrepared(ctx context.Context, id int64, workerID string, postID int64, assetID string) error
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
	// SaveProbe persists the ffprobe-derived technical metadata
	// (duration, resolution, FPS, audio, codecs — migration 092) for
	// a ready asset. Best-effort by contract: the ingest probe must
	// never fail a job, so callers ignore errors.
	SaveProbe(id string, probe *models.MediaProbe) error
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

	// Delivery-queue surface (migration 125): the row is the claimable
	// (video, channel) queue unit consumed by the GLOBAL delivery pool
	// (runYouTubeDeliveryPool). A single upload_job with N YouTube
	// targets fans out to N independent rows claimed concurrently by
	// different pool workers — instead of one worker looping targets
	// sequentially inside a single job claim.
	ClaimReadyDeliveries(ctx context.Context, workerID string, limit int, lease time.Duration) ([]*models.YouTubeTargetPublication, error)
	HeartbeatDelivery(ctx context.Context, id int64, workerID string, lease time.Duration) (bool, error)
	ReleaseDeliveryLease(ctx context.Context, id int64, workerID string) error
	MarkDeliveryUploaded(ctx context.Context, id int64, workerID, videoID string) error
	MarkDeliveryFailed(ctx context.Context, id int64, workerID, errorCode, errMessage string, nextAttemptAt time.Time) error
	MarkDeliveryBlockedAuth(ctx context.Context, id int64, workerID, reason string) error
	// MarkDeliveryQuotaWait parks a claimed delivery in 'quota_wait'
	// (resume_state='ready_to_upload', next_attempt_at set to the daily
	// reset) when the YouTubeQuotaManager gate refused the videos.insert
	// because the video_uploads bucket is exhausted. Distinct from
	// MarkDeliveryFailed: the attempt budget is NOT consumed, so the
	// row resumes normally after the Pacific-time daily reset.
	MarkDeliveryQuotaWait(ctx context.Context, id int64, workerID string, nextAttemptAt time.Time) error
	ReclaimExpiredDeliveryLeases(ctx context.Context, maxRows int) (int64, error)
}

// YouTubeQuotaGate is the narrow pre-call gate the delivery pool needs
// on the YouTube Data API under the Google 2026 quota model (three
// independent daily buckets). *services.YouTubeQuotaManager implements
// it; tests inject a stub to exercise the gate without a Postgres
// backend. The contract is fail-closed: when the gate cannot decide
// (err != nil) the caller must NOT make the API call.
type YouTubeQuotaGate interface {
	// ReserveOperation resolves an operation name (e.g.
	// services.YouTubeOpVideoInsert) to its (bucket, cost) spec and
	// charges it atomically. allowed=false + retryAfterSeconds>0 means
	// the bucket is exhausted for the day.
	ReserveOperation(ctx context.Context, operation string) (allowed bool, retryAfterSeconds int, err error)
	// RecordError bumps the informational errors counter for a bucket
	// after a real API failure (5xx / transport / validation).
	RecordError(ctx context.Context, bucket string) error
}

// UploadDeliveryPostStore resolves the post + post_target rows a
// delivery needs to run the per-channel private upload. The delivery
// row only carries ids (upload_job_id, post_target_id,
// platform_account_id); the worker loads the parent post (title,
// caption, metadata, privacy, media asset) and the target through this
// narrow surface. *repository.PostRepository implements both methods;
// tests inject an in-memory fake.
type UploadDeliveryPostStore interface {
	// FindByID / FindTargetByID mirror *repository.PostRepository's
	// signatures (no ctx — the repo binds queries to the pool's
	// database/sql driver which manages its own timeouts).
	FindByID(id int64) (*models.Post, error)
	FindTargetByID(id int64) (*models.PostTarget, error)
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
	// EmptyQueueBackoffMin is the initial delay after a claim returns
	// no jobs. It prevents an empty queue from becoming a tight DB loop.
	// Default 1s.
	EmptyQueueBackoffMin time.Duration
	// EmptyQueueBackoffMax caps the exponential empty-queue delay.
	// Default 30s.
	EmptyQueueBackoffMax time.Duration
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

// SetYouTubeQuotaGate wires the Google 2026 quota pre-call gate into
// the delivery pool. When set, uploadVideoAsPrivateForDelivery reserves
// the videos.insert charge BEFORE the API call (fail-closed on gate
// errors) and parks the delivery in 'quota_wait' when the video_uploads
// bucket is exhausted; after a real API failure it records the error
// against the bucket. When nil (legacy / test fixtures), the upload
// proceeds ungated exactly as before.
func (w *UploadWorker) SetYouTubeQuotaGate(gate YouTubeQuotaGate) {
	w.quotaGate = gate
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
	sourceRegistry    *ArtifactSourceRegistry
	deliveryVerifier  ExternalDeliveryVerifier
	ytPubStore        UploadYouTubeTargetPubStore
	deliveryPostStore UploadDeliveryPostStore
	quotaGate         YouTubeQuotaGate
	resolver          services.MediaDownloadResolver
	prober            MediaProber
	interval          time.Duration
	logger            *slog.Logger
	uploadTimeout     time.Duration
	s3HTTPClient      *http.Client
	opts              UploadWorkerOptions
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
		s3HTTPClient:     services.NewHTTPClientWithTimeout(30 * time.Minute),
		opts:             opts,
	}
}

// SetYouTubeTargetPublishStore wires the per-target YouTube
// publication store. The upload worker reads this from
// processPublishJob's per-target phase to Create / MarkYouTubeUploaded
// / IncrementAttempt on the youtube_target_publications table.
//
// Called once by the worker bootstrap immediately after NewUploadWorker.
// If never called (or called with nil), the upload
// worker logs at its first per-target upload attempt + gracefully
// skips the private upload phase — the legacy publish-only flow
// remains intact. The setter pattern keeps the constructor signature
// stable across wires (production + tests) without breaking the
// optional-stage contract.
func (w *UploadWorker) SetYouTubeTargetPublishStore(store UploadYouTubeTargetPubStore) {
	w.ytPubStore = store
}

// SetYouTubeDeliveryPostStore wires the post/target lookup surface the
// global delivery pool needs to resolve a claimed (video, channel)
// delivery row back to its parent post + target. The concrete
// *repository.PostRepository implements both methods. When never
// called (or nil) the delivery pool logs once and stays disabled —
// the enqueue side (processPublishJob materialization) keeps working
// and rows simply wait for a wired worker.
func (w *UploadWorker) SetYouTubeDeliveryPostStore(store UploadDeliveryPostStore) {
	w.deliveryPostStore = store
}

// SetMediaDownloadResolver wires the shared just-in-time media resolver.
// The setter keeps the existing constructor stable for test fixtures while
// production ensures every publisher signs from the owned, ready asset.
func (w *UploadWorker) SetMediaDownloadResolver(resolver services.MediaDownloadResolver) {
	w.resolver = resolver
}

// SetMediaProber wires the optional ffprobe pass (migration 092).
// When nil (or never called) the ingest path skips probing entirely:
// the asset's probe columns stay NULL and the asset remains fully
// usable — the live wizard simply shows compatibility as "unknown"
// instead of "Pronto per live".
func (w *UploadWorker) SetMediaProber(prober MediaProber) {
	w.prober = prober
}

// YouTubeTargetPublishStore returns the wired per-target publication
// store, or nil if not yet wired. Read-only accessor used by tests
// assertions + the per-target helper's nil-check.
func (w *UploadWorker) YouTubeTargetPublishStore() UploadYouTubeTargetPubStore {
	return w.ytPubStore
}

// uploadVideoAsPrivateForDelivery performs the per-(video, channel)
// YouTube resumable upload-as-private for a single claimed delivery
// row (Blocco #1 P0 + delivery-queue refactor, migration 125). The
// upload lands regardless of publish_at so the rest of the pipeline
// (Velox thumbnail editor, etc.) can resolve to a real
// youtube_video_id immediately. publish_at remains on the delivery
// row for the LATER publish phase (Blocco #1 phase 2, owned by
// publish_worker / native status.publishAt when scheduled public).
//
// Routing:
//   - Delivery already youtube_uploaded        → idempotent skip (return nil).
//   - Platform ≠ YouTube                        → skip (return nil);
//     only YouTube gets the per-delivery private step; other
//     platforms keep using publish_worker's synchronous flow.
//   - Token refresh transient error            → return error (the
//     delivery pool routes the row to retry_wait via MarkDeliveryFailed).
//   - Channel binding ErrYouTubeChannelMismatch → handleTargetBlockedAuth
//     (delivery state='failed' + post_target.status='blocked_auth' +
//      platform_account.status='reauth_required') + return nil (no
//     retry). Other binding errors (5xx, network) → return error
//     (retry_wait).
//   - UploadChannelUploader not on the provider → return error
//     (registration bug — bootstrap must register YouTube's
//     UploadChannelUploader conformance).
//   - Chunked PUT erred                          → return error; the
//     delivery pool's processYouTubeDelivery routes the row to
//     retry_wait / dead_letter via MarkDeliveryFailed (attempt++ +
//     last_error in ONE atomic UPDATE).
//   - Chunked PUT succeeded with non-empty videoID → MarkDeliveryUploaded
//     (state='youtube_uploaded' + video_id + attempt++ + lease release)
//     + return nil.
//
// Runs inside runDeliveryWithHeartbeat's lease heartbeat so a worker
// crash mid-upload leaves the row in state='uploading' with an expired
// lease; the delivery reclaimer (ReclaimExpiredDeliveryLeases) returns
// it to 'ready_to_upload' and the next claim re-runs the helper
// idempotently (UNIQUE(post_target_id) + the youtube_uploaded
// short-circuit mean re-runs never double-upload a channel).
