// Package worker — Velox artifact downloader.
//
// Drains the POST /internal/v1/deliveries enqueue channel and
// registers each accepted job as an upload_jobs row. The existing
// UploadWorker pool (#7 in internal/bootstrap/app.go) ClaimBatch'es
// the row for the per-source ingest pipeline (HEAD/GET via
// VeloxArtifactSource + io.TeeReader(io.LimitReader) SHA + size
// verification + storage.Upload + MarkIngested). The downloader's
// responsibility is narrow: register the work.
//
// The VeloxDownloadJob struct was relocated here from
// pkg/api/internal_velox.go so the worker owns its own input type
// (clean import direction: pkg/api imports internal/worker, NOT
// the reverse). The pkg/api handler writes worker.VeloxDownloadJob{
// ...} to a chan worker.VeloxDownloadJob bound on
// Router.downloadJobCh (via the WithVeloxDownloadJobChannel
// RouterOption).

package worker

import (
	"context"
	"log/slog"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// VeloxDownloadJob is the channel-item shape the POST handler
// enqueues after a successful insert. The downloader pulls one item
// per iteration, hydrates an upload_jobs row from the carryover
// fields, links the row to the external_deliveries record, and
// advances the external_delivery state-machine from
// accepted → downloading.
//
// Fields:
//   - ExternalDeliveryID: social_delivery_id mapping (sdel_01J…)
//     stored at Insert time. The FK for both the upload_jobs row
//     (via LinkUploadJob) and the FSM advance.
//   - UserID / WorkspaceID: carry-overs from the authed request
//     scope. Required because the production BuildUploadKey scopes
//     upload_jobs by user_id (one bucket per user); WorkspaceID is
//     the post's foreign key on the publish side.
//   - Title / Caption: forwarded to upload_jobs then to the
//     publish_worker's post-creation path. Without them the
//     YouTube videos.insert call has nothing to publish.
//   - DefaultPrivacyLevel: maps to upload_jobs.default_privacy_level.
//     The publish_worker's cascade chain reads this column as the
//     middle term. Empty string means "let cascade pick" (the
//     publisher's per-platform rule applies).
//   - ArtifactSHA256 / SizeBytes / MimeType / DownloadURL: the
//     artifact triple used by the pool's deliveryVerifier
//     (GetExpectedTripleByUploadJobID) for hash + size guard.
//     DownloadURL is flat from the producer's *string (nil-safe);
//     empty string indicates a metadata-only delivery which the
//     downloader short-circuits (see processOne step 1a).
//   - PublishAt: optional, stamped into upload_jobs.publish_at.
//     publish_worker's ClaimBatchForPublish gates on
//     (publish_at IS NULL OR publish_at <= NOW()), so a future
//     PublishAt pauses the row in the queue until the cursor.
type VeloxDownloadJob struct {
	ExternalDeliveryID  string
	UserID              int64
	WorkspaceID         int64
	Title               string
	Caption             string
	DefaultPrivacyLevel string
	ArtifactSHA256      string
	SizeBytes           int64
	MimeType            string
	DownloadURL         string
	PublishAt           *time.Time
}

// ExternalDeliveryLookup is the minimal GetByID surface the
// downloader requires. The real impl is
// *repository.ExternalDeliveryRepository (production) or an
// in-process fake (tests). Defined here so the worker package
// doesn't reach into the repository package for one method.
type ExternalDeliveryLookup interface {
	GetByID(ctx context.Context, id string) (*models.ExternalDelivery, error)
}

// UploadJobCreator is the minimal Insert surface for the
// downloader's "register the work" responsibility. Production
// matches *repository.UploadJobRepository.Create exactly: the new
// upload_job ID is stamped back into j.ID via the INSERT's
// RETURNING clause (see internal/repository/upload_job_repo.go::Create
// migration 049a's RETURNING id, created_at, updated_at + the
// .Scan(&job.ID, &job.CreatedAt, &job.UpdatedAt) pair). The
// downloader reads j.ID after the call returns nil — NOT via a
// composite return value — to keep the surface identical to the
// production repo (test fakes just call into a sqlmock).
type UploadJobCreator interface {
	Create(j *models.UploadJob) error
}

// ExternalDeliveryLinker is the minimal LinkUploadJob surface.
// Production: *repository.ExternalDeliveryRepository.LinkUploadJob.
type ExternalDeliveryLinker interface {
	LinkUploadJob(ctx context.Context, externalDeliveryID string, uploadJobID int64) error
}

// VeloxArtifactDownloader is the single-goroutine consumer that
// drains the channel the POST handler writes to. Optimised for
// throughput rather than concurrency: a single drain goroutine is
// enough because the heavy lifting (HEAD / GET / SHA / S3 PUT)
// happens in the existing UploadWorker ClaimBatchForPublish pool.
type VeloxArtifactDownloader struct {
	extDeliveryLookup ExternalDeliveryLookup
	uploadJobs        UploadJobCreator
	extDeliveryLinker ExternalDeliveryLinker
	fsm               *IngestFSM
	logger            *slog.Logger
}

// NewVeloxArtifactDownloader wires the consumer. logger nil-safe
// (defaults to slog.Default()). Each dependency is required; nil
// panics at processOne time, surfaced loudly at boot rather than as
// a silent nil-pointer during operator triage.
func NewVeloxArtifactDownloader(
	lookup ExternalDeliveryLookup,
	jobs UploadJobCreator,
	linker ExternalDeliveryLinker,
	fsm *IngestFSM,
	logger *slog.Logger,
) *VeloxArtifactDownloader {
	if logger == nil {
		logger = slog.Default()
	}
	return &VeloxArtifactDownloader{
		extDeliveryLookup: lookup,
		uploadJobs:        jobs,
		extDeliveryLinker: linker,
		fsm:               fsm,
		logger:            logger,
	}
}

// Run is the consumer loop. Single goroutine — the channel is
// buffered (size 64 in bootstrap) so backpressure is bounded and the
// producer-side POST can return 202 inside the 500ms SLA.
//
// On ctx.Done the loop exits promptly. Jobs already buffered in the
// channel are LOST (Velox retries on its own delivery loop). We
// deliberately do NOT drain-and-process on shutdown: the shutdown
// budget is bounded (15s per goroutine per the bootstrap
// goroutineCtx pattern) and a multi-GiB verify-then-upload cycle
// inside processOne would exceed the budget under load. Contract:
// Velox retries on its own supervision, so a graceful shutdown
// that drops in-flight jobs is safe.
func (d *VeloxArtifactDownloader) Run(ctx context.Context, ch <-chan VeloxDownloadJob) error {
	d.logger.Info("velox artifact downloader started")
	defer d.logger.Info("velox artifact downloader stopped")

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case j, ok := <-ch:
			if !ok {
				return nil
			}
			d.processOne(ctx, j)
		}
	}
}

