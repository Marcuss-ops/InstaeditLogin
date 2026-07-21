package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/database"
)

// readyTimeout caps each /ready sub-check. The whole endpoint MUST
// return inside this window so a slow DB isn't held open longer
// than the K8s readinessProbe's timeoutSeconds (typically 1-3s).
// 2s leaves room for two slow queries (DB ping + SchemaHealthy)
// without tripping default probe budgets.
const readyTimeout = 2 * time.Second

// readinessCheck is the contract the /ready handler depends on for
// its 3 green-path dots. Tests inject stubs that satisfy this
// shape (DB pinger, migrations checker, worker-status query).
// Kept narrow: an in-memory fake satisfies it without dragging
// the real *sql.DB or production WorkerStatus into unit tests.
//
// The implementation bound to the production wiring is
// router.handleReady (the same Receiver owns DB + WorkerStatus);
// the per-check methods below are the call sites.
type readinessCheck interface {
	PingDB(ctx context.Context) error
	MigrationsApplied(ctx context.Context) error
	AllWorkersStarted(ctx context.Context) (allOK bool, pending []string)
}

// readinessResponse is the canonical JSON envelope /ready returns.
// Per-check status substrings ("ok"/"db_down"/"migrations_missing"/"workers_not_ready")
// are stable across releases so an alert monitor can pattern-match
// on them. The numeric overall HTTP status follows the "any failure
// = 503, all green = 200" canonical readiness contract.
type readinessResponse struct {
	Status         string   `json:"status"`                    // "ok" | "not_ready"
	DB             string   `json:"db"`                        // "ok" | <error string>
	Migrations     string   `json:"migrations"`                // "ok" | <error string>
	WorkersReady   bool     `json:"workers_ready"`             // allOK from AllWorkersStarted
	WorkersPending []string `json:"workers_pending,omitempty"` // non-empty when not all started
}

// WithWorkerStatus wires the per-goroutine "started" monitor into
// the Router. Production wiring in internal/bootstrap.Wire passes
// app.WorkerStatus (*api.WorkerStatus). Tests pass nil + use the
// in-memory WorkerStatus stub via a separate test-only helper.
func WithWorkerStatus(ws *WorkerStatus) RouterOption {
	return func(r *Router) { r.workerStatus = ws }
} // handleReady is the production /ready implementation. It runs the
// 3 readiness checks with a single shared context bounded by
// readyTimeout; any sub-check that exceeds the budget produces an
// error string in its slot, the overall response is 503, and the
// caller (Fly readinessProbe or K8s readinessProbe) drops the pod
// from the rotation until the next tick.
//
// Why top-level (NOT /api/v1/ready): the readiness probe is
// anonymous from the orchestrator's perspective (no JWT, no
// cookie), and orchestrators should not have to know the API's
// URL convention. /ready is also the canonical Kubernetes-friendly
// probe path.
//
// Rate limiter + CORS are NOT enforced on this route: probes never
// carry credentials, and basic rate-limit-on-IP would harm a probe's
// retry storm. Logging still applies (every /ready hit shows in
// slog) so an operator can see probe traffic.
func (r *Router) handleReady(w http.ResponseWriter, req *http.Request) {
	ctx, cancel := context.WithTimeout(req.Context(), readyTimeout)
	defer cancel()

	resp := readinessResponse{Status: "ok"}

	// Check 1 — DB ping. Connection-pool pressure or a killed
	// Postgres are the typical failures here.
	if err := r.pingDB(ctx); err != nil {
		resp.DB = err.Error()
		resp.Status = "not_ready"
	} else {
		resp.DB = "ok"
	}

	// Check 2 — migrations schema-state. SchemaHealthy misses a
	// canary table → not_ready (the operator needs to run
	// cmd/migrate to apply pending migrations).
	if err := r.migrationsApplied(ctx); err != nil {
		resp.Migrations = err.Error()
		resp.Status = "not_ready"
	} else {
		resp.Migrations = "ok"
	}

	// Check 3 — workers started. Atomic.Bool flipped on
	// goroutine-entry; missing workers = something deadlocked
	// during RunWorkers setup (DB connection, registry build,
	// etc.).
	if r.workerStatus != nil {
		allOK, pending := r.workerStatus.AllStarted()
		resp.WorkersReady = allOK
		resp.WorkersPending = pending
		if !allOK {
			resp.Status = "not_ready"
		}
	} else {
		// No WorkerStatus wired (test fixture or pre-bloco
		// bootstrap call) → assume the worker side is OK; the
		// proxy's responsibility is to wire it in production.
		resp.WorkersReady = true
	}

	status := http.StatusOK
	if resp.Status != "ok" {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, resp)
}

// pingDB rounds a DB ping through the bounded ctx so a stuck
// Postgres doesn't hold /ready open past the budget.
func (r *Router) pingDB(ctx context.Context) error {
	if r.dbForReady == nil {
		// No DB wired (test fixture not configured): the readiness
		// probe says DB is fine so other checks can run. Production
		// ALWAYS wires r.dbForReady via api.WithDB — see bootstrap.
		return nil
	}
	return r.dbForReady.PingContext(ctx)
}

// migrationsApplied delegates to internal/database.SchemaHealthy.
// The returned error names the first canary table that's missing —
// the /ready envelope surfaces that string so the operator can
// see "users" or "workspace_id" and know which migration failed
// to apply.
func (r *Router) migrationsApplied(ctx context.Context) error {
	if r.dbForReady == nil {
		// No DB wired (test fixture): treat as "not configured".
		// The /ready body surfaces "db not configured" so the
		// operator notices the misconfiguration.
		return errReadyDBNotConfigured
	}
	return database.SchemaHealthy(r.dbForReady)
}

// errReadyDBNotConfigured is the sentinel returned by /ready when
// the Router was constructed without api.WithDB. The HTTP handler
// surfaces this via the migrations slot so the alert monitor sees
// a single canonical string and the operator can correlate.
var errReadyDBNotConfigured = errors.New("db not configured for /ready (api.WithDB missing)")
