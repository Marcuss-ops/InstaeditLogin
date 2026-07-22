package veloxclient

import (
	"context"
	"fmt"
	"net/url"

	veloxapi "github.com/Marcuss-ops/InstaeditLogin/pkg/api/velox"
)

// GetAsset implements veloxapi.Client.GetAsset.
func (c *Client) GetAsset(ctx context.Context, workspaceID int64, assetID string) (*veloxapi.Asset, error) {
	var resp assetResponse
	path := fmt.Sprintf("/api/v1/instaedit/assets/%s", url.PathEscape(assetID))
	if err := c.do(ctx, "GET", path, workspaceID, workspaceID, nil, &resp); err != nil {
		return nil, err
	}
	return &veloxapi.Asset{
		ID:          resp.ID,
		WorkspaceID: resp.WorkspaceID,
		SHA256:      resp.SHA256,
		SizeBytes:   resp.SizeBytes,
		MimeType:    resp.MimeType,
		DownloadURL: resp.DownloadURL,
	}, nil
}
