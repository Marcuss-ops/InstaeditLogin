package sources

import (
	"errors"
	"fmt"
	"sync"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// ArtifactSourceRegistry resolves an upload_job's SourceType into
// the per-source ArtifactSource implementation. The map is the
// single source of truth; adding a new source means calling Register.
//
// Concurrency: the registry is built at process start (main.go
// boot-time wiring) AND only mutated via Register. After Run starts,
// Register is never called from the worker's hot path — callers in
// tests MAY race; production uses the boot-time once-only pattern
// documented on Register.
//
// The package-level NOOP source fallback (Resolve returning
// (nil, false)) is what makes the registry gracefully no-op for
// legacy sources (public_drive DEPRECATED, etc.): the worker sees
// "no registered source for this SourceType" and routes through
// its existing legacy switch instead of crashing.
type ArtifactSourceRegistry struct {
	mu      sync.RWMutex
	sources map[models.UploadJobSource]ArtifactSource
}

// NewArtifactSourceRegistry builds an empty registry. main.go wires
// each source via Register() one at a time during boot.
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
// Production wiring pattern (cmd/server/main.go):
//
//	registry := sources.NewArtifactSourceRegistry()
//	if err := registry.Register(sources.NewVeloxSource(...)); err != nil {
//	    log.Fatal(err)
//	}
//	worker := worker.NewUploadWorker(..., registry, ...)
//
// Register is intended for boot-time ONCE-ONLY use. Calling it after
// the worker pool has started is racy (sync.RWMutex guards the map
// but Resolve from the worker hot path could miss a mid-flight
// Register). Tests that need to swap a source mid-Run can hold the
// write lock via Register holding it organically.
func (r *ArtifactSourceRegistry) Register(source ArtifactSource) error {
	if source == nil {
		return errors.New("artifact source registration: nil source")
	}
	name := source.Name()
	if name == "" {
		return errors.New("artifact source registration: source.Name() returned empty string")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.sources[name]; exists {
		return fmt.Errorf("artifact source already registered: %s", name)
	}
	r.sources[name] = source
	return nil
}

// Resolve returns the source registered for t, plus an "ok" bool
// matching the ok-form reading convention. ok=false means no source
// is registered for t (worker should fall through to its existing
// legacy switch). The returned ArtifactSource is NEVER nil when
// ok=true.
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
// the post-Register set; package-private utility.
func (r *ArtifactSourceRegistry) Names() []models.UploadJobSource {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]models.UploadJobSource, 0, len(r.sources))
	for name := range r.sources {
		out = append(out, name)
	}
	// sort.Slice is overkill for a tiny slice; use the standard
	// library's stable sort via impl. Skip when len < 2 to avoid
	// import-side overhead in tests.
	if len(out) > 1 {
		sortStrings(out)
	}
	return out
}

// sortStrings is a 5-line insert-sort to avoid pulling the sort
// package into this small registry file. The slice length is bounded
// by the few dozen enum values UploadJobSource has (today: 3,
// tomorrow: 5 at most) so O(n²) is fine.
func sortStrings(s []models.UploadJobSource) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
