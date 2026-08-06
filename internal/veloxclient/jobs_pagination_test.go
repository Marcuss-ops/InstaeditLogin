package veloxclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	veloxapi "github.com/Marcuss-ops/InstaeditLogin/internal/veloxcontract"
)

func TestListJobsPage_ForwardsCursorAndMapsMetadata(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("cursor"); got != "cursor-1" {
			t.Fatalf("cursor = %q", got)
		}
		if got := r.URL.Query().Get("limit"); got != "2" {
			t.Fatalf("limit = %q", got)
		}
		_ = json.NewEncoder(w).Encode(listJobsResponse{
			Jobs:       []jobResponse{{ID: "job-2", WorkspaceID: 42, RenderStatus: "RUNNING"}},
			NextCursor: "cursor-2",
			HasMore:    true,
		})
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	page, err := client.ListJobsPage(context.Background(), 42, 99, veloxapi.ListJobsFilter{Limit: 2, Cursor: "cursor-1"})
	if err != nil {
		t.Fatalf("ListJobsPage: %v", err)
	}
	if len(page.Jobs) != 1 || page.NextCursor != "cursor-2" || !page.HasMore {
		t.Fatalf("page = %+v", page)
	}
}
