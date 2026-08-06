package worker

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// RenderPriority controls which media processes are admitted first. Lower
// numeric values have higher priority; aging prevents low-priority starvation.
type RenderPriority int

var ErrInvalidRenderPriority = errors.New("invalid render priority")

const (
	RenderPriorityInteractive RenderPriority = iota
	RenderPriorityPublish
	RenderPriorityThumbnail
	RenderPriorityTranscode
	RenderPriorityLivestream
	RenderPriorityMaintenance
)

const (
	defaultRenderMaxConcurrency = 1
	defaultFFmpegThreads        = 1
	renderStarvationAfter       = 5 * time.Second
)

// RenderRegistryEvent is emitted after queue/admission transitions. Hooks are
// optional and run outside the registry lock.
type RenderRegistryEvent struct {
	Priority RenderPriority
	Wait     time.Duration
	Active   int
	Queued   int
	Threads  int
}

type renderRequest struct {
	ctx      context.Context
	priority RenderPriority
	queuedAt time.Time
	ready    chan struct{}
	granted  bool
}

// RenderLease represents one admitted external media process. Threads is the
// configured per-process budget for future ffmpeg commands; ffprobe is admitted
// by the same process budget but does not accept an ffmpeg -threads flag.
type RenderLease struct {
	Threads int
	once    sync.Once
	release func()
}

// Release returns the registry capacity. It is idempotent and safe to defer.
func (l *RenderLease) Release() {
	if l == nil {
		return
	}
	l.once.Do(func() {
		if l.release != nil {
			l.release()
		}
	})
}

// RenderConcurrencyRegistry is the process-wide admission controller for
// CPU-heavy media processes. It owns one global concurrency budget and a
// priority queue instead of allowing separate limits in each worker.
type RenderConcurrencyRegistry struct {
	mu             sync.Mutex
	maxConcurrency int
	threads        int
	active         int
	queues         map[RenderPriority][]*renderRequest
	onQueued       func(RenderRegistryEvent)
	onAcquired     func(RenderRegistryEvent)
	onReleased     func(RenderRegistryEvent)
}

// NewRenderConcurrencyRegistry constructs a registry. Non-positive values
// are normalized to a conservative CPU-only default of one process/thread.
func NewRenderConcurrencyRegistry(maxConcurrency, ffmpegThreads int) *RenderConcurrencyRegistry {
	if maxConcurrency <= 0 {
		maxConcurrency = defaultRenderMaxConcurrency
	}
	if ffmpegThreads <= 0 {
		ffmpegThreads = defaultFFmpegThreads
	}
	return &RenderConcurrencyRegistry{
		maxConcurrency: maxConcurrency,
		threads:        ffmpegThreads,
		queues:         make(map[RenderPriority][]*renderRequest),
	}
}

// DefaultRenderConcurrencyRegistry is the safe fallback for tests and
// components constructed without bootstrap wiring. Production injects the
// configured App registry so every worker shares one budget.
var defaultRenderConcurrencyRegistry = NewRenderConcurrencyRegistry(0, 0)

func DefaultRenderConcurrencyRegistry() *RenderConcurrencyRegistry {
	return defaultRenderConcurrencyRegistry
}

// SetHooks installs optional low-cardinality observability callbacks.
func (r *RenderConcurrencyRegistry) SetHooks(onQueued, onAcquired, onReleased func(RenderRegistryEvent)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.onQueued, r.onAcquired, r.onReleased = onQueued, onAcquired, onReleased
}

// Acquire waits for a priority slot and returns the controlled thread count.
// Cancellation removes a queued request and never consumes capacity.
func (r *RenderConcurrencyRegistry) Acquire(ctx context.Context, priority RenderPriority) (*RenderLease, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if priority < RenderPriorityInteractive || priority > RenderPriorityMaintenance {
		return nil, fmt.Errorf("%w: %d", ErrInvalidRenderPriority, priority)
	}
	req := &renderRequest{
		ctx: ctx, priority: priority, queuedAt: time.Now(), ready: make(chan struct{}),
	}

	r.mu.Lock()
	if r.active < r.maxConcurrency && r.queuedLocked() == 0 {
		r.active++
		event := r.eventLocked(priority, 0)
		hook := r.onAcquired
		r.mu.Unlock()
		if hook != nil {
			hook(event)
		}
		return r.lease(), nil
	}
	r.queues[priority] = append(r.queues[priority], req)
	event := r.eventLocked(priority, 0)
	hook := r.onQueued
	r.mu.Unlock()
	if hook != nil {
		hook(event)
	}

	select {
	case <-req.ready:
		r.mu.Lock()
		granted := req.granted
		if granted {
			wait := time.Since(req.queuedAt)
			event := r.eventLocked(priority, wait)
			hook := r.onAcquired
			r.mu.Unlock()
			if hook != nil {
				hook(event)
			}
			return r.lease(), nil
		}
		r.mu.Unlock()
		return nil, ctx.Err()
	case <-ctx.Done():
		// A release may grant this request concurrently with cancellation.
		// In that race the request is already out of its queue, so remove
		// would do nothing and the slot would leak unless we release it
		// explicitly here.
		r.mu.Lock()
		granted := req.granted
		if !granted {
			r.removeLocked(req)
		}
		r.mu.Unlock()
		if granted {
			r.release()
		}
		return nil, ctx.Err()
	}
}

