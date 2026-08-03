package velox

import (
	"context"
	"net/http"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/Marcuss-ops/InstaeditLogin/pkg/metrics"
)

func TestCreateJob_UsesSubmissionAdapterAndRecordsUsage(t *testing.T) {
	mc := &mockClient{createJobFn: func(_ context.Context, wsID, uid int64, req CreateJobRequest) (*Job, error) {
		if wsID != testWSID || uid != testUID {
			t.Fatalf("identity not forwarded: ws=%d uid=%d", wsID, uid)
		}
		if req.ProjectID != "project_123" || len(req.RenderSpec) == 0 {
			t.Fatalf("legacy payload not forwarded: %+v", req)
		}
		return &Job{ID: "legacy-adapter-job", WorkspaceID: wsID}, nil
	}}
	mux := newMux(t, mc, stubAuth)
	before := testutil.ToFloat64(metrics.LegacyJobEndpointUsageCounter(metrics.LegacyJobEndpointVeloxJobs, metrics.LegacyJobOutcomeAccepted))
	body := `{"contract_version":"velox.job.v1","idempotency_key":"legacy-adapter-1","project_id":"project_123","render_spec":{"template":"news"},"delivery_plan":{"destinations":[{"external_destination_id":"dest-1"}]}}`
	w := do(t, mux, http.MethodPost, "/api/v1/velox/jobs", body)
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}
	after := testutil.ToFloat64(metrics.LegacyJobEndpointUsageCounter(metrics.LegacyJobEndpointVeloxJobs, metrics.LegacyJobOutcomeAccepted))
	if after != before+1 {
		t.Fatalf("legacy accepted metric = %v, want %v", after, before+1)
	}
}
