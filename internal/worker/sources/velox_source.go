package sources

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// veloxDownloadTimeout is the per-HTTP-request timeout for both
// Inspect (HEAD) and Open (GET) against the Velox signed
// download_url. Sized to a generous 5 minutes so a slow Velox
// producer with a multi-GiB artifact doesn't trigger
// context-deadline during legitimate streaming. Operators can
// override via VELOX_DOWNLOAD_TIMEOUT_SECONDS at the
// internal/config layer once a deployment profile demands it;
// Phase 1 ships with the constant.
//
// The timeout is applied via http.Client.Timeout (not ctx-timeout)
// because http.Client.Timeout also applies to the TLS handshake
// + DNS — ctx-timeout would not protect against a server that
// accepts the TCP connection but stalls the body.
const veloxDownloadTimeout = 5 * time.Minute

// veloxMaxArtifactBytes is the defense-in-depth cap for the
// streaming Open path. The per-row expected_size from
// external_deliveries.expected_size_bytes is the primary cap
// (via io.LimitReader inside the stream wrapper); this constant
// is the SECOND-order cap so a misconfigured upstream that
// declares e.g. 2 GB but actually serves 100 GB cannot OOM the
// worker pool before io.LimitReader fires.
//
// Default 2 GiB matches the architectural doc's "operator-side
// cap" + the existing driveDownloadMaxBytes constant for the
// Drive pipeline. Override via env VELOX_MAX_ARTIFACT_BYTES once
// a deployment profile needs more headroom.
const veloxMaxArtifactBytes int64 = 2 * 1024 * 1024 * 1024

// VeloxSource is the HTTP-backed ArtifactSource implementation for
// the upload worker's velox ingest path.
//
// Per the Velox→InstaEdit contract the artifact arrives as a
// one-shot HTTP GET against an externally-signed download_url +
// an expected_sha256 + expected_size_bytes triple carried on the
// external_deliveries row at /deliveries acceptance time. The
// worker's ingest pipeline:
//
//   1. Inspect(ctx, job) issues HEAD against the signed URL to
//      confirm the artifact is still reachable + extract an
//      ETAG for the UI cache validator. NOT authoritative for
//      size + SHA — those come from the external_deliveries row.
//   2. Open(ctx, job, expectedSize, expectedSHA) returns an
//      ArtifactStream wrapping http.Response.Body. The wrapper:
//        - io.TeeReader → sha256.Hasher (every byte flows through)
//        - io.LimitReader(expectedSize + 1) so reads > expected
//          return EOF prematurely (size-mismatch detection on
//          the cheap read layer; the post-Read count is the
//          authoritative check)
//        - close hook that records actualSize + actualSHA into
//          the stream's Result() for the worker's post-Read
//          constant-time comparison against the expected triple.
//
// The interface asks for SourceMetadata on Inspect AND for the
// actual sha/size in SourceMetadata after Open; we satisfy the
// first from the HEAD response, the second from the stream's
// post-close Result() (NOT from a returned SourceMetadata — the
// SHA can only be computed as the bytes flow past, not before).
type VeloxSource struct {
	client *http.Client
	logger *slog.Logger
}

// NewVeloxSource constructs a VeloxSource with a 5-minute HTTP
// timeout and a struct-level logger. logger nil-safe: a nil logger
// inherits slog.Default(). Pool one VeloxSource across the
// registry (http.Client has its own Transport-level concurrency;
// reusing the same client across goroutines is safe AND efficient).
func NewVeloxSource(logger *slog.Logger) *VeloxSource {
	if logger == nil {
		logger = slog.Default()
	}
	return &VeloxSource{
		client: &http.Client{Timeout: veloxDownloadTimeout},
		logger: logger,
	}
}

// Name implements ArtifactSource — KEY for the registry.
func (s *VeloxSource) Name() models.UploadJobSource {
	return models.UploadJobSourceVeloxArtifact
}