func (r *RenderConcurrencyRegistry) lease() *RenderLease {
	return &RenderLease{Threads: r.threads, release: r.release}
}

func (r *RenderConcurrencyRegistry) release() {
	r.mu.Lock()
	var event RenderRegistryEvent
	hook := r.onReleased
	if req := r.nextLocked(); req != nil {
		req.granted = true
		close(req.ready)
		event = r.eventLocked(req.priority, 0)
	} else {
		r.active--
		event = r.eventLocked(0, 0)
	}
	r.mu.Unlock()

	// Hooks are external code and may inspect or use the registry. Never
	// invoke them while holding r.mu, otherwise a metrics callback that
	// calls Active/Queued (or a re-entrant Acquire) can deadlock.
	if hook != nil {
		hook(event)
	}
}

func (r *RenderConcurrencyRegistry) remove(target *renderRequest) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.removeLocked(target)
}

func (r *RenderConcurrencyRegistry) removeLocked(target *renderRequest) {
	queue := r.queues[target.priority]
	for i, req := range queue {
		if req == target {
			r.queues[target.priority] = append(queue[:i], queue[i+1:]...)
			return
		}
	}
}

func (r *RenderConcurrencyRegistry) queuedLocked() int {
	total := 0
	for _, queue := range r.queues {
		total += len(queue)
	}
	return total
}

func (r *RenderConcurrencyRegistry) nextLocked() *renderRequest {
	now := time.Now()
	var oldest *renderRequest
	// Iterate in priority order rather than ranging over the map so equal
	// enqueue timestamps have deterministic priority tie-breaking.
	for priority := RenderPriorityInteractive; priority <= RenderPriorityMaintenance; priority++ {
		for _, req := range r.queues[priority] {
			if req.ctx.Err() != nil {
				continue
			}
			if oldest == nil || req.queuedAt.Before(oldest.queuedAt) {
				oldest = req
			}
		}
	}
	if oldest == nil {
		for priority := range r.queues {
			r.queues[priority] = nil
		}
		return nil
	}
	if now.Sub(oldest.queuedAt) >= renderStarvationAfter {
		return r.popLocked(oldest)
	}
	for priority := RenderPriorityInteractive; priority <= RenderPriorityMaintenance; priority++ {
		for _, req := range r.queues[priority] {
			if req.ctx.Err() == nil {
				return r.popLocked(req)
			}
		}
	}
	return nil
}

func (r *RenderConcurrencyRegistry) popLocked(target *renderRequest) *renderRequest {
	queue := r.queues[target.priority]
	for i, req := range queue {
		if req == target {
			r.queues[target.priority] = append(queue[:i], queue[i+1:]...)
			return req
		}
	}
	return nil
}

func (r *RenderConcurrencyRegistry) eventLocked(priority RenderPriority, wait time.Duration) RenderRegistryEvent {
	return RenderRegistryEvent{Priority: priority, Wait: wait, Active: r.active, Queued: r.queuedLocked(), Threads: r.threads}
}

// Active and Queued expose snapshots for health/metrics tests without exposing
// internal queues.
func (r *RenderConcurrencyRegistry) Active() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.active
}

func (r *RenderConcurrencyRegistry) Queued() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.queuedLocked()
}

// Priorities returns the supported queue order for config/observability UI.
func Priorities() []RenderPriority {
	out := []RenderPriority{RenderPriorityInteractive, RenderPriorityPublish, RenderPriorityThumbnail, RenderPriorityTranscode, RenderPriorityLivestream, RenderPriorityMaintenance}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
