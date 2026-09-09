package metrics

import (
	"os"
	"strconv"

	"github.com/google/uuid"
)

// NewWorkerID (commit DI refactor) generates a fresh per-process worker id
// WITHOUT registering it as a process-wide global. The format matches the
// historical InitWorkerID contract: "worker-<hostname>-<pid>-<uuid>" —
// log-parseable, stable across the process lifetime, and unique across N
// replicas / restarts.
//
// Usage (explicit DI, the only supported path after the bootstrap DI
// refactor):
//
//	workerID := metrics.NewWorkerID()
//	slog.Info("worker_id initialised", "worker_id", workerID)
//	pw := worker.NewPublishWorker(..., workerID, ...)
//
// The caller stores the value on its own struct (e.g. App.WorkerID) and
// threads it into each worker constructor. There is intentionally NO
// pkg/metrics.WorkerID() global reader and NO SetWorkerID writer: a mutable
// process-global identity read under an RWMutex on every heartbeat/log line
// was the last remaining mutable-global state in this package, and the DI
// refactor removed every production caller of it.
//
// Why a helper at all (vs callers inlining uuid.New().String()):
//   - The format consistency ("worker-<host>-<pid>-<uuid>") lives in
//     one place. A grep for `"worker-.*-"` matches every caller.
//   - main.go doesn't import google/uuid itself; importing pkg/metrics
//     keeps the dependency surface lean.
//
// Failure modes:
//   - hostname lookup error → fallback to "unknown", log id
//     carries "worker-unknown-<pid>-<uuid>". Operator can still
//     disambiguate via pid + uuid.
//   - uuid generator failure (crypto/rand exhaustion — never
//     happens in practice) → panic inside uuid.NewString(). The
//     process can't start without a stable id; fail-fast is right.
//
// SECURITY NOTE: hostname + pid are deliberately emitted into log
// lines and metric external_labels (set by Prometheus scraper, not
// stored in app source). Operators WANT to see host identity in logs
// for cross-replica correlation. The "leak" framing is wrong: this is
// the canonical ops-telemetry use case. Operator workstations that
// need to redact this can run a log-rewriter at the collector.
func NewWorkerID() string {
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "unknown"
	}
	return "worker-" + hostname + "-" + strconv.Itoa(os.Getpid()) + "-" + uuid.NewString()
}
