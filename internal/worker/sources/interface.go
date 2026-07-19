package sources

import (
	"context"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// ArtifactSource is the per-source ingest contract for the upload
// worker. Each implementation owns its upstream protocol (Velox today:
// HEAD + HTTP GET against signed download_url + HMAC verification;
// future: Dropbox OAuth download; future: S3 presigned PUT direct).
//
// The interface is intentionally NARROW: just three methods. The
// worker composes the lifecycle:
//
//   1. Resolve source from registry via job.SourceType.
//   2. (Optional) Call Inspect for pre-flight (size/mime/etag) — not
//      authoritative but useful for thumbnail/UI rendering.
//   3. Call Open to obtain an ArtifactStream.
//   4. Stream the stream's Body through storage.Upload into S3.
//   5. After Read fully drains, call stream.Result() to extract
//      (actualSize, actualSHA) computed on-the-fly via TeeReader.
//   6. Compare actual vs expected -> PermanentError on mismatch.
//
// Inspect is decoupled from Open because Open is the SOURCE OF
// TRUTH (HEAD can be lying, the body is the canonical bytes). The
// worker MAY skip Inspect when the upstream contract guarantees the
// metadata in a separate channel (Velox: expected_sha256 +
// expected_size_bytes live on the external_deliveries row at POST
// time, so the worker DOES NOT need to re-derive them from HEAD).
//
// Open is the whole load-bearing method: bytes + computed size +
// computed sha, all three delivered atomically (the stream's
// Close returns Result() so partial reads don't leak half-asserted
// metadata).
type ArtifactSource interface {
	// Name returns the UploadJobSource enum value this source
	// handles. The registry keys on this; must MATCH the
	// UploadJobSource value the worker puts in upload_jobs.source_type.
	Name() models.UploadJobSource

	// Inspect is an OPTIONAL pre-flight: probes the upstream for
	// (size, mime, etag) metadata without downloading the bytes.
	// Used for UI preview UIs and the "you're about to import a
	// 2-hour 4K clip" toast. Not authoritative for ingest.
	//
	// Implementations that cannot / do not want to expose this
	// (Velox HEAD against the signed URL works fine; but the
	// expected size + sha are already on the external_deliveries
	// row in our model) MAY return ErrInspectNotImplemented —
	// the worker treats it as a no-op and skips the pre-flight.
	Inspect(ctx context.Context, job *models.UploadJob) (*SourceMetadata, error)

	// Open streams the artifact bytes. expectedSize is the
	// upstream-declared size in bytes; the stream uses it to
	// cap reads via io.LimitReader so over-size bodies surface as
	// PermanentError{Code: ARTIFACT_SIZE_MISMATCH} before the
	// storage.Upload call backs the wrong byte count into S3.
	// expectedSHA256Hex is the upstream-declared SHA-256 (lowercase
	// hex); the on-the-fly hasher compares via subtle.ConstantTimeCompare
	// post-Read and surfaces PermanentError{Code: ARTIFACT_SHA256_MISMATCH}
	// on mismatch.
	//
	// Expected values come from external_deliveries.expected_size_bytes
	// + expected_sha256 (the row Velox POSTed at /deliveries time).
	// We pass them explicitly rather than re-deriving from the
	// source to keep the worker's contract unambiguous.
	Open(ctx context.Context, job *models.UploadJob, expectedSize int64, expectedSHA256Hex string) (ArtifactStream, error)
}

// ErrInspectNotImplemented is the typed sentinel Inspect returns
// when the source doesn't have a usable pre-flight probe (HEAD
// against a Velox signed URL works, but if a future source doesn't
// expose metadata cheaply, this lets it opt out cleanly). Workers
// use errors.Is(err, ErrInspectNotImplemented) to skip the
// pre-flight without treating it as an error.
var ErrInspectNotImplemented = errors.New("inspect not implemented by this source")

// SourceMetadata is the artifact metadata extracted from Inspect.
// Field rationale:
//
//   SizeBytes: server-reported length (from HEAD Content-Length or
//     equivalent). Used by the UI to show "importing a 800 MiB file".
//     Not used by ingest (the actual byte count comes from the
//     stream drained THROUGH storage.Upload).
//
//   MimeType: server-reported. Used by the UI for the "we're
//     uploading a video/mp4" badge. The ingest layer uses
//     external_deliveries.expected_mime_type (the upstream's
//     declared mime) NOT this — Velox puts the declared mime on
//     the row before handoff.
//
//   ETag: server-reported cache validator. Used to short-circuit
//     repeated Inspects (cheap); for ingest, the worker's
//     comparison is byte-level via SHA so the etag is purely
//     observational.
//
//   SHA256Hex: server-reported SHA-256 (some S3-compatible stores
//     surface this via x-amz-checksum-sha256 or similar; Velox may
//     or may not). When present, a fast-path source MAY use it
//     to skip the on-the-fly hasher and trust the upstream; when
//     absent, the source MUST compute SHA via TeeReader.
//     Empty string means "I don't have it; trust the stream".
type SourceMetadata struct {
	SizeBytes int64
	MimeType  string
	ETag      string
	SHA256Hex string
}

// ArtifactStream is the readable side of an Open call. The Body
// field carries the streaming bytes (wrapped by TeeReader + LimitReader
// inside the implementation); the Result() method returns the
// computed size + SHA AFTER Close.
//
// Why the dual ReadCloser + Result() shape (rather than returning
// the bytes in Open like a synchronous fetch): the worker MUST
// stream the bytes through storage.Upload BEFORE knowing the final
// SHA — the SHA is computed DURING the read, not before. So Open
// returns a stream that the worker drains atomically. Result() is
// called exactly after Close() to read out the captured size + SHA.
//
// The interface (rather than a struct) keeps tests mailable: an
// in-memory fake stream returns predictable Result values without
// touching the network.
type ArtifactStream interface {
	io.ReadCloser
	// Result returns (actualSizeBytes, actualSHA256Hex) AFTER the
	// caller has drained Read fully AND called Close. Calling
	// Result before Close is a caller bug — implementations return
	// an error (typically sources.ErrStreamNotClosed or similar)
	// so the misuse is surfaced loudly rather than returning
	// stale data.
	Result() (actualSizeBytes int64, actualSHA256Hex string, err error)
}

// ErrStreamNotClosed is the sentinel the worker checks after a Read
// loop. If the worker forgets to Close before calling Result() the
// implementation returns this error; the worker should treat it as
// a programming bug (logs + crashes the goroutine via the existing
// panic-recovery path), NOT as a transient retry.
var ErrStreamNotClosed = errors.New("artifact stream: Result() called before Close()")

// verifySHAConstantTime performs a constant-time equality check on
// two hex-encoded SHA-256 strings. Returns nil on match, otherwise
// a PermanentError{Code: ARTIFACT_SHA256_MISMATCH} with the actual
// vs expectedSHA message detail operators need to triage.
//
// The hex decode + byte comparison is preferred over direct string
// compare to:
//
//   - tolerate upstream-mismatched casing (the canonical form is
//     lowercase hex from sha256.Sum256().hex.EncodeToString() but
//     upstream MAY submit uppercase)
//   - constant-time the BYTE comparison (hex.Equal); string compare
//     is constant-time per-byte too but the hex decode path is
//     more idiomatic in this codebase (date format is lowercase
//     hex everywhere else — credential fingerprints, request SHA,
//     etc.)
func verifySHAConstantTime(expectedHex, actualHex string) error {
	expected, err := hexDecodeStrict(expectedHex)
	if err != nil {
		return PermanentError{
			Code:    CodeArtifactSHA256Mismatch,
			Message: fmt.Sprintf("expected sha hex parse: %v", err),
		}
	}
	actual, err := hexDecodeStrict(actualHex)
	if err != nil {
		return PermanentError{
			Code:    CodeArtifactSHA256Mismatch,
			Message: fmt.Sprintf("actual sha hex parse: %v", err),
		}
	}
	if subtleConstantTimeCompareBytes(expected, actual) != 1 {
		return PermanentError{
			Code:    CodeArtifactSHA256Mismatch,
			Message: fmt.Sprintf("sha mismatch (expected %s, got %s)", expectedHex, actualHex),
		}
	}
	return nil
}

// verifySizeExact checks the streamed byte count against the
// upstream-declared expected size. Strict equality (==), not "≤".
// A SHORT body means the upstream truncated mid-stream (it broke
// its own contract) and is just as permanent as an over-size body.
func verifySizeExact(actual, expected int64) error {
	if actual != expected {
		return PermanentError{
			Code: CodeArtifactSizeMismatch,
			Message: fmt.Sprintf(
				"size mismatch (expected exactly %d bytes, got %d)",
				expected, actual,
			),
		}
	}
	return nil
}

// hexDecodeStrict decodes 64-char lowercase hex into 32 bytes. The
// hex package's stdlib DecodeString already accepts upper or lower
// case, but we ALSO reject any non-hex character BEFORE calling
// hex.DecodeString so the error message is precise ("uppercase hex
// at pos N") instead of the stdlib's "invalid hex character" —
// the worker logs that message directly to operators and the
// precision helps them triage data-entry bugs upstream.
//
// We do NOT use bytes.Equal — subtle.ConstantTimeCompare is the
// crypto-safe choice here even though our compared values are
// 32-byte public hashes: defense-in-depth against timing attacks
// would matter if the expected sha ever came from an untrusted
// source. Today it's the external_deliveries row at handoff time,
// which IS user-controlled but bounded by the 64-hex regex.
func hexDecodeStrict(s string) ([]byte, error) {
	if len(s) != 64 {
		return nil, fmt.Errorf("sha256 hex must be 64 chars (got %d)", len(s))
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return nil, fmt.Errorf("sha256 hex must be lowercase (got char %q at pos %d)", c, i)
		}
	}
	out, err := hex.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("sha256 hex decode: %w", err)
	}
	return out, nil
}

// subtleConstantTimeCompareBytes returns 1 on full byte equality, 0
// otherwise. Uses crypto/subtle.ConstantTimeCompare which is
// constant-time per-byte over equal-length buffers; length-mismatch
// returns 0 immediately (the function DOES NOT perform constant-
// time comparison across differing lengths, by design).
func subtleConstantTimeCompareBytes(a, b []byte) int {
	return subtle.ConstantTimeCompare(a, b)
}
