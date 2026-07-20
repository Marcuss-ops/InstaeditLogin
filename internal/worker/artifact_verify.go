// Package worker — artifact_verify.go is the generic, source-agnostic
// streaming SHA + size verifier used by BOTH Velox and Drive ingest
// flows. Mid-2025 the worker had a Velox-only verifier named
// veloxVerifyReader; Drive was a stub that ignored SHA on the reader
// layer (verified by S3 VerifyUpload alone, which trusts the upstream).
// Task 4/10 of the operator's goal plan replaces that with a single
// artifactVerifyReader whose behaviour is governed by the supplied
// ArtifactVerificationPolicy.
//
// The verifier enforces:
//   - size cap via a TeeReader-equivalent over the underlying body so
//     an over-size stream surfaces as PermanentError BEFORE the bytes
//     hit storage.Upload (defence-in-depth against runaway upstreams)
//   - streamed SHA-256 computation during the same single pass that
//     drains into storage.Upload
//   - post-Read comparison of (size + SHA) against the policy
//
// The verifier does NOT enforce ExpectedMIME — MIME is a header-level
// concept that is correctly compared against the S3 response's
// detected content_type AFTER the stream drains. Callers (worker +
// pkg/api/drive_import.go) do the boundary comparison explicitly.
package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// ExternalDeliveryVerifier is the narrow contract the upload worker
// uses to read the expected (size, sha) triple for a Velox-sourced
// upload_job. Implemented in production by an adapter over
// *repository.ExternalDeliveryRepository.GetExpectedTripleByUploadJobID.
//
// The interface is unchanged from the prior Velox-only reader
// revision; Task 4/10 only consolidated the SHA + size gate under a
// single artifact verify reader (no API ergonomics change for the
// upload_worker).
type ExternalDeliveryVerifier interface {
	GetExpectedTripleByUploadJobID(ctx context.Context, uploadJobID int64) (sizeBytes int64, sha256Hex string, err error)
}

// Compile-time assertion that artifactVerifyReader satisfies
// io.ReadCloser. Cheap insurance against future drift.
var _ io.ReadCloser = (*artifactVerifyReader)(nil)

// artifactVerifyReader is the worker / API-layer io.ReadCloser that
// computes SHA-256 + byte count during the same pass that streams
// into storage.Upload. Used by BOTH Velox (worker pull-path) and
// Drive (worker pull-path + the pkg/api/drive_import.go push-path)
// ingest flows. Replaces veloxVerifyReader from the prior Velox-only
// revision.
//
// The reader is NOT a tee against an external hasher; it owns the
// hash + byteCount fields directly so the post-Read Verify call
// finalises BOTH in a single go (no second-pass over the bytes).
type artifactVerifyReader struct {
	body      io.ReadCloser
	hash      hash.Hash
	policy    models.ArtifactVerificationPolicy
	byteCount int64
	closed    bool
	// resultError stores a permanent error raised DURING reads
	// (e.g. an over-size body detected via the size cap guard).
	// Read returns the error verbatim once it has fired.
	resultError error
}

// NewArtifactVerifyReader wraps body in a sha256-computing +
// size-cap gate governed by policy. Returns an error if body is nil
// (caller bug — the source path's Open returned nil).
//
// policy.ExpectedSize <= 0 disables the size cap entirely (the
// stream drains to EOF unconstrained; size comparison at Verify()
// is a no-op downstream). policy.RequireSHA controls whether SHA
// comparison fires; the local SHA is ALWAYS computed regardless so
// callers can persist ActualSHA256Hex() to media_assets.sha256 even
// when RequireSHA=false.
func NewArtifactVerifyReader(body io.ReadCloser, policy models.ArtifactVerificationPolicy) (*artifactVerifyReader, error) {
	if body == nil {
		return nil, errors.New("artifactVerifyReader: nil body")
	}
	return &artifactVerifyReader{
		body:   body,
		hash:   sha256.New(),
		policy: policy,
	}, nil
}

