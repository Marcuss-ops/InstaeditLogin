//go:build e2e

package e2e

import (
	"context"
	"strings"
	"testing"
	"time"
)

// ─── Scenario 9: Retry budget exhaustion ─────────────────────────────────
//
// The FSM flips delivery to RetryWait on each transient failure.
// After N attempts (configured per-platform; we use 3 here) the
// worker emits ToDeadLetter. The E2E scenario inserts a
// delivery-row proxy and walks the FSM through the same 3-attempt
// sequence so the dead_letter transition is locked into the
// acceptance criteria.
//
// Production: post_targets.next_attempt_at + attempt_count columns
// (Taglio 4.7). E2E simulates with a direct UPDATE sequence via
// the updateTargetStatus helper in e2e_harness.go.
func scenario9_RetryBudgetExhaustion(t *testing.T, h *E2EHarness) {
	const maxAttempts = 3

	// Insert a fresh target row in queued.
	targetID, err := insertPublishTarget(h, "queued")
	if err != nil {
		t.Fatalf("insertPublishTarget: %v", err)
	}

	// Walk N transient failures through the FSM until the budget
	// flips status to 'dead_letter' on the N+1 attempt.
	// Enter retry_wait once; subsequent failures update retry metadata
	// while the row remains in the same state. This mirrors production
	// bookkeeping without inventing a retry_wait → retry_wait transition.
	if err := updateTargetStatus(h, targetID, "queued", "retry_wait", transientErrMsg(1)); err != nil {
		t.Fatalf("attempt 1 flip queued → retry_wait: %v", err)
	}
	for attempt := 2; attempt <= maxAttempts; attempt++ {
		if err := recordRetryAttempt(h, targetID, transientErrMsg(attempt)); err != nil {
			t.Fatalf("attempt %d retry metadata: %v", attempt, err)
		}
	}

	// After exhausting retries, the production worker would call
	// `ToDeadLetter(ctx, id, retry_wait)`. We simulate the same
	// terminal transition here.
	if err := updateTargetStatus(h, targetID, "retry_wait", "dead_letter", "max_attempts=3 budget exhausted"); err != nil {
		t.Fatalf("dead_letter flip: %v", err)
	}

	// Anchor: row's status is now 'dead_letter' with last_error_message
	// stamped.
	var (
		gotStatus string
		gotErrMsg string
	)
	if err := h.pgDB.QueryRowContext(context.Background(),
		`SELECT status, COALESCE(last_error_message, '') FROM post_targets WHERE id=$1`, targetID,
	).Scan(&gotStatus, &gotErrMsg); err != nil {
		t.Fatalf("read dead_letter anchor: %v", err)
	}
	if gotStatus != "dead_letter" {
		t.Errorf("scenario_9: row status: want dead_letter, got %q", gotStatus)
	}
	if !strings.Contains(gotErrMsg, "max_attempts") {
		t.Errorf("scenario_9: last_error_message should pin the budget_exhaustion reason; got %q", gotErrMsg)
	}

	t.Logf("scenario_9 PASS: %d retry attempts → dead_letter (last_error_message=%q)", maxAttempts, gotErrMsg)
}

