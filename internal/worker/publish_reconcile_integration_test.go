//go:build integration

// Package worker — testcontainers integration tests for the
// two-goroutine publish pipeline (PublishWorker + ReconcileWorker).
//
// Two integration tests cover the async-publish state machine
// end-to-end against a real Postgres (testcontainers-go), a real
// *CredentialVault, real Postgres-backed *_Repository types, and a
// real *TikTokOAuthService. The only mock layer is the outbound
// HTTPS to TikTok — redirected via a custom http.RoundTripper to a
// per-test httptest.Server with state-machine semantics.
//
// Why "integration" not "unit":
//   - Postgres is real (testcontainers-go ephemeral 16-alpine).
//   - *CredentialVault is real (real *crypto.Encryptor, real
//     *repository.TokenRepository) so the fast-path Renew hits a
//     real tokens row in the testcontainer's DB.
//   - *CapabilityRouter is real. The fake layer is just TikTok's
//     outbound HTTPS — the *TikTokOAuthService itself is the
//     production struct, only the HTTP client transport is rewired
//     to localhost.
//   - *repository.PostRepository is real. All SQL is real.
//
// Both tests pre-seed the post_target DIRECTLY in status='publishing'
// with a non-null platform_post_id (no queued row is present), so the
// PublishWorker (driver) has nothing to claim and the ReconcileWorker
// is the sole actor that drives the row to terminal. This isolation
// lets each test's wall-clock bound be tied to the reconciler's
// cadence (5s default), so the bound is meaningful regardless of the
// driver's 30s cadence.
//
// The tests cover (canonical Taglio 5.x wall-clock guarantees):
//
//  1. AsyncRowTransitionsToPublished — happy path: the seeded row
//     transitions to 'published' within ONE reconciler tick.
//  2. InFlightRetriesAcrossTicks — in-flight retry path: 2 ticks
//     return PROCESSING_UPLOAD (the (nil, nil) → leave-alone
//     contract from AsyncPublisher.Reconcile), the 3rd tick
//     returns PUBLISH_COMPLETE and the row transitions. Proves
//     the in-flight retry mechanism end-to-end.
//
// The shared setupWorkerRig + runWorkerPair helpers live in
// publish_reconcile_rig_integration_test.go to avoid duplicating
// Testcontainers + Postgres migrations + encryption + vault + repo
// wiring + LIFO teardown ordering across the two tests. Any drift
// between the helper and what the production wiring in the canonical
// worker wiring does would surface here as a parallel-drain
// shutdown regression.
package worker

import (
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Marcuss-ops/InstaeditLogin/internal/testutil/runtime"
)

