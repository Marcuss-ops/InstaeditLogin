package worker

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
	"github.com/Marcuss-ops/InstaeditLogin/pkg/metrics"
)

// processIngestJob handles the per-source ingest path. On success
// transitions the row to ready_to_publish via MarkIngested so the
// upload pool can claim it next.
//
// Phase 1 (registry refactor): the legacy switch over source_type is
// REPLACED by `sourceRegistry.Resolve(job.SourceType)`. The worker is
// generic — every per-source concern (OAuth refresh for Drive, signed
// URL GET for Velox, deprecation for PublicDrive) lives in the
// corresponding ArtifactSource implementation invoked here via the
// registry key.
//
// Worker-layer invariants (force-fail BEFORE storage.Upload):
//   - job.SourceType must be registered (else "unsupported source type")
//   - Inspect pre-flight surfaces size + mime used to size the asset + S3 PUT
//   - Open returns an io.ReadCloser that the worker drains through S3
//   - The downstream storage.Upload path is unchanged from the prior
//     revision; the only thing that moved is the bytestream source.
func (w *UploadWorker) processIngestJob(ctx context.Context, job *models.UploadJob, workerID string) error {
	// (1) Resolve the source via the registry. ok=false means the
	// worker doesn't recognise this SourceType — caller bug if we
	// ever see one (an upload_job's SourceType comes from the producer
	// and matches an enum value the worker must have a source for).
	src, ok := w.sourceRegistry.Resolve(job.SourceType)
	if !ok {
		return fmt.Errorf("unsupported source type: %s", job.SourceType)
	}

	// (2) Optional Inspect for pre-flight metadata. Most sources
	// implement it (Velox HEAD, Drive GetFileMetadata); the deprecated
	// PublicDrive source returns the actionable error verbatim. The
	// worker treats Inspect as best-effort: tolerate ErrInspectNotImplemented
	// as a soft no-op (no metadata means Open is the only source of
	// truth for ingest invariants).
	//
	// `md` is lifted to outer scope (Task 4/10) so the build-policy
	// block below can use SHA256Hex (Drive's sha256Checksum) when
	// RequireSHA is gated on the surface-declared value.
	var sizeBytes int64
	var contentType string
	var md *SourceMetadata
	if inspectMd, inspectErr := src.Inspect(ctx, job); inspectErr == nil && inspectMd != nil {
		md = inspectMd
		sizeBytes = md.SizeBytes
		contentType = md.MimeType
	} else if inspectErr != nil && !errors.Is(inspectErr, ErrInspectNotImplemented) {
		// PublicDrive's deprecation error (or any non-soft-Inspect
		// error from another source) bubbles up so the operator sees
		// the same guidance regardless of which entry point surfaced
		// the rejection.
		return fmt.Errorf("inspect source: %w", inspectErr)
	}

	// (3) Open the byte stream. The worker drains this through S3;
	// per-source OAuth refresh / signed URL GET / deprecation gates
	// live inside the source.
	srcBody, err := src.Open(ctx, job)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}

	if sizeBytes <= 0 {
		_ = srcBody.Close()
		return fmt.Errorf("source returned unknown or zero size for job %d; cannot import", job.ID)
	}

	// (3.5) GENERIC ArtifactVerificationPolicy AT THE WORKER LAYER
	// (Task 4/10). The prior Velox-only VeloxVerifyReader is replaced
	// by the unified artifactVerifyReader used by BOTH Velox and
	// Drive source paths. The policy is built per-source:
	//   * Velox: canonical ExpectedSize + ExpectedSHA256 from the
	//     external_deliveries row (via deliveryVerifier); RequireSHA=true
	//     unless the row is missing/legacy (skip-or-best-effort path).
	//   * Drive: ExpectedSize + ExpectedMIME from Inspect; ExpectedSHA256
	//     from sha256Checksum when present, RequireSHA accordingly.
	// "Drive verification is a follow-up" is no longer true as of
	// Task 4/10 — Drive with declared sha256Checksum now feeds the
	// policy and a mismatch causes MarkFailed + the post never
	// publishes.
	policy := models.ArtifactVerificationPolicy{
		ExpectedSize: sizeBytes,
		ExpectedMIME: contentType,
	}
	switch job.SourceType {
	case models.UploadJobSourceVeloxArtifact:
		if w.deliveryVerifier != nil {
			expSize, expSHA, vErr := w.deliveryVerifier.GetExpectedTripleByUploadJobID(ctx, job.ID)
			switch {
			case vErr == nil && expSize > 0:
				// Prefer the canonical external_deliveries row over
				// Inspect's HEAD — they're the producer's authoritative
				// triple; Inspect is the network probe (best-effort).
				policy.ExpectedSize = expSize
				policy.ExpectedSHA256 = expSHA
				policy.RequireSHA = true
			case IsDeliveryVerificationSkipErr(vErr):
				// peek-ordering race / legacy row — best-effort no-op
			default:
				return fmt.Errorf("velox: load expected triple: %w", vErr)
			}
		}
	case models.UploadJobSourceAuthenticatedDrive:
		if md != nil {
			policy.ExpectedSHA256 = md.SHA256Hex
			policy.RequireSHA = md.SHA256Hex != ""
		}
	default:
		// best-effort no-op for unmapped / future sources
	}
	verifyReader, err := NewArtifactVerifyReader(srcBody, policy)
	if err != nil {
		_ = srcBody.Close()
		return fmt.Errorf("wrap body for verification: %w", err)
	}
	defer verifyReader.Close()
	srcBody = verifyReader // S3 PUT now reads via the verify wrapper

	// Build S3 key and create pending media asset.
	key := services.BuildUploadKey(job.UserID, job.SourceID)
	// Blocco #2 P0 — buffer-aware TTL. The worker creates the
	// media_asset at ingest time, BEFORE the post is created via
	// PostRepository.Create (which stamps PublishAt). The asset must
	// live long enough for the post-creation + publish phases to
	// consume it; buffer (default 7d) covers the worst-case lag from
	// ingest → publish_at (limited by the user's cron drop). Falls
	// back to 7d if applyDefaults hasn't been called yet (test fixtures).
	buffer := w.opts.VideoRetentionBufferDays
	if buffer <= 0 {
		buffer = 7
	}
	asset := &models.MediaAsset{
		UserID:      job.UserID,
		UploadKey:   key,
		Bucket:      storageBucket(w.storage),
		ContentType: contentType,
		SizeBytes:   sizeBytes,
		Status:      models.MediaAssetStatusPending,
		ExpiresAt:   time.Now().Add(time.Duration(buffer) * 24 * time.Hour),
	}
	if err := w.mediaStore.Create(asset); err != nil {
		return fmt.Errorf("create media asset: %w", err)
	}

	// Sign S3 PUT and stream.
	grant, err := w.storage.SignUpload(ctx, job.UserID, key, contentType, sizeBytes, 15*time.Minute)
	if err != nil {
		w.markAssetFailed(job.ID, asset.ID, err.Error(), err)
		return fmt.Errorf("sign s3 upload: %w", err)
	}

	uploadReq, err := http.NewRequestWithContext(ctx, http.MethodPut, grant.UploadURL, srcBody)
	if err != nil {
		w.markAssetFailed(job.ID, asset.ID, err.Error(), err)
		return fmt.Errorf("build s3 upload request: %w", err)
	}
	uploadReq.Header.Set("Content-Type", contentType)
	uploadReq.ContentLength = sizeBytes

	s3Client := &http.Client{Timeout: w.uploadTimeout}
	uploadResp, err := s3Client.Do(uploadReq)
	if err != nil {
		w.markAssetFailed(job.ID, asset.ID, err.Error(), err)
		return fmt.Errorf("upload to s3: %w", err)
	}
	uploadResp.Body.Close()
	// The HTTP client has drained the verifier. Close it before Verify
	// so the reader's documented lifecycle is explicit rather than
	// relying only on the deferred cleanup.
	if err := verifyReader.Close(); err != nil {
		w.markAssetFailed(job.ID, asset.ID, err.Error(), err)
		return fmt.Errorf("close source stream: %w", err)
	}

	// POST-stream artifact verification (Task 4/10). MUST run AFTER
	// s3Client.Do has fully drained srcBody + BEFORE MarkReady /
	// MarkIngested so a SHA or size mismatch fails loud before the
	// row transitions to ready_to_publish. Both Velox and Drive
	// paths share this single gate. verifyReader owns and closes the
	// source body for the remainder of this function.
	if vErr := verifyReader.Verify(); vErr != nil {
		w.markAssetFailed(job.ID, asset.ID, vErr.Error(), vErr)
		return fmt.Errorf("artifact verification: %w", vErr)
	}
	if uploadResp.StatusCode >= 300 {
		reason := fmt.Sprintf("s3 upload returned %d", uploadResp.StatusCode)
		w.markAssetFailed(job.ID, asset.ID, reason, errors.New(reason))
		return fmt.Errorf("%s", reason)
	}

	// Verify upload.
	verifiedContentType, verifiedSize, err := w.storage.VerifyUpload(ctx, key)
	if err != nil {
		w.markAssetFailed(job.ID, asset.ID, err.Error(), err)
		return fmt.Errorf("verify s3 upload: %w", err)
	}
	// Boundary MIME check: S3-reported content_type must match the
	// policy's ExpectedMIME (typically the upstream-declared mime).
	// A mismatch means the upstream lied about the bytes — fail loud
	// instead of marking the asset ready so the operator-triage
	// dashboard can surface the upstream-side regression.
	if policy.ExpectedMIME != "" && verifiedContentType != policy.ExpectedMIME {
		reason := fmt.Sprintf("mime mismatch (expected %q, S3 returned %q)", policy.ExpectedMIME, verifiedContentType)
		w.markAssetFailed(job.ID, asset.ID, reason, errors.New(reason))
		return fmt.Errorf("%s", reason)
	}
	// MarkReady now receives the LOCALLY-COMPUTED SHA — always,
	// even when RequireSHA=false — so media_assets.sha256 stores the
	// authoritative hash for downstream re-verification. The repo
	// already handles "COALESCE(NULLIF($2, ''), sha256)" so a
	// non-empty local SHA always overwrites the existing row's
	// empty sha256 with the truth source.
	if err := w.mediaStore.MarkReady(asset.ID, verifyReader.ActualSHA256Hex(), verifiedSize, verifiedContentType); err != nil {
		return fmt.Errorf("mark media asset ready: %w", err)
	}

	// P2 — ops dashboard throughput counter. Increment BEFORE
	// the MarkIngested CAS so a worker crash between the
	// successful S3 verify and the DB stamp doesn't double-count
	// the bytes on retry. The "ingest phase" gate is implicit:
	// the upload worker only reaches this point on the ingest
	// pool's hot path, never on publish.
	if verifiedSize > 0 {
		metrics.RecordUploadBytes(models.PlatformYouTube, "ingest", verifiedSize)
	}

	// Transition the row: leased → ready_to_publish + asset_id +
	// total_bytes/progress_bytes (CAS against workerID that
	// ClaimBatch stamped on the row).
	if err := w.jobRepo.MarkIngested(ctx, job.ID, workerID, asset.ID, verifiedSize); err != nil {
		return fmt.Errorf("mark ingested: %w", err)
	}

	// Best-effort ffprobe pass (migration 092) — runs AFTER the job
	// transition so a probe failure can never fail the ingest. A
	// missing ffprobe binary or a probe error leaves the asset's
	// probe columns NULL (the live wizard then shows compatibility
	// as "unknown", never a hard failure).
	w.probeReadyAsset(ctx, asset.ID, key)

	w.logger.Info("upload worker: ingest done",
		"pool", "ingest", "job_id", job.ID, "asset_id", asset.ID, "size", verifiedSize)
	return nil
}

