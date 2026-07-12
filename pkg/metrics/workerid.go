package metrics

import (
	"sync"
)

// workerID is the per-process identity for SPRINT 6.1 (Observability
// with SLO). Set once at process startup (cmd/server/main.go calls
// metrics.SetWorkerID(uuid.New().String())) and read by:
//
//   - slog.With("worker_id", metrics.WorkerID()) on every structured
//     log line emitted from a background goroutine (workers emit
//     thousands of lines per hour; correlating them across replicas
//     requires a worker_id in every line).
//   - The Prometheus scrape job's `external_labels` block, which
//     attaches worker_id to every metric exposed by this process.
//     This is the canonical pattern — a label injected by the
//     scraper, NOT by the application code, so it stays consistent
//     across metric registrations.
//
// Why NOT a metric label:
//
//   Each application-emitted "worker_id" label would multiply the
//   cardinality by N (N = number of worker replicas) for every
//   metric. The scrape job's external_labels approach is "free" —
//   applied by Prometheus at scrape time, applies uniformly, never
//   appears in the application's source. This is the explicit
//   choice documented at the top of observability.go.
//
// Thread safety:
//
//   SetWorkerID is called ONCE at process start (by main.go before
//   the HTTP server starts accepting requests). After that point
//   WorkerID() is the only consumer and reads must be cheap. The
//   RWMutex allows concurrent reads from many goroutines without
//   contention — the write-side critical section is held only
//   during the single SetWorkerID call.
//
// Defensive default "unset":
//
//   If the application code reads WorkerID() BEFORE SetWorkerID has
//   been called (e.g. in a test that doesn't call SetWorkerID), the
//   default value of "unset" makes the missing-init obvious in
//   dashboards / log queries. A panic would also be defensible
//   (fail-fast) but the user's spec calls for structured logs that
//   survive partial init, so we keep the soft default.
var (
	workerID      = "unset"
	workerIDMutex sync.RWMutex
)

// SetWorkerID sets the per-process worker_id. Called once at process
// start from cmd/server/main.go. Idempotent: calling it twice with
// the same value is a no-op; calling it twice with different values
// is a defensive keep-the-first pattern (the first wins — a later
// SetWorkerID call is ignored). The keep-first rule guards against
// a misconfigured test setup from clobbering the production ID.
//
// Returning an error is unnecessary: the input is always a fresh
// UUID from crypto/rand or a test stub; validation would be over-
// engineering. If the input is empty, we keep "unset" rather than
// panicking (fail-open beats fail-closed for identity).
func SetWorkerID(id string) {
	if id == "" {
		return
	}
	workerIDMutex.Lock()
	defer workerIDMutex.Unlock()
	if workerID != "unset" {
		// Keep the first non-empty SetWorkerID call. A second call with
		// a different value (e.g. from a verbose test) does NOT
		// clobber the production ID.
		return
	}
	workerID = id
}

// WorkerID returns the per-process worker_id set by SetWorkerID.
// Returns "unset" if SetWorkerID was never called. Cheap — the
// RWMutex's RLock/RUnlock pair is inlined and contention-free for
// the hot read path (every background goroutine reads it on every
// heartbeat / log line).
func WorkerID() string {
	workerIDMutex.RLock()
	defer workerIDMutex.RUnlock()
	return workerID
}
