package veloxclient

import (
	"context"
	"fmt"
	"net/url"

	veloxapi "github.com/Marcuss-ops/InstaeditLogin/pkg/api/velox"
)

// ListWorkers implements veloxapi.Client.ListWorkers. The workspace
// scope is signed into the JWT; Velox scopes the query.
func (c *Client) ListWorkers(ctx context.Context, workspaceID int64) ([]veloxapi.Worker, error) {
	var resp listWorkersResponse
	if err := c.do(ctx, "GET", "/api/v1/workers", workspaceID, workspaceID, nil, &resp); err != nil {
		return nil, err
	}
	workers := make([]veloxapi.Worker, 0, len(resp.Workers))
	for _, w := range resp.Workers {
		workers = append(workers, veloxapi.Worker{
			ID:          w.ID,
			WorkspaceID: w.WorkspaceID,
			Status:      w.Status,
			CPU:         w.CPU,
			RAMMB:       w.RAMMB,
			GPU:         w.GPU,
			DiskGB:      w.DiskGB,
		})
	}
	return workers, nil
}

// GetWorker implements veloxapi.Client.GetWorker.
func (c *Client) GetWorker(ctx context.Context, workspaceID int64, workerID string) (*veloxapi.Worker, error) {
	var resp workerResponse
	path := fmt.Sprintf("/api/v1/workers/%s", url.PathEscape(workerID))
	if err := c.do(ctx, "GET", path, workspaceID, workspaceID, nil, &resp); err != nil {
		return nil, err
	}
	return &veloxapi.Worker{
		ID:          resp.ID,
		WorkspaceID: resp.WorkspaceID,
		Status:      resp.Status,
		CPU:         resp.CPU,
		RAMMB:       resp.RAMMB,
		GPU:         resp.GPU,
		DiskGB:      resp.DiskGB,
	}, nil
}
