package worker

import (
	"errors"
	"fmt"
)

// PermanentError is the typed error returned by an ArtifactSource
// when the artifact bytes CANNOT be safely persisted. Distinct from
// the standard `error` type because the worker's failure-routing
// logic needs to distinguish:
//
//	(a) transient errors — retry with backoff (network blip, 5xx,
//	    Velox download_url expired)
//	(b) permanent errors — terminate via MarkDeadLetter (no retry
//	    would help) because the artifact itself violates the
//	    contract (size_mismatch, sha_mismatch, mime_mismatch)
//	(c) programming errors — panic recovery path handles these
//
// PermanentError carries the (Code, Message) pair. Code is a stable
// string ("ARTIFACT_SIZE_MISMATCH", "ARTIFACT_SHA256_MISMATCH",
// …) that the worker stores on error_code at the SQL level so the
// dashboard filters by failure mode without parsing the message blob.
type PermanentError struct {
	Code    string
	Message string
}

// ErrPermanent is the sentinel the worker uses for errors.Is
// dispatch on any PermanentError. The dual-Is method on the struct
// ensures `errors.Is(pe, ErrPermanent)` returns true regardless of
// the pe.Code variant, so the worker's routing logic can match on
// the sentinel without enumerating every Code.
var ErrPermanent = errors.New("permanent ingest error")

// Error implements error. The shape is "{code}: {message}" — the
// leading Code prefix is what classifyUploadError greps for to fill
// the upload_jobs.error_code column (migration 046). Don't reformat
// the prefix without updating classifyUploadError.
func (e PermanentError) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Is makes errors.Is dispatch work on the sentinel. Pattern:
//
//	if errors.Is(err, worker.ErrPermanent) {
//	    // route to MarkDeadLetter — no retry
//	}
func (e PermanentError) Is(target error) bool {
	return target == ErrPermanent
}

// Common Code values used by the worker for routing. Centralised
// here so the classifier (classifyUploadError) reads from the same
// namespace; previously these lived with the source package.
const (
	CodeArtifactSizeMismatch   = "ARTIFACT_SIZE_MISMATCH"
	CodeArtifactSHA256Mismatch = "ARTIFACT_SHA256_MISMATCH"
	CodeArtifactMIMEMismatch   = "ARTIFACT_MIME_MISMATCH"
	// Task 5/10 — Drive canDownload=false path. Matched by
	// errors.Is(err, ErrPermanent) on the canDownload reject branch
	// (internal/worker/authenticated_drive_source.go::Inspect) so the
	// upload worker's handleProcessingError fast-paths to
	// MarkDeadLetter immediately, bypassing the retry budget. Same
	// short-circuit as the SHA/size/mime mismatch categories — the
	// file's bytes cannot be obtained so retries never help.
	CodeDriveNotDownloadable = "DRIVE_NOT_DOWNLOADABLE"
)
