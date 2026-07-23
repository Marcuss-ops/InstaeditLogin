// Package worker — Velox artifact downloader.
//
// Polls external_deliveries for accepted Velox deliveries and
// registers each claimed row as an upload_jobs row. The existing
// UploadWorker pool then ClaimBatch'es the row for the per-source
// ingest pipeline (HEAD/GET via VeloxArtifactSource +
// io.TeeReader(io.LimitReader) SHA + size verification +
// storage.Upload + MarkIngested). The downloader's responsibility
// is narrow: claim the durable row, validate it, and register the
// work.
//
// The API and worker do NOT share a Go channel. external_deliveries
// is the single source of truth and the only queue.

package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// VeloxDownloadJob is kept for backward compatibility with tests and
// any code that imports the name. It is no longer used as a channel
// payload; the worker now reads directly from the database queue.
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

// ExternalDeliveryClaimStore is the repository surface the worker
// uses to claim rows and record retry / dead-letter outcomes.
type ExternalDeliveryClaimStore interface {
	ClaimDelivery(ctx context.Context, workerID string, lease time.Duration, maxAttempts int) (*models.ExternalDelivery, error)
	MarkRetry(ctx context.Context, id string, nextAttemptAt time.Time, errorCode, errorMessage string) error
	MarkDeadLetter(ctx context.Context, id string, errorCode, errorMessage string) error
	MarkFailed(ctx context.Context, id string, errorCode, errorMessage string) error
	MarkBlockedAuth(ctx context.Context, id string, errorCode, errorMessage string) error
}

// ExternalDestinationLookup resolves a destination row for a
// delivery. Production wiring uses *repository.ExternalDestinationRepository.
type ExternalDestinationLookup interface {
	GetByID(ctx context.Context, id string) (*models.ExternalDestination, error)
}

// ExternalDeliveryWorkspaceLookup resolves the workspace (and owner)
// for a delivery. Production wiring uses *repository.WorkspaceRepository.
type ExternalDeliveryWorkspaceLookup interface {
	FindByID(id int64) (*models.Workspace, error)
}

// ExternalDeliveryUploadCreator is the atomic "create upload_job +
// link external_delivery + advance status" surface used by the
// downloader. Production: *repository.ExternalDeliveryRepository.
type ExternalDeliveryUploadCreator interface {
	CreateUploadJobAndLink(ctx context.Context, job *models.UploadJob, deliveryID, workerID string) (int64, error)
}

// VeloxArtifactDownloader polls the external_deliveries table for
// accepted rows and registers each claimed row as an upload_jobs row.
type VeloxArtifactDownloader struct {
	claimStore      ExternalDeliveryClaimStore
	uploader        ExternalDeliveryUploadCreator
	destinationStore ExternalDestinationLookup
	workspaceStore  ExternalDeliveryWorkspaceLookup
	fsm             *IngestFSM
	logger          *slog.Logger
	workerID        string
	lease           time.Duration
	pollInterval    time.Duration
	maxAttempts     int
}

// VeloxArtifactDownloaderOptions groups optional runtime settings.
type VeloxArtifactDownloaderOptions struct {
	Lease        time.Duration
	PollInterval time.Duration
	MaxAttempts  int
}

// NewVeloxArtifactDownloader wires the consumer. logger nil-safe.
func NewVeloxArtifactDownloader(
	claimStore ExternalDeliveryClaimStore,
	uploader ExternalDeliveryUploadCreator,
	fsm *IngestFSM,
	destinationStore ExternalDestinationLookup,
	workspaceStore ExternalDeliveryWorkspaceLookup,
	workerID string,
	logger *slog.Logger,
	opts ...VeloxArtifactDownloaderOptions,
) *VeloxArtifactDownloader {
	if logger == nil {
		logger = slog.Default()
	}
	options := VeloxArtifactDownloaderOptions{
		Lease:        5 * time.Minute,
		PollInterval: 2 * time.Second,
		MaxAttempts:  5,
	}
	if len(opts) > 0 {
		options = opts[0]
	}
	if options.Lease <= 0 {
		options.Lease = 5 * time.Minute
	}
	if options.PollInterval <= 0 {
		options.PollInterval = 2 * time.Second
	}
	if options.MaxAttempts <= 0 {
		options.MaxAttempts = 5
	}
	return &VeloxArtifactDownloader{
		claimStore:       claimStore,
		uploader:         uploader,
		fsm:              fsm,
		destinationStore: destinationStore,
		workspaceStore:   workspaceStore,
		workerID:         workerID,
		logger:           logger,
		lease:            options.Lease,
		pollInterval:     options.PollInterval,
		maxAttempts:      options.MaxAttempts,
	}
}

// Run polls the database queue until ctx is cancelled. On each tick
// it claims the next eligible delivery, processes it, and records the
// outcome (success, retry, dead-letter, blocked-auth, or failed).
func (d *VeloxArtifactDownloader) Run(ctx context.Context) error {
	d.logger.Info("velox artifact downloader started", "worker_id", d.workerID)
	defer d.logger.Info("velox artifact downloader stopped", "worker_id", d.workerID)

	t := time.NewTicker(d.pollInterval)
	defer t.Stop()

	// Immediate first iteration so the worker is active right after
	// startup, not after the first tick.
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		d.claimAndProcess(ctx)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
}