// Read exposes the underlying body bytes to the caller's
// io.Copy(storage.Upload sink) while:
//
//   - updating the streaming SHA hasher
//   - enforcing the size cap so an over-size stream surfaces as
//     PermanentError BEFORE the worker recycles more bytes into S3
//
// An over-size stream returns n<len(p)+io.EOF and stores the
// PermanentError in resultError for Verify() to surface.
func (r *artifactVerifyReader) Read(p []byte) (int, error) {
	if r.closed {
		return 0, errors.New("artifactVerifyReader: read after close")
	}
	if r.resultError != nil {
		return 0, r.resultError
	}
	if r.policy.ExpectedSize <= 0 {
		// Unknown-size path: pure SHA compute.
		n, err := r.body.Read(p)
		if n > 0 {
			r.hash.Write(p[:n])
			r.byteCount += int64(n)
		}
		return n, err
	}
	remaining := (r.policy.ExpectedSize + 1) - r.byteCount
	if remaining <= 0 {
		// Already over the limit; surface EOF + flag for post-close.
		r.resultError = PermanentError{
			Code:    CodeArtifactSizeMismatch,
			Message: fmt.Sprintf("size exceeds expected (read %d > limit %d)", r.byteCount, r.policy.ExpectedSize),
		}
		return 0, io.EOF
	}
	if int64(len(p)) > remaining {
		p = p[:remaining]
	}
	n, err := r.body.Read(p)
	if n > 0 {
		r.hash.Write(p[:n])
		r.byteCount += int64(n)
	}
	return n, err
}

// Close finalises the byte stream + populates the captured size +
// SHA for Verify. Idempotent — a second Close is a no-op (matches
// the http.Response.Body contract; workers typically defer Close
// AND have a defensive guard close in the error path).
func (r *artifactVerifyReader) Close() error {
	if r.closed {
		return nil
	}
	r.closed = true
	return r.body.Close()
}

// Verify runs the post-Read comparison against the policy. Returns
// nil on full match + PermanentError on size or SHA mismatch. The
// verifier never returns a transient error — size / SHA mismatches
// are permanent (the byte stream is what it is).
//
// MUST be called AFTER Close has been called (the SHA hasher
// finalises its digest only after the last byte has been written +
// drain completed). Idempotent: calling Verify twice on the same
// reader returns the same answer.
//
// The boundary MIME comparison (verified content_type vs
// policy.ExpectedMIME) is the caller's responsibility — this reader
// doesn't enforce MIME because MIME is a header concept, not a
// byte-stream-detectable property.
func (r *artifactVerifyReader) Verify() error {
	if r.resultError != nil {
		return r.resultError
	}
	actualSize := r.byteCount
	actualSHA := hex.EncodeToString(r.hash.Sum(nil))
	if r.policy.ExpectedSize > 0 {
		if err := verifySizeExact(actualSize, r.policy.ExpectedSize); err != nil {
			return err
		}
	}
	if r.policy.RequireSHA {
		if err := verifySHAConstantTime(r.policy.ExpectedSHA256, actualSHA); err != nil {
			return err
		}
	}
	return nil
}

// ActualSHA256Hex returns the SHA-256 we computed during streaming.
// Callers persist this to media_assets.sha256 via MarkReady — the
// local SHA is ALWAYS recorded (even when RequireSHA=false) so the
// row carries the truth source for downstream re-verification.
func (r *artifactVerifyReader) ActualSHA256Hex() string {
	return hex.EncodeToString(r.hash.Sum(nil))
}

// ActualSize returns the byte count observed during streaming.
// Useful for double-check assertions in tests + for ops dashboards
// that want the streamed byte count vs the storage HEAD response.
func (r *artifactVerifyReader) ActualSize() int64 {
	return r.byteCount
}

// IsDeliveryVerificationSkipErr is a helper that decides whether
// the verifier's "no triple" path is a no-op-or-skip case for
// the worker's caller. True for ErrExternalDeliveryNotLinked
// (peek-ordering race) AND ErrExternalDeliveryNoExpectedTriple
// (legacy row). False for other errors (caller should bubble up).
//
// Kept in artifact_verify.go (rather than velox-specific code) so
// it's co-located with the verify reader, and because it gates the
// Velox branch's "RequireSHA=false" fallback in upload_worker.
func IsDeliveryVerificationSkipErr(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, repository.ErrExternalDeliveryNotLinked) ||
		errors.Is(err, repository.ErrExternalDeliveryNoExpectedTriple)
}