// processOne handles a single incoming download job. Each step has
// fail-loud semantics: a failure logs WARN and skips the remainder
// without rolling back earlier successful steps. The pool's claim
// loop will pick up any orphaned upload_jobs (retry/backoff per
// MarkRetry/MarkDeadLetter in upload_worker.go).
func (d *VeloxArtifactDownloader) processOne(ctx context.Context, j VeloxDownloadJob) {
	// (1) Load the canonical external_delivery row for the FK
	// + workspace_id + status fields. The download_job's
	// carryovers come from the producer but the row is still the
	// source of truth for status (a peer may have stamped it
	// between channel-enqueue and our pull).
	delivery, err := d.extDeliveryLookup.GetByID(ctx, j.ExternalDeliveryID)
	if err != nil {
		d.logger.Warn("velox downloader: GetByID failed; skipping",
			"external_delivery_id", j.ExternalDeliveryID, "error", err)
		return
	}
	if delivery == nil {
		d.logger.Warn("velox downloader: external_delivery row missing; skipping",
			"external_delivery_id", j.ExternalDeliveryID)
		return
	}

	// (1a) Metadata-only delivery — the producer's payload omits
	// DownloadURL. The Velox peer never published bytes, so
	// there's nothing to download+stream. We mark the row as
	// "blocked_auth" with a typed code so the operator dashboard
	// surfaces it cleanly; the row terminates AT this step and no
	// upload_job is created. We pick blocked_auth (which can
	// transition back to queued if the Velox peer re-delivers with
	// a populated URL) instead of failed (terminal) so the retry
	// path is recoverable.
	isMetadataOnly := delivery.DownloadURL == nil || *delivery.DownloadURL == ""
	if isMetadataOnly {
		code := "VELOX_METADATA_ONLY"
		msg := "delivery has no download_url; metadata-only"
		if tErr := d.fsm.ToBlockedAuth(ctx, j.ExternalDeliveryID, delivery.Status, code, msg); tErr != nil {
			d.logger.Warn("velox downloader: ToBlockedAuth (metadata-only) failed; ignoring",
				"external_delivery_id", j.ExternalDeliveryID, "error", tErr)
		}
		d.logger.Info("velox downloader: metadata-only delivery; no upload_job created",
			"external_delivery_id", j.ExternalDeliveryID)
		return
	}

	// (2) Build the upload_job. SourceType=VeloxArtifact drives the
	// sourceRegistry.Resolve path; SourceID is the canonical URL
	// from the DB row (NOT j.DownloadURL — the channel carryover
	// is best-effort and may diverge under peer race). Step 1a
	// guarantees delivery.DownloadURL is non-nil + non-empty before
	// we reach this line. PublishAt/Default/Title/Caption carry
	// from the producer verbatim.
	// Defensive: nil-check before deref (step 1a already handles
	// the metadata-only early-return, but if a future refactor
	// skips step 1a or invokes processOne from a separate
	// path, the deref must not panic). Empty URL re-routes
	// ToBlockedAuth symmetric with step 1a so a stale-accepted
	// row never persists.
	var srcID string
	if delivery.DownloadURL != nil {
		srcID = *delivery.DownloadURL
	}
	if srcID == "" {
		code := "VELOX_DOWNLOAD_URL_EMPTY"
		msg := "delivery.DownloadURL is empty at consumer time"
		if tErr := d.fsm.ToBlockedAuth(ctx, j.ExternalDeliveryID, delivery.Status, code, msg); tErr != nil {
			d.logger.Warn("velox downloader: empty-url ToBlockedAuth failed",
				"external_delivery_id", j.ExternalDeliveryID, "error", tErr)
		}
		return
	}
	uploadJob := &models.UploadJob{
		UserID:              j.UserID,
		WorkspaceID:         j.WorkspaceID,
		SourceType:          models.UploadJobSourceVeloxArtifact,
		SourceID:            srcID,
		Title:               j.Title,
		Caption:             j.Caption,
		DefaultPrivacyLevel: j.DefaultPrivacyLevel,
		PublishAt:           j.PublishAt,
	}
	if createErr := d.uploadJobs.Create(uploadJob); createErr != nil {
		d.logger.Warn("velox downloader: UploadJobRepository.Create failed; skipping",
			"external_delivery_id", j.ExternalDeliveryID, "error", createErr)
		return
	}
	newJobID := uploadJob.ID
	if newJobID <= 0 {
		// Defensive: if a fake or a future repo variant swallows the
		// RETURNING, surface loudly so LinkUploadJob rejects with its
		// own "uploadJobID must be positive" guard rather than blind-
		// writing a 0 or negative FK.
		d.logger.Warn("velox downloader: UploadJobRepository.Create returned id<=0; skipping link",
			"external_delivery_id", j.ExternalDeliveryID, "id", newJobID)
		return
	}

	// (3) Link upload_job → external_delivery. The pool reads
	// the row via this link to fetch the expected sha256+size
	// triple for verification. Without the link the row's
	// deliveryVerifier returns nothing and the pool silently
	// no-ops the integrity check.
	if linkErr := d.extDeliveryLinker.LinkUploadJob(ctx, j.ExternalDeliveryID, newJobID); linkErr != nil {
		d.logger.Warn("velox downloader: LinkUploadJob failed; upload_job orphan, reaper will recover",
			"external_delivery_id", j.ExternalDeliveryID,
			"upload_job_id", newJobID, "error", linkErr)
		return
	}

	// (4) Advance external_delivery status: accepted → downloading.
	// Non-fatal: the upload_job is linked so the pool will process
	// it anyway. A FSM skew usually means a peer already-stamped
	// the row, in which case ErrIllegalTransition is the expected
	// outcome (drop silently, log WARN).
	if fsmErr := d.fsm.ToDownloading(ctx, j.ExternalDeliveryID, delivery.Status); fsmErr != nil {
		d.logger.Warn("velox downloader: ToDownloading failed; upload_job still linked",
			"external_delivery_id", j.ExternalDeliveryID,
			"from_status", delivery.Status, "error", fsmErr)
		return
	}

	d.logger.Info("velox artifact downloader: registered download job",
		"external_delivery_id", j.ExternalDeliveryID,
		"upload_job_id", newJobID,
		"size_bytes", j.SizeBytes)
}
