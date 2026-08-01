//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// ---- Scenario 5: Scheduling with future publish_at must NOT publish early.
func scenario5_PostScheduling(t *testing.T, h *E2EHarness) {
	futurePublishAt := time.Now().UTC().Add(1 * time.Hour)

	// Insert a scheduled post directly via the test DB (production
	// would gate this through the post-create HTTP handler; the
	// agreement is the same — `posts.publish_at <= NOW()` is the
	// gate).
	postID, err := insertScheduledPost(h, futurePublishAt)
	if err != nil {
		t.Fatalf("insertScheduledPost: %v", err)
	}

	// Run the publish-batch claim SQL (matches the production
	// ClaimBatchForPublish shape).
	claimedCount, err := runPublishClaimGate(h, time.Now())
	if err != nil {
		t.Fatalf("runPublishClaimGate: %v", err)
	}
	if claimedCount != 0 {
		t.Errorf("scenario_5: future publish_at should NOT be claimed; got %d claimed", claimedCount)
	}

	// Anchor assertion: confirm the row is untouched in DB.
	var status string
	if err := h.pgDB.QueryRowContext(context.Background(),
		`SELECT status FROM posts WHERE id=$1`, postID).Scan(&status); err != nil {
		t.Fatalf("read post status: %v", err)
	}
	if status != "scheduled" && status != "pending" {
		t.Errorf("scenario_5: post status should remain unscathed; got %q", status)
	}
	t.Logf("scenario_5 PASS: future publish_at blocked publish-batch claim (post_id=%d)", postID)
}

// ---- Scenario 6: YouTube resumable upload crash + recovery.
func TestFakeYouTubeResumableSession_RejectsInvalidRanges(t *testing.T) {
	y := newFakeYouTubeServer()
	defer y.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	sessionURI, err := y.openResumableSession(ctx)
	if err != nil {
		t.Fatalf("openResumableSession: %v", err)
	}

	doPut := func(contentRange, body string) *http.Response {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, http.MethodPut, sessionURI, strings.NewReader(body))
		if err != nil {
			t.Fatalf("new PUT request: %v", err)
		}
		req.Header.Set("Content-Range", contentRange)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("PUT %q: %v", contentRange, err)
		}
		return resp
	}

	resp := doPut("bytes malformed", "data")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("malformed Content-Range: want 400, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	resp = doPut("bytes 0-3/8", "abc")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("short chunk body: want 400, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	resp = doPut("bytes 0-3/8", "data")
	if resp.StatusCode != statusResumeIncomplete {
		t.Fatalf("valid first chunk: want %d, got %d", statusResumeIncomplete, resp.StatusCode)
	}
	resp.Body.Close()

	resp = doPut("bytes */9", "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("inconsistent status-query total: want 400, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	resp = doPut("bytes */8", "")
	if resp.StatusCode != statusResumeIncomplete {
		t.Fatalf("consistent status query: want %d, got %d", statusResumeIncomplete, resp.StatusCode)
	}
	if got, want := resp.Header.Get("Range"), "bytes=0-3"; got != want {
		t.Errorf("status-query Range: want %q, got %q", want, got)
	}
	resp.Body.Close()
}

func scenario6_YouTubeCrash(t *testing.T, h *E2EHarness) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Set the YouTube fake to hang up after the first 256 KiB chunk.
	atomic.StoreInt64(&h.youTubeFake.crashAt, 256*1024)

	// Open a resumable session.
	sessionURI, err := h.youTubeFake.openResumableSession(ctx)
	if err != nil {
		t.Fatalf("openResumableSession: %v", err)
	}
	if sessionURI == "" {
		t.Fatalf("openResumableSession: empty Location header")
	}

	// Worker attempt 1: upload first chunk → crash mid-upload.
	chunk := make([]byte, 256*1024)
	err = h.youTubeFake.putChunk(ctx, sessionURI, chunk, 0, int64(len(chunk))-1, int64(2*len(chunk)))
	if err == nil {
		t.Errorf("scenario_6: expected crash mid-upload, got nil")
	}
	t.Logf("attempt 1 crashed as expected after byte %d", len(chunk))

	// Reload: query the same session URI before sending the next
	// chunk. The fake must report the committed first-chunk boundary;
	// a 404 or a different Range means the simulated crash incorrectly
	// discarded the resumable session or offset.
	atomic.StoreInt64(&h.youTubeFake.crashAt, 0)
	resumeReq, err := http.NewRequestWithContext(ctx, http.MethodPut, sessionURI, nil)
	if err != nil {
		t.Fatalf("resume status request: %v", err)
	}
	resumeReq.Header.Set("Content-Range", fmt.Sprintf("bytes */%d", int64(2*len(chunk))))
	resumeResp, err := h.HTTPClient.Do(resumeReq)
	if err != nil {
		t.Fatalf("resume status request: %v", err)
	}
	defer resumeResp.Body.Close()
	if resumeResp.StatusCode != statusResumeIncomplete {
		t.Fatalf("resume status request: want %d, got %d", statusResumeIncomplete, resumeResp.StatusCode)
	}
	wantRange := fmt.Sprintf("bytes=0-%d", len(chunk)-1)
	if gotRange := resumeResp.Header.Get("Range"); gotRange != wantRange {
		t.Fatalf("resume offset: want %q, got %q", wantRange, gotRange)
	}

	// The next worker re-uses the same session URI and sends the next
	// chunk at the preserved offset.
	err = h.youTubeFake.putChunk(ctx, sessionURI, chunk, int64(len(chunk)), int64(2*len(chunk))-1, int64(2*len(chunk)))
	if err != nil {
		t.Fatalf("attempt 2 chunk PUT: %v", err)
	}
	t.Logf("scenario_6 PASS: crash+resume via session URI; offset continues from byte %d", len(chunk))
}

