package worker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// ArtifactSource is the per-source ingest contract for the upload
// worker. Each implementation owns its upstream protocol (Velox
// today: HTTP HEAD + GET against a signed download URL; Authenticated
// Drive today: Google OAuth refresh + Files API download; future:
// Dropbox, S3-drop, etc.).
//
// The interface is intentionally narrow: three methods. The worker
// composes the ingest lifecycle:
//
//  1. Resolve source from registry via job.SourceType.
//  2. (Optional) Call Inspect for pre-flight (size, mime, etag).
//     Not authoritative for ingest — byte-stream comparison
//     downstream is the source of truth.
//  3. Call Open to obtain an io.ReadCloser for the artifact bytes.
//  4. Wrap the body in io.TeeReader + io.LimitReader at the worker
//     level so SHA + size verification happens in a single pass
//     while storage.Upload drains the bytes into S3.
//
// Inspect is the cheap pre-flight; Open is the load-bearing call.
// SHA/size verification lived inside the source in the prior
// ArtifactStream-based design; this revision moves that
// responsibility to the worker (where the verify-after-Read
// invariant still holds but the per-source plumbing is simpler).
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
	// MAY return ErrInspectNotImplemented — the worker treats it
	// as a no-op and skips the pre-flight, falling back to headers
	// or to the external_deliveries row's declared triple.
	Inspect(ctx context.Context, job *models.UploadJob) (*SourceMetadata, error)

	// Open streams the artifact bytes via an io.ReadCloser. The
	// caller (worker) MUST Close the returned ReadCloser.
	//
	// The worker layer wraps the returned ReadCloser with
	// io.TeeReader(sha256.New()) for SHA computation AND
	// io.LimitReader(expectedSize+1) for size-guard, so an
	// over-size body triggers a CleanRead+io.EOF BEFORE the
	// bytes hit storage.Upload. SHA is computed DURING the same
	// single pass so post-Read verification is one-shot.
	//
	// Expected size + SHA come from the external_deliveries row
	// for Velox, OR from content-length on subsequent reads for
	// streams that surface it. The worker is responsible for
	// hydrating those values; the source is responsible for
	// returning an honest read-until-EOF byte stream.
	Open(ctx context.Context, job *models.UploadJob) (io.ReadCloser, error)
}

// ErrInspectNotImplemented is the typed sentinel Inspect returns
// when the source doesn't have a usable pre-flight probe. Workers
// use errors.Is(err, ErrInspectNotImplemented) to skip the
// pre-flight without treating it as an error.
var ErrInspectNotImplemented = errors.New("artifact source: inspect not implemented")

// SourceMetadata is the artifact metadata extracted from Inspect.
// Field rationale:
//
//	SizeBytes: server-reported length (HEAD Content-Length or
//	  equivalent). Used by the UI to show "importing a 800 MiB file".
//	  For ingest, the worker ALSO reads Content-Length from the
//	  Open response headers or the external_deliveries row when
//	  Inspect is not implemented by the source.
//
//	MimeType: server-reported. Used by the UI for the "we're
//	  uploading a video/mp4" badge. The ingest layer uses
//	  external_deliveries.expected_mime_type (the upstream's
//	  declared mime) NOT this, when both are present.
//
//	ETag: server-reported cache validator. Used to short-circuit
//	  repeated Inspects (cheap); for ingest, the worker's
//	  comparison is byte-level via SHA so the etag is purely
//	  observational.
//
//	SHA256Hex: server-reported SHA-256 (some S3-compatible stores
//	  surface this via x-amz-checksum-sha256 or similar; Velox may
//	  or may not). When present, a fast-path source MAY use it
//	  to skip the on-the-fly hasher and trust the upstream; when
//	  absent, the source stays honest and the worker hashes via
//	  TeeReader. Empty string means "I don't have it".
type SourceMetadata struct {
	SizeBytes int64
	MimeType  string
	ETag      string
	SHA256Hex string
}

// ArtifactSourceRegistry resolves an upload_job's SourceType into
// the per-source ArtifactSource implementation. The map is the
// single source of truth; adding a new source means calling Register.
//
// Concurrency: the registry is built at process start (main.go
// boot-time wiring) AND only mutated via Register. After Run starts,
// Register is rarely called from the worker's hot path — production
// uses the boot-time once-only pattern documented on Register.
// sync.RWMutex guards concurrent Resolve (read-mostly) — many
// goroutines Resolve per tick without serialising on each call.
type ArtifactSourceRegistry struct {
	mu      sync.RWMutex
	sources map[models.UploadJobSource]ArtifactSource
}

// NewArtifactSourceRegistry builds an empty registry. bootstrap/app.go
// wires each source via Register() one at a time during boot.
func NewArtifactSourceRegistry() *ArtifactSourceRegistry {
	return &ArtifactSourceRegistry{
		sources: make(map[models.UploadJobSource]ArtifactSource),
	}
}

// Register adds source to the registry keyed by source.Name(). The
// Name() must be unique across the registry — duplicate registration
// returns an error so a misconfigured boot doesn't silently shadow
// a source. nil sources are rejected (caller bug — passing nil
// typically means the constructor wasn't called; surface this
// fail-loud at boot, not silently via Resolve returning nil).
//
// Empty Name() is rejected for the same reason — a source that
// doesn't surface its key can't be resolved and is almost certainly
// a programming error.
//
// Production wiring pattern:
//
//	registry := worker.NewArtifactSourceRegistry()
//	if err := registry.Register(worker.NewVeloxSource(logger)); err != nil {
//	    log.Fatal(err)
//	}
//	if err := registry.Register(worker.NewAuthenticatedDriveSource(importer, vault)); err != nil {
//	    log.Fatal(err)
//	}
//	uw := worker.NewUploadWorker(..., registry)
//
// Register is intended for boot-time ONCE-ONLY use. Calling it after
// the worker pool has started is racy (sync.RWMutex guards the map
// but Resolve from the worker hot path could miss a mid-flight
// Register).
func (r *ArtifactSourceRegistry) Register(source ArtifactSource) error {
	if source == nil {
		return errors.New("artifact source Register: nil source")
	}
	name := source.Name()
	if name == "" {
		return errors.New("artifact source Register: source.Name() returned empty string")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.sources[name]; exists {
		return fmt.Errorf("artifact source Register: already registered for %s", name)
	}
	r.sources[name] = source
	return nil
}

// Resolve returns the source registered for t, plus an "ok" bool
// matching the ok-form reading convention. ok=false means no source
// is registered for t (worker should fall through to its existing
// legacy switch OR return an "unsupported source type" error). The
// returned ArtifactSource is NEVER nil when ok=true.
//
// Resolve is read-mostly (worker hot path) — uses RLock not Lock
// to allow concurrent Resolves from the per-row goroutines without
// serialising on each call.
func (r *ArtifactSourceRegistry) Resolve(t models.UploadJobSource) (ArtifactSource, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	src, ok := r.sources[t]
	return src, ok
}

// Names returns the registered source names sorted lexicographically
// (deterministic across process restarts). Used by tests asserting
// the post-Register set. The slice length is bounded by the few
// dozen enum values UploadJobSource has; O(n²) insert-sort is fine.
func (r *ArtifactSourceRegistry) Names() []models.UploadJobSource {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]models.UploadJobSource, 0, len(r.sources))
	for name := range r.sources {
		out = append(out, name)
	}
	if len(out) > 1 {
		for i := 1; i < len(out); i++ {
			for j := i; j > 0 && out[j-1] > out[j]; j-- {
				out[j-1], out[j] = out[j], out[j-1]
			}
		}
	}
	return out
}