// Inspect implements ArtifactSource. Issues HEAD against the
// Velox-supplied download_url (read off the upload_job's link
// record) and returns the size + mime + ETag. NOT authoritative
// for ingest — the worker uses external_deliveries.expected_size_bytes
// + expected_sha256 (set at /deliveries POST time) as the source
// of truth for size and SHA.
//
// Inspect errors that are NOT already context errors propagate
// as transient (worker retries with backoff). 404 / 410 / etc.
// are returned verbatim so the worker can match on the status
// code in classifyUploadError for routing ("artifact_missing" or
// similar is NOT a PermanentError — the upstream DELETE might be
// transient per the Velox contract).
func (s *VeloxSource) Inspect(ctx context.Context, job *models.UploadJob) (*SourceMetadata, error) {
	downloadURL := resolveDownloadURL(job)
	if downloadURL == "" {
		return nil, errors.New("velox inspect: empty download_url")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, downloadURL, nil)
	if err != nil {
		return nil, fmt.Errorf("velox inspect: build HEAD request: %w", err)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("velox inspect: HEAD failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("velox inspect: HEAD returned status %d", resp.StatusCode)
	}
	mime := resp.Header.Get("Content-Type")
	etag := resp.Header.Get("ETag")
	size := resp.ContentLength
	if size < 0 {
		// Some origins (S3 in legacy configurations) DON'T
		// return Content-Length on HEAD. Treat unknown as "the
		// worker must rely on the external_deliveries row's
		// expected_size_bytes, not the HEAD probe". Inspect is
		// NOT authoritative; Open is.
		size = 0
	}
	return &SourceMetadata{
		SizeBytes: size,
		MimeType:  mime,
		ETag:      etag,
		// SHA256Hex intentionally empty: even if the upstream
		// surfaces it via x-amz-checksum-sha256, we still
		// re-derive via TeeReader on Open. Defense-in-depth:
		// never trust upstream-declared hashes.
	}, nil
}

// Open implements ArtifactSource. Streams the artifact body
// through:
//
//   1. http.Get(download_url)
//   2. wrap in veloxArtifactStream that:
//      - io.TeeReader(body, sha256.New()) → hash accumulated
//      - io.LimitReader(treader, expectedSize+1) → reads beyond
//        expected return EOF prematurely
//      - tracking byte count via io.CopyBuffer helper
//      - on Close: capture actualSize (final count) and actualSHA
//        (hash finaliser) for the worker's constant-time compare
//
// The stream's Body is what the worker drains through
// storage.Upload. SHA is computed DURING the same pass so the
// entire post-Read comparison is single-pass — no double-read
// of the artifact.
//
// expectedSize is capped at veloxMaxArtifactBytes to prevent a
// misconfigured upstream from declaring a 10 TiB file that OOMs
// the worker. The cap is a defense-in-depth secondary to the
// io.LimitReader cap (which is precise to expectedSize+1) but
// guards against the caller's accidentally bypassing the cap
// (e.g. by passing a wrong expectedSize from a malformed row).
func (s *VeloxSource) Open(ctx context.Context, job *models.UploadJob, expectedSize int64, expectedSHA256Hex string) (ArtifactStream, error) {
	if expectedSize <= 0 {
		return nil, PermanentError{
			Code:    CodeArtifactSizeMismatch,
			Message: fmt.Sprintf("expected size must be positive (got %d)", expectedSize),
		}
	}
	if expectedSize > veloxMaxArtifactBytes {
		return nil, PermanentError{
			Code:    CodeArtifactSizeMismatch,
			Message: fmt.Sprintf("expected size %d exceeds cap veloxMaxArtifactBytes=%d", expectedSize, veloxMaxArtifactBytes),
		}
	}
	if expectedSHA256Hex == "" {
		return nil, PermanentError{
			Code:    CodeArtifactSHA256Mismatch,
			Message: "expected sha256 is empty; cannot verify without it",
		}
	}
	downloadURL := resolveDownloadURL(job)
	if downloadURL == "" {
		return nil, errors.New("velox open: empty download_url")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return nil, fmt.Errorf("velox open: build GET request: %w", err)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("velox open: GET failed: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("velox open: GET returned status %d", resp.StatusCode)
	}
	// Return a wrapped reader. Close() on the stream closes the
	// underlying http.Response.Body too — caller MUST Close even
	// if Read stopped early (size-detected mismatch).
	return &veloxArtifactStream{
		body:           resp.Body,
		hasher:         sha256.New(),
		limitExpected:  expectedSize,
		expectedSHAHex: expectedSHA256Hex,
		source:         s,
		sourceJob:      job,
		httpResp:       resp,
	}, nil
}

// veloxArtifactStream is the ArtifactStream implementation backing
// VeloxSource.Open. Wraps http.Response.Body with three concurrent
// transforms:
//
//   1. Read count: every Read increments `read`. On Close the
//      final count is captured into actualSize.
//   2. SHA hasher: every byte flows through the hasher via the
//      inner Read path. On Close the finalised digest becomes
//      actualSHA256Hex.
//   3. Size guard: LimitReader(counter, expectedSize+1) so an
//      over-size stream surfaces as a Read returning n<len(p)+EOF
//      BEFORE the worker calls Close.
//
// The stream does NOT compute the comparison itself — that lives
// in the worker's post-Read call to stream.Result() + verify
// helpers. Keeping the comparison out of the source preserves the
// "source is honest bytes; the worker is the verifier" invariant.
type veloxArtifactStream struct {
	body io.ReadCloser
	// TeeReader wiring. Pair the hasher + a counting reader so
	// each Read increments both bytesRead AND feeds the hasher.
	hasher        hash.Hash
	bytesRead     int64
	limitExpected int64
	// Tracking & result fields. Filled on Close().
	actualSize       int64
	actualSHA256Hex  string
	expectedSHAHex   string
	source           *VeloxSource
	sourceJob        *models.UploadJob
	httpResp         *http.Response
	closed           bool
}

// Read exposes io.TeeReader(httpBody, hasher) capped to
// expectedSize+1 so over-size streams surface as a Read returning
// n<len(p)+EOF BEFORE the worker can pass more bytes into
// storage.Upload.
//
// We use a TWO-LAYER read (counterWriter AND direct hasher write)
// rather than io.TeeReader alone because TeeReader returns a
// Reader that wraps the original; reading through Tee increments
// the hasher correctly but doesn't separately count the bytes.
// Implementing our own tiny "counterWriter wraps hasher" avoids
// pulling in io.Pipe (which would buffer the artifact in memory —
// defeats the streaming purpose).
//
// Layout: outerReader → counter writer AND hasher writes happen
// alongside each other from each call to Read.
//
// Implementation: the count is just `len(p)` accumulation per Read
// (rather than the more sophisticated `n` returned per Read which
// is the bytes ACTUALLY transferred, can be < len(p) when partial
// reads happen). Partial reads are short — accumulating len(p)
// outer is a 1-byte over-count error per partial read, totally
// acceptable for size-bounding.
//
// We also stay consistent with io.LimitReader semantics: if
// bytesRead >= expectedSize+1 we return io.EOF irrespective of
// whether the underlying body has more bytes.
func (s *veloxArtifactStream) Read(p []byte) (int, error) {
	if s.closed {
		return 0, errors.New("veloxArtifactStream: read after close")
	}
	// Cap the requested read so we don't pull past expectedSize+1.
	remaining := s.limitExpected + 1 - s.bytesRead
	if remaining <= 0 {
		// Already over — terminate the read.
		return 0, io.EOF
	}
	if int64(len(p)) > remaining {
		p = p[:remaining]
	}
	n, err := s.body.Read(p)
	// Hash-then-update-count: order matters only for "what do I
	// see in the hasher" but the OUTCOME is identical because
	// we hash every byte we add to count, and we count every byte
	// we hashed.
	if n > 0 {
		s.hasher.Write(p[:n])
		s.bytesRead += int64(n)
	}
	return n, err
}

// Close finalises the stream. Captures actualSize (final byte
// count) and actualSHA256Hex (final hash) so the worker's
// post-Close Result() returns the verified triple.
//
// Idempotent: a second Close is a no-op (returns the previous
// result's nil error) so a double-close pattern from the worker
// (defensive close in defer + explicit close) doesn't double-count
// or panic.
func (s *veloxArtifactStream) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	if err := s.body.Close(); err != nil {
		return fmt.Errorf("veloxArtifactStream: close underlying body: %w", err)
	}
	s.actualSize = s.bytesRead
	s.actualSHA256Hex = hex.EncodeToString(s.hasher.Sum(nil))
	return nil
}

// Result returns the captured (actualSize, actualSHA256Hex) AFTER
// Close has been called. If the caller reads to EOF without calling
// Close (a programming bug) Result returns ErrStreamNotClosed.
//
// The worker's canonical post-Read pattern is:
//
//	defer stream.Close()
//	io.Copy(storage.UploadSink, stream)
//	actualSize, actualSHAHex, _ := stream.Result()
//	sources.VerifySize(actualSize, expected)  → PermanentError
//	sources.VerifySHA256(actualSHAHex, expected) → PermanentError
func (s *veloxArtifactStream) Result() (int64, string, error) {
	if !s.closed {
		return 0, "", ErrStreamNotClosed
	}
	return s.actualSize, s.actualSHA256Hex, nil
}

// resolveDownloadURL is the per-job download_url extractor. We
// look at the upload_job for a DownloadURL field (the canonical
// place) and, if absent, search for ANY field-lookup-able cache
// (future-proofing). Today the field is read off the upload_job
// itself if the worker stamps it AT creation time (see
// /internal/v1/deliveries handler — the social_delivery_id is
// crypto-derived so the worker can re-fetch the row by id).
//
// Phase 1 (current VeloxSource): the upload_job is created with
// the external_delivery's ID stuffed into upload_jobs.source_id
// (+ stored on the row's external_delivery_id pointer-column via
// the LinkUploadJob bridge). Since the URL shape is per-row and
// stored at upload_job time, we read it via job.SourceID when no
// dedicated DownloadURL field exists.
//
// Looking at upload_job.go (just-read), no DownloadURL column
// exists YET — Phase 1 wiring will pass the URL via the
// external_deliveries.download_url field, but the worker can only
// re-derive that via the upload_job_id FK. To avoid forcing the
// worker to glue two stores together, we EXPECT a download_url
// field on the upload_job itself (added in a follow-up migration).
//
// For this commit we degrade gracefully: if job.SourceID parses as
// a URL we treat it AS the download_url (i.e. the URL is stuffed
// into SourceID by the producer). When the proper DownloadURL
// column lands, ResolveDownloadURL switches to reading it and the
// SourceID-as-URL hack retires.
func resolveDownloadURL(job *models.UploadJob) string {
	if job == nil {
		return ""
	}
	if strings.HasPrefix(job.SourceID, "http://") || strings.HasPrefix(job.SourceID, "https://") {
		return job.SourceID
	}
	// Future: read job.DownloadURL when that field lands.
	return ""
}

// hex.EncodeToString is the canonical Go stdlib helper we use
// to render the lower-case hex digest from the SHA-256 hasher's
// Sum output. The import lives at the top of this file.
