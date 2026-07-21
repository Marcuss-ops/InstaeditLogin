package worker

import (
	"errors"
	"time"

	"encoding/json"

	"github.com/Marcuss-ops/InstaeditLogin/internal/models"
)

// ------------------------------------------------------------------
// mockPostStore — driver-only repository mock. Distinct from
// mockReconcilePostStore (reconcile_worker_test.go) because the
// driver's surface (ListPending, ClaimQueuedTarget, FindByID,
// SetProviderIdempotencyKey) is different from the reconciler's
// (ListPublishing, UpdateStatus). The interface split
// (PublisherPostStore vs ReconcilePostStore) compiles-in the
// invariant that each goroutine only exercises its own surface.
// ------------------------------------------------------------------

// mockPostStore is a PublisherPostStore with configurable function
// fields and call counters. The counters let each test assert the
// exact ordering of repository calls (e.g. Claim must happen BEFORE
// FindByID).
//
// Taglio 4.7 LEVEL 2: added setKeyFn + setKeyCalls + setKeyIDs +
// setKeyVals for the SetProviderIdempotencyKey interface method.
// Default behaviour (no setKeyFn configured) is no-op return nil so
// tests that don't exercise the stamp path continue to pass.
//
// Taglio 5.x: dropped listPublishingFn + updatePublishStateFn +
// related counters. Those moved to mockReconcilePostStore
// (reconcile_worker_test.go) on the Reconciler's surface.
type mockPostStore struct {
	// Call counters — one per method, incremented on every invocation.
	// Tests assert on the relative ordering (e.g. claimCalls > 0 before
	// findByIDCalls is allowed) and the final counts.
	claimCalls       int
	findByIDCalls    int
	updateCalls      int
	listPendingCalls int
	setKeyCalls      int

	// Function fields — each test overrides only what it exercises.
	listPendingFn  func(before time.Time) ([]models.PostTarget, error)
	claimFn        func(id int64) (bool, error)
	findByIDFn     func(id int64) (*models.Post, error)
	updateStatusFn func(*models.PostTarget) error
	// setKeyFn lets a test simulate ErrProviderIdempotencyConflict
	// from the repository's SetProviderIdempotencyKey call. Default
	// (nil) returns nil — the worker's happy path.
	setKeyFn func(id int64, key string) error

	// Captured targets from UpdateStatus — lets tests inspect the
	// final status (published vs failed) and assert on the worker
	// writing the right terminal state. Stored as struct values
	// (not pointers) so later mutations to the caller's target
	// don't leak into the captured snapshot.
	updateTargets []models.PostTarget

	// Captured SetProviderIdempotencyKey calls — (id, key) pairs in
	// invocation order. Tests verify the deterministic SHA-256
	// prefix path produced the expected hex string for a given
	// (post, account) pair on the last attempt.
	setKeyIDs  []int64
	setKeyVals []string
}

func (m *mockPostStore) ListPending(before time.Time) ([]models.PostTarget, error) {
	m.listPendingCalls++
	if m.listPendingFn == nil {
		return nil, nil
	}
	return m.listPendingFn(before)
}

func (m *mockPostStore) FindByID(id int64) (*models.Post, error) {
	m.findByIDCalls++
	if m.findByIDFn == nil {
		return nil, errors.New("FindByID not implemented in this test")
	}
	return m.findByIDFn(id)
}

func (m *mockPostStore) ClaimQueuedTarget(id int64) (bool, error) {
	m.claimCalls++
	if m.claimFn == nil {
		return false, errors.New("ClaimQueuedTarget not implemented in this test")
	}
	return m.claimFn(id)
}

func (m *mockPostStore) UpdateStatus(target *models.PostTarget) error {
	m.updateCalls++
	// Snapshot the struct by value so later mutations to the
	// caller's target don't leak into the captured row. Pointers
	// inside the struct (e.g. *time.Time PublishedAt) still
	// alias, but the worker only writes PublishedAt once and the
	// test reads it at assertion time, so this is safe.
	m.updateTargets = append(m.updateTargets, *target)
	if m.updateStatusFn == nil {
		return nil
	}
	return m.updateStatusFn(target)
}

// SetProviderIdempotencyKey (Taglio 4.7 LEVEL 2) — captures the
// (id, key) tuple for assertion. Default behaviour is no-op so
// tests that don't exercise the stamp path continue to pass
// without configuring a setKeyFn.
func (m *mockPostStore) SetProviderIdempotencyKey(id int64, key string) error {
	m.setKeyCalls++
	m.setKeyIDs = append(m.setKeyIDs, id)
	m.setKeyVals = append(m.setKeyVals, key)
	if m.setKeyFn == nil {
		return nil
	}
	return m.setKeyFn(id, key)
}

// GetMetadata (Task 7/10) stub on mockPostStore — nil/no-op default.
func (m *mockPostStore) GetMetadata(postID int64) (json.RawMessage, error) {
	return nil, nil
}

// SetTargetCanaryVideoID (Task 7/10) stub on mockPostStore — nil default.
func (m *mockPostStore) SetTargetCanaryVideoID(targetID int64, videoID string) error {
	return nil
}
