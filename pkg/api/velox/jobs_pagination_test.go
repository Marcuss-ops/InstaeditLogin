package velox

import (
	"context"
	"net/http"
	"testing"

	"github.com/Marcuss-ops/InstaeditLogin/internal/veloxcontract"
)

type pagedMockClient struct {
	*mockClient
	pageFn func(context.Context, int64, int64, ListJobsFilter) (JobsPage, error)
}

func (m *pagedMockClient) ListJobsPage(ctx context.Context, workspaceID, userID int64, filter ListJobsFilter) (JobsPage, error) {
	return m.pageFn(ctx, workspaceID, userID, filter)
}

func TestListJobs_CursorUsesOptionalPagerAndReturnsEnvelope(t *testing.T) {
	mc := &pagedMockClient{
		mockClient: &mockClient{},
		pageFn: func(_ context.Context, workspaceID, userID int64, filter ListJobsFilter) (JobsPage, error) {
			if workspaceID != testWSID || userID != testUID {
				t.Fatalf("identity = (%d, %d)", workspaceID, userID)
			}
			if filter.Cursor != "cursor-1" || filter.Limit != 2 || filter.Status != "RUNNING" {
				t.Fatalf("filter = %+v", filter)
			}
			return JobsPage{
				Jobs:       []Job{{ID: "job-2", WorkspaceID: testWSID, RenderStatus: "RUNNING"}},
				NextCursor: "cursor-2",
				HasMore:    true,
			}, nil
		},
	}
	mux := newMux(t, mc, stubAuth)
	w := do(t, mux, http.MethodGet, "/api/v1/velox/jobs?status=RUNNING&limit=2&cursor=cursor-1", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	body := decodeBody(t, w)
	if body["next_cursor"] != "cursor-2" || body["has_more"] != true {
		t.Fatalf("pagination metadata = %v", body)
	}
}

var _ veloxcontract.Client = (*pagedMockClient)(nil)
var _ veloxcontract.JobsPager = (*pagedMockClient)(nil)