func (d *VeloxArtifactDownloader) claimAndProcess(ctx context.Context) {
	delivery, err := d.claimStore.ClaimDelivery(ctx, d.workerID, d.lease, d.maxAttempts)
	if err != nil {
		// No eligible row is the normal empty-queue case.
		if !isErrExternalDeliveryNotFound(err) {
			d.logger.Warn("velox downloader: claim failed", "error", err)
		}
		return
	}
	if delivery == nil {
		return
	}

	log := d.logger.With("external_delivery_id", delivery.ID)
	log.Debug("velox downloader: claimed delivery", "attempt", delivery.AttemptCount)

	if err := d.processOne(ctx, delivery); err != nil {
		log.Warn("velox downloader: processing failed", "error", err)
		d.handleFailure(ctx, delivery, err)
		return
	}

	log.Info("velox downloader: registered download job", "external_delivery_id", delivery.ID)
}

// processOne validates and registers a single claimed delivery. On
// success the delivery row has been advanced to 'downloading' by
// CreateUploadJobAndLink. On error the caller is responsible for
// retry/dead-letter bookkeeping.
func (d *VeloxArtifactDownloader) processOne(ctx context.Context, delivery *models.ExternalDelivery) error {
	// Metadata-only delivery — the producer's payload omits
	// DownloadURL. There is nothing to download+stream.
	if delivery.DownloadURL == nil || *delivery.DownloadURL == "" {
		return errMetadataOnly{deliveryID: delivery.ID}
	}

	dest, err := d.destinationStore.GetByID(ctx, delivery.ExternalDestinationID)
	if err != nil {
		return transientError{err: err}
	}
	if dest == nil {
		return terminalError{err: fmt.Errorf("external destination %s not found", delivery.ExternalDestinationID)}
	}

	ws, err := d.workspaceStore.FindByID(dest.WorkspaceID)
	if err != nil {
		return transientError{err: err}
	}
	if ws == nil {
		return terminalError{err: fmt.Errorf("workspace %d not found", dest.WorkspaceID)}
	}

	meta, err := models.ParseVeloxDeliveryMetadata(delivery.Metadata)
	if err != nil {
		return terminalError{err: err}
	}
	if err := meta.Validate(); err != nil {
		return terminalError{err: err}
	}

	uploadJob := &models.UploadJob{
		UserID:              ws.OwnerID,
		WorkspaceID:         ws.ID,
		SourceType:          models.UploadJobSourceVeloxArtifact,
		SourceID:            *delivery.DownloadURL,
		Status:              models.UploadJobStatusPending,
		Title:               meta.Title,
		Caption:             meta.Description,
		DefaultPrivacyLevel: meta.PrivacyStatus,
		PublishAt:           delivery.PublishAt,
		Targets:             meta.TargetAccountIDs,
		DriveAccountID:      meta.DriveAccountID,
		FolderID:            meta.FolderID,
	}

	newJobID, err := d.uploader.CreateUploadJobAndLink(ctx, uploadJob, delivery.ID, d.workerID)
	if err != nil {
		return transientError{err: err}
	}
	if newJobID <= 0 {
		return terminalError{err: errors.New("CreateUploadJobAndLink returned non-positive id")}
	}
	return nil
}

// handleFailure routes a processing failure to the appropriate
// terminal or retry state. Transient errors are retried with
// exponential backoff until maxAttempts is reached, at which point
// the row is dead-lettered.
func (d *VeloxArtifactDownloader) handleFailure(ctx context.Context, delivery *models.ExternalDelivery, err error) {
	var md errMetadataOnly
	if errors.As(err, &md) {
		_ = d.claimStore.MarkBlockedAuth(ctx, delivery.ID, "VELOX_METADATA_ONLY", "delivery has no download_url; metadata-only")
		return
	}

	var te transientError
	if !errors.As(err, &te) {
		_ = d.claimStore.MarkFailed(ctx, delivery.ID, "VELOX_PROCESSING_ERROR", err.Error())
		return
	}

	if delivery.AttemptCount >= d.maxAttempts {
		_ = d.claimStore.MarkDeadLetter(ctx, delivery.ID, "MAX_ATTEMPTS_EXCEEDED", "retry budget exhausted")
		return
	}

	backoff := d.retryBackoff(delivery.AttemptCount)
	nextAttempt := time.Now().Add(backoff)
	_ = d.claimStore.MarkRetry(ctx, delivery.ID, nextAttempt, "TRANSIENT_ERROR", te.err.Error())
}

func (d *VeloxArtifactDownloader) retryBackoff(attemptCount int) time.Duration {
	base := 5 * time.Second
	// exponential: 5s, 10s, 20s, 40s, ... capped at 30 minutes
	backoff := base
	for i := 1; i < attemptCount; i++ {
		backoff *= 2
		if backoff >= 30*time.Minute {
			backoff = 30 * time.Minute
			break
		}
	}
	return backoff
}

type transientError struct{ err error }

func (e transientError) Error() string { return e.err.Error() }

type terminalError struct{ err error }

func (e terminalError) Error() string { return e.err.Error() }

type errMetadataOnly struct{ deliveryID string }

func (e errMetadataOnly) Error() string { return "metadata-only delivery" }

func isErrExternalDeliveryNotFound(err error) bool {
	// The production repository returns ErrExternalDeliveryNotFound
	// when the queue is empty. In-memory fakes should return the same
	// sentinel.
	return errors.Is(err, repository.ErrExternalDeliveryNotFound)
}
