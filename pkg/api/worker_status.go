package api

import (
	"sort"
	"sync/atomic"
)

// WorkerNames lists the 7 background goroutines RunWorkers spawns.
// The list is the canonical "what should the /ready endpoint check"
// surface used by WorkerStatus. Keep in sync with
// internal/bootstrap/app.go::RunWorkers' spawn order (publish,
// reconcile, outbox, webhook, metrics, sessions_cleanup, upload).
//
// Lives in pkg/api (not in internal/bootstrap) because the /ready
// handler is in pkg/api and the type visibility must satisfy BOTH
// packages. The dependency direction in this repo is one-way
// (internal/* may import pkg/api; pkg/api never imports internal/*).
// The WorkerStatus type itself is owned by pkg/api; internal/bootstrap
// constructs and stores *pkg/api.WorkerStatus on App.WorkerStatus.
var WorkerNames = []string{"publish", "reconcile", "outbox", "webhook", "metrics", "sessions_cleanup", "upload"}

// WorkerStatus holds the per-goroutine "started" signal used by the
// /ready endpoint. Each entry is an atomic.Bool flipped to true on
// the goroutine's first executable line in RunWorkers. The /ready
// handler reads AllStarted() to decide whether to mark the process
// ready.
//
// Interpretation (Blocco #5.3): "started" is at-goroutine-entry. It
// proves the goroutine reached its first executable statement
// (didn't deadlock in setup) but does NOT prove the first tick
// completed. Whether to wait for first-tick before declaring ready
// is a deploy-policy decision (K8s readinessProbe with a tighter
// timeout would want it); for the Blocco #5.3 contract "started (no
// deadlock)" at-goroutine-entry is sufficient.
//
// Concurrency: AllStarted reads every flag under its own atomic.Load
// (no lock required); flag-stores are STORE then atomic.Bool's
// happens-before edge gives subsequent Loads a guaranteed-visible
// true value.
type WorkerStatus struct {
	flags map[string]*atomic.Bool
}

// NewWorkerStatus constructs a status monitor with one flag per name.
// Names are typically WorkerNames (the canonical 7 goroutines), but
// tests can supply a smaller list.
func NewWorkerStatus(names []string) *WorkerStatus {
	flags := make(map[string]*atomic.Bool, len(names))
	for _, n := range names {
		flags[n] = new(atomic.Bool)
	}
	return &WorkerStatus{flags: flags}
}

// Mark flips the named worker's flag to true. Idempotent. Names
// not registered at construction are silently ignored (defensive
// for future workers that add a name without updating NewWorkerStatus).
func (w *WorkerStatus) Mark(name string) {
	if f, ok := w.flags[name]; ok {
		f.Store(true)
	}
}

// AllStarted returns (true, nil) when every registered worker has
// flipped its flag; otherwise (false, pending) with pending listing
// the not-yet-started workers in alphabetical order. Defensive: an
// empty WorkerStatus (or no goroutines spawned) returns (true, nil)
// because no worker can be missing.
func (w *WorkerStatus) AllStarted() (allOK bool, pending []string) {
	if len(w.flags) == 0 {
		return true, nil
	}
	for name, f := range w.flags {
		if !f.Load() {
			pending = append(pending, name)
		}
	}
	// Stable order for log/diff sanity.
	sort.Strings(pending)
	return len(pending) == 0, pending
}
