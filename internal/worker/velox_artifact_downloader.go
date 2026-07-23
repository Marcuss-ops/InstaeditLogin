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
	Targets             []int64
	DriveAccountID      *int64
	FolderID            *string
}

// ExternalDeliveryLookup is the minimal GetByID surface the
// downloader requires. The real impl is
// *repository.ExternalDeliveryRepository (production) or an
// in-process fake (tests). Defined here so the worker package
// doesn't reach into the repository package for one method.
type ExternalDeliveryLookup interface {
	GetByID(ctx context.Context, id string) (*models.ExternalDelivery, error)
}

type externalDeliveryLister interface {
	ListByStatus(ctx context.Context, status models.ExternalDeliveryStatus, limit int) ([]models.ExternalDelivery, error)
}

type externalDeliveryByExternalID interface {
	GetByExternalDeliveryID(ctx context.Context, sourceSystem, id string) (*models.ExternalDelivery, error)
}

// ExternalDeliveryUploadCreator is the atomic "create upload_job +
// link external_delivery + advance status" surface used by the
// downloader. Production: *repository.ExternalDeliveryRepository.
type ExternalDeliveryUploadCreator interface {
	CreateUploadJobAndLink(ctx context.Context, job *models.UploadJob, deliveryID string) (int64, error)
}

// VeloxArtifactDownloader is the single-goroutine consumer that
// drains the channel the POST handler writes to. Optimised for
// throughput rather than concurrency: a single drain goroutine is
// enough because the heavy lifting (HEAD / GET / SHA / S3 PUT)
// happens in the existing UploadWorker ClaimBatchForPublish pool.
type VeloxArtifactDownloader struct {
	extDeliveryLookup ExternalDeliveryLookup
	uploader          ExternalDeliveryUploadCreator
	fsm               *IngestFSM
	logger            *slog.Logger
}

// NewVeloxArtifactDownloader wires the consumer. logger nil-safe
// (defaults to slog.Default()). Each dependency is required; nil
// panics at processOne time, surfaced loudly at boot rather than as
// a silent nil-pointer during operator triage.
func NewVeloxArtifactDownloader(
	lookup ExternalDeliveryLookup,
	uploader ExternalDeliveryUploadCreator,
	fsm *IngestFSM,
	logger *slog.Logger,
) *VeloxArtifactDownloader {
	if logger == nil {
		logger = slog.Default()
	}
	return &VeloxArtifactDownloader{
		extDeliveryLookup: lookup,
		uploader:          uploader,
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

// RunPersistent also polls the durable external_deliveries journal. This is
// required when the API and worker run as separate processes: their Go
// channels are intentionally not shared across containers.
func (d *VeloxArtifactDownloader) RunPersistent(ctx context.Context, ch <-chan VeloxDownloadJob, resolve func(context.Context, models.ExternalDelivery) (VeloxDownloadJob, bool)) error {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case j, ok := <-ch:
			if !ok {
				ch = nil
				continue
			}
			d.processOne(ctx, j)
		case <-t.C:
			lister, ok := d.extDeliveryLookup.(externalDeliveryLister)
			if !ok || resolve == nil {
				continue
			}
			rows, err := lister.ListByStatus(ctx, models.ExternalDeliveryStatusAccepted, 32)
			if err != nil {
				d.logger.Warn("velox downloader: durable poll failed", "error", err)
				continue
			}
			for _, delivery := range rows {
				if delivery.UploadJobID != nil || delivery.DownloadURL == nil || *delivery.DownloadURL == "" {
					continue
				}
				if j, ok := resolve(ctx, delivery); ok {
					d.processOne(ctx, j)
				}
			}
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
	if delivery == nil {
		if byExternal, ok := d.extDeliveryLookup.(externalDeliveryByExternalID); ok {
			delivery, err = byExternal.GetByExternalDeliveryID(ctx, "velox", j.ExternalDeliveryID)
		}
	}
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
	deliveryKey := delivery.ID
	if deliveryKey == "" {
		deliveryKey = j.ExternalDeliveryID
	}

	// (1b) Only accepted deliveries are eligible for the atomic
	// create+link. Rows already claimed by a peer (or advanced by
	// the durable poll from another replica) must be skipped without
	// churning the database. The atomic repository method also
	// enforces this, but an early skip keeps logs quiet.
	if delivery.Status != models.ExternalDeliveryStatusAccepted {
		d.logger.Debug("velox downloader: delivery not accepted; skipping",
			"external_delivery_id", j.ExternalDeliveryID, "status", delivery.Status)
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
		if tErr := d.fsm.ToBlockedAuth(ctx, deliveryKey, delivery.Status, code, msg); tErr != nil {
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
		if tErr := d.fsm.ToBlockedAuth(ctx, deliveryKey, delivery.Status, code, msg); tErr != nil {
			d.logger.Warn("velox downloader: empty-url ToBlockedAuth failed",
				"external_delivery_id", j.ExternalDeliveryID, "error", tErr)
		}
		return
	}
	meta, err := models.ParseVeloxDeliveryMetadata(delivery.Metadata)
	if err != nil {
		d.logger.Warn("velox downloader: failed to parse delivery metadata; skipping",
			"external_delivery_id", j.ExternalDeliveryID, "error", err)
		return
	}

	uploadJob := &models.UploadJob{
		UserID:              j.UserID,
		WorkspaceID:         j.WorkspaceID,
		SourceType:          models.UploadJobSourceVeloxArtifact,
		SourceID:            srcID,
		Status:              models.UploadJobStatusPending,
		Title:               j.Title,
		Caption:             j.Caption,
		DefaultPrivacyLevel: j.DefaultPrivacyLevel,
		PublishAt:           j.PublishAt,
		Targets:             meta.TargetAccountIDs,
		DriveAccountID:      meta.DriveAccountID,
		FolderID:            meta.FolderID,
	}

	// (2-4) Atomically create the upload_job, stamp the FK on
	// external_deliveries, and move the delivery to 'downloading' in
	// one transaction. The UPDATE in CreateUploadJobAndLink only
	// succeeds for rows still in 'accepted' with no linked job,
	// so concurrent worker replicas cannot create duplicate jobs.
	newJobID, createErr := d.uploader.CreateUploadJobAndLink(ctx, uploadJob, deliveryKey)
	if createErr != nil {
		d.logger.Warn("velox downloader: CreateUploadJobAndLink failed; skipping",
			"external_delivery_id", j.ExternalDeliveryID, "error", createErr)
		return
	}

	// Defensive: if a fake or future repo variant swallows the
	// RETURNING, surface loudly. The repository already rejects
	// non-positive IDs before committing, so this is a last-resort
	// guard for in-memory test fakes.
	if newJobID <= 0 {
		d.logger.Warn("velox downloader: CreateUploadJobAndLink returned id<=0; skipping",
			"external_delivery_id", j.ExternalDeliveryID, "id", newJobID)
		return
	}

	d.logger.Info("velox artifact downloader: registered download job",
		"external_delivery_id", j.ExternalDeliveryID,
		"upload_job_id", newJobID,
		"size_bytes", j.SizeBytes)
}