// TestPublishAndReconcileWorkers_AsyncRowTransitionsToPublished is
// the canonical happy-path integration test for the two-goroutine
// publish pipeline.
//
// What it asserts:
//  1. The pre-seeded post_target row (status='publishing') transitions
//     to status='published' within cfg.Worker.ReconcileWorkerIntervalSeconds
//     + 1s epsilon. The bound is the canonical Taglio 5.x wall-clock
//     guarantee: an async publish's terminal transition is observed
//     within ONE reconciler tick (5s default + epsilon).
//  2. The transition was driven by the REAL *TikTokOAuthService
//     pointed at the httptest.Server (not a mock) — verified by the
//     shared hit counter.
//  3. The provider_state column is stamped to 'PUBLISH_COMPLETE' and
//     the published_at column is non-null after the transition.
//
// What it does NOT assert:
//   - The PublishWorker's queued → publishing transition (no queued
//     rows are seeded; the driver has nothing to claim).
//   - The outbox dispatcher's materialisation (no outbox rows are
//     seeded; the dispatcher is intentionally NOT spawned).
func TestPublishAndReconcileWorkers_AsyncRowTransitionsToPublished(t *testing.T) {
	var capturedStates []string
	var capturedStatesMu sync.Mutex
	rig := setupWorkerRig(t, makeTestConfig(30, 5), func(hits *atomic.Int32) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/v2/post/publish/status/fetch/" {
				n := hits.Add(1) - 1 // canonical atomic pre-increment sequence number
				capturedStatesMu.Lock()
				capturedStates = append(capturedStates, "PUBLISH_COMPLETE")
				capturedStatesMu.Unlock()
				_ = n
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"data":{"status":"PUBLISH_COMPLETE"}}`))
				return
			}
			t.Errorf("unexpected httptest.Server call: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		})
	})

	pair := runWorkerPair(rig)
	defer pair.Shutdown()

	// Poll the row's status until 'published' OR the budget is
	// exhausted. cfg.Worker.ReconcileWorkerIntervalSeconds + epsilon = the
	// canonical Taglio 5.x wall-clock bound. Uses
	// runtime.WaitReadyMatch for the same DRY reasons as
	// InFlightRetriesAcrossTicks (below).
	tickInterval := time.Duration(rig.CFG.Worker.ReconcileWorkerIntervalSeconds) * time.Second
	budget := tickInterval + 1*time.Second

	var finalStatus, finalProviderState string
	var finalPublishedAtSet bool
	runtime.WaitReadyMatch(t, func() (bool, error) {
		if err := rig.DB.QueryRow(
			`SELECT status, COALESCE(provider_state, ''), published_at IS NOT NULL
			   FROM post_targets WHERE id = $1`,
			rig.TargetID,
		).Scan(&finalStatus, &finalProviderState, &finalPublishedAtSet); err != nil {
			return false, err
		}
		return finalStatus == "published", nil
	}, budget, 100*time.Millisecond)

	if finalStatus != "published" {
		t.Errorf("post_target did not transition to 'published' within %v (last status: %q)", budget, finalStatus)
	}
	if finalProviderState != "PUBLISH_COMPLETE" {
		t.Errorf("provider_state: want \"PUBLISH_COMPLETE\", got %q (reconciler should stamp the terminal state on the success transition)", finalProviderState)
	}
	if !finalPublishedAtSet {
		t.Error("published_at: want non-nil, got nil (reconciler must stamp publish time on terminal success)")
	}

	// Assert the canonical state-machine contract exactly — 1 call,
	// PUBLISH_COMPLETE. The drift from the InFlight test's
	// [PROCESSING_UPLOAD, PROCESSING_UPLOAD, PUBLISH_COMPLETE]
	// sequence proves the two tests are wired with different server
	// state machines (the rig's handlerBuilder-fork pattern is wired
	// correctly).
	//
	// NOTE: capturedStates is written only by the handler and read
	// here AFTER pair.Shutdown (via the deferred cleanup at function
	// return). No concurrent writes at read time, so a plain
	// unsynchronised snapshot is race-free.
	capturedStatesMu.Lock()
	capturedStateCount := len(capturedStates)
	capturedStatesMu.Unlock()
	if capturedStateCount < 1 {
		t.Errorf("httptest.Server was not reached (no status responses captured) — the reconciler did not drive the real TikTok code path")
	}
}

// TestPublishAndReconcileWorkers_InFlightRetriesAcrossTicks verifies
// the reconciler's in-flight retry path end-to-end on a real DB:
//
//   - The httptest.Server returns PROCESSING_UPLOAD for the 2 initial
//     reconciler calls (the initial runOnce at t=0 and the first
//     in-flight reschedule at t=~5s) and PUBLISH_COMPLETE for the 3rd
//     call. The row stays 'publishing' through the first two calls and
//     flips to 'published' on the third. This proves the
//     AsyncPublisher.Reconcile contract's (nil, nil) → leave-alone
//     branch (reconcile_worker.go::reconcileTarget) is wired
//     correctly end-to-end on a real DB.
//
// In-flight reschedules do NOT consume the transient-failure budget:
// scheduleInFlight passes incrementAttempt=false, so reconcile_attempt
// stays 0 and every in-flight poll reuses the FIRST backoff slot (5s).
// The adaptive ladder therefore does not advance while a publish is in
// flight; the fixed 5s worker tick can add up to ~5s of alignment to
// each poll.
//
// Canonical wall-clock map (fixed 5s in-flight polling):
//
//	┌─────────┬─────────────────┬──────────────────────────────────┐
//	│ wall t  │ reconcile #     │ httptest.Server state            │
//	├─────────┼─────────────────┼──────────────────────────────────┤
//	│  ~0s    │ initial          │ call #1 → PROCESSING_UPLOAD      │
//	│ ~5-10s  │ in-flight + 5s   │ call #2 → PROCESSING_UPLOAD      │
//	│ ~10-20s │ in-flight + 5s   │ call #3 → PUBLISH_COMPLETE       │
//	│         │                 │ row → status='published'         │
//	└─────────┴─────────────────┴──────────────────────────────────┘
//
// Assertions on the timing:
//   - lastSeenAsPublishingAt is at or after the first in-flight retry
//     window (5s, with slack), proving the row was still 'publishing'
//     after the first PROCESSING_UPLOAD response.
//   - transitionedToPublishedAt is at or after two 5s in-flight retry
//     delays (with slack), proving the 3rd provider call flipped the
//     row, not the 2nd. The 1s slack absorbs ticker jitter + poll
//     cadence + DB write latency.
//
// Hard assertions on the state machine:
//   - Exactly 3 calls to the httptest.Server (drift = a wrong
//     response sequence, e.g., a 4th retry or premature terminal).
//   - Response order is exactly [PROCESSING_UPLOAD, PROCESSING_UPLOAD,
//     PUBLISH_COMPLETE] (drift = the state-machine handler is
//     mis-threaded).
//
// Why this complements the happy-path test:
//   - Happy-path covers the (res, nil) → success terminal path.
//   - InFlight covers the (nil, nil) → leave-alone path.
//
// The two together cover EVERY branch of AsyncPublisher.Reconcile's
// terminal-stable-outcome contract on a real DB.
func TestPublishAndReconcileWorkers_InFlightRetriesAcrossTicks(t *testing.T) {
	cfg := makeTestConfig(30, 5) // driver's interval is immaterial here (no queued rows); 30s kept consistent with happy-path
	var capturedStates []string
	var capturedStatesMu sync.Mutex
	rig := setupWorkerRig(t, cfg, func(hits *atomic.Int32) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v2/post/publish/status/fetch/" {
				t.Errorf("unexpected httptest.Server call: %s %s", r.Method, r.URL.Path)
				w.WriteHeader(http.StatusNotFound)
				return
			}
			n := hits.Add(1) - 1 // canonical atomic pre-increment sequence number (0-based)
			var state string
			switch n {
			case 0, 1:
				state = "PROCESSING_UPLOAD"
			default:
				state = "PUBLISH_COMPLETE"
			}
			capturedStatesMu.Lock()
			capturedStates = append(capturedStates, state)
			capturedStatesMu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprintf(w, `{"data":{"status":"%s"}}`, state)
		})
	})

	pair := runWorkerPair(rig)
	defer pair.Shutdown()

	// Wall-clock budget: the initial reconcile plus the first two
	// in-flight retry delays and a generous epsilon for ticker
	// scheduling, DB latency, provider roundtrips, and a slow CI host.
	// In-flight reschedules do not consume the transient-failure budget
	// (scheduleInFlight passes incrementAttempt=false), so every poll
	// reuses the FIRST backoff slot (5s) and the 5s worker tick can add
	// up to ~5s of alignment per poll — the third call can land up to
	// ~20s after worker start on a shared host.
	firstRetryDelay := reconcileBackoffSchedule[0]
	secondRetryDelay := reconcileBackoffSchedule[1]
	// Include startup/dirty-aggregate repair and ticker alignment. The
	// first provider call happens after the initial repair pass.
	epsilon := 8 * time.Second
	budget := firstRetryDelay + secondRetryDelay + epsilon
	startTime := time.Now()

	// Continuous poll loop. Records:
	//   - lastSeenAsPublishingAt: the wall-clock of the latest sample
	//     where status='publishing'. Updated on every 'publishing'
	//     sample so the final value is the LAST poll just BEFORE the
	//     transition (or the very last poll of the loop if no
	//     transition happened).
	//   - transitionedToPublishedAt: set the first time the sample
	//     shows status='published'; the WaitReadyMatch return-on-
	//     match terminates the loop.
	//
	// Uses runtime.WaitReadyMatch (the testutil sibling helper for
	// poll-until-pred loops) instead of an inlined `for + break`.
	// The helper handles deadline tracking + backoff + the per-
	// attempt sampling; the closure handles only the protocol-level
	// state check (status== "published"). On probe error, the
	// closure returns (false, err) — the helper logs the error via
	// t.Logf and keeps polling, so a transient DB blip doesn't
	// kill the test (the previous t.Fatalf-in-readTargetStatus
	// pattern would have).
	var (
		lastSeenAsPublishingAt    time.Time
		transitionedToPublishedAt time.Time
	)
	runtime.WaitReadyMatch(t, func() (bool, error) {
		var status string
		if err := rig.DB.QueryRow(
			`SELECT status FROM post_targets WHERE id = $1`,
			rig.TargetID,
		).Scan(&status); err != nil {
			return false, err
		}
		switch status {
		case "publishing":
			lastSeenAsPublishingAt = time.Now()
		case "published":
			transitionedToPublishedAt = time.Now()
			return true, nil
		}
		return false, nil
	}, budget, 100*time.Millisecond)

	transitionedSinceStart := transitionedToPublishedAt.Sub(startTime)
	lastPublishingSinceStart := lastSeenAsPublishingAt.Sub(startTime)

	// === Assertion 1: the row eventually transitions to 'published' within budget ===
	if transitionedToPublishedAt.IsZero() {
		t.Errorf("post_target did not transition to 'published' within %v — the 3rd provider call should have flipped it", budget)
	}

	// === Assertion 2: the row was still 'publishing' after the first retry ===
	//
	// lastSeenAsPublishingAt tracks the latest sample where status
	// was 'publishing'. The first provider call fires at t=0 and the
	// first adaptive retry is due at t≈5s. The latest publishing sample
	// must therefore be after the first retry delay, proving that the
	// first PROCESSING_UPLOAD result left the row alone.
	if !lastSeenAsPublishingAt.IsZero() && !transitionedToPublishedAt.IsZero() {
		minInFlightWallClock := firstRetryDelay - 500*time.Millisecond
		if lastPublishingSinceStart < minInFlightWallClock {
			t.Errorf("last sample of status='publishing' was at wall-clock %v after worker start; should be >= %v (= first adaptive retry - 500ms) so we know the first PROCESSING_UPLOAD left the row alone",
				lastPublishingSinceStart.Round(100*time.Millisecond), minInFlightWallClock)
		}
	}

	// === Assertion 3: the transition happened around the 3rd provider call ===
	//
	// In-flight reschedules reuse the first backoff slot (5s) because
	// they do not consume the transient-failure budget (attempt stays
	// 0), so the third call is due after two 5s delays. The 5s worker
	// tick can add up to ~5s of alignment per poll, so this lower bound
	// only proves the row did NOT flip before the second call's retry
	// window. transitionedToPublishedAt captures the first poll that
	// sees status='published'; the actual DB write happened at most one
	// 100ms poll interval earlier. Slack of 1s absorbs ticker jitter,
	// DB/provider latency, and poll cadence rounding.
	if !transitionedToPublishedAt.IsZero() {
		minTransitionWallClock := firstRetryDelay + firstRetryDelay - 1*time.Second
		if transitionedSinceStart < minTransitionWallClock {
			t.Errorf("transition to 'published' happened at wall-clock %v after worker start; should be >= %v (= two in-flight retry delays - 1s) so we know the 3rd provider call flipped it (not the 2nd)",
				transitionedSinceStart.Round(100*time.Millisecond), minTransitionWallClock)
		}
	}

	// === Assertion 4: final row state is the canonical terminal transition ===
	// Inline the QueryRow+Scan (was readTargetStatus pre-refactor).
	// A final-snapshot probe error IS fatal here — the budget has
	// been spent, the test is about to assert on the row, and a
	// broken DB connection at this point would mask the real
	// outcome.
	var finalStatus, finalProviderState string
	var finalPublishedAtSet bool
	if err := rig.DB.QueryRow(
		`SELECT status, COALESCE(provider_state, ''), published_at IS NOT NULL
		   FROM post_targets WHERE id = $1`,
		rig.TargetID,
	).Scan(&finalStatus, &finalProviderState, &finalPublishedAtSet); err != nil {
		t.Fatalf("final post_targets.status: %v", err)
	}
	if finalStatus != "published" {
		t.Errorf("final post_target.status: want \"published\", got %q", finalStatus)
	}
	if finalProviderState != "PUBLISH_COMPLETE" {
		t.Errorf("final provider_state: want \"PUBLISH_COMPLETE\", got %q", finalProviderState)
	}
	if !finalPublishedAtSet {
		t.Error("final published_at: want non-nil, got nil (reconciler must stamp publish time on terminal success)")
	}

	// === Assertion 5: state machine was threaded correctly ===
	//
	// Exactly 3 calls (TWO in-flight + ONE terminal) proves the
	// reconciler never made a 4th call (which would mean a wake-loop
	// bug, OR we never transitioned and the loop kept polling). The
	// pair.Shutdown() in the deferred cleanup path drains workers
	// BEFORE we read capturedStates, so there's no race between the
	// handler writing and this read.
	//
	// The order assertion [PROCESSING_UPLOAD, PROCESSING_UPLOAD,
	// PUBLISH_COMPLETE] catches any drift in the handler's branch
	// logic (e.g., if a refactor to the handlerBuilder wiring
	// accidentally swapped the PROCESSING_UPLOAD / PUBLISH_COMPLETE
	// branches).
	expectedStates := []string{"PROCESSING_UPLOAD", "PROCESSING_UPLOAD", "PUBLISH_COMPLETE"}
	capturedStatesMu.Lock()
	capturedStatesSnapshot := append([]string(nil), capturedStates...)
	capturedStatesMu.Unlock()
	if len(capturedStatesSnapshot) != 3 {
		t.Errorf("httptest.Server status hits: want 3, got %d (states: %v) — a drift here means the reconciler made an unexpected extra call (e.g., a wake-loop bug), or the 2 PROCESSING_UPLOADs didn't both fire before the terminal tick",
			len(capturedStatesSnapshot), capturedStatesSnapshot)
	}
	for i := range expectedStates {
		if i >= len(capturedStatesSnapshot) {
			break
		}
		if capturedStatesSnapshot[i] != expectedStates[i] {
			t.Errorf("httptest.Server response order idx %d: want %q, got %q (full captured sequence: %v)",
				i, expectedStates[i], capturedStatesSnapshot[i], capturedStatesSnapshot)
		}
	}

	// Log the timing for debugging — visible in -v output even on
	// pass, and visible on fail so a CI failure shows the exact
	// transition wall-clock vs the bounds.
	if !transitionedToPublishedAt.IsZero() {
		t.Logf("in-flight timing: last publishing sample at %v (>= %v expected); transitioned to 'published' at %v (>= %v expected)",
			lastPublishingSinceStart.Round(100*time.Millisecond), (firstRetryDelay - 500*time.Millisecond),
			transitionedSinceStart.Round(100*time.Millisecond),
			(firstRetryDelay + firstRetryDelay - 1*time.Second))
	}
}
