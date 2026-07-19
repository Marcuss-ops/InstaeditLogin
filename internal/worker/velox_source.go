package worker

import (
	"context"
	"errors"
	"fmt"
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
// override via VELOX_DOWNLOAD_TIMEOUT_SECONDS at config layer once
// a deployment profile demands it; Phase 1 ships with the constant.
//
// The timeout is applied via http.Client.Timeout (not ctx-timeout)
// because http.Client.Timeout also applies to the TLS handshake
// + DNS — ctx-timeout would not protect against a server that
// accepts the TCP connection but stalls the body.
const veloxDownloadTimeout = 5 * time.Minute

// VeloxSource is the HTTP-backed ArtifactSource implementation for
// the upload worker's Velox ingest path.
//
// Per the Velox→InstaEdit contract the artifact arrives as a one-shot
// HTTP GET against an externally-signed download_url; the expected
// SHA256 + size are stored on the external_deliveries row at /deliveries
// acceptance time. Open returns the http.Response.Body verbatim —
// the worker layer wraps it in io.TeeReader + io.LimitReader for the
// single-pass SHA compute + size-guard (operator-readable in
// processIngestJob).
//
// The previous ArtifactStream-based design deferred both the hasher
// and the size guard to the source itself; this revision simplifies
// the surface so the source contract is "give me an honest
// read-until-EOF ReadCloser" and the worker is the verifier. SHA/size
// invariants are unchanged — only their location moved up.
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
// Velox-supplied download_url and returns the size + mime + ETag.
// NOT authoritative for ingest — the worker uses the external_deliveries
// row's expected_size_bytes + expected_sha256 (set at /deliveries POST
// time) as the source of truth.
//
// Inspect errors that are NOT already context errors propagate as
// transient (worker retries with backoff). 404 / 410 / etc. are
// returned verbatim so the worker's classifyUploadError can match on
// the status code for routing.
func (s *VeloxSource) Inspect(ctx context.Context, job *models.UploadJob) (*SourceMetadata, error) {
	downloadURL := resolveVeloxDownloadURL(job)
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
		// Some origins (S3 in legacy configurations) DON'T return
		// Content-Length on HEAD. Treat unknown as "the worker
		// must rely on the external_deliveries row's
		// expected_size_bytes, not the HEAD probe". Inspect is
		// NOT authoritative; Open is.
		size = 0
	}
	return &SourceMetadata{
		SizeBytes: size,
		MimeType:  mime,
		ETag:      etag,
		// SHA256Hex intentionally empty: even if the upstream
		// surfaces it via x-amz-checksum-sha256, the worker still
		// re-derives via TeeReader on Open. Defense-in-depth:
		// never trust upstream-declared hashes.
	}, nil
}

// Open implements ArtifactSource. Issues HTTP GET against the Velox
// download_url and returns the response body verbatim as an
// io.ReadCloser.
//
// The previous ArtifactStream-based design wrapped the body in a
// hasher + size-guard before returning; this revision returns the
// raw body and lets the worker layer compose the verification
// (io.TeeReader + io.LimitReader + storage.Upload). The
// single-pass-SHA-during-stream invariant is preserved — only the
// pipe is on the worker side now.
//
// Caller MUST Close the returned ReadCloser even if Read stopped
// early (size-detected mismatch). The deferred resp.Body.Close()
// inside this function is paired with Close on the returned
// ReadCloser (which closes the same body); double-close is harmless
// because io.ReadCloser's Close is conventionally idempotent.
func (s *VeloxSource) Open(ctx context.Context, job *models.UploadJob) (io.ReadCloser, error) {
	downloadURL := resolveVeloxDownloadURL(job)
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
	return resp.Body, nil
}

// resolveVeloxDownloadURL is the per-job download_url extractor.
// We look at the upload_job for a DownloadURL field (the canonical
// place) and, if absent, treat the SourceID as the URL (for rows
// where Velox stuffed the link into the generic source_id column).
//
// Phase 1 wiring: the upload_job is created via LinkUploadJob with
// external_delivery's ID stuffed into upload_jobs.source_id. The
// Shape is per-row and stored at upload_job time; we read it via
// job.SourceID when no dedicated DownloadURL field exists.
//
// For this commit we degrade gracefully: if job.SourceID parses as
// a URL we treat it AS the download_url. When the proper DownloadURL
// column lands, this function switches to reading it and the
// SourceID-as-URL hack retires.
func resolveVeloxDownloadURL(job *models.UploadJob) string {
	if job == nil {
		return ""
	}
	if strings.HasPrefix(job.SourceID, "http://") || strings.HasPrefix(job.SourceID, "https://") {
		return job.SourceID
	}
	return ""
}
