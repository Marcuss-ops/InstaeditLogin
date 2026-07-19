// Package sources is the artifact-source registry + per-source
// implementations for the upload worker's ingest pool. The
// ArtifactSource interface abstracts the upstream (Velox today,
// future Dropbox/S3-drop tomorrow) so the worker's processIngestJob
// switches on the AbstractSourceRegistry.Resolve(job.SourceType)
// result, NOT on a hand-rolled switch over the UploadJobSource enum.
//
// Three observations motivate this abstraction:
//
//  1. The Velox contract hands the worker a one-shot artifact
//     pointer (sha256 + size + download_url) plus an HMAC-signed
//     callback channel. The artifact MUST be downloaded, verified,
//     and persisted before any publish call fires. The download
//     path (HEAD for metadata, GET with streaming SHA compute)
//     needs a dedicated ArtifactSource so each upstream's quirks
//     stay isolated and tests can mock the registry cleanly.
//
//  2. The Drive authenticated path has a DIFFERENT universe of
//     concerns (OAuth refresh + Google Files API + 10 GiB Drive-
//     side cap + capabilities.canDownload). Forcing both paths
//     through a single interface would dilute the safety
//     invariants. The registry pattern isolates the contracts.
//
//  3. A pass-through switch over UploadJobSource values becomes a
//     laundry list as more sources join. Registry + interface
//     let the worker claim "I expect an ArtifactSource here" and
//     delegate the per-source plumbing to the registry.
package sources

import (
	"errors"
	"fmt"
)

// PermanentError is the typed error returned by an ArtifactSource
// when the artifact bytes CANNOT be safely persisted. Distinct from
// the standard `error` type because the worker's failure-routing
// logic needs to distinguish:
//
//   (a) transient errors — retry with backoff (network blip, 5xx,
//       Velox download_url expired)
//   (b) permanent errors — terminate via MarkDeadLetter (no retry
//       would help) because the artifact itself violates the
//       contract (size_mismatch, sha_mismatch, mime_mismatch)
//   (c) programming errors — panic recovery path handles these
//
// PermanentError carries the (Code, Message) pair. Code is a stable
// string ("ARTIFACT_SIZE_MISMATCH", "ARTIFACT_SHA256_MISMATCH",
// …) that the worker stores on error_code at the SQL level so the
// dashboard filters by failure mode without parsing the message blob.
type PermanentError struct {
	Code    string
	Message string
}

// ErrPermanent is the sentinel workers use to errors.Is-dispatch
// any PermanentError. The dual-Is method on PermanentError ensures
// `errors.Is(pe, ErrPermanent)` returns true regardless of the pe.Code
// variant, so the worker's routing logic can match on the sentinel
// without enumerating every Code.
var ErrPermanent = errors.New("permanent ingest error")

// Error implements error. The shape is "{code}: {message}" — the
// leading Code prefix is what classifyUploadError greps for to fill
// the upload_jobs.error_code column (migration 046). Don't reformat
// the prefix without updating classifyUploadError.
func (e PermanentError) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Is makes errors.Is dispatch work on the sentinel. Pattern:
//   if errors.Is(err, sources.ErrPermanent) {
//       // route to MarkDeadLetter — no retry
//   }
func (e PermanentError) Is(target error) bool {
	return target == ErrPermanent
}

// Common Code values used by the worker for routing. The worker
// declares these as constants in its own package; the source-package
// side only needs the typed error + sentinel — the Codes are
// application-side names.
const (
	CodeArtifactSizeMismatch   = "ARTIFACT_SIZE_MISMATCH"
	CodeArtifactSHA256Mismatch = "ARTIFACT_SHA256_MISMATCH"
	CodeArtifactMIMEMismatch   = "ARTIFACT_MIME_MISMATCH"
)
