package worker

import (
	"context"
	"errors"
	"math/rand"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
	"github.com/Marcuss-ops/InstaeditLogin/internal/services"
)

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

	classification := services.ClassifyErrorFor("", poolName, processErr)
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
	if errors.Is(processErr, context.Canceled) {
		// Shutdown cancellation is not a provider failure and must not
		// consume retry budget or create a misleading DLQ row.
		return
	}
	if (classification != nil && (classification.Kind == services.ErrorKindPermanent || classification.Kind == services.ErrorKindAuth)) ||
		errors.Is(processErr, ErrPermanent) || errors.Is(processErr, services.ErrPermanentUpload) {
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
	if retryAfter := services.RetryAfterFromError(processErr); retryAfter > 0 {
		backoff = retryAfter
	}
	if markErr := w.jobRepo.MarkRetry(ctx, job.ID, workerID, errorCode, processErr.Error(), time.Now().Add(backoff)); markErr != nil {
		w.logger.Error("upload worker: MarkRetry failed",
			"pool", poolName, "job_id", job.ID, "error", markErr)
	}
}

// classifyUploadError maps a process-time error onto a stable taxonomy
// used by error_code (migration 046) for dashboard filtering and retry
// routing. Empty string means "unclassified" — the repository will
// store NULL via NULLIF.
func classifyUploadError(err error) string {
	if err == nil {
		return ""
	}
	classification := services.ClassifyError(err)
	if classification != nil {
		switch classification.Kind {
		case services.ErrorKindRateLimited:
			return "rate_limited"
		case services.ErrorKindAuth:
			return "auth_error"
		case services.ErrorKindPermanent:
			// Preserve the legacy provider buckets when the message carries
			// one; they are already used by operator filters.
			if legacy := classifyUploadErrorLegacy(err.Error()); legacy != "" {
				return legacy
			}
			return "permanent_error"
		case services.ErrorKindTransient:
			if classification.Code == "timeout" {
				return "timeout"
			}
			if classification.Provider != "" {
				switch classification.Provider {
				case "google_drive":
					return "drive_error"
				case "storage":
					return "s3_error"
				case "youtube":
					return "youtube_error"
				}
			}
		}
	}
	return classifyUploadErrorLegacy(err.Error())
}

func classifyUploadErrorLegacy(s string) string {
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

// computeUploadBackoff implements exponential backoff with full jitter
// for the upload worker. The lower bound remains base and the upper
// bound grows as min(cap, base*3^(attempt-1)); this spreads workers
// instead of scheduling every retry at the same deterministic instant.
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
	span := int64(temp) - int64(base)
	if span <= 0 {
		return base
	}
	return base + time.Duration(rand.Int63n(span+1))
}
