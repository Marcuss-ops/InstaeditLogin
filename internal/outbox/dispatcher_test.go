// Package outbox unit-tests for the dispatcher goroutine. The
// dispatcher depends on a narrow OutboxStore interface (not the
// concrete *OutboxRepository) so its tests mock the interface
// directly — no sqlmock, no *sql.DB plumbing, no transactional
// setup. Each test simulates the production contract by sequencing
// mock store returns against the dispatcher's expected call pattern.
//
// The tests themselves live in concern-scoped files:
//
//	dispatcher_dispatch_test.go     — claim/drain loop behaviour
//	dispatcher_retry_test.go        — backoff, max-attempts, re-claim
//	dispatcher_errors_test.go       — terminal/panic/partial-persistence
//	dispatcher_concurrency_test.go  — heartbeat + graceful shutdown
//
// Sub-tests run in-band (not parallel) because they share a mock
// store and the dispatcher's Run() loops are time-sensitive; a
// parallelisation refactor would require per-test OutboxStore
// instances plus separate time.Ticker wiring.
package outbox_test

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
	"github.com/Marcuss-ops/InstaeditLogin/internal/repository"
)

// --- Mock OutboxStore --------------------------------------------------------

// mockOutboxStore drives the dispatcher's interface with a FIFO queue
// of ClaimNext responses (out of which the dispatcher sees one per
// cycle) and a counter set for Assert-able side effects (renews, marks).
//
// The counters are atomic so a test that uses a background dispatcher
// goroutine (e.g. grace-shutdown test) can poll them via atomic.Load
// from the test goroutine without a data race.
type mockOutboxStore struct {
	mu sync.Mutex

	// claimResponses is a FIFO; each call to ClaimNext consumes the
	// next entry. Tests that don't enqueue enough responses see
	// ErrOutboxAlreadyClaimed by default (queue-empty behaviour).
	claimResponses []claimResponse
	claimFallback  error

	// renewErr is the value returned by RenewLease; nil means success.
	// Most happy-path tests don't care because the heartbeat exits
	// cleanly when Mark* clears the lease; this lets tests force the
	// "peer stole the row" path explicitly.
	renewErr error

	// Per-Mark error simulations.
	markProcessedErr  error
	markFailedErr     error
	markDeadLetterErr error

	// Per-Mark dynamic error funcs (when set, override the static
	// markXxxErr). Used by tests that want N-th-call semantics
	// (e.g. fail first call, succeed second to simulate lease expiry
	// + re-claim). Returns the error (or nil) for each call.
	markProcessedFn  func() error
	markFailedFn     func() error
	markDeadLetterFn func() error

	// Counters — accessed atomically because the dispatcher goroutine
	// and the test goroutine race on them.
	renews         atomic.Int64
	markProcessed  atomic.Int64
	markFailed     atomic.Int64
	markDeadLetter atomic.Int64

	// Capture — last args for assertions.
	lastProcessed     atomic.Int64 // OutboxEvent.ID
	lastFailed        atomic.Int64 // OutboxEvent.ID
	lastFailedBo      atomic.Int64 // backoff duration in nanoseconds (0 if nil)
	lastDeadLetter    atomic.Int64 // OutboxEvent.ID
	lastDeadLetterMsg atomic.Value // string
}

type claimResponse struct {
	ev  *models.OutboxEvent
	err error
}

func (m *mockOutboxStore) ClaimNext(_ time.Duration) (*models.OutboxEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.claimResponses) == 0 {
		if m.claimFallback != nil {
			return nil, m.claimFallback
		}
		return nil, repository.ErrOutboxAlreadyClaimed
	}
	resp := m.claimResponses[0]
	m.claimResponses = m.claimResponses[1:]
	return resp.ev, resp.err
}

func (m *mockOutboxStore) RenewLease(_ int64, _ string, _ time.Duration) error {
	m.renews.Add(1)
	return m.renewErr
}

func (m *mockOutboxStore) MarkProcessed(id int64, _ string) error {
	m.markProcessed.Add(1)
	m.lastProcessed.Store(id)
	if m.markProcessedFn != nil {
		return m.markProcessedFn()
	}
	return m.markProcessedErr
}

func (m *mockOutboxStore) MarkFailed(id int64, _ string, _ string, backoff *time.Duration) error {
	m.markFailed.Add(1)
	m.lastFailed.Store(id)
	if backoff != nil {
		m.lastFailedBo.Store(int64(*backoff))
	} else {
		m.lastFailedBo.Store(int64(0))
	}
	if m.markFailedFn != nil {
		return m.markFailedFn()
	}
	return m.markFailedErr
}

func (m *mockOutboxStore) MarkDeadLetter(id int64, _ string, msg string) error {
	m.markDeadLetter.Add(1)
	m.lastDeadLetter.Store(id)
	m.lastDeadLetterMsg.Store(msg)
	if m.markDeadLetterFn != nil {
		return m.markDeadLetterFn()
	}
	return m.markDeadLetterErr
}

// --- Helper: build a minimal claim-shaped OutboxEvent -----------------------

// newEvent constructs a minimal OutboxEvent suitable for the
// dispatcher's claim path. attemptCount is set externally; attempt
// count N means the row has been retried N times and (after N+1)
// would exceed maxAttempts.
func newEvent(id int64, attempt int) *models.OutboxEvent {
	lease := fmt.Sprintf("lease-%d", id)
	return &models.OutboxEvent{
		ID:            id,
		AggregateType: "post_target",
		AggregateID:   100 + id,
		EventType:     "post_target.publish_requested",
		Payload:       []byte(`{"v":1}`),
		Status:        models.OutboxStatusPending,
		LeaseID:       &lease,
		AttemptCount:  attempt,
	}
}

// --- utilities --------------------------------------------------------------

// contains is a small substring check that swallows the strings import
// for tests that don't otherwise need it.
func contains(haystack, needle string) bool {
	if len(needle) == 0 {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
