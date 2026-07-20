package services

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/Marcuss-ops/InstaeditLogin/pkg/metrics"
)

// TestQueryUploadStatus_RecoveryMetricFiresOnResumeProbe
// (Task 10.10.x polish #1, Scenario 2 — relocated here from
// internal/worker/task_10_10_recovery_test.go because the cleanest
// production wire-up for the recovery metric lives inside
// queryUploadStatus's 308 success branch, and queryUploadStatus is
// package-private to internal/services).
//
// Drives the production queryUploadStatus method end-to-end via
// httptest and asserts that a successful 308 response (i.e. the
// "we have a partial upload, where should we resume from?" probe)
// increments metrics.RecordResumableRecovery once.
//
// Why a successful 308 IS a recovery event: the 308 reply only
// happens when YouTube's resumable-upload endpoint has a record of
// a previous chunk PUT for the same URI (otherwise the server has
// nothing to resume from and would reply 200/404 instead). Every
// probe that successfully parses a Range header is therefore by
// definition a "we resumed from a partial state" event — exactly
// what Task 10.10.x polish #1 wants to count.
//
// Failure modes (regression caught by this test):
//   - WIRE-UP REMOVED: the metrics.RecordResumableRecovery line
//     inside queryUploadStatus's 308 success branch is deleted →
//     counter stays flat against the asserted delta of 1 → t.Fatalf.
//   - Reason regression: the wired reason is something other than
//     ChunkLost → t.Errorf compares the wrong series
//     (counter-WithLabelValues("wrong_reason") stays flat).
//   - Wire-up placed on a different status branch (e.g. 404 instead
//     of 308): the test fires 308, the wire-up doesn't trigger →
//     counter stays flat → t.Fatalf.
//   - queryUploadStatus returns the wrong offset (Range header
//     misparsed): the offset == 500 assertion catches the
//     regression independently of the metric path.
//
// The pre-polish equivalent of this test
// (TestYouTubeResumableRecovery_FailsIfClearNotCalled) used
// sqlmock + manual metrics.RecordResumableRecovery in the test
// body, which masked any deletion of the production wire-up. The
// polish #1 replacement removes the manual metric call and routes
// the assertion through the production method so a real regression
// is caught.
func TestQueryUploadStatus_RecoveryMetricFiresOnResumeProbe(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("status-probe method: got %s, want PUT", r.Method)
		}
		if got := r.Header.Get("Content-Range"); !strings.HasPrefix(got, "bytes */") {
			t.Errorf("status-probe Content-Range: got %q, want bytes */...", got)
		}
		w.Header().Set("Range", "bytes=0-499")
		w.WriteHeader(http.StatusPermanentRedirect) // 308
	}))
	defer srv.Close()

	// Build a minimal production-shaped YouTubeOAuthService. We
	// don't need a real sessionStore / encryptor wiring because
	// queryUploadStatus is the HTTP probe, not the chunk-PUT loop;
	// memSessionStore{} + fakeEncryptor{} are inert enough that
	// the nil-attachment invariant doesn't matter.
	svc := newResumeReadyService(t, &memSessionStore{}, fakeEncryptor{})

	// Capture ResumableRecoveryCount{chunk_lost} BEFORE the call.
	// The `WithLabelValues` form is the production-shape label
	// combination that the queryUploadStatus 308 branch wires;
	// passing the wrong series here would surface as a flat line
	// against the asserted delta of 1.
	before := testutil.ToFloat64(metrics.ResumableRecoveryCount.WithLabelValues(metrics.ResumableRecoveryReasonChunkLost))

	offset, err := svc.queryUploadStatus(context.Background(), srv.URL, 500)

	after := testutil.ToFloat64(metrics.ResumableRecoveryCount.WithLabelValues(metrics.ResumableRecoveryReasonChunkLost))

	if err != nil {
		t.Fatalf("queryUploadStatus on 308: want nil err, got %v (production Range-header parse regressed)", err)
	}
	if offset != 500 {
		t.Errorf("queryUploadStatus on 308: parsed offset got %d, want 500 (Range 0-499 → resume at byte 500)", offset)
	}
	if delta := after - before; delta != 1 {
		t.Fatalf("resumable_recovery_total{chunk_lost} delta = %v; want 1 (queryUploadStatus 308 wire-up was removed)", delta)
	}
}
