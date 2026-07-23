// Package worker implements background processes for InstaEditLogin.
// registry.go owns the lifecycle, supervision, and observability of
// every long-running worker goroutine.
package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// DefaultShutdownTimeout is the global timeout the registry uses when
// waiting for all workers to stop during graceful shutdown.
const DefaultShutdownTimeout = 15 * time.Second

// errShutdownTimeout marks a worker that ignored context cancellation
// and had to be force-failed when the global shutdown deadline
// expired. It is not treated as a runtime critical failure.
var errShutdownTimeout = errors.New("worker did not stop within shutdown deadline")

// WorkerState is the lifecycle state of a registered worker.
type WorkerState string

const (
	// StateStarting is set while the worker goroutine is being launched.
	StateStarting WorkerState = "starting"
	// StateHealthy means the worker has started and is still running.
	StateHealthy WorkerState = "healthy"
	// StateFailed means the worker terminated with a non-nil error.
	StateFailed WorkerState = "failed"
	// StateStopped means the worker returned cleanly (usually because
	// its context was cancelled during shutdown).
	StateStopped WorkerState = "stopped"
)

// WorkerStatus is a point-in-time snapshot of a single worker.
type WorkerStatus struct {
	Name          string      `json:"name"`
	State         WorkerState `json:"state"`
	Critical      bool        `json:"critical"`
	LastSuccessAt time.Time   `json:"last_success_at,omitempty"`
	HeartbeatAt   time.Time   `json:"heartbeat_at,omitempty"`
	Error         string      `json:"error,omitempty"`
}

// WorkerSpec describes a worker to be supervised by the registry.
type WorkerSpec struct {
	Name     string
	Critical bool
	// Run is the blocking entry point for the worker. It should honour
	// ctx cancellation and return nil (or context.Canceled) on
	// shutdown. Any other error is interpreted as an unexpected failure.
	Run func(ctx context.Context) error
}

// Registry supervises a set of workers from start to shutdown.
type Registry struct {
	mu               sync.RWMutex
	workers          []WorkerSpec
	status           map[string]*WorkerStatus
	wg               sync.WaitGroup
	started          bool
	stopOnce         sync.Once
	cancel           context.CancelFunc
	stopped          chan struct{}
	criticalSent     atomic.Bool
	shutdownDeadline atomic.Value // time.Time set by StopAll
}

// NewRegistry creates an empty worker registry.
func NewRegistry() *Registry {
	r := &Registry{
		workers: []WorkerSpec{},
		status:  make(map[string]*WorkerStatus),
		stopped: make(chan struct{}),
	}
	r.shutdownDeadline.Store(time.Time{})
	return r
}

// Register adds a worker spec to the registry. Register must be called
// before StartAll; calling it afterwards is a no-op.
func (r *Registry) Register(spec WorkerSpec) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.started {
		return
	}
	if spec.Name == "" {
		panic("worker.Registry.Register: worker name cannot be empty")
	}
	if spec.Run == nil {
		panic(fmt.Sprintf("worker.Registry.Register: worker %q has nil Run", spec.Name))
	}

	r.workers = append(r.workers, spec)
	r.status[spec.Name] = &WorkerStatus{Name: spec.Name, State: StateStarting, Critical: spec.Critical}
}

// StartAll launches every registered worker in its own goroutine and
// returns a channel that receives an error the first time a critical
// worker exits unexpectedly. The channel closes after the registry has
// been fully stopped.
func (r *Registry) StartAll(ctx context.Context) <-chan error {
	r.mu.Lock()
	if r.started {
		r.mu.Unlock()
		ch := make(chan error, 1)
		close(ch)
		return ch
	}

	r.started = true
	ctx, cancel := context.WithCancel(ctx)
	r.cancel = cancel
	criticalErr := make(chan error, 1)

	for _, spec := range r.workers {
		r.wg.Add(1)
		go r.supervise(ctx, spec, criticalErr)
	}

	// Close the stopped channel once every supervisor has exited so
	// StopAll can return without a timeout. The critical error
	// channel is also closed at this point unless a critical worker
	// already reported a failure.
	go func() {
		r.wg.Wait()
		close(r.stopped)
		if !r.criticalSent.Load() {
			close(criticalErr)
		}
	}()
	r.mu.Unlock()

	return criticalErr
} // supervise runs a single worker and keeps its state/heartbeat up to date.
func (r *Registry) supervise(parent context.Context, spec WorkerSpec, criticalErr chan<- error) {
	defer r.wg.Done() // balance the Add(1) in StartAll when this supervisor exits.
	defer func() {
		// Ensure we always update final state, even on panic.
		if rec := recover(); rec != nil {
			r.setError(spec.Name, fmt.Sprintf("panic: %v", rec))
			r.setState(spec.Name, StateFailed)
			if spec.Critical {
				select {
				case criticalErr <- fmt.Errorf("critical worker %q panicked: %v", spec.Name, rec):
				default:
				}
			}
		}
	}()

	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	r.setState(spec.Name, StateStarting)

	done := make(chan error, 1)
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				done <- fmt.Errorf("panic: %v", rec)
			}
			close(done)
		}()
		done <- spec.Run(ctx)
	}()

	heartbeatTicker := time.NewTicker(5 * time.Second)
	defer heartbeatTicker.Stop()

	for {
		select {
		case <-parent.Done():
			// Wait for the worker goroutine to finish, bounded by the single
			// global shutdown deadline set by StopAll. All workers share the
			// same budget in parallel; a worker that ignores context
			// cancellation is force-failed when the deadline expires so the
			// supervisor goroutine does not leak.
			deadline, _ := r.shutdownDeadline.Load().(time.Time)
			wait := DefaultShutdownTimeout
			if !deadline.IsZero() {
				if d := time.Until(deadline); d > 0 {
					wait = d
				}
			}
			select {
			case err := <-done:
				r.handleExit(spec, err, criticalErr)
			case <-time.After(wait):
				r.handleExit(spec, fmt.Errorf("worker %q: %w", spec.Name, errShutdownTimeout), criticalErr)
			}
			return
		case <-heartbeatTicker.C:
			r.recordHeartbeat(spec.Name)
		case err := <-done:
			r.handleExit(spec, err, criticalErr)
			return
		}
	}
}