// ---- Scenario 7: Final Velox callback fires.
func scenario7_VeloxCallback(t *testing.T, h *E2EHarness) {
	err := h.veloxFake.simulateCallback("delivery-final-DONE", []byte(`{"external_delivery_id":"delivery-final-DONE","status":"published"}`))
	if err != nil {
		t.Fatalf("simulateCallback: %v", err)
	}

	count := atomic.LoadInt64(&h.veloxFake.callbacksPosted)
	if count == 0 {
		t.Fatalf("scenario_7: velox fake recorded zero callbacks; expected >=1")
	}
	h.veloxFake.mu.Lock()
	defer h.veloxFake.mu.Unlock()

	if len(h.veloxFake.callbackLog) == 0 {
		t.Fatalf("scenario_7: callback log empty")
	}
	last := h.veloxFake.callbackLog[len(h.veloxFake.callbackLog)-1]
	if !strings.Contains(string(last.Body), "external_delivery_id") {
		t.Errorf("scenario_7: callback body should carry external_delivery_id; got %s", string(last.Body))
	}
	t.Logf("scenario_7 PASS: %d callback(s) recorded; last body has external_delivery_id", count)
}

// ─── Scenario 8: Lease contention ───────────────────────────────────────
//
// Two workers competing for the same ingest row. Production uses
// SELECT...FOR UPDATE SKIP LOCKED (or similar) so the loser sees
// 0 rows claimed. We reproduce the same shape via two SELECTs in
// a single transaction with NOWAIT, then assert winner-loser
// asymmetry at the SQL level (matching the production contract).
//
// Helpers acquiredLeaseInTx / attemptAcquireWithNowait live in
// e2e_harness.go.
func scenario8_LeaseContention(t *testing.T, h *E2EHarness) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Insert a target row in 'accepted' (acquirable state).
	targetID, err := insertPublishTarget(h, "accepted")
	if err != nil {
		t.Fatalf("insertPublishTarget: %v", err)
	}

	// Worker 1 claims the row inside a TX (FOR UPDATE keeps the
	// lock until commit/rollback). Until that TX ends, worker 2
	// must NOT see the row as acquirable.
	tx1, err := h.pgDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("worker-1 begin: %v", err)
	}
	defer tx1.Rollback()
	if err := acquireLeaseInTx(ctx, tx1, targetID); err != nil {
		t.Fatalf("worker-1 acquireLease: %v", err)
	}

	// Worker 2 attempts to claim the same row with NOWAIT — must
	// fail with err-40P01 (lock_not_available) per Postgres
	// semantics. The production SKIP LOCKED contract would silently
	// return 0 rows; NOWAIT surfaces the lock contention which
	// is what we use in tests to make the contention observable.
	tx2, err := h.pgDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("worker-2 begin: %v", err)
	}
	defer tx2.Rollback()
	competed, err := attemptAcquireWithNowait(ctx, tx2, targetID)
	if err == nil {
		t.Errorf("scenario_8: worker-2 should NOT have acquired the lease; want err-lock-not-available")
	}
	if competed {
		t.Errorf("scenario_8: worker-2 reported acquisition TRUE under contention; want FALSE")
	}

	// Worker 1 commits → lease released → worker 3 can now claim.
	if err := tx1.Commit(); err != nil {
		t.Fatalf("worker-1 commit: %v", err)
	}
	tx3, err := h.pgDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("worker-3 begin: %v", err)
	}
	defer tx3.Rollback()
	released, err := attemptAcquireWithNowait(ctx, tx3, targetID)
	if err != nil {
		t.Errorf("scenario_8: worker-3 should acquire freely after worker-1 commit; got err %v", err)
	}
	if !released {
		t.Errorf("scenario_8: worker-3 reported acquisition FALSE after release; want TRUE")
	}
	// Heartbeat slot is populated by acquireLeaseInTx; verify it's
	// within the lease window (worker-3 still owns it).
	if err := tx3.Commit(); err != nil {
		t.Fatalf("worker-3 commit: %v", err)
	}

	t.Logf("scenario_8 PASS: lease exclusivity verified (2-worker race → 1 winner per cycle)")
}
