// Package worker — velox_verify.go is the worker-layer SHA + size
// verifier. The user-spec'd simpler Open(io.ReadCloser) signature
// moved verification OUT of the source; this file restores the
// invariant AT THE WORKER LAYER, gated on SourceType == Velox so
// the streaming hasher + size-guard still fire for Velox rows.
//
// Authentication via ExternalDeliveryVerifier (in this package):
// the worker tells the verifier `give me the (size, sha) for
// upload_job_id=X`; the verifier (in production:
// *repository.ExternalDeliveryRepository) reads it from the linked
// external_deliveries row.
package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"

	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// ExternalDeliveryVerifier is the narrow contract the upload worker
// uses to read the expected (size, sha) triple for a Velox-sourced
// upload_job. Implemented in production by an adapter over
// *repository.ExternalDeliveryRepository.GetExpectedTripleByUploadJobID.
//
// The interface exists BECAUSE the registry refactor moved SHA + size
// verification OUT of the source (per the simpler Open(io.ReadCloser)
// signature the user specified). The worker is now the verifier —
// for Velox it must look up the expected triple BEFORE draining
// the source body through storage.Upload. For non-Velox sources
// (AuthenticatedDrive) the worker relies on Inspect's size only
// and skips SHA verification (Drive API surface is SHA-256 now, but
// the worker's defense-in-depth policy is "hash during Open, never
// trust upstream-declared" — same as before).
type ExternalDeliveryVerifier interface {
	GetExpectedTripleByUploadJobID(ctx context.Context, uploadJobID int64) (sizeBytes int64, sha256Hex string, err error)
}

// Compile-time assertion the canonical sentinels live in the
// repository package. The worker dispatches via repository.Err*
// references so the layering (worker -> repository -> models) is
// unidirectional; the repository never imports worker.
var _ = errors.Is   // anchor for the errors package import below
var _ = fmt.Sprintf // anchor for fmt usage below

// veloxVerifyReader is the worker-layer io.ReadCloser used for
// Velox-sourced ingest: it computes SHA-256 + byte count
// DURING the same pass that streams into storage.Upload, so the
// post-Read verification is single-pass (no double-read of an
// artifact). The same factoring previously lived in
// veloxArtifactStream inside internal/worker/sources/. The
// refactor moved this to the worker layer per the simpler Open
// contract. Permanent errors raised here are returned to the
// worker via a stored error accessible after Close.
type veloxVerifyReader struct {
	body io.ReadCloser
	hash hash.Hash

	// limitExpected is the external_deliveries.expected_size_bytes+1
	// cap; reading beyond this returns io.EOF BEFORE the stream is
	// drained, allowing the worker to short-circuit over-size bodies.
	limitExpected int64

	// byteCount is monotonically non-decreasing as bytes flow past
	// the TeeReader.
	byteCount int64

	// closed: worker must Close before calling Verify().
	closed bool

	// resultError stores a permanent error raised DURING reads
	// (e.g. an over-size body detected via the limitExpected guard).
	// Read returns the error verbatim once it has fired.
	resultError error
}

// NewVeloxVerifyReader wraps body in a TeeReader-against-SHA +
// LimitReader-against-expectedSize gate. The returned ReadCloser
// is the worker's primary stream passed to storage.Upload; after
// the worker drains + Closes, Verify reports the captured size +
// SHA to the worker for post-Read comparison.
//
// expectedSize <= 0 disables the LimitReader gate (size-bounded
// verification falls through to a pure SHA computation; the
// worker compares via verifySizeExact only when expectedSize > 0).
// Defense-in-depth: storage.Upload still has its own size guard
// so a runaway upstream can't OOM the worker.
func NewVeloxVerifyReader(body io.ReadCloser, expectedSize int64) *veloxVerifyReader {
	if body == nil {
		return &veloxVerifyReader{resultError: errors.New("veloxVerifyReader: nil body")}
	}
	if expectedSize <= 0 {
		return &veloxVerifyReader{
			body: body,
			hash: sha256.New(),
		}
	}
	return &veloxVerifyReader{
		body:          body,
		hash:          sha256.New(),
		limitExpected: expectedSize + 1,
	}
}

// Read exposes the underlying body bytes to the caller's
// io.Copy(storage.Upload sink), capped to expectedSize+1. An
// over-size stream surfaces as n<len(p)+io.EOF BEFORE the
// worker can recycle more bytes into S3, surfacing the
// PermanentError at the post-Close Verify call.
func (r *veloxVerifyReader) Read(p []byte) (int, error) {
	if r.closed {
		return 0, errors.New("veloxVerifyReader: read after close")
	}
	if r.resultError != nil {
		return 0, r.resultError
	}
	if r.limitExpected <= 0 {
		// Unknown-size path.
		n, err := r.body.Read(p)
		if n > 0 {
			r.hash.Write(p[:n])
			r.byteCount += int64(n)
		}
		return n, err
	}
	remaining := r.limitExpected - r.byteCount
	if remaining <= 0 {
		// Already over the limit; surface EOF + flag for post-close.
		r.resultError = PermanentError{
			Code:    CodeArtifactSizeMismatch,
			Message: fmt.Sprintf("size exceeds expected (read %d > limit %d)", r.byteCount, r.limitExpected-1),
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
// SHA for Verify. Idempotent (a second Close is a no-op).
func (r *veloxVerifyReader) Close() error {
	if r.closed {
		return nil
	}
	r.closed = true
	return r.body.Close()
}

// Verify runs the post-Read comparison against the EXPECTED triple
// (size, sha256_hex). Returns PermanentError on any mismatch +
// nil on full match. The verifier hashes via hex.Equal-bytes
// (constant-time via crypto/subtle.ConstantTimeCompare, see
// permanent_error.go and verification_helpers.go).
//
// MUST be called AFTER Close has been called (the SHA hasher
// finalises its digest only after the last byte has been written
// + drain completed). Idempotent: calling Verify twice on the
// same reader returns the same answer (byteCode + hash.Sum are
// monotonically final after Close).
func (r *veloxVerifyReader) Verify(expectedSize int64, expectedSHA256Hex string) error {
	if r.resultError != nil {
		return r.resultError
	}
	actualSize := r.byteCount
	actualSHA := hex.EncodeToString(r.hash.Sum(nil))
	// Size comparison is conditional on a positive expectedSize —
	// size=0 means "the caller has no size expectation" (e.g. an
	// Open whose upstream didn't surface one). Forcing byteCount
	// == 0 would reject any successful byte stream, so we skip
	// the size gate and rely on the SHA gate alone.
	if expectedSize > 0 {
		if err := verifySizeExact(actualSize, expectedSize); err != nil {
			return err
		}
	}
	if err := verifySHAConstantTime(expectedSHA256Hex, actualSHA); err != nil {
		return err
	}
	return nil
}

// IsDeliveryVerificationSkipErr is a helper that decides whether
// the verifier's "no triple" path is a no-op-or-skip case for
// the worker's caller. True for ErrExternalDeliveryNotLinked
// (peek-ordering race) AND ErrExternalDeliveryNoExpectedTriple
// (legacy row). False for other errors (caller should bubble up).
func IsDeliveryVerificationSkipErr(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, repository.ErrExternalDeliveryNotLinked) ||
		errors.Is(err, repository.ErrExternalDeliveryNoExpectedTriple)
}