// probeReadyAsset is the best-effort ffprobe pass over the
// just-ingested object. Errors are logged, never returned: the
// ingest job has already transitioned (MarkIngested) and a probe
// failure must not bounce the job back into the pool. The probe runs
// against a short-lived presigned GET URL minted by the storage
// provider (the worker never reads the private bucket directly).
func (w *UploadWorker) probeReadyAsset(ctx context.Context, assetID, key string) {
	if w.prober == nil {
		return
	}
	url, err := w.storage.GetObject(ctx, key, 15*time.Minute)
	if err != nil {
		w.logger.Debug("upload worker: probe skip (mint presigned url)", "asset_id", assetID, "error", err)
		return
	}
	probe, err := w.prober.Probe(ctx, url)
	if err != nil {
		if errors.Is(err, ErrProbeUnavailable) {
			w.logger.Debug("upload worker: ffprobe unavailable; probe columns stay NULL", "asset_id", assetID)
		} else {
			w.logger.Warn("upload worker: media probe failed (best-effort)", "asset_id", assetID, "error", err)
		}
		return
	}
	probe.ProbedAt = time.Now()
	if err := w.mediaStore.SaveProbe(assetID, probe); err != nil {
		w.logger.Warn("upload worker: persist media probe failed (best-effort)", "asset_id", assetID, "error", err)
	}
}

// markAssetFailed transitions the media asset to failed and, when the
// bookkeeping write ITSELF fails, logs a loud slog.Error with the job
// and asset ids instead of silently discarding the error (the
// historical `_ = w.mediaStore.MarkFailedWithReason(...)` pattern).
// The caller still returns the ORIGINAL ingest error — that is the
// failure the tick counter and the retry state machine act on; a
// failed mark leaves the asset in 'pending' where the asset-cleanup
// sweep and this error line are the operator's recovery signals.
func (w *UploadWorker) markAssetFailed(jobID int64, assetID string, reason string, cause error) {
	if markErr := w.mediaStore.MarkFailedWithReason(assetID, reason, cause); markErr != nil {
		w.logger.Error("upload worker: failed to mark media asset failed; asset stays pending",
			"pool", "ingest",
			"job_id", jobID,
			"asset_id", assetID,
			"mark_error", markErr,
			"original_reason", reason,
			"original_error", cause)
	}
}