// handleExit updates state when a worker goroutine returns. If the
// worker is critical and the error is not a context cancellation or
// shutdown timeout, the error is sent on criticalErr so the process can
// fail. Shutdown timeouts are not propagated as runtime failures.
func (r *Registry) handleExit(spec WorkerSpec, err error, criticalErr chan<- error) {
	if err == nil || errors.Is(err, context.Canceled) {
		r.setState(spec.Name, StateStopped)
		return
	}

	if errors.Is(err, errShutdownTimeout) {
		r.setError(spec.Name, err.Error())
		r.setState(spec.Name, StateFailed)
		return
	}

	r.setError(spec.Name, err.Error())
	r.setState(spec.Name, StateFailed)
	if spec.Critical {
		r.criticalSent.Store(true)
		select {
		case criticalErr <- fmt.Errorf("critical worker %q exited: %w", spec.Name, err):
		default:
		}
	}
}

// StopAll cancels the context shared by all workers and waits up to
// timeout for every worker to stop. It is safe to call multiple times.
func (r *Registry) StopAll(timeout time.Duration) error {
	// Establish a single global shutdown deadline *before* cancelling the
	// parent context so every supervisor shares the same budget and can
	// unblock itself if its worker ignores cancellation.
	deadline := time.Now().Add(timeout)
	r.shutdownDeadline.Store(deadline)

	r.stopOnce.Do(func() {
		if r.cancel != nil {
			r.cancel()
		}
	})

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	r.mu.RLock()
	stopped := r.stopped
	r.mu.RUnlock()

	select {
	case <-stopped:
		return nil
	case <-timer.C:
		return fmt.Errorf("worker shutdown did not complete within %s", timeout)
	}
}

// GetStatus returns a snapshot of every registered worker's status.
func (r *Registry) GetStatus() []WorkerStatus {
	r.mu.RLock()
	defer r.mu.RUnlock()
	statuses := make([]WorkerStatus, 0, len(r.status))
	for _, s := range r.status {
		statuses = append(statuses, *s)
	}
	return statuses
}

// AllHealthy returns true when every registered worker is either
// starting or healthy and no worker has failed.
func (r *Registry) AllHealthy() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, s := range r.status {
		switch s.State {
		case StateStarting, StateHealthy:
			// still ok
		case StateStopped:
			// stopped during shutdown is not unhealthy
		default:
			return false
		}
	}
	return len(r.status) > 0
}

// Heartbeat marks a specific worker as alive. Workers that have access
// to the registry can call this to refresh the heartbeat timestamp
// when they complete a meaningful unit of work.
func (r *Registry) Heartbeat(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.status[name]
	if !ok {
		return
	}
	s.HeartbeatAt = time.Now()
	if s.State == StateStarting {
		s.State = StateHealthy
	}
}

// RecordSuccess updates the last success timestamp for a worker.
func (r *Registry) RecordSuccess(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.status[name]
	if !ok {
		return
	}
	s.LastSuccessAt = time.Now()
}

func (r *Registry) recordHeartbeat(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.status[name]
	if !ok {
		return
	}
	s.HeartbeatAt = time.Now()
	if s.State == StateStarting {
		s.State = StateHealthy
	}
}

func (r *Registry) setState(name string, state WorkerState) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.status[name]
	if !ok {
		return
	}
	s.State = state
	if state == StateHealthy {
		s.HeartbeatAt = time.Now()
	}
}

func (r *Registry) setError(name string, msg string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	s, ok := r.status[name]
	if !ok {
		return
	}
	s.Error = msg
}

// LogStatus writes the current status of every worker to the provided
// logger. Useful for shutdown/diagnostics logging.
func (r *Registry) LogStatus(logger *slog.Logger) {
	if logger == nil {
		return
	}
	for _, s := range r.GetStatus() {
		logger.Info("worker status",
			"name", s.Name,
			"state", s.State,
			"critical", s.Critical,
			"heartbeat_at", s.HeartbeatAt,
			"last_success_at", s.LastSuccessAt,
			"error", s.Error)
	}
}