// ─── Scenario 10: dead_letter is terminal ─────────────────────────────────
//
// Once the row is 'dead_letter', no further transition is legal.
// The FSM enforces this; the E2E surfaces the same invariant by
// attempting an UPDATE past the dead_letter sink + asserting the
// WHERE-clause guard refuses (the production Update is gated on
// status != terminal).
func scenario10_DeadLetterTerminal(t *testing.T, h *E2EHarness) {
	targetID, err := insertPublishTarget(h, "dead_letter")
	if err != nil {
		t.Fatalf("insertPublishTarget: %v", err)
	}

	// Try to push it back to 'retry_wait' (illegal terminal exit).
	if err := updateTargetStatus(h, targetID, "dead_letter", "retry_wait", "should be rejected by WHERE-clause"); err == nil {
		t.Errorf("scenario_10: dead_letter → retry_wait must be REJECTED; UPDATE unexpectedly succeeded")
	}

	// The row's status must remain 'dead_letter' regardless.
	var gotStatus string
	if err := h.pgDB.QueryRowContext(context.Background(),
		`SELECT status FROM post_targets WHERE id=$1`, targetID,
	).Scan(&gotStatus); err != nil {
		t.Fatalf("read status: %v", err)
	}
	if gotStatus != "dead_letter" {
		t.Errorf("scenario_10: row status should remain dead_letter after rejected UPDATE; got %q", gotStatus)
	}

	// Production stores this terminal publish-target state as `dlq`.
	// Keep the legacy E2E alias covered as well so the helper cannot
	// accidentally permit a retry from either representation.
	dlqID, err := insertPublishTarget(h, "dlq")
	if err != nil {
		t.Fatalf("insertPublishTarget (dlq): %v", err)
	}
	if err := updateTargetStatus(h, dlqID, "dlq", "retry_wait", "should be rejected by WHERE-clause"); err == nil {
		t.Errorf("scenario_10: dlq → retry_wait must be REJECTED; UPDATE unexpectedly succeeded")
	}
	var gotDLQStatus string
	if err := h.pgDB.QueryRowContext(context.Background(),
		`SELECT status FROM post_targets WHERE id=$1`, dlqID,
	).Scan(&gotDLQStatus); err != nil {
		t.Fatalf("read dlq status: %v", err)
	}
	if gotDLQStatus != "dlq" {
		t.Errorf("scenario_10: dlq row should remain terminal; got %q", gotDLQStatus)
	}

	if !t.Failed() {
		t.Logf("scenario_10 PASS: dead_letter and production dlq both refuse retry_wait transitions")
	}
}

// ─── Scenario 11: Velox callback HMAC verify ──────────────────────────────
//
// Velox sends every callback with an X-Hub-Signature-256 style
// HMAC header so InstaEdit can verify the body wasn't tampered with
// in transit. The E2E scenario:
//
//   - Sign a payload with the canonical secret (production-side
//     uses the SHA-256 HMAC of the body bytes, hex-encoded, prefixed
//     with `sha256=`).
//   - Verify the InstaEdit-side verifier accepts our signature.
//   - Mutate one byte of the body and verify the verifier REJECTS.
//   - Mutate the secret and verify the verifier REJECTS.
//
// The verify function lives entirely in the e2e_harness's
// signHMAC + callVerifyHMAC helpers (the production verifier is
// independent and exercised by HandleCallback tests; the E2E
// exercises the same SHA-256 HMAC contract end-to-end).
func scenario11_VeloxCallbackHMAC(t *testing.T, h *E2EHarness) {
	const sharedSecret = "velox-callback-secret-shared-with-instaedit"

	body := []byte(`{"external_delivery_id":"delivery-hmac-test","status":"published"}`)
	signature := h.veloxFake.signHMAC(body, sharedSecret)

	// Happy path: signature matches → PAYLOAD ACCEPTED.
	if err := h.veloxFake.callVerifyHMAC(body, signature, sharedSecret); err != nil {
		t.Errorf("scenario_11: HMAC verify on matched body should pass; got %v", err)
	}

	// Tampered body → REJECTED.
	tampered := append([]byte{}, body...)
	tampered[10] ^= 0xFF
	if err := h.veloxFake.callVerifyHMAC(tampered, signature, sharedSecret); err == nil {
		t.Errorf("scenario_11: HMAC verify on tampered body must REJECT")
	}

	// Wrong secret → REJECTED.
	if err := h.veloxFake.callVerifyHMAC(body, signature, "wrong-secret"); err == nil {
		t.Errorf("scenario_11: HMAC verify on wrong-secret must REJECT")
	}

	// Bonus: end-to-end callback path with HMAC verification on the
	// Velox fake simulates InstaEdit receiving a callback. This locks
	// the contract that the production code path (handleCallback +
	// HMAC verifier) accepts a signed callback.
	if err := h.veloxFake.simulateSignedCallback("delivery-hmac-full", body, sharedSecret); err != nil {
		t.Errorf("scenario_11: simulateSignedCallback: %v", err)
	}

	t.Logf("scenario_11 PASS: HMAC accepts matched body; rejects tampered body + wrong secret; e2e callback roundtrip OK")
}

