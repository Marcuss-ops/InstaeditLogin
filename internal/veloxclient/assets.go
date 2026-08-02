package veloxclient

import (
	"context"
	"fmt"
	"net/url"

	veloxapi "github.com/Marcuss-ops/InstaeditLogin/internal/veloxcontract"
)

// GetAsset implements veloxapi.Client.GetAsset.
//
// Permission: editor.project.read (per the scope architect verdict;
// an asset exists only by virtue of having been uploaded, but the GET
// itself is a read operation against a project resource).
func (c *Client) GetAsset(ctx context.Context, workspaceID, userID int64, assetID string) (*veloxapi.Asset, error) {
	var resp assetResponse
	path := fmt.Sprintf("/api/v1/instaedit/assets/%s", url.PathEscape(assetID))
	if err := c.do(ctx, "GET", path, userID, workspaceID, []string{ScopeVeloxAssetsRead}, nil, &resp); err != nil {
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