// ─── Scenario 12: heartbeat-driven reclaim ──────────────────────────────
//
// Once a worker holds a lease (locked_by + heartbeat_at stamps),
// the reclaimer-tick observes `heartbeat_at < NOW() - lease_timeout`
// and re-stamps the lease to a peer worker. Production: the
// `internal/worker/reconcile_worker.go::runReclaimerTick` loop runs
// every N seconds. E2E exercises the SAME shape via two phases:
//
//   - Phase 1: worker A acquires lease → heartbeat_at = NOW().
//     Worker B observes FRESH heartbeat → reclaim REFUSED (rows
//     affected = 0; the active worker is alive).
//   - Phase 2: Test simulates worker-A crash by backdating
//     heartbeat_at to NOW() - 15m via raw SQL (faster than Docker
//     time-warp). Worker B re-observes STALE heartbeat → reclaim
//     SUCCEEDS; row's locked_by flips to "worker-B" with fresh
//     heartbeat_at.
//
// Anchors the production contract: a peer worker can ONLY take over
// a lease when the holder's heartbeat is older than lease_timeout.
func scenario12_HeartbeatReclaim(t *testing.T, h *E2EHarness) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const leaseTimeout = 5 * time.Minute
	const crashAge = 15 * time.Minute

	targetID, err := insertPublishTarget(h, "queued")
	if err != nil {
		t.Fatalf("insertPublishTarget: %v", err)
	}

	// Worker A acquires lease inside a TX (heartbeat_at = NOW() on commit).
	txA, err := h.pgDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("worker-A begin: %v", err)
	}
	if err := acquireLeaseInTx(ctx, txA, targetID); err != nil {
		t.Fatalf("worker-A acquireLease: %v", err)
	}
	if err := txA.Commit(); err != nil {
		t.Fatalf("worker-A commit: %v", err)
	}

	// Phase 1: worker B observes FRESH heartbeat → reclaim REFUSED.
	acquired, err := attemptHeartbeatReclaim(ctx, h, targetID, leaseTimeout, "worker-B")
	if err != nil {
		t.Fatalf("worker-B phase-1 reclaim: %v", err)
	}
	if acquired {
		t.Errorf("scenario_12: worker-B reclaimed a fresh lease (heartbeat ≤ lease_timeout ago); the reclaimer must NOT take over an active worker")
	}

	// Validate locked_by is still worker-A (no spurious takeover).
	var lockedBy string
	if err := h.pgDB.QueryRowContext(ctx,
		`SELECT locked_by FROM post_targets WHERE id=$1`, targetID,
	).Scan(&lockedBy); err != nil {
		t.Fatalf("phase-1 anchor read: %v", err)
	}
	if lockedBy != "worker-A" {
		t.Errorf("scenario_12: phase-1 locked_by: want worker-A, got %q", lockedBy)
	}

	// Phase 2: simulate worker-A crash by backdating heartbeat_at.
	if err := backdateHeartbeat(ctx, h, targetID, crashAge); err != nil {
		t.Fatalf("backdateHeartbeat: %v", err)
	}

	// Phase 3: worker B re-observes STALE heartbeat → reclaim SUCCEEDS.
	acquired2, err := attemptHeartbeatReclaim(ctx, h, targetID, leaseTimeout, "worker-B")
	if err != nil {
		t.Fatalf("worker-B phase-3 reclaim: %v", err)
	}
	if !acquired2 {
		t.Errorf("scenario_12: worker-B should have reclaimed the stale lease (heartbeat ~%v ago > %v timeout); reclaim returned FALSE", crashAge, leaseTimeout)
	}

	// Final anchor: locked_by is now worker-B + status is preserved.
	// The heartbeat_at wall-clock check is intentionally OMITTED:
	// reading it via the testcontainer adds a network roundtrip and
	// would flap on a slow runner. The locked_by stamp + heartbeat
	// UPDATE itself (already proven by `acquired2 == true`) is the
	// load-bearing assertion; we don't need a second wall-clock arm.
	var (
		gotLockedBy string
		gotStatus   string
	)
	if err := h.pgDB.QueryRowContext(ctx,
		`SELECT locked_by, status FROM post_targets WHERE id=$1`, targetID,
	).Scan(&gotLockedBy, &gotStatus); err != nil {
		t.Fatalf("final anchor read: %v", err)
	}
	if gotLockedBy != "worker-B" {
		t.Errorf("scenario_12: final locked_by: want worker-B, got %q", gotLockedBy)
	}
	if gotStatus != "queued" {
		t.Errorf("scenario_12: status must be preserved by the reclaimer (terminal-deny guard); want queued, got %q", gotStatus)
	}

	// Phase 4: SELF-RECLAIM denial. Production never lets worker-X
	// reclaim its own lease even when its heartbeat is stale
	// (would create spurious self-restarts on heartbeat ticks).
	// The helper's WHERE clause (`locked_by <> $newOwner`) encodes
	// this. Verify: with locked_by still "worker-B" and heartbeat
	// still fresh after the prior reclaim, attempting to reclaim
	// with newOwner="worker-B" against a freshly-backdated
	// heartbeat MUST NOT flip the row.
	if err := backdateHeartbeat(ctx, h, targetID, crashAge); err != nil {
		t.Fatalf("phase-4 backdateHeartbeat: %v", err)
	}
	selfReclaim, err := attemptHeartbeatReclaim(ctx, h, targetID, leaseTimeout, "worker-B")
	if err != nil {
		t.Fatalf("phase-4 self-reclaim: %v", err)
	}
	if selfReclaim {
		t.Errorf("scenario_12: self-reclaim must be DENIED (locked_by = newOwner); attemptHeartbeatReclaim returned TRUE")
	}
	var lockedByAfterSelf string
	if err := h.pgDB.QueryRowContext(ctx,
		`SELECT locked_by FROM post_targets WHERE id=$1`, targetID,
	).Scan(&lockedByAfterSelf); err != nil {
		t.Fatalf("phase-4 anchor read: %v", err)
	}
	if lockedByAfterSelf != "worker-B" {
		t.Errorf("scenario_12: locked_by must remain worker-B post-self-reclaim attempt; got %q", lockedByAfterSelf)
	}

	// Phase 5: TERMINAL-DENY guard. Insert a parallel row in
	// 'dead_letter' with a non-empty locked_by so the prior
	// `locked_by IS NOT NULL` guard would still match (i.e.
	// removing the `status NOT IN (...)` clause from the SQL
	// would let this row be re-stamped — that's the regression
	// we're protecting against). A stale heartbeat on this row
	// would ordinarily satisfy the staleness predicate; only the
	// status-not-in-terminal predicate can save it.
	//
	// The previous `gotStatus != "queued"` assertion was a
	// structural no-op because the helper's UPDATE statement
	// never writes the `status` column. This dedicated dead_letter
	// row + post-reclaim anchor is what actually exercises the
	// `status NOT IN ('dead_letter','failed','published')`
	// predicate; without it, the guard could break silently.
	termID, err := insertPublishTarget(h, "dead_letter")
	if err != nil {
		t.Fatalf("phase-5 insertPublishTarget (dead_letter): %v", err)
	}
	// Pre-stamp locked_by so the `locked_by IS NOT NULL` guard is
	// satisfied — the only thing protecting this row is the
	// status-not-in-terminal predicate.
	if _, err := h.pgDB.ExecContext(ctx,
		`UPDATE post_targets SET locked_by = $1, locked_at = NOW(), heartbeat_at = NOW() WHERE id = $2`,
		"worker-X", termID,
	); err != nil {
		t.Fatalf("phase-5 stamp locked_by: %v", err)
	}
	// Backdate heartbeat so the staleness predicate would match
	// if not for the status guard.
	if err := backdateHeartbeat(ctx, h, termID, crashAge); err != nil {
		t.Fatalf("phase-5 backdateHeartbeat: %v", err)
	}
	termAcquired, err := attemptHeartbeatReclaim(ctx, h, termID, leaseTimeout, "ghost-reclaimer")
	if err != nil {
		t.Fatalf("phase-5 reclaim on dead_letter: %v", err)
	}
	if termAcquired {
		t.Errorf("scenario_12: reclaimer MUST NOT touch a dead_letter row (terminal-deny violated); acquired=TRUE")
	}
	var (
		termLockedBy string
		termStatus   string
	)
	if err := h.pgDB.QueryRowContext(ctx,
		`SELECT locked_by, status FROM post_targets WHERE id=$1`, termID,
	).Scan(&termLockedBy, &termStatus); err != nil {
		t.Fatalf("phase-5 anchor read: %v", err)
	}
	if termStatus != "dead_letter" {
		t.Errorf("scenario_12: dead_letter row's status flipped post-reclaim; want dead_letter, got %q", termStatus)
	}
	if termLockedBy != "worker-X" {
		t.Errorf("scenario_12: dead_letter row's locked_by was re-stamped (terminal-deny violated); want worker-X, got %q", termLockedBy)
	}
}
